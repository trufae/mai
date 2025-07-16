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
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
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
	messages         []Message
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
	fmt.Print("  /clear         - Clear conversation messages\r\n")
	fmt.Print("  /log           - Display conversation messages\r\n")
	fmt.Print("  /undo [N]      - Remove last or Nth message from conversation\r\n")
	fmt.Print("  /stream        - Enable streaming mode\r\n")
	fmt.Print("  /nostream      - Disable streaming mode\r\n")
	fmt.Print("  /quit          - Exit REPL\r\n")
	fmt.Print("  !<command>     - Execute shell command\r\n")
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
			fmt.Print("\r\n^C\r\n>>> ")
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
	
	// Handle shell commands
	if strings.HasPrefix(input, "!") {
		return r.executeShellCommand(input[1:])
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

	// Check if we need to complete a file path for a command that accepts a file
	parts := strings.SplitN(input, " ", 2)
	if len(parts) == 2 && (parts[0] == "/image" || parts[0] == "/file" || parts[0] == "/prompt") {
		r.handleFilePathCompletion(line, parts[0], parts[1])
		return
	}

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
			"/clear",
			"/log",
			"/undo",
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
	case "/clear":
		r.messages = []Message{}
		fmt.Print("Conversation messages cleared\r\n")
	case "/log":
		r.displayConversationLog()
	case "/undo":
		if len(parts) > 1 {
			// Parse the index argument
			r.undoMessageByIndex(parts[1])
		} else {
			// Default behavior - remove the last message
			r.undoLastMessage()
		}
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

	// Create client
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	
	// Add system prompt if present
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}
	
	// Add any existing conversation messages
	messages = append(messages, r.messages...)
	
	// Add user message
	userMessage := Message{Role: "user", Content: input}
	messages = append(messages, userMessage)
	
	// Save the user message to conversation history
	r.messages = append(r.messages, userMessage)
	
	// Print prompt for the AI response
	fmt.Print("\r\nAI: ")
	
	// Send message with streaming based on REPL settings
	response, err := client.SendMessage(messages, r.streamingEnabled)
	
	// Save the assistant's response to conversation history
	if err == nil && response != "" {
		// If not streaming, we need to print the response here
		if !r.streamingEnabled {
			fmt.Print(strings.ReplaceAll(response, "\n", "\r\n"))
		}
		r.messages = append(r.messages, Message{Role: "assistant", Content: response})
	}
	
	fmt.Print("\r\n")
	return err
}

// Legacy function kept for compatibility
func (r *REPL) supportsStreaming() bool {
	// Check if streaming mode is enabled in REPL
	if !r.streamingEnabled {
		return false
	}
	// Check if API supports streaming
	provider := strings.ToLower(r.config.PROVIDER)
	return provider == "ollama" || provider == "openai" || provider == "claude"
}

// Legacy function kept for compatibility
func (r *REPL) regularResponse(input string) error {
	// Create messages
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}
	messages = append(messages, Message{Role: "user", Content: input})
	
	// Create client and send message
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	
	// Print prompt for AI response
	fmt.Print("\r\nAI: ")
	
	// Send message without streaming
	_, err = client.SendMessage(messages, false)
	
	fmt.Print("\r\n")
	return err
}

// Legacy function kept for compatibility
func (r *REPL) streamOllama(input string) error {
	// Create a new LLM client
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	
	// Prepare messages
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}
	messages = append(messages, Message{Role: "user", Content: input})
	
	// Send message with streaming
	_, err = client.SendMessage(messages, true)
	return err
}

// Legacy function kept for compatibility
func (r *REPL) streamOpenAI(input string) error {
	// Create a new LLM client
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	
	// Prepare messages
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}
	messages = append(messages, Message{Role: "user", Content: input})
	
	// Send message with streaming
	_, err = client.SendMessage(messages, true)
	return err
}

// Legacy function kept for compatibility
func (r *REPL) streamClaude(input string) error {
	// Create a new LLM client
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	
	// Prepare messages
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}
	messages = append(messages, Message{Role: "user", Content: input})
	
	// Send message with streaming
	_, err = client.SendMessage(messages, true)
	return err
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
// handleFilePathCompletion handles tab completion for file paths
func (r *REPL) handleFilePathCompletion(line *strings.Builder, cmd, partialPath string) {
	// Expand ~ to home directory if present
	if strings.HasPrefix(partialPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			partialPath = filepath.Join(homeDir, partialPath[1:])
		}
	}

	// If this is the first tab press, find matching files
	if r.completeState == 0 {
		// Get the directory and file prefix
		dir, prefix := filepath.Split(partialPath)
		
		// If no directory specified, use current directory
		if dir == "" {
			dir = "."
		} else if !filepath.IsAbs(dir) && !strings.HasPrefix(partialPath, "./") && !strings.HasPrefix(partialPath, "../") {
			// Handle relative paths that don't start with ./ or ../
			dir = "." + string(filepath.Separator) + dir
		}

		// Make sure dir ends with separator
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}
		
		// Read the directory
		files, err := os.ReadDir(dir)
		if err != nil {
			return // Cannot read directory
		}
		
		// Find matching files
		r.completeOptions = nil
		for _, file := range files {
			name := file.Name()
			if strings.HasPrefix(name, prefix) {
				// Add separator if it's a directory
				if file.IsDir() {
					name += string(filepath.Separator)
				}
				r.completeOptions = append(r.completeOptions, dir+name)
			}
		}
		
		// If no matches, do nothing
		if len(r.completeOptions) == 0 {
			return
		}
		
		r.completeState = 1
		r.completePrefix = cmd + " "
		
		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}
		
		// Get the first match
		firstMatch := r.completePrefix + r.completeOptions[0]
		
		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}
		
		// Find current option
		currentInput := line.String()
		currentPath := strings.TrimPrefix(currentInput, r.completePrefix)
		
		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentPath {
				currentIdx = i
				break
			}
		}
		
		// Get next option
		nextIdx := (currentIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[nextIdx]
		
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}
		
		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
	}
}

