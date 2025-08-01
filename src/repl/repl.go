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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Command represents a REPL command with its description and handler
type Command struct {
	Name        string
	Description string
	Handler     func(r *REPL, args []string) error
}

type REPL struct {
	config           *Config
	currentClient    *LLMClient
	readline         *ReadLine // Persistent readline instance for input handling
	currentInput     strings.Builder
	cursorPos        int // Current cursor position in the line
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	isStreaming      bool
	oldState         *term.State
	completeState    int
	completeOptions  []string
	completePrefix   string
	completeIdx      int // Current index in completion options
	streamingEnabled bool
	systemPrompt     string
	messages         []Message
	includeReplies   bool               // Whether to include assistant replies in the context
	pendingFiles     []pendingFile      // Files and images to include in the next message
	reasoningEnabled bool               // Whether reasoning is enabled for the AI model
	loggingEnabled   bool               // Whether to save conversation history
	markdownEnabled  bool               // Whether to render markdown with colors
	useToolsEnabled  bool               // Whether to process input using tools.go functions
	commands         map[string]Command // Registry of available commands
}

type pendingFile struct {
	filePath string
	content  string
	isImage  bool
	imageB64 string // Base64 encoded image data
}

type StreamingClient interface {
	StreamChat(ctx context.Context, messages []Message) (<-chan string, <-chan error)
}

func NewREPL(config *Config) (*REPL, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a persistent readline instance
	readLine, err := NewReadLine()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize readline: %v", err)
	}

	// Initialize the REPL
	repl := &REPL{
		config:          config,
		readline:        readLine,
		cursorPos:       0, // Initialize cursor position to 0
		ctx:             ctx,
		cancel:          cancel,
		completeState:   0,
		completeOptions: []string{},
		completeIdx:     0,                        // Initialize completion index
		pendingFiles:    []pendingFile{},          // Initialize empty pending files slice
		commands:        make(map[string]Command), // Initialize command registry
	}

	// Initialize streaming from options with the NoStream flag as a fallback
	streamDefault := "true"
	if config.NoStream {
		streamDefault = "false"
	}

	// Override defaults based on command line flags if needed
	if _, exists := repl.config.options.GetOptionInfo("stream"); !exists {
		repl.config.options.RegisterOption("stream", BooleanOption, "Enable streaming mode", streamDefault)
	}

	// Initialize all settings from options
	repl.streamingEnabled = repl.config.options.GetBool("stream")
	repl.includeReplies = repl.config.options.GetBool("include_replies")
	repl.reasoningEnabled = repl.config.options.GetBool("reasoning")
	repl.loggingEnabled = repl.config.options.GetBool("logging")
	repl.markdownEnabled = repl.config.options.GetBool("markdown")
	repl.useToolsEnabled = repl.config.options.GetBool("usetools")

	// Set prompts in the readline instance
	if prompt := repl.config.options.Get("prompt"); prompt != "" {
		repl.readline.SetPrompt(prompt)
	}

	if readlinePrompt := repl.config.options.Get("readlineprompt"); readlinePrompt != "" {
		repl.readline.SetReadlinePrompt(readlinePrompt)
	}

	// Synchronize provider and model settings with config options
	if provider := repl.config.options.Get("provider"); provider != "" {
		repl.config.PROVIDER = provider
	} else if repl.config.PROVIDER != "" {
		// Set the provider option if it's not set but PROVIDER is
		repl.config.options.Set("provider", repl.config.PROVIDER)
	}

	// Synchronize model setting based on current provider
	if model := repl.config.options.Get("model"); model != "" {
		// Set the appropriate model based on provider
		switch strings.ToLower(repl.config.PROVIDER) {
		case "ollama":
			repl.config.OllamaModel = model
		case "openai":
			repl.config.OpenAIModel = model
		case "claude":
			repl.config.ClaudeModel = model
		case "gemini", "google":
			repl.config.GeminiModel = model
		case "mistral":
			repl.config.MistralModel = model
		case "deepseek":
			repl.config.DeepSeekModel = model
		case "bedrock", "aws":
			repl.config.BedrockModel = model
		}
	} else {
		// Set the model option based on the current provider's model
		currentModel := repl.getCurrentModelForProvider()
		if currentModel != "" {
			repl.config.options.Set("model", currentModel)
		}
	}

	// Load system prompt from promptfile if set
	if promptFile := repl.config.options.Get("promptfile"); promptFile != "" {
		content, err := os.ReadFile(promptFile)
		if err == nil {
			repl.systemPrompt = string(content)
		}
	} else if systemPrompt := repl.config.options.Get("systemprompt"); systemPrompt != "" {
		// Or use systemprompt text if set
		repl.systemPrompt = systemPrompt
	}

	// Set baseurl from command line flag if provided
	if repl.config.BaseURL != "" {
		repl.config.options.Set("baseurl", repl.config.BaseURL)
	} else if baseURL := repl.config.options.Get("baseurl"); baseURL != "" {
		// Or use the config option if set
		repl.config.BaseURL = baseURL
	}

	// Register listener to sync BaseURL when changed via config options
	repl.config.options.RegisterOptionListener("baseurl", func(value string) {
		repl.config.BaseURL = value
	})

	// Register listeners for prompt option changes
	repl.config.options.RegisterOptionListener("prompt", func(value string) {
		if repl.readline != nil {
			repl.readline.SetPrompt(value)
		}
	})

	repl.config.options.RegisterOptionListener("readlineprompt", func(value string) {
		if repl.readline != nil {
			repl.readline.SetReadlinePrompt(value)
		}
	})

	// Set useragent from command line flag if provided
	if repl.config.UserAgent != "mai-repl/1.0" {
		repl.config.options.Set("useragent", repl.config.UserAgent)
	} else if userAgent := repl.config.options.Get("useragent"); userAgent != "" {
		// Or use the config option if set
		repl.config.UserAgent = userAgent
	}

	// Initialize command registry
	repl.initCommands()

	// Auto-detect and set promptdir and templatedir
	repl.autoDetectPromptDir()
	repl.autoDetectTemplateDir()

	return repl, nil
}

// loadRCFile loads and processes commands from ~/.mairc file
func (r *REPL) loadRCFile() error {
	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	// Form the path to the .mairc file
	rcFilePath := filepath.Join(homeDir, ".mairc")

	// Check if the file exists
	if _, err := os.Stat(rcFilePath); os.IsNotExist(err) {
		// File doesn't exist, not an error
		return nil
	} else if err != nil {
		return fmt.Errorf("error checking .mairc file: %v", err)
	}

	// Read the file
	rcFileContent, err := os.ReadFile(rcFilePath)
	if err != nil {
		return fmt.Errorf("failed to read .mairc file: %v", err)
	}

	// Process each line in the file
	lines := strings.Split(string(rcFileContent), "\n")
	for _, line := range lines {
		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines and lines that don't start with /
		if line == "" || !strings.HasPrefix(line, "/") {
			continue
		}

		// Process the command
		if err := r.handleCommand(line); err != nil {
			fmt.Printf("Error in .mairc: %v\r\n", err)
		}
	}

	return nil
}

func (r *REPL) Run() error {
	defer r.cleanup()

	// Handle interrupt signals
	r.setupSignalHandler()

	fmt.Print(fmt.Sprintf("mai-repl - %s - /help\r\n", strings.ToUpper(r.config.PROVIDER)))

	// Load and process ~/.mairc if not in stdin mode and not skipped
	if !r.config.IsStdinMode && !r.config.SkipRcFile {
		if err := r.loadRCFile(); err != nil {
			fmt.Printf("Error loading .mairc: %v\r\n", err)
		}
	}

	for {
		if err := r.handleInput(); err != nil {
			if err == io.EOF {
				break
			}
			// Don't exit the REPL loop for errors, just print them and continue
			fmt.Fprintf(os.Stderr, "REPL error: %v\r\n", err)
			continue
		}
	}

	return nil
}

