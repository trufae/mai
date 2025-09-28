package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func NewMCPService(yoloMode bool, drunkMode bool, reportFile string) *MCPService {
	return &MCPService{
		servers:       make(map[string]*MCPServer),
		yoloMode:      yoloMode,
		drunkMode:     drunkMode,
		toolPerms:     make(map[string]ToolPermission),
		promptPerms:   make(map[string]PromptPermission),
		reportEnabled: reportFile != "",
		reportFile:    reportFile,
		report:        Report{Entries: []ReportEntry{}},
	}
}

// getServerNameFromCommand extracts server name from the command string
func getServerNameFromCommand(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}

	firstPart := parts[0]
	serverName := firstPart

	if idx := strings.LastIndex(firstPart, "/"); idx != -1 {
		serverName = firstPart[idx+1:]
	}

	return serverName
}

// StartServer starts an MCP server process
func (s *MCPService) StartServer(name, command string) error {
	return s.StartServerWithEnv(name, command, nil)
}

// StartServerWithEnv starts an MCP server process with custom environment variables
func (s *MCPService) StartServerWithEnv(name, command string, env map[string]string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Parse command string
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	// Apply custom environment variables if provided
	if env != nil && len(env) > 0 {
		// Start with current environment
		cmdEnv := os.Environ()

		// Add or override with custom variables
		for key, value := range env {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", key, value))
		}

		// Set the environment for the command
		cmd.Env = cmdEnv
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %v", err)
	}

	server := &MCPServer{
		Name:          name,
		Command:       command,
		Process:       cmd,
		Stdin:         stdin,
		Stdout:        stdout,
		Stderr:        stderr,
		Tools:         []Tool{},
		stderrDone:    make(chan struct{}),
		stderrActive:  true,
		monitorDone:   make(chan struct{}),
		monitorActive: true,
	}

	// Start a goroutine to handle stderr output
	go s.handleStderr(server)

	// Start a goroutine to monitor the server process
	go s.monitorServer(server)

	s.servers[name] = server

	// Initialize the server (handshake)
	if err := s.initializeServer(server); err != nil {
		s.stopServer(server)
		delete(s.servers, name)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	// Load tools
	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for server %s: %v", name, err)
	}

	// Load prompts (best-effort)
	if err := s.loadPrompts(server); err != nil {
		log.Printf("Warning: failed to load prompts for server %s: %v", name, err)
	}

	log.Printf("Started MCP server: %s", name)
	return nil
}

// initializeServer performs the MCP handshake
func (s *MCPService) initializeServer(server *MCPServer) error {
	initRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools":   map[string]interface{}{},
				"prompts": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    "mai-wmcp",
				"version": "1.0.0",
			},
		},
		ID: 1,
	}

	response, err := s.sendRequest(server, initRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("initialization failed: %v", response.Error)
	}

	// Send initialized notification
	initNotification := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]interface{}{},
	}

	// Send notification (no response expected)
	reqBytes, _ := json.Marshal(initNotification)
	server.Stdin.Write(reqBytes)
	server.Stdin.Write([]byte("\n"))

	return nil
}

// loadTools loads available tools from the server
func (s *MCPService) loadTools(server *MCPServer) error {
	toolsRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		Params:  map[string]interface{}{},
		ID:      2,
	}

	response, err := s.sendRequest(server, toolsRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("tools/list failed: %v", response.Error)
	}

	// Parse tools response
	resultBytes, _ := json.Marshal(response.Result)
	var toolsResult ToolsListResult
	if err := json.Unmarshal(resultBytes, &toolsResult); err != nil {
		return fmt.Errorf("failed to parse tools response: %v", err)
	}

	// Process tool parameters
	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		tool.Parameters = extractParametersFromSchema(tool.InputSchema)
	}

	server.mutex.Lock()
	server.Tools = toolsResult.Tools
	server.mutex.Unlock()

	log.Printf("Loaded %d tools for server %s", len(toolsResult.Tools), server.Name)
	return nil
}

