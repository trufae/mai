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
)

// OrchestratorServer manages task routing to agents
type OrchestratorServer struct {
	config     *config.SwanConfig
	daemonMgr  *daemon.DaemonManager
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
	return &OrchestratorServer{
		config:    cfg,
		daemonMgr: daemonMgr,
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

	// Select agent
	agent := os.selectAgent()
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
		agent = os.selectAgent()
		if agent == nil {
			http.Error(w, "No agents available", http.StatusServiceUnavailable)
			return
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

// selectAgent selects an agent for task execution (simple round-robin for now)
func (os *OrchestratorServer) selectAgent() *daemon.AgentProcess {
	os.mu.Lock()
	defer os.mu.Unlock()

	agents := os.daemonMgr.ListAgents()
	if len(agents) == 0 {
		return nil
	}

	// Simple round-robin selection
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
		return &TaskResult{
			AgentName: agent.Name,
			Error:     err.Error(),
			Duration:  time.Since(start),
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &TaskResult{
			AgentName: agent.Name,
			Error:     fmt.Sprintf("failed to read response: %v", err),
			Duration:  time.Since(start),
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return &TaskResult{
			AgentName: agent.Name,
			Error:     fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
			Duration:  time.Since(start),
		}, nil
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
		return &TaskResult{
			AgentName: agent.Name,
			Response:  string(body),
			Duration:  time.Since(start),
		}, nil
	}

	response := ""
	if len(openaiResp.Choices) > 0 {
		response = openaiResp.Choices[0].Message.Content
	}

	return &TaskResult{
		AgentName: agent.Name,
		Response:  response,
		Duration:  time.Since(start),
	}, nil
}
