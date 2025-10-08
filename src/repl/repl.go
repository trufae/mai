package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/term"

	"github.com/trufae/mai/src/repl/art"
	"github.com/trufae/mai/src/repl/llm"
)

// Command represents a REPL command with its description and handler
type Command struct {
	Name        string
	Description string
	Handler     func(r *REPL, args []string) (string, error)
}

type REPL struct {
	configOptions    ConfigOptions
	currentClient    *llm.LLMClient
	readline         *ReadLine // Persistent readline instance for input handling
	currentInput     strings.Builder
	cursorPos        int // Current cursor position in the line
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	isStreaming      bool
	isInterrupted    bool
	oldState         *term.State
	completeState    int
	completeOptions  []string
	completePrefix   string
	completeIdx      int    // Current index in completion options
	lastTabInput     string // last input text when Tab was pressed
	messages         []llm.Message
	pendingFiles     []pendingFile      // Files and images to include in the next message
	commands         map[string]Command // Registry of available commands
	currentSession   string             // Name of the active chat session
	unsavedTopic     string             // Topic for unsaved session before saving to disk
	initialCommand   string             // Command to execute on startup
	quitAfterActions bool               // Exit after executing initial command
	// Guard to avoid recursive followup execution
	followupInProgress bool
	// Callback to stop demo animation when first token is received
	stopDemoCallback func()
	wmcpProcess      *exec.Cmd
	wmcpPort         int
}

// parseShellArgs parses a string into shell-like arguments, handling quotes
func parseShellArgs(s string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case !inQuotes && (c == '"' || c == '\''):
			inQuotes = true
			quoteChar = c
		case inQuotes && c == quoteChar:
			inQuotes = false
			quoteChar = 0
		case !inQuotes && c == ' ':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// buildLLMConfig constructs a provider config from environment defaults and current options.
// This avoids storing a persistent config in the REPL and ensures providers
// always receive up-to-date settings (provider, model, schema, headers, etc.).
func (r *REPL) buildLLMConfig() *llm.Config {
	cfg := loadConfig()
	// Apply current options into the provider config (provider, model, baseurl, toggles, schema)
	applyConfigOptionsToLLMConfig(cfg, &r.configOptions)
	// Respect REPL streaming option
	cfg.NoStream = !r.configOptions.GetBool("llm.stream")
	// Set demo mode option
	cfg.DemoMode = r.configOptions.GetBool("repl.demo")
	return cfg
}

type pendingFile struct {
	filePath string
	content  string
	isImage  bool
	imageB64 string // Base64 encoded image data
}

type StreamingClient interface {
	StreamChat(ctx context.Context, messages []llm.Message) (<-chan string, <-chan error)
}

// AskYesNo prompts the user with a yes/no question, defaulting to 'y' or 'n'.
// Returns true for yes, false for no.
func AskYesNo(question string, defaultVal rune) bool {
	// Normalize default and validate
	dv := unicode.ToLower(defaultVal)
	if dv != 'y' && dv != 'n' {
		panic("default value must be 'y' or 'n'")
	}

	var defaultText string
	if dv == 'y' {
		defaultText = "[Y/n]"
	} else {
		defaultText = "[y/N]"
	}

	fmt.Printf("%s %s ", question, defaultText)

	fd := int(os.Stdin.Fd())
	// If stdin is not a terminal, fall back to the default choice instead of panicking
	if !term.IsTerminal(fd) {
		fmt.Println()
		return dv == 'n'
	}

	// Put terminal in raw mode; if this fails, fall back to default
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: unable to set terminal raw mode: %v\n", err)
		fmt.Println()
		return dv == 'y'
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	// Read one byte
	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return dv == 'y'
	}

	c := buf[0]
	if c == '\r' || c == '\n' { // Enter pressed -> use default
		return dv == 'y'
	}

	c = byte(unicode.ToLower(rune(c)))
	return c == 'y'
}

func NewREPL(configOptions ConfigOptions, initialCommand string, quitAfterActions bool) (*REPL, error) {
	ctx, cancel := context.WithCancel(context.Background())

	var readLine *ReadLine
	if !quitAfterActions {
		// Create a persistent readline instance only if we need interactive input
		var err error
		readLine, err = NewReadLine()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to initialize readline: %v", err)
		}
	}

	// Initialize the REPL
	repl := &REPL{
		readline:         readLine,
		cursorPos:        0, // Initialize cursor position to 0
		ctx:              ctx,
		cancel:           cancel,
		completeState:    0,
		completeOptions:  []string{},
		completeIdx:      0,                        // Initialize completion index
		pendingFiles:     []pendingFile{},          // Initialize empty pending files slice
		commands:         make(map[string]Command), // Initialize command registry
		configOptions:    configOptions,
		initialCommand:   initialCommand,
		quitAfterActions: quitAfterActions,
	}

	// Create chat directory and history file
	if err := repl.setupHistory(); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up history: %v\n", err)
	}
	// Load persistent REPL history into readline
	repl.loadReplHistory()

	// Set prompts in the readline instance
	if repl.readline != nil {
		if prompt := repl.configOptions.Get("repl.prompt"); prompt != "" {
			repl.readline.SetPrompt(prompt)
		}

		if readlinePrompt := repl.configOptions.Get("repl.prompt2"); readlinePrompt != "" {
			repl.readline.SetReadlinePrompt(readlinePrompt)
		}
	}

	// Initialize baseurl/useragent options from environment defaults if not set
	envCfg := loadConfig()
	if repl.configOptions.Get("ai.model") == "" {
		// Check MAI_MODEL first, then use provider's DefaultModel
		if model := os.Getenv("MAI_MODEL"); model != "" {
			repl.configOptions.Set("ai.model", model)
		} else if dm := repl.resolveDefaultModelForProvider(repl.configOptions.Get("ai.provider")); dm != "" {
			repl.configOptions.Set("ai.model", dm)
		}
	}
	if repl.configOptions.Get("ai.baseurl") == "" && envCfg.BaseURL != "" {
		repl.configOptions.Set("ai.baseurl", envCfg.BaseURL)
	}
	if repl.configOptions.Get("http.useragent") == "" && envCfg.UserAgent != "" {
		repl.configOptions.Set("http.useragent", envCfg.UserAgent)
	}

	// Set the stop demo callback to transition out of the "thinking" action
	// when the first token is received. Previously this stopped the demo loop
	// entirely which caused subsequent streaming tokens to be buffered but
	// not rendered until the next prompt restarted the loop. Instead, clear
	// the action label so the demo loop continues running and will display
	// tokens as they arrive.
	repl.stopDemoCallback = func() {
		if repl.configOptions.GetBool("repl.demo") {
			// Stop the demo entirely. We only call this callback when the
			// first token from the model is not a <think> tag, so the
			// greyscaled scroller should be stopped.
			art.StopLoop()
		}
	}

	// Do not cache system prompt here; it will be read dynamically from configOptions (or file)

	// Validate schema if provided (no storage here; providers read from options via buildLLMConfig)
	if schemaFile := repl.configOptions.Get("llm.schemafile"); schemaFile != "" {
		if content, err := os.ReadFile(schemaFile); err == nil {
			var tmp map[string]interface{}
			if err := json.Unmarshal(content, &tmp); err != nil {
				fmt.Fprintf(os.Stderr, "Invalid JSON in schemafile %s: %v\n", schemaFile, err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Failed to read schemafile %s: %v\n", schemaFile, err)
		}
	} else if inline := repl.configOptions.Get("llm.schema"); inline != "" {
		var tmp map[string]interface{}
		if err := json.Unmarshal([]byte(inline), &tmp); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid JSON for schema: %v\n", err)
		}
	}

	// Keep schema in sync when options change at runtime
	// Schema listeners no longer need to update any local state
	repl.configOptions.RegisterOptionListener("llm.schemafile", func(value string) {})
	repl.configOptions.RegisterOptionListener("llm.schema", func(value string) {})

	// baseurl is fully handled in options; providers will receive it via buildLLMConfig

	// Register listeners for prompt option changes
	repl.configOptions.RegisterOptionListener("repl.prompt", func(value string) {
		if repl.readline != nil {
			repl.readline.SetPrompt(value)
		}
	})

	repl.configOptions.RegisterOptionListener("repl.prompt2", func(value string) {
		if repl.readline != nil {
			repl.readline.SetReadlinePrompt(value)
		}
	})

	// When repl.debug is toggled, display a colorful debug banner so it's
	// obvious to the user that REPL internal debug logging is enabled.
	repl.configOptions.RegisterOptionListener("repl.debug", func(value string) {
		v := strings.ToLower(strings.TrimSpace(value))
		if v == "true" || v == "1" || v == "yes" {
			art.DebugBanner("REPL Debug", "REPL internal debug logging enabled")
		} else {
			art.DebugBanner("REPL Debug", "REPL internal debug logging disabled")
		}
	})

	// Initialize command registry
	repl.initCommands()

	// Auto-detect and set promptdir, templatedir, and wwwroot
	repl.autoDetectPromptDir()
	repl.autoDetectTemplateDir()
	repl.autoDetectWwwRoot()

	repl.loadAgentsFile()

	// Spawn mai-wmcp if mcp.config or mcp.args is set
	var wmcpArgs []string
	if v := repl.configOptions.Get("mcp.config"); v != "" {
		wmcpArgs = []string{"-c", v}
	} else if v := repl.configOptions.Get("mcp.args"); v != "" {
		wmcpArgs = parseShellArgs(v)
	}

	if len(wmcpArgs) > 0 {
		listener, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding random port for wmcp: %v\n", err)
		} else {
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			repl.wmcpPort = port
			os.Setenv("MAI_WMCP_BASEURL", fmt.Sprintf("localhost:%d", port))
			// Append the base URL argument
			wmcpArgs = append(wmcpArgs, "-b", fmt.Sprintf("localhost:%d", port))
			cmd := exec.Command("mai-wmcp", wmcpArgs...)
			err = cmd.Start()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error starting wmcp: %v\n", err)
			} else {
				repl.wmcpProcess = cmd
			}
		}
	}

	return repl, nil
}

