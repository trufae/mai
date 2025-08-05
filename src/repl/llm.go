package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Use Message type from ai.go

// LLMProvider is a generic interface for all LLM providers
type LLMProvider interface {
	// SendMessage sends a message to the LLM and returns the response
	SendMessage(ctx context.Context, messages []Message, stream bool) (string, error)

	// GetName returns the name of the provider
	GetName() string

	// ListModels returns a list of available models for this provider
	ListModels(ctx context.Context) ([]Model, error)
}

/*
// sendOpenAIWithImages injects image data URIs into user messages for OpenAI vision-enabled models
func (c *LLMClient) sendOpenAIWithImages(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	// Prepend each image as a markdown image message before the rest
	for _, uri := range images {
		messages = append([]Message{{Role: "user", Content: fmt.Sprintf("![image](%s)", uri)}}, messages...)
	}
	// Delegate to the regular SendMessage path
	return c.provider.SendMessage(ctx, messages, stream)
}
*/

type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

func (c *LLMClient) sendOpenAIWithImages(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	// Add image blocks
	var blocks []ContentBlock
	for _, uri := range images {
		blocks = append(blocks, ContentBlock{
			Type: "image_url",
			ImageURL: &struct {
				URL string `json:"url"`
			}{URL: uri},
		})
	}

	// Add image content as one user message
	if len(blocks) > 0 {
		imageMessage := Message{
			Role:    "user",
			Content: blocks,
		}
		messages = append([]Message{imageMessage}, messages...)
	}

	return c.provider.SendMessage(ctx, messages, stream)
}

/*
// workaround hack
func (c *LLMClient) sendOpenAIWithImages(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	// Build a single markdown string with all image URLs
	var imageMarkdown string
	for _, uri := range images {
		imageMarkdown += fmt.Sprintf("![image](%s)\n", uri)
	}

	if imageMarkdown != "" {
		// Insert image markdown as a new user message
		imageMsg := Message{
			Role:    "user",
			Content: imageMarkdown,
		}
		messages = append([]Message{imageMsg}, messages...)
	}

	return c.provider.SendMessage(ctx, messages, stream)
}
*/

// LLMResponse is a generic response handler for both streaming and non-streaming responses
type LLMResponse struct {
	Text string
	Err  error
}

// Model represents information about an available model from a provider
type Model struct {
	ID          string `json:"id""`          // Model identifier
	Name        string `json:"name""`        // Human-readable name (may be the same as ID)
	Description string `json:"description""` // Optional model description
	Provider    string `json:"provider""`    // The provider this model belongs to
}

// LLMClient manages interactions with LLM providers
type LLMClient struct {
	config         *Config
	provider       LLMProvider
	responseCancel func() // Function to cancel the current response
}

// ListModelsResult contains the list of available models with optional error
type ListModelsResult struct {
	Models []Model
	Error  error
}

// NewLLMClient creates a new LLM client for the specified provider
func NewLLMClient(config *Config) (*LLMClient, error) {
	provider, err := createProvider(config)
	if err != nil {
		return nil, err
	}

	return &LLMClient{
		config:         config,
		provider:       provider,
		responseCancel: func() {}, // Initialize with no-op function
	}, nil
}

// newContext returns a cancellable context carrying the client config.
func (c *LLMClient) newContext() (context.Context, context.CancelFunc) {
	ctx := context.WithValue(context.Background(), "config", c.config)
	ctx, cancel := context.WithCancel(ctx)
	c.responseCancel = cancel
	return ctx, cancel
}

// printScissors outputs separators if enabled in config.
func (c *LLMClient) printScissors() {
	if c.config.ShowScissors {
		fmt.Print("\n\r------------8<------------\n\r")
	}
}

// createProvider instantiates the appropriate provider based on config
func createProvider(config *Config) (LLMProvider, error) {
	provider := strings.ToLower(config.PROVIDER)

	switch provider {
	case "ollama":
		return NewOllamaProvider(config), nil
	case "openai":
		return NewOpenAIProvider(config), nil
	case "claude":
		return NewClaudeProvider(config), nil
	case "gemini", "google":
		return NewGeminiProvider(config), nil
	case "mistral":
		return NewMistralProvider(config), nil
	case "deepseek":
		return NewDeepSeekProvider(config), nil
	case "bedrock", "aws":
		return NewBedrockProvider(config), nil
	case "openapi":
		return NewOpenAPIProvider(config), nil
	default:
		// Default to Claude if unknown provider
		return NewClaudeProvider(config), nil
	}
}

// SendMessage sends a message to the LLM and handles the response
func (c *LLMClient) SendMessage(messages []Message, stream bool) (string, error) {
	return c.SendMessageWithImages(messages, stream, nil)
}

// ListModels returns a list of available models for the current provider
func (c *LLMClient) ListModels() ([]Model, error) {
	ctx, cancel := c.newContext()
	defer cancel()
	return c.provider.ListModels(ctx)
}

// InterruptResponse cancels the current LLM response if one is being generated
func (c *LLMClient) InterruptResponse() {
	if c.responseCancel != nil {
		c.responseCancel()
	}
}

