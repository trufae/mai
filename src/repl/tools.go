package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	Plan        string   // Overall plan for solving the problem
	Progress    string   // Current progress through the plan
	StepNumber  int      // Current step number in the execution
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
	// Validate the tool name
	if tool.Name == "" {
		return "", fmt.Errorf("empty tool name provided")
	}

	// Add some basic sanitization
	toolName := strings.TrimSpace(tool.Name)

	// Check for potential command injection
	if strings.ContainsAny(toolName, ";&|<>$\\\"'`") {
		return "", fmt.Errorf("invalid characters in tool name: %s", tool.Name)
	}

	// Sanitize arguments
	var safeArgs []string
	for _, arg := range tool.Args {
		// Basic argument sanitization
		if strings.TrimSpace(arg) != "" {
			safeArgs = append(safeArgs, arg)
		}
	}

	// Combine the tool name and arguments for the mai-tool command
	// tool.Name may be in the format "server/tool"
	cmdArgs := append([]string{"call", toolName}, safeArgs...)
	cmd := exec.Command("mai-tool", cmdArgs...)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	// Set a timeout for the command execution
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Set the command context with timeout
	cmd = exec.CommandContext(timeoutCtx, "mai-tool", cmdArgs...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("tool execution timed out after 30 seconds: %s", tool.Name)
		}
		return "", fmt.Errorf("error executing tool %s: %v: %s", tool.Name, err, stderr.String())
	}

	result := out.String()
	// Provide some feedback if the result is empty
	if strings.TrimSpace(result) == "" {
		return "", fmt.Errorf("tool %s returned empty result", tool.Name)
	}

	return result, nil
}