func (r *REPL) showCommands() {
	fmt.Print("Commands:\r\n")

	// Sort commands for consistent display
	var cmdNames []string
	for name := range r.commands {
		cmdNames = append(cmdNames, name)
	}
	sort.Strings(cmdNames)

	// Display all registered commands with descriptions
	for _, name := range cmdNames {
		cmd := r.commands[name]
		fmt.Printf("  %-15s - %s\r\n", name, cmd.Description)
	}

	// Display special commands that aren't in the registry
	fmt.Print("  #              - List available prompt files (.md)\r\n")
	fmt.Print("  #<n> <text>    - Use content from prompt file with text\r\n")
	fmt.Print("  $              - List available template files\r\n")
	fmt.Print("  $<n> <text>    - Use template with interactive prompts and optional text\r\n")
	fmt.Print("  !<command>     - Execute shell command\r\n")
	fmt.Print("  _              - Print the last assistant reply\r\n")

	// Display keyboard shortcuts
	fmt.Print("  Ctrl+C         - Cancel current request\r\n")
	fmt.Print("  Ctrl+D         - Exit REPL (when line is empty)\r\n")
	fmt.Print("  Ctrl+W         - Delete last word\r\n")
	fmt.Print("  Up/Down arrows - Navigate history\r\n")
	fmt.Print("  Tab            - Command/path completion\r\n")
	fmt.Print("  @<path>        - File path with tab completion (can appear anywhere in input)\r\n")
	fmt.Print("\r\n")
}

func (r *REPL) cleanup() {
	if r.readline != nil {
		r.readline.Restore()
	} else if r.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), r.oldState)
	}
	r.cancel()
}

// interruptResponse interrupts the current LLM response if one is being generated
func (r *REPL) interruptResponse() {
	r.mu.Lock()
	isStreaming := r.isStreaming
	r.mu.Unlock()

	if isStreaming {
		// Cancel the current context
		r.cancel()

		// Create new context for next request
		r.ctx, r.cancel = context.WithCancel(context.Background())

		// Also interrupt the LLM client if it's active
		client, err := NewLLMClient(r.config)
		if err == nil && client != nil {
			client.InterruptResponse()
			r.mu.Lock()
			if r.currentClient != nil {
				r.currentClient.InterruptResponse()
			}
			r.mu.Unlock()
		}
	}
}

func (r *REPL) setupSignalHandler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		// Reset the current editing line when interrupted
		if r.readline != nil {
			fmt.Print("^C\n")
			r.readline.SetContent("")
		}
		r.interruptResponse()
		r.setupSignalHandler()
	}()
}

func (r *REPL) handleInput() error {
	input, err := r.readLine()
	fmt.Print("\x1b[0m") // Reset color after input
	if err != nil {
		return err
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	// Handle commands (slash- and dot-prefixed, plus '_' for last reply)
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, ".") || input == "_" {
		// Add to history
		r.addToHistory(input)
		return r.handleCommand(input)
	}

	// Handle # prompt commands
	if strings.HasPrefix(input, "#") {
		// Add to history (also added in handlePromptCommand, but keep here for consistency)
		r.addToHistory(input)
		return r.handlePromptCommand(input)
	}

	// Handle $ template commands
	if strings.HasPrefix(input, "$") {
		// Add to history
		r.addToHistory(input)
		return r.handleTemplateCommand(input)
	}

	// Handle ? prompt commands
	if strings.HasPrefix(input, "?") {
		// Add to history (also added in handlePromptCommand, but keep here for consistency)
		r.addToHistory(input)
		return r.handleCommand("/help")
	}

	// Handle shell commands
	if strings.HasPrefix(input, "!") {
		// Add to history
		r.addToHistory(input)
		return r.executeShellCommand(input[1:])
	}

	// Add to history (original input with command substitutions preserved)
	r.addToHistory(input)

	// Send to AI
	return r.sendToAI(input)
}

func (r *REPL) readLine() (string, error) {
	// Ensure we have a readline instance
	if r.readline == nil {
		readLine, err := NewReadLine()
		if err != nil {
			return "", fmt.Errorf("failed to initialize readline: %v", err)
		}
		r.readline = readLine
	}

	// Set the interrupt function to handle Ctrl+C
	r.readline.SetInterruptFunc(r.interruptResponse)

	// Main input loop
	for {
		// Read the line of input
		input, err := r.readline.Read()
		if err != nil {
			return "", err
		}

		// Handle tab completion
		if input == "\t" {
			// Get current content from readline
			currentContent := r.readline.GetContent()

			// Update REPL's cursor position from readline's cursor position
			r.cursorPos = r.readline.GetCursorPos()

			// Debug logging - uncomment if needed
			// fmt.Printf("\r\nDEBUG: Current content: '%s'\r\n", currentContent)
			// fmt.Printf("DEBUG: Prefix: '%s'\r\n", r.completePrefix)
			// fmt.Printf("DEBUG: State: %d, Idx: %d\r\n", r.completeState, r.completeIdx)

			// Set up a builder for tab completion
			var line strings.Builder
			line.WriteString(currentContent)

			// Handle tab completion
			r.handleTabCompletion(&line)

			// Get the updated content
			completedContent := line.String()

			// Only update if content changed
			if completedContent != currentContent {
				r.readline.SetContent(completedContent)
			}
			continue
		}
		// Return the input
		return input, nil
	}
}