// SendMessageWithImages sends a message with optional images to the LLM and handles the response
func (c *LLMClient) SendMessageWithImages(messages []Message, stream bool, images []string) (string, error) {
	ctx, cancel := c.newContext()
	defer cancel()

	c.printScissors()
	var response string
	var err error

	if len(images) > 0 {
		switch strings.ToLower(c.config.PROVIDER) {
		case "ollama":
			response, err = c.sendOllamaWithImages(ctx, messages, stream && !c.config.NoStream, images)
		case "openai":
			response, err = c.sendOpenAIWithImages(ctx, messages, stream && !c.config.NoStream, images)
		default:
			response, err = c.provider.SendMessage(ctx, messages, stream && !c.config.NoStream)
		}
	} else {
		response, err = c.provider.SendMessage(ctx, messages, stream && !c.config.NoStream)
	}

	c.printScissors()
	return response, err
}

// sendOllamaWithImages sends a message with images to Ollama
func (c *LLMClient) sendOllamaWithImages(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	// Build request JSON, injecting only raw base64 images into the first user message
	var rawImages []string
	for _, uri := range images {
		if idx := strings.Index(uri, ","); idx != -1 && strings.HasPrefix(uri, "data:") {
			rawImages = append(rawImages, uri[idx+1:])
		} else {
			rawImages = append(rawImages, uri)
		}
	}
	var apiMessages []map[string]interface{}
	for i, m := range messages {
		msg := map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		}
		if i == len(messages)-1 && len(rawImages) > 0 {
			msg["images"] = rawImages
		}
		apiMessages = append(apiMessages, msg)
	}
	// fmt.Println(apiMessages)

	request := map[string]interface{}{
		"stream":   stream,
		"model":    c.config.OllamaModel,
		"messages": apiMessages,
	}
	if c.config.options != nil && c.config.options.GetBool("deterministic") {
		request["options"] = map[string]float64{
			"repeat_last_n":  0,
			"top_p":          0.0,
			"top_k":          1.0,
			"temperature":    0.0,
			"repeat_penalty": 1.0,
			"seed":           123,
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("http://%s:%s/api/chat", c.config.OllamaHost, c.config.OllamaPort)
	if c.config.BaseURL != "" {
		url = strings.TrimRight(c.config.BaseURL, "/") + "/api/chat"
	}

	if stream {
		provider := NewOllamaProvider(c.config)
		return llmMakeStreamingRequest(ctx, "POST", url, headers, jsonData, provider.parseStream)
	}

	respBody, err := llmMakeRequest(ctx, "POST", url, headers, jsonData)
	if err != nil {
		fmt.Println(string(respBody))
		return "", err
	}

	var response struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	return response.Message.Content, nil
}

// ExtractSystemPrompt extracts a system prompt from the input if present
func ExtractSystemPrompt(input string) (string, string) {
	systemPrompt := ""
	userPrompt := input

	// Simplified parsing to extract system prompt if it's at the beginning
	if strings.HasPrefix(input, "<system>\n") {
		parts := strings.SplitN(input, "</system>\n", 2)
		if len(parts) == 2 {
			systemPrompt = strings.TrimPrefix(parts[0], "<system>\n")
			userPrompt = parts[1]
		}
	}

	return systemPrompt, userPrompt
}

// PrepareMessages creates a message array with optional system prompt
func PrepareMessages(input string) []Message {
	systemPrompt, userPrompt := ExtractSystemPrompt(input)

	messages := []Message{}

	// Add system message if present
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: userPrompt})

	return messages
}

// httpDo prepares and executes an HTTP request, shared by streaming and non-streaming.
func httpDo(ctx context.Context, method, url string, headers map[string]string, body []byte, stream bool) (*http.Response, error) {
	var req *http.Request
	var err error
	if ctx != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequest(method, url, bytes.NewBuffer(body))
	}
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if ctx != nil {
		if cfg, ok := ctx.Value("config").(*Config); ok && cfg.UserAgent != "" {
			req.Header.Set("User-Agent", cfg.UserAgent)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if stream {
		client.Timeout = 0
	}

	// Set up a channel to signal when the request is done
	done := make(chan struct{})

	// Create a goroutine to check for context cancellation
	var cancelOnce sync.Once
	go func() {
		// Only proceed if context is not nil
		if ctx == nil {
			return
		}

		// Wait for either context cancellation or request completion
		select {
		case <-ctx.Done():
			// Cancel the request if context is canceled
			cancelOnce.Do(func() {
				// Transport.CancelRequest was deprecated in Go 1.5, but client.Transport.CancelRequest
				// is still usable. However, the preferred method is to use request contexts.
				// This is an extra safety measure in case the context cancellation
				// doesn't propagate quickly enough.
				transport, ok := client.Transport.(*http.Transport)
				if ok && transport != nil {
					transport.CancelRequest(req)
				}
			})
		case <-done:
			// Request completed normally, do nothing
		}
	}()

	// Execute the request
	resp, err := client.Do(req)

	// Signal that the request is done
	close(done)

	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if stream {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
		}
		fmt.Fprintf(os.Stderr, "Error: Non-200 status code: %d %s\n", resp.StatusCode, resp.Status)
	}
	return resp, nil
}