// loadPrompts loads available prompts from the server
func (s *MCPService) loadPrompts(server *MCPServer) error {
	promptsRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "prompts/list",
		Params:  map[string]interface{}{},
		ID:      3,
	}

	response, err := s.sendRequest(server, promptsRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		// Not all servers implement prompts; don't treat as fatal
		log.Printf("prompts/list failed on %s: %v", server.Name, response.Error)
		return nil
	}

	resultBytes, _ := json.Marshal(response.Result)
	var list PromptsListResult
	if err := json.Unmarshal(resultBytes, &list); err != nil {
		return fmt.Errorf("failed to parse prompts response: %v", err)
	}

	server.mutex.Lock()
	server.Prompts = list.Prompts
	server.mutex.Unlock()

	log.Printf("Loaded %d prompts for server %s", len(list.Prompts), server.Name)
	return nil
}

// isToolAvailable checks if a tool with the given name is available across all servers
func (s *MCPService) isToolAvailable(toolName string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, server := range s.servers {
		server.mutex.RLock()
		for _, tool := range server.Tools {
			if tool.Name == toolName {
				server.mutex.RUnlock()
				return true
			}
		}
		server.mutex.RUnlock()
	}
	return false
}

// promptToolNotFoundDecision prompts the user when a tool doesn't exist
func (s *MCPService) promptToolNotFoundDecision(toolName string) YoloDecision {
	fmt.Printf("\n===== TOOL NOT FOUND =====\n")
	fmt.Printf("Tool '%s' does not exist.\n\n", toolName)
	fmt.Printf("Options:\n")
	fmt.Printf("[1] Respond that the tool doesn't exist\n")
	fmt.Printf("[2] Let me enter a custom response\n")
	fmt.Printf("[3] Show available tools and let me adjust the request\n")
	fmt.Printf("[4] Respond with a message to guide the model\n")
	fmt.Printf("\nYour decision: ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
	case "1":
		return YoloToolNotFound
	case "2":
		return YoloCustomResponse
	case "3":
		return YoloModify
	case "4":
		return YoloGuideModel
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
	default:
		fmt.Println("Invalid option, defaulting to reject")
		return YoloReject
	}
}