func (r *REPL) Run() error {
	defer r.cleanup()

	// Handle interrupt signals
	r.setupSignalHandler()

	// Save command-line set options before loading rc file
	cmdLineModel := r.configOptions.Get("ai.model")
	cmdLineProvider := r.configOptions.Get("ai.provider")

	// Load and process 'rc' file from project or home .mai directory unless skipped by option
	if !r.configOptions.GetBool("repl.skiprc") {
		if err := r.loadRCFile(); err != nil {
			fmt.Printf("Error loading rc file: %v\r\n", err)
		}
	}

	// Restore command-line set options (they have priority over rc file)
	if cmdLineModel != "" {
		r.configOptions.Set("ai.model", cmdLineModel)
	}
	if cmdLineProvider != "" {
		r.configOptions.Set("ai.provider", cmdLineProvider)
	}

	// Set MAI_PROVIDER/MAI_MODEL if not set by rc file or command line
	if r.configOptions.Get("ai.provider") == "" {
		if provider := os.Getenv("MAI_PROVIDER"); provider != "" {
			r.configOptions.Set("ai.provider", provider)
		}
	}
	if r.configOptions.Get("ai.model") == "" {
		if model := os.Getenv("MAI_MODEL"); model != "" {
			r.configOptions.Set("ai.model", model)
		}
	}

	// Execute initial command if provided
	if r.initialCommand != "" {
		if err := r.handleCommand(r.initialCommand, "", ""); err != nil {
			fmt.Fprintf(os.Stderr, "Error executing initial command: %v\r\n", err)
			if r.quitAfterActions {
				return nil // Exit if quit after actions is enabled
			}
			// Continue to REPL even if initial command fails
		} else if r.quitAfterActions {
			return nil // Exit after successful command execution
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

func (r *REPL) showCommands() string {
	var output strings.Builder
	output.WriteString("Commands:\r\n")

	// Sort commands for consistent display
	var cmdNames []string
	for name := range r.commands {
		cmdNames = append(cmdNames, name)
	}
	sort.Strings(cmdNames)

	// Display all registered commands with descriptions
	for _, name := range cmdNames {
		cmd := r.commands[name]
		output.WriteString(fmt.Sprintf("  %-15s - %s\r\n", name, cmd.Description))
	}

	// Display special commands that aren't in the registry
	output.WriteString("  @<path>         - File path with tab completion (anywhere in input)\r\n")
	output.WriteString("  #               - List available prompt files (.md)\r\n")
	output.WriteString("  #<n> <text>     - Use content from prompt file with text\r\n")
	output.WriteString("  %               - List available template files\r\n")
	output.WriteString("  %<n> <text>     - Use template with interactive prompts and optional text\r\n")
	output.WriteString("  $<text>         - Prompt the model with shell backticks, redirections and prompts\r\n")
	output.WriteString("  !<command>      - Execute shell command\r\n")
	output.WriteString("  _               - Print the last assistant reply\r\n")

	output.WriteString("Shortcuts:\r\n")
	// Display keyboard shortcuts
	output.WriteString("  Ctrl+C          - Cancel current request\r\n")
	output.WriteString("  Ctrl+D          - Exit REPL (when line is empty)\r\n")
	output.WriteString("  Ctrl+W          - Delete last word\r\n")
	output.WriteString("  Up/Down arrows  - Navigate history\r\n")
	output.WriteString("  Tab             - Command/path completion\r\n")
	output.WriteString("\r\n")
	return output.String()
}

func (r *REPL) cleanup() {
	if r.readline != nil {
		r.readline.Restore()
	} else if r.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), r.oldState)
	}
	r.cancel()
	if err := r.saveHistory(); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving history: %v\n", err)
	}
	// Auto-save the chat session if history is enabled and messages exist,
	// updating the current session or creating a new one if none selected
	if r.configOptions.GetBool("repl.history") && len(r.messages) > 0 {
		mode := r.configOptions.Get("chat.save")
		if mode != "never" {
			var name string
			if r.currentSession != "" {
				name = r.currentSession
			} else {
				// name = time.Now().Format("20060102150405")
				name = time.Now().Format("05041502012006")
			}
			if mode == "prompt" {
				if !AskYesNo("Save session?", 'y') {
					return
				}
			}
			if err := r.saveSession(name); err != nil {
				fmt.Fprintf(os.Stderr, "Error auto-saving session: %v\n", err)
			}
			r.currentSession = name
		}
	}
	// Kill wmcp process if running
	if r.wmcpProcess != nil {
		r.wmcpProcess.Process.Kill()
		r.wmcpProcess.Wait()
	}
}

