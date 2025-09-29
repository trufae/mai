package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/trufae/mai/src/repl/art"
)

// Demo callbacks used by the REPL/demo UI. These are package-level so that
// provider streaming parsers can emit tokens as they arrive without changing
// every provider signature. Callbacks are nil by default.
var (
	demoPhaseCallback func(string) // e.g. "Thinking", "Processing"
	demoTokenCallback func(phase string, token string)
	demoInThink       bool
	// thinkFilterInThink tracks whether we're inside a <think> region
	// when filtering provider output for display (so we don't print
	// internal reasoning or the tags when demo mode is enabled).
	thinkFilterInThink bool
	// after closing </think>, some models emit leading newlines/spaces
	// before visible content. When this is set, trim leading whitespace
	// at the start of the next chunk before printing.
	thinkJustClosed bool
)

// SetDemoPhaseCallback sets a callback that receives phase/action updates.
func SetDemoPhaseCallback(cb func(string)) {
	demoPhaseCallback = cb
}

// SetDemoTokenCallback sets a callback that receives streaming tokens.
// phase will be either "thinking" or "message" (or other phases as needed).
func SetDemoTokenCallback(cb func(phase string, token string)) {
	demoTokenCallback = cb
}

// EmitDemoTokens is a small helper used by streaming parsers to push
// incoming text to the demo token callback. It understands simple
// <think>...</think> tags and preserves an internal state so tags that
// cross chunk boundaries still work.
func EmitDemoTokens(text string) {
	if demoTokenCallback == nil {
		return
	}

	// Normalize newlines into spaces to avoid breaking terminal layout
	text = strings.ReplaceAll(text, "\n", " ")

	// Iterate through the text and split on <think> tags. Only content
	// inside <think>...</think> will be sent to the demo token callback.
	// When an opening tag is seen we notify the phase callback so the UI
	// can start the greyscale scroller; when the closing tag is seen we
	// notify the phase callback with an empty string to indicate the
	// animation should stop.
	for {
		if demoInThink {
			// We are inside a thinking region; look for closing tag
			idx := strings.Index(text, "</think>")
			if idx == -1 {
				// Entire chunk is thinking — emit character by character
				for _, r := range text {
					demoTokenCallback("thinking", string(r))
				}
				return
			}
			// Emit up to closing tag as thinking, character by character
			if idx > 0 {
				for _, r := range text[:idx] {
					demoTokenCallback("thinking", string(r))
				}
			}
			// Advance past the closing tag
			text = text[idx+len("</think>"):]
			demoInThink = false
			// Notify phase callback that thinking region ended (stop animation)
			if demoPhaseCallback != nil {
				demoPhaseCallback("")
			}
			// Continue loop to handle remaining text (but do not stream message parts)
			continue
		}

		// Not in think; look for opening tag
		idx := strings.Index(text, "<think>")
		if idx == -1 {
			// No opening tag: do not forward non-thinking text to the demo
			// API (the greyscaled scroller should only show internal
			// reasoning). Return since there's nothing to stream.
			return
		}
		// Found opening tag. If there is non-thinking prefix before the
		// tag, we intentionally do not stream it to the demo.
		// Advance past the opening tag and mark we are inside think
		text = text[idx+len("<think>"):]
		demoInThink = true
		// Notify phase callback that a thinking region started
		if demoPhaseCallback != nil {
			demoPhaseCallback("Reasoning..")
		}
		// Loop to find closing tag in subsequent iterations
	}
}

// FilterOutThinkForOutput removes <think>...</think> sections and the tags
// from a streaming chunk for display when demo mode is enabled. It maintains
// a small package-level state so tags that cross chunk boundaries are handled.
func FilterOutThinkForOutput(chunk string) string {
	if chunk == "" {
		return ""
	}
	var out strings.Builder
	s := chunk
	// If we just closed a think block in a prior chunk, trim any
	// leading whitespace/newlines at the start of this chunk.
	if thinkJustClosed {
		s = strings.TrimLeft(s, " \t\r\n")
		thinkJustClosed = false
	}
	for len(s) > 0 {
		if thinkFilterInThink {
			// Look for closing tag
			if idx := strings.Index(s, "</think>"); idx >= 0 {
				// Skip content up to and including closing tag
				s = s[idx+len("</think>"):]
				thinkFilterInThink = false
				// Trim any immediate whitespace/newlines following the think block
				s = strings.TrimLeft(s, " \t\r\n")
				// Also signal that next chunk (if any) should trim leading whitespace too
				thinkJustClosed = true
				continue
			}
			// Entire remainder is within think; drop it
			return out.String()
		}
		// Not in think: look for opening tag
		if idx := strings.Index(s, "<think>"); idx >= 0 {
			// Emit prefix before the opening tag
			out.WriteString(s[:idx])
			// Enter think region and skip the opening tag
			s = s[idx+len("<think>"):]
			thinkFilterInThink = true
			continue
		}
		// No opening tag in remainder: emit all and finish
		out.WriteString(s)
		break
	}
	return out.String()
}

