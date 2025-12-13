package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

// ServerStatus represents the current state of the web server
type ServerStatus int

const (
	ServerStopped ServerStatus = iota
	ServerStarting
	ServerRunning
	ServerStopping
)

// ServerManager manages the background web server
type ServerManager struct {
	mu         sync.RWMutex
	status     ServerStatus
	server     *http.Server
	config     *llm.Config
	llmClient  *llm.LLMClient
	listenAddr string
	wwwRoot    string
	repl       *REPL
}

// Global server manager instance
var serverManager *ServerManager

// OpenAI-compatible request/response structures
type ChatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []OpenAIMessage `json:"messages"`
	Stream         bool            `json:"stream,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type ResponseFormat struct {
	Type       string                 `json:"type"`
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
}

type ChatCompletionChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message,omitempty"`
	Delta        OpenAIMessage `json:"delta,omitempty"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// Simple chat request/response structures
type SimpleChatRequest struct {
	Message string `json:"message"`
	System  string `json:"system,omitempty"`
	Stream  bool   `json:"stream,omitempty"`
}

type SimpleChatResponse struct {
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

// Generate request/response structures
type GenerateRequest struct {
	Prompt    string `json:"prompt"`
	System    string `json:"system,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Stream    bool   `json:"stream,omitempty"`
}

type GenerateResponse struct {
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

// NewServerManager creates a new server manager
func NewServerManager(config *llm.Config, listenAddr string, wwwRoot string, repl *REPL) *ServerManager {
	return &ServerManager{
		status:     ServerStopped,
		config:     config,
		listenAddr: listenAddr,
		wwwRoot:    wwwRoot,
		repl:       repl,
	}
}

// Start starts the web server in a background goroutine
func (sm *ServerManager) Start() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.status == ServerRunning || sm.status == ServerStarting {
		return fmt.Errorf("server is already running")
	}

	sm.status = ServerStarting

	// Create HTTP server
	mux := http.NewServeMux()

	// Static file serving
	wwwRoot := sm.wwwRoot
	if wwwRoot != "" {
		absPath, err := filepath.Abs(wwwRoot)
		if err == nil {
			mux.Handle("/", http.FileServer(http.Dir(absPath)))
		}
	}

	// API endpoints
	mux.HandleFunc("/v1/models", sm.handleModels)
	mux.HandleFunc("/v1/chat/completions", sm.handleChatCompletions)
	mux.HandleFunc("/health", sm.handleHealth)

	// Additional simplified endpoints
	mux.HandleFunc("/api/chat", sm.handleSimpleChat)
	mux.HandleFunc("/api/generate", sm.handleGenerate)

	// Web interface API endpoints
	mux.HandleFunc("/api/config", sm.handleGetConfig)
	mux.HandleFunc("/api/config/set", sm.handleSetConfig)
	mux.HandleFunc("/api/models/", sm.handleGetProviderModels)

	sm.server = &http.Server{
		Addr:    sm.listenAddr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		sm.mu.Lock()
		sm.status = ServerRunning
		sm.mu.Unlock()

		// fmt.Printf("Server started on http://%s\n", sm.listenAddr)
		if err := sm.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
			sm.mu.Lock()
			sm.status = ServerStopped
			sm.mu.Unlock()
		}
	}()

	return nil
}

// getLLMClient returns the current REPL client if available, or creates one
func (sm *ServerManager) getLLMClient() (*llm.LLMClient, error) {
	if sm.repl != nil {
		// Try to reuse the REPL's current client if set
		sm.repl.mu.Lock()
		cc := sm.repl.currentClient
		sm.repl.mu.Unlock()
		if cc != nil {
			return cc, nil
		}
		// Otherwise, create a new client using REPL's config
		return llm.NewLLMClient(sm.repl.buildLLMConfig(), sm.repl.ctx)
	}
	// Fallback to server config if REPL is not present
	return llm.NewLLMClient(sm.config, context.Background())
}

