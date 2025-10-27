package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/trufae/mai/src/repl/llm"
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

// OpenAITool represents a tool in OpenAI's tool calling format
type OpenAITool struct {
	Type     string             `json:"type"`
	Function OpenAIToolFunction `json:"function"`
}

// OpenAIToolFunction represents the function part of an OpenAI tool
type OpenAIToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Define an enum-like type
type Format int

const (
	Markdown Format = iota
	JSON
	Quiet
	XML
	Simple
)

// parseToolFormat parses the tool format string and returns the appropriate Format enum
func parseToolFormat(formatStr string) Format {
	switch strings.ToLower(strings.TrimSpace(formatStr)) {
	case "xml":
		return XML
	case "markdown":
		return Markdown
	case "simple":
		return Simple
	case "quiet":
		return Quiet
	case "json":
		return JSON
	default:
		return Simple // default fallback
	}
}

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
	case XML:
		cmd = exec.Command("mai-tool", "-x", "list")
	case Simple:
		cmd = exec.Command("mai-tool", "-s", "list")
	}
	// cmd = exec.Command("mai-tool", "-j", "list")
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	return out.String(), err
}

// GetAvailableToolsWithConfig gets available tools using the specified config
func GetAvailableToolsWithConfig(configOptions ConfigOptions, defaultFormat Format) (string, error) {
	toolFormat := configOptions.Get("mcp.toolformat")
	if toolFormat == "" || toolFormat == "?" {
		return GetAvailableTools(defaultFormat)
	}
	format := parseToolFormat(toolFormat)
	return GetAvailableTools(format)
}

// GetOpenAITools gets available tools in OpenAI tool calling format
func GetOpenAITools() ([]OpenAITool, error) {
	// Get tools in JSON format from MCP
	jsonStr, err := GetAvailableTools(JSON)
	if err != nil {
		return nil, err
	}

	// Parse the MCP JSON format
	var mcpTools map[string][]map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &mcpTools); err != nil {
		return nil, err
	}

	var openAITools []OpenAITool
	for _, tools := range mcpTools {
		for _, tool := range tools {
			name, ok := tool["name"].(string)
			if !ok {
				continue
			}
			description, _ := tool["description"].(string)
			inputSchema, ok := tool["inputSchema"].(map[string]interface{})
			if !ok {
				continue
			}

			openAITool := OpenAITool{
				Type: "function",
				Function: OpenAIToolFunction{
					Name:        name,
					Description: description,
					Parameters:  inputSchema,
				},
			}
			openAITools = append(openAITools, openAITool)
		}
	}

	return openAITools, nil
}

// callTool executes a specified tool with provided arguments and returns the output
func callTool(tool *Tool, debug bool, format string, timeoutSeconds int) (string, error) {
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

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmdArgs := append([]string{"call", toolName}, safeArgs...)
	if format == "json" {
		cmdArgs = append([]string{"-j"}, cmdArgs...)
	} else if format == "xml" {
		cmdArgs = append([]string{"-x"}, cmdArgs...)
	}

	// Add debug flag if enabled
	/*
		if debug {
			cmdArgs = append([]string{"-d"}, cmdArgs...)
		}
	*/

	// Set a timeout for the command execution
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	// Set the command context with timeout
	cmd := exec.CommandContext(timeoutCtx, "mai-tool", cmdArgs...)
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("tool execution timed out after %d seconds: %s", timeoutSeconds, tool.Name)
		}
		return "", fmt.Errorf("error executing tool %s: %v: %s", tool.Name, err, stderr.String())
	}

	result := out.String()
	// Provide some feedback if the result is empty
	if strings.TrimSpace(result) == "" {
		return "", nil // fmt.Errorf("tool %s returned empty result", tool.Name)
	}

	return result, nil
}

// ExecuteTool runs a specified tool with provided arguments and returns the output
// Kept for backward compatibility
func (r *REPL) ExecuteTool(toolName string, args ...string) (string, error) {
	tool := &Tool{
		Name: toolName,
		Args: args,
	}
	toolFormat := r.configOptions.Get("mcp.toolformat")
	return callTool(tool, false, toolFormat, 60)
}

type PlanResponse struct {
	Plan             []string `json:"plan"`
	CurrentPlanIndex int      `json:"current_plan_index"`
	Progress         string   `json:"progress"`
	NextStep         string   `json:"next_step"`
	Action           string   `json:"action"`
	ToolRequired     bool     `json:"tool_required"`
	Reasoning        string   `json:"reasoning"`
	Tool             string   `json:"tool,omitempty"`
	// ToolParams     map[string]interface{} `json:"tool_params,omitempty"`
	ToolParams interface{} `json:"tool_params,omitempty"`
}

// extractJSONBlock locates the first balanced JSON object in text (or fenced JSON)
// and returns it plus any remaining tail text.
func extractJSONBlock(text string) (string, string) {
	// Attempt fenced JSON block: ```json ... ```
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		content := matches[1]
		// Trim any prefix before the first '{' to remove titles or comments
		if idx := strings.Index(content, "{"); idx >= 0 {
			content = content[idx:]
		}
		return content, ""
	}
	// Attempt fenced JSON-like block: ``` { ... ```
	re2 := regexp.MustCompile("(?s)```\\s*{(.*?)\\s*```")
	matches2 := re2.FindStringSubmatch(text)
	if len(matches2) >= 2 {
		content := "{" + matches2[1]
		// Trim any prefix before the first '{' to remove titles or comments
		if idx := strings.Index(content, "{"); idx >= 0 {
			content = content[idx:]
		}
		return content, ""
	}
	// Find first '{'
	start := strings.Index(text, "{")
	if start < 0 {
		return "", text
	}
	// Scan for balanced braces
	in := text[start:]
	depth := 0
	endIdx := -1
	for i, r := range in {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				endIdx = i
				break
			}
		}
		if endIdx != -1 {
			break
		}
	}
	if endIdx != -1 {
		// JSON block is in[:endIdx+1], remainder follows
		return in[:endIdx+1], strings.TrimSpace(in[endIdx+1:])
	}
	// No balanced end; return from first '{' to end
	return in, ""
}
func (t *Tool) ToString() string {
	args := strings.Join(t.Args, " ")
	return fmt.Sprintf("%s %s", t.Name, args)
}

// ReactLoop selects between schema/grammar-guided tool loop and
// markdown-based loop based on the `mcp.grammar` option. This provides a
// single entry point for tool-calling behavior.
func (r *REPL) ReactLoop(messages []llm.Message, input string) (string, error) {
	if r.configOptions.GetBool("mcp.grammar") {
		return r.ReactJson(messages, input)
	}
	return r.ReactText(messages, input)
}

// NativeToolLoop handles native tool calling protocol
func (r *REPL) NativeToolLoop(messages []llm.Message, input string) (string, error) {
	// Get available tools in OpenAI format
	tools, err := GetOpenAITools()
	if err != nil {
		return input, fmt.Errorf("failed to get tools: %v", err)
	}

	// For now, just modify the input to include tool instructions
	toolPrompt := "You have access to the following tools:\n"
	for _, tool := range tools {
		toolPrompt += fmt.Sprintf("- %s: %s\n", tool.Function.Name, tool.Function.Description)
	}
	toolPrompt += "\nWhen you need to use a tool, use the tool calling format. After receiving tool results, provide your final answer."

	modifiedInput := toolPrompt + "\n\n" + input

	return modifiedInput, nil
}