func llmMakeRequest(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// llmMakeStreamingRequest is a utility function for making streaming HTTP requests (renamed to avoid conflict)
func llmMakeStreamingRequest(ctx context.Context, method, url string, headers map[string]string,
	body []byte, parser func(io.Reader) (string, error)) (string, error) {
	resp, err := httpDo(ctx, method, url, headers, body, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parser(resp.Body)
}

// buildURL constructs a full URL using baseURL override, defaultURL, or host/port and path suffix
func buildURL(defaultURL, baseURL, host, port, suffix string) string {
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/") + suffix
	}
	if defaultURL != "" {
		return defaultURL
	}
	return fmt.Sprintf("http://%s:%s%s", host, port, suffix)
}

// ==================== PROVIDER IMPLEMENTATIONS ====================

// OllamaProvider implements the LLM provider interface for Ollama
type OllamaProvider struct {
	config *Config
}

// OllamaModelsResponse is the response structure for Ollama model list endpoint
type OllamaModelsResponse struct {
	Models []struct {
		Name     string `json:"name""`
		Digest   string `json:"digest""`
		Size     int64  `json:"size""`
		Modified int64  `json:"modified""`
	} `json:"models""`
}

func NewOllamaProvider(config *Config) *OllamaProvider {
	return &OllamaProvider{
		config: config,
	}
}

func (p *OllamaProvider) GetName() string {
	return "Ollama"
}

func (p *OllamaProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise construct from host/port
	url := fmt.Sprintf("http://%s:%s/api/tags", p.config.OllamaHost, p.config.OllamaPort)
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the tags endpoint
		url = strings.TrimRight(p.config.BaseURL, "/") + "/api/tags"
	}

	headers := map[string]string{}

	respBody, err := llmMakeRequest(ctx, "GET", url, headers, nil)
	if err != nil {
		return nil, err
	}
	if string(respBody)[0] != "{"[0] {
		return nil, fmt.Errorf("failed %v", string(respBody))
	}

	var ollamaResp OllamaModelsResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return nil, err
	}

	models := make([]Model, 0, len(ollamaResp.Models))
	for _, m := range ollamaResp.Models {
		// Convert size to a human-readable format for the description
		sizeDesc := ""
		if m.Size > 0 {
			size := float64(m.Size)
			unit := "B"
			if size >= 1024*1024*1024 {
				size /= 1024 * 1024 * 1024
				unit = "GB"
			} else if size >= 1024*1024 {
				size /= 1024 * 1024
				unit = "MB"
			} else if size >= 1024 {
				size /= 1024
				unit = "KB"
			}
			sizeDesc = fmt.Sprintf("Size: %.1f %s", size, unit)
		}

		models = append(models, Model{
			ID:          m.Name,
			Name:        m.Name,
			Provider:    "ollama",
			Description: sizeDesc,
		})
	}

	return models, nil
}

func (p *OllamaProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		Stream   bool               `json:"stream""`
		Model    string             `json:"model""`
		Messages []Message          `json:"messages""`
		Options  map[string]float64 `json:"options,omitempty""`
	}{
		Stream:   stream,
		Model:    p.config.OllamaModel,
		Messages: messages,
	}

	// Apply deterministic settings if enabled
	if p.config.options != nil && p.config.options.GetBool("deterministic") {
		request.Options = map[string]float64{
			"repeat_last_n":  0,
			"top_p":          0.0,
			"top_k":          1.0,
			"temperature":    0.0,
			"repeat_penalty": 1.0,
			"seed":           123,
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Use the configured base URL if available, otherwise construct from host/port
	url := fmt.Sprintf("http://%s:%s/api/chat", p.config.OllamaHost, p.config.OllamaPort)
	if p.config.BaseURL != "" {
		url = strings.TrimRight(p.config.BaseURL, "/") + "/api/chat"
	}

	if stream {
		return llmMakeStreamingRequest(ctx, "POST", url, headers, jsonData, p.parseStream)
	}

	respBody, err := llmMakeRequest(ctx, "POST", url, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Message struct {
			Content string `json:"content""`
		} `json:"message""`
		Error string `json:"error,omitempty""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}
	if response.Error != "" {
		return "", fmt.Errorf(response.Error)
	}

	// Return raw content - newline conversion happens in the REPL
	return response.Message.Content, nil
}

func (p *OllamaProvider) parseStream(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder

	// Check if markdown is enabled
	markdownEnabled := false
	if p.config.options != nil {
		markdownEnabled = p.config.options.GetBool("markdown")
	}

	// Reset the stream renderer if markdown is enabled
	if markdownEnabled {
		ResetStreamRenderer()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var response struct {
			Message struct {
				Content string `json:"content""`
			} `json:"message""`
			Done bool `json:"done""`
		}

		if err := json.Unmarshal([]byte(line), &response); err != nil {
			continue
		}

		// Format content based on markdown setting
		content := response.Message.Content
		if !markdownEnabled {
			// Standard formatting - just replace newlines for terminal display
			content = strings.ReplaceAll(content, "\n", "\n\r")
		} else {
			// Format the content using our streaming-friendly formatter
			content = FormatStreamingChunk(content, markdownEnabled)
		}
		fmt.Print(content)
		fullResponse.WriteString(response.Message.Content)

		if response.Done {
			break
		}
	}

	fmt.Println()

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			fmt.Print(final)
		}
	}

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}

// OpenAIProvider implements the LLM provider interface for OpenAI
type OpenAIProvider struct {
	config *Config
}

// OpenAIModelsResponse is the response structure for OpenAI model list endpoint
type OpenAIModelsResponse struct {
	Object string `json:"object""`
	Data   []struct {
		ID      string `json:"id""`
		Object  string `json:"object""`
		Created int64  `json:"created""`
		OwnedBy string `json:"owned_by""`
	} `json:"data""`
}

