package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

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

func (r *REPL) initCommands() {
	// Register command groups
	registerHelpCommands(r)
	registerFileCommands(r)
	registerChatCommands(r)
	registerExitCommands(r)

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

	// Skills command: manage Claude Skills
	r.commands["/skills"] = Command{
		Name:        "/skills",
		Description: "Manage Claude Skills",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleSkillsCommand(args)
		},
	}

	// MCP command: manage MCP servers
	r.commands["/mcp"] = Command{
		Name:        "/mcp",
		Description: "Manage MCP servers (start, stop, restart, enable, disable, edit, status)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleMCPCommand(args)
		},
	}

	// Register file and image handling commands
	registerFileCommands(r)

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

	// Register conversation management commands
	registerChatCommands(r)

	// Register exit commands
	registerExitCommands(r)

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

	// Template command
	r.commands["/template"] = Command{
		Name:        "/template",
		Description: "Fill template with key=value pairs and send to AI (use - as file to read from stdin)",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", r.handleTemplateSlashCommand(args)
		},
	}
}
