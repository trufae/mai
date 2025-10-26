package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// registerHelpCommands registers help and utility commands
func registerHelpCommands(r *REPL) {
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
			return "", handleSlurpCommand(r)
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
			return "", handleScriptCommand(r, args[1])
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
}

// handleSlurpCommand reads from stdin until EOF (Ctrl+D) and returns the content
func handleSlurpCommand(r *REPL) error {
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

func handleScriptCommand(r *REPL, scriptPath string) error {
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
