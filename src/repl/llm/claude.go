package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// ClaudeProvider implements the LLM provider interface for Claude
type ClaudeProvider struct {
	config *Config
	apiKey string
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
		apiKey: GetAPIKey("CLAUDE_API_KEY", "~/.r2ai.anthropic-key"),
	}
}

func (p *ClaudeProvider) GetName() string {
	return "Claude"
}

func (p *ClaudeProvider) DefaultModel() string {
	if v := os.Getenv("CLAUDE_MODEL"); v != "" {
		return v
	}
	return "claude-3-5-sonnet-20241022"
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
		"x-api-key":         p.apiKey,
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

func (p *ClaudeProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: Claude")
	}
	effectiveModel := p.config.Model
	if effectiveModel == "" {
		effectiveModel = p.DefaultModel()
	}
	request := map[string]interface{}{
		"model":      effectiveModel,
		"max_tokens": 5128,
		"messages":   messages,
	}

	// If a schema is provided, use tools with forced tool_choice to enforce shape
	if p.config.Schema != nil {
		// Streaming tool_use events require different parsing; force non-stream for now
		stream = false
		request["tools"] = []map[string]interface{}{
			{
				"name":         "output_schema_tool",
				"description":  "Return the response following the given JSON schema.",
				"input_schema": p.config.Schema,
			},
		}
		request["tool_choice"] = map[string]interface{}{
			"type": "tool",
			"name": "output_schema_tool",
		}
	} else if stream {
		request["stream"] = true
	}

	// Apply deterministic settings if enabled
	if p.config.Deterministic {
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
		"x-api-key":         p.apiKey,
	}

	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.anthropic.com/v1/messages"
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/v1/messages"
	}

	if stream {
		return llmMakeStreamingRequestWithCallback(ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader, stopCallback func()) (string, error) {
				return p.parseStreamWithCallback(r, stopCallback)
			}, nil)
	}

	respBody, err := llmMakeRequest(ctx, "POST", apiURL,
		headers, jsonData)
	if err != nil {
		return "", err
	}
	// If schema was used, extract the tool_use input as the structured output
	if p.config.Schema != nil {
		var response struct {
			Content []struct {
				Type  string                 `json:"type"`
				Text  string                 `json:"text,omitempty"`
				Name  string                 `json:"name,omitempty"`
				Input map[string]interface{} `json:"input,omitempty"`
			} `json:"content"`
		}
		if err := json.Unmarshal(respBody, &response); err != nil {
			return "", err
		}
		for _, c := range response.Content {
			if c.Type == "tool_use" && c.Name == "output_schema_tool" {
				// Return the input payload as JSON string
				b, _ := json.Marshal(c.Input)
				return string(b), nil
			}
		}
		return "", fmt.Errorf("no tool_use content in response")
	}

	// Default text extraction path
	var response struct {
		Content []struct {
			Text string `json:"text""`
		} `json:"content""`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}
	if len(response.Content) > 0 {
		return response.Content[0].Text, nil
	}
	return "", fmt.Errorf("no content in response")
}

func (p *ClaudeProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *ClaudeProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder
	firstTokenReceived := false

	// Check if markdown is enabled
	markdownEnabled := false
	markdownEnabled = p.config.Markdown

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
			// Stop demo animation on first token received
			if !firstTokenReceived {
				firstTokenReceived = true
				if stopCallback != nil {
					stopCallback()
				}
			}

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
