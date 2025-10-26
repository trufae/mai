package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var errToolModificationCancelled = errors.New("tool modification cancelled")

// checkToolPermission checks if a tool is allowed to run based on stored permissions
func (s *MCPService) checkToolPermission(toolName string, paramsJSON string) bool {
	s.toolPermsLock.RLock()
	defer s.toolPermsLock.RUnlock()

	// Check if all tools are approved globally
	if perm, exists := s.toolPerms["y"]; exists && perm.Approved {
		return true
	}

	// Check exact tool+params match
	key := toolName + "#" + paramsJSON
	if perm, exists := s.toolPerms[key]; exists {
		return perm.Approved
	}

	// Check tool-only match
	if perm, exists := s.toolPerms[toolName]; exists {
		return perm.Approved
	}

	// No permission record found
	return false
}

// storeToolPermission stores a tool permission decision
func (s *MCPService) storeToolPermission(toolName string, paramsJSON string, decision YoloDecision) {
	s.toolPermsLock.Lock()
	defer s.toolPermsLock.Unlock()

	switch decision {
	case YoloPermitToolForever:
		s.toolPerms[toolName] = ToolPermission{
			ToolName: toolName,
			Approved: true,
		}
	case YoloPermitToolWithParamsForever:
		key := toolName + "#" + paramsJSON
		s.toolPerms[key] = ToolPermission{
			ToolName:   toolName,
			Parameters: paramsJSON,
			Approved:   true,
		}
	case YoloRejectForever:
		s.toolPerms[toolName] = ToolPermission{
			ToolName: toolName,
			Approved: false,
		}
	case YoloPermitAllToolsForever:
		// Also enable YOLO mode for future requests
		// Special key for approving all tools
		s.toolPerms["y"] = ToolPermission{
			ToolName: "y",
			Approved: true,
		}
	}
}

// checkPromptPermission checks if a prompt is allowed to run based on stored permissions
func (s *MCPService) checkPromptPermission(promptName string, argsJSON string) bool {
	s.promptPermsLock.RLock()
	defer s.promptPermsLock.RUnlock()

	// Check if all prompts are approved globally
	if perm, exists := s.promptPerms["y"]; exists && perm.Approved {
		return true
	}

	// Check exact prompt+args match
	key := promptName + "#" + argsJSON
	if perm, exists := s.promptPerms[key]; exists {
		return perm.Approved
	}

	// Check prompt-only match
	if perm, exists := s.promptPerms[promptName]; exists {
		return perm.Approved
	}

	// No permission record found
	return false
}

// storePromptPermission stores a prompt permission decision
func (s *MCPService) storePromptPermission(promptName, argsJSON string, decision PromptDecision) {
	s.promptPermsLock.Lock()
	defer s.promptPermsLock.Unlock()

	switch decision {
	case PromptPermitPromptForever:
		s.promptPerms[promptName] = PromptPermission{
			PromptName: promptName,
			Approved:   true,
		}
	case PromptPermitPromptWithArgsForever:
		key := promptName + "#" + argsJSON
		s.promptPerms[key] = PromptPermission{
			PromptName: promptName,
			Arguments:  argsJSON,
			Approved:   true,
		}
	case PromptRejectForever:
		s.promptPerms[promptName] = PromptPermission{
			PromptName: promptName,
			Approved:   false,
		}
	case PromptPermitAllPromptsForever:
		// Also enable YOLO mode for future requests
		// Special key for approving all prompts
		s.promptPerms["y"] = PromptPermission{
			PromptName: "y",
			Approved:   true,
		}
	}
}

// promptToolNotFoundDecision prompts the user when a tool doesn't exist
func (s *MCPService) promptToolNotFoundDecision(toolName string) YoloDecision {
	if s.nonInteractive || s.yoloMode {
		// In non-interactive mode or yolo mode, just return tool not found
		return YoloToolNotFound
	}

	fmt.Printf("\n===== TOOL NOT FOUND =====\n")
	fmt.Printf("Tool '%s' does not exist.\n\n", toolName)
	fmt.Printf("Options:\n")
	fmt.Printf("[e] Respond that the tool doesn't exist\n")
	fmt.Printf("[c] Let me enter a custom response\n")
	fmt.Printf("[s] Show available tools and let me adjust the request\n")
	fmt.Printf("[g] Respond with a message to guide the model\n")
	fmt.Printf("[y] Always respond that tools don't exist (yolo mode)\n")
	fmt.Printf("\nYour decision: ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
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
		fmt.Println("Invalid option, defaulting to tool not found")
		return YoloToolNotFound
	}
}

