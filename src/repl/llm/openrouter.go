package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/art"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenRouterProvider implements the LLM provider interface for OpenRouter
type OpenRouterProvider struct {
	BaseProvider
}

// OpenRouterModelsResponse is the response structure for OpenRouter model list endpoint
type OpenRouterModelsResponse struct {
	Object string `json:"object""`
	Data   []struct {
		ID      string `json:"id""`
		Object  string `json:"object""`
		Created int64  `json:"created""`
		OwnedBy string `json:"owned_by""`
	} `json:"data""`
}

func NewOpenRouterProvider(config *Config, ctx context.Context) *OpenRouterProvider {
	apiKey := GetAPIKey("openrouter")

	// Set default base URL for OpenRouter
	if config.BaseURL == "" {
		config.BaseURL = "https://openrouter.ai/api/v1"
	}

	return &OpenRouterProvider{
		BaseProvider: BaseProvider{
			config: config,
			apiKey: apiKey,
			ctx:    ctx,
		},
	}
}

func (p *OpenRouterProvider) GetName() string {
	return "OpenRouter"
}

func (p *OpenRouterProvider) DefaultModel() string {
	if v := os.Getenv("OPENROUTER_MODEL"); v != "" {
		return v
	}
	return "google/gemma-3-27b-it:free"
}

func (p *OpenRouterProvider) IsAvailable() bool {
	// Check if API key is available
	if p.apiKey == "" {
		return false
	}

	// Check HTTP endpoint
	baseURL := p.config.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Head(baseURL + "/models")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

func (p *OpenRouterProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Build models endpoint URL
	apiURL := buildURL("https://openrouter.ai/api/v1/models", p.config.BaseURL, "", "", "/models")

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
	}

	// Try multiple parsing strategies so that non-standard
	// model endpoints can be handled gracefully.
	tryParse := func(body []byte) ([]Model, bool) {
		if len(body) == 0 {
			return nil, false
		}

		// 1) OpenAI style: {"object":"list","data":[{id:..., owned_by:...}, ...]}
		var openaiResp OpenRouterModelsResponse
		if err := json.Unmarshal(body, &openaiResp); err == nil && len(openaiResp.Data) > 0 {
			models := make([]Model, 0, len(openaiResp.Data))
			for _, m := range openaiResp.Data {
				models = append(models, Model{ID: m.ID, Name: m.ID, Provider: "openrouter"})
			}
			return models, true
		}

		// 2) Plain list of model IDs: ["gpt-4", "gpt-3.5"]
		var list []string
		if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
			models := make([]Model, 0, len(list))
			for _, id := range list {
				models = append(models, Model{ID: id, Name: id, Provider: "openrouter"})
			}
			return models, true
		}

		// 3) Array of objects with id/name fields
		var objList []map[string]interface{}
		if err := json.Unmarshal(body, &objList); err == nil && len(objList) > 0 {
			models := make([]Model, 0, len(objList))
			for _, item := range objList {
				var id string
				if v, ok := item["id"].(string); ok {
					id = v
				} else if v, ok := item["name"].(string); ok {
					id = v
				}
				if id == "" {
					continue
				}
				owner := ""
				if v, ok := item["owned_by"].(string); ok {
					owner = v
				}
				models = append(models, Model{ID: id, Name: id, Provider: "openrouter", Description: owner})
			}
			if len(models) > 0 {
				return models, true
			}
		}

		return nil, false
	}

	if models, ok := tryParse(respBody); ok {
		return models, nil
	}

	// If the response didn't parse as expected, try an alternate common path
	// e.g., some local servers expose /models (without the v1 prefix).
	altURLs := []string{"/models", "/v1/models"}
	for _, suffix := range altURLs {
		alt := buildURL("https://openrouter.ai/api/v1/models", p.config.BaseURL, "", "", suffix)
		if alt == apiURL {
			continue
		}
		if body, err := llmMakeRequest(ctx, "GET", alt, headers, nil); err == nil {
			if models, ok := tryParse(body); ok {
				return models, nil
			}
		}
	}

	return nil, fmt.Errorf("failed to parse response from models endpoint, raw: %s", string(respBody))

	// nothing more to do; parsing helper already returned results or an error
}