// executeInputWithCapture runs a plain user input through the REPL and captures output
func (sm *ServerManager) executeInputWithCapture(input string, stream bool, system string) (string, error) {
	if sm.repl == nil {
		return "", fmt.Errorf("REPL not available")
	}

	// Optionally override streaming and system prompt for this call
	oldStream := sm.repl.configOptions.Get("llm.stream")
	oldSystem := sm.repl.configOptions.Get("llm.systemprompt")
	// Best-effort set; ignore validation errors (boolean requires true/false)
	_ = sm.repl.configOptions.Set("llm.stream", map[bool]string{true: "true", false: "false"}[stream])
	if system != "" {
		_ = sm.repl.configOptions.Set("llm.systemprompt", system)
	}

	// Capture stdout/stderr while invoking REPL sendToAI
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return "", fmt.Errorf("failed to create stderr pipe: %v", err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW

	// Run the input through the REPL pipeline
	callErr := sm.repl.sendToAI(input, "", "", true, false)

	// Restore streams
	stdoutW.Close()
	stderrW.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	// Read captured output
	outBytes, rerr := io.ReadAll(stdoutR)
	stdoutR.Close()
	if rerr != nil {
		return "", fmt.Errorf("failed to read stdout: %v", rerr)
	}
	errBytes, rerr := io.ReadAll(stderrR)
	stderrR.Close()
	if rerr != nil {
		return "", fmt.Errorf("failed to read stderr: %v", rerr)
	}

	// Restore previous config
	_ = sm.repl.configOptions.Set("llm.stream", oldStream)
	if system != "" || oldSystem != "" {
		if oldSystem == "" {
			sm.repl.configOptions.Unset("llm.systemprompt")
		} else {
			_ = sm.repl.configOptions.Set("llm.systemprompt", oldSystem)
		}
	}

	var b strings.Builder
	if len(outBytes) > 0 {
		b.Write(outBytes)
	}
	if len(errBytes) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(errBytes)
	}

	if callErr != nil {
		return "", callErr
	}

	return strings.TrimRight(b.String(), "\n\r"), nil
}

// Stop stops the web server
func (sm *ServerManager) Stop() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.status == ServerStopped || sm.status == ServerStopping {
		return fmt.Errorf("server is not running")
	}

	sm.status = ServerStopping

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sm.server.Shutdown(ctx); err != nil {
		sm.status = ServerStopped
		return fmt.Errorf("failed to stop server: %v", err)
	}

	sm.status = ServerStopped
	fmt.Println("Server stopped")
	return nil
}

// GetStatus returns the current server status
func (sm *ServerManager) GetStatus() ServerStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.status
}

// GetStatusString returns a human-readable status string
func (sm *ServerManager) GetStatusString() string {
	switch sm.GetStatus() {
	case ServerStopped:
		return "stopped"
	case ServerStarting:
		return "starting"
	case ServerRunning:
		return "running"
	case ServerStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

// executeCommandWithCapture executes a REPL command and captures its output
func (sm *ServerManager) executeCommandWithCapture(command string) (string, error) {
	// Create pipes to capture both stdout and stderr
	oldStdout := os.Stdout
	oldStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return "", fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	// Redirect stdout and stderr to the pipes
	os.Stdout = stdoutW
	os.Stderr = stderrW

	// Execute the command
	err = sm.repl.handleCommand(command, "", "")

	// Restore stdout and stderr
	stdoutW.Close()
	stderrW.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	// Read the captured output from both pipes
	var output strings.Builder

	stdoutData, readErr := io.ReadAll(stdoutR)
	stdoutR.Close()
	if readErr != nil {
		return "", fmt.Errorf("failed to read stdout: %v", readErr)
	}

	stderrData, readErr := io.ReadAll(stderrR)
	stderrR.Close()
	if readErr != nil {
		return "", fmt.Errorf("failed to read stderr: %v", readErr)
	}

	// Combine stdout and stderr output
	if len(stdoutData) > 0 {
		output.Write(stdoutData)
	}
	if len(stderrData) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.Write(stderrData)
	}

	// Return any execution error
	if err != nil {
		return "", err
	}

	// Clean up the output (remove trailing newlines and carriage returns)
	cleanedOutput := strings.TrimRight(output.String(), "\n\r")

	return cleanedOutput, nil
}

// handleModels handles the /v1/models endpoint
func (sm *ServerManager) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get available models using REPL-configured client
	client, err := sm.getLLMClient()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to init client: %v", err), http.StatusInternalServerError)
		return
	}
	models, err := client.ListModels()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list models: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert to OpenAI format
	var modelInfos []ModelInfo
	for _, model := range models {
		modelInfos = append(modelInfos, ModelInfo{
			ID:      model.ID,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: model.Provider,
		})
	}

	response := ModelsResponse{
		Object: "list",
		Data:   modelInfos,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleChatCompletions handles the /v1/chat/completions endpoint
func (sm *ServerManager) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Convert OpenAI messages to internal format
	messages := make([]llm.Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = llm.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// Handle schema if provided
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		sm.config.Schema = req.ResponseFormat.JSONSchema
	} else {
		sm.config.Schema = nil
	}

	// Set streaming based on request
	stream := req.Stream

	if stream {
		sm.handleStreamingResponse(w, r, messages, req.Model)
	} else {
		sm.handleNonStreamingResponse(w, r, messages, req.Model)
	}
}

