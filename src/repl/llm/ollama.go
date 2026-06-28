package llm

import (
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

type ollamaRequest struct {
	Stream   bool               `json:"stream"`
	Model    string             `json:"model"`
	Messages interface{}        `json:"messages,omitempty"`
	Prompt   string             `json:"prompt,omitempty"`
	System   string             `json:"system,omitempty"`
	Images   []string           `json:"images,omitempty"`
	Think    interface{}        `json:"think,omitempty"`
	Format   interface{}        `json:"format,omitempty"`
	Options  map[string]float64 `json:"options,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

type ollamaResponse struct {
	Message struct {
		Content   string           `json:"content"`
		Thinking  string           `json:"thinking,omitempty"`
		ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	Response string `json:"response,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Error    string `json:"error,omitempty"`
}

const (
	ollamaAPITypeChat     = "chat"
	ollamaAPITypeGenerate = "generate"
)

func NormalizeOllamaAPIType(value string) (string, bool) {
	apiType := strings.ToLower(strings.TrimSpace(value))
	switch apiType {
	case "", ollamaAPITypeChat:
		return ollamaAPITypeChat, true
	case ollamaAPITypeGenerate:
		return ollamaAPITypeGenerate, true
	default:
		return "", false
	}
}

func OllamaAPITypeValues() string {
	return ollamaAPITypeChat + ", " + ollamaAPITypeGenerate
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

func (p *OllamaProvider) apiType() string {
	if apiType, ok := NormalizeOllamaAPIType(p.config.APIType); ok {
		return apiType
	}
	return ollamaAPITypeChat
}

func ollamaEndpointCandidates(baseURL, apiType string) []string {
	switch apiType {
	case ollamaAPITypeGenerate:
		return []string{
			buildURL("", baseURL, "", "", "/api/generate"),
			buildURL("", baseURL, "", "", "/v1/generate"),
		}
	default:
		return []string{
			buildURL("", baseURL, "", "", "/api/chat"),
			buildURL("", baseURL, "", "", "/v1/chat/completions"),
		}
	}
}

func ollamaGeneratePromptFromMessages(messages []Message) (string, string) {
	var prompt strings.Builder
	system := ""
	for _, msg := range messages {
		content := msg.Content
		if strings.TrimSpace(content) == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			role = "user"
		}
		if role == "system" || role == "developer" {
			if system == "" {
				system = content
				continue
			}
		}
		prompt.WriteString(role)
		prompt.WriteString(": ")
		prompt.WriteString(content)
		prompt.WriteString("\n\n")
	}
	text := strings.TrimSpace(prompt.String())
	if text == "" {
		text = system
	}
	return text, system
}

func ollamaRawPrompt(messages []Message) string {
	var prompt strings.Builder
	for _, msg := range messages {
		prompt.WriteString(msg.Content)
	}
	return prompt.String()
}

func ollamaRawImages(images []string) []string {
	rawImages := make([]string, 0, len(images))
	for _, uri := range images {
		if idx := strings.Index(uri, ","); idx != -1 && strings.HasPrefix(uri, "data:") {
			rawImages = append(rawImages, uri[idx+1:])
		} else {
			rawImages = append(rawImages, uri)
		}
	}
	return rawImages
}

func ollamaMessagesWithImages(messages []Message, rawImages []string) []map[string]interface{} {
	var apiMessages []map[string]interface{}
	for i, msg := range messages {
		apiMessage := map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if i == len(messages)-1 && len(rawImages) > 0 {
			apiMessage["images"] = rawImages
		}
		apiMessages = append(apiMessages, apiMessage)
	}
	return apiMessages
}

func ollamaDeterministicOptions(seed float64) map[string]float64 {
	return map[string]float64{
		"repeat_last_n":  0,
		"top_p":          0,
		"top_k":          1,
		"temperature":    0,
		"repeat_penalty": 1,
		"seed":           seed,
	}
}

func (r *ollamaRequest) setInput(apiType string, messages interface{}, prompt, system string) {
	if apiType == ollamaAPITypeGenerate {
		r.Prompt = prompt
		r.System = system
		return
	}
	r.Messages = messages
}

func (p *OllamaProvider) effectiveModel() string {
	if p.config.Model != "" {
		return p.config.Model
	}
	return p.DefaultModel()
}

func (p *OllamaProvider) newOllamaRequest(model string, stream bool, seed float64) ollamaRequest {
	request := ollamaRequest{
		Stream: stream,
		Model:  model,
	}
	if think, ok := ollamaThinkValue(model, p.config.ReasoningEffort); ok {
		request.Think = think
	}
	if p.config.Schema != nil {
		request.Format = p.config.Schema
	}
	if p.config.Deterministic {
		request.Options = ollamaDeterministicOptions(seed)
	}
	return request
}

func ollamaHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

func (p *OllamaProvider) ollamaRequestJSON(request ollamaRequest) ([]byte, error) {
	jsonData, err := MarshalNoEscape(request)
	if err != nil {
		return nil, err
	}
	if p.config.Debug {
		art.DebugBanner("Ollama Request", string(jsonData))
	}
	return jsonData, nil
}

func (p *OllamaProvider) postOllama(apiType string, request ollamaRequest) ([]byte, error) {
	jsonData, err := p.ollamaRequestJSON(request)
	if err != nil {
		return nil, err
	}
	respBody, err := tryPostCandidatesNonStream(p.ctx, ollamaEndpointCandidates(p.config.BaseURL, apiType), ollamaHeaders(), jsonData)
	if err != nil {
		return nil, err
	}
	if len(respBody) == 0 {
		return nil, fmt.Errorf("empty response from %s server", p.GetName())
	}
	if p.config.Debug {
		art.DebugBanner("Ollama Response", string(respBody))
	}
	return respBody, nil
}

func (p *OllamaProvider) streamOllama(apiType string, request ollamaRequest) (string, error) {
	jsonData, err := p.ollamaRequestJSON(request)
	if err != nil {
		return "", err
	}
	return tryPostCandidatesStream(p.ctx, ollamaEndpointCandidates(p.config.BaseURL, apiType), ollamaHeaders(), jsonData, p.parseStreamWithTiming)
}

func parseOllamaResponse(respBody []byte) (ollamaResponse, string, string, error) {
	var response ollamaResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return response, "", "", err
	}
	if response.Error != "" {
		return response, "", "", fmt.Errorf("%s", response.Error)
	}
	content := response.Message.Content
	thinking := response.Message.Thinking
	if response.Response != "" {
		content = response.Response
	}
	if response.Thinking != "" {
		thinking = response.Thinking
	}
	return response, content, thinking, nil
}