// interruptResponse interrupts the current LLM response if one is being generated
func (r *REPL) interruptResponse() {
	r.mu.Lock()
	if r.readline != nil {
		r.readline.Interrupted()
	}
	isStreaming := r.isStreaming
	r.isInterrupted = true
	r.mu.Unlock()

	if isStreaming {
		// Cancel the current context
		r.cancel()

		// Create new context for next request
		r.ctx, r.cancel = context.WithCancel(context.Background())

		// Also interrupt the LLM client if it's active
		client, err := llm.NewLLMClient(r.buildLLMConfig())
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

	skipMessage := strings.HasPrefix(input, " ")

	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	// Handle verbatim inputs
	var isVerbatim bool
	if len(input) >= 2 {
		if input[0] == '\'' && input[len(input)-1] == '\'' {
			input = input[1 : len(input)-1]
			isVerbatim = true
		}
	}

	// Handle redirection if not verbatim and not shell ($) mode.
	// Shell mode ('$') has its own redirection parsing in handleShellInput,
	// so avoid stripping redirections here which would prevent shell-mode
	// handlers from seeing them.
	var redirectType, redirectTarget string
	if !isVerbatim && !strings.HasPrefix(input, "$") {
		if idx := strings.LastIndex(input, " > "); idx != -1 {
			redirectType = "file"
			redirectTarget = strings.TrimSpace(input[idx+3:])
			input = strings.TrimSpace(input[:idx])
		} else if idx := strings.LastIndex(input, " | "); idx != -1 {
			redirectType = "pipe"
			redirectTarget = strings.TrimSpace(input[idx+3:])
			input = strings.TrimSpace(input[:idx])
		}
	}

	// Handle commands (slash- and dot-prefixed, plus '_' for last reply)
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, ".") || input == "_" {
		// Add to history
		r.addToHistory(input)
		err = r.handleCommand(input, redirectType, redirectTarget)
	} else if strings.HasPrefix(input, "#") {
		// Add to history (also added in handlePromptCommand, but keep here for consistency)
		r.addToHistory(input)
		err = r.handlePromptCommand(input)
	} else if strings.HasPrefix(input, "%") {
		// Add to history
		r.addToHistory(input)
		err = r.handleTemplateCommand(input)
	} else if strings.HasPrefix(input, "?") {
		// Add to history (also added in handlePromptCommand, but keep here for consistency)
		r.addToHistory(input)
		err = r.handleCommand("/help", "", "")
	} else if strings.HasPrefix(input, "!") {
		// Add to history
		r.addToHistory(input)
		err = r.executeShellCommand(input[1:])
	} else if strings.HasPrefix(input, "$") {
		// Add to history
		r.addToHistory(input)
		err = r.handleShellInput(input[1:])
	} else {
		// Add to history
		r.addToHistory(input)
		err = r.sendToAI(input, redirectType, redirectTarget, true, false)
	}

	if skipMessage {
		r.handleCommand("/chat undo", "", "")
		r.handleCommand("/chat undo", "", "")
	}
	return err
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
	// Capture original input and ensure lastTabInput is updated after completion
	origInput := line.String()
	defer func() {
		r.lastTabInput = line.String()
	}()
	input := origInput

	// Fresh vs cycling: reset if first tab or input changed since last tab press
	if r.completeState == 0 || origInput != r.lastTabInput {
		r.completeState = 0
		r.completeIdx = 0
		r.completeOptions = nil
		r.completePrefix = ""
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
	fileParts := strings.SplitN(input, " ", 2)
	if len(fileParts) == 2 && (fileParts[0] == "/image" || fileParts[0] == "/file" || fileParts[0] == "/template" || fileParts[0] == ".") {
		r.handleFilePathCompletion(line, fileParts[0], fileParts[1])
		return
	}

	// Check for /set dir.promptfile and dir.prompt value completion
	setParts := strings.SplitN(input, " ", 3)
	if len(setParts) >= 2 && setParts[0] == "/set" {
		if len(setParts) == 3 {
			switch setParts[1] {
			case "dir.promptfile":
				// Complete file paths for dir.promptfile
				r.handleFilePathCompletion(line, "/set dir.promptfile", setParts[2])
				return
			case "dir.prompt":
				// Complete directory paths for dir.prompt
				r.handleDirectoryCompletion(line, "/set dir.prompt", setParts[2])
				return
			}
		}
		// Fallthrough for option completion
	}

	// Check for /set, /get, and /unset option completion
	configParts := strings.SplitN(input, " ", 2)
	if len(configParts) == 2 && (configParts[0] == "/set" || configParts[0] == "/get" || configParts[0] == "/unset") {
		r.handleOptionCompletion(line, configParts[0], configParts[1])
		return
	}

	// Handle tab completion for /chat subcommands
	chatParts := strings.SplitN(input, " ", 3)
	if strings.HasPrefix(input, "/chat ") && len(chatParts) >= 2 {
		if len(chatParts) == 2 {
			// Complete /chat subcommands
			subcmd := chatParts[1]
			r.handleChatSubcommandCompletion(line, subcmd)
			return
		} else if len(chatParts) == 3 && (chatParts[1] == "save" || chatParts[1] == "load") {
			// Complete file paths for save/load
			r.handleFilePathCompletion(line, "/chat "+chatParts[1], chatParts[2])
			return
		}
	}

	// Handle tab completion for /session subcommands
	sessionParts := strings.SplitN(input, " ", 3)
	if strings.HasPrefix(input, "/session ") && len(sessionParts) >= 2 {
		if len(sessionParts) == 2 {
			// Complete /session subcommands
			subcmd := sessionParts[1]
			r.handleSessionSubcommandCompletion(line, subcmd)
			return
		} else if len(sessionParts) == 3 && (sessionParts[1] == "use" || sessionParts[1] == "del" || sessionParts[1] == "show") {
			// Complete session names for use/del
			r.handleSessionNameCompletion(line, "/session "+sessionParts[1], sessionParts[2])
			return
		}
	}

	// Only handle tab completion at the beginning of the line for commands
	if !(strings.HasPrefix(input, "/") || strings.HasPrefix(input, "#") || strings.HasPrefix(input, "%")) {
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
			promptDir := r.configOptions.Get("dir.prompt")
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

	// Template command completion for commands like "%<tab>"
	if strings.HasPrefix(input, "%") {
		needFreshOptions := false
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		if needFreshOptions {
			// Determine template directory
			templDir := r.configOptions.Get("dir.templates")
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
					allTemps = append(allTemps, "%"+base)
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
func (r *REPL) handleChatCommand(args []string) (string, error) {
	// Show help if no arguments provided
	if len(args) < 2 {
		var output strings.Builder
		output.WriteString("Chat conversation management commands:\r\n")
		output.WriteString("  /chat save [name] - Save conversation to a session file\r\n")
		output.WriteString("  /chat load <name> - Load conversation from a session file\r\n")
		output.WriteString("  /chat sessions    - List all saved sessions\r\n")
		output.WriteString("  /chat clear       - Clear conversation messages\r\n")
		output.WriteString("  /chat list        - Display conversation messages (truncated)\r\n")
		output.WriteString("  /chat log         - Display full conversation with preserved formatting\r\n")
		output.WriteString("  /chat undo [N]    - Remove last or Nth message\r\n")
		output.WriteString("  /chat compact     - Compact conversation into a single message\r\n")
		return output.String(), nil
	}

	// Handle subcommands
	action := args[1]
	switch action {
	case "save":
		var sessionName string
		if len(args) > 2 {
			sessionName = args[2]
		} else {
			sessionName = time.Now().Format("20060102150405")
		}
		return "", r.saveSession(sessionName)
	case "load":
		if len(args) < 3 {
			return "Usage: /chat load <name>\r\n", nil
		}
		return "", r.loadSession(args[2])
	case "sessions":
		output, err := r.listSessions()
		if err != nil {
			return "", err
		}
		return output, nil
	case "clear":
		r.messages = []llm.Message{}
		return "Conversation messages cleared\r\n", nil
	case "list":
		output := r.displayConversationLog()
		return output, nil
	case "log":
		output := r.displayFullConversationLog()
		return output, nil
	case "undo":
		if len(args) > 2 {
			// Parse the index argument
			r.undoMessageByIndex(args[2])
		} else {
			// Default behavior - remove the last message
			r.undoLastMessage()
		}
		return "", nil
	case "compact":
		return "", r.handleCompactCommand()
	case "memory":
		// Generate or manage consolidated memory file
		if len(args) < 3 || args[2] == "generate" {
			return "", r.generateMemory()
		}
		if args[2] == "show" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Sprintf("Cannot get home directory: %v\r\n", err), nil
			}
			memFile := filepath.Join(homeDir, ".mai", "memory.txt")
			b, err := os.ReadFile(memFile)
			if err != nil {
				return fmt.Sprintf("Cannot read memory file: %v\r\n", err), nil
			}
			return fmt.Sprintf("%s\r\n", string(b)), nil
		}
		if args[2] == "clear" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Sprintf("Cannot get home directory: %v\r\n", err), nil
			}
			memFile := filepath.Join(homeDir, ".mai", "memory.txt")
			_ = os.Remove(memFile)
			return "Memory file removed\r\n", nil
		}
		return "Usage: /chat memory [generate|show|clear]\r\n", nil
	default:
		return fmt.Sprintf("Unknown action: %s\r\nAvailable actions: save, load, sessions, clear, list, log, undo, compact\r\n", action), nil
	}
}

// generateMemory walks over all saved chat sessions, summarizes them using the memory prompt, and writes the consolidated memory file to ~/.mai/memory.txt
func (r *REPL) generateMemory() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home directory: %v", err)
	}
	chatDir := filepath.Join(homeDir, ".mai", "chat")
	files, err := os.ReadDir(chatDir)
	if err != nil {
		return fmt.Errorf("cannot read chat directory: %v", err)
	}

	var combined strings.Builder
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(chatDir, file.Name()))
		if err != nil {
			continue
		}
		var sess sessionData
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		sessionName := strings.TrimSuffix(file.Name(), ".json")
		combined.WriteString("Session: " + sessionName + "\n")
		for _, m := range sess.Messages {
			role := m.Role
			content := fmt.Sprintf("%v", m.Content)
			combined.WriteString(fmt.Sprintf("%s: %s\n", role, content))
		}
		combined.WriteString("\n---\n\n")
	}

	if combined.Len() == 0 {
		return fmt.Errorf("no conversation data found in %s", chatDir)
	}

	// Load memory prompt template
	promptPath, err := r.resolvePromptPath("memory.md")
	promptContent := ""
	if err == nil {
		if b, err := os.ReadFile(promptPath); err == nil {
			promptContent = string(b)
		}
	}

	client, err := llm.NewLLMClient(r.buildLLMConfig())
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	messages := []llm.Message{}
	if promptContent != "" {
		messages = append(messages, llm.Message{Role: "system", Content: promptContent})
	}
	messages = append(messages, llm.Message{Role: "user", Content: combined.String()})

	response, err := client.SendMessage(messages, false, nil)
	if err != nil {
		return fmt.Errorf("failed to generate memory: %v", err)
	}

	memFile := filepath.Join(homeDir, ".mai", "memory.txt")
	if err := os.WriteFile(memFile, []byte(response), 0644); err != nil {
		return fmt.Errorf("cannot write memory file: %v", err)
	}

	fmt.Printf("Memory written to %s\r\n", memFile)
	return nil
}

// getVDBContext executes mai-vdb with the configured directory and current message
// Returns the context output to be used as [CONTEXT] for the LLM
func (r *REPL) getVDBContext(message string) (string, error) {
	vdbDir := r.configOptions.Get("vdb.datadir")
	if vdbDir == "" {
		return "", fmt.Errorf("vdb.datadir not configured")
	}

	// Execute mai-vdb command
	vdbLimitNum, err := r.configOptions.GetNumber("vdb.limit")
	if err != nil {
		vdbLimitNum = 5 // fallback to default
	}
	vdbLimit := fmt.Sprintf("%.0f", vdbLimitNum)
	cmd := exec.Command("mai-vdb", "-s", vdbDir, "-n", vdbLimit, message)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to execute mai-vdb: %v", err)
	}

	return string(output), nil
}

// handleScriptCommand executes a script file containing REPL commands
func (r *REPL) handleScriptCommand(scriptPath string) error {
	// Expand ~ to home directory
	if strings.HasPrefix(scriptPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		scriptPath = filepath.Join(homeDir, scriptPath[1:])
	}

	// Read the script file
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script file: %v", err)
	}

	// Split into lines and execute each command
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		fmt.Printf("> %s\n", line)
		err := r.handleCommand(line, "", "")
		if err != nil {
			return fmt.Errorf("error executing command '%s': %v", line, err)
		}
	}

	return nil
}