// handleStreamingResponse handles streaming chat completions
func (sm *ServerManager) handleStreamingResponse(w http.ResponseWriter, r *http.Request, messages []llm.Message, model string) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// For now, use non-streaming and simulate streaming
	client, err := sm.getLLMClient()
	if err != nil {
		fmt.Fprintf(w, "data: [ERROR] %v\n\n", err)
		return
	}
	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		fmt.Fprintf(w, "data: [ERROR] %v\n\n", err)
		return
	}

	// Simulate streaming by sending chunks
	words := strings.Fields(response)
	var fullResponse strings.Builder

	for i, word := range words {
		chunk := word + " "
		fullResponse.WriteString(chunk)

		// Send chunk
		chunkResponse := ChatCompletionResponse{
			ID:      "chatcmpl-" + fmt.Sprintf("%d", time.Now().Unix()),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Delta: OpenAIMessage{
						Role:    "assistant",
						Content: chunk,
					},
				},
			},
		}

		data, _ := json.Marshal(chunkResponse)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		// Small delay to simulate streaming
		time.Sleep(50 * time.Millisecond)

		// Send finish reason on last chunk
		if i == len(words)-1 {
			finalChunk := ChatCompletionResponse{
				ID:      "chatcmpl-" + fmt.Sprintf("%d", time.Now().Unix()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   model,
				Choices: []ChatCompletionChoice{
					{
						Index:        0,
						Delta:        OpenAIMessage{},
						FinishReason: "stop",
					},
				},
			}

			data, _ := json.Marshal(finalChunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleNonStreamingResponse handles non-streaming chat completions
func (sm *ServerManager) handleNonStreamingResponse(w http.ResponseWriter, r *http.Request, messages []llm.Message, model string) {
	// Send message using REPL-configured client
	client, err := sm.getLLMClient()
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM init error: %v", err), http.StatusInternalServerError)
		return
	}
	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}

	// Create response
	completionResponse := ChatCompletionResponse{
		ID:      "chatcmpl-" + fmt.Sprintf("%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Message: OpenAIMessage{
					Role:    "assistant",
					Content: response,
				},
				FinishReason: "stop",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(completionResponse)
}

// handleHealth handles the /health endpoint
func (sm *ServerManager) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"server": sm.GetStatusString(),
	})
}