// StreamDemo centralizes demo/animation notifications for streaming flows.
// Create one per streaming request and call OnToken with each raw token.
// It will:
//   - On first non-empty token: stop the demo animation if it doesn't start
//     with a <think> tag by invoking the provided stop callback.
//   - Feed all tokens to EmitDemoTokens so <think> regions get rendered in
//     the demo scroller and </think> closes the animation cleanly.
type StreamDemo struct {
	firstHandled bool
	stop         func()
}

// NewStreamDemo returns a helper bound to a stop callback.
func NewStreamDemo(stop func()) *StreamDemo {
	return &StreamDemo{stop: stop}
}

// OnToken processes a raw token from the provider stream.
func (sd *StreamDemo) OnToken(raw string) {
	if sd == nil {
		return
	}
	// Handle first non-empty token: stop demo unless it begins with <think>
	if !sd.firstHandled {
		if strings.TrimSpace(raw) != "" {
			sd.firstHandled = true
			if !strings.HasPrefix(strings.TrimSpace(raw), "<think>") && sd.stop != nil {
				sd.stop()
			}
		}
	}
	// Always emit tokens so <think>...</think> is handled centrally
	EmitDemoTokens(raw)
}

// LLMProvider is a generic interface for all LLM providers
type LLMProvider interface {
	// SendMessage sends a message to the LLM and returns the response
	SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error)

	// GetName returns the name of the provider
	GetName() string

	// ListModels returns a list of available models for this provider
	ListModels(ctx context.Context) ([]Model, error)

	// DefaultModel returns the provider's default model, honoring env/config
	DefaultModel() string
}

type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// LLMResponse is a generic response handler for both streaming and non-streaming responses
type LLMResponse struct {
	Text string
	Err  error
}

// Model represents information about an available model from a provider
type Model struct {
	ID          string `json:"id""`          // Model identifier
	Name        string `json:"name""`        // Human-readable name (may be the same as ID)
	Description string `json:"description""` // Optional model description
	Provider    string `json:"provider""`    // The provider this model belongs to
}

// LLMClient manages interactions with LLM providers
type LLMClient struct {
	config         *Config
	provider       LLMProvider
	responseCancel func() // Function to cancel the current response
	// Optional callback to notify when the first streaming token arrives
	responseStopCallback func()
}

// ListModelsResult contains the list of available models with optional error
type ListModelsResult struct {
	Models []Model
	Error  error
}

// NewLLMClient creates a new LLM client for the specified provider
func NewLLMClient(config *Config) (*LLMClient, error) {
	provider, err := createProvider(config)
	if err != nil {
		return nil, err
	}

	return &LLMClient{
		config:               config,
		provider:             provider,
		responseCancel:       func() {}, // Initialize with no-op function
		responseStopCallback: nil,
	}, nil
}

// SetResponseStopCallback sets an optional callback that will be embedded into
// contexts used for streaming requests. When the first token from the provider
// is received, streaming parsers may invoke this callback (if present) to
// perform UI updates such as stopping a demo animation.
func (c *LLMClient) SetResponseStopCallback(cb func()) {
	c.responseStopCallback = cb
}

// newContext returns a cancellable context carrying the client config.
func (c *LLMClient) newContext() (context.Context, context.CancelFunc) {
	ctx := context.WithValue(context.Background(), "config", c.config)
	// Include optional stop callback in the context so streaming helpers can
	// invoke it when the first token is received.
	ctx = context.WithValue(ctx, "stop_callback", c.responseStopCallback)
	ctx, cancel := context.WithCancel(ctx)
	c.responseCancel = cancel
	return ctx, cancel
}

