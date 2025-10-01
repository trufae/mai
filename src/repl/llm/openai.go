package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/art"
	"io"
	"os"
	"strings"
)

// OpenAIProvider implements the LLM provider interface for OpenAI
type OpenAIProvider struct {
	config *Config
	apiKey string
}

// OpenAIModelsResponse is the response structure for OpenAI model list endpoint
type OpenAIModelsResponse struct {
	Object string `json:"object""`
	Data   []struct {
		ID      string `json:"id""`
		Object  string `json:"object""`
		Created int64  `json:"created""`
		OwnedBy string `json:"owned_by""`
	} `json:"data""`
}

func NewOpenAIProvider(config *Config) *OpenAIProvider {
	var apiKey string
	switch strings.ToLower(config.PROVIDER) {
	case "openai":
		apiKey = GetAPIKey("OPENAI_API_KEY", "~/.r2ai.openai-key")
	case "ollamacloud":
		apiKey = GetAPIKey("OLLAMA_API_KEY", "~/.r2ai.ollama-key")
	}

	// Local OpenAI-compatible servers (LM Studio, shimmy) do not require auth.
	// Use sensible defaults when no explicit base URL has been provided so we
	// avoid unintentionally reaching the public OpenAI endpoint.
	switch strings.ToLower(config.PROVIDER) {
	case "lmstudio":
		if config.BaseURL == "" {
			config.BaseURL = "http://localhost:1234/v1"
		}
	case "shimmy":
		if config.BaseURL == "" {
			config.BaseURL = "http://localhost:11435/v1"
		}
	case "ollamacloud":
		if config.BaseURL == "" {
			config.BaseURL = "https://ollama.com/v1"
		}
	}
	return &OpenAIProvider{
		config: config,
		apiKey: apiKey,
	}
}

func (p *OpenAIProvider) GetName() string {
	switch strings.ToLower(p.config.PROVIDER) {
	case "lmstudio":
		return "LMStudio"
	case "shimmy":
		return "Shimmy"
	case "ollamacloud":
		return "OllamaCloud"
	default:
		return "OpenAI"
	}
}

func (p *OpenAIProvider) DefaultModel() string {
	switch strings.ToLower(p.config.PROVIDER) {
	case "ollamacloud":
		if v := os.Getenv("OLLAMA_MODEL"); v != "" {
			return v
		}
		return "gpt-oss:20b"
	case "lmstudio", "shimmy":
		return "local-model"
	default:
		if v := os.Getenv("OPENAI_MODEL"); v != "" {
			return v
		}
		return "gpt-4o"
	}
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Build models endpoint URL
	apiURL := buildURL("https://api.openai.com/v1/models", p.config.BaseURL, "", "", "/models")

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
		var openaiResp OpenAIModelsResponse
		if err := json.Unmarshal(body, &openaiResp); err == nil && len(openaiResp.Data) > 0 {
			models := make([]Model, 0, len(openaiResp.Data))
			for _, m := range openaiResp.Data {
				models = append(models, Model{ID: m.ID, Name: m.ID, Provider: "openai", Description: "Owner: " + m.OwnedBy})
			}
			return models, true
		}

		// 2) Plain list of model IDs: ["gpt-4", "gpt-3.5"]
		var list []string
		if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
			models := make([]Model, 0, len(list))
			for _, id := range list {
				models = append(models, Model{ID: id, Name: id, Provider: "openai"})
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
				models = append(models, Model{ID: id, Name: id, Provider: "openai", Description: owner})
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
		alt := buildURL("https://api.openai.com/v1/models", p.config.BaseURL, "", "", suffix)
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

func (p *OpenAIProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
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

	// Add response_format with JSON schema if provided
	if p.config.Schema != nil {
		if strings.ToLower(p.config.PROVIDER) == "shimmy" {
			request["format"] = p.config.Schema
		} else {
			request["response_format"] = map[string]interface{}{
				"type": "json_schema",
				"json_schema": map[string]interface{}{
					"name":   "output_schema",
					"schema": p.config.Schema,
				},
			}
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
	apiURL := buildURL("https://api.openai.com/v1/chat/completions", p.config.BaseURL, "", "", "/chat/completions")

	if p.config.Debug {
		art.DebugBanner("OpenAI Request", string(jsonData))
	}
	if stream {
		return llmMakeStreamingRequestWithCallback(ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader, stopCallback func()) (string, error) {
				return p.parseStreamWithCallback(r, stopCallback)
			}, nil)
	}

	respBody, err := llmMakeRequest(ctx, "POST", apiURL,
		headers, jsonData)
	if p.config.Debug {
		art.DebugBanner("OpenAI Response", string(respBody))
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
	fmt.Println(string(respBody))

	return "", fmt.Errorf("no content in response")
}

func (p *OpenAIProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *OpenAIProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	scanner := bufio.NewScanner(reader)
	var fullResponse strings.Builder
	sd := NewStreamDemo(stopCallback)

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
			// Filter out <think> regions from printed output in demo mode
			toPrint := raw
			if p.config.DemoMode {
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
			if p.config.DemoMode {
				trimmed := FilterOutThinkForOutput(final)
				if !printed {
					trimmed = strings.TrimLeft(trimmed, " \t\r\n")
				}
				fmt.Print(trimmed)
			} else {
				fmt.Print(final)
			}
		}
	}

	fmt.Println()

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}
