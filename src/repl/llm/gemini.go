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

// GeminiProvider implements the LLM provider interface for Google's Gemini
type GeminiProvider struct {
	config *Config
	apiKey string
}

func NewGeminiProvider(config *Config) *GeminiProvider {
	return &GeminiProvider{
		config: config,
		apiKey: GetAPIKey("GEMINI_API_KEY", "~/.r2ai.gemini-key"),
	}
}

func (p *GeminiProvider) GetName() string {
	return "Gemini"
}

func (p *GeminiProvider) DefaultModel() string {
	if v := os.Getenv("GEMINI_MODEL"); v != "" {
		return v
	}
	return "gemini-2.5-flash"
}

func defaultString(s, def string) string {
	if s != "" {
		return strings.TrimRight(s, "/")
	}
	return def
}

func (p *GeminiProvider) ListModels(ctx context.Context) ([]Model, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Missing key for gemini")
	}

	baseURL := defaultString(p.config.BaseURL, "https://generativelanguage.googleapis.com/v1beta")
	apiURL := baseURL + "/models?key=" + p.apiKey
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if p.apiKey != "" && strings.HasPrefix(p.apiKey, "ya29.") {
		headers["Authorization"] = "Bearer " + p.apiKey
	} else if p.apiKey != "" {
		// Some installs may prefer the x-goog-api-key header
		headers["x-goog-api-key"] = p.apiKey
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
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
		return nil, err
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

	return models, nil
}

func (p *GeminiProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by provider: Gemini")
	}
	content := ""
	for _, msg := range messages {
		if msg.Role == "system" {
			content += "System: " + msg.Content.(string) + "\n\n"
		} else {
			content += msg.Content.(string)
		}
	}

	request := map[string]interface{}{}

	// contents style
	request["contents"] = []map[string]interface{}{
		{
			"parts": []map[string]interface{}{
				{"text": content},
			},
		},
	}

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
		stream = false
		request["generationConfig"] = map[string]interface{}{
			"responseMimeType": "application/json",
			"responseSchema":   p.config.Schema,
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if stream {
		headers["Accept"] = "text/event-stream"
	}

	// If the key looks like an OAuth2 access token, send it as a Bearer token.
	if p.apiKey != "" && strings.HasPrefix(p.apiKey, "ya29.") {
		headers["Authorization"] = "Bearer " + p.apiKey
	} else if p.apiKey != "" {
		// Send via x-goog-api-key header as well, some deployments prefer this
		headers["x-goog-api-key"] = p.apiKey
	}

	// Use the configured base URL if available, otherwise use the default API URL
	model := p.config.Model
	if model == "" {
		model = p.DefaultModel()
	}
	var action = "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	baseURL := defaultString(p.config.BaseURL, "https://generativelanguage.googleapis.com/v1beta")
	apiURL := fmt.Sprintf("%s/models/%s:%s?alt=sse&key=%s", baseURL, model, action, p.apiKey)

	// If streaming requested, use the streaming helper which will call our parser
	if stream {
		return llmMakeStreamingRequestWithTiming(ctx, "POST", apiURL, headers, jsonData, func(r io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
			return p.parseStreamWithTiming(r, stopCallback, firstTokenCallback, streamEndCallback)
		}, nil, nil, nil)
	}

	// non-streaming fallback
	respBody, err := llmMakeRequest(ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(string(respBody), "data: ") {
		respBody = respBody[5:]
	}

	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}
	if response.Error.Message != "" {
		return "", fmt.Errorf("%s", response.Error.Message)
	}

	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		// Return raw content - newline conversion happens in the REPL
		txt := response.Candidates[0].Content.Parts[0].Text
		return txt, nil
	}

	return "", fmt.Errorf("no content in response")
}

func (p *GeminiProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *GeminiProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	return p.parseStreamWithTiming(reader, stopCallback, nil, nil)
}

func (p *GeminiProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder
	sd := NewStreamDemo(stopCallback, firstTokenCallback, streamEndCallback)

	// Check if markdown is enabled
	markdownEnabled := false
	markdownEnabled = p.config.Markdown

	// Reset the stream renderer if markdown is enabled
	if markdownEnabled {
		ResetStreamRenderer()
	}

	printed := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			line = strings.TrimPrefix(line, "data: ")
			if line == "[DONE]" {
				break
			}
		}

		// Try to parse JSON payload
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			// not JSON, skip
			continue
		}

		// If the event contains an error object, return it as an error immediately.
		if errObj, ok := obj["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok && msg != "" {
				// Return any collected response along with the error message
				return fullResponse.String(), fmt.Errorf("%s", msg)
			}
		}

		// Attempt to extract text from known Gemini shape
		var chunk string
		if cands, ok := obj["candidates"].([]interface{}); ok && len(cands) > 0 {
			if cand0, ok := cands[0].(map[string]interface{}); ok {
				if content, ok := cand0["content"].(map[string]interface{}); ok {
					// parts array
					if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
						if p0, ok := parts[0].(map[string]interface{}); ok {
							if txt, ok := p0["text"].(string); ok {
								chunk = txt
							}
						}
					}
					// some variants may include a top-level text field under content
					if chunk == "" {
						if txt, ok := content["text"].(string); ok {
							chunk = txt
						}
					}
				}
			}
		}

		// Fallbacks: direct fields some wrappers use
		if chunk == "" {
			if txt, ok := obj["text"].(string); ok {
				chunk = txt
			} else if resp, ok := obj["response"].(string); ok {
				chunk = resp
			}
		}

		if chunk == "" {
			// Nothing extracted from this event
			continue
		}

		// Centralized demo handling and then format/print the content
		sd.OnToken(chunk)
		toPrint := chunk
		if p.config.DemoMode || thinkDropLeading {
			toPrint = FilterOutThinkForOutput(toPrint)
		}
		// Trim leading whitespace/newlines on first visible output in demo mode
		if p.config.DemoMode && !printed {
			toPrint = strings.TrimLeft(toPrint, " \t\r\n")
		}
		fmt.Print(FormatStreamingChunk(toPrint, markdownEnabled))
		if toPrint != "" {
			printed = true
		}
		fullResponse.WriteString(chunk)
	}

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			EmitDemoTokens(final)
			if p.config.ThinkHide {
				trimmed := FilterOutThinkForOutput(final)
				if !printed {
					trimmed = strings.TrimLeft(trimmed, " \t\r\n")
				}
				fmt.Print(trimmed)
			} else {
				trimmed := TrimLeadingThink(final)
				if !printed {
					trimmed = strings.TrimLeft(trimmed, " \t\r\n")
				}
				fmt.Print(trimmed)
			}
		}
	}

	fmt.Println()

	// Call stream end callback for timing
	sd.OnStreamEnd()

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}
