package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

type REPL struct {
	config       *Config
	history      []string
	historyIndex int
	currentInput strings.Builder
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	isStreaming  bool
	oldState     *term.State
}

type StreamingClient interface {
	StreamChat(ctx context.Context, messages []Message) (<-chan string, <-chan error)
}

func NewREPL(config *Config) (*REPL, error) {
	ctx, cancel := context.WithCancel(context.Background())

	return &REPL{
		config:       config,
		history:      make([]string, 0),
		historyIndex: -1,
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

func (r *REPL) Run() error {
	defer r.cleanup()

	// Set up terminal for raw input
	if err := r.setupTerminal(); err != nil {
		return fmt.Errorf("failed to setup terminal: %v", err)
	}

	// Handle interrupt signals
	r.setupSignalHandler()

	fmt.Printf("AI REPL Mode - %s\n", strings.ToUpper(r.config.API))
	fmt.Println("Commands:")
	fmt.Println("  /image <path>  - Send an image")
	fmt.Println("  /file <path>   - Send a file")
	fmt.Println("  /cancel        - Cancel current request")
	fmt.Println("  /quit          - Exit REPL")
	fmt.Println("  Ctrl+C         - Cancel current request or exit")
	fmt.Println("  Up/Down arrows - Navigate history")
	fmt.Println()

	for {
		if err := r.handleInput(); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	return nil
}

func (r *REPL) setupTerminal() error {
	var err error
	r.oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
	return err
}

func (r *REPL) cleanup() {
	if r.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), r.oldState)
	}
	r.cancel()
}

func (r *REPL) setupSignalHandler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		r.mu.Lock()
		isStreaming := r.isStreaming
		r.mu.Unlock()

		if isStreaming {
			fmt.Print("\n^C (Request cancelled)\n> ")
			r.cancel()
			// Create new context for next request
			r.ctx, r.cancel = context.WithCancel(context.Background())
		} else {
			fmt.Println("\nGoodbye!")
			r.cleanup()
			os.Exit(0)
		}
	}()
}

func (r *REPL) handleInput() error {
	fmt.Print("> ")

	input, err := r.readLine()
	if err != nil {
		return err
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	// Handle commands
	if strings.HasPrefix(input, "/") {
		return r.handleCommand(input)
	}

	// Add to history
	r.addToHistory(input)

	// Send to AI
	return r.sendToAI(input)
}

func (r *REPL) readLine() (string, error) {
	var line strings.Builder
	buf := make([]byte, 1)

	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", err
		}

		if n == 0 {
			continue
		}

		b := buf[0]

		switch b {
		case '\r', '\n':
			fmt.Print("\r\n")
			return line.String(), nil
		case 127, 8: // Backspace
			if line.Len() > 0 {
				s := line.String()
				line.Reset()
				line.WriteString(s[:len(s)-1])
				fmt.Print("\b \b")
			}
		case 27: // Escape sequence (arrow keys)
			if err := r.handleEscapeSequence(&line); err != nil {
				return "", err
			}
		case 3: // Ctrl+C
			return "", fmt.Errorf("interrupted")
		default:
			if b >= 32 && b <= 126 { // Printable characters
				line.WriteByte(b)
				fmt.Printf("%c", b)
			}
		}
	}
}

func (r *REPL) handleEscapeSequence(line *strings.Builder) error {
	buf := make([]byte, 2)
	n, err := os.Stdin.Read(buf)
	if err != nil || n < 2 {
		return nil
	}

	if buf[0] == '[' {
		switch buf[1] {
		case 'A': // Up arrow
			r.navigateHistory(-1, line)
		case 'B': // Down arrow
			r.navigateHistory(1, line)
		}
	}

	return nil
}

func (r *REPL) navigateHistory(direction int, line *strings.Builder) {
	if len(r.history) == 0 {
		return
	}

	newIndex := r.historyIndex + direction

	if direction == -1 && r.historyIndex == -1 {
		newIndex = len(r.history) - 1
	} else if newIndex < -1 {
		newIndex = -1
	} else if newIndex >= len(r.history) {
		newIndex = len(r.history) - 1
	}

	// Clear current line
	for i := 0; i < line.Len(); i++ {
		fmt.Print("\b \b")
	}

	r.historyIndex = newIndex
	line.Reset()

	if r.historyIndex >= 0 {
		historyItem := r.history[r.historyIndex]
		line.WriteString(historyItem)
		fmt.Print(historyItem)
	}
}

func (r *REPL) addToHistory(input string) {
	r.history = append(r.history, input)
	r.historyIndex = -1
}

func (r *REPL) handleCommand(input string) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	command := parts[0]

	switch command {
	case "/quit", "/exit":
		return io.EOF
	case "/cancel":
		r.cancel()
		r.ctx, r.cancel = context.WithCancel(context.Background())
		fmt.Println("Request cancelled")
	case "/image":
		if len(parts) < 2 {
			fmt.Println("Usage: /image <path>")
			return nil
		}
		return r.sendImage(parts[1])
	case "/file":
		if len(parts) < 2 {
			fmt.Println("Usage: /file <path>")
			return nil
		}
		return r.sendFile(parts[1])
	default:
		fmt.Printf("Unknown command: %s\n", command)
	}

	return nil
}

