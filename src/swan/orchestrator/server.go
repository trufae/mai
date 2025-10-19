package orchestrator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mai/src/swan/config"
	"mai/src/swan/daemon"
	"mai/src/swan/learning"
)

// AnalysisResult represents the result of LLM analysis
type AnalysisResult struct {
	Intent     string              `json:"intent"`
	Confidence float64             `json:"confidence"`
	KeyInfo    []string            `json:"key_info"`
	StoreFlag  bool                `json:"store_flag"`
	Insights   map[string][]string `json:"insights,omitempty"` // Different types of insights
}

// OrchestratorServer manages task routing to agents
type OrchestratorServer struct {
	config         *config.SwanConfig
	daemonMgr      *daemon.DaemonManager
	learning       *learning.LearningEngine
	server         *http.Server
	agentIndex     int
	mu             sync.Mutex
	idleLearning   bool
	evolutionTimer *time.Ticker
	requestLogger  *os.File
	lastAnalysis   *AnalysisResult
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
		// Start idle learning
		learningEngine.StartIdleLearning(daemonMgr)
		fmt.Printf("DEBUG: Idle learning started\n")
	}

	// Initialize request logger
	requestLogPath := filepath.Join(cfg.WorkDir, "tmp", "swan_requests.log")
	requestLogger, err := os.OpenFile(requestLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("Warning: failed to open request log file: %v\n", err)
		requestLogger = nil
	}

	server := &OrchestratorServer{
		config:        cfg,
		daemonMgr:     daemonMgr,
		learning:      learningEngine,
		idleLearning:  true,
		requestLogger: requestLogger,
	}

	// Start idle learning if learning engine is available
	if learningEngine != nil {
		go server.idleLearningLoop()
		go server.autonomousEvolutionLoop()
	}

	return server
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
	mux.HandleFunc("/api/mcps", s.handleListMCPs)
	mux.HandleFunc("/api/task", s.handleTask)
	mux.HandleFunc("/api/communicate", s.handleInterAgentCommunication)
	mux.HandleFunc("/api/network-knowledge", s.handleGetNetworkKnowledge)
	mux.HandleFunc("/api/statistics", s.handleStatistics)
	mux.HandleFunc("/api/dataset", s.handleGetDataset)
	mux.HandleFunc("/api/trigger-evolution", s.handleTriggerEvolution)
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
	fmt.Printf("To connect with MAI: mai -b http://%s\n", listenAddr)
	fmt.Printf("OpenAI-compatible API: http://%s/v1/chat/completions\n", listenAddr)
	fmt.Printf("Ollama-compatible API: http://%s/api/generate\n", listenAddr)
	fmt.Printf("Models list: http://%s/api/tags\n", listenAddr)
	fmt.Printf("Health check: http://%s/health\n", listenAddr)
	fmt.Printf("Agent list: http://%s/api/agents\n", listenAddr)
	fmt.Printf("MCP list: http://%s/api/mcps\n", listenAddr)
	fmt.Printf("Statistics: http://%s/api/statistics\n", listenAddr)
	fmt.Printf("DEBUG: Calling ListenAndServe...\n")
	return s.server.ListenAndServe()
}

