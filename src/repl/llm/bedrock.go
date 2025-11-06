package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// BedrockProvider implements the LLM provider interface for AWS Bedrock
type BedrockProvider struct {
	BaseProvider
}

func NewBedrockProvider(config *Config, ctx context.Context) *BedrockProvider {
	return &BedrockProvider{
		BaseProvider: BaseProvider{
			config: config,
			apiKey: GetAPIKey("bedrock"),
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

func (p *BedrockProvider) SendMessage(messages []Message, stream bool, images []string, tools []OpenAITool) (string, error) {
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

	// Build conversation string from messages
	inputText := BuildConversationString(messages, false, true, "plain", false)

	// Prepare the request body
	body := map[string]string{
		"inputText": inputText,
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	// Run AWS CLI command
	cmd := exec.CommandContext(p.ctx, "aws", "bedrock-runtime", "invoke-model",
		"--model-id", model,
		"--body", string(jsonData),
		"--cli-binary-format", "raw-in-base64-out",
		"/dev/stdout")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to invoke model: %v", err)
	}

	return string(output), nil
}

func (p *BedrockProvider) Embed(input string) ([]float64, error) {
	return nil, fmt.Errorf("embeddings not supported by Bedrock provider")
}