func (p *OpenRouterProvider) SendMessage(messages []Message, stream bool, images []string, tools []OpenAITool) (string, error) {
	// If images are provided, prepend a user message with OpenAI vision content blocks
	if len(images) > 0 {
		fmt.Println("sending images")
		var blocks []ContentBlock
		for _, uri := range images {
			blocks = append(blocks, ContentBlock{
				Type: "image_url",
				ImageURL: &struct {
					URL string `json:"url"`
				}{URL: uri},
			})
		}
		imageMessage := Message{Role: "user", Content: blocks}
		messages = append([]Message{imageMessage}, messages...)
	}
	effectiveModel := p.config.Model
	if effectiveModel == "" {
		effectiveModel = p.DefaultModel()
	}
	request := map[string]interface{}{
		"model":    effectiveModel,
		"messages": messages,
	}

	// Add tools if provided
	if len(tools) > 0 {
		request["tools"] = tools
	}

	// Add response_format with JSON schema if provided
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

	// Apply deterministic settings if enabled
	if p.config.Deterministic {
		// Skip for o4 and o1 models which don't support these parameters
		modelName := strings.ToLower(effectiveModel)
		if !strings.HasPrefix(modelName, "o4") && !strings.HasPrefix(modelName, "o1") {
			request["temperature"] = 0
			request["top_p"] = 0
		}
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	// Build chat completions endpoint URL
	apiURL := buildURL("https://openrouter.ai/api/v1/chat/completions", p.config.BaseURL, "", "", "/chat/completions")

	if p.config.Debug {
		art.DebugBanner("OpenRouter Request", string(jsonData))
	}
	if stream {
		return llmMakeStreamingRequestWithTiming(p.ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
				return p.parseStreamWithTiming(r, stopCallback, firstTokenCallback, streamEndCallback)
			}, nil, nil, nil)
	}

	respBody, err := llmMakeRequest(p.ctx, "POST", apiURL,
		headers, jsonData)
	if p.config.Debug {
		art.DebugBanner("OpenRouter Response", string(respBody))
	}
	if err != nil {
		return "", err
	}
	if len(respBody) == 0 {
		return "", fmt.Errorf("empty response from %s server", p.GetName())
	}

	// Check if response is in streaming format (starts with "data: ")
	if strings.Contains(string(respBody), "data: ") {
		reader := strings.NewReader(string(respBody))
		content, err := p.parseStream(reader)
		if err != nil {
			return "", err
		}
		return content, nil
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}

	if len(response.Choices) > 0 {
		// Return raw content - newline conversion happens in the REPL
		return response.Choices[0].Message.Content, nil
	}
	fmt.Println(string(respBody))

	return "", fmt.Errorf("no content in response")
}

func (p *OpenRouterProvider) Embed(input string) ([]float64, error) {
	effectiveModel := p.config.Model
	if effectiveModel == "" {
		effectiveModel = "text-embedding-3-small" // Use embedding model by default
	}

	request := map[string]interface{}{
		"model": effectiveModel,
		"input": input,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	// Build embeddings endpoint URL
	apiURL := buildURL("https://openrouter.ai/api/v1/embeddings", p.config.BaseURL, "", "", "/embeddings")

	respBody, err := llmMakeRequest(p.ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return nil, err
	}

	if p.config.Debug {
		art.DebugBanner("OpenRouter Embed Response", string(respBody))
	}

	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, err
	}

	if response.Error.Message != "" {
		return nil, fmt.Errorf("OpenRouter API error: %s", response.Error.Message)
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return response.Data[0].Embedding, nil
}

func (p *OpenRouterProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *OpenRouterProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	return p.parseStreamWithTiming(reader, stopCallback, nil, nil)
}

func (p *OpenRouterProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
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
	for {
		select {
		case <-p.ctx.Done():
			return "", p.ctx.Err()
		default:
		}
		if !scanner.Scan() {
			break
		}
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
			raw := response.Choices[0].Delta.Content
			// Centralized demo handling
			sd.OnToken(raw)
			// Filter out think regions from printed output in demo mode
			// or if this is the start of a response where a leading
			// think block should be dropped.
			toPrint := raw
			if thinkHideEnabled || thinkDropLeading {
				toPrint = FilterOutThinkForOutput(toPrint)
			}
			// Trim leading whitespace/newlines on first visible output in demo mode
			if p.config.DemoMode && !printed {
				toPrint = strings.TrimLeft(toPrint, " \t\r\n")
			}
			// Format the content using our streaming-friendly formatter
			content := FormatStreamingChunk(toPrint, markdownEnabled)
			fmt.Print(content)
			if toPrint != "" {
				printed = true
			}
			fullResponse.WriteString(raw)
		}
	}

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			// Final flush also reports to demo
			EmitDemoTokens(final)
			// If the client prefers hidden thinking, filter all think
			// regions. Otherwise only trim a leading think block.
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