// Stop stops the orchestrator server
func (s *OrchestratorServer) Stop() error {
	if s.requestLogger != nil {
		s.requestLogger.Close()
	}
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// logRequest logs a request to the request log file
func (s *OrchestratorServer) logRequest(req *http.Request, query string, agentName string, quality float64, vdbContext string, reasoning string) {
	if s.requestLogger == nil {
		return
	}

	logEntry := map[string]interface{}{
		"timestamp":   time.Now().Format(time.RFC3339),
		"method":      req.Method,
		"path":        req.URL.Path,
		"query":       query,
		"agent":       agentName,
		"quality":     quality,
		"vdb_context": vdbContext,
		"reasoning":   reasoning,
		"user_agent":  req.Header.Get("User-Agent"),
		"remote_addr": req.RemoteAddr,
	}

	data, _ := json.Marshal(logEntry)
	s.requestLogger.WriteString(string(data) + "\n")
	s.requestLogger.Sync() // Ensure it's written immediately
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
	fmt.Printf("🔍 SWAN REQUEST: Received query '%s' (length: %d chars)\n", query, len(query))

	agent := s.selectAgentWithCompetition(query)
	if agent == nil {
		fmt.Printf("❌ SWAN DECISION: No agents available for query\n")
		s.sendOpenAIError(w, "service_unavailable", "No agents available", http.StatusServiceUnavailable)
		return
	}

	// Get VDB context for both logging and agent context
	vdbContext := "none"
	correctiveContext := ""

	// Retrieve corrective context from VDB
	correctiveContext = s.retrieveCorrectiveContext(query)
	if correctiveContext != "" {
		fmt.Printf("   📚 SWAN VDB: Found relevant corrective context (%d chars)\n", len(correctiveContext))
		vdbContext = "corrective_context_available"
	}

	// Get similar tasks for logging
	if s.learning != nil {
		if similar, err := s.learning.QuerySimilarTasks(query, 1); err == nil && len(similar) > 0 {
			if vdbContext == "none" {
				vdbContext = fmt.Sprintf("similar_task (agent: %s, quality: %.2f)", similar[0].AgentName, similar[0].Quality)
			} else {
				vdbContext += fmt.Sprintf(" + similar_task (agent: %s, quality: %.2f)", similar[0].AgentName, similar[0].Quality)
			}
		}
	}

	// Log agent selection reasoning
	fmt.Printf("🎯 SWAN DECISION: Selected agent '%s' for query\n", agent.Name)
	fmt.Printf("   📊 Agent: %s (Provider: %s, Model: %s)\n", agent.Name, agent.Config.Provider, agent.Config.Model)
	fmt.Printf("   🧠 VDB Context: %s\n", vdbContext)

	// Build task payload with corrective context if available
	taskPayload := map[string]interface{}{
		"messages": req.Messages,
		"model":    req.Model,
		"stream":   req.Stream,
	}

	// Include corrective context in the system message if available
	if correctiveContext != "" {
		// Convert req.Messages to the expected format and prepend system message
		messages := []map[string]interface{}{
			{
				"role":    "system",
				"content": correctiveContext,
			},
		}

		// Add original messages
		for _, msg := range req.Messages {
			messages = append(messages, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}

		taskPayload["messages"] = messages
		fmt.Printf("   📋 SWAN CONTEXT: Included corrective context in task payload\n")
	}

	// Execute task
	result, err := s.executeTask(agent, taskPayload)
	if err != nil {
		fmt.Printf("❌ SWAN ERROR: Task execution failed: %v\n", err)
		http.Error(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Calculate quality and perform learning on EVERY query
	quality := 0.5
	var cleanedResponse string
	var discardedInfo string
	if s.learning != nil {
		record := &learning.TaskRecord{
			Query:     query,
			Response:  result.Response,
			Duration:  result.Duration,
			Success:   result.Error == "",
			Error:     result.Error,
			Timestamp: time.Now(),
		}
		quality = s.learning.AssessQuality(record)

		// Perform prompt-based quality evaluation and cleanup
		cleanedResponse, discardedInfo = s.learning.EvaluateAndCleanResponse(query, result.Response)
		if cleanedResponse != result.Response {
			fmt.Printf("🧹 SWAN CLEANUP: Response cleaned - discarded %d chars of invalid information\n", len(result.Response)-len(cleanedResponse))
			result.Response = cleanedResponse
		}

		// Ensure learning happens on EVERY query - record the task
		fmt.Printf("📚 SWAN LEARNING: Processing query for learning and improvement\n")
		s.learning.RecordTask(record)
		fmt.Printf("   ✅ Task recorded in learning dataset\n")
		fmt.Printf("   📊 Quality assessment completed: %.2f/1.0\n", quality)
	}

	fmt.Printf("✅ SWAN RESULT: Response generated (%.2f%% quality, %v duration)\n", quality*100, result.Duration)

	// Show response preview
	previewLen := 200
	if len(result.Response) < previewLen {
		previewLen = len(result.Response)
	}
	fmt.Printf("   💬 Response preview: %s", result.Response[:previewLen])
	if len(result.Response) > previewLen {
		fmt.Printf("...\n")
	} else {
		fmt.Printf("\n")
	}

	// Log learning details
	fmt.Printf("📚 SWAN LEARNING: Recording task for future improvement\n")
	if discardedInfo != "" {
		fmt.Printf("   🗑️  Discarded info: %s\n", discardedInfo)
	}
	fmt.Printf("   📊 Quality assessment: %.2f/1.0\n", quality)
	fmt.Printf("   ⏱️  Duration: %v\n", result.Duration)
	fmt.Printf("   🤖 Agent: %s\n", agent.Name)

	// Log to request file
	reasoning := fmt.Sprintf("Selected %s based on performance metrics and VDB context", agent.Name)
	s.logRequest(r, query, agent.Name, quality, vdbContext, reasoning)

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

// handleListMCPs returns list of running MCPs
func (s *OrchestratorServer) handleListMCPs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mcps := s.daemonMgr.ListMCPs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mcps)
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

// handleStatistics returns learning engine statistics
func (s *OrchestratorServer) handleStatistics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var stats map[string]interface{}
	if s.learning != nil {
		stats = s.learning.GetStatistics()
		// Add dataset statistics
		datasetStats := s.learning.GetDatasetStats()
		stats["dataset"] = datasetStats
	} else {
		stats = map[string]interface{}{
			"status": "learning_engine_disabled",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleTriggerEvolution manually triggers evolution
func (s *OrchestratorServer) handleTriggerEvolution(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := s.TriggerEvolution()
	if err != nil {
		s.sendOllamaError(w, fmt.Sprintf("Evolution failed: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"status":    "evolution_triggered",
		"message":   "Evolution cycle completed successfully",
		"timestamp": time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleGetDataset returns the learning dataset
func (s *OrchestratorServer) handleGetDataset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendOllamaError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.learning == nil {
		s.sendOllamaError(w, "Learning engine not available", http.StatusServiceUnavailable)
		return
	}

	dataset := s.learning.GetDataset()

	// Check query parameters for filtering
	query := r.URL.Query()
	limitStr := query.Get("limit")
	agentFilter := query.Get("agent")

	var filteredDataset []*learning.LearningDataset
	for _, entry := range dataset {
		if agentFilter != "" && entry.AgentName != agentFilter {
			continue
		}
		filteredDataset = append(filteredDataset, entry)
	}

	// Apply limit if specified
	if limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(filteredDataset) {
			filteredDataset = filteredDataset[len(filteredDataset)-limit:]
		}
	}

	response := map[string]interface{}{
		"dataset":          filteredDataset,
		"total_entries":    len(dataset),
		"filtered_entries": len(filteredDataset),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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

// analyzeTone analyzes the tone of a user query using LLM analysis
func (s *OrchestratorServer) analyzeTone(query string) (tone string, confidence float64) {
	fmt.Printf("   🤖 SWAN LLM ANALYSIS: Analyzing query intent with model\n")

	// Use an available agent to analyze the query intent
	agents := s.daemonMgr.ListAgents()
	if len(agents) == 0 {
		fmt.Printf("   ⚠️  SWAN LLM ANALYSIS: No agents available, falling back to enhanced pattern analysis\n")
		return s.enhancedPatternAnalysis(query)
	}

	// Select the first available agent for analysis
	var analyzer *daemon.AgentProcess
	for _, agent := range agents {
		analyzer = agent
		break
	}

	// Create analysis prompt with clearer instructions
	analysisPrompt := `You are an expert at analyzing user queries and extracting insights. For the following query, determine:

INTENT: The primary intention (must be exactly one of: corrective, informational, aggressive, urgent, neutral)
CONFIDENCE: Your confidence in this classification (0.0 to 1.0)
KEY_INFO: If corrective, list the key statements that should be remembered (as an array)
STORE_FLAG: Whether this should be stored as corrective knowledge (true/false)
INSIGHTS: Extract any useful insights from this query categorized by type:
  - user_preferences: User preferences, coding styles, tool preferences
  - technical_insights: Technical knowledge, best practices, code patterns
  - error_patterns: Common errors, debugging tips, solutions
  - learning_points: General learning insights, concepts explained
  - conversation_notes: Important conversation points, context

Query: "` + query + `"

Format your response as valid JSON:
{
  "intent": "corrective",
  "confidence": 0.95,
  "key_info": ["The function should return null instead of undefined"],
  "store_flag": true,
  "insights": {
    "technical_insights": ["Use null instead of undefined for intentional empty values"],
    "user_preferences": ["Prefers explicit error handling"],
    "learning_points": ["Understanding difference between null and undefined"]
  }
}

 Do not include any other text or explanation.`

	fmt.Printf("   📝 ANALYSIS PROMPT: %s\n", analysisPrompt)

	// Build analysis task
	analysisPayload := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "user", "content": analysisPrompt},
		},
		"model":  analyzer.Config.Model,
		"stream": false,
	}

	// Execute analysis with timeout
	result, err := s.executeTask(analyzer, analysisPayload)
	if err != nil {
		fmt.Printf("   ❌ SWAN LLM ANALYSIS: Failed to analyze query: %v\n", err)
		return s.enhancedPatternAnalysis(query)
	}

	// Parse the JSON response
	var analysis AnalysisResult

	// Try to extract JSON from response - look for JSON patterns
	response := strings.TrimSpace(result.Response)

	// Remove any markdown code blocks if present
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr := response[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
			fmt.Printf("   ⚠️  SWAN LLM ANALYSIS: Failed to parse JSON response: %v\n", err)
			truncated := result.Response
			if len(truncated) > 200 {
				truncated = truncated[:200]
			}
			fmt.Printf("   📄 Raw response: %s\n", truncated)
			return s.enhancedPatternAnalysis(query)
		}
	} else {
		fmt.Printf("   ⚠️  SWAN LLM ANALYSIS: No JSON found in response\n")
		truncated := result.Response
		if len(truncated) > 200 {
			truncated = truncated[:200]
		}
		fmt.Printf("   📄 Raw response: %s\n", truncated)
		return s.enhancedPatternAnalysis(query)
	}

	// Validate the analysis results
	if analysis.Intent == "" {
		fmt.Printf("   ⚠️  SWAN LLM ANALYSIS: Empty intent, using fallback\n")
		return s.enhancedPatternAnalysis(query)
	}

	fmt.Printf("   ✅ SWAN LLM ANALYSIS: Intent=%s, Confidence=%.2f, Store=%t\n", analysis.Intent, analysis.Confidence, analysis.StoreFlag)
	if len(analysis.KeyInfo) > 0 {
		fmt.Printf("   📝 Key info extracted: %d statements\n", len(analysis.KeyInfo))
		for i, stmt := range analysis.KeyInfo {
			fmt.Printf("      %d: %s\n", i+1, stmt)
		}
	}

	// Store analysis results for later use
	s.lastAnalysis = &analysis

	return analysis.Intent, analysis.Confidence
}

// enhancedPatternAnalysis provides enhanced pattern-based tone analysis
func (s *OrchestratorServer) enhancedPatternAnalysis(query string) (tone string, confidence float64) {
	query = strings.ToLower(query)

	// Basic pattern matching as fallback
	correctionWords := []string{"wrong", "incorrect", "mistake", "error", "fix", "should be", "not working"}
	correctivePhrases := []string{"that's not", "you're wrong", "correction:", "actually,", "no,", "wait,"}

	correctionScore := 0
	words := strings.Fields(query)

	for _, word := range words {
		for _, cw := range correctionWords {
			if strings.Contains(word, cw) {
				correctionScore++
			}
		}
	}

	for _, phrase := range correctivePhrases {
		if strings.Contains(query, phrase) {
			correctionScore += 2
		}
	}

	if correctionScore > 0 {
		return "corrective", math.Min(float64(correctionScore)/float64(len(words)+1), 1.0)
	}

	return "neutral", 0.5
}

// extractCorrectiveStatements extracts key statements from LLM analysis results
func (s *OrchestratorServer) extractCorrectiveStatements(query string) []string {
	// Use LLM analysis results if available
	if s.lastAnalysis != nil && len(s.lastAnalysis.KeyInfo) > 0 {
		return s.lastAnalysis.KeyInfo
	}

	// Fallback to basic extraction if no LLM analysis available
	var statements []string
	sentences := strings.Split(query, ".")
	for _, sentence := range sentences {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}

		// Basic corrective pattern matching as fallback
		correctiveIndicators := []string{
			"wrong", "incorrect", "mistake", "error", "fix", "should be", "not working",
			"that's not", "you're wrong", "correction", "actually",
		}

		for _, indicator := range correctiveIndicators {
			if strings.Contains(strings.ToLower(sentence), indicator) {
				cleanSentence := strings.TrimSpace(sentence)
				if !strings.HasSuffix(cleanSentence, ".") {
					cleanSentence += "."
				}
				statements = append(statements, cleanSentence)
				break
			}
		}
	}

	return statements
}

// storeCorrectiveStatements stores corrective statements in VDB as plaintext
func (s *OrchestratorServer) storeCorrectiveStatements(statements []string, query string) error {
	if len(statements) == 0 {
		return nil
	}

	vdbDir := filepath.Join(s.config.WorkDir, "vdb")
	if err := os.MkdirAll(vdbDir, 0755); err != nil {
		return fmt.Errorf("failed to create VDB directory: %v", err)
	}

	// Create a statements file for corrective knowledge
	statementsFile := filepath.Join(vdbDir, "corrective_statements.txt")

	// Read existing content
	existingContent := ""
	if data, err := os.ReadFile(statementsFile); err == nil {
		existingContent = string(data)
	}

	// Add new statements
	newContent := existingContent
	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		newContent += "\n"
	}

	timestamp := time.Now().Format(time.RFC3339)
	newContent += fmt.Sprintf("=== CORRECTIVE STATEMENTS [%s] ===\n", timestamp)
	newContent += fmt.Sprintf("Original Query: %s\n", query)
	newContent += "Statements:\n"

	for i, statement := range statements {
		newContent += fmt.Sprintf("%d. %s\n", i+1, statement)
	}
	newContent += "\n"

	// Write back to file
	if err := os.WriteFile(statementsFile, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write corrective statements: %v", err)
	}

	fmt.Printf("   💾 SWAN VDB: Stored %d corrective statements\n", len(statements))
	return nil
}

// storeInsights stores different types of insights in separate VDB files
func (s *OrchestratorServer) storeInsights(insights map[string][]string, query string) error {
	if len(insights) == 0 {
		return nil
	}

	vdbDir := filepath.Join(s.config.WorkDir, "vdb")
	if err := os.MkdirAll(vdbDir, 0755); err != nil {
		return fmt.Errorf("failed to create VDB directory: %v", err)
	}

	timestamp := time.Now().Format(time.RFC3339)

	// Define the insight file mappings
	insightFiles := map[string]string{
		"user_preferences":   "user_preferences.txt",
		"technical_insights": "technical_insights.txt",
		"error_patterns":     "error_patterns.txt",
		"learning_points":    "learning_points.txt",
		"conversation_notes": "conversation_notes.txt",
	}

	storedCount := 0

	for insightType, statements := range insights {
		if len(statements) == 0 {
			continue
		}

		filename, exists := insightFiles[insightType]
		if !exists {
			continue // Skip unknown insight types
		}

		filePath := filepath.Join(vdbDir, filename)

		// Read existing content
		existingContent := ""
		if data, err := os.ReadFile(filePath); err == nil {
			existingContent = string(data)
		}

		// Add new insights
		newContent := existingContent
		if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
			newContent += "\n"
		}

		newContent += fmt.Sprintf("=== %s [%s] ===\n", strings.ToUpper(strings.ReplaceAll(insightType, "_", " ")), timestamp)
		newContent += fmt.Sprintf("Source Query: %s\n", query)
		newContent += "Insights:\n"

		for i, statement := range statements {
			newContent += fmt.Sprintf("%d. %s\n", i+1, statement)
		}
		newContent += "\n"

		// Write back to file
		if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
			fmt.Printf("   ⚠️  SWAN VDB: Failed to store %s: %v\n", insightType, err)
			continue
		}

		fmt.Printf("   💾 SWAN VDB: Stored %d %s\n", len(statements), insightType)
		storedCount += len(statements)
	}

	if storedCount > 0 {
		fmt.Printf("   ✅ SWAN VDB: Total insights stored: %d across %d categories\n", storedCount, len(insights))
	}

	return nil
}

// retrieveCorrectiveContext retrieves relevant corrective statements from VDB
func (s *OrchestratorServer) retrieveCorrectiveContext(query string) string {
	vdbDir := filepath.Join(s.config.WorkDir, "vdb")
	statementsFile := filepath.Join(vdbDir, "corrective_statements.txt")

	// Read the corrective statements file
	data, err := os.ReadFile(statementsFile)
	if err != nil {
		return "" // No corrective context available
	}

	content := string(data)
	if content == "" {
		return ""
	}

	// Simple relevance matching - look for keywords from query in corrective statements
	queryWords := strings.Fields(strings.ToLower(query))
	var relevantStatements []string

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "1. ") || strings.HasPrefix(line, "2. ") || strings.HasPrefix(line, "3. ") {
			// This is a statement line
			statement := strings.TrimPrefix(line, "1. ")
			statement = strings.TrimPrefix(statement, "2. ")
			statement = strings.TrimPrefix(statement, "3. ")

			// Check if statement is relevant to current query
			statementLower := strings.ToLower(statement)
			relevanceScore := 0

			for _, qWord := range queryWords {
				if len(qWord) > 3 && strings.Contains(statementLower, qWord) {
					relevanceScore++
				}
			}

			// If statement shares keywords with query, include it
			if relevanceScore > 0 {
				relevantStatements = append(relevantStatements, statement)
			}
		}
	}

	if len(relevantStatements) == 0 {
		return ""
	}

	// Return up to 3 most relevant statements
	context := "Previous corrections and important statements:\n"
	for i, stmt := range relevantStatements {
		if i >= 3 {
			break
		}
		context += fmt.Sprintf("- %s\n", stmt)
	}

	return context
}