func (r *REPL) handleTabCompletion(line *strings.Builder) {
	input := line.String()

	// Only reset completion state if necessary
	// This makes backspace handling simpler - if the user has modified
	// the input so it's no longer related to our completion,
	// we reset the completion state
	if r.completeState != 0 {
		// Reset if input has been modified to be unrelated to current completion
		// but preserve state for proper cycling
		if input != r.completePrefix {
			var currentOption string
			if r.completeIdx < len(r.completeOptions) {
				currentOption = r.completeOptions[r.completeIdx]
			}
			// Check if input matches the option itself (for commands) or prefix+option (for @ path)
			if r.completeIdx >= len(r.completeOptions) ||
				(input != currentOption && input != r.completePrefix+currentOption) {
				r.completeState = 0
				r.completeIdx = 0
				r.completeOptions = nil
				r.completePrefix = ""
			}
		}
	}

	// Check if input contains @ for file path completion
	if strings.Contains(input, "@") {
		// Find the position of @ in the input
		pos := strings.LastIndex(input, "@")

		// Get the prefix (text before @) and the partial path (text after @)
		prefix := input[:pos]
		partialPath := input[pos+1:]

		// Only attempt path completion if we're at or after the @ character
		if r.cursorPos >= pos {
			r.handleAtFilePathCompletion(line, prefix, partialPath)
			return
		}
	}

	// Check if we need to complete a file path for a command that accepts a file
	parts := strings.SplitN(input, " ", 2)
	if len(parts) == 2 && (parts[0] == "/image" || parts[0] == "/file" || parts[0] == ".") {
		r.handleFilePathCompletion(line, parts[0], parts[1])
		return
	}

	// Check for /set promptfile and promptdir value completion
	if len(parts) == 3 && parts[0] == "/set" {
		switch parts[1] {
		case "promptfile":
			// Complete file paths for promptfile
			r.handleFilePathCompletion(line, "/set promptfile", parts[2])
			return
		case "promptdir":
			// Complete directory paths for promptdir
			r.handleDirectoryCompletion(line, "/set promptdir", parts[2])
			return
		}
	}

	// Check for /set, /get, and /unset option completion
	if len(parts) == 2 && (parts[0] == "/set" || parts[0] == "/get" || parts[0] == "/unset") {
		r.handleOptionCompletion(line, parts[0], parts[1])
		return
	}

	// Handle tab completion for /chat subcommands
	if strings.HasPrefix(input, "/chat ") && len(parts) >= 2 {
		if len(parts) == 2 {
			// Complete /chat subcommands
			subcmd := parts[1]
			r.handleChatSubcommandCompletion(line, subcmd)
			return
		} else if len(parts) == 3 && (parts[1] == "save" || parts[1] == "load") {
			// Complete file paths for save/load
			r.handleFilePathCompletion(line, "/chat "+parts[1], parts[2])
			return
		}
	}

	// Only handle tab completion at the beginning of the line for commands
	if !(strings.HasPrefix(input, "/") || strings.HasPrefix(input, "#") || strings.HasPrefix(input, "$")) {
		return
	}

	// Prompt command completion for commands like "#<tab>"
	if strings.HasPrefix(input, "#") {
		needFreshOptions := false
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		if needFreshOptions {
			// Determine prompt directory
			promptDir := r.config.options.Get("promptdir")
			if promptDir == "" {
				for _, loc := range []string{"./prompts", "../prompts"} {
					if _, err := os.Stat(loc); err == nil {
						promptDir = loc
						break
					}
				}
				if promptDir == "" {
					return
				}
			}
			// Read prompt files
			files, err := os.ReadDir(promptDir)
			if err != nil {
				return
			}
			var allPrompts []string
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
					name := strings.TrimSuffix(f.Name(), ".md")
					allPrompts = append(allPrompts, "#"+name)
				}
			}
			sort.Strings(allPrompts)
			r.completePrefix = input
			r.completeOptions = nil
			for _, p := range allPrompts {
				if strings.HasPrefix(p, input) {
					r.completeOptions = append(r.completeOptions, p)
				}
			}
			if len(r.completeOptions) == 0 {
				return
			}
			r.completeState = 1
			r.completeIdx = 0
			first := r.completeOptions[0]
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(first)
			line.Reset()
			line.WriteString(first)
			r.cursorPos = line.Len()
		} else {
			if len(r.completeOptions) <= 1 {
				return
			}
			current := line.String()
			r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
			next := r.completeOptions[r.completeIdx]
			for i := 0; i < len(current); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(next)
			line.Reset()
			line.WriteString(next)
			r.cursorPos = line.Len()
		}
		return
	}

	// Template command completion for commands like "$<tab>"
	if strings.HasPrefix(input, "$") {
		needFreshOptions := false
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		if needFreshOptions {
			// Determine template directory
			templDir := r.config.options.Get("templatedir")
			if templDir == "" {
				for _, loc := range []string{"./templates", "../templates"} {
					if _, err := os.Stat(loc); err == nil {
						templDir = loc
						break
					}
				}
				if templDir == "" {
					return
				}
			}
			files, err := os.ReadDir(templDir)
			if err != nil {
				return
			}
			var allTemps []string
			for _, f := range files {
				if !f.IsDir() {
					base := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
					allTemps = append(allTemps, "$"+base)
				}
			}
			sort.Strings(allTemps)
			r.completePrefix = input
			r.completeOptions = nil
			for _, t := range allTemps {
				if strings.HasPrefix(t, input) {
					r.completeOptions = append(r.completeOptions, t)
				}
			}
			if len(r.completeOptions) == 0 {
				return
			}
			r.completeState = 1
			r.completeIdx = 0
			first := r.completeOptions[0]
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(first)
			line.Reset()
			line.WriteString(first)
			r.cursorPos = line.Len()
		} else {
			if len(r.completeOptions) <= 1 {
				return
			}
			current := line.String()
			r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
			next := r.completeOptions[r.completeIdx]
			for i := 0; i < len(current); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(next)
			line.Reset()
			line.WriteString(next)
			r.cursorPos = line.Len()
		}
		return
	}

	// Command completion for commands like "/no<tab>"
	if strings.HasPrefix(input, "/") {
		// Check if we need to generate fresh completion options
		needFreshOptions := false

		// In these cases we need to generate fresh options:
		// 1. First tab press
		// 2. No options available
		// 3. Back to original command prefix
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		// Do we need to regenerate the completion options?
		if needFreshOptions {
			// Collect all commands from registry
			allCommands := []string{}
			for cmdName := range r.commands {
				allCommands = append(allCommands, cmdName)
			}

			// Sort alphabetically for consistent order
			sort.Strings(allCommands)

			// Store the original prefix for future reference
			r.completePrefix = input

			// Find all commands that match our prefix
			r.completeOptions = []string{}
			for _, cmd := range allCommands {
				if strings.HasPrefix(cmd, input) {
					r.completeOptions = append(r.completeOptions, cmd)
				}
			}

			// No matches found
			if len(r.completeOptions) == 0 {
				return
			}

			// Update completion state
			r.completeState = 1 // Entering tab cycle mode
			r.completeIdx = 0   // Start with first option

			// Show first match
			firstMatch := r.completeOptions[0]

			// Clear current input
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}

			// Show the match
			fmt.Print(firstMatch)
			line.Reset()
			line.WriteString(firstMatch)
			r.cursorPos = line.Len()
		} else {
			// We're cycling through options with subsequent tab presses
			// Make sure we have multiple options to cycle through
			if len(r.completeOptions) <= 1 {
				return
			}

			// Get current input text
			currentText := line.String()

			// Advance to next option
			r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
			nextOption := r.completeOptions[r.completeIdx]

			// Clear current line
			for i := 0; i < len(currentText); i++ {
				fmt.Print("\b \b")
			}

			// Show next option
			fmt.Print(nextOption)
			line.Reset()
			line.WriteString(nextOption)
			r.cursorPos = line.Len()
		}
		return // Command completion handled
	}
}

func (r *REPL) addToHistory(input string) {
	r.readline.AddToHistory(input)
}

// handleChatCommand handles the /chat command and its subcommands
func (r *REPL) handleChatCommand(args []string) error {
	// Show help if no arguments provided
	if len(args) < 2 {
		fmt.Print("Chat conversation management commands:\r\n")
		fmt.Print("  /chat save <path> - Save conversation history to file\r\n")
		fmt.Print("  /chat load <path> - Load conversation history from file\r\n")
		fmt.Print("  /chat clear      - Clear conversation messages\r\n")
		fmt.Print("  /chat list       - Display conversation messages (truncated)\r\n")
		fmt.Print("  /chat log        - Display full conversation with preserved formatting\r\n")
		fmt.Print("  /chat undo [N]   - Remove last or Nth message\r\n")
		fmt.Print("  /chat compact    - Compact conversation into a single message\r\n")
		return nil
	}

	// Handle subcommands
	action := args[1]
	switch action {
	case "save":
		if len(args) < 3 {
			fmt.Print("Usage: /chat save <path>\r\n")
			return nil
		}
		return r.saveConversation(args[2])
	case "load":
		if len(args) < 3 {
			fmt.Print("Usage: /chat load <path>\r\n")
			return nil
		}
		return r.loadConversation(args[2])
	case "clear":
		r.messages = []Message{}
		fmt.Print("Conversation messages cleared\r\n")
		return nil
	case "list":
		r.displayConversationLog()
		return nil
	case "log":
		r.displayFullConversationLog()
		return nil
	case "undo":
		if len(args) > 2 {
			// Parse the index argument
			r.undoMessageByIndex(args[2])
		} else {
			// Default behavior - remove the last message
			r.undoLastMessage()
		}
		return nil
	case "compact":
		return r.handleCompactCommand()
	default:
		fmt.Printf("Unknown action: %s\r\n", action)
		fmt.Print("Available actions: save, load, clear, list, log, undo, compact\r\n")
		return nil
	}
}

func (r *REPL) handleCommand(input string) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	command := parts[0]

	// Check if the command exists in the registry
	if cmd, exists := r.commands[command]; exists {
		// Execute the command handler
		return cmd.Handler(r, parts)
	} else {
		fmt.Printf("Unknown command: %s\n\r", command)
	}

	return nil
}