func NewOpenAIProvider(config *Config) *OpenAIProvider {
	return &OpenAIProvider{
		config: config,
	}
}

func (p *OpenAIProvider) GetName() string {
	return "OpenAI"
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Build models endpoint URL
	apiURL := buildURL("https://api.openai.com/v1/models", p.config.BaseURL, "", "", "/models")

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.config.OpenAIKey,
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
	}

	var openaiResp OpenAIModelsResponse
	if err := json.Unmarshal(respBody, &openaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v, raw: %s", err, string(respBody))
	}

	// Filter out non-chat models and sort by ID
	chatModels := make([]Model, 0, len(openaiResp.Data))
	for _, m := range openaiResp.Data {
		chatModels = append(chatModels, Model{
			ID:          m.ID,
			Name:        m.ID,
			Provider:    "openai",
			Description: "Owner: " + m.OwnedBy,
		})
	}

	// Sort models alphabetically by ID
	sort.Slice(chatModels, func(i, j int) bool {
		return chatModels[i].ID < chatModels[j].ID
	})

	return chatModels, nil
}

func (p *OpenAIProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := map[string]interface{}{
		"model":    p.config.OpenAIModel,
		"messages": messages,
	}

	if stream {
		request["stream"] = true
	}

	// Apply deterministic settings if enabled
	if p.config.options != nil && p.config.options.GetBool("deterministic") {
		// Skip for o4 and o1 models which don't support these parameters
		modelName := strings.ToLower(p.config.OpenAIModel)
		if !strings.HasPrefix(modelName, "o4") && !strings.HasPrefix(modelName, "o1") {
			request["temperature"] = 0
			request["top_p"] = 0
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.config.OpenAIKey,
	}

	// Build chat completions endpoint URL
	apiURL := buildURL("https://api.openai.com/v1/chat/completions", p.config.BaseURL, "", "", "/chat/completions")

	if stream {
		return llmMakeStreamingRequest(ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader) (string, error) {
				return p.parseStream(r)
			})
	}

	respBody, err := llmMakeRequest(ctx, "POST", apiURL,
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content""`
			} `json:"message""`
		} `json:"choices""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	if len(response.Choices) > 0 {
		// Return raw content - newline conversion happens in the REPL
		return response.Choices[0].Message.Content, nil
	}
	fmt.Println(string(respBody))

	return "", fmt.Errorf("no content in response")
}

func (p *OpenAIProvider) parseStream(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder

	// Check if markdown is enabled
	markdownEnabled := false
	if p.config.options != nil {
		markdownEnabled = p.config.options.GetBool("markdown")
	}

	// Reset the stream renderer if markdown is enabled
	if markdownEnabled {
		ResetStreamRenderer()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var response struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content""`
				} `json:"delta""`
			} `json:"choices""`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			content := response.Choices[0].Delta.Content

			// Format the content using our streaming-friendly formatter
			content = FormatStreamingChunk(content, markdownEnabled)

			fmt.Print(content)
			fullResponse.WriteString(response.Choices[0].Delta.Content)
		}
	}

	fmt.Println()

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			fmt.Print(final)
		}
	}

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}

// ClaudeProvider implements the LLM provider interface for Claude
type ClaudeProvider struct {
	config *Config
}

// ClaudeModelsResponse is the response structure for Claude model list endpoint
type ClaudeModelsResponse struct {
	Object string `json:"object""`
	Data   []struct {
		ID            string `json:"id""`
		Name          string `json:"name""`
		Description   string `json:"description""`
		MaxTokens     int    `json:"max_tokens,omitempty""`
		ContextWindow int    `json:"context_window,omitempty""`
	} `json:"data""`
}

func NewClaudeProvider(config *Config) *ClaudeProvider {
	return &ClaudeProvider{
		config: config,
	}
}

func (p *ClaudeProvider) GetName() string {
	return "Claude"
}

func (p *ClaudeProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.anthropic.com/v1/models"
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the models endpoint
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/models"
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": "2023-06-01",
		"x-api-key":         p.config.ClaudeKey,
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
	}

	var claudeResp ClaudeModelsResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to parse Claude response: %v, raw: %s", err, string(respBody))
	}

	models := make([]Model, 0, len(claudeResp.Data))
	for _, m := range claudeResp.Data {
		description := m.Description
		if m.ContextWindow > 0 {
			description += fmt.Sprintf(" (Context: %dk tokens)", m.ContextWindow/1000)
		} else if m.MaxTokens > 0 {
			description += fmt.Sprintf(" (Max tokens: %d)", m.MaxTokens)
		}

		models = append(models, Model{
			ID:          m.ID,
			Name:        m.Name,
			Description: description,
			Provider:    "claude",
		})
	}

	return models, nil
}