// selectAgent selects an agent for task execution using learning engine and tone analysis
func (s *OrchestratorServer) selectAgent(query string) *daemon.AgentProcess {
	s.mu.Lock()
	defer s.mu.Unlock()

	agents := s.daemonMgr.ListAgents()
	if len(agents) == 0 {
		fmt.Printf("   ❌ SWAN DECISION: No agents available\n")
		return nil
	}

	fmt.Printf("   🤔 SWAN REASONING: Analyzing %d available agents for query\n", len(agents))

	// Analyze query tone for intelligent routing
	tone, confidence := s.analyzeTone(query)
	fmt.Printf("   🎭 Query tone: %s (confidence: %.2f)\n", tone, confidence)

	// Store insights from LLM analysis
	if s.lastAnalysis != nil && len(s.lastAnalysis.Insights) > 0 {
		fmt.Printf("   📊 SWAN INSIGHTS: Processing %d insight categories\n", len(s.lastAnalysis.Insights))
		totalInsights := 0
		for category, insights := range s.lastAnalysis.Insights {
			if len(insights) > 0 {
				fmt.Printf("   📝 %s: %d insights\n", category, len(insights))
				totalInsights += len(insights)
			}
		}

		if totalInsights > 0 {
			if err := s.storeInsights(s.lastAnalysis.Insights, query); err != nil {
				fmt.Printf("   ❌ SWAN INSIGHTS: Failed to store insights: %v\n", err)
			}
		}
	}

	// Handle corrective tones specially - store statements in VDB
	if tone == "corrective" && confidence > 0.05 && s.lastAnalysis != nil && s.lastAnalysis.StoreFlag {
		fmt.Printf("   📝 SWAN CORRECTIVE: Detected corrective tone - storing key information\n")
		correctiveStatements := s.lastAnalysis.KeyInfo
		fmt.Printf("   🔍 Extracted %d corrective statements\n", len(correctiveStatements))
		for i, stmt := range correctiveStatements {
			fmt.Printf("      %d: %s\n", i+1, stmt)
		}
		if len(correctiveStatements) > 0 {
			if err := s.storeCorrectiveStatements(correctiveStatements, query); err != nil {
				fmt.Printf("   ❌ SWAN CORRECTIVE: Failed to store statements: %v\n", err)
			} else {
				fmt.Printf("   ✅ SWAN CORRECTIVE: Stored %d corrective statements in VDB\n", len(correctiveStatements))
			}
		}
	}

	// Log tone analysis
	if s.learning != nil {
		s.learning.GetLogger().LogDecision("tone_analysis", fmt.Sprintf("Detected tone: %s (confidence: %.2f)", tone, confidence),
			map[string]interface{}{"tone": tone, "confidence": confidence, "query_length": len(query)}, nil, true, nil)
	}

	// For aggressive tones, prefer more experienced/stable agents
	if tone == "aggressive" && confidence > 0.1 {
		fmt.Printf("   ⚠️  SWAN DECISION: Aggressive tone detected - prioritizing experienced agents\n")
		// Try to find an agent with good performance history
		if s.learning != nil {
			stats := s.learning.GetStatistics()
			if agentRankings, ok := stats["agent_rankings"].([]map[string]interface{}); ok && len(agentRankings) > 0 {
				// Pick the highest ranked agent
				bestAgentName := agentRankings[0]["agent"].(string)
				if agent, exists := agents[bestAgentName]; exists {
					fmt.Printf("   🏆 SWAN DECISION: Selected top-ranked agent '%s' based on performance history\n", bestAgentName)
					fmt.Printf("   📊 Agent stats: %.1f%% success rate\n", agentRankings[0]["success_rate"].(float64)*100)
					return agent
				}
			}
		}
		fmt.Printf("   ⚠️  SWAN DECISION: No high-performing agents found for aggressive query\n")
	}

	// Try to get best agent from learning engine based on time/quality metrics
	if s.learning != nil {
		fmt.Printf("   🔍 SWAN DECISION: Consulting learning engine for agent recommendation\n")
		if bestAgent, err := s.learning.GetBestAgent(query); err == nil {
			if agent, exists := agents[bestAgent]; exists {
				fmt.Printf("   📈 SWAN DECISION: Learning engine recommends agent '%s'\n", bestAgent)
				fmt.Printf("   🎯 Reasoning: Based on historical performance for similar queries\n")
				return agent
			} else {
				fmt.Printf("   ⚠️  SWAN DECISION: Recommended agent '%s' not available\n", bestAgent)
			}
		} else {
			fmt.Printf("   📊 SWAN DECISION: Learning engine has no recommendation (%v)\n", err)
		}
	} else {
		fmt.Printf("   ⚠️  SWAN DECISION: Learning engine not available\n")
	}

	// Check for cached responses
	if s.learning != nil {
		if cached, exists := s.learning.GetCache()[query]; exists {
			fmt.Printf("   💾 SWAN DECISION: Found cached response from agent '%s'\n", cached.AgentName)
			if agent, exists := agents[cached.AgentName]; exists {
				fmt.Printf("   🚀 SWAN DECISION: Using cached agent '%s' for instant response\n", cached.AgentName)
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

	fmt.Printf("   🔄 SWAN DECISION: Using fallback round-robin selection\n")
	fmt.Printf("   🎲 Selected agent: '%s' (agent %d of %d)\n", selected, s.agentIndex%len(agentNames)+1, len(agentNames))
	return agents[selected]
}

// executeTask executes a task on the specified agent
func (s *OrchestratorServer) executeTask(agent *daemon.AgentProcess, payload map[string]interface{}) (*TaskResult, error) {
	fmt.Printf("   🤖 EXECUTING TASK on agent '%s' with payload: %+v\n", agent.Name, payload)
	start := time.Now()

	// Build request to the provider endpoint using agent's model
	baseURL := agent.Config.BaseURL
	if baseURL == "" {
		if agent.Config.Provider == "openai" {
			baseURL = "https://api.openai.com/v1"
		} else {
			baseURL = "http://localhost:11434"
		}
	}

	var agentURL string
	if agent.Config.Provider == "openai" {
		agentURL = baseURL + "/chat/completions"
	} else {
		agentURL = baseURL + "/api/chat"
	}

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

	var response string

	if agent.Config.Provider == "openai" {
		// Parse OpenAI response
		var openaiResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}

		if err := json.Unmarshal(body, &openaiResp); err != nil || len(openaiResp.Choices) == 0 {
			response = string(body)
			// Fallback for empty or unparseable response
			if response == "" {
				response = "I'm sorry, I couldn't generate a response at this time. Please try rephrasing your query or try again later."
				fmt.Printf("DEBUG: Empty or unparseable response from OpenAI agent %s, using fallback\n", agent.Name)
			}
		} else {
			response = openaiResp.Choices[0].Message.Content
		}
	} else {
		// Parse Ollama response (try /api/chat format first, then /api/generate)
		var ollamaResp struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}

		var generateResp struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}

		if err := json.Unmarshal(body, &ollamaResp); err == nil && ollamaResp.Message.Content != "" {
			response = ollamaResp.Message.Content
		} else if err2 := json.Unmarshal(body, &generateResp); err2 == nil && generateResp.Response != "" {
			response = generateResp.Response
		} else {
			response = string(body)
			// Fallback for empty or unparseable response
			if response == "" {
				response = "I'm sorry, I couldn't generate a response at this time. Please try rephrasing your query or try again later."
				fmt.Printf("DEBUG: Empty or unparseable response from Ollama agent %s, using fallback\n", agent.Name)
			}
		}
	}

	// Fallback for empty response to ensure self-sufficiency
	if response == "" {
		response = "I'm sorry, I couldn't generate a response at this time. Please try rephrasing your query or try again later."
		fmt.Printf("DEBUG: Empty response from agent %s, using fallback\n", agent.Name)
	}

	result := &TaskResult{
		AgentName: agent.Name,
		Response:  response,
		Duration:  time.Since(start),
	}
	s.recordTask(payload, result)
	return result, nil
}

