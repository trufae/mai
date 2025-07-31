package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var cmdRegex = regexp.MustCompile(`\$\((.*?)\)`)

// ExecuteCommandSubstitution processes text and replaces command substitutions $(command)
// with the output of executing those commands. Returns the processed text.
func ExecuteCommandSubstitution(input string) (string, error) {
	// Find all command substitutions in the input text
	result := input
	matches := cmdRegex.FindAllStringSubmatch(input, -1)

	for _, match := range matches {
		fullMatch := match[0] // The full $(command) string
		command := match[1]   // Just the command inside the $()

		// Execute the command
		output, err := executeCommand(command)
		if err != nil {
			return input, fmt.Errorf("command execution failed: %v", err)
		}

		// Replace the command substitution with its output
		result = strings.Replace(result, fullMatch, output, 1)
	}

	return result, nil
}

// executeCommand runs a shell command and returns its output.
func executeCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command execution error: %v\nStderr: %s", err, stderr.String())
	}

	// Trim trailing newlines from the command output
	return strings.TrimRight(stdout.String(), "\n"), nil
}