// promptYoloDecision prompts the user for a yolo decision on tool execution
func (s *MCPService) promptYoloDecision(toolName string, paramsJSON string) YoloDecision {
	fmt.Printf("\n===== TOOL EXECUTION CONFIRMATION =====\n")
	fmt.Printf("Tool: %s\n", toolName)
	fmt.Printf("Parameters: %s\n\n", paramsJSON)
	fmt.Printf("Options:\n")
	fmt.Printf("[a] Approve execution\n")
	fmt.Printf("[r] Reject execution\n")
	fmt.Printf("[t] Permit this tool forever\n")
	fmt.Printf("[p] Permit this tool with these parameters forever\n")
	fmt.Printf("[x] Reject this tool forever\n")
	fmt.Printf("[y] Approve all tools forever (Yolo mode)\n")
	fmt.Printf("[m] Modify tool name/parameters and run\n")
	fmt.Printf("[c] Provide custom response text\n")
	fmt.Printf("\nYour decision: ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
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
		fmt.Println("Invalid option, defaulting to reject")
		return YoloReject
	}
}

// promptPromptDecision prompts the user for a prompt decision on prompt execution
func (s *MCPService) promptPromptDecision(promptName string, argsJSON string) PromptDecision {
	fmt.Printf("\n===== PROMPT EXECUTION CONFIRMATION =====\n")
	fmt.Printf("Prompt: %s\n", promptName)
	fmt.Printf("Arguments: %s\n\n", argsJSON)
	fmt.Printf("Options:\n")
	fmt.Printf("[a] Approve execution\n")
	fmt.Printf("[r] Reject execution\n")
	fmt.Printf("[p] Permit this prompt forever\n")
	fmt.Printf("[g] Permit this prompt with these arguments forever\n")
	fmt.Printf("[x] Reject this prompt forever\n")
	fmt.Printf("[y] Approve all prompts forever (Yolo mode)\n")
	fmt.Printf("[c] Write your custom prompt in response\n")
	fmt.Printf("[l] Get a list of the available prompts\n")
	fmt.Printf("\nYour decision: ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
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
		fmt.Println("Invalid option, defaulting to reject")
		return PromptReject
	}
}

// promptModifyTool prompts the user to modify the tool name and arguments.
// Accepts simple syntax: "toolname key=value key2=value"
// Or a JSON object (must start with '{') with optional fields: {"name":"tool","arguments":{...}}
func (s *MCPService) promptModifyTool(callParams *CallToolParams) (*CallToolParams, error) {
	fmt.Printf("\nEnter new tool name and arguments.\n")
	fmt.Printf("Simple: <toolname> key=value key2=value\n")
	fmt.Printf("Or JSON (must start with '{'): {\"name\":\"tool\", \"arguments\":{...}}\n")
	fmt.Printf("Type 'cancel' to abort.\n")
	fmt.Printf("Input: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty input")
	}

	if strings.EqualFold(line, "cancel") {
		return nil, errToolModificationCancelled
	}

	// If JSON
	if strings.HasPrefix(line, "{") {
		var parsed struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		if parsed.Name == "" {
			parsed.Name = callParams.Name
		}
		if parsed.Arguments == nil {
			parsed.Arguments = callParams.Arguments
		}
		return &CallToolParams{Name: parsed.Name, Arguments: parsed.Arguments}, nil
	}

	// Simple syntax: split tokens
	toks := strings.Fields(line)
	if len(toks) == 0 {
		return nil, fmt.Errorf("invalid input")
	}
	newName := toks[0]
	newArgs := make(map[string]interface{})
	// Start with existing arguments
	for k, v := range callParams.Arguments {
		newArgs[k] = v
	}
	for _, tok := range toks[1:] {
		if !strings.Contains(tok, "=") {
			return nil, fmt.Errorf("invalid parameter '%s': expected format name=value", tok)
		}
		parts := strings.SplitN(tok, "=", 2)
		k := parts[0]
		v := parts[1]
		// Try parse JSON for complex values
		if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
			var vv interface{}
			if err := json.Unmarshal([]byte(v), &vv); err == nil {
				newArgs[k] = vv
				continue
			}
		}
		// Try number
		if num, err := strconv.ParseFloat(v, 64); err == nil {
			newArgs[k] = num
			continue
		}
		// Try bool
		if b, err := strconv.ParseBool(v); err == nil {
			newArgs[k] = b
			continue
		}
		newArgs[k] = v
	}

	return &CallToolParams{Name: newName, Arguments: newArgs}, nil
}