// idleLearningLoop performs learning tasks during idle periods
func (s *OrchestratorServer) idleLearningLoop() {
	ticker := time.NewTicker(10 * time.Minute) // Check every 10 minutes
	defer ticker.Stop()

	for s.idleLearning {
		select {
		case <-ticker.C:
			s.performIdleLearning()
		}
	}
}

// performIdleLearning executes idle learning tasks
func (s *OrchestratorServer) performIdleLearning() {
	if s.learning == nil {
		return
	}

	dataset := s.learning.GetDataset()
	if len(dataset) < 5 {
		return // Not enough data for meaningful learning
	}

	// Find queries that haven't been tested with multiple agents
	queryAgentMap := make(map[string]map[string]bool)
	for _, entry := range dataset {
		if queryAgentMap[entry.Query] == nil {
			queryAgentMap[entry.Query] = make(map[string]bool)
		}
		queryAgentMap[entry.Query][entry.AgentName] = true
	}

	// Get available agents
	agents := s.daemonMgr.ListAgents()
	if len(agents) < 2 {
		return // Need at least 2 agents for comparison
	}

	// Find a query that could benefit from testing with another agent
	for query, testedAgents := range queryAgentMap {
		if len(testedAgents) >= len(agents) {
			continue // Already tested with all agents
		}

		// Find an agent that hasn't tested this query
		for agentName := range agents {
			if !testedAgents[agentName] {
				// Execute this query with the new agent
				s.executeIdleLearningTask(query, agentName)
				return // Only do one task per idle learning cycle
			}
		}
	}
}