// promptModifyTool prompts the user to modify the tool name and arguments.
// Accepts simple syntax: "toolname key=value key2=value"
// Or a JSON object (must start with '{') with optional fields: {"name":"tool","arguments":{...}}
func (s *MCPService) promptModifyTool(callParams *CallToolParams) (*CallToolParams, error) {
	fmt.Printf("\nEnter new tool name and arguments.\n")
	fmt.Printf("Simple: <toolname> key=value key2=value\n")
	fmt.Printf("Or JSON (must start with '{'): {\"name\":\"tool\", \"arguments\":{...}}\n")
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

// promptDecision prompts the user for a prompt decision on prompt execution
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
func (s *MCPService) storePromptPermission(promptName string, argsJSON string, decision PromptDecision) {
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

// addReportEntry adds an entry to the report
func (s *MCPService) addReportEntry(server string, tool string, params interface{}, result interface{}, err error) {
	if !s.reportEnabled {
		return
	}

	s.reportLock.Lock()
	defer s.reportLock.Unlock()

	entry := ReportEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Server:    server,
		Tool:      tool,
		Params:    params,
		Result:    result,
	}

	if err != nil {
		entry.Error = err.Error()
	}

	s.report.Entries = append(s.report.Entries, entry)

	// Write to file
	reportJSON, _ := json.MarshalIndent(s.report, "", "  ")
	ioutil.WriteFile(s.reportFile, reportJSON, 0644)
}

// sendRequest sends a JSONRPC request to the server and returns the response
func (s *MCPService) sendRequest(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	// Handle prompt execution confirmation for prompts/get requests when NOT in yolo mode
	if !s.yoloMode && request.Method == "prompts/get" {
		// Extract prompt name and args
		var getPromptParams GetPromptParams
		paramsBytes, _ := json.Marshal(request.Params)
		json.Unmarshal(paramsBytes, &getPromptParams)

		// Convert arguments to JSON string for comparison
		argsJSON, _ := json.Marshal(getPromptParams.Arguments)

		// Check if we already have a permission decision
		allowed := s.checkPromptPermission(getPromptParams.Name, string(argsJSON))
		if !allowed {
			// No existing permission, ask user
			decision := s.promptPromptDecision(getPromptParams.Name, string(argsJSON))

			switch decision {
			case PromptApprove:
				// Continue with request
				break
			case PromptReject:
				return nil, fmt.Errorf("prompt execution rejected by user")
			case PromptPermitPromptForever, PromptPermitAllPromptsForever:
				s.yoloMode = true
				break
			case PromptPermitPromptWithArgsForever, PromptRejectForever:
				// Store the decision
				s.storePromptPermission(getPromptParams.Name, string(argsJSON), decision)

				// If it was a reject decision, return error
				if decision == PromptRejectForever {
					return nil, fmt.Errorf("prompt execution rejected by user policy")
				}
			case PromptCustom:
				// Return a special error that the handler can catch to provide custom prompt
				return nil, fmt.Errorf("PROMPT_CUSTOM_REQUEST")
			case PromptList:
				// Return a special error that the handler can catch to list prompts
				return nil, fmt.Errorf("PROMPT_LIST_REQUEST")
			}
		}
	}

	// Handle tool execution confirmation for tools/call requests when NOT in yolo mode
	if !s.yoloMode && request.Method == "tools/call" {
		// Extract tool name and params
		var callParams CallToolParams
		paramsBytes, _ := json.Marshal(request.Params)
		json.Unmarshal(paramsBytes, &callParams)

		// Check if tool exists first
		if !s.isToolAvailable(callParams.Name) {
			// Tool doesn't exist, prompt user for what to do
			decision := s.promptToolNotFoundDecision(callParams.Name)

			switch decision {
			case YoloToolNotFound:
				return nil, fmt.Errorf("tool '%s' does not exist", callParams.Name)
			case YoloCustomResponse:
				// Prompt for custom response
				fmt.Print("Enter your custom response: ")
				reader := bufio.NewReader(os.Stdin)
				customResponse, _ := reader.ReadString('\n')
				customResponse = strings.TrimSpace(customResponse)
				if customResponse == "" {
					return nil, fmt.Errorf("tool '%s' does not exist", callParams.Name)
				}
				// Return a special result that indicates this is a custom response
				return &JSONRPCResponse{
					JSONRPC: "2.0",
					Result: CallToolResult{
						Content: []Content{{Type: "text", Text: customResponse}},
					},
					ID: request.ID,
				}, nil
			case YoloModify:
				// Show available tools and let user modify
				fmt.Println("\nAvailable tools:")
				s.mutex.RLock()
				toolCount := 0
				for _, server := range s.servers {
					server.mutex.RLock()
					for _, tool := range server.Tools {
						fmt.Printf("  %s - %s\n", tool.Name, tool.Description)
						toolCount++
					}
					server.mutex.RUnlock()
				}
				s.mutex.RUnlock()

				if toolCount == 0 {
					fmt.Println("  No tools available")
					return nil, fmt.Errorf("tool '%s' does not exist and no alternatives available", callParams.Name)
				}

				fmt.Print("Enter new tool name and arguments (or 'cancel' to abort): ")
				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.TrimSpace(input)
				if input == "cancel" || input == "" {
					return nil, fmt.Errorf("tool execution cancelled by user")
				}

				// Parse input: "toolname arg1=value arg2=value"
				parts := strings.Fields(input)
				if len(parts) == 0 {
					return nil, fmt.Errorf("invalid input")
				}

				newToolName := parts[0]
				newArgs := make(map[string]interface{})

				// Copy existing args as defaults
				for k, v := range callParams.Arguments {
					newArgs[k] = v
				}

				// Parse additional arguments
				for _, part := range parts[1:] {
					if !strings.Contains(part, "=") {
						return nil, fmt.Errorf("invalid parameter format: %s (expected key=value)", part)
					}
					kv := strings.SplitN(part, "=", 2)
					k, v := kv[0], kv[1]

					// Try to parse as JSON for complex values
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

					// Default to string
					newArgs[k] = v
				}

				// Update the request with modified parameters
				callParams.Name = newToolName
				callParams.Arguments = newArgs
				request.Params = callParams

				// Check if the new tool exists
				if !s.isToolAvailable(newToolName) {
					return nil, fmt.Errorf("modified tool '%s' also does not exist", newToolName)
				}

				// Continue with the modified request (will go through normal permission checking)
			case YoloGuideModel:
				guideMessage := "The tool you requested doesn't exist. Please check the available tools and try again with a valid tool name."
				return &JSONRPCResponse{
					JSONRPC: "2.0",
					Result: CallToolResult{
						Content: []Content{{Type: "text", Text: guideMessage}},
					},
					ID: request.ID,
				}, nil
			}
		}

		// Convert arguments to JSON string for comparison
		paramsJSON, _ := json.Marshal(callParams.Arguments)

		// Check if we already have a permission decision
		allowed := s.checkToolPermission(callParams.Name, string(paramsJSON))
		if !allowed {
			// No existing permission, ask user
			decision := s.promptYoloDecision(callParams.Name, string(paramsJSON))

			switch decision {
			case YoloApprove:
				// Continue with request
				break
			case YoloReject:
				return nil, fmt.Errorf("tool execution rejected by user")
			case YoloPermitToolForever, YoloPermitAllToolsForever:
				s.yoloMode = true
				break
			case YoloPermitToolWithParamsForever, YoloRejectForever:
				// Store the decision
				s.storeToolPermission(callParams.Name, string(paramsJSON), decision)

				// If it was a reject decision, return error
				if decision == YoloRejectForever {
					return nil, fmt.Errorf("tool execution rejected by user policy")
				}
			case YoloModify:
				// Ask the user for a modified tool name/arguments
				newCallParams, err := s.promptModifyTool(&callParams)
				if err != nil {
					return nil, fmt.Errorf("failed to parse modified params: %v", err)
				}
				callParams = *newCallParams
				// Update original request params so the modified values are sent
				request.Params = callParams
				// Recompute params JSON and re-check permissions
				paramsJSON, _ = json.Marshal(callParams.Arguments)
				allowed = s.checkToolPermission(callParams.Name, string(paramsJSON))
				if !allowed {
					// Ask user again for the modified tool if still no permission
					decision2 := s.promptYoloDecision(callParams.Name, string(paramsJSON))
					switch decision2 {
					case YoloApprove:
						break
					case YoloReject:
						return nil, fmt.Errorf("tool execution rejected by user")
					case YoloPermitToolForever, YoloPermitAllToolsForever:
						s.yoloMode = true
					case YoloPermitToolWithParamsForever, YoloRejectForever:
						s.storeToolPermission(callParams.Name, string(paramsJSON), decision2)
						if decision2 == YoloRejectForever {
							return nil, fmt.Errorf("tool execution rejected by user policy")
						}
					case YoloModify:
						// If user asks to modify again, return an error to avoid deep loops
						return nil, fmt.Errorf("multiple modifications not supported in one prompt")
					}
				}
			}
		}
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Check if server process is still running
	if server.Process.ProcessState != nil {
		log.Printf("ERROR: Server %s process has exited with state: %v", server.Name, server.Process.ProcessState)
		return nil, fmt.Errorf("server process has exited")
	}
	if server.Process.Process != nil {
		debugLog(s.debugMode, "Server %s process PID: %d", server.Name, server.Process.Process.Pid)
	}

	debugLog(s.debugMode, "Sending JSONRPC request to server %s: %s", server.Name, string(reqBytes))

	// Send request
	if _, err := server.Stdin.Write(reqBytes); err != nil {
		log.Printf("ERROR: Failed to write request to server %s stdin: %v", server.Name, err)
		return nil, fmt.Errorf("failed to write request: %v", err)
	}
	if _, err := server.Stdin.Write([]byte("\n")); err != nil {
		log.Printf("ERROR: Failed to write newline to server %s stdin: %v", server.Name, err)
		return nil, fmt.Errorf("failed to write newline: %v", err)
	}

	debugLog(s.debugMode, "Request sent to server %s, waiting for response", server.Name)

	// Read response with timeout
	type scanResult struct {
		ok    bool
		bytes []byte
		err   error
	}

	resultChan := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(server.Stdout)
		buf := make([]byte, 10*1024*1024) // 10MB buffer
		scanner.Buffer(buf, 10*1024*1024)
		ok := scanner.Scan()
		var err error
		var bytes []byte
		if ok {
			bytes = scanner.Bytes()
		} else {
			err = scanner.Err()
		}
		resultChan <- scanResult{ok: ok, bytes: bytes, err: err}
	}()

	timeout := 30 * time.Second
	var result scanResult
	select {
	case result = <-resultChan:
		// Got result
	case <-time.After(timeout):
		log.Printf("ERROR: Timeout waiting for response from server %s after %v", server.Name, timeout)
		return nil, fmt.Errorf("timeout waiting for response")
	}

	if !result.ok {
		if result.err != nil {
			log.Printf("ERROR: Scanner error while reading response from server %s: %v", server.Name, result.err)
		} else {
			log.Printf("ERROR: No response received from server %s (EOF or empty)", server.Name)
		}
		return nil, fmt.Errorf("failed to read response")
	}

	// Get the response bytes
	responseBytes := result.bytes

	debugLog(s.debugMode, "Received raw response from server %s: %s", server.Name, string(responseBytes))

	// Debug logging for JSONRPC response
	if s.debugMode {
		// Try to pretty print the JSON
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, responseBytes, "", "  ") == nil {
			debugLog(s.debugMode, "Received JSONRPC response from %s: %s", server.Name, prettyJSON.String())
		} else {
			// If not valid JSON, print as string
			debugLog(s.debugMode, "Received JSONRPC response from %s: %s", server.Name, string(responseBytes))
		}
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		log.Printf("ERROR: Failed to unmarshal response from server %s: %v, raw response: %s", server.Name, err, string(responseBytes))
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Add to report if this is a tool call
	if request.Method == "tools/call" {
		var toolParams CallToolParams
		paramsBytes, _ := json.Marshal(request.Params)
		json.Unmarshal(paramsBytes, &toolParams)

		// Always log tool execution regardless of report being enabled
		log.Printf("MCP tool executed - Server: %s, Tool: %s, Params: %s",
			server.Name, toolParams.Name, string(paramsBytes))

		// Add to structured report if enabled
		if s.reportEnabled {
			s.addReportEntry(server.Name, toolParams.Name, toolParams.Arguments, response.Result, nil)
		}
	}

	return &response, nil
}

// handleStderr reads from the stderr pipe and logs all messages
func (s *MCPService) handleStderr(server *MCPServer) {
	scanner := bufio.NewScanner(server.Stderr)
	for server.stderrActive && scanner.Scan() {
		text := scanner.Text()
		log.Printf("[%s stderr] %s", server.Name, text)
	}
	close(server.stderrDone)
}

// monitorServer monitors the server process and restarts it if it crashes
func (s *MCPService) monitorServer(server *MCPServer) {
	for server.monitorActive {
		// Wait for the process to exit
		err := server.Process.Wait()
		if !server.monitorActive {
			break
		}

		// Process has exited, log the error
		if err != nil {
			log.Printf("ERROR: MCP server '%s' crashed: %v", server.Name, err)
		} else {
			log.Printf("ERROR: MCP server '%s' exited unexpectedly", server.Name)
		}

		// Wait 1 second before restarting
		time.Sleep(1 * time.Second)

		// Restart the server
		log.Printf("Restarting MCP server '%s'...", server.Name)
		if restartErr := s.restartServer(server); restartErr != nil {
			log.Printf("ERROR: Failed to restart MCP server '%s': %v", server.Name, restartErr)
			// Continue monitoring in case we can restart later
		} else {
			log.Printf("Successfully restarted MCP server '%s'", server.Name)
		}
	}
	close(server.monitorDone)
}

// restartServer restarts a crashed MCP server
func (s *MCPService) restartServer(server *MCPServer) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Stop existing goroutines and close pipes
	server.stderrActive = false
	server.monitorActive = false

	// Close existing pipes if they exist
	if server.Stdin != nil {
		server.Stdin.Close()
	}
	if server.Stdout != nil {
		server.Stdout.Close()
	}
	if server.Stderr != nil {
		server.Stderr.Close()
	}

	// Wait for goroutines to finish
	<-server.stderrDone
	<-server.monitorDone

	// Parse command string
	parts := strings.Fields(server.Command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %v", err)
	}

	// Recreate channels
	server.stderrDone = make(chan struct{})
	server.monitorDone = make(chan struct{})

	// Update server with new process and pipes
	server.Process = cmd
	server.Stdin = stdin
	server.Stdout = stdout
	server.Stderr = stderr

	// Reset monitoring flags
	server.stderrActive = true
	server.monitorActive = true

	// Start new goroutines for stderr and monitoring
	go s.handleStderr(server)
	go s.monitorServer(server)

	// Re-initialize the server (handshake)
	if err := s.initializeServer(server); err != nil {
		s.stopServer(server)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	// Re-load tools
	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for restarted server %s: %v", server.Name, err)
	}

	// Re-load prompts
	if err := s.loadPrompts(server); err != nil {
		log.Printf("Warning: failed to load prompts for restarted server %s: %v", server.Name, err)
	}

	return nil
}