func (r *REPL) addImage(imagePath string) error {
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

	// Encode to base64 and build data URI
	encoded := base64.StdEncoding.EncodeToString(imageData)
	mimeType := http.DetectContentType(imageData)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

	// Add to pending files with data URI for image
	r.pendingFiles = append(r.pendingFiles, pendingFile{
		filePath: imagePath,
		isImage:  true,
		imageB64: dataURI,
	})

	r.addToHistory(fmt.Sprintf("/image %s", imagePath))
	fmt.Printf("Image added: %s (%d bytes). Send a message to analyze it.\r\n",
		filepath.Base(imagePath), len(imageData))
	return nil
}

func (r *REPL) addFile(filePath string) error {
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

	// Add to pending files
	r.pendingFiles = append(r.pendingFiles, pendingFile{
		filePath: filePath,
		content:  string(content),
		isImage:  false,
	})

	r.addToHistory(fmt.Sprintf("/file %s", filePath))
	fmt.Printf("File added: %s (%d bytes). Send a message to analyze it.\r\n",
		filepath.Base(filePath), len(content))
	return nil
}

func (r *REPL) sendToAI(input string) error {
	r.mu.Lock()
	r.isStreaming = true
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.isStreaming = false
		r.currentClient = nil
		r.mu.Unlock()
	}()

	// Process command substitutions in the input
	// originalInput := input // Store the original input for history
	processedInput, err := ExecuteCommandSubstitution(input)
	if err != nil {
		return fmt.Errorf("command substitution failed: %v", err)
	}
	input = processedInput

	// Process backtick substitutions
	processedInput, err = ExecuteBacktickSubstitution(input, r)
	if err != nil {
		return fmt.Errorf("backtick substitution failed: %v", err)
	}
	input = processedInput

	// Process environment variable substitutions
	processedInput, err = ExecuteEnvVarSubstitution(input)
	if err != nil {
		return fmt.Errorf("environment variable substitution failed: %v", err)
	}
	input = processedInput

	// Create client
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	r.mu.Lock()
	r.currentClient = client
	r.mu.Unlock()

	if r.useToolsEnabled {
		// new json mode
		tool, err := r.QueryWithTools(input)
		if err != nil {
			return fmt.Errorf("tool execution failed: %v", err)
		}
		input = tool
	}

	// Add system prompt if present
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}

	// Handle conversation history based on logging and reply settings
	if r.loggingEnabled {
		// When logging is enabled, use normal message history behavior
		if r.includeReplies {
			// Include all messages
			messages = append(messages, r.messages...)
		} else {
			// Include only user messages
			for _, msg := range r.messages {
				if msg.Role == "user" {
					messages = append(messages, msg)
				}
			}
		}
	}
	// When logging is disabled, we don't append any previous messages

	// Process @mentions in the input
	enhancedInput := r.processAtMentions(input)

	// Process pending files and incorporate them into the input
	var images []string // For storing base64 encoded images for Ollama

	if len(r.pendingFiles) > 0 {
		// Add file contents to the input
		enhancedInput += "\n\n"

		for _, file := range r.pendingFiles {
			if file.isImage {
				// For images, we'll collect them separately for providers that support image attachments
				images = append(images, file.imageB64)
				enhancedInput += fmt.Sprintf("[Image attached: %s]\n", filepath.Base(file.filePath))
			} else {
				// For regular files, add the content
				enhancedInput += fmt.Sprintf("File content from %s:\n```\n%s\n```\n\n",
					file.filePath, file.content)
			}
		}

		// Clear pending files after use
		r.pendingFiles = []pendingFile{}
	}

	// Add user message with enhanced input
	// Store the original input (with commands) for display in message history,
	// but use the processed input (with command output) for sending to the AI
	userMessage := Message{Role: "user", Content: enhancedInput}

	// Handle conversation history based on logging settings
	if r.loggingEnabled {
		// Save the user message to conversation history when logging is enabled
		r.messages = append(r.messages, userMessage)
	} else {
		// When logging is disabled, replace the entire history with just this message
		r.messages = []Message{userMessage}
	}

	// If reasoning is disabled, append /no_think to the last message sent to the LLM
	if !r.reasoningEnabled {
		// Create a copy of the messages for the API call with /no_think appended
		messagesCopy := make([]Message, len(messages))
		copy(messagesCopy, messages)

		// Append the user message with /no_think to the copy
		messagesCopy = append(messagesCopy, Message{Role: "user", Content: enhancedInput + "/no_think"})
		messages = messagesCopy
	} else {
		// Add the original user message
		messages = append(messages, userMessage)
	}

	// Reset the markdown processor state before starting a new streaming session
	if r.streamingEnabled && r.markdownEnabled {
		ResetStreamRenderer()
	}

	// Send message with streaming based on REPL settings
	response, err := client.SendMessageWithImages(messages, r.streamingEnabled, images)

	// Handle the assistant's response based on logging settings
	if err == nil && response != "" {
		// If not streaming, we need to print the response here
		if !r.streamingEnabled {
			if r.markdownEnabled {
				// Use markdown formatting
				fmt.Print(RenderMarkdown(response))
			} else {
				// Use standard formatting
				fmt.Print(strings.ReplaceAll(response, "\n", "\r\n"))
			}
		}

		// Create assistant message
		assistantMessage := Message{Role: "assistant", Content: response}

		if r.loggingEnabled {
			// Save to conversation history when logging is enabled
			r.messages = append(r.messages, assistantMessage)
		} else {
			// When logging is disabled, keep just the current exchange
			r.messages = []Message{userMessage, assistantMessage}
		}
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

// getLastAssistantReply returns the content of the last assistant reply in the conversation
func (r *REPL) getLastAssistantReply() (string, error) {
	// Iterate backwards through messages to find the last assistant message
	for i := len(r.messages) - 1; i >= 0; i-- {
		if r.messages[i].Role == "assistant" {
			return r.messages[i].Content.(string), nil
		}
	}
	return "", fmt.Errorf("no assistant replies found in conversation history")
}

// handleSlurpCommand reads from stdin until EOF (Ctrl+D) and returns the content
func (r *REPL) handleSlurpCommand() error {
	// Save the current terminal state
	oldState, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to get terminal state: %v", err)
	}

	// Restore the terminal to normal mode so we can read multiline text
	term.Restore(int(os.Stdin.Fd()), oldState)

	fmt.Println("Enter your text (press Ctrl+D when finished):")

	// Read from stdin until EOF
	var content strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		content.WriteString(scanner.Text())
		content.WriteString("\n")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		// Make terminal raw again
		MakeRawPreserveNewline(int(os.Stdin.Fd()))
		return fmt.Errorf("error reading input: %v", err)
	}

	// Make terminal raw again
	MakeRawPreserveNewline(int(os.Stdin.Fd()))

	// Get the content
	input := content.String()

	if input == "" {
		fmt.Println("No input provided.")
		return nil
	}

	// Send the input to the AI
	return r.sendToAI(input)
}

