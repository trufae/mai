package orchestrator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"mai/src/swan/config"
	"mai/src/swan/daemon"
	"mai/src/swan/learning"
)

// OrchestratorServer manages task routing to agents
type OrchestratorServer struct {
	config     *config.SwanConfig
	daemonMgr  *daemon.DaemonManager
	learning   *learning.LearningEngine
	server     *http.Server
	agentIndex int
	mu         sync.Mutex
}

// TaskResult represents the result of a task execution
type TaskResult struct {
	AgentName string        `json:"agent_name"`
	Response  string        `json:"response"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
}

// NewOrchestratorServer creates a new orchestrator server
func NewOrchestratorServer(cfg *config.SwanConfig, daemonMgr *daemon.DaemonManager) *OrchestratorServer {
	fmt.Printf("DEBUG: Initializing learning engine...\n")
	learningEngine, err := learning.NewLearningEngine(cfg)
	if err != nil {
		// Log error but continue - learning features will be disabled
		fmt.Printf("Warning: failed to initialize learning engine: %v\n", err)
		learningEngine = nil
	} else {
		fmt.Printf("DEBUG: Learning engine initialized\n")
	}

	return &OrchestratorServer{
		config:    cfg,
		daemonMgr: daemonMgr,
		learning:  learningEngine,
	}
}

// Start starts the orchestrator server
func (s *OrchestratorServer) Start() error {
	fmt.Printf("DEBUG: Creating HTTP mux...\n")
	mux := http.NewServeMux()

	// Root endpoint for status
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("SWAN orchestrator is running"))
	})

	// OpenAI-compatible endpoints
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleModels)

	// Ollama-compatible endpoints
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/generate", s.handleGenerate)
	mux.HandleFunc("/v1/generate", s.handleGenerate) // Alias for compatibility
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/version", s.handleVersion)

	// SWAN-specific endpoints
	mux.HandleFunc("/api/agents", s.handleListAgents)
	mux.HandleFunc("/api/task", s.handleTask)
	mux.HandleFunc("/api/communicate", s.handleInterAgentCommunication)
	mux.HandleFunc("/api/network-knowledge", s.handleGetNetworkKnowledge)
	mux.HandleFunc("/health", s.handleHealth)

	listenAddr := fmt.Sprintf("%s:%d", s.config.Orchestrator.ListenAddr, s.config.Orchestrator.Port)
	fmt.Printf("DEBUG: Creating HTTP server on %s...\n", listenAddr)
	s.server = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	fmt.Printf("SWAN Orchestrator starting...\n")
	fmt.Printf("Main orchestrator listening on: %s\n", listenAddr)
	fmt.Printf("Port: %d\n", s.config.Orchestrator.Port)
	fmt.Printf("To connect with MAI: mai -M -b http://%s\n", listenAddr)
	fmt.Printf("OpenAI-compatible API: http://%s/v1/chat/completions\n", listenAddr)
	fmt.Printf("Ollama-compatible API: http://%s/api/generate\n", listenAddr)
	fmt.Printf("Models list: http://%s/api/tags\n", listenAddr)
	fmt.Printf("Health check: http://%s/health\n", listenAddr)
	fmt.Printf("Agent list: http://%s/api/agents\n", listenAddr)
	fmt.Printf("DEBUG: Calling ListenAndServe...\n")
	return s.server.ListenAndServe()
}

// Stop stops the orchestrator server
func (s *OrchestratorServer) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// handleChatCompletions handles OpenAI chat completions
func (s *OrchestratorServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendOpenAIError(w, "method_not_allowed", "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Model     string `json:"model,omitempty"`
		Stream    bool   `json:"stream,omitempty"`
		MaxTokens int    `json:"max_tokens,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendOpenAIError(w, "invalid_request", fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Extract query from last user message
	query := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			query = req.Messages[i].Content
			break
		}
	}

	// Handle admin queries directly
	if response := s.handleAdminQuery(query); response != "" {
		s.recordTask(map[string]interface{}{"messages": req.Messages}, &TaskResult{
			AgentName: "swan-admin",
			Response:  response,
			Duration:  0,
		})
		s.sendOpenAIResponse(w, "swan-admin", response, req.Stream)
		return
	}

	// Select agent (may involve competition)
	agent := s.selectAgentWithCompetition(query)
	if agent == nil {
		s.sendOpenAIError(w, "service_unavailable", "No agents available", http.StatusServiceUnavailable)
		return
	}

	// Build task payload
	taskPayload := map[string]interface{}{
		"messages": req.Messages,
		"model":    req.Model,
		"stream":   req.Stream,
	}

	// Execute task
	result, err := s.executeTask(agent, taskPayload)
	if err != nil {
		http.Error(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Return OpenAI-compatible response
	response := map[string]interface{}{
		"id":      fmt.Sprintf("swan-%d", time.Now().Unix()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   agent.Name,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": result.Response,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     0, // TODO: calculate
			"completion_tokens": 0, // TODO: calculate
			"total_tokens":      0, // TODO: calculate
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleModels returns available models (agents)
func (s *OrchestratorServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOpenAIError(w, "method_not_allowed", "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents := s.daemonMgr.ListAgents()
	var models []map[string]interface{}

	for _, agent := range agents {
		models = append(models, map[string]interface{}{
			"id":       agent.Name,
			"object":   "model",
			"created":  agent.StartTime.Unix(),
			"owned_by": agent.Config.Provider,
		})
	}

	response := map[string]interface{}{
		"object": "list",
		"data":   models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleListAgents returns list of running agents
func (s *OrchestratorServer) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents := s.daemonMgr.ListAgents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

// handleTask handles direct task execution
func (s *OrchestratorServer) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AgentName string                 `json:"agent_name,omitempty"`
		Payload   map[string]interface{} `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	var agent *daemon.AgentProcess
	if req.AgentName != "" {
		var exists bool
		agent, exists = s.daemonMgr.GetAgent(req.AgentName)
		if !exists {
			s.sendOllamaError(w, "Agent not found", http.StatusNotFound)
			return
		}
	} else {
		// Extract query from payload
		query := ""
		if messages, ok := req.Payload["messages"].([]interface{}); ok && len(messages) > 0 {
			for i := len(messages) - 1; i >= 0; i-- {
				if msg, ok := messages[i].(map[string]interface{}); ok {
					if role, ok := msg["role"].(string); ok && role == "user" {
						if content, ok := msg["content"].(string); ok {
							query = content
							break
						}
					}
				}
			}
		} else if q, ok := req.Payload["query"].(string); ok {
			query = q
		}

		agent = s.selectAgent(query)
		if agent == nil {
			// Try to create a new dynamic agent
			if s.learning != nil {
				if suggestion, err := s.learning.SuggestNewAgent(); err == nil {
					if err := s.daemonMgr.StartResolvedAgent(*suggestion); err == nil {
						// Get the newly created agent
						agent, _ = s.daemonMgr.GetAgent(suggestion.Name)
					}
				}
			}
			if agent == nil {
				s.sendOllamaError(w, "No agents available", http.StatusServiceUnavailable)
				return
			}
		}
	}

	result, err := s.executeTask(agent, req.Payload)
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleHealth returns health status
func (s *OrchestratorServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	agents := s.daemonMgr.ListAgents()
	status := "healthy"
	if len(agents) == 0 {
		status = "degraded"
	}

	response := map[string]interface{}{
		"status":    status,
		"agents":    len(agents),
		"timestamp": time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// selectAgent selects an agent for task execution using learning engine
func (s *OrchestratorServer) selectAgent(query string) *daemon.AgentProcess {
	s.mu.Lock()
	defer s.mu.Unlock()

	agents := s.daemonMgr.ListAgents()
	if len(agents) == 0 {
		return nil
	}

	// Try to get best agent from learning engine based on time/quality metrics
	if s.learning != nil {
		if bestAgent, err := s.learning.GetBestAgent(query); err == nil {
			if agent, exists := agents[bestAgent]; exists {
				return agent
			}
		}
	}

	// Fallback to round-robin if no learning data available
	agentNames := make([]string, 0, len(agents))
	for name := range agents {
		agentNames = append(agentNames, name)
	}

	selected := agentNames[s.agentIndex%len(agentNames)]
	s.agentIndex++

	return agents[selected]
}

// executeTask executes a task on the specified agent
func (s *OrchestratorServer) executeTask(agent *daemon.AgentProcess, payload map[string]interface{}) (*TaskResult, error) {
	start := time.Now()

	// Build request to agent's provider endpoint
	baseURL := agent.Config.BaseURL
	if baseURL == "" {
		if agent.Config.Provider == "openai" {
			baseURL = "https://api.openai.com/v1"
		} else {
			baseURL = "http://localhost:11434/v1" // Default for Ollama
		}
	}
	agentURL := baseURL + "/chat/completions"

	// Set up headers
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Add API key if needed
	if agent.Config.Provider == "openai" {
		if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
	}

	// Override model with agent's configured model
	payload["model"] = agent.Config.Model

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %v", err)
	}

	// Create request
	req, err := http.NewRequest("POST", agentURL, bytes.NewReader(payloadBytes))
	if err != nil {
		result := &TaskResult{
			AgentName: agent.Name,
			Error:     err.Error(),
			Duration:  time.Since(start),
		}
		s.recordTask(payload, result)
		return result, nil
	}

	// Add headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		result := &TaskResult{
			AgentName: agent.Name,
			Error:     err.Error(),
			Duration:  time.Since(start),
		}
		s.recordTask(payload, result)
		return result, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result := &TaskResult{
			AgentName: agent.Name,
			Error:     fmt.Sprintf("failed to read response: %v", err),
			Duration:  time.Since(start),
		}
		s.recordTask(payload, result)
		return result, nil
	}

	if resp.StatusCode != http.StatusOK {
		result := &TaskResult{
			AgentName: agent.Name,
			Error:     fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
			Duration:  time.Since(start),
		}
		s.recordTask(payload, result)
		return result, nil
	}

	// Parse OpenAI response
	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &openaiResp); err != nil {
		result := &TaskResult{
			AgentName: agent.Name,
			Response:  string(body),
			Duration:  time.Since(start),
		}
		s.recordTask(payload, result)
		return result, nil
	}

	response := ""
	if len(openaiResp.Choices) > 0 {
		response = openaiResp.Choices[0].Message.Content
	}

	result := &TaskResult{
		AgentName: agent.Name,
		Response:  response,
		Duration:  time.Since(start),
	}
	s.recordTask(payload, result)
	return result, nil
}

// selectAgentWithCompetition selects an agent, potentially running a competition
func (s *OrchestratorServer) selectAgentWithCompetition(query string) *daemon.AgentProcess {
	// Check if we should run an evaluation competition
	if s.shouldRunCompetition(query) {
		result, err := s.runCompetition(query)
		if err == nil && result.Winner != "" {
			// Use the winning agent
			if winnerAgent, exists := s.daemonMgr.GetAgent(result.Winner); exists {
				return winnerAgent
			}
		}
	}

	// Fall back to regular agent selection
	return s.selectAgent(query)
}

// shouldRunCompetition determines if a competition should be run for this query
func (s *OrchestratorServer) shouldRunCompetition(query string) bool {
	// Run competitions for:
	// 1. Important/complex queries (longer than 50 characters)
	// 2. Queries we haven't seen much data for
	// 3. Periodically to keep agents competitive

	if len(query) < 50 {
		return false // Too simple for competition
	}

	// Check if we have enough agents for a competition
	agents := s.daemonMgr.ListAgents()
	if len(agents) < 2 {
		return false // Need at least 2 agents
	}

	// Run competition 10% of the time for important queries
	return time.Now().UnixNano()%10 == 0
}

// runCompetition executes a competition between agents
func (s *OrchestratorServer) runCompetition(query string) (*learning.CompetitionResult, error) {
	agents := s.daemonMgr.ListAgents()
	if len(agents) < 2 {
		return nil, fmt.Errorf("not enough agents for competition")
	}

	// Select 2-3 agents for the competition
	var competitorNames []string
	count := 0
	maxCompetitors := 3
	if len(agents) < maxCompetitors {
		maxCompetitors = len(agents)
	}

	for name := range agents {
		competitorNames = append(competitorNames, name)
		count++
		if count >= maxCompetitors {
			break
		}
	}

	// Build task payload for competition
	taskPayload := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "user", "content": query},
		},
		"competition": true, // Mark as competition task
	}

	// Run tasks on each competitor and collect results
	results := make(map[string]*TaskResult)
	for _, agentName := range competitorNames {
		if agent, exists := s.daemonMgr.GetAgent(agentName); exists {
			result, err := s.executeTask(agent, taskPayload)
			if err == nil {
				results[agentName] = result
			}
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no competition results")
	}

	// Convert to CompetitionResult format
	competitionResult := &learning.CompetitionResult{
		Query:     query,
		Timestamp: time.Now(),
		Results:   make(map[string]*learning.TaskRecord),
	}

	var bestQuality float64
	var winner string

	for agentName, result := range results {
		// Assess quality for competition
		quality := 0.5 // Default quality if learning engine is disabled
		if s.learning != nil {
			quality = s.learning.AssessQuality(&learning.TaskRecord{
				Query:     query,
				Response:  result.Response,
				Duration:  result.Duration,
				Success:   result.Error == "",
				Error:     result.Error,
				Timestamp: time.Now(),
			})
		}

		taskRecord := &learning.TaskRecord{
			TaskID:    fmt.Sprintf("competition-%d", time.Now().UnixNano()),
			AgentName: agentName,
			Query:     query,
			Response:  result.Response,
			Duration:  result.Duration,
			Success:   result.Error == "",
			Error:     result.Error,
			Quality:   quality,
			Timestamp: time.Now(),
		}

		competitionResult.Results[agentName] = taskRecord

		if quality > bestQuality {
			bestQuality = quality
			winner = agentName
		}
	}

	competitionResult.Winner = winner
	competitionResult.Reasoning = fmt.Sprintf("Agent %s won with quality score %.2f", winner, bestQuality)

	return competitionResult, nil
}

// handleInterAgentCommunication handles communication between agents
func (s *OrchestratorServer) handleInterAgentCommunication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		FromAgent string `json:"from_agent"`
		ToAgent   string `json:"to_agent"`
		Message   string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.FromAgent == "" || req.ToAgent == "" || req.Message == "" {
		s.sendOllamaError(w, "Missing required fields: from_agent, to_agent, message", http.StatusBadRequest)
		return
	}

	// Record the communication
	if s.learning != nil {
		err := s.learning.RecordInterAgentCommunication(req.FromAgent, req.ToAgent, req.Message)
		if err != nil {
			s.sendOllamaError(w, fmt.Sprintf("Failed to record communication: %v", err), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}

// handleGetNetworkKnowledge returns network knowledge for an agent
func (s *OrchestratorServer) handleGetNetworkKnowledge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentName := r.URL.Query().Get("agent")
	if agentName == "" {
		s.sendOllamaError(w, "Missing agent parameter", http.StatusBadRequest)
		return
	}

	var knowledge []string
	var err error
	if s.learning != nil {
		knowledge, err = s.learning.GetNetworkKnowledge(agentName)
		if err != nil {
			s.sendOllamaError(w, fmt.Sprintf("Failed to get network knowledge: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		knowledge = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agent":     agentName,
		"knowledge": knowledge,
	})
}

// TriggerEvolution triggers autonomous evolution of prompts and configurations
func (s *OrchestratorServer) TriggerEvolution() error {
	if s.learning != nil {
		return s.learning.EvolvePrompts()
	}
	return nil
}

// handleAdminQuery handles special administrative queries
func (s *OrchestratorServer) handleAdminQuery(query string) string {
	switch query {
	case "/health", "show health", "health":
		agents := s.daemonMgr.ListAgents()
		status := "healthy"
		if len(agents) == 0 {
			status = "degraded"
		}
		return fmt.Sprintf("SWAN Status: %s\nAgents running: %d\nTimestamp: %d", status, len(agents), time.Now().Unix())
	case "/agents", "list agents", "agents":
		agents := s.daemonMgr.ListAgents()
		if len(agents) == 0 {
			return "No agents currently running"
		}
		response := "Running agents:\n"
		for name, agent := range agents {
			response += fmt.Sprintf("- %s (PID: %d, Port: %d, Provider: %s, Model: %s)\n",
				name, agent.PID, agent.Port, agent.Config.Provider, agent.Config.Model)
		}
		return response
	case "/help", "help":
		return "Available commands:\n/health - Show system health\n/agents - List running agents\n/help - Show this help"
	default:
		return ""
	}
}

// sendOpenAIResponse sends a response in OpenAI format
func (s *OrchestratorServer) sendOpenAIResponse(w http.ResponseWriter, model string, content string, stream bool) {
	if stream {
		// Send streaming response
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Send the full content as a single streaming chunk
		streamResponse := map[string]interface{}{
			"id":      fmt.Sprintf("swan-%d", time.Now().Unix()),
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"content": content,
					},
					"finish_reason": nil,
				},
			},
		}

		data, _ := json.Marshal(streamResponse)
		fmt.Fprintf(w, "data: %s\n\n", data)

		// Send completion message
		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		// Send regular response
		response := map[string]interface{}{
			"id":      fmt.Sprintf("swan-%d", time.Now().Unix()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": content,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     0,
				"completion_tokens": 0,
				"total_tokens":      0,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// handleChat handles Ollama /api/chat
func (s *OrchestratorServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Extract query from last user message
	query := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			query = req.Messages[i].Content
			break
		}
	}

	// Handle admin queries
	if response := s.handleAdminQuery(query); response != "" {
		s.recordTask(map[string]interface{}{"messages": req.Messages}, &TaskResult{
			AgentName: "swan-admin",
			Response:  response,
			Duration:  0,
		})
		s.sendOllamaChatResponse(w, "swan-admin", response, req.Stream)
		return
	}

	// Select agent
	agent := s.selectAgent(query)
	if agent == nil {
		s.sendOllamaError(w, "No agents available", http.StatusServiceUnavailable)
		return
	}

	// Convert messages
	messages := make([]map[string]interface{}, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = map[string]interface{}{"role": m.Role, "content": m.Content}
	}

	if req.Stream {
		s.handleStreamingChat(w, agent, messages)
	} else {
		// Build task payload
		taskPayload := map[string]interface{}{
			"messages": messages,
			"model":    req.Model,
			"stream":   req.Stream,
		}

		// Execute task
		result, err := s.executeTask(agent, taskPayload)
		if err != nil {
			s.sendOllamaError(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
			return
		}

		// Return Ollama chat response
		s.sendOllamaChatResponse(w, agent.Name, result.Response, req.Stream)
	}
}

// handleStreamingChat handles streaming for /api/chat
func (s *OrchestratorServer) handleStreamingChat(w http.ResponseWriter, agent *daemon.AgentProcess, messages []map[string]interface{}) {
	var agentURL string
	var payload map[string]interface{}
	var headers map[string]string

	if agent.Config.Provider == "openai" {
		baseURL := agent.Config.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		agentURL = baseURL + "/chat/completions"
		payload = map[string]interface{}{
			"messages": messages,
			"model":    agent.Config.Model,
			"stream":   true,
		}
		headers = map[string]string{"Content-Type": "application/json"}
		if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
	} else {
		baseURL := "http://localhost:11434"
		agentURL = baseURL + "/api/chat"
		payload = map[string]interface{}{
			"model":    agent.Config.Model,
			"messages": messages,
			"stream":   true,
		}
		headers = map[string]string{"Content-Type": "application/json"}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Failed to marshal payload: %v", err), http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest("POST", agentURL, bytes.NewReader(payloadBytes))
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
		return
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Request failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.sendOllamaError(w, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)), http.StatusInternalServerError)
		return
	}

	if agent.Config.Provider == "openai" {
		w.Header().Set("Content-Type", "text/plain")
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				dataStr := line[6:]
				if dataStr == "[DONE]" {
					w.Write([]byte("data: [DONE]\n\n"))
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					continue
				}
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
					continue
				}
				data["model"] = "mai-swan"
				jsonBytes, _ := json.Marshal(data)
				w.Write([]byte("data: "))
				w.Write(jsonBytes)
				w.Write([]byte("\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			line = strings.Replace(line, `"model":"`+agent.Config.Model+`"`, `"model":"mai-swan"`, 1)
			w.Write([]byte(line))
			w.Write([]byte("\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// handleGenerate handles Ollama /api/generate
func (s *OrchestratorServer) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		Stream bool   `json:"stream"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Handle admin queries
	if response := s.handleAdminQuery(req.Prompt); response != "" {
		s.recordTask(map[string]interface{}{"prompt": req.Prompt}, &TaskResult{
			AgentName: "swan-admin",
			Response:  response,
			Duration:  0,
		})
		s.sendOllamaResponse(w, "swan-admin", response, req.Stream)
		return
	}

	// Select agent
	agent := s.selectAgent(req.Prompt)
	if agent == nil {
		s.sendOllamaError(w, "No agents available", http.StatusServiceUnavailable)
		return
	}

	if req.Stream {
		// Handle streaming
		s.handleStreamingGenerate(w, agent, req.Prompt)
	} else {
		// Build task payload
		taskPayload := map[string]interface{}{
			"messages": []map[string]interface{}{
				{"role": "user", "content": req.Prompt},
			},
			"model":  req.Model,
			"stream": req.Stream,
		}

		// Execute task
		result, err := s.executeTask(agent, taskPayload)
		if err != nil {
			s.sendOllamaError(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
			return
		}

		// Return Ollama response
		s.sendOllamaResponse(w, "mai-swan", result.Response, req.Stream)
	}
}

// handleStreamingGenerate handles streaming for /api/generate
func (s *OrchestratorServer) handleStreamingGenerate(w http.ResponseWriter, agent *daemon.AgentProcess, prompt string) {
	// Build request to agent's provider endpoint
	baseURL := agent.Config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434" // Default for Ollama
	}
	// For Ollama, use /api/generate
	if strings.Contains(baseURL, "11434") {
		baseURL = "http://localhost:11434"
	}
	agentURL := baseURL + "/api/generate"

	// Build payload for Ollama /api/generate
	taskPayload := map[string]interface{}{
		"model":  agent.Config.Model,
		"prompt": prompt,
		"stream": true,
	}

	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Failed to marshal payload: %v", err), http.StatusInternalServerError)
		return
	}

	// Create request
	req, err := http.NewRequest("POST", agentURL, bytes.NewReader(payloadBytes))
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Request failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.sendOllamaError(w, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)), http.StatusInternalServerError)
		return
	}

	// Stream response
	w.Header().Set("Content-Type", "application/json")
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// Replace the model in the JSON string
		line = strings.Replace(line, `"model":"`+agent.Config.Model+`"`, `"model":"mai-swan"`, 1)
		w.Write([]byte(line))
		w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// handleTags handles Ollama /api/tags
func (s *OrchestratorServer) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Return a single fake model named "mai-swan"
	models := []map[string]interface{}{
		{
			"name":   "mai-swan",
			"size":   0,
			"digest": "",
			"details": map[string]interface{}{
				"format":             "gguf",
				"family":             "swan",
				"families":           []string{"swan"},
				"parameter_size":     "unknown",
				"quantization_level": "unknown",
			},
			"model":       "mai-swan",
			"modified_at": time.Now().Format(time.RFC3339),
		},
	}

	response := map[string]interface{}{
		"models": models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleVersion handles Ollama /api/version
func (s *OrchestratorServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"version": "0.1.0",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// sendOllamaChatResponse sends a response in Ollama /api/chat format
func (s *OrchestratorServer) sendOllamaChatResponse(w http.ResponseWriter, model string, content string, stream bool) {
	if stream {
		// Send streaming response - simulate with single chunk
		w.Header().Set("Content-Type", "application/json")

		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)

		// Send the content as a single streaming message
		streamResponse := map[string]interface{}{
			"model": model,
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": content,
			},
			"done":              false,
			"total_duration":    0,
			"load_duration":     0,
			"prompt_eval_count": 0,
			"eval_count":        0,
			"eval_duration":     0,
		}

		enc.Encode(streamResponse)
		w.Write([]byte("\n"))

		// Send completion message
		doneResponse := map[string]interface{}{
			"model":             model,
			"message":           map[string]interface{}{},
			"done":              true,
			"total_duration":    0,
			"load_duration":     0,
			"prompt_eval_count": 0,
			"eval_count":        0,
			"eval_duration":     0,
		}

		enc.Encode(doneResponse)
	} else {
		// Send regular response
		response := map[string]interface{}{
			"model": model,
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": content,
			},
			"done":              true,
			"total_duration":    0,
			"load_duration":     0,
			"prompt_eval_count": 0,
			"eval_count":        0,
			"eval_duration":     0,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// sendOllamaResponse sends a response in Ollama /api/generate format
func (s *OrchestratorServer) sendOllamaResponse(w http.ResponseWriter, model string, content string, stream bool) {
	// Streaming is handled separately in handleStreamingGenerate
	if stream {
		// Should not reach here
		return
	}
	response := map[string]interface{}{
		"model":             model,
		"response":          content,
		"done":              true,
		"context":           []int{},
		"total_duration":    0,
		"load_duration":     0,
		"prompt_eval_count": 0,
		"eval_count":        0,
		"eval_duration":     0,
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(response)
}

// recordTask records the task execution for learning
func (s *OrchestratorServer) recordTask(payload map[string]interface{}, result *TaskResult) {
	// Extract query from payload
	query := ""
	if messages, ok := payload["messages"].([]interface{}); ok && len(messages) > 0 {
		if msg, ok := messages[len(messages)-1].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				query = content
			}
		}
	}

	record := &learning.TaskRecord{
		TaskID:    fmt.Sprintf("task-%d", time.Now().UnixNano()),
		AgentName: result.AgentName,
		Query:     query,
		Response:  result.Response,
		Duration:  result.Duration,
		Success:   result.Error == "",
		Error:     result.Error,
		Timestamp: time.Now(),
	}

	if s.learning != nil {
		s.learning.RecordTask(record)
	}
}

// sendOpenAIError sends an error response in OpenAI format
func (s *OrchestratorServer) sendOpenAIError(w http.ResponseWriter, errorType, message string, statusCode int) {
	response := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// sendOllamaError sends an error response in Ollama format
func (s *OrchestratorServer) sendOllamaError(w http.ResponseWriter, message string, statusCode int) {
	response := map[string]interface{}{
		"error": message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}
