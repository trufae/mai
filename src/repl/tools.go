package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tool represents a tool that can be called by name with arguments
// This structure is designed to be easily expanded for function calling
// where remote LLMs will decide which tools to call with which arguments
type Tool struct {
	Name        string   // Name of the tool to be called
	Description string   // Description of what the tool does
	Args        []string // Arguments to pass to the tool
}

// getAvailableTools runs the 'acli-tool list' command and returns the output as a string
func getAvailableTools() (string, error) {
	cmd := exec.Command("acli-tool", "list")
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error executing acli-tool list: %v: %s", err, stderr.String())
	}

	return out.String(), nil
}

// callTool executes a specified tool with provided arguments and returns the output
func callTool(tool *Tool) (string, error) {
	// Combine the tool name and arguments for the acli-tool command
	cmdArgs := append([]string{tool.Name}, tool.Args...)
	cmd := exec.Command("acli-tool", cmdArgs...)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error executing tool %s: %v: %s", tool.Name, err, stderr.String())
	}

	return out.String(), nil
}

// getToolsFromMessage parses a user message to identify tool calls
// This function will be expanded when implementing function calling
// where the remote LLM will provide structured function call data
// that this function will parse and convert to Tool instances
func getToolsFromMessage(message string) ([]*Tool, error) {
	// Currently a placeholder - will be implemented for function calling
	// The implementation will parse structured function call data from the LLM
	// such as JSON objects representing tool calls with name and arguments
	// For now, return an empty slice
	return []*Tool{}, nil
}

// ExecuteToolList runs the 'acli-tool list' command and returns the output as a string
// Kept for backward compatibility
func ExecuteToolList() (string, error) {
	return getAvailableTools()
}

// ExecuteTool runs a specified tool with provided arguments and returns the output
// Kept for backward compatibility
func ExecuteTool(toolName string, args ...string) (string, error) {
	tool := &Tool{
		Name: toolName,
		Args: args,
	}
	return callTool(tool)
}

// getToolPromptPath returns the path to the tool.md prompt file
func getToolPromptPath(repl *REPL) (string, error) {
	// Get prompt directory path
	promptDir := repl.config.options.Get("promptdir")
	if promptDir == "" {
		// Try common locations if promptdir is not set
		commonLocations := []string{
			"./prompts",
			"../prompts",
		}

		for _, loc := range commonLocations {
			if _, err := os.Stat(loc); err == nil {
				promptDir = loc
				break
			}
		}

		if promptDir == "" {
			// If still not found, return error
			return "", fmt.Errorf("prompt directory not found")
		}
	}

	return filepath.Join(promptDir, "tool.md"), nil
}

// buildMessageWithTools formats a message with tool information
func buildMessageWithTools(toolPrompt string, userInput string, toolList string) string {
	return fmt.Sprintf("%s\n%s\n----\nThese are the tools available:\n%s", 
		toolPrompt, userInput, toolList)
}

// executeToolsInMessage processes any tool calls found in a message and returns results
// When function calling is implemented, this function will:
// 1. Extract tool calls from the LLM response
// 2. Execute each tool with its arguments
// 3. Collect the results for potentially sending back to the LLM
func executeToolsInMessage(message string) (string, error) {
	// Parse message to extract tool calls
	tools, err := getToolsFromMessage(message)
	if err != nil {
		return "", err
	}
	
	// Execute each tool and collect results
	results := []string{}
	for _, tool := range tools {
		result, err := callTool(tool)
		if err != nil {
			return "", err
		}
		results = append(results, result)
	}
	
	// For function calling, these results would be formatted and sent back to the LLM
	return strings.Join(results, "\n"), nil
}

// ProcessUserInput is a function that takes user input and the REPL context
// and returns a processed string. When the "usetools" option is enabled, 
// user input is processed through this function.
func ProcessUserInput(input string, repl interface{}) string {
	// Type assertion to access REPL methods and fields
	replImpl, ok := repl.(*REPL)
	if !ok {
		// If type assertion fails, return input unchanged
		return input
	}
	
	// Get the tool prompt path
	toolPromptPath, err := getToolPromptPath(replImpl)
	if err != nil {
		return input
	}
	
	// Read the tool.md content
	toolPromptBytes, err := os.ReadFile(toolPromptPath)
	if err != nil {
		// If can't read the tool prompt, return input unchanged
		return input
	}
	toolPrompt := string(toolPromptBytes)

	// Get list of available tools
	toolList, err := getAvailableTools()
	if err != nil {
		// If can't get tool list, use tool.md and user input without tool list
		return buildMessageWithTools(toolPrompt, input, 
			fmt.Sprintf("[Error getting tool list: %v]", err))
	}

	// Process any tool calls in the message (placeholder for future implementation)
	// Currently does nothing
	_, _ = executeToolsInMessage(input)
	
	// Build and return the processed input
	return buildMessageWithTools(toolPrompt, input, toolList)
}