// initCommands initializes the command registry with all available commands
func (r *REPL) initCommands() {
	// Helper commands
	r.commands["/help"] = Command{
		Name:        "/help",
		Description: "Show available commands",
		Handler: func(r *REPL, args []string) error {
			r.showCommands()
			return nil
		},
	}

	r.commands["/slurp"] = Command{
		Name:        "/slurp",
		Description: "Read from stdin until EOF (Ctrl+D)",
		Handler: func(r *REPL, args []string) error {
			return r.handleSlurpCommand()
		},
	}

	// Dot command: read one or more files and send their combined contents as a prompt
	r.commands["."] = Command{
		Name:        ".",
		Description: "Load file(s) and send contents as a single prompt",
		Handler: func(r *REPL, args []string) error {
			if len(args) < 2 {
				fmt.Print("Usage: . <path>\n\r")
				return nil
			}
			var buf strings.Builder
			for _, path := range args[1:] {
				data, err := os.ReadFile(path)
				if err != nil {
					fmt.Printf("failed to read file '%s': %v\n\r", path, err)
					return nil
				}
				buf.Write(data)
				buf.WriteString("\n")
			}
			return r.sendToAI(buf.String())
		},
	}

	// File handling commands
	r.commands["/image"] = Command{
		Name:        "/image",
		Description: "Add an image to the next message",
		Handler: func(r *REPL, args []string) error {
			if len(args) < 2 {
				fmt.Print("Usage: /image <path>\n\r")
				return nil
			}
			return r.addImage(args[1])
		},
	}

	r.commands["/file"] = Command{
		Name:        "/file",
		Description: "Add a file to the next message",
		Handler: func(r *REPL, args []string) error {
			if len(args) < 2 {
				fmt.Print("Usage: /file <path>\n\r")
				return nil
			}
			return r.addFile(args[1])
		},
	}

	r.commands["/noimage"] = Command{
		Name:        "/noimage",
		Description: "Remove pending images",
		Handler: func(r *REPL, args []string) error {
			return r.clearPendingImages()
		},
	}

	r.commands["/nofiles"] = Command{
		Name:        "/nofiles",
		Description: "Remove pending files",
		Handler: func(r *REPL, args []string) error {
			return r.clearPendingFiles()
		},
	}

	// Configuration commands
	r.commands["/set"] = Command{
		Name:        "/set",
		Description: "Set or display configuration option",
		Handler: func(r *REPL, args []string) error {
			return r.handleSetCommand(args)
		},
	}

	r.commands["/get"] = Command{
		Name:        "/get",
		Description: "Display configuration option value",
		Handler: func(r *REPL, args []string) error {
			return r.handleGetCommand(args)
		},
	}

	r.commands["/unset"] = Command{
		Name:        "/unset",
		Description: "Unset configuration option",
		Handler: func(r *REPL, args []string) error {
			return r.handleUnsetCommand(args)
		},
	}

	// Conversation management commands
	r.commands["/chat"] = Command{
		Name:        "/chat",
		Description: "Manage conversation (save, load, clear, list, log, undo, compact)",
		Handler: func(r *REPL, args []string) error {
			return r.handleChatCommand(args)
		},
	}

	// /compact command moved under /chat

	r.commands["/cancel"] = Command{
		Name:        "/cancel",
		Description: "Cancel current request",
		Handler: func(r *REPL, args []string) error {
			r.cancel()
			r.ctx, r.cancel = context.WithCancel(context.Background())
			return nil
		},
	}

	r.commands["/clear"] = Command{
		Name:        "/clear",
		Description: "Clear conversation messages",
		Handler: func(r *REPL, args []string) error {
			r.messages = []Message{}
			fmt.Print("Conversation messages cleared\r\n")
			return nil
		},
	}

	// Last reply command
	r.commands["_"] = Command{
		Name:        "_",
		Description: "Print the last assistant reply",
		Handler: func(r *REPL, args []string) error {
			content, err := r.getLastAssistantReply()
			if err != nil {
				fmt.Printf("%v\r\n", err)
				return nil
			}

			// Print the content with markdown rendering if enabled
			if r.markdownEnabled {
				fmt.Print(RenderMarkdown(content))
			} else {
				// Replace single newlines with \r\n for proper terminal display
				content = strings.ReplaceAll(content, "\n", "\r\n")
				fmt.Print(content)
			}
			fmt.Print("\r\n")
			return nil
		},
	}

	// Exit commands
	r.commands["/quit"] = Command{
		Name:        "/quit",
		Description: "Exit REPL",
		Handler: func(r *REPL, args []string) error {
			return io.EOF
		},
	}

	r.commands["/exit"] = Command{
		Name:        "/exit",
		Description: "Exit REPL",
		Handler: func(r *REPL, args []string) error {
			return io.EOF
		},
	}

	r.commands["/tool"] = Command{
		Name:        "/tool",
		Description: "Execute the mai-tool command, passing arguments",
		Handler: func(r *REPL, args []string) error {
			return r.handleToolCommand(args)
		},
	}

	// System prompt shortcuts
	r.commands["/prompt"] = Command{
		Name:        "/prompt",
		Description: "Show current system prompt",
		Handler: func(r *REPL, args []string) error {
			if r.systemPrompt == "" {
				fmt.Print("No system prompt set\r\n")
			} else {
				fmt.Printf("System prompt (%d chars):\r\n%s\r\n", len(r.systemPrompt), r.systemPrompt)
			}
			return nil
		},
	}

	r.commands["/noprompt"] = Command{
		Name:        "/noprompt",
		Description: "Clear system prompt",
		Handler: func(r *REPL, args []string) error {
			r.systemPrompt = ""
			r.config.options.Unset("promptfile")
			fmt.Print("System prompt cleared\r\n")
			return nil
		},
	}

	// Only keep the models command for listing available models
	r.commands["/models"] = Command{
		Name:        "/models",
		Description: "List available models",
		Handler: func(r *REPL, args []string) error {
			return r.listModels()
		},
	}

	// Command to list available providers
	r.commands["/providers"] = Command{
		Name:        "/providers",
		Description: "List available providers",
		Handler: func(r *REPL, args []string) error {
			return r.listProviders()
		},
	}
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
			return nil // Return without error when context is canceled
		default:
			// Continue processing
		}
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

		// Format the content based on markdown setting
		content := response.Message.Content
		if r.markdownEnabled {
			content = FormatStreamingChunk(content, true)
		} else {
			content = strings.ReplaceAll(content, "\n", "\r\n")
		}
		fmt.Print(content)

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
			return nil // Return without error when context is canceled
		default:
			// Continue processing
		}
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
			// Format the content based on markdown setting
			content := response.Choices[0].Delta.Content
			if r.markdownEnabled {
				content = FormatStreamingChunk(content, true)
			} else {
				content = strings.ReplaceAll(content, "\n", "\r\n")
			}
			fmt.Print(content)
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
			return nil // Return without error when context is canceled
		default:
			// Continue processing
		}
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
			// Format the content based on markdown setting
			content := response.Delta.Text
			if r.markdownEnabled {
				content = FormatStreamingChunk(content, true)
			} else {
				content = strings.ReplaceAll(content, "\n", "\r\n")
			}
			fmt.Print(content)
		}
	}

	fmt.Println()
	return scanner.Err()
}

// Load system prompt from a file

// handlePromptCommand handles the # command for prompt expansion
func (r *REPL) handlePromptCommand(input string) error {
	// Split the input into command and arguments
	parts := strings.SplitN(input, " ", 2)
	promptName := parts[0][1:] // Remove the # prefix

	// If no prompt name is provided, list all .md files from promptdir
	if promptName == "" {
		return r.listPrompts()
	}

	// Load the prompt file content
	promptPath, err := r.resolvePromptPath(promptName)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}

	// Read the prompt file content
	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}

	// Replace the #command with the file content in the input
	expandedInput := string(promptContent)
	if len(parts) > 1 && parts[1] != "" {
		expandedInput += "\n\n" + parts[1]
	}

	// Send expanded input to AI
	return r.sendToAI(expandedInput)
}

// listPrompts lists all .md files in the promptdir
func (r *REPL) listPrompts() error {
	// Get the prompt directory from config
	promptDir := r.config.options.Get("promptdir")
	if promptDir == "" {
		// Try common locations
		commonLocations := []string{
			"./prompts",
			"../prompts",
		}

		found := false
		for _, loc := range commonLocations {
			if _, err := os.Stat(loc); err == nil {
				promptDir = loc
				found = true
				break
			}
		}

		if !found {
			fmt.Print("No prompt directory found. Set one with /set promptdir <path>\r\n")
			return nil
		}
	}

	// List all .md files in the directory
	files, err := os.ReadDir(promptDir)
	if err != nil {
		fmt.Printf("Error reading prompt directory: %v\r\n", err)
		return nil
	}

	// Filter for .md files and display
	mdFiles := []string{}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".md") {
			baseName := strings.TrimSuffix(file.Name(), ".md")
			mdFiles = append(mdFiles, baseName)
		}
	}

	if len(mdFiles) == 0 {
		fmt.Printf("No prompt files (.md) found in %s\r\n", promptDir)
		return nil
	}

	fmt.Printf("Available prompts (use # followed by name):\r\n")
	for _, file := range mdFiles {
		fmt.Printf("  %s\r\n", file)
	}

	return nil
}