// getToolsFromMessage parses a user message to identify tool calls
// This function will be expanded when implementing function calling
// where the remote LLM will provide structured function call data
// that this function will parse and convert to Tool instances
func getToolsFromMessage(message string) ([]*Tool, error) {
	// First try: check if tool is required by looking for "Tool Required: Yes"
	// but also handle cases where models don't follow the exact format
	toolRequired := strings.Contains(message, "Tool Required: Yes")

	// If not explicitly required, look for other indicators that a tool might be needed
	if !toolRequired {
		// Check for other phrases that might indicate tool usage
		toolPhrases := []string{
			"we need to use",
			"I will use",
			"let's use",
			"need to run",
			"should use",
			"will run",
			"using the tool",
		}

		for _, phrase := range toolPhrases {
			if strings.Contains(strings.ToLower(message), strings.ToLower(phrase)) {
				toolRequired = true
				break
			}
		}
	}

	if !toolRequired {
		return []*Tool{}, nil
	}

	// Extract the selected tool line
	toolLineIdx := strings.Index(message, "Selected Tool: ")
	if toolLineIdx == -1 {
		// Try alternate formats that models might use
		alternateFormats := []string{"Tool: ", "Using tool: ", "Tool name: ", "I'll use: "}

		for _, format := range alternateFormats {
			idx := strings.Index(message, format)
			if idx != -1 {
				toolLineIdx = idx
				break
			}
		}

		// If still not found, give up
		if toolLineIdx == -1 {
			return []*Tool{}, nil
		}
	}

	// Extract the tool name and command
	toolLine := message[toolLineIdx:]
	toolLine = strings.Split(toolLine, "\n")[0]

	// Handle different prefix formats
	knownPrefixes := []string{
		"Selected Tool: ",
		"Tool: ",
		"Using tool: ",
		"Tool name: ",
		"I'll use: ",
	}

	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(toolLine, prefix) {
			toolLine = strings.TrimPrefix(toolLine, prefix)
			break
		}
	}

	// Split the tool line to get the tool name and command
	toolParts := strings.SplitN(toolLine, " ", 2)
	toolName := strings.TrimSpace(toolParts[0])
	if toolName == "" || toolName == "None" || toolName == "N/A" {
		return []*Tool{}, nil
	}

	// Default empty args slice
	args := []string{}

	// Try to extract parameters from various formats models might use
	paramPrefixes := []string{
		"Parameters: ",
		"Params: ",
		"Arguments: ",
		"Args: ",
		"with parameters: ",
		"with arguments: ",
	}

	// Track if we've found parameters
	foundParams := false

	// Check each possible parameter prefix
	for _, prefix := range paramPrefixes {
		paramsIdx := strings.Index(message, prefix)
		if paramsIdx != -1 {
			paramsLine := message[paramsIdx:]
			paramsLine = strings.Split(paramsLine, "\n")[0]
			paramsText := strings.TrimPrefix(paramsLine, prefix)

			// Parse space-separated key=value parameters
			if paramsText == "N/A" {
				continue
			}
			paramPairs := strings.Fields(paramsText)
			for _, pair := range paramPairs {
				// Check if the parameter contains <value> placeholder
				if strings.Contains(pair, "=<value>") || strings.Contains(pair, "=<VALUE>") {
					// Extract parameter name (part before =<value>)
					paramName := strings.Split(pair, "=")[0]
					// Warn that user should replace <value> with actual value
					fmt.Printf("\r\033[33m(warning) Replace %s=<value> with an actual value (e.g., %s=myvalue)\033[0m\n", paramName, paramName)
				}
				args = append(args, pair)
			}

			foundParams = true
			break
		}
	}

	// If no explicit parameters found but there are arguments in the tool line, use those
	if !foundParams && len(toolParts) > 1 {
		args = strings.Fields(toolParts[1])
	}

	// Extract the reasoning for display with flexible matching
	reasoningPrefixes := []string{
		"Reasoning: ",
		"Rationale: ",
		"Reason: ",
		"Why: ",
		"Explanation: ",
	}

	// Find reasoning with any of the prefixes
	var reasoningText string
	for _, prefix := range reasoningPrefixes {
		reasoningIdx := strings.Index(message, prefix)
		if reasoningIdx != -1 {
			reasoningLine := message[reasoningIdx:]
			reasoningText = strings.Split(reasoningLine, "\n")[0]
			reasoningText = strings.TrimPrefix(reasoningText, prefix)
			break
		}
	}

	// If we have reasoning text, print it
	if reasoningText != "" {
		// Print reasoning in magenta
		fmt.Printf("\r\033[35m(tool) %s\033[0m\n", reasoningText)
	} else {
		// If no explicit reasoning found, try to extract something useful from the message
		// Look for common phrases that might indicate reasoning
		phrases := []string{"I need to", "We should", "To solve this", "This will", "In order to"}
		for _, phrase := range phrases {
			idx := strings.Index(message, phrase)
			if idx != -1 {
				// Get the sentence containing this phrase
				start := idx
				for start > 0 && message[start-1] != '.' && message[start-1] != '\n' {
					start--
				}

				end := idx + len(phrase)
				for end < len(message) && message[end] != '.' && message[end] != '\n' {
					end++
				}
				if end < len(message) {
					end++ // Include the period
				}

				// Print the extracted reasoning
				fmt.Printf("\r\033[35m(tool) %s\033[0m\n", strings.TrimSpace(message[start:end]))
				break
			}
		}
	}

	// Extract the Action field with flexible prefixes
	action := "Iterate" // Default to Iterate as the safest default
	actionPrefixes := []string{
		"Action: ",
		"Next action: ",
		"Action type: ",
		"Should: ",
	}

	for _, prefix := range actionPrefixes {
		actionIdx := strings.Index(message, prefix)
		if actionIdx != -1 {
			actionLine := message[actionIdx:]
			actionText := strings.Split(actionLine, "\n")[0]
			action = strings.TrimPrefix(actionText, prefix)
			break
		}
	}

	// Normalize action to expected values
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "error" || action == "fail" {
		action = "Error"
	} else if action == "done" || action == "complete" || action == "finish" || action == "solved" || action == "completed" {
		action = "Solve"
	} else {
		action = "Iterate" // Default for unrecognized actions
	}

	// Extract the NextStep field with flexible prefixes
	nextStep := "" // Default empty next step
	nextStepPrefixes := []string{
		"NextStep: ",
		"Next step: ",
		"Next: ",
		"Then: ",
		"Following step: ",
	}

	for _, prefix := range nextStepPrefixes {
		nextStepIdx := strings.Index(message, prefix)
		if nextStepIdx != -1 {
			nextStepLine := message[nextStepIdx:]
			nextStepText := strings.Split(nextStepLine, "\n")[0]
			nextStep = strings.TrimPrefix(nextStepText, prefix)
			break
		}
	}

	// If no explicit next step found but we're iterating, try to infer one
	if nextStep == "" && action == "Iterate" {
		// Look for phrases that might indicate next steps
		phrases := []string{"next, ", "now I'll", "now I need to", "after this", "we should now"}
		for _, phrase := range phrases {
			idx := strings.Index(strings.ToLower(message), phrase)
			if idx != -1 {
				// Extract the sentence containing this phrase
				start := idx
				end := idx + len(phrase)
				// Find the end of this sentence or thought
				for end < len(message) && message[end] != '.' && message[end] != '\n' {
					end++
				}
				if end < len(message) {
					end++ // Include the period or newline
				}

				nextStep = strings.TrimSpace(message[start:end])
				break
			}
		}
	}

	// Extract the Plan field with flexible prefixes
	plan := "" // Default empty plan
	planPrefixes := []string{
		"Plan: ",
		"Overall plan: ",
		"Strategy: ",
		"Approach: ",
	}

	for _, prefix := range planPrefixes {
		planIdx := strings.Index(message, prefix)
		if planIdx != -1 {
			planLine := message[planIdx:]
			prefixLen := len(prefix)
			planEndIdx := strings.Index(planLine, "\n\n")
			if planEndIdx != -1 {
				plan = planLine[prefixLen:planEndIdx] // Skip prefix
			} else {
				// If no double newline, try to get what we can
				planText := strings.Split(planLine, "\n")[0]
				plan = strings.TrimPrefix(planText, prefix)
			}
			break
		}
	}

	// Extract the Progress field with flexible prefixes
	progress := "" // Default empty progress
	progressPrefixes := []string{
		"Progress: ",
		"Current progress: ",
		"Status: ",
		"Current step: ",
	}

	for _, prefix := range progressPrefixes {
		progressIdx := strings.Index(message, prefix)
		if progressIdx != -1 {
			progressLine := message[progressIdx:]
			prefixLen := len(prefix)
			progressEndIdx := strings.Index(progressLine, "\n\n")
			if progressEndIdx != -1 {
				progress = progressLine[prefixLen:progressEndIdx] // Skip prefix
			} else {
				// If no double newline, try to get what we can
				progressText := strings.Split(progressLine, "\n")[0]
				progress = strings.TrimPrefix(progressText, prefix)
			}
			break
		}
	}

	// Get step number if it's included in the progress
	stepNumber := 0
	if progress != "" {
		// Try to extract step number from progress text
		stepPattern := regexp.MustCompile(`Step\s+(\d+)`)
		matches := stepPattern.FindStringSubmatch(progress)
		if len(matches) >= 2 {
			stepNumber, _ = strconv.Atoi(matches[1])
		}
	}

	// Print plan and progress information if available
	if plan != "" {
		fmt.Printf("\r\033[36m(plan) %s\033[0m\n", plan)
	}

	if progress != "" {
		fmt.Printf("\r\033[36m(progress) %s\033[0m\n", progress)
	}

	// Create and return the tool
	return []*Tool{{
		Name:       toolName,
		Args:       args,
		Action:     action,
		NextStep:   nextStep,
		Plan:       plan,
		Progress:   progress,
		StepNumber: stepNumber,
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
	msg := fmt.Sprintf("%s\n## User Prompt\n\n%s\n----\n## Available Tools:\n%s",
		toolPrompt, userInput, toolList)
	// DEBUG fmt.Println(msg)
	return msg
}

// extractToolName extracts the tool name from a response string
func extractToolName(response string) string {
	// Check different possible prefixes
	/*
		fmt.Println("RESPONSE")
		fmt.Println(response)
		fmt.Println("RESPONSE")
	*/
	knownPrefixes := []string{
		"Selected Tool: ",
		"Tool: ",
		"ToolName: ",
		"Using tool: ",
		"Tool name: ",
		"I'll use: ",
	}

	for _, prefix := range knownPrefixes {
		startIdx := strings.Index(response, prefix)
		if startIdx != -1 {
			startIdx += len(prefix) // Skip the prefix

			endIdx := strings.Index(response[startIdx:], "\n")
			if endIdx == -1 {
				toolLine := response[startIdx:] // Get to the end if no newline found
				// If there's a space, just get the first word (the tool name)
				if spaceIdx := strings.Index(toolLine, " "); spaceIdx != -1 {
					return toolLine[:spaceIdx]
				}
				return toolLine
			}

			toolLine := response[startIdx : startIdx+endIdx]
			// If there's a space, just get the first word (the tool name)
			if spaceIdx := strings.Index(toolLine, " "); spaceIdx != -1 {
				return toolLine[:spaceIdx]
			}
			return toolLine
		}
	}

	// If no prefix found
	return ""
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

		// Include plan and progress information in the result if available
		if tool.Plan != "" || tool.Progress != "" || tool.Action != "" || tool.NextStep != "" {
			// Build a comprehensive result with plan and progress information
			metadata := []string{}

			if tool.Plan != "" {
				metadata = append(metadata, fmt.Sprintf("Plan: %s", tool.Plan))
			}

			if tool.Progress != "" {
				metadata = append(metadata, fmt.Sprintf("Progress: %s", tool.Progress))
			}

			if tool.Action != "" {
				metadata = append(metadata, fmt.Sprintf("Action: %s", tool.Action))
			}

			if tool.NextStep != "" {
				metadata = append(metadata, fmt.Sprintf("NextStep: %s", tool.NextStep))
			}

			// Join metadata with newlines and add to results
			results = append(results, "\n"+strings.Join(metadata, "\n"))
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

// ProcessToolExecution executes tool-based processing for the given input and REPL client
// This function handles the multi-step, context-aware processing of user input with tools
func ProcessToolExecution(input string, client *LLMClient, repl interface{}) (string, error) {
	// Type assertion to access REPL methods and fields
	replImpl, ok := repl.(*REPL)
	if !ok {
		// If type assertion fails, return an error
		return "", fmt.Errorf("invalid REPL implementation")
	}

	// Initialize state tracking variables
	contextHistory := []string{}
	stepCount := 0
	maxSteps := 10 // Add maximum steps to prevent infinite loops
	var overallPlan string
	var progress string
	var lastToolName string
	repeatCount := 0
	maxRepeats := 3 // Maximum number of times the same tool can be called consecutively

	for {
		stepCount++

		// Check if we've exceeded the maximum number of steps
		if stepCount > maxSteps {
			fmt.Printf("Exceeded maximum number of steps (%d). Breaking loop to prevent infinite execution.\n\r", maxSteps)
			break
		}
		// Construct input with context history
		toolinput := ProcessUserInput(input, repl)

		// Add context history to the input
		if len(contextHistory) > 0 {
			toolinput += "\n\n# Previous Steps and Results:\n"
			// Include last 3 steps to prevent context overflow
			startIdx := 0
			if len(contextHistory) > 3 {
				startIdx = len(contextHistory) - 3
			}
			for i := startIdx; i < len(contextHistory); i++ {
				toolinput += fmt.Sprintf("\n## Step %d:\n%s\n", i+1, contextHistory[i])
			}
		}

		// Include current step count
		toolinput += fmt.Sprintf("\n\n# Current Step: %d\n", stepCount)

		// Include overall plan if we have one
		if overallPlan != "" {
			toolinput += fmt.Sprintf("\n# Current Plan:\n%s\n", overallPlan)
		}

		// Include progress if we have it
		if progress != "" {
			toolinput += fmt.Sprintf("\n# Current Progress:\n%s\n", progress)
		}

		trick := "Be concise in your responses, follow the plan and only respond with verified information from the tool calls. Maintain context between steps."
		// Send message with streaming based on REPL settings
		messages := []Message{{"user", trick + toolinput}}
		response, err := client.SendMessage(messages, false)
		if err != nil {
			return "", fmt.Errorf("failed to get response for tools: %v", err)
		}

		// Handle the assistant's response based on logging settings
		if err == nil && response != "" {
			if replImpl.config.options.GetBool("debug") {
				fmt.Println("==============TOOLS FROM MESSAGE=================")
				fmt.Println(response)
				fmt.Println("==============TOOLS FROM MESSAGE=================")
			}

			// Extract Plan and Progress if present
			planIdx := strings.Index(response, "Plan: ")
			if planIdx != -1 {
				planLine := response[planIdx:]
				planEndIdx := strings.Index(planLine, "\n\n")
				if planEndIdx != -1 {
					overallPlan = planLine[6:planEndIdx] // Skip "Plan: "
				}
			}

			progressIdx := strings.Index(response, "Progress: ")
			if progressIdx != -1 {
				progressLine := response[progressIdx:]
				progressEndIdx := strings.Index(progressLine, "\n\n")
				if progressEndIdx != -1 {
					progress = progressLine[10:progressEndIdx] // Skip "Progress: "
				}
			}

			newres, err := executeToolsInMessage(response)
			if err != nil {
				errorMsg := fmt.Sprintf("Error executing tool: %v", err)
				contextHistory = append(contextHistory, errorMsg)
				input += "\n\n# ToolsError:\n" + err.Error()
				fmt.Printf("Error %v\n\r", err)
				// Continue with next iteration after error
				continue
			} else if newres != "" {
				// Check for Action and NextStep in the result
				actionIdx := strings.Index(newres, "\nAction: ")
				nextStepIdx := strings.Index(newres, "\nNextStep: ")

				var toolAction, nextStep string
				var toolResult string

				if actionIdx != -1 && nextStepIdx != -1 {
					// Extract the tool result (everything before Action)
					toolResult = newres[:actionIdx]

					// Extract Action value
					actionLine := newres[actionIdx+9:] // +9 to skip "\nAction: "
					actionEndIdx := strings.Index(actionLine, "\n")
					if actionEndIdx != -1 {
						toolAction = actionLine[:actionEndIdx]
					}

					// Extract NextStep value
					nextStepLine := newres[nextStepIdx+11:] // +11 to skip "\nNextStep: "
					nextStep = nextStepLine

					// Add the tool result to the context history and input
					contextEntry := fmt.Sprintf("Tool: %s\nResult: %s\nAction: %s\nNextStep: %s",
						extractToolName(response),
						toolResult,
						toolAction,
						nextStep)
					contextHistory = append(contextHistory, contextEntry)
					input += "\n\n# ToolsContext:\n" + strings.TrimSpace(toolResult)

					// Process based on Action type
					switch toolAction {
					case "Solve":
						// The tool solved the problem, no further action needed
						fmt.Printf("Tool solved the request: %s\n\r", nextStep)
						// Flag to exit the loop when solved
						goto exitLoop
					case "Error":
						// There was an error, add error context
						input += "\n\n# ToolsError:\n" + nextStep
						fmt.Printf("Tool error: %s\n\r", nextStep)
						// Continue with next iteration after error
						continue
					case "Iterate":
						// Need to iterate, add next step to input
						input += "\n\n# NextStep:\n" + nextStep + "\n----\n"
						fmt.Printf("Tool requires iteration: %s\n\r", nextStep)

						// Check for repeated tool calls
						currentToolName := extractToolName(response)
						fmt.Println("TOOLNAME ")
						fmt.Println(currentToolName)
						fmt.Println("TOOLNAME ")
						if currentToolName != "" && currentToolName == lastToolName {
							repeatCount++
							if repeatCount >= maxRepeats {
								fmt.Printf("Same tool called %d times in a row. Breaking loop to prevent infinite execution.\n\r", repeatCount)
								goto exitLoop
							}
						} else {
							// Reset counter for new tool
							repeatCount = 0
							lastToolName = currentToolName
						}

						continue
					default:
						// Handle unexpected action types by continuing iteration
						fmt.Printf("Unknown action type: %s\n\r", toolAction)
						continue
					}
				} else {
					// No Action/NextStep found, process as before but continue iterating
					contextHistory = append(contextHistory, fmt.Sprintf("Result: %s", strings.TrimSpace(newres)))
					input += "\n\n# ToolsContext:\n" + strings.TrimSpace(newres)

					// Continue with next iteration for models that don't properly format output
					fmt.Printf("Tool result received, continuing iteration\n\r")
					continue
				}
			} else {
				// No result received, but continue iterating
				fmt.Printf("No tool result, continuing iteration\n\r")
				continue
			}
		exitLoop:
			// Break only reaches here if explicitly breaking from the loop or via goto
			break
		} else {
			contextHistory = append(contextHistory, "Error: Could not run tools")
			input += "\n----\n# ToolsContext:\nWe could not run the tools"
		}
	}

	if replImpl.config.options.GetBool("debug") {
		fmt.Println("-------------------")
		fmt.Println(input)
		fmt.Println("-------------------")
	}

	return input, nil
}
