package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
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
	return &OpenAIProvider{
		config: config,
		apiKey: GetAPIKey("OPENAI_API_KEY", "~/.r2ai.openai-key"),
	}
}

func (p *OpenAIProvider) GetName() string {
	return "OpenAI"
}

func (p *OpenAIProvider) DefaultModel() string {
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		return v
	}
	return "gpt-4o"
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Build models endpoint URL
	apiURL := buildURL("https://api.openai.com/v1/models", p.config.BaseURL, "", "", "/models")

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.apiKey,
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
	}

	var openaiResp OpenAIModelsResponse
	if err := json.Unmarshal(respBody, &openaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v, raw: %s", err, string(respBody))
	}

	// Filter out non-chat models and sort by ID
	chatModels := make([]Model, 0, len(openaiResp.Data))
	for _, m := range openaiResp.Data {
		chatModels = append(chatModels, Model{
			ID:          m.ID,
			Name:        m.ID,
			Provider:    "openai",
			Description: "Owner: " + m.OwnedBy,
		})
	}

	// Sort models alphabetically by ID
	sort.Slice(chatModels, func(i, j int) bool {
		return chatModels[i].ID < chatModels[j].ID
	})

	return chatModels, nil
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
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.apiKey,
	}

	// Build chat completions endpoint URL
	apiURL := buildURL("https://api.openai.com/v1/chat/completions", p.config.BaseURL, "", "", "/chat/completions")

	if stream {
		return llmMakeStreamingRequestWithCallback(ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader, stopCallback func()) (string, error) {
				return p.parseStreamWithCallback(r, stopCallback)
			}, nil)
	}

	respBody, err := llmMakeRequest(ctx, "POST", apiURL,
		headers, jsonData)
	if err != nil {
		return "", err
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
	firstTokenReceived := false

	// Check if markdown is enabled
	markdownEnabled := false
	markdownEnabled = p.config.Markdown

	// Reset the stream renderer if markdown is enabled
	if markdownEnabled {
		ResetStreamRenderer()
	}
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
			// Stop demo animation on first token received
			if !firstTokenReceived {
				firstTokenReceived = true
				if stopCallback != nil {
					stopCallback()
				}
			}

			content := response.Choices[0].Delta.Content

			// Format the content using our streaming-friendly formatter
			content = FormatStreamingChunk(content, markdownEnabled)

			fmt.Print(content)
			fullResponse.WriteString(response.Choices[0].Delta.Content)
		}
	}

	fmt.Println()

	// Flush any remaining content in the stream renderer buffer
	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			fmt.Print(final)
		}
	}

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}