// executeShellCommand executes a shell command and returns its output
func (r *REPL) executeShellCommand(cmdString string) error {
	// Trim leading/trailing whitespace
	cmdString = strings.TrimSpace(cmdString)
	if cmdString == "" {
		return nil
	}
	
	// Handle special case for cd command - change working directory
	if strings.HasPrefix(cmdString, "cd ") {
		dir := strings.TrimSpace(strings.TrimPrefix(cmdString, "cd "))
		// Expand ~ to home directory if present
		if strings.HasPrefix(dir, "~") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting home directory: %v\r\n", err)
				return nil
			}
			dir = filepath.Join(homeDir, dir[1:])
		}
		
		// Change directory
		err := os.Chdir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error changing directory: %v\r\n", err)
		} else {
			cwd, _ := os.Getwd()
			fmt.Printf("Changed directory to: %s\r\n", cwd)
		}
		return nil
	}
	
	// For other commands, create a shell command
	cmd := exec.Command("sh", "-c", cmdString)
	
	// Set up pipes for stdout and stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating stdout pipe: %v\r\n", err)
		return nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating stderr pipe: %v\r\n", err)
		return nil
	}
	
	// Start the command
	err = cmd.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting command: %v\r\n", err)
		return nil
	}
	
	// Set up a wait group to coordinate goroutines
	var wg sync.WaitGroup
	wg.Add(2)
	
	// Read stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			fmt.Printf("%s\r\n", scanner.Text())
		}
	}()
	
	// Read stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			fmt.Fprintf(os.Stderr, "%s\r\n", scanner.Text())
		}
	}()
	
	// Wait for both goroutines to finish
	wg.Wait()
	
	// Wait for the command to finish
	err = cmd.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Command exited with error: %v\r\n", err)
	}
	
	return nil
}

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

// displayConversationLog prints the current conversation messages
func (r *REPL) displayConversationLog() {
	if len(r.messages) == 0 {
		fmt.Print("No conversation messages yet\r\n")
		return
	}
	
	fmt.Print("Conversation log:\r\n")
	fmt.Print("-----------------\r\n")
	
	for i, msg := range r.messages {
		role := formatRole(msg.Role)
		
		fmt.Printf("[%d] %s: ", i+1, role)
		
		// For log display, use a larger truncation limit
		content := msg.Content
		if len(content) > 100 {
			content = content[:97] + "..."
		}
		
		// Replace newlines with space for compact display
		content = strings.ReplaceAll(content, "\n", " ")
		
		fmt.Printf("%s\r\n", content)
	}
	
	fmt.Printf("Total messages: %d\r\n", len(r.messages))
}

// undoLastMessage removes the last message from the conversation history
func (r *REPL) undoLastMessage() {
	if len(r.messages) == 0 {
		fmt.Print("No messages to undo\r\n")
		return
	}
	
	// Get the last message to show what was removed
	lastMsg := r.messages[len(r.messages)-1]
	
	// Remove the last message
	r.messages = r.messages[:len(r.messages)-1]
	
	// Show information about the removed message
	role := formatRole(lastMsg.Role)
	content := truncateContent(lastMsg.Content)
	
	fmt.Printf("Removed last message (%s: %s)\r\n", role, content)
	fmt.Printf("Remaining messages: %d\r\n", len(r.messages))
}

// undoMessageByIndex removes a specific message by its 1-based index
func (r *REPL) undoMessageByIndex(indexStr string) {
	if len(r.messages) == 0 {
		fmt.Print("No messages to undo\r\n")
		return
	}
	
	// Parse the index
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		fmt.Printf("Invalid index: %s. Please provide a number.\r\n", indexStr)
		return
	}
	
	// Convert from 1-based (displayed to user) to 0-based (array index)
	index--
	
	// Check if the index is valid
	if index < 0 || index >= len(r.messages) {
		fmt.Printf("Invalid index: %d. Valid range is 1-%d.\r\n", index+1, len(r.messages))
		return
	}
	
	// Get the message being removed for display
	msg := r.messages[index]
	role := formatRole(msg.Role)
	content := truncateContent(msg.Content)
	
	// Remove the message using slice operations
	r.messages = append(r.messages[:index], r.messages[index+1:]...)
	
	fmt.Printf("Removed message %d (%s: %s)\r\n", index+1, role, content)
	fmt.Printf("Remaining messages: %d\r\n", len(r.messages))
}

// Helper function to format role for display
func formatRole(role string) string {
	if len(role) > 0 {
		return strings.ToUpper(role[:1]) + role[1:]
	}
	return role
}

// Helper function to truncate and format content for display
func truncateContent(content string) string {
	if len(content) > 30 {
		content = content[:27] + "..."
	}
	return strings.ReplaceAll(content, "\n", " ")
}
