package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/trufae/mai/src/repl/art"
	"github.com/trufae/mai/src/repl/llm"
)

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
		mcpProcesses:     make(map[string]*MCPProcess), // Initialize MCP processes map
	}

	// Create chat directory and history file
	if err := repl.setupHistory(); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up history: %v\n", err)
	}
	// Load persistent REPL history into readline
	repl.loadReplHistory()

	// Set prompts in the readline instance
	if repl.readline != nil {
		repl.readline.SetPrompt(repl.configOptions.Get("repl.prompt"))

		repl.readline.SetReadlinePrompt(repl.configOptions.Get("repl.prompt2"))

		if bgcolor := repl.configOptions.Get("ui.bgcolor"); bgcolor != "" {
			repl.readline.SetBgColor(bgcolor)
		}

		if fgcolor := repl.configOptions.Get("ui.fgcolor"); fgcolor != "" {
			repl.readline.SetFgColor(fgcolor)
		}

		if repl.configOptions.GetBool("ui.bold") {
			repl.readline.SetBold(true)
		}

		if fgprompt := repl.configOptions.Get("ui.fgprompt"); fgprompt != "" {
			repl.readline.SetFgPromptColor(fgprompt)
		}

		if bgprompt := repl.configOptions.Get("ui.bgprompt"); bgprompt != "" {
			repl.readline.SetBgPromptColor(bgprompt)
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

	// Load Claude Skills
	repl.skillRegistry = NewSkillRegistry()
	skillsDir, err := getSkillsDirForConfig(repl.configOptions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to determine skills directory: %v\n", err)
	} else {
		if err := repl.skillRegistry.LoadSkills(skillsDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load skills: %v\n", err)
		} else {
			skills := repl.skillRegistry.ListSkills()
			if len(skills) > 0 {
				fmt.Fprintf(os.Stderr, "Loaded %d Claude Skills\n", len(skills))
			}
		}
	}

	// Set the stop demo callback to transition out of the "thinking" action
	// when the first token is received. Previously this stopped the demo loop
	// entirely which caused subsequent streaming tokens to be buffered but
	// not rendered until the next prompt restarted the loop. Instead, clear
	// the action label so the demo loop continues running and will display
	// tokens as they arrive.
	repl.stopDemoCallback = func() {
		if repl.configOptions.GetBool("ui.demo") {
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

	repl.configOptions.RegisterOptionListener("ui.bgcolor", func(value string) {
		if repl.readline != nil {
			repl.readline.SetBgColor(value)
		}
	})

	repl.configOptions.RegisterOptionListener("ui.fgcolor", func(value string) {
		if repl.readline != nil {
			repl.readline.SetFgColor(value)
		}
	})

	repl.configOptions.RegisterOptionListener("ui.bold", func(value string) {
		if repl.readline != nil {
			repl.readline.SetBold(strings.ToLower(value) == "true")
		}
	})

	repl.configOptions.RegisterOptionListener("ui.bgline", func(value string) {
		if repl.readline != nil {
			repl.readline.SetBgLineColor(value)
		}
	})

	repl.configOptions.RegisterOptionListener("ui.fgprompt", func(value string) {
		if repl.readline != nil {
			repl.readline.SetFgPromptColor(value)
		}
	})

	repl.configOptions.RegisterOptionListener("ui.bgprompt", func(value string) {
		if repl.readline != nil {
			repl.readline.SetBgPromptColor(value)
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

	// Load MCP configuration
	if err := repl.loadMCPConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load MCP config: %v\n", err)
	}

	// Auto-start enabled MCP servers only if not running a single command
	if repl.initialCommand == "" {
		for name, server := range repl.mcpConfig.Servers {
			if server.Enabled {
				if err := repl.startMCPServer(name); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: Failed to start MCP server %s: %v\n", name, err)
				} else {
					fmt.Fprintf(os.Stderr, "Started MCP server: %s\n", name)
				}
			}
		}
	}

	if repl.configOptions.GetBool("mcp.daemon") {
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
				os.Setenv("MAI_TOOL_BASEURL", fmt.Sprintf("http://localhost:%d", port))
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

func (r *REPL) cleanup() {
	if r.wmcpProcess != nil && r.wmcpProcess.Process != nil {
		r.wmcpProcess.Process.Kill()
	}
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

	// Note: MCP processes are designed to run independently of REPL sessions
	// They are not killed on REPL exit so they can persist across sessions
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
		client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
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
