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

// image sending handled inside Provider.SendMessage

// OllamaProvider implements the LLM provider interface for Ollama
type OllamaProvider struct {
	config *Config
}

// OllamaModelsResponse is the response structure for Ollama model list endpoint
type OllamaModelsResponse struct {
	Models []struct {
		Name     string `json:"name""`
		Digest   string `json:"digest""`
		Size     int64  `json:"size""`
		Modified int64  `json:"modified""`
	} `json:"models""`
}

func NewOllamaProvider(config *Config) *OllamaProvider {
	return &OllamaProvider{
		config: config,
	}
}

func (p *OllamaProvider) GetName() string {
	return "Ollama"
}

func (p *OllamaProvider) DefaultModel() string {
	if v := os.Getenv("OLLAMA_MODEL"); v != "" {
		return v
	}
	return "gemma3:1b"
}

func (p *OllamaProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Use the configured base URL if available, otherwise construct from host/port
	url := fmt.Sprintf("http://%s:%s/api/tags", p.config.OllamaHost, p.config.OllamaPort)
	if p.config.BaseURL != "" {
		// Adjust the base URL to point to the tags endpoint
		url = strings.TrimRight(p.config.BaseURL, "/") + "/api/tags"
	}

	headers := map[string]string{}

	respBody, err := llmMakeRequest(ctx, "GET", url, headers, nil)
	if err != nil {
		return nil, err
	}
	if string(respBody)[0] != "{"[0] {
		return nil, fmt.Errorf("failed %v", string(respBody))
	}

	var ollamaResp OllamaModelsResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return nil, err
	}

	models := make([]Model, 0, len(ollamaResp.Models))
	for _, m := range ollamaResp.Models {
		// Convert size to a human-readable format for the description
		sizeDesc := ""
		if m.Size > 0 {
			size := float64(m.Size)
			unit := "B"
			if size >= 1024*1024*1024 {
				size /= 1024 * 1024 * 1024
				unit = "GB"
			} else if size >= 1024*1024 {
				size /= 1024 * 1024
				unit = "MB"
			} else if size >= 1024 {
				size /= 1024
				unit = "KB"
			}
			sizeDesc = fmt.Sprintf("Size: %.1f %s", size, unit)
		}

		models = append(models, Model{
			ID:          m.Name,
			Name:        m.Name,
			Provider:    "ollama",
			Description: sizeDesc,
		})
	}

	return models, nil
}