// executeIdleLearningTask executes a learning task during idle time
func (s *OrchestratorServer) executeIdleLearningTask(query, agentName string) {
	agent, exists := s.daemonMgr.GetAgent(agentName)
	if !exists {
		return
	}

	// Build task payload
	taskPayload := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "user", "content": query},
		},
		"idle_learning": true, // Mark as idle learning task
	}

	s.mu.Lock()
	result, err := s.executeTask(agent, taskPayload)
	s.mu.Unlock()

	if err != nil {
		s.learning.GetLogger().LogDecision("idle_learning_error",
			fmt.Sprintf("Failed idle learning task for query with agent %s: %v", agentName, err),
			map[string]interface{}{"query": query, "agent": agentName}, nil, false, err)
		return
	}

	s.learning.GetLogger().LogDecision("idle_learning_completed",
		fmt.Sprintf("Completed idle learning task for query with agent %s", agentName),
		map[string]interface{}{
			"query":       query,
			"agent":       agentName,
			"quality":     result.Response != "",
			"duration_ms": result.Duration.Milliseconds(),
		}, nil, true, nil)
}

// autonomousEvolutionLoop runs the autonomous evolution process
func (s *OrchestratorServer) autonomousEvolutionLoop() {
	// Run evolution every hour
	s.evolutionTimer = time.NewTicker(1 * time.Hour)
	defer s.evolutionTimer.Stop()

	for {
		select {
		case <-s.evolutionTimer.C:
			s.performEvolution()
		}
	}
}