func (r *REPL) handleCommand(input string, redirectType, redirectTarget string) error {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	command := parts[0]

	// Check if the command exists in the registry
	if cmd, exists := r.commands[command]; exists {
		// Execute the command handler
		output, err := cmd.Handler(r, parts)
		if err != nil {
			return err
		}
		if output != "" {
			if redirectType == "file" {
				err = os.WriteFile(redirectTarget, []byte(output), 0644)
				if err != nil {
					return fmt.Errorf("failed to write to file %s: %v", redirectTarget, err)
				}
				fmt.Printf("Output written to %s\r\n", redirectTarget)
			} else if redirectType == "pipe" {
				cmd := exec.Command("/bin/sh", "-c", redirectTarget)
				cmd.Stdin = strings.NewReader(output)
				pipeOutput, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("failed to execute command %s: %v", redirectTarget, err)
				}
				fmt.Print(string(pipeOutput))
			} else {
				fmt.Print(output)
			}
		}
		return nil
	} else {
		fmt.Printf("Unknown command: %s\n\r", command)
	}

	return nil
}

func (r *REPL) addImage(imagePath string) (string, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(imagePath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %v", err)
		}
		imagePath = filepath.Join(homeDir, imagePath[1:])
	}

	// Read image file
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read image: %v", err)
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
	message := fmt.Sprintf("Image added: %s (%d bytes). Send a message to analyze it.\r\n",
		filepath.Base(imagePath), len(imageData))
	return message, nil
}

func (r *REPL) addFile(filePath string) (string, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(filePath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %v", err)
		}
		filePath = filepath.Join(homeDir, filePath[1:])
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	// Add to pending files
	r.pendingFiles = append(r.pendingFiles, pendingFile{
		filePath: filePath,
		content:  string(content),
		isImage:  false,
	})

	r.addToHistory(fmt.Sprintf("/file %s", filePath))
	message := fmt.Sprintf("File added: %s (%d bytes). Send a message to analyze it.\r\n",
		filepath.Base(filePath), len(content))
	return message, nil
}

func (r *REPL) substituteInput(input string) (string, error) {
	processedInput, err := ExecuteCommandSubstitution(input)
	if err != nil {
		return "", fmt.Errorf("command substitution failed: %v", err)
	}
	input = processedInput

	// Process backtick substitutions
	processedInput, err = ExecuteBacktickSubstitution(input, r)
	if err != nil {
		return "", fmt.Errorf("backtick substitution failed: %v", err)
	}
	input = processedInput

	// Process environment variable substitutions
	processedInput, err = ExecuteEnvVarSubstitution(input)
	if err != nil {
		return "", fmt.Errorf("environment variable substitution failed: %v", err)
	}
	input = processedInput

	// Process @mentions in the input
	enhancedInput := r.processAtMentions(input)

	/*
		// Process pending files and incorporate them into the input
		var images []string // For storing base64 encoded images for Ollama
		if len(r.pendingFiles) > 0 {
			// Add file contents to the input
			enhancedInput += "\n\n"

			for _, file := range r.pendingFiles {
				if strings.Contains(file.filePath, "://") {
					enhancedInput += fmt.Sprintf("URL Link: `%s`\n", file.filePath)
				} else if file.isImage {
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
	*/
	input = enhancedInput

	return input, nil
}

// buildUserDetails creates a string with user context information
func (r *REPL) buildUserDetails() string {
	if !r.configOptions.GetBool("user.details") {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	if username == "" {
		username = "unknown"
	}

	osName := runtime.GOOS

	lang := r.configOptions.Get("user.lang")
	if lang == "" {
		lang = os.Getenv("LANG")
	}
	if lang == "" {
		lang = "unknown"
	}

	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04:05 MST")

	return fmt.Sprintf("Current Working Directory: %s\nUsername: %s\nOperating System: %s\nLanguage: %s\nCurrent Time/Date/Timezone: %s",
		cwd, username, osName, lang, timeStr)
}

func (r *REPL) sendToAI(input string, redirectType string, redirectTarget string, processSubstitutions bool, forceDisableStreaming bool) error {
	r.mu.Lock()
	r.isStreaming = redirectType == ""
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.isStreaming = false
		r.currentClient = nil
		r.mu.Unlock()
	}()

	if processSubstitutions {
		processedInput, err := r.substituteInput(input)
		if err != nil {
			return err
		}
		input = processedInput
	}

	// Create client
	client, err := llm.NewLLMClient(r.buildLLMConfig())
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	r.mu.Lock()
	r.currentClient = client
	r.mu.Unlock()

	// Add system prompt if present
	messages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}

	// Add user details if enabled
	if userDetails := r.buildUserDetails(); userDetails != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "USER CONTEXT:\n" + userDetails})
	}

	// If memory option is enabled, load consolidated memory and include as system context
	if r.configOptions.GetBool("chat.memory") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			memFile := filepath.Join(homeDir, ".mai", "memory.txt")
			if b, err := os.ReadFile(memFile); err == nil && len(b) > 0 {
				messages = append(messages, llm.Message{Role: "system", Content: "MEMORY:\n" + string(b)})
			}
		}
	}

	var vdbContext string
	var vdbErr error
	// If vdb option is enabled, get context from vector database and include as system context
	if r.configOptions.GetBool("vdb.use") {
		vdbContext, vdbErr = r.getVDBContext(input)
		if vdbErr == nil && vdbContext != "" {
			messages = append(messages, llm.Message{Role: "user", Content: "[CONTEXT]\n" + vdbContext + "\n[/CONTEXT]"})
		} else if vdbErr != nil {
			fmt.Fprintf(os.Stderr, "VDB context error: %v\n", vdbErr)
		}
	}

	// Handle conversation history based on logging and reply settings
	if r.configOptions.GetBool("chat.log") {
		// When logging is enabled, use normal message history behavior
		if r.configOptions.GetBool("chat.replies") {
			// Include all messages
			messages = append(messages, r.messages...)
		} else {
			// Include only user messages
			for _, msg := range r.messages {
				if msg.Role == "user" {
					messages = append(messages, msg)
				} else {
					msg2 := msg
					msg2.Content = ""
					// include empty response from the llm
					messages = append(messages, msg2)
				}
			}
		}
	} else {
		// When logging is disabled, we don't append any previous messages
	}

	if r.configOptions.GetBool("mcp.use") {
		StartTimer()
		tool, err := r.ReactLoop(messages, input)
		if err != nil {
			return fmt.Errorf("tool execution failed: %v", err)
		}
		input = tool
		fmt.Println("(tools) loop finished.")
		StopTimer()
	}

	// Before adding the user message, optionally auto-compact the conversation
	// when the `autocompact` option is enabled and the chat history is large.
	// If `autocompact` is non-zero and there are more than 5 messages, run
	// the compact operation which will replace the conversation with a
	// compact summary produced by the AI.
	if ac, err := r.configOptions.GetNumber("chat.autocompact"); err == nil {
		if ac != 0 && len(r.messages) > 5 {
			if err := r.handleCompactCommand(); err != nil {
				// Log compact errors but continue sending the message
				fmt.Fprintf(os.Stderr, "Auto-compact failed: %v\n", err)
			}
		}
	}

	// Add user message with enhanced input
	// Store the original input (with commands) for display in message history,
	// but use the processed input (with command output) for sending to the AI
	userMessage := llm.Message{Role: "user", Content: input}

	// Handle conversation history based on logging settings
	if r.configOptions.GetBool("chat.log") {
		// Save the user message to conversation history when logging is enabled.
		// NOTE: VDB context (when enabled) is included in the API call below
		// but must NOT be stored in the persistent chat log. Do not append
		// vdbContext to r.messages so it remains out of the saved conversation.
		r.messages = append(r.messages, userMessage)
	} else {
		// When logging is disabled, keep just the current user message in memory.
		// VDB context is still sent to the LLM but not stored in r.messages.
		r.messages = []llm.Message{userMessage}
	}

	// Set default topic from first user message for unsaved sessions
	if r.currentSession == "" && r.unsavedTopic == "" {
		// Use the first few words of the message as the session topic
		words := strings.Fields(userMessage.Content.(string))
		snippetWords := words
		if len(words) > 5 {
			snippetWords = words[:5]
		}
		r.unsavedTopic = strings.Join(snippetWords, " ")
	}

	// If reasoning is disabled, append /no_think to the last message sent to the LLM
	if !r.configOptions.GetBool("llm.think") && r.configOptions.GetBool("llm.rawmode") {
		// Create a copy of the messages for the API call with /no_think appended
		messagesCopy := make([]llm.Message, len(messages))
		copy(messagesCopy, messages)

		disable_reasoning := "\n# Reasoning\nDo /nothink /no_think\nUse Reasoning: low\n\n"
		// Append the user message with /no_think to the copy
		messagesCopy = append(messagesCopy, llm.Message{Role: "user", Content: input + disable_reasoning})
		messages = messagesCopy
	} else {
		// Add the original user message
		messages = append(messages, userMessage)
	}

	// Do not start the demo animation here. The animation will be started
	// only when a streaming provider emits a <think> tag. This avoids
	// creating the scroller for responses that do not contain internal
	// reasoning blocks.

	// Send message with streaming based on REPL settings, but disable if redirected
	streamEnabled := r.configOptions.GetBool("llm.stream") && redirectType == "" && !forceDisableStreaming

	// Reset the markdown processor state before starting a new streaming session
	if streamEnabled && r.configOptions.GetBool("scr.markdown") {
		llm.ResetStreamRenderer()
	}

	var images []string // base64 encoded images

	for _, file := range r.pendingFiles {
		if file.isImage {
			images = append(images, file.imageB64)
		}
	}

	// If demo mode is active, let the LLM client notify the demo stop callback
	// as soon as the first streaming token arrives. We set the callback on the
	// client so it will be embedded into the request context used by providers.
	if r.configOptions.GetBool("repl.demo") && client != nil {
		client.SetResponseStopCallback(r.stopDemoCallback)
		defer client.SetResponseStopCallback(nil)

		// Also set demo callbacks so streaming parsers can emit tokens and phase
		// updates. The llm package exposes SetDemoPhaseCallback and
		// SetDemoTokenCallback which we can use here.
		llm.SetDemoPhaseCallback(func(phase string) {
			// Update the demo action label and ensure the scroller is running.
			if phase == "" {
				art.StopLoop()
			} else {
				art.StartLoop(phase)
			}
		})
		defer llm.SetDemoPhaseCallback(nil)

		llm.SetDemoTokenCallback(func(phase string, token string) {
			if !r.configOptions.GetBool("repl.demo") {
				return
			}
			// Feed text into the demo scroller; filtering/newline removal is
			// handled by llm.EmitDemoTokens
			art.FeedText(token)
		})
		defer llm.SetDemoTokenCallback(nil)
	}

	// Start the demo animation immediately; it will remain visible until
	// the first token arrives. If the first token is not a <think> tag the
	// streaming parser will invoke the stop callback to stop the animation.
	if r.configOptions.GetBool("repl.demo") && client != nil {
		art.StartLoop("Thinking...")
	}

	response, err := client.SendMessage(messages, streamEnabled, images)

	// Stop the animation after SendMessage returns (for non-streaming)
	// For streaming, the animation will be stopped when the first token arrives.
	if r.configOptions.GetBool("repl.demo") && !streamEnabled {
		art.StopLoop()
	}

	// Handle the assistant's response based on logging settings
	if err == nil && response != "" {
		// Handle redirection
		if redirectType == "file" {
			// Write response to file
			err = os.WriteFile(redirectTarget, []byte(response), 0644)
			if err != nil {
				return fmt.Errorf("failed to write to file %s: %v", redirectTarget, err)
			}
			fmt.Printf("Response written to %s\r\n", redirectTarget)
		} else if redirectType == "pipe" {
			// Pipe response to command. Attach command stdout/stderr to the
			// current terminal so interactive tools (like `less`) can operate
			// normally. Write the AI response to the command's stdin.
			cmd := exec.Command("/bin/sh", "-c", redirectTarget)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			stdinPipe, err := cmd.StdinPipe()
			if err != nil {
				return fmt.Errorf("failed to create stdin pipe: %v", err)
			}

			// Start the command
			err = cmd.Start()
			if err != nil {
				return fmt.Errorf("failed to start command %s: %v", redirectTarget, err)
			}

			// Write the response into the command's stdin and close it
			_, err = io.WriteString(stdinPipe, response)
			_ = stdinPipe.Close()
			if err != nil {
				// If writing fails, still wait for process and return error
				_ = cmd.Wait()
				return fmt.Errorf("failed to write to command %s stdin: %v", redirectTarget, err)
			}

			// Wait for the command to finish
			err = cmd.Wait()
			if err != nil {
				return fmt.Errorf("command %s failed: %v", redirectTarget, err)
			}
		} else {
			// Normal output
			if !streamEnabled {
				// Optionally strip <think> regions from printed output in demo mode
				out := response
				if r.configOptions.GetBool("repl.demo") {
					out = llm.FilterOutThinkForOutput(out)
					out = strings.TrimLeft(out, " \t\r\n")
				}
				if r.configOptions.GetBool("scr.markdown") {
					// Use markdown formatting
					fmt.Print(llm.RenderMarkdown(out))
				} else {
					// Use standard formatting
					fmt.Println(strings.ReplaceAll(out, "\n", "\r\n"))
				}
			}
		}

		// Create assistant message
		assistantMessage := llm.Message{Role: "assistant", Content: response}

		if r.configOptions.GetBool("chat.log") {
			// Save to conversation history when logging is enabled
			r.messages = append(r.messages, assistantMessage)
		} else {
			// When logging is disabled, keep just the current exchange
			r.messages = []llm.Message{userMessage, assistantMessage}
		}

		// Handle TTS if enabled
		if r.configOptions.GetBool("chat.tts") {
			voice := r.configOptions.Get("chat.ttsvoice")
			if voice == "" {
				voice = "Mónica"
			}
			Speak(response, voice)
		}

		// If followup is enabled, run the #followup prompt once asynchronously
		if r.configOptions.GetBool("chat.followup") {
			r.mu.Lock()
			if !r.followupInProgress {
				r.followupInProgress = true
				r.mu.Unlock()
				go func() {
					defer func() {
						r.mu.Lock()
						r.followupInProgress = false
						r.mu.Unlock()
					}()
					// Call the prompt handler for #followup; ignore errors but print them
					if err := r.handlePromptCommand("#followup"); err != nil {
						fmt.Printf("Followup error: %v\r\n", err)
					}
				}()
			} else {
				r.mu.Unlock()
			}
		}
	}

	// Ensure the demo animation is stopped before returning to the readline prompt
	art.StopLoop()

	// Use carriage return only so we don't create an extra blank line
	fmt.Print("\r")
	return err
}