// createProvider instantiates the appropriate provider based on config
func createProvider(config *Config) (LLMProvider, error) {
	provider := strings.ToLower(config.PROVIDER)

	switch provider {
	case "ollama":
		return NewOllamaProvider(config), nil
	case "lmstudio":
		return NewOpenAIProvider(config), nil
	case "shimmy":
		return NewOpenAIProvider(config), nil
	case "openai":
		return NewOpenAIProvider(config), nil
	case "claude":
		return NewClaudeProvider(config), nil
	case "gemini", "google":
		return NewGeminiProvider(config), nil
	case "mistral":
		return NewMistralProvider(config), nil
	case "deepseek":
		return NewDeepSeekProvider(config), nil
	case "bedrock", "aws":
		return NewBedrockProvider(config), nil
	case "xai":
		return NewXAIProvider(config), nil
	case "openapi":
		return NewOpenAPIProvider(config), nil
	default:
		// Default to Claude if unknown provider
		return NewClaudeProvider(config), nil
	}
}

// SendMessage sends a message to the LLM and handles the response
func (c *LLMClient) SendMessage(messages []Message, stream bool, images []string) (string, error) {
	// Apply conversation message limit if configured: only keep the last N messages
	messagesToSend := messages
	if c.config != nil && c.config.ConversationMessageLimit > 0 {
		limit := c.config.ConversationMessageLimit
		if len(messages) > limit {
			messagesToSend = messages[len(messages)-limit:]
		}
	}

	// If debug is enabled in the config, prepare a debug view of the
	// messages about to be sent. If the REPL provides a DebugBannerFunc
	// we use that for prettier output; otherwise fall back to stderr.
	if c.config != nil && c.config.Debug {
		var buf bytes.Buffer
		for i, m := range messagesToSend {
			// Attempt to pretty-print the content
			var contentStr string
			switch v := m.Content.(type) {
			case string:
				contentStr = v
			default:
				if b, err := MarshalNoEscape(v); err == nil {
					contentStr = string(b)
				} else {
					contentStr = fmt.Sprintf("<unprintable content: %T>", v)
				}
			}
			fmt.Fprintf(&buf, "  - [%d] role=%s\n", i, m.Role)
			// When not using rawdog, show a user/content split if present
			if !c.config.Rawdog && m.Role == "user" {
				var parsed interface{}
				if json.Unmarshal([]byte(contentStr), &parsed) == nil {
					if b, err := MarshalNoEscape(parsed); err == nil {
						fmt.Fprintf(&buf, "    content: %s\n", string(b))
						continue
					}
				}
			}
			// Default: print the content as-is (possibly large)
			for _, line := range strings.Split(contentStr, "\n") {
				fmt.Fprintf(&buf, "    %s\n", line)
			}
		}
		art.DebugBanner("LLM Query "+c.config.PROVIDER, buf.String())
	}

	ctx, cancel := c.newContext()
	defer cancel()

	// Single entry point for all providers; providers handle images support.
	// Delegate to provider and capture response so we can debug-print it
	resp, err := c.provider.SendMessage(ctx, messagesToSend, stream && !c.config.NoStream, images)

	if c.config != nil && c.config.Debug {
		var buf bytes.Buffer
		// Attempt to pretty-print JSON responses
		var parsed interface{}
		if json.Unmarshal([]byte(resp), &parsed) == nil {
			if b, e := json.MarshalIndent(parsed, "", "  "); e == nil {
				fmt.Fprintln(&buf, string(b))
			} else {
				fmt.Fprintln(&buf, resp)
			}
		} else {
			// Non-JSON: print raw with simple indentation
			for _, line := range strings.Split(resp, "\n") {
				fmt.Fprintf(&buf, "  %s\n", line)
			}
		}
		art.DebugBanner("LLM Response From "+c.config.PROVIDER, buf.String())
	}

	return resp, err
}

// ListModels returns a list of available models for the current provider
func (c *LLMClient) ListModels() ([]Model, error) {
	ctx, cancel := c.newContext()
	defer cancel()
	return c.provider.ListModels(ctx)
}

// DefaultModel returns the default model of the current provider
func (c *LLMClient) DefaultModel() string {
	if c.provider == nil {
		return ""
	}
	return c.provider.DefaultModel()
}

// InterruptResponse cancels the current LLM response if one is being generated
func (c *LLMClient) InterruptResponse() {
	if c.responseCancel != nil {
		c.responseCancel()
	}
}

// ExtractSystemPrompt extracts a system prompt from the input if present
func ExtractSystemPrompt(input string) (string, string) {
	systemPrompt := ""
	userPrompt := input

	// Simplified parsing to extract system prompt if it's at the beginning
	if strings.HasPrefix(input, "<system>\n") {
		parts := strings.SplitN(input, "</system>\n", 2)
		if len(parts) == 2 {
			systemPrompt = strings.TrimPrefix(parts[0], "<system>\n")
			userPrompt = parts[1]
		}
	}

	return systemPrompt, userPrompt
}

