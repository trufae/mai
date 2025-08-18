package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// LLMProvider is a generic interface for all LLM providers
type LLMProvider interface {
	// SendMessage sends a message to the LLM and returns the response
	SendMessage(ctx context.Context, messages []Message, stream bool, images []string) (string, error)

	// GetName returns the name of the provider
	GetName() string

	// ListModels returns a list of available models for this provider
	ListModels(ctx context.Context) ([]Model, error)
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
		config:         config,
		provider:       provider,
		responseCancel: func() {}, // Initialize with no-op function
	}, nil
}

// newContext returns a cancellable context carrying the client config.
func (c *LLMClient) newContext() (context.Context, context.CancelFunc) {
	ctx := context.WithValue(context.Background(), "config", c.config)
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
	case "openapi":
		return NewOpenAPIProvider(config), nil
	default:
		// Default to Claude if unknown provider
		return NewClaudeProvider(config), nil
	}
}

// SendMessage sends a message to the LLM and handles the response
func (c *LLMClient) SendMessage(messages []Message, stream bool, images []string) (string, error) {
	ctx, cancel := c.newContext()
	defer cancel()

	// Single entry point for all providers; providers handle images support.
	return c.provider.SendMessage(ctx, messages, stream && !c.config.NoStream, images)
}

// ListModels returns a list of available models for the current provider
func (c *LLMClient) ListModels() ([]Model, error) {
	ctx, cancel := c.newContext()
	defer cancel()
	return c.provider.ListModels(ctx)
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
func PrepareMessages(input string) []Message {
	systemPrompt, userPrompt := ExtractSystemPrompt(input)

	messages := []Message{}

	// Add system message if present
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: userPrompt})

	return messages
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
