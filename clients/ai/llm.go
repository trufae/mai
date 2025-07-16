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
	"strings"
	"time"
)

// Use Message type from ai.go

// LLMProvider is a generic interface for all LLM providers
type LLMProvider interface {
	// SendMessage sends a message to the LLM and returns the response
	SendMessage(ctx context.Context, messages []Message, stream bool) (string, error)
	
	// GetName returns the name of the provider
	GetName() string
}

// LLMResponse is a generic response handler for both streaming and non-streaming responses
type LLMResponse struct {
	Text string
	Err  error
}

// LLMClient manages interactions with LLM providers
type LLMClient struct {
	config   *Config
	provider LLMProvider
}

// NewLLMClient creates a new LLM client for the specified provider
func NewLLMClient(config *Config) (*LLMClient, error) {
	provider, err := createProvider(config)
	if err != nil {
		return nil, err
	}

	return &LLMClient{
		config:   config,
		provider: provider,
	}, nil
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
	// Create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Print scissors before the response if enabled
	if c.config.ShowScissors {
		fmt.Print("\r\n------------8<------------\r\n")
	}

	// Call the provider's SendMessage method
	response, err := c.provider.SendMessage(ctx, messages, stream && !c.config.NoStream)
	
	// We only need to convert newlines in the returned response string
	// The actual printing of response is handled in streaming functions
	// or in the REPL's sendToAI function for non-streaming mode
	
	// Print scissors after the response if enabled
	if c.config.ShowScissors {
		fmt.Print("\r\n------------8<------------\r\n")
	}
	
	return response, err
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

// llmMakeRequest is a utility function for making HTTP requests to APIs (renamed to avoid conflict)
func llmMakeRequest(method, url string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating HTTP request: %v\n", err)
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

//	fmt.Fprintf(os.Stderr, "Sending %s request to %s\n", method, url)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP request failed: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	// fmt.Fprintf(os.Stderr, "Response status code: %d\n", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Non-200 status code: %d %s\n", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response body: %v\n", err)
		return nil, err
	}

	return respBody, nil
}

// llmMakeStreamingRequest is a utility function for making streaming HTTP requests (renamed to avoid conflict)
func llmMakeStreamingRequest(ctx context.Context, method, url string, headers map[string]string, 
                         body []byte, parser func(io.Reader) (string, error)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 0} // No timeout for streaming
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return parser(resp.Body)
}

// ==================== PROVIDER IMPLEMENTATIONS ====================

// OllamaProvider implements the LLM provider interface for Ollama
type OllamaProvider struct {
	config *Config
}

func NewOllamaProvider(config *Config) *OllamaProvider {
	return &OllamaProvider{
		config: config,
	}
}

func (p *OllamaProvider) GetName() string {
	return "Ollama"
}

func (p *OllamaProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		Stream   bool      `json:"stream"`
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
	}{
		Stream:   stream,
		Model:    p.config.OllamaModel,
		Messages: messages,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("http://%s:%s/api/chat", p.config.OllamaHost, p.config.OllamaPort)

	if stream {
		return llmMakeStreamingRequest(ctx, "POST", url, headers, jsonData, p.parseStream)
	}

	respBody, err := llmMakeRequest("POST", url, headers, jsonData)
	if err != nil {
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

	// Return raw content - newline conversion happens in the REPL
	return response.Message.Content, nil
}

func (p *OllamaProvider) parseStream(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder
	
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var response struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}

		if err := json.Unmarshal([]byte(line), &response); err != nil {
			continue
		}

		// Replace newlines for terminal display
		content := strings.ReplaceAll(response.Message.Content, "\n", "\r\n")
		fmt.Print(content)
		fullResponse.WriteString(response.Message.Content)

		if response.Done {
			break
		}
	}

	fmt.Println()
	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}
	
	return fullResponse.String(), nil
}

// OpenAIProvider implements the LLM provider interface for OpenAI
type OpenAIProvider struct {
	config *Config
}

func NewOpenAIProvider(config *Config) *OpenAIProvider {
	return &OpenAIProvider{
		config: config,
	}
}

func (p *OpenAIProvider) GetName() string {
	return "OpenAI"
}

func (p *OpenAIProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := map[string]interface{}{
		"model":    p.config.OpenAIModel,
		"messages": messages,
	}
	
	if stream {
		request["stream"] = true
	} else {
		request["max_tokens"] = 4096
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.config.OpenAIKey,
	}

	if stream {
		return llmMakeStreamingRequest(ctx, "POST", "https://api.openai.com/v1/chat/completions", 
			headers, jsonData, p.parseStream)
	}

	respBody, err := llmMakeRequest("POST", "https://api.openai.com/v1/chat/completions", 
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
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

func (p *OpenAIProvider) parseStream(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder
	
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
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			// Replace newlines for terminal display
			content := strings.ReplaceAll(response.Choices[0].Delta.Content, "\n", "\r\n")
			fmt.Print(content)
			fullResponse.WriteString(response.Choices[0].Delta.Content)
		}
	}

	fmt.Println()
	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}
	
	return fullResponse.String(), nil
}

