package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
func NewServerManager(config *llm.Config, listenAddr string) *ServerManager {
	return &ServerManager{
		status:     ServerStopped,
		config:     config,
		listenAddr: listenAddr,
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

	// Create LLM client
	client, err := llm.NewLLMClient(sm.config)
	if err != nil {
		sm.status = ServerStopped
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	sm.llmClient = client

	// Create HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", sm.handleModels)
	mux.HandleFunc("/v1/chat/completions", sm.handleChatCompletions)
	mux.HandleFunc("/health", sm.handleHealth)

	// Additional simplified endpoints
	mux.HandleFunc("/api/chat", sm.handleSimpleChat)
	mux.HandleFunc("/api/generate", sm.handleGenerate)

	sm.server = &http.Server{
		Addr:    sm.listenAddr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		sm.mu.Lock()
		sm.status = ServerRunning
		sm.mu.Unlock()

		fmt.Printf("Server started on %s\n", sm.listenAddr)
		if err := sm.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
			sm.mu.Lock()
			sm.status = ServerStopped
			sm.mu.Unlock()
		}
	}()

	return nil
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

// handleModels handles the /v1/models endpoint
func (sm *ServerManager) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get available models from the LLM client
	models, err := sm.llmClient.ListModels()
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
	response, err := sm.llmClient.SendMessage(messages, false, nil)
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
	// Send message to LLM
	response, err := sm.llmClient.SendMessage(messages, false, nil)
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

	// Build messages
	var messages []llm.Message
	if req.System != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: req.System,
		})
	}
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: req.Message,
	})

	// Send to LLM
	response, err := sm.llmClient.SendMessage(messages, false, nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SimpleChatResponse{
			Response: "",
			Error:    fmt.Sprintf("LLM error: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SimpleChatResponse{
		Response: response,
	})
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

	// Build messages
	var messages []llm.Message
	if req.System != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: req.System,
		})
	}
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: req.Prompt,
	})

	// Send to LLM
	response, err := sm.llmClient.SendMessage(messages, false, nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(GenerateResponse{
			Text:  "",
			Error: fmt.Sprintf("LLM error: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GenerateResponse{
		Text: response,
	})
}

// handleServeCommand handles the /serve command
func (r *REPL) handleServeCommand(args []string) error {
	if len(args) < 2 {
		// Show current status
		if serverManager == nil {
			fmt.Print("Server not initialized\r\n")
			return nil
		}
		fmt.Printf("Server status: %s\r\n", serverManager.GetStatusString())
		if serverManager.GetStatus() == ServerRunning {
			fmt.Printf("Listening on: %s\r\n", serverManager.listenAddr)
		}
		return nil
	}

	action := args[1]

	switch action {
	case "start":
		if serverManager != nil && serverManager.GetStatus() == ServerRunning {
			fmt.Print("Server is already running\r\n")
			return nil
		}

		// Get listen address from config
		listenAddr := r.configOptions.Get("listen")
		if listenAddr == "" {
			listenAddr = "0.0.0.0:9000"
		}

		// Create server manager if it doesn't exist
		if serverManager == nil {
			config := r.buildLLMConfig()
			serverManager = NewServerManager(config, listenAddr)
		}

		// Start the server
		if err := serverManager.Start(); err != nil {
			return fmt.Errorf("failed to start server: %v", err)
		}

		fmt.Printf("Server started on %s\r\n", listenAddr)
		return nil

	case "stop":
		if serverManager == nil || serverManager.GetStatus() != ServerRunning {
			fmt.Print("Server is not running\r\n")
			return nil
		}

		if err := serverManager.Stop(); err != nil {
			return fmt.Errorf("failed to stop server: %v", err)
		}

		return nil

	default:
		fmt.Printf("Unknown action: %s\r\n", action)
		fmt.Print("Available actions: start, stop\r\n")
		return nil
	}
}