func ollamaToolPlan(toolCall ollamaToolCall, thinking string) (string, error) {
	planResponse := map[string]interface{}{
		"plan":               []string{"Call tool " + toolCall.Function.Name},
		"current_plan_index": 0,
		"progress":           thinking,
		"reasoning":          thinking,
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	apiType := p.apiType()
	model := p.effectiveModel()
	if len(images) > 0 {
		return p.sendOllamaImageMessage(apiType, model, messages, stream, images)
	}
	if p.config.Rawdog {
		return p.sendOllamaRawMessage(apiType, model, messages, stream)
	}
	return p.sendOllamaChatMessage(apiType, model, messages, stream)
}

func (p *OllamaProvider) sendOllamaImageMessage(apiType, model string, messages []Message, stream bool, images []string) (string, error) {
	rawImages := ollamaRawImages(images)
	request := p.newOllamaRequest(model, stream, 123)
	if apiType == ollamaAPITypeGenerate {
		prompt, system := ollamaGeneratePromptFromMessages(messages)
		request.setInput(apiType, nil, prompt, system)
		request.Images = rawImages
	} else {
		request.setInput(apiType, ollamaMessagesWithImages(messages, rawImages), "", "")
	}
	if stream {
		return p.streamOllama(apiType, request)
	}
	respBody, err := p.postOllama(apiType, request)
	if err != nil {
		return "", err
	}
	return p.finishOllamaResponse(respBody, "content in image mode", false, true, true)
}

func (p *OllamaProvider) sendOllamaRawMessage(apiType, model string, messages []Message, stream bool) (string, error) {
	prompt := ollamaRawPrompt(messages)
	request := p.newOllamaRequest(model, stream, 0)
	request.setInput(apiType, []Message{{Role: "user", Content: prompt}}, prompt, "")
	if stream {
		return p.streamOllama(apiType, request)
	}
	respBody, err := p.postOllama(apiType, request)
	if err != nil {
		return "", err
	}
	return p.finishOllamaResponse(respBody, "response in rawdog mode", false, false, false)
}

func (p *OllamaProvider) sendOllamaChatMessage(apiType, model string, messages []Message, stream bool) (string, error) {
	request := p.newOllamaRequest(model, stream, 123)
	if apiType == ollamaAPITypeGenerate {
		prompt, system := ollamaGeneratePromptFromMessages(messages)
		request.setInput(apiType, nil, prompt, system)
	} else {
		request.setInput(apiType, messages, "", "")
	}
	if p.config.Schema != nil && apiType != ollamaAPITypeGenerate {
		request.Prompt = BuildConversationString(messages, p.config.ConversationIncludeLLM, p.config.ConversationIncludeSystem, p.config.ConversationFormat, p.config.ConversationUseLastUser)
	}
	if stream {
		return p.streamOllama(apiType, request)
	}
	respBody, err := p.postOllama(apiType, request)
	if err != nil {
		return "", err
	}
	return p.finishOllamaResponse(respBody, "message content", p.config.Schema != nil, false, false)
}

func (p *OllamaProvider) finishOllamaResponse(respBody []byte, emptyLabel string, schemaMode, colorThinking, account bool) (string, error) {
	response, content, thinking, err := parseOllamaResponse(respBody)
	if err != nil {
		return "", err
	}
	if p.config.Debug {
		fmt.Printf("DEBUG: Ollama response: content=%q thinking=%q\n", content, thinking)
	}
	if account {
		accountResponseText(p.ctx, thinking)
		accountResponseText(p.ctx, content)
	}
	if content == "" && len(response.Message.ToolCalls) > 0 {
		return ollamaToolPlan(response.Message.ToolCalls[0], thinking)
	}
	if schemaMode && content == "" {
		if thinking != "" {
			art.DebugBanner("Thinking", thinking)
		}
		fmt.Printf("DEBUG: Ollama provider returned empty response with schema. Response body: %s\n", string(respBody))
		return "", fmt.Errorf("LLM returned empty response in schema mode - this may indicate the model cannot generate valid structured output")
	}
	if colorThinking && thinking != "" && !p.config.ThinkHide {
		content = "\033[36m" + thinking + "\033[0m" + content
	}
	if p.config.Debug {
		fmt.Printf("DEBUG: Final content: %q\n", content)
	}
	if content == "" {
		fmt.Printf("DEBUG: Ollama provider returned empty %s. Response body: %s\n", emptyLabel, string(respBody))
	}
	return content, nil
}

func (p *OllamaProvider) parseStream(reader io.Reader) (string, error) {
	return p.parseStreamWithCallback(reader, nil)
}

func (p *OllamaProvider) parseStreamWithCallback(reader io.Reader, stopCallback func()) (string, error) {
	return p.parseStreamWithTiming(reader, stopCallback, nil, nil)
}

func (p *OllamaProvider) parseStreamWithTiming(reader io.Reader, stopCallback, firstTokenCallback, streamEndCallback func()) (string, error) {
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
	streamErr := streamEachLine(p.ctx, reader, func(line string) (bool, error) {
		if line == "" {
			return false, nil
		}
		if p.config.Debug {
			fmt.Println("")
			art.DebugBanner("Ollama Stream", string(line))
		}

		isDone := false
		raw := ""
		isThinkingChunk := false

		// Check for OpenAI-style streaming (data: prefix)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return true, nil
			}
			content, reasoning := extractOpenAIStreamDelta(data)
			for _, text := range reasoning {
				sd.OnToken(text)
				accountResponseText(p.ctx, text)
			}
			if content != "" {
				raw = content
			}
			// OpenAI-style doesn't have a done flag, so we continue until [DONE]
			isDone = false
		} else if p.config.Rawdog {
			var response struct {
				Response string `json:"response"`
				Message  struct {
					Content string `json:"content"`
				} `json:"message"`
				Done bool `json:"done"`
			}

			if err := json.Unmarshal([]byte(line), &response); err != nil {
				return false, nil
			}

			raw = response.Response
			if raw == "" {
				raw = response.Message.Content
			}
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
				return false, nil
			}
			if response.Response != "" {
				raw = response.Response
			} else if response.Message.Thinking != "" {
				sd.OnToken(response.Message.Thinking)
				if thinkHideEnabled {
					accountResponseText(p.ctx, response.Message.Thinking)
				} else {
					raw = response.Message.Thinking
					isThinkingChunk = true
				}
			} else {
				raw = response.Message.Content
			}
			isDone = response.Done
		}

		// Centralized demo handling
		if raw != "" {
			sd.OnToken(raw)
		}
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
		appendResponseText(&fullResponse, p.ctx, raw)

		if isDone {
			return true, nil
		}
		return false, nil
	})
	if p.ctx != nil && p.ctx.Err() != nil && streamErr == p.ctx.Err() {
		return "", streamErr
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

	if streamErr != nil {
		return fullResponse.String(), streamErr
	}

	if p.config.Debug {
		art.DebugBanner("Ollama Response", fullResponse.String())
	}

	return fullResponse.String(), nil
}

func (p *OllamaProvider) Embed(input string) ([]float64, error) {
	effectiveModel := p.config.Model
	if effectiveModel == "" {
		effectiveModel = p.DefaultModel()
	}

	request := map[string]interface{}{
		"model":  effectiveModel,
		"prompt": input,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Build embeddings endpoint URL
	apiURL := buildURL("", p.config.BaseURL, "", "", "/api/embeddings")

	respBody, err := llmMakeRequest(p.ctx, "POST", apiURL, headers, jsonData)
	if err != nil {
		return nil, err
	}

	var response struct {
		Embedding []float64 `json:"embedding"`
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, err
	}

	return response.Embedding, nil
}

func (p *OllamaProvider) CountTokens(text string) (int, error) {
	return EstimateTokenCount(text), nil
}