// handleFilePathCompletion handles tab completion for file paths

// handleAtFilePathCompletion handles tab completion for file paths with @ prefix
func (r *REPL) handleAtFilePathCompletion(line *strings.Builder, prefix, partialPath string) {
	// Normalize backslashes to forward slashes for consistent path handling
	partialPath = strings.ReplaceAll(partialPath, "\\", "/")

	// Handle special case where partialPath is empty
	if partialPath == "" {
		partialPath = "."
	}

	// Expand ~ to home directory if present
	if strings.HasPrefix(partialPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			partialPath = filepath.Join(homeDir, partialPath[1:])
		}
	}

	// First tab press - generate options
	if r.completeState == 0 {
		// Get the directory and file prefix
		dir, filePrefix := filepath.Split(partialPath)

		// If no directory specified, use current directory
		if dir == "" {
			dir = "."
		} else if !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "./") && !strings.HasPrefix(dir, "../") {
			// Handle relative paths that don't start with ./ or ../
			dir = "." + string(filepath.Separator) + dir
		}

		// Make sure dir ends with separator for directory operations
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}

		// Read the directory
		files, err := os.ReadDir(dir)
		if err != nil {
			// Cannot read directory - just return without changing anything
			return
		}

		// Find matching files at current level only
		r.completeOptions = nil
		for _, file := range files {
			name := file.Name()
			// Only show files that match the prefix
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(filePrefix)) {
				pathToAdd := dir + name
				// Add separator if it's a directory
				if file.IsDir() {
					pathToAdd += string(filepath.Separator)
				}
				r.completeOptions = append(r.completeOptions, pathToAdd)
			}
		}

		// Sort options alphabetically for consistent behavior
		sort.Strings(r.completeOptions)

		// If no matches, do nothing
		if len(r.completeOptions) == 0 {
			return
		}

		// Set up completion state
		r.completeState = 1
		r.completePrefix = prefix + "@"
		r.completeIdx = 0 // Start with first option

		// Show first match
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
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Simple cycling through options
		r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[r.completeIdx]

		// Clear current line
		currentInput := line.String()
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
		r.cursorPos = line.Len()
	}
}

// handleChatSubcommandCompletion handles tab completion for /chat subcommands
func (r *REPL) handleChatSubcommandCompletion(line *strings.Builder, partialCmd string) {
	// Available chat subcommands
	subcommands := []string{"save", "load", "clear", "list", "log", "undo", "compact"}

	// Filter subcommands by the partial input
	var filteredCommands []string
	for _, cmd := range subcommands {
		if strings.HasPrefix(cmd, partialCmd) {
			filteredCommands = append(filteredCommands, cmd)
		}
	}

	// If no matches, return
	if len(filteredCommands) == 0 {
		return
	}

	// If this is the first tab press, set the state and show the first match
	if r.completeState == 0 {
		r.completeState = 1
		r.completeOptions = filteredCommands
		r.completePrefix = "/chat "

		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + filteredCommands[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option
		currentInput := line.String()
		currentCmd := strings.TrimPrefix(currentInput, r.completePrefix)

		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentCmd {
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
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

// handleDirectoryCompletion handles tab completion for directory paths
func (r *REPL) handleDirectoryCompletion(line *strings.Builder, cmd, partialPath string) {
	// Expand ~ to home directory if present
	if strings.HasPrefix(partialPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			partialPath = filepath.Join(homeDir, partialPath[1:])
		}
	}

	// If this is the first tab press, find matching directories
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

		// Find matching directories only
		r.completeOptions = nil
		for _, file := range files {
			name := file.Name()
			if strings.HasPrefix(name, prefix) && file.IsDir() {
				// Add separator for directories
				name += string(filepath.Separator)
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
		// Update cursor position to end of line
		r.cursorPos = line.Len()
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
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

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
		// Update cursor position to end of line
		r.cursorPos = line.Len()
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
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

// executeLLMQueryWithoutStreaming executes an LLM query without streaming and returns the result
func (r *REPL) executeLLMQueryWithoutStreaming(query string) (string, error) {
	// Create a new client for this query
	client, err := NewLLMClient(r.config)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Process the query similar to sendToAI but without streaming
	// Process command substitutions in the input
	processedQuery, err := ExecuteCommandSubstitution(query)
	if err != nil {
		return "", fmt.Errorf("command substitution failed: %v", err)
	}

	// Process environment variable substitutions
	processedQuery, err = ExecuteEnvVarSubstitution(processedQuery)
	if err != nil {
		return "", fmt.Errorf("environment variable substitution failed: %v", err)
	}

	// Build the messages array
	messages := []Message{}
	if r.systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: r.systemPrompt})
	}

	// Add conversation history if we should include replies
	if r.includeReplies && len(r.messages) > 0 {
		messages = append(messages, r.messages...)
	}

	// Add the user query
	messages = append(messages, Message{Role: "user", Content: processedQuery})

	// Call the LLM with streaming disabled
	response, err := client.SendMessage(messages, false)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %v", err)
	}

	// Return the response
	return response, nil
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

// loadSystemPrompt loads a system prompt from a file and updates the config
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

	// Update the promptfile configuration
	r.config.options.Set("promptfile", path)

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
		content := msg.Content.(string)
		if len(content) > 100 {
			content = content[:97] + "..."
		}

		// Replace newlines with space for compact display
		content = strings.ReplaceAll(content, "\n", " ")

		fmt.Printf("%s\r\n", content)
	}

	fmt.Printf("Total messages: %d\r\n", len(r.messages))
	fmt.Printf("Settings: replies=%t, streaming=%t, reasoning=%t, logging=%t\r\n",
		r.includeReplies, r.streamingEnabled, r.reasoningEnabled, r.loggingEnabled)

	// Display pending files if any
	if len(r.pendingFiles) > 0 {
		fmt.Print("\r\nPending files for next message:\r\n")
		imageCount := 0
		fileCount := 0

		for _, file := range r.pendingFiles {
			if file.isImage {
				imageCount++
				fmt.Printf(" - Image: %s\r\n", file.filePath)
			} else {
				fileCount++
				fmt.Printf(" - File: %s\r\n", file.filePath)
			}
		}

		fmt.Printf("Total pending: %d images, %d files\r\n", imageCount, fileCount)
	}
}

// displayFullConversationLog prints the complete conversation without truncating or filtering
func (r *REPL) displayFullConversationLog() {
	if len(r.messages) == 0 {
		fmt.Print("No conversation messages yet\r\n")
		return
	}

	fmt.Print("# Full conversation log:\r\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)

		fmt.Printf("\r\n## [%d] %s:\r\n", i+1, role)

		// Print the full content with preserved formatting
		// Apply markdown rendering if enabled
		if r.markdownEnabled {
			fmt.Printf("%s\r\n", RenderMarkdown(msg.Content.(string)))
		} else {
			// Replace single newlines with \r\n for proper terminal display
			content := strings.ReplaceAll(msg.Content.(string), "\n", "\r\n")
			fmt.Printf("%s\r\n", content)
		}
		fmt.Print("--------------------\r\n")
	}

	fmt.Printf("\r\nTotal messages: %d\r\n", len(r.messages))
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
	content := truncateContent(lastMsg.Content.(string))

	fmt.Printf("Removed last message (%s: %s)\r\n", role, content)
	fmt.Printf("Remaining messages: %d\r\n", len(r.messages))
}

// clearPendingImages removes all pending images
func (r *REPL) clearPendingImages() error {
	imageCount := 0

	// Count images and remove them from pendingFiles
	var remainingFiles []pendingFile
	for _, file := range r.pendingFiles {
		if file.isImage {
			imageCount++
		} else {
			remainingFiles = append(remainingFiles, file)
		}
	}

	r.pendingFiles = remainingFiles

	if imageCount > 0 {
		fmt.Printf("Removed %d pending image(s)\r\n", imageCount)
	} else {
		fmt.Print("No pending images to remove\r\n")
	}

	return nil
}

// clearPendingFiles removes all pending non-image files
func (r *REPL) clearPendingFiles() error {
	fileCount := 0

	// Count regular files and remove them from pendingFiles
	var remainingFiles []pendingFile
	for _, file := range r.pendingFiles {
		if !file.isImage {
			fileCount++
		} else {
			remainingFiles = append(remainingFiles, file)
		}
	}

	r.pendingFiles = remainingFiles

	if fileCount > 0 {
		fmt.Printf("Removed %d pending file(s)\r\n", fileCount)
	} else {
		fmt.Print("No pending files to remove\r\n")
	}

	return nil
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
	content := truncateContent(msg.Content.(string))

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

// processAtMentions extracts words starting with @ from input text,
// checks if they correspond to existing files, and returns the enhanced prompt
func (r *REPL) processAtMentions(input string) string {
	// Use regex to find all words starting with @ including dots for filenames
	re := regexp.MustCompile(`@[\w.]+`)
	matches := re.FindAllString(input, -1)

	if len(matches) == 0 {
		return input // No @mentions found, return original input
	}

	// Process each @mention
	var fileContents []string
	var processedFiles []string

	for _, match := range matches {
		// Remove the @ prefix to get the filename
		filename := match[1:] // Skip the @ character

		// Check if the file exists in the current directory
		if _, err := os.Stat(filename); err == nil {
			// File exists, read its content
			content, err := os.ReadFile(filename)
			if err == nil {
				// Format the content with markdown
				fileContent := fmt.Sprintf("\n\n## File: %s\n\n```\n%s\n```", filename, string(content))
				fileContents = append(fileContents, fileContent)
				processedFiles = append(processedFiles, filename)
			}
		}
	}

	// If no valid files were found, return the original input
	if len(fileContents) == 0 {
		return input
	}

	// Notify the user about processed @mentions
	if len(processedFiles) > 0 {
		fmt.Print("\r\nProcessed @mentions: ")
		for i, file := range processedFiles {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s", file)
		}
		fmt.Print("\r\n")
	}

	// Append all file contents to the original input
	enhancedInput := input
	for _, content := range fileContents {
		enhancedInput += content
	}

	return enhancedInput
}

// autoDetectPromptDir attempts to find a prompts directory relative to the executable path
// and sets the promptdir config variable if found
func (r *REPL) autoDetectPromptDir() {
	// Skip if promptdir is already set
	if r.config.options.Get("promptdir") != "" {
		return
	}

	// Get the executable path
	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("Warning: Could not determine executable path: %v\r\n", err)
		return
	}

	// Follow symlink if the executable is a symlink
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		fmt.Printf("Warning: Could not evaluate symlinks: %v\r\n", err)
		realPath = execPath // Fall back to the original path
	}

	// Get the directory containing the executable
	execDir := filepath.Dir(realPath)

	// Start searching from the executable directory and go up to root
	currentDir := execDir
	for {
		// Check if a prompts directory exists in the current directory
		promptsDir := filepath.Join(currentDir, "prompts")
		if _, err := os.Stat(promptsDir); err == nil {
			// Found a prompts directory
			r.config.options.Set("promptdir", promptsDir)
			fmt.Printf("Auto-detected prompts directory: %s\r\n", promptsDir)
			return
		}

		// Move up one directory
		parentDir := filepath.Dir(currentDir)

		// Stop if we've reached the root directory
		if parentDir == currentDir {
			break
		}

		// Continue with the parent directory
		currentDir = parentDir
	}
}