// Legacy function kept for compatibility
func (r *REPL) supportsStreaming() bool {
	// Check if streaming mode is enabled in REPL
	if !r.configOptions.GetBool("llm.stream") {
		return false
	}
	// Check if API supports streaming
	provider := strings.ToLower(r.configOptions.Get("ai.provider"))
	return provider != "bedrock"
}

// Legacy function kept for compatibility
func (r *REPL) regularResponse(input string) error {
	// Create messages
	messages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}
	messages = append(messages, llm.Message{Role: "user", Content: input})

	// Create client and send message
	client, err := llm.NewLLMClient(r.buildLLMConfig())
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Print prompt for AI response
	fmt.Print("\r\nAI: ")

	// Send message without streaming
	_, err = client.SendMessage(messages, false, nil)

	fmt.Print("\r\n")
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

// handleShellInput processes input starting with '$' as hybrid AI/shell mode
func (r *REPL) handleShellInput(input string) error {
	// Handle redirection first, before substitutions
	var redirectType, redirectTarget string
	if idx := strings.LastIndex(input, ">"); idx != -1 {
		redirectType = "file"
		redirectTarget = strings.TrimSpace(input[idx+1:])
		input = strings.TrimSpace(input[:idx])
	} else if idx := strings.LastIndex(input, "|"); idx != -1 {
		redirectType = "pipe"
		redirectTarget = strings.TrimSpace(input[idx+1:])
		input = strings.TrimSpace(input[:idx])
	}

	// Check if this is command mode (contains /) or AI mode
	if strings.Contains(input, "/") {
		// Command mode: execute commands and handle output
		// Process slash substitutions
		processedInput, err := ExecuteSlashSubstitution(input, r)
		if err != nil {
			return fmt.Errorf("slash substitution failed: %v", err)
		}
		input = processedInput

		// If pipe, execute the command on the current input
		if redirectType == "pipe" {
			cmd := exec.Command("/bin/sh", "-c", redirectTarget)
			cmd.Stdin = strings.NewReader(input)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err = cmd.Run()
			if err != nil {
				return fmt.Errorf("pipe command failed: %v", err)
			}
			input = ""
			redirectType = ""
		}

		// Check for backticks
		hasBackticks := strings.Contains(input, "`")

		// Process backtick substitutions
		processedInput, err = ExecuteBacktickSubstitution(input, r)
		if err != nil {
			return fmt.Errorf("backtick substitution failed: %v", err)
		}
		input = processedInput

		// If backticks, send to AI; otherwise, handle output directly
		if hasBackticks {
			return r.sendToAI(input, redirectType, redirectTarget, false, true)
		} else {
			if redirectType == "file" {
				err = os.WriteFile(redirectTarget, []byte(input), 0644)
				if err != nil {
					return fmt.Errorf("failed to write to file %s: %v", redirectTarget, err)
				}
				fmt.Printf("Output written to %s\r\n", redirectTarget)
			} else {
				fmt.Print(input)
			}
			return nil
		}
	} else {
		// AI mode: send input to AI with redirection on response
		// Process backtick substitutions
		processedInput, err := ExecuteBacktickSubstitution(input, r)
		if err != nil {
			return fmt.Errorf("backtick substitution failed: %v", err)
		}
		input = processedInput

		// Send to AI with redirection
		return r.sendToAI(input, redirectType, redirectTarget, false, true)
	}
}