func (p *ClaudeProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := map[string]interface{}{
		"model":      p.config.ClaudeModel,
		"max_tokens": 5128,
		"messages":   messages,
	}

	if stream {
		request["stream"] = true
	}

	// Apply deterministic settings if enabled
	if p.config.options != nil && p.config.options.GetBool("deterministic") {
		request["temperature"] = 0
		request["top_p"] = 0
		request["top_k"] = 1
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": "2023-06-01",
		"x-api-key":         p.config.ClaudeKey,
	}

	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.anthropic.com/v1/messages"
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/v1/messages"
	}

	if stream {
		return llmMakeStreamingRequest(ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader) (string, error) {
				return p.parseStream(r)
			})
	}

	respBody, err := llmMakeRequest(ctx, "POST", apiURL,
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Content []struct {
			Text string `json:"text""`
		} `json:"content""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	if len(response.Content) > 0 {
		// Return raw content - newline conversion happens in the REPL
		return response.Content[0].Text, nil
	}

	return "", fmt.Errorf("no content in response")
}

func (p *ClaudeProvider) parseStream(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder

	// Check if markdown is enabled
	markdownEnabled := false
	if p.config.options != nil {
		markdownEnabled = p.config.options.GetBool("markdown")
	}

	// Reset the stream renderer if markdown is enabled
	if markdownEnabled {
		ResetStreamRenderer()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var response struct {
			Type  string `json:"type""`
			Delta struct {
				Text string `json:"text""`
			} `json:"delta""`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if response.Type == "content_block_delta" && response.Delta.Text != "" {
			content := response.Delta.Text

			// Format the content using our streaming-friendly formatter
			content = FormatStreamingChunk(content, markdownEnabled)

			fmt.Print(content)
			fullResponse.WriteString(response.Delta.Text)
		}
	}

	fmt.Println()

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			fmt.Print(final)
		}
	}

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}

// GeminiProvider implements the LLM provider interface for Google's Gemini
type GeminiProvider struct {
	config *Config
}

func NewGeminiProvider(config *Config) *GeminiProvider {
	return &GeminiProvider{
		config: config,
	}
}

func (p *GeminiProvider) GetName() string {
	return "Gemini"
}

func (p *GeminiProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", p.config.GeminiKey)
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the models endpoint
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/models?key=" + p.config.GeminiKey
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// First try the API endpoint
	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)

	// If API call fails or we don't have a key, fall back to hardcoded models
	if err != nil || p.config.GeminiKey == "" {
		// Gemini doesn't have a consistently available models listing endpoint
		// Return hardcoded list of common Gemini models
		return []Model{
			{
				ID:          "gemini-1.5-pro",
				Name:        "Gemini 1.5 Pro",
				Description: "Advanced large multimodal model with broader capabilities",
				Provider:    "gemini",
			},
			{
				ID:          "gemini-1.5-flash",
				Name:        "Gemini 1.5 Flash",
				Description: "Faster, more efficient multimodal model",
				Provider:    "gemini",
			},
			{
				ID:          "gemini-1.0-pro",
				Name:        "Gemini 1.0 Pro",
				Description: "Original Gemini professional model",
				Provider:    "gemini",
			},
		}, nil
	}

	// Parse response if we got one
	type GeminiModelsResponse struct {
		Models []struct {
			Name        string   `json:"name""`
			DisplayName string   `json:"displayName""`
			Description string   `json:"description""`
			Versions    []string `json:"supportedGenerationMethods,omitempty""`
		} `json:"models""`
	}

	var geminiResp GeminiModelsResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		// If JSON parsing fails, fall back to hardcoded models
		return []Model{
			{
				ID:          "gemini-1.5-pro",
				Name:        "Gemini 1.5 Pro",
				Description: "Advanced large multimodal model with broader capabilities",
				Provider:    "gemini",
			},
			{
				ID:          "gemini-1.5-flash",
				Name:        "Gemini 1.5 Flash",
				Description: "Faster, more efficient multimodal model",
				Provider:    "gemini",
			},
			{
				ID:          "gemini-1.0-pro",
				Name:        "Gemini 1.0 Pro",
				Description: "Original Gemini professional model",
				Provider:    "gemini",
			},
		}, nil
	}

	models := make([]Model, 0, len(geminiResp.Models))
	for _, m := range geminiResp.Models {
		// Extract model ID from name (which is typically a path)
		parts := strings.Split(m.Name, "/")
		modelID := parts[len(parts)-1]

		// Skip non-Gemini models
		if !strings.Contains(strings.ToLower(modelID), "gemini") {
			continue
		}

		models = append(models, Model{
			ID:          modelID,
			Name:        m.DisplayName,
			Description: m.Description,
			Provider:    "gemini",
		})
	}

	if len(models) == 0 {
		// If no models were found, fall back to hardcoded models
		return []Model{
			{
				ID:          "gemini-1.5-pro",
				Name:        "Gemini 1.5 Pro",
				Description: "Advanced large multimodal model with broader capabilities",
				Provider:    "gemini",
			},
			{
				ID:          "gemini-1.5-flash",
				Name:        "Gemini 1.5 Flash",
				Description: "Faster, more efficient multimodal model",
				Provider:    "gemini",
			},
			{
				ID:          "gemini-1.0-pro",
				Name:        "Gemini 1.0 Pro",
				Description: "Original Gemini professional model",
				Provider:    "gemini",
			},
		}, nil
	}

	return models, nil
}