// PrepareMessages creates a message array with optional system prompt
func PrepareMessages(input string, cfg *Config) []Message {
	// First, expand any @filename mentions in the input so callers that
	// pass file-includes via @file get the file contents inlined (stdin mode
	// and simple API paths rely on this behavior).
	expanded := expandAtMentions(input)

	// Next, allow a default system prompt file to be loaded when no explicit
	// inline <system>...</system> prompt is present. This mirrors the REPL's
	// behavior where .mai/systemprompt.md (project or $HOME/.mai) is used.
	systemPrompt, userPrompt := ExtractSystemPrompt(expanded)

	messages := []Message{}

	// Preference order:
	// 1) inline <system>...</system> in the input
	// 2) explicit config SystemPrompt (cfg.SystemPrompt)
	// 3) explicit config SystemPromptFile (cfg.SystemPromptFile)
	// 4) repository or home `.mai/systemprompt.md` (loadDefaultSystemPrompt)
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	} else if cfg != nil && cfg.SystemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: cfg.SystemPrompt})
	} else if cfg != nil && cfg.SystemPromptFile != "" {
		if b, err := os.ReadFile(cfg.SystemPromptFile); err == nil {
			messages = append(messages, Message{Role: "system", Content: processIncludes(string(b), filepath.Dir(cfg.SystemPromptFile))})
		}
	} else {
		if sp := loadDefaultSystemPrompt(); sp != "" {
			messages = append(messages, Message{Role: "system", Content: sp})
		}
	}

	// If a prompt file is provided via config, include its content at the
	// start of the user message (this mirrors the REPL behavior for
	// `#promptname` which injects prompt file content into the user level).
	if cfg != nil && cfg.PromptFile != "" {
		if b, err := os.ReadFile(cfg.PromptFile); err == nil {
			// Prepend prompt file content, separated by blank lines
			userPrompt = string(b) + "\n\n" + userPrompt
		}
	}

	// Add user message (use userPrompt which preserves user-provided <system> removal)
	messages = append(messages, Message{Role: "user", Content: userPrompt})

	return messages
}

// expandAtMentions searches the input for simple @filename mentions and
// inlines existing file contents. This is a lightweight adaptation of the
// REPL's @-mention processing so stdin/API callers get the same behavior.
func expandAtMentions(input string) string {
	var out strings.Builder
	// Split on whitespace but preserve newlines so multi-line @-lines work
	tokens := strings.FieldsFunc(input, func(r rune) bool { return r == ' ' || r == '\t' })
	// Simple approach: if a token starts with '@' and the file exists, read it
	// and append a markdown-formatted code block representing the file content.
	// Otherwise, leave the token as-is. Reconstruct using spaces (we keep newlines
	// from the original input by replacing tokens in order into the original).

	// Build a map of token->replacement to allow simple reconstruction
	repl := make(map[string]string)
	for _, t := range tokens {
		if len(t) > 1 && t[0] == '@' {
			path := t[1:]
			// Try to read file as-is
			if b, err := os.ReadFile(path); err == nil {
				// Format as markdown snippet so LLM sees filename and content
				repl[t] = "\n\n## File: " + path + "\n\n```\n" + string(b) + "\n```\n\n"
				continue
			}
		}
		// default: identity
		repl[t] = t
	}

	// Reconstruct the original input preserving newlines
	// This is a conservative reconstruction: iterate over original runes
	// and replace tokens when encountering whitespace boundaries.
	var tokenBuf strings.Builder
	for _, r := range input {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if tokenBuf.Len() > 0 {
				tk := tokenBuf.String()
				if v, ok := repl[tk]; ok {
					out.WriteString(v)
				} else {
					out.WriteString(tk)
				}
				tokenBuf.Reset()
			}
			out.WriteRune(r)
		} else {
			tokenBuf.WriteRune(r)
		}
	}
	if tokenBuf.Len() > 0 {
		tk := tokenBuf.String()
		if v, ok := repl[tk]; ok {
			out.WriteString(v)
		} else {
			out.WriteString(tk)
		}
	}
	return out.String()
}