func (p *OllamaProvider) SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error) {
	// If images are attached, construct request injecting images into the last user message
	if len(images) > 0 {
		// Build request JSON, injecting only raw base64 images into the first/last user message
		var rawImages []string
		for _, uri := range images {
			if idx := strings.Index(uri, ","); idx != -1 && strings.HasPrefix(uri, "data:") {
				rawImages = append(rawImages, uri[idx+1:])
			} else {
				rawImages = append(rawImages, uri)
			}
		}

		var apiMessages []map[string]interface{}
		for i, m := range messages {
			msg := map[string]interface{}{
				"role":    m.Role,
				"content": m.Content,
			}
			// attach to the last message
			if i == len(messages)-1 && len(rawImages) > 0 {
				msg["images"] = rawImages
			}
			apiMessages = append(apiMessages, msg)
		}

		effectiveModel := p.config.Model
		if effectiveModel == "" {
			effectiveModel = p.DefaultModel()
		}
		request := map[string]interface{}{
			"stream":   stream,
			"model":    effectiveModel,
			"messages": apiMessages,
		}
		if p.config.Schema != nil {
			request["format"] = p.config.Schema
		}
		if p.config.Deterministic {
			request["options"] = map[string]float64{
				"repeat_last_n":  0,
				"top_p":          0.0,
				"top_k":          1.0,
				"temperature":    0.0,
				"repeat_penalty": 1.0,
				"seed":           123,
			}
		}

		jsonData, err := MarshalNoEscape(request)
		if err != nil {
			return "", err
		}

		headers := map[string]string{
			"Content-Type": "application/json",
		}

		var url string
		// If a structured output schema is requested, use the /api/generate endpoint per doc/format.md
		if p.config.Schema != nil {
			url = fmt.Sprintf("http://%s:%s/api/generate", p.config.OllamaHost, p.config.OllamaPort)
			if p.config.BaseURL != "" {
				url = strings.TrimRight(p.config.BaseURL, "/") + "/api/generate"
			}
		} else {
			// Default to /api/chat
			url = fmt.Sprintf("http://%s:%s/api/chat", p.config.OllamaHost, p.config.OllamaPort)
			if p.config.BaseURL != "" {
				url = strings.TrimRight(p.config.BaseURL, "/") + "/api/chat"
			}
		}

		if stream {
			return llmMakeStreamingRequestWithCallback(ctx, "POST", url, headers, jsonData, p.parseStreamWithCallback, nil)
		}

		respBody, err := llmMakeRequest(ctx, "POST", url, headers, jsonData)
		if err != nil {
			return "", err
		}

		var response struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}

		if err := json.Unmarshal(respBody, &response); err != nil {
			return "", err
		}

		return response.Message.Content, nil
	}
	if p.config.Rawdog {
		messageline := "" // <start_of_turn>user\nhello world<end_of_turn>\n<start_of_turn>model\n"
		for _, msg := range messages {
			messageline += msg.Content.(string)
		}
		effectiveModel := p.config.Model
		if effectiveModel == "" {
			effectiveModel = p.DefaultModel()
		}
		request := struct {
			Model   string             `json:"model""`
			Prompt  string             `json:"prompt""`
			Stream  bool               `json:"stream""`
			Format  interface{}        `json:"format,omitempty""`
			Options map[string]float64 `json:"options,omitempty""`
		}{
			Stream: stream,
			Model:  effectiveModel,
			Prompt: messageline,
		}
		if p.config.Schema != nil {
			request.Format = p.config.Schema
		}
		// Apply deterministic settings if enabled
		if p.config.Deterministic || true {
			request.Options = map[string]float64{
				"repeat_last_n":  0,
				"top_p":          0.0,
				"top_k":          1.0,
				"temperature":    0.0,
				"repeat_penalty": 1.0,
				"seed":           0,
			}
		}
		jsonData, err := MarshalNoEscape(request)
		if err != nil {
			return "", err
		}

		headers := map[string]string{
			"Content-Type": "application/json",
		}

		// fmt.Println("(send)" + string(jsonData))
		// Use the configured base URL if available, otherwise construct from host/port
		url := fmt.Sprintf("http://%s:%s/api/generate", p.config.OllamaHost, p.config.OllamaPort)
		if p.config.BaseURL != "" {
			url = strings.TrimRight(p.config.BaseURL, "/") + "/api/generate"
		}

		// stream-mode
		if stream {
			return llmMakeStreamingRequestWithCallback(ctx, "POST", url, headers, jsonData, p.parseStreamWithCallback, nil)
		}

		// non-stream
		respBody, err := llmMakeRequest(ctx, "POST", url, headers, jsonData)
		if err != nil {
			return "", err
		}
		// fmt.Println("(recv)" + string(respBody))

		// fmt.Println(string(respBody))
		var response struct {
			Response string `json:"response""`
			Error    string `json:"error,omitempty""`
		}

		if err := json.Unmarshal(respBody, &response); err != nil {
			return "", err
		}
		if response.Error != "" {
			return "", fmt.Errorf("%s", response.Error)
		}
		// Return raw content - newline conversion happens in the REPL
		return response.Response, nil
	}
	effectiveModel := p.config.Model
	if effectiveModel == "" {
		effectiveModel = p.DefaultModel()
	}
	request := struct {
		Stream   bool               `json:"stream""`
		Model    string             `json:"model""`
		Messages []Message          `json:"messages""`
		Prompt   string             `json:"prompt,omitempty""`
		Format   interface{}        `json:"format,omitempty""`
		Options  map[string]float64 `json:"options,omitempty""`
	}{
		Stream:   stream,
		Model:    effectiveModel,
		Messages: messages,
		// Prompt: "Summarize: Alice (29) likes cycling and reading",
	}
	if p.config.Schema != nil {
		request.Format = p.config.Schema
		// If conversation formatting options are not set, preserve the
		// historical behavior of using the first message as the prompt.
		// Build a conversation string according to configuration
		request.Prompt = BuildConversationString(messages, p.config.ConversationIncludeLLM, p.config.ConversationIncludeSystem, p.config.ConversationFormat, p.config.ConversationUseLastUser)
		// fmt.Println(request.Prompt)
	}

	// Apply deterministic settings if enabled
	if p.config.Deterministic {
		request.Options = map[string]float64{
			"repeat_last_n":  0,
			"top_p":          0.0,
			"top_k":          1.0,
			"temperature":    0.0,
			"repeat_penalty": 1.0,
			"seed":           123,
		}
	}

	jsonData, err := MarshalNoEscape(request)
	if err != nil {
		return "", err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Use the configured base URL if available, otherwise construct from host/port
	url := fmt.Sprintf("http://%s:%s/api/chat", p.config.OllamaHost, p.config.OllamaPort)
	if p.config.BaseURL != "" {
		url = strings.TrimRight(p.config.BaseURL, "/") + "/api/chat"
	}
	// If a structured output schema is requested, use the /api/generate endpoint per doc/format.md
	if p.config.Schema != nil {
		url = fmt.Sprintf("http://%s:%s/api/generate", p.config.OllamaHost, p.config.OllamaPort)
		if p.config.BaseURL != "" {
			url = strings.TrimRight(p.config.BaseURL, "/") + "/api/generate"
		}
	}

	if stream {
		return llmMakeStreamingRequestWithCallback(ctx, "POST", url, headers, jsonData, p.parseStreamWithCallback, nil)
	}

	respBody, err := llmMakeRequest(ctx, "POST", url, headers, jsonData)
	if err != nil {
		return "", err
	}

	var response struct {
		Message struct {
			Content string `json:"content""`
		} `json:"message""`
		Response string `json:"response,omitempty""`
		Error    string `json:"error,omitempty""`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return "", err
	}
	if response.Error != "" {
		return "", fmt.Errorf("%s", response.Error)
	}
	if p.config.Schema != nil {
		return response.Response, nil
	}

	// Return raw content - newline conversion happens in the REPL
	return response.Message.Content, nil
}

func (p *OllamaProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *OllamaProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
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
		if line == "" {
			continue
		}

		isDone := false
		raw := ""
		if p.config.Rawdog {
			var response struct {
				Response string `json:"response""`
				Done     bool   `json:"done""`
			}

			if err := json.Unmarshal([]byte(line), &response); err != nil {
				continue
			}

			raw = response.Response
			isDone = response.Done
		} else {
			var response struct {
				Message struct {
					Content string `json:"content""`
				} `json:"message""`
				Response string `json:"response,omitempty""`
				Done     bool   `json:"done""`
			}

			if err := json.Unmarshal([]byte(line), &response); err != nil {
				continue
			}
			if response.Response != "" {
				raw = response.Response
			} else {
				raw = response.Message.Content
			}
			isDone = response.Done
		}

		// Stop demo animation on first token received
		if !firstTokenReceived && raw != "" {
			firstTokenReceived = true
			if stopCallback != nil {
				stopCallback()
			}
		}

		// Format for printing only, keep raw for storage
		formatted := FormatStreamingChunk(raw, markdownEnabled)
		fmt.Print(formatted)
		fullResponse.WriteString(raw)

		if isDone {
			break
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