// handleNormalInput processes regular input (not starting with '$')
func (r *REPL) handleNormalInput(input string) error {
	// Handle verbatim inputs
	if len(input) >= 2 {
		if input[0] == '\'' && input[len(input)-1] == '\'' {
			input = input[1 : len(input)-1]
		}
	}

	// For normal input, skip backtick processing
	return r.sendToAI(input, "", "", false, false)
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
	return r.sendToAI(input, "", "", true, false)
}

// initCommands initializes the command registry with all available commands
func (r *REPL) initCommands() {
	// Helper commands
	r.commands["/help"] = Command{
		Name:        "/help",
		Description: "Show available commands",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.showCommands(), nil
		},
	}

	r.commands["/slurp"] = Command{
		Name:        "/slurp",
		Description: "Read from stdin until EOF (Ctrl+D)",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", r.handleSlurpCommand()
		},
	}

	// Dot command: read one or more files and send their combined contents as a prompt
	r.commands["."] = Command{
		Name:        ".",
		Description: "Load file(s) and send contents as a single prompt",
		Handler: func(r *REPL, args []string) (string, error) {
			if len(args) < 2 {
				return "Usage: . <path>\n\r", nil
			}
			var buf strings.Builder
			for _, path := range args[1:] {
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Sprintf("failed to read file '%s': %v\n\r", path, err), nil
				}
				buf.Write(data)
				buf.WriteString("\n")
			}
			return "", r.sendToAI(buf.String(), "", "", true, false)
		},
	}

	// Script command: execute a script file containing REPL commands
	r.commands["/script"] = Command{
		Name:        "/script",
		Description: "Execute a script file containing REPL commands",
		Handler: func(r *REPL, args []string) (string, error) {
			if len(args) < 2 {
				return "Usage: /script <path>\n\r", nil
			}
			return "", r.handleScriptCommand(args[1])
		},
	}

	// File handling commands
	r.commands["/image"] = Command{
		Name:        "/image",
		Description: "Add an image to the next message",
		Handler: func(r *REPL, args []string) (string, error) {
			if len(args) < 2 {
				return "Usage: /image <path>\n\r", nil
			}
			return r.addImage(args[1])
		},
	}

	r.commands["/file"] = Command{
		Name:        "/file",
		Description: "Add a file to the next message",
		Handler: func(r *REPL, args []string) (string, error) {
			if len(args) < 2 {
				return "Usage: /file <path>\n\r", nil
			}
			return r.addFile(args[1])
		},
	}

	r.commands["/noimage"] = Command{
		Name:        "/noimage",
		Description: "Remove pending images",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.clearPendingImages()
		},
	}

	r.commands["/nofiles"] = Command{
		Name:        "/nofiles",
		Description: "Remove pending files",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.clearPendingFiles()
		},
	}

	// Configuration commands
	r.commands["/set"] = Command{
		Name:        "/set",
		Description: "Set or display configuration option",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleSetCommand(args)
		},
	}

	r.commands["/get"] = Command{
		Name:        "/get",
		Description: "Display configuration option value",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleGetCommand(args)
		},
	}

	r.commands["/unset"] = Command{
		Name:        "/unset",
		Description: "Unset configuration option",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleUnsetCommand(args)
		},
	}

	r.commands["/env"] = Command{
		Name:        "/env",
		Description: "Set or display environment variable",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleEnvCommand(args)
		},
	}

	// Conversation management commands
	r.commands["/chat"] = Command{
		Name:        "/chat",
		Description: "Manage conversation (save, load, clear, list, log, undo, compact)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleChatCommand(args)
		},
	}
	// Session management commands
	r.commands["/session"] = Command{
		Name:        "/session",
		Description: "Manage chat sessions (new, list, use, del, purge)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleSessionCommand(args)
		},
	}

	// /compact command moved under /chat

	r.commands["/cancel"] = Command{
		Name:        "/cancel",
		Description: "Cancel current request",
		Handler: func(r *REPL, args []string) (string, error) {
			r.cancel()
			r.ctx, r.cancel = context.WithCancel(context.Background())
			return "", nil
		},
	}

	r.commands["/clear"] = Command{
		Name:        "/clear",
		Description: "Clear conversation messages",
		Handler: func(r *REPL, args []string) (string, error) {
			r.messages = []llm.Message{}
			return "Conversation messages cleared\r\n", nil
		},
	}

	// Last reply command
	r.commands["_"] = Command{
		Name:        "_",
		Description: "Print the last assistant reply",
		Handler: func(r *REPL, args []string) (string, error) {
			content, err := r.getLastAssistantReply()
			if err != nil {
				return fmt.Sprintf("%v\r\n", err), nil
			}

			// Return the content with markdown rendering if enabled
			if r.configOptions.GetBool("scr.markdown") {
				return llm.RenderMarkdown(content) + "\r\n", nil
			} else {
				// Replace single newlines with \r\n for proper terminal display
				content = strings.ReplaceAll(content, "\n", "\r\n")
				return content + "\r\n", nil
			}
		},
	}

	// Exit commands
	r.commands["/quit"] = Command{
		Name:        "/quit",
		Description: "Exit REPL",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", io.EOF
		},
	}

	r.commands["/exit"] = Command{
		Name:        "/exit",
		Description: "Exit REPL",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", io.EOF
		},
	}

	r.commands["/tool"] = Command{
		Name:        "/tool",
		Description: "Execute the mai-tool command, passing arguments",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleToolCommand(args)
		},
	}

	// Server management commands
	r.commands["/serve"] = Command{
		Name:        "/serve",
		Description: "Manage the background web server (start, stop, status)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleServeCommand(args)
		},
	}

	// System prompt shortcuts
	r.commands["/prompt"] = Command{
		Name:        "/prompt",
		Description: "Show current system prompt",
		Handler: func(r *REPL, args []string) (string, error) {
			sp := r.currentSystemPrompt()
			if sp == "" {
				return "No system prompt set\r\n", nil
			} else {
				return fmt.Sprintf("System prompt (%d chars):\r\n%s\r\n", len(sp), sp), nil
			}
		},
	}

	r.commands["/noprompt"] = Command{
		Name:        "/noprompt",
		Description: "Clear system prompt",
		Handler: func(r *REPL, args []string) (string, error) {
			// Clear inline and file-based system prompt settings
			r.configOptions.Unset("llm.systemprompt")
			r.configOptions.Unset("dir.promptfile")
			r.configOptions.Unset("llm.systempromptfile")
			return "System prompt cleared\r\n", nil
		},
	}

	// Only keep the models command for listing available models
	r.commands["/models"] = Command{
		Name:        "/models",
		Description: "List available models",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.listModels()
		},
	}

	// Command to list available providers
	r.commands["/providers"] = Command{
		Name:        "/providers",
		Description: "List available providers",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.listProviders()
		},
	}

	// Version command
	r.commands["/version"] = Command{
		Name:        "/version",
		Description: "Show version information",
		Handler: func(r *REPL, args []string) (string, error) {
			return fmt.Sprintf("mai-repl version %s\r\n", Version), nil
		},
	}

	// Template command
	r.commands["/template"] = Command{
		Name:        "/template",
		Description: "Fill template with key=value pairs and send to AI (use - as file to read from stdin)",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", r.handleTemplateSlashCommand(args)
		},
	}
}

// handleTemplateSlashCommand handles the /template command for template filling
func (r *REPL) handleTemplateSlashCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("Usage: /template <file> [key=value ...] or /template <file> -  (use - as file to read template from stdin)\n\r")
	}

	templateFile := args[1]
	var keyValues []string
	interactive := false

	if len(args) > 2 {
		if args[2] == "-" {
			interactive = true
		} else {
			keyValues = args[2:]
		}
	}

	// Read template content
	var templateContent []byte
	var err error
	if templateFile == "-" {
		// Read from stdin
		templateContent, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read template from stdin: %v", err)
		}
	} else {
		// Read from file
		templatePath, err := r.resolveTemplatePath(templateFile)
		if err != nil {
			return fmt.Errorf("template file not found: %v", err)
		}

		templateContent, err = os.ReadFile(templatePath)
		if err != nil {
			return fmt.Errorf("failed to read template file: %v", err)
		}
	}

	// Parse key=value pairs
	vars := make(map[string]string)
	for _, kv := range keyValues {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid key=value format: %s", kv)
		}
		key, value := parts[0], parts[1]

		// Handle @file slurping
		if strings.HasPrefix(value, "@") {
			filePath := value[1:]
			if strings.HasPrefix(filePath, "~") {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home directory: %v", err)
				}
				filePath = filepath.Join(homeDir, filePath[1:])
			}
			content, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %v", filePath, err)
			}
			value = string(content)
		}

		vars[key] = value
	}

	// Find all {key} placeholders
	re := regexp.MustCompile(`\{([^}]+)\}`)
	matches := re.FindAllStringSubmatch(string(templateContent), -1)

	// Collect required variables
	requiredVars := make(map[string]bool)
	for _, match := range matches {
		requiredVars[match[1]] = true
	}

	// Check for missing variables
	var missingVars []string
	for varName := range requiredVars {
		if _, exists := vars[varName]; !exists {
			missingVars = append(missingVars, varName)
		}
	}

	// Handle missing variables
	if len(missingVars) > 0 {
		if interactive {
			// Interactive mode: prompt for missing vars
			prompt := r.configOptions.Get("repl.prompt")
			if prompt == "" {
				prompt = ">>>"
			}

			p := r.readline.defaultPrompt
			r.readline.defaultPrompt = "?"
			for _, varName := range missingVars {
				fmt.Printf("%s\n\r%s ", varName, prompt)
				response, err := r.readline.Read()
				fmt.Print("\033[0m")
				if err != nil {
					r.readline.defaultPrompt = p
					return fmt.Errorf("error reading input: %v", err)
				}
				vars[varName] = response
			}
			r.readline.defaultPrompt = p
		} else {
			// Not interactive: show error listing all required vars
			var allRequired []string
			for varName := range requiredVars {
				allRequired = append(allRequired, varName)
			}
			sort.Strings(allRequired)
			return fmt.Errorf("missing required template variables. All required variables: %s", strings.Join(allRequired, ", "))
		}
	}

	// Replace placeholders
	result := string(templateContent)
	for key, value := range vars {
		placeholder := "{" + key + "}"
		result = strings.ReplaceAll(result, placeholder, value)
	}

	// Send to AI
	return r.sendToAI(result, "", "", true, false)
}