func (r *REPL) sendImage(imagePath string) error {
	// Expand ~ to home directory
	if strings.HasPrefix(imagePath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		imagePath = filepath.Join(homeDir, imagePath[1:])
	}

	// Read image file
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Errorf("failed to read image: %v", err)
	}

	// Encode to base64
	encoded := base64.StdEncoding.EncodeToString(imageData)

	// Create message with image
	message := fmt.Sprintf("I'm sending you an image from %s. Please analyze it.\n[Image data: %s]",
		imagePath, encoded[:100]+"...") // Show first 100 chars for reference

	r.addToHistory(fmt.Sprintf("/image %s", imagePath))
	return r.sendToAI(message)
}

func (r *REPL) sendFile(filePath string) error {
	// Expand ~ to home directory
	if strings.HasPrefix(filePath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		filePath = filepath.Join(homeDir, filePath[1:])
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	// Create message with file content
	message := fmt.Sprintf("Here's the content of file %s:\n\n```\n%s\n```\n\nPlease analyze or help with this file.",
		filePath, string(content))

	r.addToHistory(fmt.Sprintf("/file %s", filePath))
	return r.sendToAI(message)
}

func (r *REPL) sendToAI(input string) error {
	r.mu.Lock()
	r.isStreaming = true
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.isStreaming = false
		r.mu.Unlock()
	}()

	// Try streaming first, fall back to regular API calls
	if r.supportsStreaming() {
		return r.streamResponse(input)
	}

	return r.regularResponse(input)
}

func (r *REPL) supportsStreaming() bool {
	api := strings.ToLower(r.config.API)
	return api == "ollama" || api == "openai" || api == "claude"
}

func (r *REPL) streamResponse(input string) error {
	fmt.Print("\nAI: ")

	switch strings.ToLower(r.config.API) {
	case "ollama":
		return r.streamOllama(input)
	case "openai":
		return r.streamOpenAI(input)
	case "claude":
		return r.streamClaude(input)
	default:
		return r.regularResponse(input)
	}
}

func (r *REPL) regularResponse(input string) error {
	fmt.Print("\nAI: ")

	var err error
	switch strings.ToLower(r.config.API) {
	case "gemini", "google":
		err = callGemini(r.config, input)
	case "deepseek":
		err = callDeepSeek(r.config, input)
	case "openapi":
		err = callOpenAPI(r.config, input)
	case "claude":
		err = callClaude(r.config, input)
	case "ollama":
		err = callOllama(r.config, input)
	case "openai":
		err = callOpenAI(r.config, input)
	case "mistral":
		err = callMistral(r.config, input)
	case "bedrock", "aws":
		err = callBedrock(r.config, input)
	default:
		err = callClaude(r.config, input)
	}

	fmt.Println()
	return err
}

func (r *REPL) streamOllama(input string) error {
	request := OllamaRequest{
		Stream:   true,
		Model:    r.config.OllamaModel,
		Messages: []Message{{Role: "user", Content: input}},
	}

	return r.makeStreamingRequest("POST",
		fmt.Sprintf("http://%s:%s/api/chat", r.config.OllamaHost, r.config.OllamaPort),
		map[string]string{"Content-Type": "application/json"},
		request,
		r.parseOllamaStream)
}

func (r *REPL) streamOpenAI(input string) error {
	request := map[string]interface{}{
		"model":    r.config.OpenAIModel,
		"messages": []Message{{Role: "user", Content: input}},
		"stream":   true,
	}

	return r.makeStreamingRequest("POST",
		"https://api.openai.com/v1/chat/completions",
		map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + r.config.OpenAIKey,
		},
		request,
		r.parseOpenAIStream)
}

func (r *REPL) streamClaude(input string) error {
	request := map[string]interface{}{
		"model":      r.config.ClaudeModel,
		"max_tokens": 5128,
		"messages":   []Message{{Role: "user", Content: input}},
		"stream":     true,
	}

	return r.makeStreamingRequest("POST",
		"https://api.anthropic.com/v1/messages",
		map[string]string{
			"Content-Type":      "application/json",
			"anthropic-version": "2023-06-01",
			"x-api-key":         r.config.ClaudeKey,
		},
		request,
		r.parseClaudeStream)
}

func (r *REPL) makeStreamingRequest(method, url string, headers map[string]string,
	request interface{}, parser func(io.Reader) error) error {

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(r.ctx, method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 0} // No timeout for streaming
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return parser(resp.Body)
}

func (r *REPL) parseOllamaStream(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		select {
		case <-r.ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		var response struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}

		if err := json.Unmarshal([]byte(line), &response); err != nil {
			continue
		}

		fmt.Print(response.Message.Content)

		if response.Done {
			break
		}
	}

	fmt.Println()
	return scanner.Err()
}

func (r *REPL) parseOpenAIStream(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		select {
		case <-r.ctx.Done():
			return nil
		default:
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

		if len(response.Choices) > 0 {
			fmt.Print(response.Choices[0].Delta.Content)
		}
	}

	fmt.Println()
	return scanner.Err()
}

func (r *REPL) parseClaudeStream(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		select {
		case <-r.ctx.Done():
			return nil
		default:
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
			Type  string `json:"type"`
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &response); err != nil {
			continue
		}

		if response.Type == "content_block_delta" {
			fmt.Print(response.Delta.Text)
		}
	}

	fmt.Println()
	return scanner.Err()
}