// performEvolution triggers the autonomous evolution of prompts and configurations
func (s *OrchestratorServer) performEvolution() {
	if s.learning == nil {
		return
	}

	fmt.Printf("🔄 SWAN EVOLUTION: Starting autonomous evolution cycle\n")

	err := s.learning.EvolvePrompts()
	if err != nil {
		fmt.Printf("❌ SWAN EVOLUTION: Failed - %v\n", err)
		s.learning.GetLogger().LogDecision("evolution_error",
			fmt.Sprintf("Failed to perform autonomous evolution: %v", err),
			nil, nil, false, err)
		return
	}

	fmt.Printf("✅ SWAN EVOLUTION: Successfully completed evolution cycle\n")
	fmt.Printf("   ⏰ Next evolution: %s\n", time.Now().Add(1*time.Hour).Format(time.RFC3339))

	s.learning.GetLogger().LogDecision("evolution_completed",
		"Successfully completed autonomous evolution cycle",
		map[string]interface{}{
			"evolution_type": "autonomous",
			"next_run":       time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		}, nil, true, nil)
}

// TriggerEvolution manually triggers evolution (for API endpoint)
func (s *OrchestratorServer) TriggerEvolution() error {
	if s.learning == nil {
		return fmt.Errorf("learning engine not available")
	}

	fmt.Printf("🔧 SWAN EVOLUTION: Manual evolution triggered via API\n")

	err := s.learning.EvolvePrompts()
	if err != nil {
		fmt.Printf("❌ SWAN EVOLUTION: Manual evolution failed - %v\n", err)
		return err
	}

	fmt.Printf("✅ SWAN EVOLUTION: Manual evolution completed successfully\n")

	s.learning.GetLogger().LogDecision("evolution_triggered",
		"Manual evolution trigger completed",
		map[string]interface{}{
			"evolution_type": "manual",
		}, nil, true, nil)

	return nil
}