func (p *GeminiProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	// Gemini currently doesn't use message structure like OpenAI, so we need to concat messages
	content := ""
	for _, msg := range messages {
		if msg.Role == "system" {
			content += "System: " + msg.Content.(string) + "\n\n"
		} else {
			content += msg.Content.(string)
		}
	}

	request := struct {
		Contents []struct {
			Parts []struct {
				Text string `json:"text""`
			} `json:"parts""`
		} `json:"contents""`
		GenerationConfig *struct {
			Temperature float64 `json:"temperature,omitempty""`
			TopP        float64 `json:"topP,omitempty""`
			TopK        int     `json:"topK,omitempty""`
		} `json:"generationConfig,omitempty""`
	}{
		Contents: []struct {
			Parts []struct {
				Text string `json:"text""`
			} `json:"parts""`
		}{
			{
				Parts: []struct {
					Text string `json:"text""`
				}{
					{
						Text: content,
					},
				},
			},
		},
	}

	// Apply deterministic settings if enabled
	if p.config.options != nil && p.config.options.GetBool("deterministic") {
		request.GenerationConfig = &struct {
			Temperature float64 `json:"temperature,omitempty""`
			TopP        float64 `json:"topP,omitempty""`
			TopK        int     `json:"topK,omitempty""`
		}{
			Temperature: 0.0,
			TopP:        1.0,
			TopK:        1,
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s",
		p.config.GeminiKey)
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + fmt.Sprintf("/v1beta/models/%s:generateContent?key=%s",
			p.config.GeminiModel, p.config.GeminiKey)
	}

	// Gemini doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text""`
				} `json:"parts""`
			} `json:"content""`
		} `json:"candidates""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		// Return raw content - newline conversion happens in the REPL
		return response.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("no content in response")
}

func (p *GeminiProvider) parseStream(reader io.Reader) (string, error) {
	// Gemini streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for Gemini")
}

// MistralProvider implements the LLM provider interface for Mistral
type MistralProvider struct {
	config *Config
}

func NewMistralProvider(config *Config) *MistralProvider {
	return &MistralProvider{
		config: config,
	}
}

func (p *MistralProvider) GetName() string {
	return "Mistral"
}

func (p *MistralProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.mistral.ai/v1/models"
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the models endpoint
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/models"
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.MistralKey,
		"Content-Type":  "application/json",
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
	}

	// Mistral API returns richer model info than OpenAI format
	type MistralModelsResponse struct {
		Object string `json:"object""`
		Data   []struct {
			ID                  string `json:"id""`
			Name                string `json:"name,omitempty""`
			ContextLength       int    `json:"context_length,omitempty""`
			MaxCompletionTokens int    `json:"max_completion_tokens,omitempty""`
			Description         string `json:"description,omitempty""`
		} `json:"data""`
	}

	var mistralResp MistralModelsResponse
	if err := json.Unmarshal(respBody, &mistralResp); err != nil {
		// If parsing fails with the richer format, try the OpenAI format
		var openAIResp OpenAIModelsResponse
		if err2 := json.Unmarshal(respBody, &openAIResp); err2 != nil {
			return nil, fmt.Errorf("failed to parse response: %v, raw: %s", err, string(respBody))
		}

		// Use the OpenAI format
		models := make([]Model, 0, len(openAIResp.Data))
		for _, m := range openAIResp.Data {
			models = append(models, Model{
				ID:       m.ID,
				Name:     m.ID,
				Provider: "mistral",
			})
		}
		return models, nil
	}

	models := make([]Model, 0, len(mistralResp.Data))
	for _, m := range mistralResp.Data {
		// Add context window info to description if available
		description := m.Description
		if m.ContextLength > 0 {
			if description != "" {
				description += " - "
			}
			description += fmt.Sprintf("Context: %dk tokens", m.ContextLength/1000)
		}

		models = append(models, Model{
			ID:          m.ID,
			Name:        m.Name,
			Description: description,
			Provider:    "mistral",
		})
	}

	return models, nil
}

func (p *MistralProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		Model       string    `json:"model""`
		Messages    []Message `json:"messages""`
		MaxTokens   int       `json:"max_tokens""`
		Stream      bool      `json:"stream,omitempty""`
		N           int       `json:"n,omitempty""`
		TopP        float64   `json:"top_p,omitempty""`
		RandomSeed  int       `json:"random_seed,omitempty""`
		Temperature float64   `json:"temperature,omitempty""`
	}{
		Model:     p.config.MistralModel,
		Messages:  messages,
		MaxTokens: 5128,
		Stream:    stream,
	}

	// Apply deterministic settings if enabled
	if p.config.options != nil && p.config.options.GetBool("deterministic") {
		request.N = 1
		request.TopP = 0.001
		request.RandomSeed = 1
		request.Temperature = 0.001
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.MistralKey,
		"Content-Type":  "application/json",
	}

	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.mistral.ai/v1/chat/completions"
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/v1/chat/completions"
	}

	// Handle streaming if requested
	if stream {
		return llmMakeStreamingRequest(ctx, "POST", apiURL, headers, jsonData, p.parseStream)
	}

	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Message string `json:"message,omitempty"`
		Choices []struct {
			Message struct {
				Content string `json:"content""`
			} `json:"message""`
		} `json:"choices""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		// Debug response in case of error
		return "", fmt.Errorf("failed to parse response: %v, raw: %s", err, string(respBody))
	}

	if len(response.Choices) > 0 {
		// Return raw content - newline conversion happens in the REPL
		return response.Choices[0].Message.Content, nil
	}
	if response.Message != "" {
		return "", fmt.Errorf(response.Message)
	}
	return "", fmt.Errorf("no content in response")
}

func (p *MistralProvider) parseStream(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder

	// Check if markdown is enabled
	markdownEnabled := false
	if p.config.options != nil {
		markdownEnabled = p.config.options.GetBool("markdown")
	}

	// Reset the stream renderer if markdown is enabled
	if markdownEnabled {
		ResetStreamRenderer()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var response struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content""`
				} `json:"delta""`
			} `json:"choices""`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			content := response.Choices[0].Delta.Content

			// Format the content using our streaming-friendly formatter
			content = FormatStreamingChunk(content, markdownEnabled)

			fmt.Print(content)
			fullResponse.WriteString(response.Choices[0].Delta.Content)
		}
	}

	fmt.Println()

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			fmt.Print(final)
		}
	}

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}