// handlePromptCommand handles the # command for prompt expansion
func (r *REPL) handlePromptCommand(input string) error {
	// Split the input into command and arguments
	parts := strings.SplitN(input, " ", 2)
	promptName := parts[0][1:] // Remove the # prefix

	if promptName == "" {
		prompts, err := r.listPrompts()
		if err != nil {
			fmt.Printf("%v\r\n", err)
			return nil
		}
		fmt.Printf("Available prompts (use # followed by name):\r\n")
		for _, name := range prompts {
			fmt.Printf("  %s\r\n", name)
		}
		return nil
	}

	// Load the prompt file content and send to AI
	var extra string
	if len(parts) > 1 {
		extra = parts[1]
	}
	expandedInput, err := r.loadPrompt(promptName, extra)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}
	return r.sendToAI(expandedInput, "", "", true, false)
}

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
	client, err := llm.NewLLMClient(r.buildLLMConfig())
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
	messages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}

	// Add conversation history if we should include replies
	if r.configOptions.GetBool("chat.replies") && len(r.messages) > 0 {
		messages = append(messages, r.messages...)
	}

	// Add the user query
	messages = append(messages, llm.Message{Role: "user", Content: processedQuery})

	// Call the LLM with streaming disabled
	response, err := client.SendMessage(messages, false, nil)
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

	// For other commands, run with inherited stdout/stderr
	cmd := exec.Command("sh", "-c", cmdString)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// displayConversationLog prints the current conversation messages
func (r *REPL) displayConversationLog() string {
	var output strings.Builder
	if len(r.messages) == 0 {
		output.WriteString("No conversation messages yet\r\n")
		return output.String()
	}

	output.WriteString("Conversation log:\r\n")
	output.WriteString("-----------------\r\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)

		output.WriteString(fmt.Sprintf("[%d] %s: ", i+1, role))

		// For log display, use a larger truncation limit
		content := msg.Content.(string)
		if len(content) > 100 {
			content = content[:97] + "..."
		}

		// Replace newlines with space for compact display
		content = strings.ReplaceAll(content, "\n", " ")

		output.WriteString(fmt.Sprintf("%s\r\n", content))
	}

	output.WriteString(fmt.Sprintf("Total messages: %d\r\n", len(r.messages)))
	output.WriteString(fmt.Sprintf("Settings: replies=%t, streaming=%t, reasoning=%t, logging=%t\r\n",
		r.configOptions.GetBool("chat.replies"),
		r.configOptions.GetBool("llm.stream"),
		r.configOptions.GetBool("llm.think"),
		r.configOptions.GetBool("chat.log")))

	// Display pending files if any
	if len(r.pendingFiles) > 0 {
		output.WriteString("\r\nPending files for next message:\r\n")
		imageCount := 0
		fileCount := 0

		for _, file := range r.pendingFiles {
			if file.isImage {
				imageCount++
				output.WriteString(fmt.Sprintf(" - Image: %s\r\n", file.filePath))
			} else {
				fileCount++
				output.WriteString(fmt.Sprintf(" - File: %s\r\n", file.filePath))
			}
		}

		output.WriteString(fmt.Sprintf("Total pending: %d images, %d files\r\n", imageCount, fileCount))
	}
	return output.String()
}

// displayFullConversationLog prints the complete conversation without truncating or filtering
func (r *REPL) displayFullConversationLog() string {
	var output strings.Builder
	if len(r.messages) == 0 {
		output.WriteString("No conversation messages yet\r\n")
		return output.String()
	}

	output.WriteString("# Full conversation log:\r\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)

		output.WriteString(fmt.Sprintf("\r\n## [%d] %s:\r\n", i+1, role))

		// Print the full content with preserved formatting
		// Apply markdown rendering if enabled
		if r.configOptions.GetBool("scr.markdown") {
			output.WriteString(fmt.Sprintf("%s\r\n", llm.RenderMarkdown(msg.Content.(string))))
		} else {
			// Replace single newlines with \r\n for proper terminal display
			content := strings.ReplaceAll(msg.Content.(string), "\n", "\r\n")
			output.WriteString(fmt.Sprintf("%s\r\n", content))
		}
		output.WriteString("--------------------\r\n")
	}

	output.WriteString(fmt.Sprintf("\r\nTotal messages: %d\r\n", len(r.messages)))
	return output.String()
}

// undoLastMessage removes the last message from the conversation history
func (r *REPL) undoLastMessage() {
	if len(r.messages) == 0 {
		fmt.Print("No messages to undo\r\n")
		return
	}
	// Remove the last message
	r.messages = r.messages[:len(r.messages)-1]
}

// clearPendingImages removes all pending images
func (r *REPL) clearPendingImages() (string, error) {
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

	var output strings.Builder
	if imageCount > 0 {
		output.WriteString(fmt.Sprintf("Removed %d pending image(s)\r\n", imageCount))
	} else {
		output.WriteString("No pending images to remove\r\n")
	}

	return output.String(), nil
}