// sendOpenAIError sends an error response in OpenAI format
func (s *OrchestratorServer) sendOpenAIError(w http.ResponseWriter, errorType string, message string, statusCode int) {
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
	case "/mcps", "list mcps", "mcps":
		mcps := s.daemonMgr.ListMCPs()
		if len(mcps) == 0 {
			return "No MCPs currently running"
		}
		response := "Running MCPs:\n"
		for name, mcp := range mcps {
			response += fmt.Sprintf("- %s (PID: %d, Port: %d, Command: %s)\n",
				name, mcp.PID, mcp.Port, mcp.Config.Command)
		}
		return response
	case "/stats", "statistics", "stats":
		if s.learning != nil {
			stats := s.learning.GetStatistics()
			return fmt.Sprintf("SWAN Statistics:\nTotal tasks: %v\nCached responses: %v\nVDB entries: %v\nAgents tracked: %v\nAvg quality: %.2f\nSuccess rate: %.2f%%",
				stats["total_tasks"], stats["cached_responses"], stats["vdb_entries"], stats["agents_tracked"],
				stats["avg_quality"], stats["success_rate"].(float64)*100)
		}
		return "Learning engine not available"
	case "/help", "help":
		return "Available commands:\n/health - Show system health\n/agents - List running agents\n/mcps - List running MCPs\n/stats - Show learning statistics\n/help - Show this help"
	default:
		return ""
	}
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

