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

// XAIProvider implements the LLM provider interface for xAI
type XAIProvider struct {
	BaseProvider
}

// XAIModelsResponse is the response structure for xAI model list endpoint
type XAIModelsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

func NewXAIProvider(config *Config, ctx context.Context) *XAIProvider {
	return &XAIProvider{
		BaseProvider: BaseProvider{
			config: config,
			apiKey: GetAPIKey("XAI_API_KEY", "~/.r2ai.xai-key"),
			ctx:    ctx,
		},
	}
}

func (p *XAIProvider) GetName() string {
	return "xAI"
}

func (p *XAIProvider) DefaultModel() string {
	if v := os.Getenv("XAI_MODEL"); v != "" {
		return v
	}
	return "grok-2-1212"
}

func (p *XAIProvider) IsAvailable() bool {
	// xAI requires API key
	return p.apiKey != ""
}

func (p *XAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Build models endpoint URL
	apiURL := buildURL("https://api.x.ai/v1/models", p.config.BaseURL, "", "", "/models")

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + p.apiKey,
	}

	respBody, err := llmMakeRequest(ctx, "GET", apiURL, headers, nil)
	if err != nil {
		return nil, err
	}

	var xaiResp XAIModelsResponse
	if err := json.Unmarshal(respBody, &xaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v, raw: %s", err, string(respBody))
	}

	// Filter out non-chat models and sort by ID
	chatModels := make([]Model, 0, len(xaiResp.Data))
	for _, m := range xaiResp.Data {
		chatModels = append(chatModels, Model{
			ID:          m.ID,
			Name:        m.ID,
			Provider:    "xai",
			Description: "Owner: " + m.OwnedBy,
		})
	}

	// Sort models alphabetically by ID
	sort.Slice(chatModels, func(i, j int) bool {
		return chatModels[i].ID < chatModels[j].ID
	})

	return chatModels, nil
}

func (p *XAIProvider) SendMessage(messages []Message, stream bool, images []string) (string, error) {
	// If images are provided, prepend a user message with xAI vision content blocks
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
	apiURL := buildURL("https://api.x.ai/v1/chat/completions", p.config.BaseURL, "", "", "/chat/completions")

	if stream {
		return llmMakeStreamingRequestWithTiming(p.ctx, "POST", apiURL,
			headers, jsonData, func(r io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
				return p.parseStreamWithTiming(r, stopCallback, firstTokenCallback, streamEndCallback)
			}, nil, nil, nil)
	}

	respBody, err := llmMakeRequest(p.ctx, "POST", apiURL,
		headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
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

func (p *XAIProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *XAIProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	return p.parseStreamWithTiming(reader, stopCallback, nil, nil)
}

func (p *XAIProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
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
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			raw := response.Choices[0].Delta.Content
			// Centralized demo handling
			sd.OnToken(raw)
			// Filter out <think> regions from printed output in demo mode or when
			// dropping a leading think block for this request.
			toPrint := raw
			if p.config.DemoMode || thinkDropLeading {
				toPrint = FilterOutThinkForOutput(toPrint)
			}
			// Trim leading whitespace/newlines on first visible output in demo mode
			if p.config.DemoMode && !printed {
				toPrint = strings.TrimLeft(toPrint, " \t\r\n")
			}
			// Format and print
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
