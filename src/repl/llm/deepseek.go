package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

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

func (p *DeepSeekProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: DeepSeek")
	}
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
