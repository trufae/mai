package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// BedrockProvider implements the LLM provider interface for AWS Bedrock
type BedrockProvider struct {
	BaseProvider
}

func NewBedrockProvider(config *Config, ctx context.Context) *BedrockProvider {
	return &BedrockProvider{
		BaseProvider: BaseProvider{
			config: config,
			apiKey: GetAPIKey("AWS_ACCESS_KEY_ID", "~/.r2ai.bedrock-key"),
			ctx:    ctx,
		},
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

func (p *BedrockProvider) IsAvailable() bool {
	// Bedrock requires AWS credentials (AWS_ACCESS_KEY_ID)
	return p.apiKey != ""
}

func (p *BedrockProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use AWS CLI to list foundation models
	cmd := exec.CommandContext(ctx, "aws", "bedrock", "list-foundation-models", "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list Bedrock models: %v", err)
	}

	var response struct {
		ModelSummaries []struct {
			ModelId      string `json:"modelId"`
			ModelName    string `json:"modelName"`
			ProviderName string `json:"providerName"`
		} `json:"modelSummaries"`
	}

	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse model list response: %v", err)
	}

	var models []Model
	for _, summary := range response.ModelSummaries {
		models = append(models, Model{
			ID:          summary.ModelId,
			Name:        summary.ModelName,
			Description: fmt.Sprintf("%s model via AWS Bedrock", summary.ProviderName),
			Provider:    "bedrock",
		})
	}

	return models, nil
}

// isClaudeModel checks if the model is an Anthropic Claude model
func isClaudeModel(modelId string) bool {
	return strings.HasPrefix(modelId, "anthropic.")
}

// isLlamaModel checks if the model is a Meta Llama model
func isLlamaModel(modelId string) bool {
	return strings.HasPrefix(modelId, "meta.llama")
}

func (p *BedrockProvider) SendMessage(messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: Bedrock")
	}
	if stream {
		return "", fmt.Errorf("streaming not supported by Bedrock CLI provider")
	}

	model := p.config.Model
	if model == "" {
		model = p.DefaultModel()
	}

	// Prepare the request body based on model type
	var body interface{}
	if isClaudeModel(model) {
		body = p.prepareClaudeRequest(messages)
	} else if isLlamaModel(model) {
		body = p.prepareLlamaRequest(messages)
	} else {
		// Default to Claude format for other models
		body = p.prepareClaudeRequest(messages)
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	// Create temporary file for the request body
	tempFile, err := os.CreateTemp("", "bedrock-request-*.json")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.Write(jsonData); err != nil {
		return "", fmt.Errorf("failed to write request to temp file: %v", err)
	}
	tempFile.Close()

	// Create temporary file for the response
	responseFile, err := os.CreateTemp("", "bedrock-response-*.json")
	if err != nil {
		return "", fmt.Errorf("failed to create response temp file: %v", err)
	}
	defer os.Remove(responseFile.Name())
	responseFile.Close()

	// Run AWS CLI command
	cmd := exec.CommandContext(p.ctx, "aws", "bedrock-runtime", "invoke-model",
		"--model-id", model,
		"--body", fmt.Sprintf("file://%s", tempFile.Name()),
		"--cli-binary-format", "raw-in-base64-out",
		fmt.Sprintf("file://%s", responseFile.Name()))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to invoke model: %v, output: %s", err, string(output))
	}

	// Read the response
	responseData, err := os.ReadFile(responseFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read response file: %v", err)
	}

	// Parse response based on model type
	if isClaudeModel(model) {
		return p.parseClaudeResponse(responseData)
	} else if isLlamaModel(model) {
		return p.parseLlamaResponse(responseData)
	} else {
		// Default to Claude parsing
		return p.parseClaudeResponse(responseData)
	}
}

// prepareClaudeRequest formats the request for Claude models
func (p *BedrockProvider) prepareClaudeRequest(messages []Message) map[string]interface{} {
	return map[string]interface{}{
		"anthropic_version": "bedrock-2023-05-31",
		"max_tokens":        4096,
		"messages":          messages,
		"temperature":       0.7,
		"top_p":             0.9,
	}
}

// prepareLlamaRequest formats the request for Llama models
func (p *BedrockProvider) prepareLlamaRequest(messages []Message) map[string]interface{} {
	// Convert messages to Llama prompt format
	var prompt strings.Builder
	for i, msg := range messages {
		if msg.Role == "user" {
			prompt.WriteString(fmt.Sprintf("<|start_header_id|>user<|end_header_id|>\n%s\n", msg.Content))
		} else if msg.Role == "assistant" {
			prompt.WriteString(fmt.Sprintf("<|start_header_id|>assistant<|end_header_id|>\n%s\n", msg.Content))
		}
		if i < len(messages)-1 || msg.Role == "assistant" {
			prompt.WriteString("<|eot_id|>")
		}
	}
	prompt.WriteString("<|start_header_id|>assistant<|end_header_id|>")

	return map[string]interface{}{
		"prompt":      prompt.String(),
		"max_gen_len": 2048,
		"temperature": 0.7,
		"top_p":       0.9,
	}
}

// parseClaudeResponse parses the response from Claude models
func (p *BedrockProvider) parseClaudeResponse(data []byte) (string, error) {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("failed to parse Claude response: %v", err)
	}

	if len(response.Content) == 0 {
		return "", fmt.Errorf("no content in Claude response")
	}

	return response.Content[0].Text, nil
}

// parseLlamaResponse parses the response from Llama models
func (p *BedrockProvider) parseLlamaResponse(data []byte) (string, error) {
	var response struct {
		Generation string `json:"generation"`
	}

	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("failed to parse Llama response: %v", err)
	}

	return response.Generation, nil
}

func (p *BedrockProvider) parseStream(reader io.Reader) (string, error) {
	// Streaming not supported with AWS CLI
	return "", fmt.Errorf("streaming not supported for Bedrock CLI provider")
}
