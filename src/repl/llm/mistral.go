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

// MistralProvider implements the LLM provider interface for Mistral
type MistralProvider struct {
	config *Config
	apiKey string
}

func NewMistralProvider(config *Config) *MistralProvider {
	return &MistralProvider{
		config: config,
		apiKey: GetAPIKey("MISTRAL_API_KEY", "~/.r2ai.mistral-key"),
	}
}

func (p *MistralProvider) GetName() string {
	return "Mistral"
}

func (p *MistralProvider) DefaultModel() string {
	if v := os.Getenv("MISTRAL_MODEL"); v != "" {
		return v
	}
	return "mistral-large-latest"
}

func (p *MistralProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.mistral.ai/v1/models"
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the models endpoint
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/models"
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.apiKey,
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

func (p *MistralProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: Mistral")
	}
	model := p.config.Model
	if model == "" {
		model = p.DefaultModel()
	}
	request := struct {
		Model          string                 `json:"model""`
		Messages       []Message              `json:"messages""`
		MaxTokens      int                    `json:"max_tokens""`
		Stream         bool                   `json:"stream,omitempty""`
		N              int                    `json:"n,omitempty""`
		TopP           float64                `json:"top_p,omitempty""`
		RandomSeed     int                    `json:"random_seed,omitempty""`
		Temperature    float64                `json:"temperature,omitempty""`
		ResponseFormat map[string]interface{} `json:"response_format,omitempty"`
	}{
		Model:     model,
		Messages:  messages,
		MaxTokens: 5128,
		Stream:    stream,
	}

	// If a structured output schema is requested, enable JSON object response_format
	// Mistral expects: { "response_format": { "type": "json_object" } }
	if p.config.Schema != nil {
		request.ResponseFormat = map[string]interface{}{
			"type": "json_object",
		}
	}

	// Apply deterministic settings if enabled
	if p.config.Deterministic {
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
		"Authorization": "Bearer " + p.apiKey,
		"Content-Type":  "application/json",
	}

	// Use the configured base URL if available, otherwise use the default API URL
	apiURL := "https://api.mistral.ai/v1/chat/completions"
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + "/v1/chat/completions"
	}

	// Handle streaming if requested
	if stream {
		return llmMakeStreamingRequestWithCallback(ctx, "POST", apiURL, headers, jsonData, func(r io.Reader, stopCallback func()) (string, error) {
			return p.parseStreamWithCallback(r, stopCallback)
		}, nil)
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
		return "", fmt.Errorf("%s", response.Message)
	}
	return "", fmt.Errorf("no content in response")
}

func (p *MistralProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *MistralProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
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
			// Stop demo animation on first token received
			if !firstTokenReceived {
				firstTokenReceived = true
				if stopCallback != nil {
					stopCallback()
				}
			}

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
