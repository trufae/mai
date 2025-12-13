package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

// Anthropic Messages API structures
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type AnthropicContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type AnthropicSystemMessage struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type AnthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type AnthropicMessagesRequest struct {
	Model       string                   `json:"model"`
	Messages    []AnthropicMessage       `json:"messages"`
	System      []AnthropicSystemMessage `json:"system,omitempty"`
	Tools       []AnthropicTool          `json:"tools,omitempty"`
	Metadata    map[string]interface{}   `json:"metadata,omitempty"`
	MaxTokens   int                      `json:"max_tokens"`
	Stream      bool                     `json:"stream,omitempty"`
	Temperature *float64                 `json:"temperature,omitempty"`
}

type AnthropicMessagesResponse struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Role         string             `json:"role"`
	Content      []AnthropicContent `json:"content"`
	Model        string             `json:"model"`
	StopReason   *string            `json:"stop_reason,omitempty"`
	StopSequence *string            `json:"stop_sequence,omitempty"`
	Usage        *AnthropicUsage    `json:"usage,omitempty"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type AnthropicCountTokensRequest struct {
	Model    string             `json:"model"`
	Messages []AnthropicMessage `json:"messages"`
	Tools    []AnthropicTool    `json:"tools,omitempty"`
}

type AnthropicCountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// Event logging structures
type EventLoggingRequest struct {
	Events []EventData `json:"events"`
}

type EventData struct {
	EventType string                 `json:"event_type"`
	EventData map[string]interface{} `json:"event_data"`
}

type EventLoggingResponse struct {
	Status string `json:"status"`
}

// handleAnthropicMessages handles the /v1/messages endpoint (Anthropic Messages API)
func (sm *ServerManager) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnthropicMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Convert Anthropic messages to internal format
	messages := make([]llm.Message, 0, len(req.Messages))
	for _, msg := range req.Messages {
		// Handle tool results (user messages with tool results)
		if msg.Role == "user" {
			if toolCallID, toolResult := extractToolResultFromContent(msg.Content); toolCallID != "" {
				// This is a tool result message
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    toolResult,
					ToolCallID: toolCallID,
				})
				continue
			}
		}

		// Extract text and tool calls from content
		text := extractTextFromContent(msg.Content)
		toolCalls := extractToolCallsFromContent(msg.Content)

		message := llm.Message{
			Role:    msg.Role,
			Content: text,
		}

		// Add tool calls if present
		if len(toolCalls) > 0 {
			message.ToolCalls = toolCalls
		}

		messages = append(messages, message)
	}

	// Handle system messages
	var systemPrompt strings.Builder
	for _, sys := range req.System {
		if sys.Type == "text" {
			if systemPrompt.Len() > 0 {
				systemPrompt.WriteString("\n")
			}
			systemPrompt.WriteString(sys.Text)
		}
	}

	// Convert Anthropic tools to OpenAI format
	var tools []llm.OpenAITool
	if req.Tools != nil && len(req.Tools) > 0 {
		tools = make([]llm.OpenAITool, len(req.Tools))
		for i, tool := range req.Tools {
			var parameters map[string]interface{}
			if paramsMap, ok := tool.InputSchema.(map[string]interface{}); ok {
				parameters = paramsMap
			}
			tools[i] = llm.OpenAITool{
				Type: "function",
				Function: llm.OpenAIToolFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  parameters,
				},
			}
		}
	}

	if req.Stream {
		sm.handleAnthropicStreamingMessages(w, r, messages, systemPrompt.String(), req, tools)
	} else {
		sm.handleAnthropicNonStreamingMessages(w, r, messages, systemPrompt.String(), req, tools)
	}
}

// handleAnthropicStreamingMessages handles streaming Anthropic messages
func (sm *ServerManager) handleAnthropicStreamingMessages(w http.ResponseWriter, r *http.Request, messages []llm.Message, systemPrompt string, req AnthropicMessagesRequest, tools []llm.OpenAITool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	client, err := sm.getLLMClient()
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", fmt.Sprintf(`{"error": {"message": "LLM init error: %v"}}`, err))
		return
	}

	// For now, simulate streaming with non-streaming response
	response, err := client.SendMessage(messages, false, nil, tools)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", fmt.Sprintf(`{"error": {"message": "LLM error: %v"}}`, err))
		return
	}

	// Send the response as a single message event
	resp := AnthropicMessagesResponse{
		ID:   fmt.Sprintf("msg_%d", time.Now().Unix()),
		Type: "message",
		Role: "assistant",
		Content: []AnthropicContent{
			{
				Type: "text",
				Text: response,
			},
		},
		Model:      req.Model,
		StopReason: stringPtr("end_turn"),
		Usage: &AnthropicUsage{
			InputTokens:  len(strings.Fields(strings.Join([]string{systemPrompt, fmt.Sprintf("%v", messages[0].Content)}, " "))),
			OutputTokens: len(strings.Fields(response)),
		},
	}

	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
	flusher.Flush()

	// Send completion event
	fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")
	flusher.Flush()
}

// handleAnthropicNonStreamingMessages handles non-streaming Anthropic messages
func (sm *ServerManager) handleAnthropicNonStreamingMessages(w http.ResponseWriter, r *http.Request, messages []llm.Message, systemPrompt string, req AnthropicMessagesRequest, tools []llm.OpenAITool) {
	client, err := sm.getLLMClient()
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM init error: %v", err), http.StatusInternalServerError)
		return
	}

	response, err := client.SendMessage(messages, false, nil, tools)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}

	resp := AnthropicMessagesResponse{
		ID:   fmt.Sprintf("msg_%d", time.Now().Unix()),
		Type: "message",
		Role: "assistant",
		Content: []AnthropicContent{
			{
				Type: "text",
				Text: response,
			},
		},
		Model:      req.Model,
		StopReason: stringPtr("end_turn"),
		Usage: &AnthropicUsage{
			InputTokens:  len(strings.Fields(strings.Join([]string{systemPrompt, fmt.Sprintf("%v", messages[0].Content)}, " "))),
			OutputTokens: len(strings.Fields(response)),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAnthropicCountTokens handles the /v1/messages/count_tokens endpoint
func (sm *ServerManager) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnthropicCountTokensRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Simple token counting - count words as approximation
	totalTokens := 0
	for _, msg := range req.Messages {
		totalTokens += countTokensFromContent(msg.Content)
	}

	resp := AnthropicCountTokensResponse{
		InputTokens: totalTokens,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAnthropicComplete handles the /v1/complete endpoint (Anthropic-compatible)
func (sm *ServerManager) handleAnthropicComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type AnthropicCompletionRequest struct {
		Model             string   `json:"model"`
		Prompt            string   `json:"prompt"`
		MaxTokensToSample int      `json:"max_tokens_to_sample,omitempty"`
		Temperature       float64  `json:"temperature,omitempty"`
		Stream            bool     `json:"stream,omitempty"`
		StopSequences     []string `json:"stop_sequences,omitempty"`
	}
	type AnthropicCompletionResponse struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		Completion string `json:"completion"`
		StopReason string `json:"stop_reason,omitempty"`
	}

	var req AnthropicCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	// Convert prompt into internal message
	messages := []llm.Message{{Role: "user", Content: req.Prompt}}

	client, err := sm.getLLMClient()
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM init error: %v", err), http.StatusInternalServerError)
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}
		response, err := client.SendMessage(messages, false, nil, nil)
		if err != nil {
			fmt.Fprintf(w, "data: [ERROR] %v\n\n", err)
			return
		}
		words := strings.Fields(response)
		for i, word := range words {
			chunk := word + " "
			resp := AnthropicCompletionResponse{
				ID:         "cmpl-" + fmt.Sprintf("%d", time.Now().Unix()),
				Model:      req.Model,
				Completion: chunk,
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
			if i == len(words)-1 {
				final := AnthropicCompletionResponse{
					ID:         "cmpl-" + fmt.Sprintf("%d", time.Now().Unix()),
					Model:      req.Model,
					Completion: "",
					StopReason: "stop",
				}
				data, _ := json.Marshal(final)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	// Non-streaming
	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}
	resp := AnthropicCompletionResponse{
		ID:         "cmpl-" + fmt.Sprintf("%d", time.Now().Unix()),
		Model:      req.Model,
		Completion: response,
		StopReason: "stop",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleEventLogging handles the /api/event_logging/batch endpoint
func (sm *ServerManager) handleEventLogging(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req EventLoggingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Log events (for now, just log to server logs)
	for _, event := range req.Events {
		log.Printf("Event logged: type=%s, data=%v", event.EventType, event.EventData)
	}

	resp := EventLoggingResponse{
		Status: "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// extractTextFromContent extracts text from Anthropic content field (can be string or array)
func extractTextFromContent(content interface{}) string {
	if content == nil {
		return ""
	}

	// If it's a string, return it directly
	if str, ok := content.(string); ok {
		return str
	}

	// If it's an array, extract text from content blocks (excluding tool_use and tool_result)
	if contentArray, ok := content.([]interface{}); ok {
		var text strings.Builder
		for _, item := range contentArray {
			if contentBlock, ok := item.(map[string]interface{}); ok {
				if contentType, exists := contentBlock["type"]; exists {
					if contentType == "text" {
						if contentText, exists := contentBlock["text"]; exists {
							if str, ok := contentText.(string); ok {
								text.WriteString(str)
							}
						}
					}
				}
			}
		}
		return text.String()
	}

	return ""
}

// extractToolCallsFromContent extracts tool calls from Anthropic content field
func extractToolCallsFromContent(content interface{}) []llm.ToolCall {
	var toolCalls []llm.ToolCall

	if content == nil {
		return toolCalls
	}

	// If it's an array, look for tool_use content blocks
	if contentArray, ok := content.([]interface{}); ok {
		for _, item := range contentArray {
			if contentBlock, ok := item.(map[string]interface{}); ok {
				if contentType, exists := contentBlock["type"]; exists && contentType == "tool_use" {
					if id, idExists := contentBlock["id"]; idExists {
						if name, nameExists := contentBlock["name"]; nameExists {
							if input, inputExists := contentBlock["input"]; inputExists {
								// Convert input to JSON string
								var argsStr string
								if inputBytes, err := json.Marshal(input); err == nil {
									argsStr = string(inputBytes)
								}

								toolCall := llm.ToolCall{
									ID:   fmt.Sprintf("%v", id),
									Type: "function",
									Function: llm.ToolCallFunction{
										Name:      fmt.Sprintf("%v", name),
										Arguments: argsStr,
									},
								}
								toolCalls = append(toolCalls, toolCall)
							}
						}
					}
				}
			}
		}
	}

	return toolCalls
}

// extractToolResultFromContent extracts tool result information from Anthropic content field
func extractToolResultFromContent(content interface{}) (string, string) {
	if content == nil {
		return "", ""
	}

	// If it's an array, look for tool_result content blocks
	if contentArray, ok := content.([]interface{}); ok {
		for _, item := range contentArray {
			if contentBlock, ok := item.(map[string]interface{}); ok {
				if contentType, exists := contentBlock["type"]; exists && contentType == "tool_result" {
					if toolCallID, idExists := contentBlock["tool_call_id"]; idExists {
						if resultContent, contentExists := contentBlock["content"]; contentExists {
							// Extract text from result content
							var resultText string
							if str, ok := resultContent.(string); ok {
								resultText = str
							} else if contentArray, ok := resultContent.([]interface{}); ok {
								// Handle array of content blocks in tool result
								var text strings.Builder
								for _, subItem := range contentArray {
									if subBlock, ok := subItem.(map[string]interface{}); ok {
										if subType, exists := subBlock["type"]; exists && subType == "text" {
											if subText, exists := subBlock["text"]; exists {
												if str, ok := subText.(string); ok {
													text.WriteString(str)
												}
											}
										}
									}
								}
								resultText = text.String()
							}
							return fmt.Sprintf("%v", toolCallID), resultText
						}
					}
				}
			}
		}
	}

	return "", ""
}

// countTokensFromContent counts tokens from Anthropic content field
func countTokensFromContent(content interface{}) int {
	text := extractTextFromContent(content)
	// Also count tool calls and results
	toolCalls := extractToolCallsFromContent(content)
	toolCallTokens := len(toolCalls) * 10 // Rough estimate for tool call overhead

	// Check for tool results
	if _, toolResult := extractToolResultFromContent(content); toolResult != "" {
		toolCallTokens += len(strings.Fields(toolResult))
	}

	return len(strings.Fields(text)) + toolCallTokens
}

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}