// selectAgentWithCompetition selects an agent, potentially running a competition
func (s *OrchestratorServer) selectAgentWithCompetition(query string) *daemon.AgentProcess {
	// Check if we should run an evaluation competition
	if s.shouldRunCompetition(query) {
		fmt.Printf("   🎪 SWAN DECISION: Competition triggered for complex query\n")
		result, err := s.runCompetition(query)
		if err == nil && result.Winner != "" {
			// Use the winning agent
			if winnerAgent, exists := s.daemonMgr.GetAgent(result.Winner); exists {
				fmt.Printf("   🏅 Using competition winner: %s\n", result.Winner)
				return winnerAgent
			}
		} else {
			fmt.Printf("   ⚠️  Competition failed or no winner, falling back to regular selection\n")
		}
	} else {
		fmt.Printf("   📋 SWAN DECISION: Using standard agent selection (no competition needed)\n")
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

	queryLen := len(query)
	if queryLen < 50 {
		fmt.Printf("   🚫 Competition skipped: Query too short (%d chars < 50)\n", queryLen)
		return false // Too simple for competition
	}

	// Check if we have enough agents for a competition
	agents := s.daemonMgr.ListAgents()
	if len(agents) < 2 {
		fmt.Printf("   🚫 Competition skipped: Need at least 2 agents (%d available)\n", len(agents))
		return false // Need at least 2 agents
	}

	// Run competition 10% of the time for important queries
	randomFactor := time.Now().UnixNano() % 10
	shouldRun := randomFactor == 0

	if shouldRun {
		fmt.Printf("   🎲 Competition triggered: Random factor hit (1/10 chance)\n")
	} else {
		fmt.Printf("   🎲 Competition skipped: Random factor not hit (%d/10)\n", randomFactor)
	}

	return shouldRun
}

// runCompetition executes a competition between agents
func (s *OrchestratorServer) runCompetition(query string) (*learning.CompetitionResult, error) {
	agents := s.daemonMgr.ListAgents()
	if len(agents) < 2 {
		return nil, fmt.Errorf("not enough agents for competition")
	}

	fmt.Printf("   🏁 SWAN COMPETITION: Starting competition for query\n")
	fmt.Printf("   👥 Available agents: %d\n", len(agents))

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

	fmt.Printf("   🎯 Competing agents: %v\n", competitorNames)

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
		fmt.Printf("   ⚡ Running competition task on agent: %s\n", agentName)
		if agent, exists := s.daemonMgr.GetAgent(agentName); exists {
			result, err := s.executeTask(agent, taskPayload)
			if err == nil {
				results[agentName] = result
				fmt.Printf("      ✅ %s completed in %v\n", agentName, result.Duration)
			} else {
				fmt.Printf("      ❌ %s failed: %v\n", agentName, err)
			}
		}
	}

	if len(results) == 0 {
		fmt.Printf("   ❌ SWAN COMPETITION: No valid results from any competitor\n")
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

	fmt.Printf("   📊 SWAN COMPETITION: Evaluating %d competition results...\n", len(results))

	for agentName, result := range results {
		fmt.Printf("   🔍 SWAN COMPETITION: Analyzing response from %s\n", agentName)

		// Assess quality for competition
		quality := 0.5 // Default quality if learning engine is disabled
		if s.learning != nil {
			record := &learning.TaskRecord{
				Query:     query,
				Response:  result.Response,
				Duration:  result.Duration,
				Success:   result.Error == "",
				Error:     result.Error,
				Timestamp: time.Now(),
			}
			quality = s.learning.AssessQuality(record)
			fmt.Printf("      📈 Quality assessment: %.2f/1.0\n", quality)
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

		fmt.Printf("      🎖️  %s: %.2f%% quality (%v duration)\n", agentName, quality*100, result.Duration)
		fmt.Printf("      📝 Competition result logged for learning\n")

		if quality > bestQuality {
			bestQuality = quality
			winner = agentName
			fmt.Printf("      🏆 New winner: %s with %.2f%% quality\n", agentName, quality*100)
		}
	}

	competitionResult.Winner = winner
	competitionResult.Reasoning = fmt.Sprintf("Agent %s won with quality score %.2f", winner, bestQuality)

	fmt.Printf("   🏆 SWAN COMPETITION: Winner is %s with %.2f%% quality!\n", winner, bestQuality*100)

	return competitionResult, nil
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
	fmt.Printf("   📥 USER QUERY: %s\n", query)

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

	// Analyze query tone for informational handling
	tone, confidence := s.analyzeTone(query)
	if tone == "informational" && confidence > 0.5 {
		// Convert messages
		messages := make([]map[string]interface{}, len(req.Messages))
		for i, m := range req.Messages {
			messages[i] = map[string]interface{}{"role": m.Role, "content": m.Content}
		}
		s.handleInformationalQuery(w, messages, req.Model, req.Stream)
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

// handleInformationalQuery handles informational queries by querying all agents and VDB
func (s *OrchestratorServer) handleInformationalQuery(w http.ResponseWriter, messages []map[string]interface{}, reqModel string, stream bool) {
	// Extract query
	query := ""
	for _, msg := range messages {
		if msg["role"] == "user" {
			query = msg["content"].(string)
			break
		}
	}
	fmt.Printf("   🤖 SWAN INFORMATIONAL: Handling informational query '%s' with all agents\n", query)

	agents := s.daemonMgr.ListAgents()
	if len(agents) == 0 {
		s.sendOllamaError(w, "No agents available", http.StatusServiceUnavailable)
		return
	}

	// Query all agents
	var responses []string
	var agentNames []string
	for name, agent := range agents {
		fmt.Printf("   📡 Querying agent '%s'\n", name)
		taskPayload := map[string]interface{}{
			"messages": messages,
			"model":    agent.Config.Model,
			"stream":   false,
		}
		result, err := s.executeTask(agent, taskPayload)
		if err != nil {
			fmt.Printf("   ❌ Failed to query agent '%s': %v\n", name, err)
			continue
		}
		responses = append(responses, result.Response)
		agentNames = append(agentNames, name)
	}

	if len(responses) == 0 {
		s.sendOllamaError(w, "All agent queries failed", http.StatusInternalServerError)
		return
	}

	// Query VDB for relevant information
	var vdbInfo []string
	var correctiveInfo []string
	if s.learning != nil && s.learning.GetVDB() != nil {
		vdbResults := s.learning.GetVDB().Query(query, 5) // get top 5
		for _, entry := range vdbResults {
			if entry.Query == "corrective_statements.txt" {
				correctiveInfo = append(correctiveInfo, entry.Response)
			} else {
				vdbInfo = append(vdbInfo, entry.Response)
			}
		}
		fmt.Printf("   📚 Retrieved %d general items and %d corrective items from VDB\n", len(vdbInfo), len(correctiveInfo))
	}

	// Synthesize response using the first agent
	synthesizer := agents[agentNames[0]]
	synthesisPrompt := fmt.Sprintf(`You are an AI assistant. Answer the user's query using the provided information.

User Query: %s

Responses from other agents:
%s

Relevant information from knowledge base:
%s

Important corrections (use these to fix any errors):
%s

Provide a clear and accurate answer to the query. If there are corrections, apply them to ensure accuracy.`, query, strings.Join(responses, "\n\n"), strings.Join(vdbInfo, "\n\n"), strings.Join(correctiveInfo, "\n\n"))

	fmt.Printf("   🔧 SYNTHESIS PROMPT: %s\n", synthesisPrompt)

	synthesisPayload := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "user", "content": synthesisPrompt},
		},
		"model":  synthesizer.Config.Model,
		"stream": false,
	}

	result, err := s.executeTask(synthesizer, synthesisPayload)
	if err != nil {
		fmt.Printf("   ❌ Failed to synthesize response: %v\n", err)
		// Fallback to first response
		s.sendOllamaChatResponse(w, agentNames[0], responses[0], stream)
		return
	}

	fmt.Printf("   ✅ SWAN INFORMATIONAL: Synthesized response from %d agents and %d VDB items\n", len(responses), len(vdbInfo))
	s.sendOllamaChatResponse(w, "swan-synthesis", result.Response, stream)
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

// handleStreamingChat handles streaming for /api/chat
func (s *OrchestratorServer) handleStreamingChat(w http.ResponseWriter, agent *daemon.AgentProcess, messages []map[string]interface{}) {
	baseURL := agent.Config.BaseURL
	if baseURL == "" {
		if agent.Config.Provider == "openai" {
			baseURL = "https://api.openai.com/v1"
		} else {
			baseURL = "http://localhost:11434"
		}
	}

	var agentURL string
	var payload map[string]interface{}
	var headers = map[string]string{"Content-Type": "application/json"}

	if agent.Config.Provider == "openai" {
		agentURL = baseURL + "/chat/completions"
		payload = map[string]interface{}{
			"messages": messages,
			"model":    agent.Config.Model,
			"stream":   true,
		}
		if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
	} else {
		agentURL = baseURL + "/api/chat"
		payload = map[string]interface{}{
			"model":    agent.Config.Model,
			"messages": messages,
			"stream":   true,
		}
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

// handleStreamingGenerate handles streaming for /api/generate
func (s *OrchestratorServer) handleStreamingGenerate(w http.ResponseWriter, agent *daemon.AgentProcess, prompt string) {
	agentURL := fmt.Sprintf("http://localhost:%d/api/generate", agent.Port)

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

// sendOllamaError sends an error response in Ollama format
func (s *OrchestratorServer) sendOllamaError(w http.ResponseWriter, message string, statusCode int) {
	response := map[string]interface{}{
		"error": message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}
