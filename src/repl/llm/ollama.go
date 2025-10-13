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

// image sending handled inside Provider.SendMessage

// OllamaProvider implements the LLM provider interface for Ollama
type OllamaProvider struct {
	config *Config
}

// OllamaModelsResponse is the response structure for Ollama model list endpoint
type OllamaModelsResponse struct {
	Models []struct {
		Name     string `json:"name"`
		Digest   string `json:"digest"`
		Size     int64  `json:"size"`
		Modified int64  `json:"modified"`
	} `json:"models"`
}

func NewOllamaProvider(config *Config) *OllamaProvider {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:11434"
	}
	return &OllamaProvider{
		config: config,
	}
}

// tryPostCandidatesNonStream POSTs to each candidate URL until one succeeds
func tryPostCandidatesNonStream(ctx context.Context, candidates []string, headers map[string]string, body []byte) ([]byte, error) {
	var lastErr error
	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		if cand == "" || cand == "/" {
			continue
		}
		respBody, err := llmMakeRequest(ctx, "POST", cand, headers, body)
		if err == nil {
			return respBody, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no valid endpoints")
}

// tryPostCandidatesStream POSTs to each candidate streaming endpoint until one succeeds
func tryPostCandidatesStream(ctx context.Context, candidates []string, headers map[string]string, body []byte, parser func(io.Reader, func(), func(), func()) (string, error)) (string, error) {
	var lastErr error
	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		if cand == "" || cand == "/" {
			continue
		}
		res, err := llmMakeStreamingRequestWithTiming(ctx, "POST", cand, headers, body, parser, nil, nil, nil)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no valid streaming endpoints")
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

func (p *OllamaProvider) IsAvailable() bool {
	// Ollama is a local service, check HTTP endpoint
	baseURL := p.config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Head(baseURL + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

func (p *OllamaProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Try multiple endpoints and parsing strategies to be tolerant of
	// different local/model server implementations (e.g., shimmy).
	// Use buildURL so an empty BaseURL does not produce relative paths like
	// "/v1/models" which would cause request errors.
	headers := map[string]string{}
	candidates := []string{
		buildURL("", p.config.BaseURL, "", "", "/api/tags"),
		// common alternatives
		buildURL("", p.config.BaseURL, "", "", "/v1/models"),
		buildURL("", p.config.BaseURL, "", "", "/models"),
		buildURL("", p.config.BaseURL, "", "", "/api/models"),
	}

	// Build a parsing helper that tries several JSON shapes
	tryParse := func(body []byte) ([]Model, bool) {
		if len(body) == 0 {
			return nil, false
		}

		// 1) Ollama tags response: {"models":[{name,digest,size,modified}, ...]}
		var oresp OllamaModelsResponse
		if err := json.Unmarshal(body, &oresp); err == nil && len(oresp.Models) > 0 {
			out := make([]Model, 0, len(oresp.Models))
			for _, m := range oresp.Models {
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
				out = append(out, Model{ID: m.Name, Name: m.Name, Provider: "ollama", Description: sizeDesc})
			}
			return out, true
		}

		// 2) OpenAI-style response: {"object":"list","data":[{id:...,owned_by:...}, ...]}
		var openaiResp struct {
			Data []struct {
				ID      string `json:"id"`
				OwnedBy string `json:"owned_by"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &openaiResp); err == nil && len(openaiResp.Data) > 0 {
			out := make([]Model, 0, len(openaiResp.Data))
			for _, m := range openaiResp.Data {
				out = append(out, Model{ID: m.ID, Name: m.ID, Provider: "ollama", Description: "Owner: " + m.OwnedBy})
			}
			return out, true
		}

		// 3) Plain array of names
		var names []string
		if err := json.Unmarshal(body, &names); err == nil && len(names) > 0 {
			out := make([]Model, 0, len(names))
			for _, n := range names {
				out = append(out, Model{ID: n, Name: n, Provider: "ollama"})
			}
			return out, true
		}

		// 4) Generic object array
		var objList []map[string]interface{}
		if err := json.Unmarshal(body, &objList); err == nil && len(objList) > 0 {
			out := make([]Model, 0, len(objList))
			for _, item := range objList {
				id := ""
				if v, ok := item["id"].(string); ok {
					id = v
				} else if v, ok := item["name"].(string); ok {
					id = v
				}
				if id == "" {
					continue
				}
				out = append(out, Model{ID: id, Name: id, Provider: "ollama"})
			}
			if len(out) > 0 {
				return out, true
			}
		}

		return nil, false
	}

	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		if cand == "" || cand == "/" {
			continue
		}
		respBody, err := llmMakeRequest(ctx, "GET", cand, headers, nil)
		if err != nil {
			continue
		}
		if models, ok := tryParse(respBody); ok {
			return models, nil
		}
	}

	return nil, fmt.Errorf("invalid response from Ollama API (tried %d endpoints)", len(candidates))
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

		// Choose candidate endpoints to try; prefer /api/generate when a schema
		candidates := []string{
			buildURL("", p.config.BaseURL, "", "", "/api/generate"),
			buildURL("", p.config.BaseURL, "", "", "/v1/generate"),
			buildURL("", p.config.BaseURL, "", "", "/v1/chat/completions"),
			buildURL("", p.config.BaseURL, "", "", "/api/chat"),
		}

		if stream {
			return tryPostCandidatesStream(ctx, candidates, headers, jsonData, p.parseStreamWithTiming)
		}

		respBody, err := tryPostCandidatesNonStream(ctx, candidates, headers, jsonData)
		if err != nil {
			return "", err
		}

		var response struct {
			Message struct {
				Content   string `json:"content"`
				Thinking  string `json:"thinking,omitempty"`
				ToolCalls []struct {
					Function struct {
						Name      string                 `json:"name"`
						Arguments map[string]interface{} `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls,omitempty"`
			} `json:"message"`
		}
		fmt.Println(respBody)
		if err := json.Unmarshal(respBody, &response); err != nil {
			return "", err
		}

		// Handle tool_calls if content is empty
		if response.Message.Content == "" && len(response.Message.ToolCalls) > 0 {
			// Construct JSON response for tool calling
			toolCall := response.Message.ToolCalls[0] // Assume one tool call
			planResponse := map[string]interface{}{
				"plan":               []string{"Call tool " + toolCall.Function.Name},
				"current_plan_index": 0,
				"progress":           response.Message.Thinking,
				"reasoning":          response.Message.Thinking,
				"next_step":          "Execute the tool",
				"action":             "Iterate",
				"tool_required":      true,
				"tool":               toolCall.Function.Name,
				"tool_params":        toolCall.Function.Arguments,
			}
			jsonBytes, err := json.Marshal(planResponse)
			if err != nil {
				return "", fmt.Errorf("failed to marshal tool call response: %v", err)
			}
			return string(jsonBytes), nil
		}

		if response.Message.Content == "" {
			fmt.Printf("DEBUG: Ollama provider returned empty content in image mode. Response body: %s\n", string(respBody))
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
		if p.config.Deterministic {
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
		// Try multiple endpoints for Rawdog mode too.
		candidates := []string{
			buildURL("", p.config.BaseURL, "", "", "/api/generate"),
			buildURL("", p.config.BaseURL, "", "", "/v1/generate"),
			buildURL("", p.config.BaseURL, "", "", "/api/chat"),
			buildURL("", p.config.BaseURL, "", "", "/v1/chat/completions"),
		}

		if stream {
			if p.config.Debug {
				art.DebugBanner("Ollama Request", string(jsonData))
			}
			return tryPostCandidatesStream(ctx, candidates, headers, jsonData, p.parseStreamWithTiming)
		}

		respBody, err := tryPostCandidatesNonStream(ctx, candidates, headers, jsonData)
		if err != nil {
			return "", err
		}

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
		if response.Response == "" {
			fmt.Printf("DEBUG: Ollama provider returned empty response in rawdog mode. Response body: %s\n", string(respBody))
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

	// Build candidate endpoints and try them (handles shimmy/ollama/openai-like servers)
	var candidates []string
	if p.config.Schema != nil {
		candidates = []string{
			buildURL("", p.config.BaseURL, "", "", "/api/generate"),
			buildURL("", p.config.BaseURL, "", "", "/v1/generate"),
			buildURL("", p.config.BaseURL, "", "", "/v1/chat/completions"),
			buildURL("", p.config.BaseURL, "", "", "/api/chat"),
		}
	} else {
		candidates = []string{
			buildURL("", p.config.BaseURL, "", "", "/api/chat"),
			buildURL("", p.config.BaseURL, "", "", "/v1/chat/completions"),
			buildURL("", p.config.BaseURL, "", "", "/api/generate"),
			buildURL("", p.config.BaseURL, "", "", "/v1/generate"),
		}
	}

	if p.config.Debug {
		art.DebugBanner("Ollama Request", string(jsonData))
	}
	if stream {
		return tryPostCandidatesStream(ctx, candidates, headers, jsonData, p.parseStreamWithTiming)
	}

	respBody, err := tryPostCandidatesNonStream(ctx, candidates, headers, jsonData)
	if err != nil {
		return "", err
	}
	if len(respBody) == 0 {
		return "", fmt.Errorf("empty response from %s server", p.GetName())
	}
	if p.config.Debug {
		art.DebugBanner("Ollama Response", string(respBody))
	}

	var response struct {
		Message struct {
			Content   string `json:"content"`
			Thinking  string `json:"thinking,omitempty"`
			ToolCalls []struct {
				Function struct {
					Name      string                 `json:"name"`
					Arguments map[string]interface{} `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"message"`
		Response string `json:"response,omitempty"`
		Error    string `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		fmt.Printf("Failed to parse Ollama response: %s\nError: %v\n", string(respBody), err)
		return "", err
	}
	if response.Error != "" {
		return "", fmt.Errorf("%s", response.Error)
	}
	if p.config.Schema != nil {
		if response.Response == "" {
			fmt.Printf("DEBUG: Ollama provider returned empty response with schema. Response body: %s\n", string(respBody))
			return "", fmt.Errorf("LLM returned empty response in schema mode - this may indicate the model cannot generate valid structured output")
		}
		return response.Response, nil
	}

	// Handle tool_calls if content is empty
	if response.Message.Content == "" && len(response.Message.ToolCalls) > 0 {
		// Construct JSON response for tool calling
		toolCall := response.Message.ToolCalls[0] // Assume one tool call
		planResponse := map[string]interface{}{
			"plan":               []string{"Call tool " + toolCall.Function.Name},
			"current_plan_index": 0,
			"progress":           response.Message.Thinking,
			"reasoning":          response.Message.Thinking,
			"next_step":          "Execute the tool",
			"action":             "Iterate",
			"tool_required":      true,
			"tool":               toolCall.Function.Name,
			"tool_params":        toolCall.Function.Arguments,
		}
		jsonBytes, err := json.Marshal(planResponse)
		if err != nil {
			return "", fmt.Errorf("failed to marshal tool call response: %v", err)
		}
		return string(jsonBytes), nil
	}

	// Return raw content - newline conversion happens in the REPL
	if response.Message.Content == "" {
		fmt.Printf("DEBUG: Ollama provider returned empty message content. Response body: %s\n", string(respBody))
	}
	return response.Message.Content, nil
}

func (p *OllamaProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *OllamaProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	return p.parseStreamWithTiming(reader, stopCallback, nil, nil)
}

func (p *OllamaProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
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
		if p.config.Debug {
			fmt.Println("")
			art.DebugBanner("Ollama Stream", string(line))
		}

		isDone := false
		raw := ""

		// Check for OpenAI-style streaming (data: prefix)
		if strings.Contains(line, "data: ") {
			data := strings.Split(line, "data: ")[1]
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
				raw = response.Choices[0].Delta.Content
			}
			// OpenAI-style doesn't have a done flag, so we continue until [DONE]
			isDone = false
		} else if p.config.Rawdog {
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

		// Centralized demo handling
		sd.OnToken(raw)
		// Filter out <think> regions from printed output in demo mode
		// or when we are dropping a leading think block for this request.
		toPrint := raw
		if p.config.DemoMode || thinkDropLeading {
			toPrint = FilterOutThinkForOutput(toPrint)
		}
		// Trim leading whitespace/newlines on first visible output in demo mode
		if p.config.DemoMode && !printed {
			toPrint = strings.TrimLeft(toPrint, " \t\r\n")
		}
		// Format for printing only, keep raw for storage
		formatted := FormatStreamingChunk(toPrint, markdownEnabled)
		fmt.Print(formatted)
		if toPrint != "" {
			printed = true
		}
		fullResponse.WriteString(raw)

		if isDone {
			break
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

	if p.config.Debug {
		art.DebugBanner("Ollama Response", fullResponse.String())
	}

	return fullResponse.String(), nil
}