// handleSimpleChat handles the /api/chat endpoint - simplified chat interface
func (sm *ServerManager) handleSimpleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SimpleChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	// Route through REPL for both commands and normal inputs
	var out string
	var err error
	if strings.HasPrefix(req.Message, "/") {
		out, err = sm.executeCommandWithCapture(req.Message)
	} else {
		out, err = sm.executeInputWithCapture(req.Message, req.Stream, req.System)
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SimpleChatResponse{
			Response: "",
			Error:    fmt.Sprintf("%v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SimpleChatResponse{Response: out})
}

// handleGenerate handles the /api/generate endpoint - simple text generation
func (sm *ServerManager) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "Prompt is required", http.StatusBadRequest)
		return
	}

	// Route through REPL input pipeline and capture output
	out, err := sm.executeInputWithCapture(req.Prompt, req.Stream, req.System)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(GenerateResponse{Text: "", Error: fmt.Sprintf("%v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GenerateResponse{Text: out})
}

// handleGetConfig handles the /api/config endpoint - get current configuration
func (sm *ServerManager) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sm.repl == nil {
		http.Error(w, "REPL not available", http.StatusInternalServerError)
		return
	}

	config := make(map[string]interface{})
	for _, key := range sm.repl.configOptions.GetKeys() {
		config[key] = sm.repl.configOptions.Get(key)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

// handleSetConfig handles the /api/config/set endpoint - set configuration
func (sm *ServerManager) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sm.repl == nil {
		http.Error(w, "REPL not available", http.StatusInternalServerError)
		return
	}

	var config map[string]string
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	for key, value := range config {
		if err := sm.repl.configOptions.Set(key, value); err != nil {
			http.Error(w, fmt.Sprintf("Error setting %s: %v", key, err), http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleGetProviderModels handles the /api/models/<provider> endpoint - get models for a specific provider
func (sm *ServerManager) handleGetProviderModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract provider from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/models/")
	if path == "" || path == r.URL.Path {
		http.Error(w, "Provider not specified", http.StatusBadRequest)
		return
	}

	provider := strings.ToLower(path)

	// Create a temporary config with the requested provider based on REPL settings
	baseCfg := sm.repl.buildLLMConfig()
	tempConfig := *baseCfg
	tempConfig.PROVIDER = provider

	// Create a temporary LLM client for the requested provider
	tempClient, err := llm.NewLLMClient(&tempConfig, context.Background())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client for provider %s: %v", provider, err), http.StatusInternalServerError)
		return
	}

	// Get models for the requested provider
	models, err := tempClient.ListModels()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list models for provider %s: %v", provider, err), http.StatusInternalServerError)
		return
	}

	// Convert to simple format for web interface
	type SimpleModel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	var simpleModels []SimpleModel
	for _, model := range models {
		simpleModels = append(simpleModels, SimpleModel{
			ID:   model.ID,
			Name: model.Name,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"provider": provider,
		"models":   simpleModels,
	})
}

// handleServeCommand handles the /serve command
func (r *REPL) handleServeCommand(args []string) (string, error) {
	if len(args) < 2 {
		// Show current status
		var output strings.Builder
		if serverManager == nil {
			output.WriteString("Server not initialized\r\n")
			return output.String(), nil
		}
		output.WriteString(fmt.Sprintf("Server status: %s\r\n", serverManager.GetStatusString()))
		if serverManager.GetStatus() == ServerRunning {
			output.WriteString(fmt.Sprintf("Listening on: %s\r\n", serverManager.listenAddr))
		}
		return output.String(), nil
	}

	action := args[1]

	switch action {
	case "start":
		if serverManager != nil && serverManager.GetStatus() == ServerRunning {
			return "Server is already running\r\n", nil
		}

		// Get listen address from config
		listenAddr := r.configOptions.Get("http.listen")
		if listenAddr == "" {
			listenAddr = "0.0.0.0:9000"
		}

		// Create server manager if it doesn't exist
		if serverManager == nil {
			config := r.buildLLMConfig()
			wwwRoot := r.configOptions.Get("http.wwwroot")

			// Resolve wwwroot to an absolute path
			if wwwRoot != "" && !filepath.IsAbs(wwwRoot) {
				// Get the executable directory as the base for relative paths
				execPath, err := os.Executable()
				if err == nil {
					realPath, err := filepath.EvalSymlinks(execPath)
					if err != nil {
						realPath = execPath
					}
					execDir := filepath.Dir(realPath)
					wwwRoot = filepath.Join(execDir, wwwRoot)
				}
			}

			serverManager = NewServerManager(config, listenAddr, wwwRoot, r)
		}

		// Start the server
		if err := serverManager.Start(); err != nil {
			return "", fmt.Errorf("failed to start server: %v", err)
		}

		return fmt.Sprintf("Server started on http://%s\r\n", listenAddr), nil

	case "stop":
		if serverManager == nil || serverManager.GetStatus() != ServerRunning {
			return "Server is not running\r\n", nil
		}

		if err := serverManager.Stop(); err != nil {
			return "", fmt.Errorf("failed to stop server: %v", err)
		}

		return "", nil

	default:
		return fmt.Sprintf("Unknown action: %s\r\nAvailable actions: start, stop\r\n", action), nil
	}
}
