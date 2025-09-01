package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

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
	// If the key looks like an OAuth2 access token, send it as a Bearer token.
	if p.config.GeminiKey != "" && strings.HasPrefix(p.config.GeminiKey, "ya29.") {
		headers["Authorization"] = "Bearer " + p.config.GeminiKey
	} else if p.config.GeminiKey != "" {
		// Some installs may prefer the x-goog-api-key header
		headers["x-goog-api-key"] = p.config.GeminiKey
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
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			Description string   `json:"description"`
			Versions    []string `json:"supportedGenerationMethods,omitempty"`
		} `json:"models"`
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

func (p *GeminiProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: Gemini")
	}
	// Gemini currently doesn't use message structure like OpenAI, so we need to concat messages
	content := ""
	for _, msg := range messages {
		if msg.Role == "system" {
			content += "System: " + msg.Content.(string) + "\n\n"
		} else {
			content += msg.Content.(string)
		}
	}

	// Build a flexible request that includes multiple common shapes so wrapped/variant APIs
	// can accept one of them. We include "contents" (used by some wrappers), an "input"
	// shortcut, and an OpenAI-style messages list.
	request := map[string]interface{}{}

	// contents style
	request["contents"] = []map[string]interface{}{
		{
			"parts": []map[string]interface{}{
				{"text": content},
			},
		},
	}

	/*
		// input style
		request["input"] = map[string]interface{}{"text": content}

		// messages style
		request["messages"] = []map[string]interface{}{{"content": map[string]interface{}{"text": content}}}
	*/

	// Apply deterministic settings if enabled
	if p.config.Deterministic {
		request["generationConfig"] = map[string]interface{}{
			"temperature": 0.0,
			"topP":        1.0,
			"topK":        1,
		}
	}

	// If a structured output schema is requested, attach several compatible
	// fields so the Gemini API (or variants) can pick up the schema. We add
	// both camelCase and snake_case variants and include both the OpenAI-style
	// json_schema wrapper and direct schema fields for broader compatibility.
	if p.config.Schema != nil {
		/*
			request["responseFormat"] = map[string]interface{}{
				"type": "JSON_SCHEMA",
				"jsonSchema": map[string]interface{}{
					"name":   "output_schema",
					"schema": p.config.Schema,
				},
			}
		*/
		request["response_format"] = map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "output_schema",
				"schema": p.config.Schema,
			},
		}
		// Some clients / wrappers expect a direct responseSchema key
		// request["responseSchema"] = p.config.Schema
		request["response_schema"] = p.config.Schema
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// If the key looks like an OAuth2 access token, send it as a Bearer token.
	if p.config.GeminiKey != "" && strings.HasPrefix(p.config.GeminiKey, "ya29.") {
		headers["Authorization"] = "Bearer " + p.config.GeminiKey
	} else if p.config.GeminiKey != "" {
		// Send via x-goog-api-key header as well, some deployments prefer this
		headers["x-goog-api-key"] = p.config.GeminiKey
	}

	// Use the configured base URL if available, otherwise use the default API URL
	defaultModel := "gemini-2.5-flash"
	model := defaultModel
	if p.config.GeminiModel != "" {
		model = p.config.GeminiModel
	}
	fmt.Println(model)
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, p.config.GeminiKey)
	if p.config.BaseURL != "" {
		apiURL = strings.TrimRight(p.config.BaseURL, "/") + fmt.Sprintf("/v1beta/models/%s:generateContent?key=%s", model, p.config.GeminiKey)
	}

	fmt.Println(string(jsonData))
	// Gemini doesn't support streaming in our implementation yet
	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}

	fmt.Println(string(respBody))
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
		txt := response.Candidates[0].Content.Parts[0].Text
		return txt, nil
	}

	return "", fmt.Errorf("no content in response")
}

func (p *GeminiProvider) parseStream(reader io.Reader) (string, error) {
	// Gemini streaming isn't implemented yet
	return "", fmt.Errorf("streaming not implemented for Gemini")
}
