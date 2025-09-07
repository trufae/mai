package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

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

func (p *BedrockProvider) DefaultModel() string {
	if v := os.Getenv("BEDROCK_MODEL"); v != "" {
		return v
	}
	return "anthropic.claude-3-5-sonnet-v1"
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

func (p *BedrockProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: Bedrock")
	}
	model := p.config.Model
	if model == "" {
		model = p.DefaultModel()
	}
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
		ModelId: model,
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
		p.config.BedrockRegion, model)
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + fmt.Sprintf("/model/%s/invoke", model)
	}

	headers := map[string]string{
		"Content-Type":       "application/json",
		"X-Amz-Access-Token": p.config.BedrockKey,
	}
	// fmt.Println(p.config.BedrockKey)
	// fmt.Println(p.config.BedrockRegion)

	// Bedrock doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Message string `json:"message""`
		Output  struct {
			Message struct {
				Content string `json:"content""`
			} `json:"message""`
		} `json:"output""`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}
	if response.Message != "" {
		return "", fmt.Errorf("bedrock: %v", response.Message)
	}

	// Return raw content - newline conversion happens in the REPL
	return response.Output.Message.Content, nil
}

func (p *BedrockProvider) parseStream(reader io.Reader) (string, error) {
	// Bedrock streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for Bedrock")
}
