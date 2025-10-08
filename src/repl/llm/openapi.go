package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenAPIProvider implements the LLM provider interface for OpenAPI
type OpenAPIProvider struct {
	config *Config
}

func NewOpenAPIProvider(config *Config) *OpenAPIProvider {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8989"
	}
	return &OpenAPIProvider{
		config: config,
	}
}

func (p *OpenAPIProvider) GetName() string {
	return "OpenAPI"
}

func (p *OpenAPIProvider) DefaultModel() string {
	// OpenAPI provider doesn't use a model string in requests; return a placeholder
	if v := os.Getenv("OPENAPI_MODEL"); v != "" {
		return v
	}
	return "default"
}

func (p *OpenAPIProvider) IsAvailable() bool {
	// OpenAPI is a local/custom service, check HTTP endpoint
	baseURL := p.config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8989"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Head(baseURL + "/models")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

func (p *OpenAPIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Try to query the OpenAPI server for available models
	apiURL := strings.TrimRight(p.config.BaseURL, "/") + "/models"

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

func (p *OpenAPIProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: OpenAPI")
	}
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

	// Use the configured base URL
	apiURL := strings.TrimRight(p.config.BaseURL, "/") + "/completion"

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
