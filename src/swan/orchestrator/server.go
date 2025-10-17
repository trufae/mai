package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	learningEngine, err := learning.NewLearningEngine(cfg)
	if err != nil {
		// Log error but continue - learning features will be disabled
		fmt.Printf("Warning: failed to initialize learning engine: %v\n", err)
		learningEngine = nil
	}

	return &OrchestratorServer{
		config:    cfg,
		daemonMgr: daemonMgr,
		learning:  learningEngine,
	}
}

// Start starts the orchestrator server
func (os *OrchestratorServer) Start() error {
	mux := http.NewServeMux()

	// OpenAI-compatible endpoints
	mux.HandleFunc("/v1/chat/completions", os.handleChatCompletions)
	mux.HandleFunc("/v1/models", os.handleModels)

	// SWAN-specific endpoints
	mux.HandleFunc("/api/agents", os.handleListAgents)
	mux.HandleFunc("/api/task", os.handleTask)
	mux.HandleFunc("/api/communicate", os.handleInterAgentCommunication)
	mux.HandleFunc("/api/network-knowledge", os.handleGetNetworkKnowledge)
	mux.HandleFunc("/health", os.handleHealth)

	listenAddr := fmt.Sprintf("%s:%d", os.config.Orchestrator.ListenAddr, os.config.Orchestrator.Port)
	os.server = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	fmt.Printf("Starting orchestrator on %s\n", listenAddr)
	return os.server.ListenAndServe()
}

// Stop stops the orchestrator server
func (os *OrchestratorServer) Stop() error {
	if os.server != nil {
		return os.server.Close()
	}
	return nil
}