// showCurrentModel displays the current model based on the provider
func (r *REPL) showCurrentModel() {
	switch strings.ToLower(r.config.PROVIDER) {
	case "ollama":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.OllamaModel, r.config.PROVIDER)
	case "openai":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.OpenAIModel, r.config.PROVIDER)
	case "claude":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.ClaudeModel, r.config.PROVIDER)
	case "gemini", "google":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.GeminiModel, r.config.PROVIDER)
	case "mistral":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.MistralModel, r.config.PROVIDER)
	case "deepseek":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.DeepSeekModel, r.config.PROVIDER)
	case "bedrock", "aws":
		fmt.Printf("Current model: %s (provider: %s)\r\n", r.config.BedrockModel, r.config.PROVIDER)
	default:
		fmt.Printf("Unknown provider: %s\r\n", r.config.PROVIDER)
	}
}

// setModel changes the model for the current provider
func (r *REPL) setModel(model string) error {
	switch strings.ToLower(r.config.PROVIDER) {
	case "ollama":
		r.config.OllamaModel = model
		r.config.options.Set("model", model)
		fmt.Printf("Ollama model set to %s\r\n", model)
	case "openai":
		r.config.OpenAIModel = model
		r.config.options.Set("model", model)
		fmt.Printf("OpenAI model set to %s\r\n", model)
	case "claude":
		r.config.ClaudeModel = model
		r.config.options.Set("model", model)
		fmt.Printf("Claude model set to %s\r\n", model)
	case "gemini", "google":
		r.config.GeminiModel = model
		r.config.options.Set("model", model)
		fmt.Printf("Gemini model set to %s\r\n", model)
	case "mistral":
		r.config.MistralModel = model
		r.config.options.Set("model", model)
		fmt.Printf("Mistral model set to %s\r\n", model)
	case "deepseek":
		r.config.DeepSeekModel = model
		r.config.options.Set("model", model)
		fmt.Printf("DeepSeek model set to %s\r\n", model)
	case "bedrock", "aws":
		r.config.BedrockModel = model
		r.config.options.Set("model", model)
		fmt.Printf("Bedrock model set to %s\r\n", model)
	default:
		return fmt.Errorf("unknown provider: %s", r.config.PROVIDER)
	}
	return nil
}

// showCurrentProvider displays the current provider
func (r *REPL) showCurrentProvider() {
	fmt.Printf("Current provider: %s\r\n", r.config.PROVIDER)
	// Also show the current model for this provider
	r.showCurrentModel()
}

// getValidProviders returns a map of valid providers
func (r *REPL) getValidProviders() map[string]bool {
	return map[string]bool{
		"ollama":   true,
		"openai":   true,
		"claude":   true,
		"gemini":   true,
		"google":   true,
		"mistral":  true,
		"deepseek": true,
		"bedrock":  true,
		"aws":      true,
	}
}

