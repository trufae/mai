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
	config           *Config
	history          []string
	historyIndex     int
	currentInput     strings.Builder
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	isStreaming      bool
	oldState         *term.State
	completeState    int
	completeOptions  []string
	completePrefix   string
	streamingEnabled bool
	systemPrompt     string
}

type StreamingClient interface {
	StreamChat(ctx context.Context, messages []Message) (<-chan string, <-chan error)
}

func NewREPL(config *Config) (*REPL, error) {
	ctx, cancel := context.WithCancel(context.Background())

	return &REPL{
		config:           config,
		history:          make([]string, 0),
		historyIndex:     -1,
		ctx:              ctx,
		cancel:           cancel,
		completeState:    0,
		completeOptions:  []string{},
		streamingEnabled: !config.NoStream, // Respect NoStream flag
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

	fmt.Print(fmt.Sprintf("AI REPL Mode - %s\r\n", strings.ToUpper(r.config.PROVIDER)))
	r.showCommands()

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

func (r *REPL) showCommands() {
	fmt.Print("Commands:\r\n")
	fmt.Print("  /help          - Show available commands\r\n")
	fmt.Print("  /image <path>  - Send an image\r\n")
	fmt.Print("  /file <path>   - Send a file\r\n")
	fmt.Print("  /prompt <path> - Load system prompt from file\r\n")
	fmt.Print("  /noprompt      - Remove system prompt\r\n")
	fmt.Print("  /cancel        - Cancel current request\r\n")
	fmt.Print("  /stream        - Enable streaming mode\r\n")
	fmt.Print("  /nostream      - Disable streaming mode\r\n")
	fmt.Print("  /quit          - Exit REPL\r\n")
	fmt.Print("  Ctrl+C         - Cancel current request\r\n")
	fmt.Print("  Ctrl+D         - Exit REPL (when line is empty)\r\n")
	fmt.Print("  Ctrl+W         - Delete last word\r\n")
	fmt.Print("  Up/Down arrows - Navigate history\r\n")
	fmt.Print("  Tab            - Command completion\r\n")
	fmt.Print("\r\n")
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
			fmt.Print("\r\n^C (Request cancelled)\r\n> ")
			r.cancel()
			// Create new context for next request
			r.ctx, r.cancel = context.WithCancel(context.Background())
		} else {
			// Just print a new prompt instead of exiting
			fmt.Print("\r\n^C\r\n> ")
		}
	}()
}

func (r *REPL) handleInput() error {
	fmt.Print("\n\r>>> ")

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
			r.completeState = 0
			return line.String(), nil
		case 127, 8: // Backspace
			if line.Len() > 0 {
				s := line.String()
				line.Reset()
				line.WriteString(s[:len(s)-1])
				fmt.Print("\b \b")
				r.completeState = 0
			}
		case 23: // Ctrl+W (delete last word)
			if line.Len() > 0 {
				s := line.String()
				// Find the beginning of the last word
				lastSpace := strings.LastIndexAny(s[:len(s)], " \t")
				if lastSpace == -1 {
					// No spaces, delete everything
					for i := 0; i < len(s); i++ {
						fmt.Print("\b \b")
					}
					line.Reset()
				} else {
					// Delete from last space to end
					for i := 0; i < len(s)-lastSpace-1; i++ {
						fmt.Print("\b \b")
					}
					line.Reset()
					line.WriteString(s[:lastSpace+1])
				}
				r.completeState = 0
			}
		case 27: // Escape sequence (arrow keys)
			if err := r.handleEscapeSequence(&line); err != nil {
				return "", err
			}
		case 9: // Tab
			r.handleTabCompletion(&line)
		case 3: // Ctrl+C
			// Cancel current request but don't exit
			r.cancel()
			r.ctx, r.cancel = context.WithCancel(context.Background())
			fmt.Print("\r\n^C\r\n> ")
			line.Reset()
		case 4: // Ctrl+D
			if line.Len() == 0 { // Only exit if the line is empty
				fmt.Print("\r\nGoodbye!\r\n")
				return "", io.EOF
			}
		default:
			if b >= 32 && b <= 126 { // Printable characters
				line.WriteByte(b)
				fmt.Printf("%c", b)
				r.completeState = 0
			}
		}
	}
}

