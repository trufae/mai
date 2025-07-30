package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"fmt"
	"os"
	"os/exec"
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


// Define an enum-like type
type Format int

const (
	Markdown Format = iota
	JSON
	Quiet
)

// GetAvailableTools runs the 'mai-tool list' command and returns the output as a string
func GetAvailableTools(f Format) (string, error) {
	var cmd *exec.Cmd
	switch f {
	case Quiet:
		cmd = exec.Command("mai-tool", "-q", "list")
	case JSON:
		cmd = exec.Command("mai-tool", "-j", "list")
	case Markdown:
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

func getToolsFromJson(message string) ([]*Tool, error) {
	return []*Tool{}, nil
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
func (repl *REPL)getToolPrompt(foo string) (string, error) {
	// Get prompt directory path
	toolPromptPath, err := repl.resolvePromptPath(foo)
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
	msg := fmt.Sprintf("%s\n<prompt>\n%s\n</prompt>\n<tools>\n%s\n</tools>",
		toolPrompt, userInput, toolList)
	// DEBUG fmt.Println(msg)
	return msg
}


type ToolCall struct {
	Name string `json:"name"`
	Parameters map[string]interface{} `json:"parameters"`
	Reasoning string `json:"reasoning"`
}
type PlanResponse struct {
	Plan          []string `json:"plan"`
	PlanIndex     int `json:"plan_index"`
	Progress      string   `json:"progress"`
	NextStep      string   `json:"next_step"`
	Action        string   `json:"action"`
	ToolRequired  bool     `json:"tool_required"`
	SelectedTool  ToolCall `json:"selected_tool"`
}

func mapToArray(m map[string]interface{}) []string {
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

func extractJSONBlock(text string) (string, error) {
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		return matches[1], nil
	}
	re2 := regexp.MustCompile("(?s)```\\s*{(.*?)\\s*```")
	matches2 := re2.FindStringSubmatch(text)
	if len(matches2) >= 2 {
		return "{" + matches2[1], nil
	}
	return text, nil
}

func (r *REPL)toolStep(toolPrompt string, input string, toolList string) (PlanResponse, error) {
	msg := buildMessageWithTools(toolPrompt, input, toolList)
	query := msg
	// trick + toolinput}}
	messages := []Message{{"user", query}}
	responseText, err := r.currentClient.SendMessage(messages, false)
	if err != nil {
		return PlanResponse{}, fmt.Errorf("failed to get response for tools: %v", err)
	}
// fmt.Println(responseText)
	responseJson, err := extractJSONBlock(responseText)
	var response PlanResponse
	err2 := json.Unmarshal([]byte(responseJson), &response)
	return response, err2
}

func (r *REPL) QueryWithTools(input string) (string, error) {
	toolPrompt, err := r.getToolPrompt("tool.md")
	if err != nil {
		// If can't get the tool prompt, return input unchanged
		return input, err
	}

	toolList, err := GetAvailableTools(JSON)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	stepCount := 0
	for {
		stepCount++
		step, err := r.toolStep(toolPrompt, input, toolList)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			break
		}
		planString := "## Plan\n\n"
		fmt.Printf("\033[32m(progress) %s\r\n", step.Progress)
		fmt.Printf("\033[32m(next) %s\r\n", step.NextStep)
		i := 0
		for _, s := range step.Plan {
			if i == step.PlanIndex {
				fmt.Print (" >> ")
			} else {
				fmt.Print (" -- ")
			}
			fmt.Printf ("%s\r\n", s)
			planString += fmt.Sprintf ("%d. %s\n", i, s)
			i++
		}
		fmt.Printf("\033[0m")

		if !step.ToolRequired {
			break
		}
		toolName := strings.ReplaceAll(step.SelectedTool.Name, ".", "/")
		tool := &Tool{
			Name: toolName,
			Args: mapToArray(step.SelectedTool.Parameters),
		}
		fmt.Printf ("(tool) %s\r\n", tool)
		fmt.Printf ("(reason) %s\r\n", step.SelectedTool.Reasoning)
		result, err := callTool(tool)
		if err != nil {
			input += fmt.Sprintf ("\nTool execution failed: %s\n\n", tool)
			continue
			// return "", err
		}
		/*
		fmt.Println ("<calltoolResult>")
		fmt.Println (result)
		fmt.Println ("</calltoolResult>")
		*/
		// results = append(results, result)
		toolResponse := fmt.Sprintf("\n\n## Context\n\nToolName: %s\nResponse: %s\n", tool.Name, result)
		// fmt.Println (toolResponse)
		input += toolResponse
		// input += planString + toolResponse
	}
	return input, nil
}

