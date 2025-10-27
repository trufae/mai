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

// OllamaProvider implements the LLM provider interface for Ollama
type OllamaProvider struct {
	BaseProvider
}

func NewOllamaProvider(config *Config, ctx context.Context) *OllamaProvider {
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:11434"
	}
	return &OllamaProvider{
		BaseProvider: BaseProvider{
			config: config,
			ctx:    ctx,
		},
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

func (p *OllamaProvider) IsAvailable() bool {
	// For Ollama, assume available if we can reach the base URL
	// Simple check: try to connect to the base URL
	url := p.config.BaseURL + "/api/version"
	req, err := http.NewRequestWithContext(p.ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *OllamaProvider) ListModels(ctx context.Context) ([]Model, error) {
	url := p.config.BaseURL + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var response struct {
		Models []struct {
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			Modified string `json:"modified"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}
	models := make([]Model, len(response.Models))
	for i, m := range response.Models {
		models[i] = Model{
			ID:       m.Name,
			Name:     m.Name,
			Provider: "ollama",
		}
	}
	return models, nil
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

// image sending handled inside Provider.SendMessage
func (p *OllamaProvider) SendMessage(messages []Message, stream bool, images []string, tools []OpenAITool) (string, error) {

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
		if p.config.Deterministic || !p.config.ThinkHide {
			options := make(map[string]interface{})
			if p.config.Deterministic {
				options["repeat_last_n"] = 0.0
				options["top_p"] = 0.0
				options["top_k"] = 1.0
				options["temperature"] = 0.0
				options["repeat_penalty"] = 1.0
				options["seed"] = 123.0
			}
			if !p.config.ThinkHide {
				options["reasoning"] = true
			}
			request["options"] = options
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
			return tryPostCandidatesStream(p.ctx, candidates, headers, jsonData, p.parseStreamWithTiming)
		}

		respBody, err := tryPostCandidatesNonStream(p.ctx, candidates, headers, jsonData)
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
		if err := json.Unmarshal(respBody, &response); err != nil {
			return "", err
		}

		if p.config.Debug {
			fmt.Printf("DEBUG: Ollama response: content=%q thinking=%q\n", response.Message.Content, response.Message.Thinking)
		}

		// Handle tool_calls if content is empty
		if len(response.Message.ToolCalls) > 0 {
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

		content := response.Message.Content
		if response.Message.Thinking != "" && !p.config.ThinkHide {
			content = "\033[36m" + response.Message.Thinking + "\033[0m" + content
		}
		if p.config.Debug {
			fmt.Printf("DEBUG: Final content: %q\n", content)
		}
		if content == "" {
			fmt.Printf("DEBUG: Ollama provider returned empty content in image mode. Response body: %s\n", string(respBody))
		}
		return content, nil
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
			return tryPostCandidatesStream(p.ctx, candidates, headers, jsonData, p.parseStreamWithTiming)
		}

		respBody, err := tryPostCandidatesNonStream(p.ctx, candidates, headers, jsonData)
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
	candidates := []string{
		buildURL("", p.config.BaseURL, "", "", "/api/chat"),
		buildURL("", p.config.BaseURL, "", "", "/v1/chat/completions"),
		buildURL("", p.config.BaseURL, "", "", "/api/generate"),
		buildURL("", p.config.BaseURL, "", "", "/v1/generate"),
	}

	if p.config.Debug {
		art.DebugBanner("Ollama Request", string(jsonData))
	}
	if stream {
		parseFunc := func(reader io.Reader, stop, first, end func()) (string, error) {
			return p.parseStreamWithTiming(reader, stop, first, end)
		}
		return tryPostCandidatesStream(p.ctx, candidates, headers, jsonData, parseFunc)
	}

	respBody, err := tryPostCandidatesNonStream(p.ctx, candidates, headers, jsonData)
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
		if response.Message.Content != "" {
			return response.Message.Content, nil
		}
		// Handle tool_calls if present
		if len(response.Message.ToolCalls) > 0 {
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
		if response.Response == "" {
			if response.Message.Thinking != "" {
				art.DebugBanner("Thinking", response.Message.Thinking)
			}
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
		if line == "" {
			continue
		}
		if p.config.Debug {
			fmt.Println("")
			art.DebugBanner("Ollama Stream", string(line))
		}

		isDone := false
		raw := ""
		isThinkingChunk := false

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
					Content  string `json:"content"`
					Thinking string `json:"thinking,omitempty"`
				} `json:"message"`
				Response string `json:"response,omitempty"`
				Done     bool   `json:"done"`
			}

			if err := json.Unmarshal([]byte(line), &response); err != nil {
				continue
			}
			if response.Response != "" {
				raw = response.Response
			} else if response.Message.Thinking != "" {
				if !thinkHideEnabled {
					raw = response.Message.Thinking
					isThinkingChunk = true
				} // else skip thinking
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
		if thinkHideEnabled || thinkDropLeading {
			toPrint = FilterOutThinkForOutput(toPrint)
		}
		// Color thinking chunks in cyan
		if isThinkingChunk {
			toPrint = "\033[36m" + toPrint + "\033[0m"
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