func (r *REPL) handleTabCompletion(line *strings.Builder) {
	input := line.String()

	// Only handle tab completion at the beginning of the line for commands
	if !strings.HasPrefix(input, "/") {
		return
	}

	if r.completeState == 0 {
		// First tab press - generate options
		r.completePrefix = input
		r.completeOptions = []string{
			"/help",
			"/image",
			"/file",
			"/cancel",
			"/stream",
			"/nostream",
			"/prompt",
			"/noprompt",
			"/quit",
			"/exit",
		}

		// Filter options that match the prefix
		var filteredOptions []string
		for _, opt := range r.completeOptions {
			if strings.HasPrefix(opt, r.completePrefix) {
				filteredOptions = append(filteredOptions, opt)
			}
		}
		r.completeOptions = filteredOptions

		if len(r.completeOptions) == 0 {
			return // No matches
		}

		r.completeState = 1

		// Replace current input with the first match
		if len(r.completeOptions) > 0 {
			// Clear current line
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}

			// Print the first match
			fmt.Print(r.completeOptions[0])
			line.Reset()
			line.WriteString(r.completeOptions[0])
		}
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option index
		currentOption := line.String()
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentOption {
				currentIdx = i
				break
			}
		}

		// Get next option
		nextIdx := (currentIdx + 1) % len(r.completeOptions)
		nextOption := r.completeOptions[nextIdx]

		// Clear current line
		for i := 0; i < len(currentOption); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
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
		fmt.Print("Request cancelled\r\n")
	case "/help":
		r.showCommands()
		return nil
	case "/stream":
		r.streamingEnabled = true
		fmt.Print("Streaming mode enabled\r\n")
	case "/nostream":
		r.streamingEnabled = false
		fmt.Print("Streaming mode disabled\r\n")
	case "/prompt":
		if len(parts) < 2 {
			fmt.Println("Usage: /prompt <path>")
			return nil
		}
		return r.loadSystemPrompt(parts[1])
	case "/noprompt":
		r.systemPrompt = ""
		fmt.Print("System prompt removed\r\n")
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
	// Check if streaming mode is enabled in REPL
	if !r.streamingEnabled {
		return false
	}
	// Check if API supports streaming
	provider := strings.ToLower(r.config.PROVIDER)
	return provider == "ollama" || provider == "openai" || provider == "claude"
}

func (r *REPL) streamResponse(input string) error {
	fmt.Print("\r\nAI: ")

	switch strings.ToLower(r.config.PROVIDER) {
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
	fmt.Print("\r\nAI: ")

	// If system prompt is set, add it to the input in the format expected by non-streaming API calls
	completeInput := input
	if r.systemPrompt != "" {
		completeInput = fmt.Sprintf("<system>\n%s</system>\n%s", r.systemPrompt, input)
	}

	var err error
	switch strings.ToLower(r.config.PROVIDER) {
	case "gemini", "google":
		err = callGemini(r.config, completeInput)
	case "deepseek":
		err = callDeepSeek(r.config, completeInput)
	case "openapi":
		err = callOpenAPI(r.config, completeInput)
	case "claude":
		err = callClaude(r.config, completeInput)
	case "ollama":
		err = callOllama(r.config, completeInput)
	case "openai":
		err = callOpenAI(r.config, completeInput)
	case "mistral":
		err = callMistral(r.config, completeInput)
	case "bedrock", "aws":
		err = callBedrock(r.config, completeInput)
	default:
		err = callClaude(r.config, completeInput)
	}

	fmt.Print("\r\n")
	return err
}

func (r *REPL) streamOllama(input string) error {
	messages := []Message{}

	// Add system prompt if exists
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: input})

	request := OllamaRequest{
		Stream:   true,
		Model:    r.config.OllamaModel,
		Messages: messages,
	}

	return r.makeStreamingRequest("POST",
		fmt.Sprintf("http://%s:%s/api/chat", r.config.OllamaHost, r.config.OllamaPort),
		map[string]string{"Content-Type": "application/json"},
		request,
		r.parseOllamaStream)
}

func (r *REPL) streamOpenAI(input string) error {
	messages := []Message{}

	// Add system prompt if exists
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: input})

	request := map[string]interface{}{
		"model":    r.config.OpenAIModel,
		"messages": messages,
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
	messages := []Message{}

	// Add system prompt if exists
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: input})

	request := map[string]interface{}{
		"model":      r.config.ClaudeModel,
		"max_tokens": 5128,
		"messages":   messages,
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

		// Replace \n with \r\n in the response
		fmt.Print(strings.ReplaceAll(response.Message.Content, "\n", "\r\n"))

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
			// Replace \n with \r\n in the response
			fmt.Print(strings.ReplaceAll(response.Choices[0].Delta.Content, "\n", "\r\n"))
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
			// Replace \n with \r\n in the response
			fmt.Print(strings.ReplaceAll(response.Delta.Text, "\n", "\r\n"))
		}
	}

	fmt.Println()
	return scanner.Err()
}

// Load system prompt from a file
func (r *REPL) loadSystemPrompt(path string) error {
	// Expand ~ to home directory if present
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Read the file
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read prompt file: %v", err)
	}

	// Set the system prompt
	r.systemPrompt = string(content)
	fmt.Printf("System prompt loaded from %s (%d bytes)\r\n", path, len(content))
	return nil
}
