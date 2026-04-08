package llm

import (
	"bufio"
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
	BaseProvider
}

type openAPICompletionRequest struct {
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream,omitempty"`
}

func NewOpenAPIProvider(config *Config, ctx context.Context) *OpenAPIProvider {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8989"
	}
	return &OpenAPIProvider{
		BaseProvider: BaseProvider{
			config: config,
			apiKey: "",
			ctx:    ctx,
		},
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

func buildOpenAPICompletionRequest(prompt string, stream bool) openAPICompletionRequest {
	return openAPICompletionRequest{
		Prompt: prompt,
		Stream: stream,
	}
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
		_ = resp.Body.Close()
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
		ID   string `json:"id"`
		Name string `json:"name,omitempty"`
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

func (p *OpenAPIProvider) SendMessage(messages []Message, stream bool, images []string, tools []OpenAITool) (string, error) {
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

	request := buildOpenAPICompletionRequest(content, stream)

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json, text/event-stream, text/plain",
	}

	// Use the configured base URL
	apiURL := strings.TrimRight(p.config.BaseURL, "/") + "/completion"

	if stream {
		return llmMakeStreamingRequestWithTiming(p.ctx, "POST", apiURL, headers, jsonData,
			func(r io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
				return p.parseStreamWithTiming(r, stopCallback, firstTokenCallback, streamEndCallback)
			}, nil, nil, nil)
	}

	respBody, err := llmMakeRequest(p.ctx, "POST", apiURL, headers, jsonData)
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
	return p.parseStreamWithTiming(reader, nil, nil, nil)
}

func (p *OpenAPIProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
	br := bufio.NewReader(reader)
	sd := NewStreamDemo(stopCallback, firstTokenCallback, streamEndCallback)
	var fullResponse strings.Builder

	markdownEnabled := p.config.Markdown
	if markdownEnabled {
		ResetStreamRenderer()
	}

	printed := false
	emit := func(raw string) {
		if raw == "" {
			return
		}
		sd.OnToken(raw)
		toPrint := raw
		if thinkHideEnabled || thinkDropLeading {
			toPrint = FilterOutThinkForOutput(toPrint)
		}
		if p.config.DemoMode && !printed {
			toPrint = strings.TrimLeft(toPrint, " \t\r\n")
		}
		fmt.Print(FormatStreamingChunk(toPrint, markdownEnabled))
		if toPrint != "" {
			printed = true
		}
		appendResponseText(&fullResponse, p.ctx, raw)
	}

	if openAPIStreamLooksStructured(br) {
		if err := p.parseStructuredOpenAPIStream(br, emit, sd); err != nil {
			return fullResponse.String(), err
		}
	} else {
		if err := p.parseRawOpenAPIStream(br, emit); err != nil {
			return fullResponse.String(), err
		}
	}

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
	sd.OnStreamEnd()
	return fullResponse.String(), nil
}

func openAPIStreamLooksStructured(br *bufio.Reader) bool {
	peek, err := br.Peek(64)
	if len(peek) == 0 && err != nil {
		return false
	}
	trimmed := strings.TrimLeft(string(peek), " \t\r\n")
	return strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func (p *OpenAPIProvider) parseStructuredOpenAPIStream(br *bufio.Reader, emit func(string), sd *StreamDemo) error {
	for {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		default:
		}

		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}

		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			done, parseErr := p.handleOpenAPIStreamLine(line, emit, sd)
			if parseErr != nil {
				return parseErr
			}
			if done {
				return nil
			}
		}

		if err == io.EOF {
			return nil
		}
	}
}

func (p *OpenAPIProvider) parseRawOpenAPIStream(br *bufio.Reader, emit func(string)) error {
	buf := make([]byte, 1024)
	for {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		default:
		}

		n, err := br.Read(buf)
		if n > 0 {
			emit(string(buf[:n]))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (p *OpenAPIProvider) handleOpenAPIStreamLine(line string, emit func(string), sd *StreamDemo) (bool, error) {
	data := strings.TrimSpace(line)
	if data == "" {
		return false, nil
	}
	if strings.HasPrefix(data, "data:") {
		data = strings.TrimSpace(strings.TrimPrefix(data, "data:"))
		if data == "" {
			return false, nil
		}
		if data == "[DONE]" {
			return true, nil
		}
	}

	raw, reasoning, done, ok := extractOpenAPIStreamPayload(data)
	if !ok {
		emit(data)
		return false, nil
	}
	for _, text := range reasoning {
		sd.OnToken(text)
		accountResponseText(p.ctx, text)
	}
	emit(raw)
	return done, nil
}

func extractOpenAPIStreamPayload(data string) (string, []string, bool, bool) {
	raw, reasoning := extractOpenAIStreamDelta(data)
	if raw != "" || len(reasoning) > 0 {
		return raw, reasoning, false, true
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", nil, false, false
	}

	done, _ := payload["done"].(bool)
	if choices, ok := payload["choices"].([]interface{}); ok && len(choices) > 0 {
		choice, _ := choices[0].(map[string]interface{})
		if choice != nil {
			if delta, ok := choice["delta"].(map[string]interface{}); ok {
				raw = strings.Join(collectDeltaTexts(delta["content"]), "")
				reasoning = append(collectDeltaTexts(delta["reasoning"]), collectDeltaTexts(delta["thinking"])...)
				reasoning = append(reasoning, collectDeltaTexts(delta["reasoning_content"])...)
				reasoning = append(reasoning, collectDeltaTexts(delta["thinking_content"])...)
				if raw != "" || len(reasoning) > 0 {
					return raw, reasoning, done, true
				}
			}
			if message, ok := choice["message"].(map[string]interface{}); ok {
				raw = strings.Join(collectDeltaTexts(message["content"]), "")
				reasoning = append(collectDeltaTexts(message["reasoning"]), collectDeltaTexts(message["thinking"])...)
				reasoning = append(reasoning, collectDeltaTexts(message["reasoning_content"])...)
				reasoning = append(reasoning, collectDeltaTexts(message["thinking_content"])...)
				if raw != "" || len(reasoning) > 0 {
					return raw, reasoning, done, true
				}
			}
		}
	}

	if value := strings.Join(collectDeltaTexts(payload["response"]), ""); value != "" {
		return value, nil, done, true
	}
	if value := strings.Join(collectDeltaTexts(payload["content"]), ""); value != "" {
		return value, nil, done, true
	}
	if value := strings.Join(collectDeltaTexts(payload["text"]), ""); value != "" {
		return value, nil, done, true
	}
	if message, ok := payload["message"].(map[string]interface{}); ok {
		if value := strings.Join(collectDeltaTexts(message["content"]), ""); value != "" {
			return value, nil, done, true
		}
	}
	return "", nil, done, true
}

func (p *OpenAPIProvider) Embed(input string) ([]float64, error) {
	return nil, fmt.Errorf("embeddings not supported by OpenAPI provider")
}

func (p *OpenAPIProvider) CountTokens(text string) (int, error) {
	return EstimateTokenCount(text), nil
}