// ClaudeProvider implements the LLM provider interface for Claude
type ClaudeProvider struct {
	config *Config
}

func NewClaudeProvider(config *Config) *ClaudeProvider {
	return &ClaudeProvider{
		config: config,
	}
}

func (p *ClaudeProvider) GetName() string {
	return "Claude"
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

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": "2023-06-01",
		"x-api-key":         p.config.ClaudeKey,
	}

	if stream {
		return llmMakeStreamingRequest(ctx, "POST", "https://api.anthropic.com/v1/messages", 
			headers, jsonData, p.parseStream)
	}

	respBody, err := llmMakeRequest("POST", "https://api.anthropic.com/v1/messages", 
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
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
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if response.Type == "content_block_delta" && response.Delta.Text != "" {
			// Replace newlines for terminal display
			content := strings.ReplaceAll(response.Delta.Text, "\n", "\r\n")
			fmt.Print(content)
			fullResponse.WriteString(response.Delta.Text)
		}
	}

	fmt.Println()
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

func (p *GeminiProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	// Gemini currently doesn't use message structure like OpenAI, so we need to concat messages
	content := ""
	for _, msg := range messages {
		if msg.Role == "system" {
			content += "System: " + msg.Content + "\n\n"
		} else {
			content += msg.Content
		}
	}
	
	request := struct {
		Contents []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}{
		Contents: []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}{
			{
				Parts: []struct {
					Text string `json:"text"`
				}{
					{
						Text: content,
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", 
		p.config.GeminiKey)

	// Gemini doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest("POST", url, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
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

func (p *MistralProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		Model     string    `json:"model"`
		Messages  []Message `json:"messages"`
		MaxTokens int       `json:"max_tokens"`
	}{
		Model:     p.config.MistralModel,
		Messages:  messages,
		MaxTokens: 5128,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.MistralKey,
		"Content-Type":  "application/json",
	}

	// Mistral doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest("POST", "https://api.mistral.ai/v1/chat/completions", 
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
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

func (p *MistralProvider) parseStream(reader io.Reader) (string, error) {
	// Mistral streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for Mistral")
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

func (p *DeepSeekProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		Model    string    `json:"model"`
		Stream   string    `json:"stream"`
		Messages []Message `json:"messages"`
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

	// DeepSeek doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest("POST", "https://api.deepseek.com/chat/completions", 
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
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

func (p *BedrockProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	request := struct {
		ModelId         string `json:"modelId"`
		InferenceParams struct {
			MaxTokens   int     `json:"maxTokenCount"`
			Temperature float64 `json:"temperature"`
			TopP        float64 `json:"topP"`
		} `json:"inferenceParams"`
		Input struct {
			Messages []Message `json:"messages"`
		} `json:"input"`
	}{
		ModelId: p.config.BedrockModel,
		InferenceParams: struct {
			MaxTokens   int     `json:"maxTokenCount"`
			Temperature float64 `json:"temperature"`
			TopP        float64 `json:"topP"`
		}{
			MaxTokens:   5128,
			Temperature: 0.7,
			TopP:        0.9,
		},
		Input: struct {
			Messages []Message `json:"messages"`
		}{
			Messages: messages,
		},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	// Bedrock requires AWS signature auth, so we'll use AWS endpoint format
	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke",
		p.config.BedrockRegion, p.config.BedrockModel)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"X-Amz-Access-Token": p.config.BedrockKey,
	}

	// Bedrock doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest("POST", url, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Output struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"output"`
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

func (p *OpenAPIProvider) SendMessage(ctx context.Context, messages []Message, stream bool) (string, error) {
	// OpenAPI doesn't use message structure, so we need to concat messages
	content := ""
	for _, msg := range messages {
		if msg.Role == "system" {
			content += "System: " + msg.Content + "\n\n"
		} else {
			content += msg.Content
		}
	}
	
	request := struct {
		Prompt string `json:"prompt"`
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

	url := fmt.Sprintf("http://%s:%s/completion", p.config.OpenAPIHost, p.config.OpenAPIPort)

	// OpenAPI doesn't support streaming in our implementation
	respBody, err := llmMakeRequest("POST", url, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Content string `json:"content"`
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