// handleChatCompletions handles OpenAI chat completions
func (os *OrchestratorServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
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

	// Select agent (may involve competition)
	agent := os.selectAgentWithCompetition(query)
	if agent == nil {
		http.Error(w, "No agents available", http.StatusServiceUnavailable)
		return
	}

	// Build task payload
	taskPayload := map[string]interface{}{
		"messages": req.Messages,
		"model":    req.Model,
		"stream":   req.Stream,
	}

	// Execute task
	result, err := os.executeTask(agent, taskPayload)
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
func (os *OrchestratorServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents := os.daemonMgr.ListAgents()
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
func (os *OrchestratorServer) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents := os.daemonMgr.ListAgents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

// handleTask handles direct task execution
func (os *OrchestratorServer) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AgentName string                 `json:"agent_name,omitempty"`
		Payload   map[string]interface{} `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	var agent *daemon.AgentProcess
	if req.AgentName != "" {
		var exists bool
		agent, exists = os.daemonMgr.GetAgent(req.AgentName)
		if !exists {
			http.Error(w, "Agent not found", http.StatusNotFound)
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

		agent = os.selectAgent(query)
		if agent == nil {
			// Try to create a new dynamic agent
			if suggestion, err := os.learning.SuggestNewAgent(); err == nil {
				if err := os.daemonMgr.StartResolvedAgent(*suggestion); err == nil {
					// Get the newly created agent
					agent, _ = os.daemonMgr.GetAgent(suggestion.Name)
				}
			}
			if agent == nil {
				http.Error(w, "No agents available", http.StatusServiceUnavailable)
				return
			}
		}
	}

	result, err := os.executeTask(agent, req.Payload)
	if err != nil {
		http.Error(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleHealth returns health status
func (os *OrchestratorServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	agents := os.daemonMgr.ListAgents()
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
func (os *OrchestratorServer) selectAgent(query string) *daemon.AgentProcess {
	os.mu.Lock()
	defer os.mu.Unlock()

	agents := os.daemonMgr.ListAgents()
	if len(agents) == 0 {
		return nil
	}

	// Try to get best agent from learning engine based on time/quality metrics
	if bestAgent, err := os.learning.GetBestAgent(query); err == nil {
		if agent, exists := agents[bestAgent]; exists {
			return agent
		}
	}

	// Fallback to round-robin if no learning data available
	agentNames := make([]string, 0, len(agents))
	for name := range agents {
		agentNames = append(agentNames, name)
	}

	selected := agentNames[os.agentIndex%len(agentNames)]
	os.agentIndex++

	return agents[selected]
}

// executeTask executes a task on the specified agent
func (os *OrchestratorServer) executeTask(agent *daemon.AgentProcess, payload map[string]interface{}) (*TaskResult, error) {
	start := time.Now()

	// Build request to agent's OpenAI endpoint
	agentURL := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", agent.Port)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %v", err)
	}

	resp, err := http.Post(agentURL, "application/json", bytes.NewReader(payloadBytes))
	if err != nil {
		result := &TaskResult{
			AgentName: agent.Name,
			Error:     err.Error(),
			Duration:  time.Since(start),
		}
		os.recordTask(payload, result)
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
		os.recordTask(payload, result)
		return result, nil
	}

	if resp.StatusCode != http.StatusOK {
		result := &TaskResult{
			AgentName: agent.Name,
			Error:     fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
			Duration:  time.Since(start),
		}
		os.recordTask(payload, result)
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
		os.recordTask(payload, result)
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
	os.recordTask(payload, result)
	return result, nil
}

// selectAgentWithCompetition selects an agent, potentially running a competition
func (os *OrchestratorServer) selectAgentWithCompetition(query string) *daemon.AgentProcess {
	// Check if we should run an evaluation competition
	if os.shouldRunCompetition(query) {
		result, err := os.runCompetition(query)
		if err == nil && result.Winner != "" {
			// Use the winning agent
			if winnerAgent, exists := os.daemonMgr.GetAgent(result.Winner); exists {
				return winnerAgent
			}
		}
	}

	// Fall back to regular agent selection
	return os.selectAgent(query)
}

// shouldRunCompetition determines if a competition should be run for this query
func (os *OrchestratorServer) shouldRunCompetition(query string) bool {
	// Run competitions for:
	// 1. Important/complex queries (longer than 50 characters)
	// 2. Queries we haven't seen much data for
	// 3. Periodically to keep agents competitive

	if len(query) < 50 {
		return false // Too simple for competition
	}

	// Check if we have enough agents for a competition
	agents := os.daemonMgr.ListAgents()
	if len(agents) < 2 {
		return false // Need at least 2 agents
	}

	// Run competition 10% of the time for important queries
	return time.Now().UnixNano()%10 == 0
}

// runCompetition executes a competition between agents
func (os *OrchestratorServer) runCompetition(query string) (*learning.CompetitionResult, error) {
	agents := os.daemonMgr.ListAgents()
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
		if agent, exists := os.daemonMgr.GetAgent(agentName); exists {
			result, err := os.executeTask(agent, taskPayload)
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
		quality := os.learning.AssessQuality(&learning.TaskRecord{
			Query:     query,
			Response:  result.Response,
			Duration:  result.Duration,
			Success:   result.Error == "",
			Error:     result.Error,
			Timestamp: time.Now(),
		})

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
func (os *OrchestratorServer) handleInterAgentCommunication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		FromAgent string `json:"from_agent"`
		ToAgent   string `json:"to_agent"`
		Message   string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.FromAgent == "" || req.ToAgent == "" || req.Message == "" {
		http.Error(w, "Missing required fields: from_agent, to_agent, message", http.StatusBadRequest)
		return
	}

	// Record the communication
	err := os.learning.RecordInterAgentCommunication(req.FromAgent, req.ToAgent, req.Message)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to record communication: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}

// handleGetNetworkKnowledge returns network knowledge for an agent
func (os *OrchestratorServer) handleGetNetworkKnowledge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentName := r.URL.Query().Get("agent")
	if agentName == "" {
		http.Error(w, "Missing agent parameter", http.StatusBadRequest)
		return
	}

	knowledge, err := os.learning.GetNetworkKnowledge(agentName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get network knowledge: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agent":     agentName,
		"knowledge": knowledge,
	})
}

// TriggerEvolution triggers autonomous evolution of prompts and configurations
func (os *OrchestratorServer) TriggerEvolution() error {
	return os.learning.EvolvePrompts()
}

// recordTask records the task execution for learning
func (os *OrchestratorServer) recordTask(payload map[string]interface{}, result *TaskResult) {
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

	os.learning.RecordTask(record)
}