// DeepSeekProvider implements the LLM provider interface for DeepSeek
type DeepSeekProvider struct {
	config *Config
}

func NewDeepSeekProvider(config *Config) *DeepSeekProvider {
	return &DeepSeekProvider{
		config: config,
	}
}

func (p *DeepSeekProvider) GetName() string {
	return "DeepSeek"
}

func (p *DeepSeekProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.deepseek.com/v1/models"
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the models endpoint
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/models"
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.DeepSeekKey,
		"Content-Type":  "application/json",
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)

	// If API call fails or no key, fall back to hardcoded values
	if err != nil || p.config.DeepSeekKey == "" {
		// DeepSeek doesn't have a well-documented model listing endpoint
		// Return hardcoded list of common DeepSeek models
		return []Model{
			{
				ID:          "deepseek-chat",
				Name:        "DeepSeek Chat",
				Description: "General purpose chat model",
				Provider:    "deepseek",
			},
			{
				ID:          "deepseek-coder",
				Name:        "DeepSeek Coder",
				Description: "Specialized model for code generation",
				Provider:    "deepseek",
			},
		}, nil
	}

	// Try parsing as OpenAI-compatible format
	var openaiResp OpenAIModelsResponse
	if err := json.Unmarshal(respBody, &openaiResp); err != nil {
		return []Model{
			{
				ID:          "deepseek-chat",
				Name:        "DeepSeek Chat",
				Description: "General purpose chat model",
				Provider:    "deepseek",
			},
			{
				ID:          "deepseek-coder",
				Name:        "DeepSeek Coder",
				Description: "Specialized model for code generation",
				Provider:    "deepseek",
			},
		}, nil
	}

	// Process the models data
	models := make([]Model, 0, len(openaiResp.Data))
	for _, m := range openaiResp.Data {
		models = append(models, Model{
			ID:          m.ID,
			Name:        m.ID,
			Description: m.OwnedBy,
			Provider:    "deepseek",
		})
	}

	// If no models found, return hardcoded ones
	if len(models) == 0 {
		return []Model{
			{
				ID:          "deepseek-chat",
				Name:        "DeepSeek Chat",
				Description: "General purpose chat model",
				Provider:    "deepseek",
			},
			{
				ID:          "deepseek-coder",
				Name:        "DeepSeek Coder",
				Description: "Specialized model for code generation",
				Provider:    "deepseek",
			},
		}, nil
	}

	return models, nil
}

