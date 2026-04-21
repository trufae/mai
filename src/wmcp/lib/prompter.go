package wmcplib

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ErrPromptCancelled is returned by ModifyToolRequest when the user aborts.
var ErrPromptCancelled = errors.New("prompt cancelled")

// ToolModificationInput is what the user typed when asked to fix up a tool
// name / arguments. Both fields may be empty; when Name is empty callers
// should keep the current tool name, when Arguments is nil callers should
// keep the current arguments.
type ToolModificationInput struct {
	Name      string
	Arguments map[string]interface{}
}

// Prompter is the permission/confirmation surface the MCP service uses when
// it cannot decide on its own whether a tool or prompt call should proceed.
//
// Implementations live outside the library so the same service can be
// driven from the mai-wmcp CLI (stdin-based prompts), mai-repl's TUI, or
// any future transport without any changes to the core logic.
type Prompter interface {
	// AskToolExecution runs when a tool invocation requires human approval.
	AskToolExecution(toolName, paramsJSON string) YoloDecision
	// AskPromptExecution runs when a prompts/get call requires approval.
	AskPromptExecution(promptName, argsJSON string) PromptDecision
	// AskToolNotFound runs when a requested tool doesn't exist on any server.
	AskToolNotFound(toolName string) YoloDecision
	// ModifyToolRequest lets the user adjust the tool call before it runs.
	ModifyToolRequest(current *CallToolParams) (*CallToolParams, error)
	// ReadCustomResponse fetches a free-form reply the library should return
	// to the model in place of the real tool output.
	ReadCustomResponse(message string) (string, error)
}

// StdinPrompter is the default Prompter used by the mai-wmcp CLI. It writes
// its menus to stdout and reads the user's answer from stdin.
type StdinPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

// NewStdinPrompter wires a StdinPrompter to os.Stdin / os.Stdout.
func NewStdinPrompter() *StdinPrompter {
	return &StdinPrompter{in: bufio.NewReader(os.Stdin), out: os.Stdout}
}

func (p *StdinPrompter) readLine() string {
	line, _ := p.in.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line))
}

func (p *StdinPrompter) readRaw() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// AskToolExecution implements Prompter.
func (p *StdinPrompter) AskToolExecution(toolName, paramsJSON string) YoloDecision {
	fmt.Fprintf(p.out, "\n===== TOOL EXECUTION CONFIRMATION =====\n")
	fmt.Fprintf(p.out, "Tool: %s\n", toolName)
	fmt.Fprintf(p.out, "Parameters: %s\n\n", paramsJSON)
	fmt.Fprintf(p.out, "Options:\n")
	fmt.Fprintf(p.out, "[a] Approve execution\n")
	fmt.Fprintf(p.out, "[r] Reject execution\n")
	fmt.Fprintf(p.out, "[t] Permit this tool forever\n")
	fmt.Fprintf(p.out, "[p] Permit this tool with these parameters forever\n")
	fmt.Fprintf(p.out, "[x] Reject this tool forever\n")
	fmt.Fprintf(p.out, "[y] Approve all tools forever (Yolo mode)\n")
	fmt.Fprintf(p.out, "[m] Modify tool name/parameters and run\n")
	fmt.Fprintf(p.out, "[c] Provide custom response text\n")
	fmt.Fprintf(p.out, "\nYour decision: ")

	switch p.readLine() {
	case "a":
		return YoloApprove
	case "r":
		return YoloReject
	case "t":
		return YoloPermitToolForever
	case "p":
		return YoloPermitToolWithParamsForever
	case "x":
		return YoloRejectForever
	case "y":
		return YoloPermitAllToolsForever
	case "m":
		return YoloModify
	case "c":
		return YoloCustomToolResponse
	default:
		fmt.Fprintln(p.out, "Invalid option, defaulting to reject")
		return YoloReject
	}
}

// AskPromptExecution implements Prompter.
func (p *StdinPrompter) AskPromptExecution(promptName, argsJSON string) PromptDecision {
	fmt.Fprintf(p.out, "\n===== PROMPT EXECUTION CONFIRMATION =====\n")
	fmt.Fprintf(p.out, "Prompt: %s\n", promptName)
	fmt.Fprintf(p.out, "Arguments: %s\n\n", argsJSON)
	fmt.Fprintf(p.out, "Options:\n")
	fmt.Fprintf(p.out, "[a] Approve execution\n")
	fmt.Fprintf(p.out, "[r] Reject execution\n")
	fmt.Fprintf(p.out, "[p] Permit this prompt forever\n")
	fmt.Fprintf(p.out, "[g] Permit this prompt with these arguments forever\n")
	fmt.Fprintf(p.out, "[x] Reject this prompt forever\n")
	fmt.Fprintf(p.out, "[y] Approve all prompts forever (Yolo mode)\n")
	fmt.Fprintf(p.out, "[c] Write your custom prompt in response\n")
	fmt.Fprintf(p.out, "[l] Get a list of the available prompts\n")
	fmt.Fprintf(p.out, "\nYour decision: ")

	switch p.readLine() {
	case "a":
		return PromptApprove
	case "r":
		return PromptReject
	case "p":
		return PromptPermitPromptForever
	case "g":
		return PromptPermitPromptWithArgsForever
	case "x":
		return PromptRejectForever
	case "y":
		return PromptPermitAllPromptsForever
	case "c":
		return PromptCustom
	case "l":
		return PromptList
	default:
		fmt.Fprintln(p.out, "Invalid option, defaulting to reject")
		return PromptReject
	}
}

