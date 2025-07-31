package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var cmdRegex = regexp.MustCompile(`\$\((.*?)\)`)

// The backtick regex uses negative lookbehind (?<!\) to ensure we don't match escaped backticks (\`)
// Note: Go's regexp doesn't support lookbehind, so we'll need a different approach
var backtickRegex = regexp.MustCompile("`(.*?)`")

// Environment variable substitution regex matches ${VAR_NAME} patterns
var envVarRegex = regexp.MustCompile(`\$\{([^{}]+)\}`)

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

// ExecuteBacktickSubstitution processes text and replaces backtick command substitutions `command`
// with the output of executing those commands. It also handles special case for LLM queries.
// Returns the processed text.
func ExecuteBacktickSubstitution(input string, r *REPL) (string, error) {
	// Pre-process input to handle escaped backticks
	// Replace \` with a temporary placeholder that won't match our regex
	const escapedBacktickPlaceholder = "ESCAPED_BACKTICK_PLACEHOLDER"
	processedInput := strings.ReplaceAll(input, "\\`", escapedBacktickPlaceholder)

	// Find all backtick substitutions in the processed text
	result := processedInput
	matches := backtickRegex.FindAllStringSubmatchIndex(processedInput, -1)

	// Process matches in reverse order so that replacements don't affect positions of later matches
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]

		// Get the start and end positions of the match and content
		fullMatchStart, fullMatchEnd := match[0], match[1]
		contentStart, contentEnd := match[2], match[3]

		// Extract the command content
		command := processedInput[contentStart:contentEnd]

		var output string
		var err error

		// Check if it's a shell command with ! prefix
		if strings.HasPrefix(command, "!") {
			// Execute as shell command
			output, err = executeCommand(command[1:])
			if err != nil {
				return input, fmt.Errorf("backtick shell command execution failed: %v", err)
			}
		} else {
			// Execute as LLM query with streaming disabled
			output, err = r.executeLLMQueryWithoutStreaming(command)
			if err != nil {
				return input, fmt.Errorf("backtick LLM query failed: %v", err)
			}
		}

		// Replace the backtick substitution with its output
		result = result[:fullMatchStart] + output + result[fullMatchEnd:]
	}

	// Restore escaped backticks
	result = strings.ReplaceAll(result, escapedBacktickPlaceholder, "`")

	return result, nil
}

// ExecuteEnvVarSubstitution processes text and replaces environment variable references ${VAR_NAME}
// with their values from the environment. Returns the processed text.
func ExecuteEnvVarSubstitution(input string) (string, error) {
	// Find all environment variable substitutions in the input text
	result := input
	matches := envVarRegex.FindAllStringSubmatch(input, -1)

	for _, match := range matches {
		fullMatch := match[0] // The full ${VAR_NAME} string
		varName := match[1]   // Just the variable name inside the ${}

		// Get the environment variable value
		varValue := os.Getenv(varName)

		// Replace the variable reference with its value
		result = strings.Replace(result, fullMatch, varValue, 1)
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