// stopServer stops an MCP server
func (s *MCPService) stopServer(server *MCPServer) {
	// Mark handlers as inactive
	server.stderrActive = false
	server.monitorActive = false

	if server.Process != nil {
		server.Process.Process.Kill()
		server.Process.Wait()
	}
	server.Stdin.Close()
	server.Stdout.Close()
	server.Stderr.Close()

	// Wait for goroutines to finish
	<-server.stderrDone
	<-server.monitorDone
}

// StopAllServers stops all MCP servers
func (s *MCPService) StopAllServers() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for name, server := range s.servers {
		s.stopServer(server)
		log.Printf("Stopped MCP server: %s", name)
	}
}

// extractParametersFromSchema extracts parameter information from JSON schema
func extractParametersFromSchema(schema map[string]interface{}) []ToolParameter {
	var parameters []ToolParameter

	// Extract properties from schema
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return parameters
	}

	// Extract required fields list
	requiredFields := make(map[string]bool)
	if required, ok := schema["required"].([]interface{}); ok {
		for _, field := range required {
			if fieldName, ok := field.(string); ok {
				requiredFields[fieldName] = true
			}
		}
	}

	// Process each property
	for name, propInterface := range properties {
		propInfo, ok := propInterface.(map[string]interface{})
		if !ok {
			continue
		}

		// Create parameter
		param := ToolParameter{
			Name:     name,
			Required: requiredFields[name],
		}

		// Extract description
		if desc, ok := propInfo["description"].(string); ok {
			param.Description = desc
		}

		// Extract type
		if typeStr, ok := propInfo["type"].(string); ok {
			param.Type = typeStr
		}

		parameters = append(parameters, param)
	}

	return parameters
}
