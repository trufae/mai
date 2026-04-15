package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/art"
)

// LlamaCppProvider implements the LLM provider interface for llama.cpp-server.
type LlamaCppProvider struct {
	BaseProvider
}

func NewLlamaCppProvider(config *Config, ctx context.Context) *LlamaCppProvider {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:8080"
	}
	return &LlamaCppProvider{
		BaseProvider: BaseProvider{
			config: config,
			apiKey: GetAPIKey("llamacpp"),
			ctx:    ctx,
		},
	}
}

func (p *LlamaCppProvider) GetName() string {
	return "LlamaCpp"
}

func (p *LlamaCppProvider) DefaultModel() string {
	if v := os.Getenv("LLAMACPP_MODEL"); v != "" {
		return v
	}
	return ""
}

func (p *LlamaCppProvider) IsAvailable() bool {
	for _, endpoint := range []string{"/health", "/v1/models", "/props"} {
		status, err := p.probeEndpoint(endpoint)
		if err == nil && status < http.StatusBadRequest {
			return true
		}
	}
	return false
}

func (p *LlamaCppProvider) ListModels(ctx context.Context) ([]Model, error) {
	var lastErr error
	for _, endpoint := range []string{"/v1/models", "/models", "/props"} {
		url := p.endpointURL(endpoint)
		body, err := llmMakeRequest(ctx, "GET", url, p.headers(), nil)
		if err != nil {
			lastErr = err
			continue
		}
		if models := parseLlamaCppModels(body); len(models) > 0 {
			return models, nil
		}
	}

	effectiveModel := strings.TrimSpace(p.config.Model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(p.DefaultModel())
	}
	if effectiveModel != "" {
		return []Model{{ID: effectiveModel, Name: effectiveModel, Provider: "llamacpp"}}, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return []Model{}, nil
}

func (p *LlamaCppProvider) SendMessage(messages []Message, stream bool, images []string, tools []OpenAITool) (string, error) {
	effectiveModel := strings.TrimSpace(p.config.Model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(p.DefaultModel())
	}

	request := map[string]interface{}{
		"messages": mergeImagesIntoLastUser(messages, images),
	}
	if effectiveModel != "" {
		request["model"] = effectiveModel
	}
	if len(tools) > 0 {
		request["tools"] = tools
		request["tool_choice"] = "auto"
	}
	if p.config.Schema != nil {
		request["response_format"] = map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "output_schema",
				"schema": p.config.Schema,
			},
		}
	}
	if stream {
		request["stream"] = true
	}
	if p.config.Deterministic {
		request["temperature"] = 0
		request["seed"] = 123
	}

	jsonData, err := MarshalNoEscape(request)
	if err != nil {
		return "", err
	}

	if p.config.Debug {
		art.DebugBanner("LlamaCpp Request", string(jsonData))
	}

	candidates := []string{
		p.endpointURL("/v1/chat/completions"),
		p.endpointURL("/chat/completions"),
	}

	if stream {
		return tryPostCandidatesStream(p.ctx, candidates, p.headers(), jsonData, p.parseStreamWithTiming)
	}

	respBody, err := tryPostCandidatesNonStream(p.ctx, candidates, p.headers(), jsonData)
	if p.config.Debug {
		art.DebugBanner("LlamaCpp Response", string(respBody))
	}
	if err != nil {
		return "", err
	}
	if len(respBody) == 0 {
		return "", fmt.Errorf("empty response from llama.cpp server")
	}
	if strings.Contains(string(respBody), "data: ") {
		return p.parseStream(strings.NewReader(string(respBody)))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}
	if response.Error.Message != "" {
		return "", fmt.Errorf("llama.cpp API error: %s", response.Error.Message)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in llama.cpp response")
	}
	if len(response.Choices[0].Message.ToolCalls) > 0 {
		return string(respBody), nil
	}
	return response.Choices[0].Message.Content, nil
}

func (p *LlamaCppProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithTiming(reader, nil, nil, nil)
}

func (p *LlamaCppProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
	openAI := &OpenAIProvider{BaseProvider: p.BaseProvider}
	return openAI.parseStreamWithTiming(reader, stopCallback, firstTokenCallback, streamEndCallback)
}