// listProviders displays all available providers
func (r *REPL) listProviders() error {
	validProviders := r.getValidProviders()

	// Extract provider names and sort them
	providers := make([]string, 0, len(validProviders))
	for provider := range validProviders {
		// Skip aliases (like "google" for "gemini" and "aws" for "bedrock")
		if provider == "google" || provider == "aws" {
			continue
		}
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	fmt.Print("Available providers:\r\n")
	for _, provider := range providers {
		if provider == r.config.PROVIDER {
			fmt.Printf("* %s (current)\r\n", provider)
		} else {
			fmt.Printf("  %s\r\n", provider)
		}
	}

	fmt.Print("\r\nUse '/set provider <name>' to change the current provider\r\n")
	return nil
}

// setProvider changes the current provider
func (r *REPL) setProvider(provider string) error {
	// Check if the provider is valid
	validProviders := r.getValidProviders()

	// Convert provider to lowercase for case-insensitive comparison
	provider = strings.ToLower(provider)

	if !validProviders[provider] {
		fmt.Printf("Invalid provider: %s\r\n", provider)
		fmt.Print("Valid providers: ollama, lmstudio, openai, claude, gemini/google, mistral, deepseek, bedrock/aws\r\n")
		return nil
	}

	// Set the new provider
	r.config.PROVIDER = provider
	// Update the provider in the config options
	r.config.options.Set("provider", provider)

	fmt.Printf("Provider set to %s\r\n", provider)

	// Also show the current model for this provider
	r.showCurrentModel()

	return nil
}

// listModels fetches and displays available models for the current provider
func (r *REPL) listModels() error {
	// Create client
	client, err := NewLLMClient(r.config)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	fmt.Printf("Fetching available models for %s...\r\n", r.config.PROVIDER)

	// Get models from the provider
	models, err := client.ListModels()
	if err != nil {
		return fmt.Errorf("failed to fetch models: %v", err)
	}

	if len(models) == 0 {
		fmt.Print("No models available for this provider\r\n")
		return nil
	}

	// Display models
	fmt.Printf("Available %s models:\r\n", r.config.PROVIDER)
	fmt.Print("-----------------------\r\n")

	// Get current model for highlighting
	currentModel := r.getCurrentModelForProvider()

	// Format and display each model
	for i, model := range models {
		// Add indicator for current model
		current := ""
		if model.ID == currentModel {
			current = " (current)"
		}

		// Display model with description if available
		if model.Description != "" {
			fmt.Printf("[%d] %s%s - %s\r\n", i+1, model.ID, current, model.Description)
		} else {
			fmt.Printf("[%d] %s%s\r\n", i+1, model.ID, current)
		}
	}

	fmt.Printf("Total models: %d\r\n", len(models))
	fmt.Print("Use '/set model <model-id>' to change the model\r\n")

	return nil
}

// getCurrentModelForProvider returns the current model ID for the active provider
func (r *REPL) getCurrentModelForProvider() string {
	switch strings.ToLower(r.config.PROVIDER) {
	case "ollama":
		return r.config.OllamaModel
	case "openai":
		return r.config.OpenAIModel
	case "claude":
		return r.config.ClaudeModel
	case "gemini", "google":
		return r.config.GeminiModel
	case "mistral":
		return r.config.MistralModel
	case "deepseek":
		return r.config.DeepSeekModel
	case "bedrock", "aws":
		return r.config.BedrockModel
	default:
		return ""
	}
}

// handleCompactCommand processes the /compact command
// It loads the compact.txt prompt and submits the entire conversation history
// to the AI, then replaces all messages with the AI's response
// handleOptionCompletion handles tab completion for configuration options
func (r *REPL) handleOptionCompletion(line *strings.Builder, cmd, partialOption string) {
	var options []string

	if cmd == "/set" || cmd == "/get" {
		// For /set and /get, show all available options
		options = GetAvailableOptions()
	} else if cmd == "/unset" {
		// For /unset, show only options that are currently set
		options = r.config.options.GetKeys()
	}

	// Filter options by the partial input
	var filteredOptions []string
	for _, opt := range options {
		if strings.HasPrefix(opt, partialOption) {
			filteredOptions = append(filteredOptions, opt)
		}
	}

	// If no matches, return
	if len(filteredOptions) == 0 {
		return
	}

	// If this is the first tab press, set the state and show the first match
	if r.completeState == 0 {
		r.completeState = 1
		r.completeOptions = filteredOptions
		r.completePrefix = cmd + " "

		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + filteredOptions[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option
		currentInput := line.String()
		currentOption := strings.TrimPrefix(currentInput, r.completePrefix)

		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentOption {
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
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

func (r *REPL) handleCompactCommand() error {
	// Check if there are enough messages to compact
	if len(r.messages) < 2 {
		fmt.Print("Not enough messages to compact. Need at least one exchange.\r\n")
		return nil
	}

	// Try to find the compact prompt using resolvePromptPath
	promptPath, err := r.resolvePromptPath("compact")
	if err != nil {
		return fmt.Errorf("failed to find compact prompt: %v", err)
	}

	// Load the compact prompt from file
	compactPrompt, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("failed to read compact prompt: %v", err)
	}

	// Create a serialized version of the conversation for the AI
	var conversationText strings.Builder
	conversationText.WriteString("# Conversation History\n\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)
		conversationText.WriteString(fmt.Sprintf("## %s %d:\n\n%s\n\n", role, i+1, msg.Content.(string)))
	}

	// Create a new message with the compact prompt and conversation history
	compactMessage := Message{
		Role:    "user",
		Content: string(compactPrompt) + "\n\n" + conversationText.String(),
	}

	// Save original messages for recovery if needed
	originalMessages := r.messages

	// Replace messages with just the compact message
	r.messages = []Message{compactMessage}

	fmt.Print("Compacting conversation...\r\n")

	// Create client and send message
	client, err := NewLLMClient(r.config)
	if err != nil {
		// Restore original messages on error
		r.messages = originalMessages
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Prepare messages for the API
	apiMessages := []Message{}
	if r.systemPrompt != "" {
		apiMessages = append(apiMessages, Message{Role: "system", Content: r.systemPrompt})
	}
	apiMessages = append(apiMessages, compactMessage)

	// Send the message to the AI (non-streaming mode for this operation)
	response, err := client.SendMessage(apiMessages, false)
	if err != nil {
		// Restore original messages on error
		r.messages = originalMessages
		return fmt.Errorf("failed to compact conversation: %v", err)
	}

	// Create the assistant response message
	assistantMessage := Message{Role: "assistant", Content: response}

	// Replace the conversation with just the compact message and response
	r.messages = []Message{
		Message{Role: "user", Content: "Please provide a compact response to my questions and needs."},
		assistantMessage,
	}

	fmt.Print("Conversation compacted successfully.\r\n")

	return nil
}

// handleToolCommand executes the mai-tool command with the given arguments
func (r *REPL) handleToolCommand(args []string) error {
	if len(args) < 2 {
		tools, err := GetAvailableTools(Quiet)
		if err == nil {
			fmt.Println(tools)
		}
	} else {
		res, err := ExecuteTool(args[1], args[2:]...)
		if err == nil {
			fmt.Println(res)
		}
	}

	return nil
}

// saveConversation saves the current conversation to a JSON file
func (r *REPL) saveConversation(path string) error {
	// Expand ~ to home directory if present
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Create simplified conversation data struct
	conversationData := struct {
		SystemPrompt string    `json:"system_prompt,omitempty"`
		Messages     []Message `json:"messages"`
	}{
		SystemPrompt: r.systemPrompt,
		Messages:     r.messages,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(conversationData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation data: %v", err)
	}

	// Write to file
	if err := os.WriteFile(path, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write conversation to file: %v", err)
	}

	fmt.Printf("Conversation saved to %s (%d messages)\r\n", path, len(r.messages))
	return nil
}

// loadConversation loads a conversation from a JSON file
func (r *REPL) loadConversation(path string) error {
	// Expand ~ to home directory if present
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read conversation file: %v", err)
	}

	// Try to parse as the current format first
	var conversationData struct {
		SystemPrompt string    `json:"system_prompt"`
		Messages     []Message `json:"messages"`
	}

	if err := json.Unmarshal(data, &conversationData); err != nil {
		// Try parsing legacy format that included provider and model
		var legacyData struct {
			SystemPrompt string    `json:"system_prompt"`
			Messages     []Message `json:"messages"`
			Provider     string    `json:"provider"`
			Model        string    `json:"model"`
		}

		if err := json.Unmarshal(data, &legacyData); err != nil {
			return fmt.Errorf("failed to parse conversation file: %v", err)
		}

		// Copy data from legacy format
		conversationData.SystemPrompt = legacyData.SystemPrompt
		conversationData.Messages = legacyData.Messages
	}

	// Update REPL with loaded data
	r.systemPrompt = conversationData.SystemPrompt
	r.messages = conversationData.Messages

	fmt.Printf("Conversation loaded from %s (%d messages)\r\n", path, len(r.messages))
	if conversationData.SystemPrompt != "" {
		fmt.Print("System prompt loaded\r\n")
	}
	return nil
}