// clearPendingFiles removes all pending non-image files
func (r *REPL) clearPendingFiles() (string, error) {
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

	var output strings.Builder
	if fileCount > 0 {
		output.WriteString(fmt.Sprintf("Removed %d pending file(s)\r\n", fileCount))
	} else {
		output.WriteString("No pending files to remove\r\n")
	}

	return output.String(), nil
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

// extractAtMentionFilenames scans input text for @filename mentions,
// supporting path separators and escaped spaces in filenames.
func extractAtMentionFilenames(input string) []string {
	var filenames []string
	r := []rune(input)
	for i := 0; i < len(r); i++ {
		if r[i] == '@' {
			i++
			var sb strings.Builder
			for i < len(r) {
				if r[i] == '\\' && i+1 < len(r) && r[i+1] == ' ' {
					sb.WriteRune(' ')
					i += 2
				} else if unicode.IsSpace(r[i]) {
					break
				} else {
					sb.WriteRune(r[i])
					i++
				}
			}
			if sb.Len() > 0 {
				filenames = append(filenames, sb.String())
			}
		}
	}
	return filenames
}

// processAtMentions extracts words starting with @ from input text,
// checks if they correspond to existing files, and returns the enhanced prompt
func (r *REPL) processAtMentions(input string) string {
	filenames := extractAtMentionFilenames(input)
	if len(filenames) == 0 {
		return input // No @mentions found, return original input
	}

	// Process each @mention
	var fileContents []string
	var processedFiles []string

	for _, filename := range filenames {
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

// processIncludeStatements processes include directives in prompt content.
// Lines starting with '@' are treated as file paths to include.
func (r *REPL) processIncludeStatements(content, baseDir string) string {
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
				fmt.Fprintf(os.Stderr, "Error including file %s: %v\n", target, err)
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

// autoDetectPromptDir attempts to find a prompts directory relative to the executable path
// and sets the promptdir config variable if found
func (r *REPL) autoDetectPromptDir() {
	r.autoDetectDirectory("dir.prompt", "prompts", true)
}

// showCurrentModel displays the current model based on the provider
func (r *REPL) showCurrentModel() {
	provider := r.configOptions.Get("ai.provider")
	model := r.configOptions.Get("ai.model")
	if provider == "" {
		fmt.Printf("Current model: %s\r\n", model)
		return
	}
	fmt.Printf("Current model: %s (provider: %s)\r\n", model, provider)
}

// setModel changes the model for the current provider
func (r *REPL) setModel(model string) error {
	r.configOptions.Set("ai.model", model)
	prov := r.configOptions.Get("ai.provider")
	if prov == "" {
		fmt.Printf("Model set to %s\r\n", model)
		return nil
	}
	fmt.Printf("%s model set to %s\r\n", strings.Title(prov), model)
	return nil
}

// showCurrentProvider displays the current provider
func (r *REPL) showCurrentProvider() {
	fmt.Printf("Current provider: %s\r\n", r.configOptions.Get("ai.provider"))
	// Also show the current model for this provider
	r.showCurrentModel()
}

// getValidProviders returns a map of valid providers
func (r *REPL) getValidProviders() map[string]bool {
	return map[string]bool{
		"ollama":   true,
		"lmstudio": true,
		"openai":   true,
		"shimmy":   true,
		"claude":   true,
		"gemini":   true,
		"google":   true,
		"mistral":  true,
		"deepseek": true,
		"bedrock":  true,
		"aws":      true,
		"xai":      true,
	}
}

// isProviderAvailable checks if a provider is available by creating a temporary config and provider instance
func (r *REPL) isProviderAvailable(provider string) bool {
	// Create a temporary config for this provider
	cfg := loadConfig()
	// Apply current options but override the provider
	applyConfigOptionsToLLMConfig(cfg, &r.configOptions)
	cfg.PROVIDER = provider

	// Create provider instance
	prov, err := llm.CreateProvider(cfg)
	if err != nil {
		return false
	}

	// Check if provider implements IsAvailable
	if availableProvider, ok := prov.(interface{ IsAvailable() bool }); ok {
		return availableProvider.IsAvailable()
	}

	// Fallback: assume available if we can create the provider
	return true
}

// listProviders displays all available providers
func (r *REPL) listProviders() (string, error) {
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

	var output strings.Builder
	output.WriteString("Available providers:\r\n")
	for _, provider := range providers {
		// Check if provider is available
		isAvailable := r.isProviderAvailable(provider)
		var emoji string
		if isAvailable {
			emoji = "\033[92m✅\033[0m" // Green checkmark
		} else {
			emoji = "\033[91m❌\033[0m" // Red X
		}

		if provider == r.configOptions.Get("ai.provider") {
			output.WriteString(fmt.Sprintf("%s * %s (current)\r\n", emoji, provider))
		} else {
			output.WriteString(fmt.Sprintf("%s   %s\r\n", emoji, provider))
		}
	}

	output.WriteString("\r\nUse '/set ai.provider <name>' to change the current provider\r\n")
	return output.String(), nil
}

// setProvider changes the current provider
func (r *REPL) setProvider(provider string) error {
	// Check if the provider is valid
	validProviders := r.getValidProviders()

	// Convert provider to lowercase for case-insensitive comparison
	provider = strings.ToLower(provider)

	if !validProviders[provider] {
		fmt.Printf("Invalid provider: %s\r\n", provider)
		fmt.Print("Valid providers: ollama, lmstudio, openai, shimmy, claude, gemini/google, mistral, deepseek, bedrock/aws, xai\r\n")
		return nil
	}

	// Update the provider in the configOptions
	r.configOptions.Set("ai.provider", provider)

	// Resolve a default model for the new provider from env/provider defaults
	if dm := r.resolveDefaultModelForProvider(provider); dm != "" {
		r.configOptions.Set("ai.model", dm)
	}

	fmt.Printf("Provider set to %s\r\n", provider)

	// Also show the current model for this provider
	r.showCurrentModel()

	return nil
}

// resolveDefaultModelForProvider returns the provider's default model using current settings
func (r *REPL) resolveDefaultModelForProvider(provider string) string {
	cfg := r.buildLLMConfig()
	cfg.PROVIDER = provider
	client, err := llm.NewLLMClient(cfg)
	if err != nil || client == nil {
		return ""
	}
	return client.DefaultModel()
}

// listModels fetches and displays available models for the current provider
func (r *REPL) listModels() (string, error) {
	var output strings.Builder

	// Create client
	client, err := llm.NewLLMClient(r.buildLLMConfig())
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %v", err)
	}

	output.WriteString(fmt.Sprintf("Fetching available models for %s...\r\n", r.configOptions.Get("ai.provider")))

	// Get models from the provider
	models, err := client.ListModels()
	if err != nil {
		return "", fmt.Errorf("failed to fetch models: %v", err)
	}

	if len(models) == 0 {
		output.WriteString("No models available for this provider\r\n")
		return output.String(), nil
	}

	// Display models
	output.WriteString(fmt.Sprintf("Available %s models:\r\n", r.configOptions.Get("ai.provider")))
	output.WriteString("-----------------------\r\n")

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
			output.WriteString(fmt.Sprintf("[%d] %s%s - %s\r\n", i+1, model.ID, current, model.Description))
		} else {
			output.WriteString(fmt.Sprintf("[%d] %s%s\r\n", i+1, model.ID, current))
		}
	}

	output.WriteString(fmt.Sprintf("Total models: %d\r\n", len(models)))
	output.WriteString("Use '/set ai.model <model-id>' to change the model\r\n")

	return output.String(), nil
}

// getCurrentModelForProvider returns the current model ID for the active provider
func (r *REPL) getCurrentModelForProvider() string {
	return r.configOptions.Get("ai.model")
}

// handleCompactCommand processes the /compact command
// It loads the compact.txt prompt and submits the entire conversation history
// to the AI, then replaces all messages with the AI's response
// handleOptionCompletion handles tab completion for configuration options
func (r *REPL) handleOptionCompletion(line *strings.Builder, cmd, partialOption string) {
	var options []string

	if cmd == "/set" || cmd == "/get" {
		// For /set and /get, show all available options
		options = r.configOptions.GetAvailableOptions()
	} else if cmd == "/unset" {
		// For /unset, show only options that are currently set
		options = r.configOptions.GetKeys()
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
	compactMessage := llm.Message{
		Role:    "user",
		Content: string(compactPrompt) + "\n\n" + conversationText.String(),
	}

	// Save original messages for recovery if needed
	originalMessages := r.messages

	// Replace messages with just the compact message
	r.messages = []llm.Message{compactMessage}

	fmt.Print("Compacting conversation...\r\n")

	// Create client and send message
	client, err := llm.NewLLMClient(r.buildLLMConfig())
	if err != nil {
		// Restore original messages on error
		r.messages = originalMessages
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Prepare messages for the API
	apiMessages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		apiMessages = append(apiMessages, llm.Message{Role: "system", Content: sp})
	}
	apiMessages = append(apiMessages, compactMessage)

	// Send the message to the AI (non-streaming mode for this operation)
	response, err := client.SendMessage(apiMessages, false, nil)
	if err != nil {
		// Restore original messages on error
		r.messages = originalMessages
		return fmt.Errorf("failed to compact conversation: %v", err)
	}

	// Create the assistant response message
	assistantMessage := llm.Message{Role: "assistant", Content: response}

	// Replace the conversation with just the compact message and response
	r.messages = []llm.Message{
		llm.Message{Role: "user", Content: "Please provide a compact response to my questions and needs."},
		assistantMessage,
	}

	fmt.Print("Conversation compacted successfully.\r\n")

	return nil
}

// handleToolCommand executes the mai-tool command with the given arguments
func (r *REPL) handleToolCommand(args []string) (string, error) {
	var output strings.Builder
	if len(args) < 2 {
		tools, err := GetAvailableToolsWithConfig(r.configOptions, Simple)
		if err == nil {
			output.WriteString(tools)
			output.WriteString("\n")
		}
	} else {
		res, err := ExecuteTool(args[1], args[2:]...)
		if err == nil {
			output.WriteString(res)
			output.WriteString("\n")
		} else {
			return "", err
		}
	}

	return output.String(), nil
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
		SystemPrompt string        `json:"system_prompt,omitempty"`
		Messages     []llm.Message `json:"messages"`
	}{
		SystemPrompt: r.currentSystemPrompt(),
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
		SystemPrompt string        `json:"system_prompt"`
		Messages     []llm.Message `json:"messages"`
	}

	if err := json.Unmarshal(data, &conversationData); err != nil {
		// Try parsing legacy format that included provider and model
		var legacyData struct {
			SystemPrompt string        `json:"system_prompt"`
			Messages     []llm.Message `json:"messages"`
			Provider     string        `json:"provider"`
			Model        string        `json:"model"`
		}

		if err := json.Unmarshal(data, &legacyData); err != nil {
			return fmt.Errorf("failed to parse conversation file: %v", err)
		}

		// Copy data from legacy format
		conversationData.SystemPrompt = legacyData.SystemPrompt
		conversationData.Messages = legacyData.Messages
	}

	// Update REPL with loaded data
	if conversationData.SystemPrompt != "" {
		_ = r.configOptions.Set("llm.systemprompt", conversationData.SystemPrompt)
	} else {
		r.configOptions.Unset("llm.systemprompt")
	}
	r.messages = conversationData.Messages

	fmt.Printf("Conversation loaded from %s (%d messages)\r\n", path, len(r.messages))
	if conversationData.SystemPrompt != "" {
		fmt.Print("System prompt loaded\r\n")
	}
	return nil
}

/// utils

var startTime time.Time

func StartTimer() {
	startTime = time.Now()
}

func StopTimer() {
	elapsed := time.Since(startTime)
	minutes := int(elapsed.Minutes())      // Get the elapsed minutes
	seconds := int(elapsed.Seconds()) % 60 // Get the remaining seconds
	fmt.Printf("⏳ Elapsed time: %d minutes and %d seconds\n", minutes, seconds)
}