func (p *LlamaCppProvider) Embed(input string) ([]float64, error) {
	request := map[string]interface{}{
		"input": input,
	}

	effectiveModel := strings.TrimSpace(p.config.Model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(p.DefaultModel())
	}
	if effectiveModel != "" {
		request["model"] = effectiveModel
	}

	jsonData, err := MarshalNoEscape(request)
	if err != nil {
		return nil, err
	}

	candidates := []string{
		p.endpointURL("/v1/embeddings"),
		p.endpointURL("/embeddings"),
		p.endpointURL("/embedding"),
	}

	respBody, err := tryPostCandidatesNonStream(p.ctx, candidates, p.headers(), jsonData)
	if err != nil {
		return nil, err
	}

	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Embedding []float64 `json:"embedding,omitempty"`
		Error     struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, err
	}
	if response.Error.Message != "" {
		return nil, fmt.Errorf("llama.cpp embeddings error: %s", response.Error.Message)
	}
	if len(response.Data) > 0 {
		return response.Data[0].Embedding, nil
	}
	if len(response.Embedding) > 0 {
		return response.Embedding, nil
	}
	return nil, fmt.Errorf("no embedding data in response")
}

func (p *LlamaCppProvider) CountTokens(text string) (int, error) {
	return EstimateTokenCount(text), nil
}

func (p *LlamaCppProvider) headers() map[string]string {
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}
	return headers
}

func (p *LlamaCppProvider) probeEndpoint(endpoint string) (int, error) {
	req, err := http.NewRequestWithContext(p.ctx, "GET", p.endpointURL(endpoint), nil)
	if err != nil {
		return 0, err
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func (p *LlamaCppProvider) endpointURL(endpoint string) string {
	baseURL := strings.TrimRight(p.config.BaseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(endpoint, "/v1/") {
		return baseURL + strings.TrimPrefix(endpoint, "/v1")
	}
	return baseURL + endpoint
}

func parseLlamaCppModels(body []byte) []Model {
	if len(body) == 0 {
		return nil
	}

	var openaiResp OpenAIModelsResponse
	if err := json.Unmarshal(body, &openaiResp); err == nil && len(openaiResp.Data) > 0 {
		models := make([]Model, 0, len(openaiResp.Data))
		for _, m := range openaiResp.Data {
			models = append(models, Model{
				ID:          m.ID,
				Name:        m.ID,
				Provider:    "llamacpp",
				Description: "Owner: " + m.OwnedBy,
			})
		}
		return models
	}

	var list []string
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		models := make([]Model, 0, len(list))
		for _, id := range list {
			models = append(models, Model{ID: id, Name: id, Provider: "llamacpp"})
		}
		return models
	}

	var objList []map[string]interface{}
	if err := json.Unmarshal(body, &objList); err == nil && len(objList) > 0 {
		models := make([]Model, 0, len(objList))
		for _, item := range objList {
			id, _ := item["id"].(string)
			if id == "" {
				id, _ = item["name"].(string)
			}
			if id == "" {
				continue
			}
			models = append(models, Model{ID: id, Name: id, Provider: "llamacpp"})
		}
		if len(models) > 0 {
			return models
		}
	}

	var props map[string]interface{}
	if err := json.Unmarshal(body, &props); err != nil {
		return nil
	}

	alias, _ := props["model_alias"].(string)
	modelPath, _ := props["model_path"].(string)
	modelID := strings.TrimSpace(alias)
	if modelID == "" {
		modelID = strings.TrimSpace(modelPath)
	}
	if modelID == "" {
		return nil
	}

	name := modelID
	if modelPath != "" {
		base := filepath.Base(modelPath)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if alias != "" {
		name = alias
	}

	return []Model{{
		ID:          modelID,
		Name:        name,
		Provider:    "llamacpp",
		Description: modelPath,
	}}
}

// mergeImagesIntoLastUser returns a JSON-ready messages slice that matches
// the standard OpenAI vision format: image blocks live inside the same user
// turn as the prompt text. The last user message's content is replaced with
// a combined [text, image_url...] block array; if no user message exists yet
// a new one is appended. Shared by all OpenAI-compatible providers (OpenAI,
// llama.cpp, ...) that accept structured content blocks for images.
func mergeImagesIntoLastUser(messages []Message, images []string) []interface{} {
	out := make([]interface{}, 0, len(messages)+1)
	for _, m := range messages {
		out = append(out, m)
	}
	if len(images) == 0 {
		return out
	}

	blocks := make([]ContentBlock, 0, len(images)+1)
	for _, uri := range images {
		blocks = append(blocks, ContentBlock{
			Type: "image_url",
			ImageURL: &struct {
				URL string `json:"url"`
			}{URL: uri},
		})
	}

	for i := len(out) - 1; i >= 0; i-- {
		msg, ok := out[i].(Message)
		if !ok || msg.Role != "user" {
			continue
		}
		combined := blocks
		if strings.TrimSpace(msg.Content) != "" {
			combined = append([]ContentBlock{{Type: "text", Text: msg.Content}}, blocks...)
		}
		out[i] = map[string]interface{}{
			"role":    "user",
			"content": combined,
		}
		return out
	}

	out = append(out, map[string]interface{}{
		"role":    "user",
		"content": blocks,
	})
	return out
}

