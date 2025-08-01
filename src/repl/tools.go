package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
func (repl *REPL) getToolPrompt(foo string) (string, error) {
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
func buildMessageWithTools(toolPrompt string, userInput string, ctx string, toolList string) string {
	return fmt.Sprintf("%s\n<prompt>\n%s\n</prompt>\n<context>%s</context>\n<tools>\n%s\n</tools>",
		toolPrompt, userInput, ctx, toolList)
}

type PlanResponse struct {
	Plan         []string               `json:"plan"`
	PlanIndex    int                    `json:"current_plan_index"`
	Progress     string                 `json:"progress"`
	NextStep     string                 `json:"next_step"`
	Action       string                 `json:"action"`
	ToolRequired bool                   `json:"tool_required"`
	Reasoning    string                 `json:"reasoning"`
	SelectedTool string                 `json:"tool,omitempty"`
	ToolArgs     map[string]interface{} `json:"tool_params,omitempty"`
}

func mapToArray(m map[string]interface{}) []string {
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

func extractJSONBlock(text string) (string, string) {
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		return matches[1], ""
	}
	re2 := regexp.MustCompile("(?s)```\\s*{(.*?)\\s*```")
	matches2 := re2.FindStringSubmatch(text)
	if len(matches2) >= 2 {
		return "{" + matches2[1], ""
	}
	nojson := strings.Index(text, "`{")
	if nojson != -1 {
		return "", text
	}
	start := strings.Index(text, "{")
	if start != -1 {
		newText := text[start:]
		end := strings.LastIndex(newText, "\n}")
		if end != -1 {
			return newText[:end+2], newText[end+2:]
		}
		end = strings.Index(newText, "```")
		if end != -1 {
			return newText[:end], ""
		}
		end = strings.LastIndex(newText, "}")
		if end != -1 {
			return newText[:end+1], ""
		}
		return newText, ""
	}
	return "", text
}

func stripJSONComments(input string) string {
	lines := strings.Split(input, "\n")
	var cleaned []string
	commentRegex := regexp.MustCompile(`//.*$`)
	comment2Regex := regexp.MustCompile(`^#.*$`)
	for _, line := range lines {
		clean := commentRegex.ReplaceAllString(line, "")
		clean = comment2Regex.ReplaceAllString(clean, "")
		cleaned = append(cleaned, clean)
	}
	return strings.Join(cleaned, "\n")
}

func (r *REPL) toolStep(toolPrompt string, input string, ctx string, toolList string) (PlanResponse, string, error) {
	query := buildMessageWithTools(toolPrompt, input, ctx, toolList)
	/*
		fmt.Println("==========================")
		fmt.Println(query)
		fmt.Println("==========================")
	*/
	messages := []Message{{"user", query}}
	responseText, err := r.currentClient.SendMessage(messages, false)
	if err != nil {
		return PlanResponse{}, "", fmt.Errorf("failed to get response for tools: %v", err)
	}
	// fmt.Println(responseText)
	responseJson, explainText := extractJSONBlock(responseText)
	responseJson = stripJSONComments(responseJson)
	/*
		fmt.Println("{{ EXPLAIN")
		fmt.Println(explainText)
		fmt.Println("}} EXPLAIN")
	*/
	var response PlanResponse
	if responseJson != "" {
		err2 := json.Unmarshal([]byte(responseJson), &response)
		if err2 != nil {
			fmt.Println("{{ JSONBLOCK")
			fmt.Println(responseJson)
			fmt.Println("}} JSONBLOCK")
		}
		// response.NextStep += "<think>" + explainText + "</think>"
		fmt.Println(response.NextStep)
		return response, explainText, err2
	}
	return response, explainText, nil
}

func (t *Tool) ToString() string {
	args := strings.Join(t.Args, " ")
	return fmt.Sprintf("%s %s", t.Name, args)
}

func (r *REPL) QueryWithTools(input string) (string, error) {
	showPlan := true
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
	context := ""
	stepCount := 0
	reasoning := ""
	clearScreen := true
	for {
		stepCount++
		step, expl, err := r.toolStep(toolPrompt, input, context, toolList)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			if strings.Contains(err.Error(), "cancel") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue
		}
		if clearScreen {
			fmt.Print("\033[2J\033[H\033[33m>>> " + input + "\r\n")
			fmt.Printf("Context: %d bytes\r\n", len(context))
		}
		fmt.Printf("\033[0m\n%s\r\n", step.Progress)
		fmt.Printf("\r\n%s\r\n\r\n", step.Reasoning)
		if showPlan {
			planString := "## Plan\n\n\r"
			i := 0
			for _, s := range step.Plan {
				if i == step.PlanIndex {
					fmt.Print("\033[36m >> ")
				} else {
					fmt.Print("\033[32m -- ")
				}
				fmt.Printf("%s\r\n", s)
				planString += fmt.Sprintf("%d. %s\n", i, s)
				i++
			}
		} else {
			fmt.Printf("\033[0m\r\n%s\r\n", step.NextStep)
		}
		fmt.Printf("\033[0m")

		if !step.ToolRequired {
			if expl != "" {
				reasoning += "\n\n## Reasoning\n\n" + expl
			}
			break
		}
		toolName := strings.ReplaceAll(step.SelectedTool, ".", "/")
		tool := &Tool{
			Name: toolName,
			Args: mapToArray(step.ToolArgs),
		}
		fmt.Printf("\r\n\033[0mUsing Tool: %s\r\n\033[0m", tool.ToString())
		result, err := callTool(tool)
		if err != nil {
			input += fmt.Sprintf("\nTool %s execution failed: %s\n\n", tool.ToString(), err.Error())
			continue
			// return "", err
		}
		/*
			fmt.Println ("<calltoolResult>")
			fmt.Println (result)
			fmt.Println ("</calltoolResult>")
		*/
		// results = append(results, result)
		// toolResponse := fmt.Sprintf("\n\n## Step %d Tool Response\n\n**Reasoning**: %s\n**Next Step**: %s\n**ToolName**: %s\n**Contents**: %s\n", stepCount, step.Reasoning, step.NextStep, tool.Name, result)
		toolResponse := fmt.Sprintf("\n\n## Step %d Tool Response\n\n**Reasoning**: %s\n**ToolName**: %s\n**Contents**:\n\n```\n%s\n```\n\n", stepCount, step.NextStep, tool.Name, result)
		toolResponse += fmt.Sprintf("reason: %s\n", step.Reasoning)
		// fmt.Println (toolResponse)
		if expl != "" {
			context += "\n\n## Context\n\n" + expl
			reasoning += "\n\n## Reasoning\n\n" + expl
		}
		reasoning += "- " + step.Progress + "\n"
		context += toolResponse
		// input += planString + toolResponse
	}
	if reasoning != "" {
		reasoning = "<think>\n" + reasoning + "</think>\n"
	}
	fmt.Println(strings.ReplaceAll(reasoning, "\n", "\r\n"))
	return input + context, nil
	// return input + context + reasoning, nil
}
