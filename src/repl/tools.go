package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Tool represents a tool that can be called by name with arguments
// This structure is designed to be easily expanded for function calling
// where remote LLMs will decide which tools to call with which arguments
type Tool struct {
	Name        string   // Name of the tool to be called
	Description string   // Description of what the tool does
	Args        []string // Arguments to pass to the tool
	Action      string   // Action type: Solve, Error, or Iterate
	NextStep    string   // Brief explanation of what should be done next
}

// GetAvailableTools runs the 'mai-tool list' command and returns the output as a string
func GetAvailableTools(quiet bool) (string, error) {
	var cmd *exec.Cmd
	if quiet {
		cmd = exec.Command("mai-tool", "-q", "list")
	} else {
		cmd = exec.Command("mai-tool", "list")
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	return out.String(), err
}

// callTool executes a specified tool with provided arguments and returns the output
func callTool(tool *Tool) (string, error) {
	// Combine the tool name and arguments for the mai-tool command
	// tool.Name may be in the format "server/tool"
	cmdArgs := append([]string{"call", tool.Name}, tool.Args...)
	cmd := exec.Command("mai-tool", cmdArgs...)

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
	// Check if tool is required by looking for "Tool Required: Yes"
	if !strings.Contains(message, "Tool Required: Yes") {
		return []*Tool{}, nil
	}

	// Extract the selected tool line
	toolLineIdx := strings.Index(message, "Selected Tool: ")
	if toolLineIdx == -1 {
		return []*Tool{}, nil
	}

	// Extract the tool name and command
	toolLine := message[toolLineIdx:]
	toolLine = strings.Split(toolLine, "\n")[0]
	toolLine = strings.TrimPrefix(toolLine, "Selected Tool: ")

	// Split the tool line to get the tool name and command
	toolParts := strings.SplitN(toolLine, " ", 2)
	toolName := toolParts[0]

	// Default empty args slice
	args := []string{}

	// Extract parameters (key=value pairs) if present
	paramsIdx := strings.Index(message, "Parameters: ")
	if paramsIdx != -1 {
		paramsLine := message[paramsIdx:]
		paramsLine = strings.Split(paramsLine, "\n")[0]
		paramsText := strings.TrimPrefix(paramsLine, "Parameters: ")

		// Parse space-separated key=value parameters
		paramPairs := strings.Fields(paramsText)
		for _, pair := range paramPairs {
			args = append(args, pair)
		}
	} else if len(toolParts) > 1 {
		// If no Parameters line but there are arguments in the tool line, use those
		args = strings.Fields(toolParts[1])
	}

	// Extract the reasoning for display
	reasoningIdx := strings.Index(message, "Reasoning: ")
	if reasoningIdx != -1 {
		reasoningLine := message[reasoningIdx:]
		reasoningText := strings.Split(reasoningLine, "\n")[0]
		reasoningText = strings.TrimPrefix(reasoningText, "Reasoning: ")
		// Print reasoning in magenta
		fmt.Printf("\r\033[35m(tool) %s\033[0m\n", reasoningText)
	}

	// Extract the Action field
	action := "Solve" // Default action
	actionIdx := strings.Index(message, "Action: ")
	if actionIdx != -1 {
		actionLine := message[actionIdx:]
		actionText := strings.Split(actionLine, "\n")[0]
		action = strings.TrimPrefix(actionText, "Action: ")
	}

	// Extract the NextStep field
	nextStep := "" // Default empty next step
	nextStepIdx := strings.Index(message, "NextStep: ")
	if nextStepIdx != -1 {
		nextStepLine := message[nextStepIdx:]
		nextStepText := strings.Split(nextStepLine, "\n")[0]
		nextStep = strings.TrimPrefix(nextStepText, "NextStep: ")
	}

	// Create and return the tool
	return []*Tool{{
		Name:     toolName,
		Args:     args,
		Action:   action,
		NextStep: nextStep,
	}}, nil
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

// getToolPrompt returns the content of the tool.md prompt file
func getToolPrompt(repl *REPL) (string, error) {
	// Get prompt directory path
	toolPromptPath, err := repl.resolvePromptPath("tool.md")
	if err != nil {
		return "", fmt.Errorf("failed to find the tool prompt: %w", err)
	}
	toolPromptBytes, err := os.ReadFile(toolPromptPath)
	if err != nil {
		return "", fmt.Errorf("failed to read tool prompt file: %w", err)
	}

	return string(toolPromptBytes), nil
}

// buildMessageWithTools formats a message with tool information
func buildMessageWithTools(toolPrompt string, userInput string, toolList string) string {
	msg := fmt.Sprintf("%s\n%s\n----\nThese are the tools available:\n%s",
		toolPrompt, userInput, toolList)
	// DEBUG fmt.Println(msg)
	return msg
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

	if strings.HasPrefix(message, "ERROR") {
		return "", fmt.Errorf("%v", message)
	}
	// Execute each tool and collect results
	results := []string{}
	for _, tool := range tools {
		result, err := callTool(tool)
		if err != nil {
			return "", err
		}
		results = append(results, result)

		// If we have Action and NextStep, add them to the result
		if tool.Action != "" || tool.NextStep != "" {
			results = append(results, fmt.Sprintf("\nAction: %s\nNextStep: %s", tool.Action, tool.NextStep))
		}
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

	// Get the tool prompt content
	toolPrompt, err := getToolPrompt(replImpl)
	if err != nil {
		// If can't get the tool prompt, return input unchanged
		return input
	}

	// Get list of available tools
	toolList, err := GetAvailableTools(false)
	if err != nil {
		// If can't get tool list, use tool.md and user input without tool list
		return buildMessageWithTools(toolPrompt, input,
			fmt.Sprintf("[Error getting tool list: %v]", err))
	}
	// Build and return the processed input
	return buildMessageWithTools(toolPrompt, input, toolList)
}