func (p *DeepSeekProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		Model    string    `json:"model""`
		Stream   string    `json:"stream""`
		Messages []Message `json:"messages""`
	}{
		Model:    "deepseek-chat",
		Stream:   "false",
		Messages: messages,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.DeepSeekKey,
		"Content-Type":  "application/json",
	}

	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.deepseek.com/chat/completions"
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/chat/completions"
	}

	// DeepSeek doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest(ctx, "POST", apiURL,
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content""`
			} `json:"message""`
		} `json:"choices""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	if len(response.Choices) > 0 {
		// Return raw content - newline conversion happens in the REPL
		return response.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("no content in response")
}

func (p *DeepSeekProvider) parseStream(reader io.Reader) (string, error) {
	// DeepSeek streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for DeepSeek")
}

// BedrockProvider implements the LLM provider interface for AWS Bedrock
type BedrockProvider struct {
	config *Config
}

func NewBedrockProvider(config *Config) *BedrockProvider {
	return &BedrockProvider{
		config: config,
	}
}

func (p *BedrockProvider) GetName() string {
	return "Bedrock"
}

func (p *BedrockProvider) ListModels(ctx context.Context) ([]Model, error) {
	// For AWS Bedrock, we'd need to use the AWS SDK to list models properly
	// Since that would add a dependency, we'll use hardcoded models for now
	// Users can use any of these models or others by setting the BedrockModel config

	// Comprehensive list of models available through Bedrock
	return []Model{
		{
			ID:          "anthropic.claude-3-5-sonnet-v1",
			Name:        "Claude 3.5 Sonnet",
			Description: "Advanced Anthropic model via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "anthropic.claude-3-sonnet-v1",
			Name:        "Claude 3 Sonnet",
			Description: "Anthropic Claude 3 Sonnet via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "anthropic.claude-3-haiku-v1",
			Name:        "Claude 3 Haiku",
			Description: "Faster, more efficient Claude model via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "anthropic.claude-3-opus-v1",
			Name:        "Claude 3 Opus",
			Description: "Most powerful Claude model via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "meta.llama3-8b-instruct-v1",
			Name:        "Meta Llama 3 8B",
			Description: "Meta's Llama 3 8B model via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "meta.llama3-70b-instruct-v1",
			Name:        "Meta Llama 3 70B",
			Description: "Meta's Llama 3 70B model via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "amazon.titan-text-express-v1",
			Name:        "Amazon Titan Text Express",
			Description: "Amazon's lightweight text generation model",
			Provider:    "bedrock",
		},
		{
			ID:          "amazon.titan-text-premier-v1",
			Name:        "Amazon Titan Text Premier",
			Description: "Amazon's advanced text generation model",
			Provider:    "bedrock",
		},
		{
			ID:          "cohere.command-r-v1:0",
			Name:        "Cohere Command R",
			Description: "Cohere's reasoning-focused model via AWS Bedrock",
			Provider:    "bedrock",
		},
		{
			ID:          "cohere.command-light-v1:0",
			Name:        "Cohere Command Light",
			Description: "Cohere's efficient model via AWS Bedrock",
			Provider:    "bedrock",
		},
	}, nil
}

func (p *BedrockProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		ModelId         string `json:"modelId""`
		InferenceParams struct {
			MaxTokens   int     `json:"maxTokenCount""`
			Temperature float64 `json:"temperature""`
			TopP        float64 `json:"topP""`
		} `json:"inferenceParams""`
		Input struct {
			Messages []Message `json:"messages""`
		} `json:"input""`
	}{
		ModelId: p.config.BedrockModel,
		InferenceParams: struct {
			MaxTokens   int     `json:"maxTokenCount""`
			Temperature float64 `json:"temperature""`
			TopP        float64 `json:"topP""`
		}{
			MaxTokens:   5128,
			Temperature: 0.7,
			TopP:        0.9,
		},
		Input: struct {
			Messages []Message `json:"messages""`
		}{
			Messages: messages,
		},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	// Use the configured base URL if available, otherwise use the default AWS endpoint format
	apiURL := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke",
		p.config.BedrockRegion, p.config.BedrockModel)
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + fmt.Sprintf("/model/%s/invoke", p.config.BedrockModel)
	}

	headers := map[string]string{
		"Content-Type":       "application/json",
		"X-Amz-Access-Token": p.config.BedrockKey,
	}

	// Bedrock doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Output struct {
			Message struct {
				Content string `json:"content""`
			} `json:"message""`
		} `json:"output""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	// Return raw content - newline conversion happens in the REPL
	return response.Output.Message.Content, nil
}

func (p *BedrockProvider) parseStream(reader io.Reader) (string, error) {
	// Bedrock streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for Bedrock")
}

// OpenAPIProvider implements the LLM provider interface for OpenAPI
type OpenAPIProvider struct {
	config *Config
}

func NewOpenAPIProvider(config *Config) *OpenAPIProvider {
	return &OpenAPIProvider{
		config: config,
	}
}

func (p *OpenAPIProvider) GetName() string {
	return "OpenAPI"
}

func (p *OpenAPIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Try to query the OpenAPI server for available models
	apiURL := fmt.Sprintf("http://%s:%s/models", p.config.OpenAPIHost, p.config.OpenAPIPort)
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the models endpoint
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/models"
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Attempt to get models list
	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)

	// If the endpoint doesn't exist or returns an error, return default model
	if err != nil {
		return []Model{
			{
				ID:          "default",
				Name:        "Default Model",
				Description: "Default model for OpenAPI provider",
				Provider:    "openapi",
			},
		}, nil
	}

	// Try to parse response as a list of models
	var modelsList []string
	if err := json.Unmarshal(respBody, &modelsList); err == nil && len(modelsList) > 0 {
		models := make([]Model, 0, len(modelsList))
		for _, name := range modelsList {
			models = append(models, Model{
				ID:       name,
				Name:     name,
				Provider: "openapi",
			})
		}
		return models, nil
	}

	// Try to parse as a more complex response format with model objects
	type ModelObject struct {
		ID   string `json:"id""`
		Name string `json:"name,omitempty""`
	}
	var modelObjects []ModelObject
	if err := json.Unmarshal(respBody, &modelObjects); err == nil && len(modelObjects) > 0 {
		models := make([]Model, 0, len(modelObjects))
		for _, model := range modelObjects {
			name := model.Name
			if name == "" {
				name = model.ID
			}
			models = append(models, Model{
				ID:       model.ID,
				Name:     name,
				Provider: "openapi",
			})
		}
		return models, nil
	}

	// If we can't parse the response, return default model
	return []Model{
		{
			ID:          "default",
			Name:        "Default Model",
			Description: "Default model for OpenAPI provider",
			Provider:    "openapi",
		},
	}, nil
}

func (p *OpenAPIProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	// OpenAPI doesn't use message structure, so we need to concat messages
	content := ""
	for _, msg := range messages {
		if msg.Role == "system" {
			content += "System: " + msg.Content.(string) + "\n\n"
		} else {
			content += msg.Content.(string)
		}
	}

	request := struct {
		Prompt string `json:"prompt""`
	}{
		Prompt: content,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Use the configured base URL if available, otherwise construct from host/port
	apiURL := fmt.Sprintf("http://%s:%s/completion", p.config.OpenAPIHost, p.config.OpenAPIPort)
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/completion"
	}

	// OpenAPI doesn't support streaming in our implementation
	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Content string `json:"content""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	// Return raw content - newline conversion happens in the REPL
	return response.Content, nil
}

func (p *OpenAPIProvider) parseStream(reader io.Reader) (string, error) {
	// OpenAPI streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for OpenAPI")
}