// loadDefaultSystemPrompt attempts to load a system prompt from the
// repository-local `.mai/systemprompt.md` or the user's `$HOME/.mai/systemprompt.md`.
// Lines starting with '@' are treated as include directives and are replaced
// with the contents of the referenced file (path is resolved relative to the
// system prompt file's directory).
func loadDefaultSystemPrompt() string {
	// Check current directory .mai/systemprompt.md first
	cand := ".mai/systemprompt.md"
	if b, err := os.ReadFile(cand); err == nil {
		return processIncludes(string(b), ".mai")
	}
	// Fallback to home
	if home, err := os.UserHomeDir(); err == nil {
		cand2 := filepath.Join(home, ".mai", "systemprompt.md")
		if b, err := os.ReadFile(cand2); err == nil {
			return processIncludes(string(b), filepath.Join(home, ".mai"))
		}
	}
	return ""
}

// processIncludes replaces lines starting with '@' with the contents of the referenced file.
func processIncludes(content, baseDir string) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@") {
			incPath := strings.TrimSpace(trimmed[1:])
			target := incPath
			if !filepath.IsAbs(incPath) && baseDir != "" {
				target = filepath.Join(baseDir, incPath)
			}
			if data, err := os.ReadFile(target); err != nil {
				// If include fails, keep the original line so the prompt author
				// can see the problem rather than silently dropping it.
				out = append(out, line)
			} else {
				out = append(out, string(data))
			}
		} else {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// httpDo prepares and executes an HTTP request, shared by streaming and non-streaming.
func httpDo(ctx context.Context, method, url string, headers map[string]string, body []byte, stream bool) (*http.Response, error) {
	var req *http.Request
	var err error
	if ctx != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	} else {
		req, err = http.NewRequest(method, url, bytes.NewBuffer(body))
	}
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if ctx != nil {
		if cfg, ok := ctx.Value("config").(*Config); ok && cfg.UserAgent != "" {
			req.Header.Set("User-Agent", cfg.UserAgent)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if stream {
		client.Timeout = 0
	}

	// Set up a channel to signal when the request is done
	done := make(chan struct{})

	// Create a goroutine to check for context cancellation
	var cancelOnce sync.Once
	go func() {
		// Only proceed if context is not nil
		if ctx == nil {
			return
		}

		// Wait for either context cancellation or request completion
		select {
		case <-ctx.Done():
			// Cancel the request if context is canceled
			cancelOnce.Do(func() {
				// Transport.CancelRequest was deprecated in Go 1.5, but client.Transport.CancelRequest
				// is still usable. However, the preferred method is to use request contexts.
				// This is an extra safety measure in case the context cancellation
				// doesn't propagate quickly enough.
				transport, ok := client.Transport.(*http.Transport)
				if ok && transport != nil {
					transport.CancelRequest(req)
				}
			})
		case <-done:
			// Request completed normally, do nothing
		}
	}()

	// Execute the request
	resp, err := client.Do(req)

	// Signal that the request is done
	close(done)

	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if stream {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
		}
		fmt.Fprintf(os.Stderr, "Error: Non-200 status code: %d %s\n", resp.StatusCode, resp.Status)
	}
	return resp, nil
}

func llmMakeRequest(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// llmMakeStreamingRequest is a utility function for making streaming HTTP requests (renamed to avoid conflict)
func llmMakeStreamingRequest(ctx context.Context, method, url string, headers map[string]string,
	body []byte, parser func(io.Reader) (string, error)) (string, error) {
	resp, err := httpDo(ctx, method, url, headers, body, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parser(resp.Body)
}

// llmMakeStreamingRequestWithCallback is a utility function for making streaming HTTP requests with a callback
func llmMakeStreamingRequestWithCallback(ctx context.Context, method, url string, headers map[string]string,
	body []byte, parser func(io.Reader, func()) (string, error), stopCallback func()) (string, error) {
	resp, err := httpDo(ctx, method, url, headers, body, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// If no explicit stopCallback was provided by the caller, try to read one
	// from the context (set by the LLM client). This allows higher-level
	// callers (like the REPL) to be notified as soon as the first token
	// arrives without requiring changes to every provider signature.
	if stopCallback == nil && ctx != nil {
		if cb, ok := ctx.Value("stop_callback").(func()); ok && cb != nil {
			stopCallback = cb
		}
	}
	return parser(resp.Body, stopCallback)
}

// buildURL constructs a full URL using baseURL override, defaultURL, or host/port and path suffix
func buildURL(defaultURL, baseURL, host, port, suffix string) string {
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/") + suffix
	}
	if defaultURL != "" {
		return defaultURL
	}
	return fmt.Sprintf("http://%s:%s%s", host, port, suffix)
}

func MarshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	if err != nil {
		return nil, err
	}
	// Remove trailing newline added by Encoder
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