// AskToolNotFound implements Prompter.
func (p *StdinPrompter) AskToolNotFound(toolName string) YoloDecision {
	fmt.Fprintf(p.out, "\n===== TOOL NOT FOUND =====\n")
	fmt.Fprintf(p.out, "Tool '%s' does not exist.\n\n", toolName)
	fmt.Fprintf(p.out, "Options:\n")
	fmt.Fprintf(p.out, "[e] Respond that the tool doesn't exist\n")
	fmt.Fprintf(p.out, "[c] Let me enter a custom response\n")
	fmt.Fprintf(p.out, "[s] Show available tools and let me adjust the request\n")
	fmt.Fprintf(p.out, "[g] Respond with a message to guide the model\n")
	fmt.Fprintf(p.out, "[y] Always respond that tools don't exist (yolo mode)\n")
	fmt.Fprintf(p.out, "\nYour decision: ")

	switch p.readLine() {
	case "e":
		return YoloToolNotFound
	case "c":
		return YoloCustomResponse
	case "s":
		return YoloModify
	case "g":
		return YoloGuideModel
	case "y":
		return YoloAlwaysRespondToolNotFound
	default:
		fmt.Fprintln(p.out, "Invalid option, defaulting to tool not found")
		return YoloToolNotFound
	}
}

// ModifyToolRequest implements Prompter. The accepted syntax is identical
// to the previous in-process prompt: either a JSON object or
// "toolname key=value key2=value".
func (p *StdinPrompter) ModifyToolRequest(current *CallToolParams) (*CallToolParams, error) {
	fmt.Fprintf(p.out, "\nEnter new tool name and arguments.\n")
	fmt.Fprintf(p.out, "Simple: <toolname> key=value key2=value\n")
	fmt.Fprintf(p.out, "Or JSON (must start with '{'): {\"name\":\"tool\", \"arguments\":{...}}\n")
	fmt.Fprintf(p.out, "Type 'cancel' to abort.\n")
	fmt.Fprintf(p.out, "Input: ")

	line, err := p.readRaw()
	if err != nil {
		return nil, err
	}
	return ParseToolModification(line, current)
}

// ReadCustomResponse implements Prompter.
func (p *StdinPrompter) ReadCustomResponse(message string) (string, error) {
	if message != "" {
		fmt.Fprint(p.out, message)
	} else {
		fmt.Fprint(p.out, "Enter your custom response: ")
	}
	return p.readRaw()
}

// ParseToolModification parses the textual syntax used by the default tool
// modification prompt so alternative Prompter implementations can reuse it.
func ParseToolModification(line string, current *CallToolParams) (*CallToolParams, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty input")
	}
	if strings.EqualFold(line, "cancel") {
		return nil, ErrPromptCancelled
	}

	if strings.HasPrefix(line, "{") {
		var parsed struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		if parsed.Name == "" && current != nil {
			parsed.Name = current.Name
		}
		if parsed.Arguments == nil && current != nil {
			parsed.Arguments = current.Arguments
		}
		return &CallToolParams{Name: parsed.Name, Arguments: parsed.Arguments}, nil
	}

	toks := strings.Fields(line)
	if len(toks) == 0 {
		return nil, fmt.Errorf("invalid input")
	}
	newName := toks[0]
	newArgs := make(map[string]interface{})
	if current != nil {
		for k, v := range current.Arguments {
			newArgs[k] = v
		}
	}
	for _, tok := range toks[1:] {
		if !strings.Contains(tok, "=") {
			return nil, fmt.Errorf("invalid parameter '%s': expected format name=value", tok)
		}
		parts := strings.SplitN(tok, "=", 2)
		k := parts[0]
		v := parts[1]
		if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
			var vv interface{}
			if err := json.Unmarshal([]byte(v), &vv); err == nil {
				newArgs[k] = vv
				continue
			}
		}
		if num, err := strconv.ParseFloat(v, 64); err == nil {
			newArgs[k] = num
			continue
		}
		if b, err := strconv.ParseBool(v); err == nil {
			newArgs[k] = b
			continue
		}
		newArgs[k] = v
	}
	return &CallToolParams{Name: newName, Arguments: newArgs}, nil
}
