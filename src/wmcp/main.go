package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// JSONRPC structures
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      interface{} `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// MCP Tool structures

// ToolParameter represents a parameter for a tool
type ToolParameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Parameters  []ToolParameter        `json:"parameters,omitempty"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// MCP Prompt structures (MCP Prompts API)
// PromptArgument represents a parameter for a prompt
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt represents a single prompt available on the server
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptsListResult is the result for prompts/list
type PromptsListResult struct {
	Prompts []Prompt `json:"prompts"`
}

// GetPromptParams is the params object for prompts/get
type GetPromptParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// PromptMessageContent models a prompt message's content item
type PromptMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// PromptMessage models a message returned by prompts/get
type PromptMessage struct {
	Role    string                 `json:"role"`
	Content []PromptMessageContent `json:"content"`
}

// GetPromptResult is the result for prompts/get
type GetPromptResult struct {
	Messages []PromptMessage `json:"messages"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type CallToolResult struct {
	Content []Content      `json:"content",omitempty`
	Error   *CallToolError `json:"error",omitempty`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Yolo prompt decision type
type YoloDecision int

const (
	YoloApprove YoloDecision = iota
	YoloReject
	YoloPermitToolForever
	YoloPermitToolWithParamsForever
	YoloRejectForever
	YoloPermitAllToolsForever
	YoloModify
)

// Tool permission record
type ToolPermission struct {
	ToolName   string
	Parameters string // JSON string of parameters for exact matching
	Approved   bool
}

// ReportEntry represents a single entry in the report
type ReportEntry struct {
	Timestamp string      `json:"timestamp"`
	Server    string      `json:"server"`
	Tool      string      `json:"tool"`
	Params    interface{} `json:"params"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// Report represents a collection of tool executions
type Report struct {
	Entries []ReportEntry `json:"entries"`
}

// MCP Server represents a running MCP server process
type MCPServer struct {
	Name          string
	Command       string
	Process       *exec.Cmd
	Stdin         io.WriteCloser
	Stdout        io.ReadCloser
	Stderr        io.ReadCloser
	Tools         []Tool
	Prompts       []Prompt
	mutex         sync.RWMutex
	stderrDone    chan struct{}
	stderrActive  bool
	monitorDone   chan struct{}
	monitorActive bool
}

// MCPService manages multiple MCP servers
type MCPService struct {
	servers       map[string]*MCPServer
	mutex         sync.RWMutex
	yoloMode      bool
	debugMode     bool
	toolPerms     map[string]ToolPermission // Map tool name or tool+params hash to permission
	toolPermsLock sync.RWMutex
	reportEnabled bool
	reportFile    string
	report        Report
	reportLock    sync.RWMutex
}

func NewMCPService(yoloMode bool, reportFile string) *MCPService {
	return &MCPService{
		servers:       make(map[string]*MCPServer),
		yoloMode:      yoloMode,
		toolPerms:     make(map[string]ToolPermission),
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
	// Handle tool execution confirmation for tools/call requests when NOT in yolo mode
	if !s.yoloMode && request.Method == "tools/call" {
		// Extract tool name and params
		var callParams CallToolParams
		paramsBytes, _ := json.Marshal(request.Params)
		json.Unmarshal(paramsBytes, &callParams)

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

// HTTP Handlers

// listToolsHandler returns all tools from all servers
func (s *MCPService) listToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("# Tools Catalog\n\n")

	for _ /*serverName */, server := range s.servers {
		// output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		server.mutex.RLock()
		// output.WriteString(fmt.Sprintf("Executable: `%s`\n", server.Command))
		// output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))

		for _, tool := range server.Tools {
			// output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			// output.WriteString(fmt.Sprintf("ToolName: %s/%s\n", serverName, tool.Name))
			output.WriteString(fmt.Sprintf("ToolName: %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("Description: %s\n", tool.Description))
			if tool.InputSchema != nil {
				// schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
				// output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))

				// Print CLI-style arguments list
				// Use the prepared Parameters array if available
				if len(tool.Parameters) > 0 {
					output.WriteString("Arguments:\n")
					for _, param := range tool.Parameters {
						// Format: name=<value> : description (type) [required]
						reqText := ""
						if param.Required {
							reqText = " [required]"
						}
						output.WriteString(fmt.Sprintf("- %s=<value> : %s (%s)%s\n",
							param.Name, param.Description, param.Type, reqText))
					}
				} else {
					//		output.WriteString("Arguments: None\n")
				}
			}
			/*
				// Construct usage example with parameters if available
				if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok && len(properties) > 0 {
					// Build URL with query parameters
					var params []string
					for key, _ := range properties {
						params = append(params, fmt.Sprintf("%s=value", key))
					}
					paramString := strings.Join(params, " ")
					output.WriteString(fmt.Sprintf("Usage: `mai-tool call %s/%s %s`\n\n", serverName, tool.Name, paramString))
					// output.WriteString(fmt.Sprintf("**Usage:** `GET /call/%s/%s?%s`\n\n", serverName, tool.Name, paramString))
				} else {
					output.WriteString(fmt.Sprintf("Usage: `mai-tool call %s %s`\n\n", serverName, tool.Name))
					// output.WriteString(fmt.Sprintf("**Usage:** `GET /call/%s/%s`\n\n", serverName, tool.Name))
				}
			*/
			output.WriteString("----\n")
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// listPromptsHandler returns all prompts from all servers
func (s *MCPService) listPromptsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("# Prompts Catalog\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		for _, prompt := range server.Prompts {
			output.WriteString(fmt.Sprintf("PromptName: %s/%s\n", serverName, prompt.Name))
			if prompt.Description != "" {
				output.WriteString(fmt.Sprintf("Description: %s\n", prompt.Description))
			}
			if len(prompt.Arguments) > 0 {
				output.WriteString("Arguments:\n")
				for _, a := range prompt.Arguments {
					req := ""
					if a.Required {
						req = " [required]"
					}
					typ := a.Type
					if typ == "" {
						typ = "string"
					}
					output.WriteString(fmt.Sprintf("- %s=<%s> : %s%s\n", a.Name, typ, a.Description, req))
				}
			}
			output.WriteString("\n")
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// jsonPromptsHandler returns all prompts in JSON grouped by server
func (s *MCPService) jsonPromptsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	result := make(map[string][]Prompt)
	for serverName, server := range s.servers {
		server.mutex.RLock()
		prompts := make([]Prompt, len(server.Prompts))
		copy(prompts, server.Prompts)
		server.mutex.RUnlock()
		result[serverName] = prompts
	}

	json.NewEncoder(w).Encode(result)
}

// getPromptHandler calls prompts/get on a server (or auto-discovers by prompt name)
func (s *MCPService) getPromptHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverName := vars["server"]
	promptName := vars["prompt"]

	// Always log HTTP requests
	log.Printf("HTTP %s %s - Server: %s, Prompt: %s", r.Method, r.URL.String(), serverName, promptName)

	s.mutex.RLock()
	server, exists := s.servers[serverName]
	s.mutex.RUnlock()
	if !exists {
		// Try to auto-discover by prompt name
		for name, srv := range s.servers {
			srv.mutex.RLock()
			for _, p := range srv.Prompts {
				if p.Name == promptName {
					serverName = name
					server = srv
					exists = true
					break
				}
			}
			srv.mutex.RUnlock()
			if exists {
				break
			}
		}
	}

	if !exists {
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	// Parse arguments (similar to tools)
	arguments := make(map[string]interface{})
	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > 0 {
				if err := json.Unmarshal(body, &arguments); err != nil {
					http.Error(w, "Invalid JSON in request body", http.StatusBadRequest)
					return
				}
			}
		} else {
			r.ParseForm()
			for key, values := range r.Form {
				if len(values) == 1 {
					if num, err := strconv.ParseFloat(values[0], 64); err == nil {
						arguments[key] = num
					} else if b, err := strconv.ParseBool(values[0]); err == nil {
						arguments[key] = b
					} else {
						arguments[key] = values[0]
					}
				} else {
					arguments[key] = values
				}
			}
		}
	} else if r.Method == "GET" {
		for key, values := range r.URL.Query() {
			if len(values) == 1 {
				if num, err := strconv.ParseFloat(values[0], 64); err == nil {
					arguments[key] = num
				} else if b, err := strconv.ParseBool(values[0]); err == nil {
					arguments[key] = b
				} else {
					arguments[key] = values[0]
				}
			} else {
				arguments[key] = values
			}
		}
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "prompts/get",
		Params: GetPromptParams{
			Name:      promptName,
			Arguments: arguments,
		},
		ID: 4,
	}

	response, err := s.sendRequest(server, req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get prompt: %v", err), http.StatusBadRequest)
		return
	}

	if response.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Return result as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response.Result)
}

// jsonToolsHandler returns all tools from all servers in JSON format
func (s *MCPService) jsonToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	res := make(map[string][]Tool)
	for serverName, server := range s.servers {
		server.mutex.RLock()
		// Make sure all tools have their Parameters populated from InputSchema
		tools := make([]Tool, len(server.Tools))
		copy(tools, server.Tools)

		// Ensure Parameters are populated for JSON output
		for i := range tools {
			if len(tools[i].Parameters) == 0 && tools[i].InputSchema != nil {
				tools[i].Parameters = extractParametersFromSchema(tools[i].InputSchema)
			}
		}

		res[serverName] = tools
		server.mutex.RUnlock()
	}
	// json.NewEncoder(w).Encode(res)

	jsonBytes, err := json.Marshal(res) // compact JSON, no newline
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonBytes)
}

// quietToolsHandler returns all tools from all servers in a minimally formatted plain text
func (s *MCPService) quietToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder

	for serverName, server := range s.servers {
		server.mutex.RLock()
		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("/* %s */\n", tool.Description))
			output.WriteString(fmt.Sprintf("Tool: %s/%s", serverName, tool.Name))
			// Use the Parameters array if available
			if len(tool.Parameters) > 0 {
				for _, param := range tool.Parameters {
					output.WriteString(fmt.Sprintf(" %s=<value>", param.Name))
				}
			} else if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
				for key, _ := range properties {
					output.WriteString(fmt.Sprintf(" %s=<value>", key))
				}
			}
			output.WriteString("\n")
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// markdownToolsHandler returns all tools from all servers in markdown format
func (s *MCPService) markdownToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/markdown")

	var output strings.Builder
	output.WriteString("# Tools Catalog\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))

		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("**Description:** %s\n\n", tool.Description))

			// Add parameters section with type and required information
			if len(tool.Parameters) > 0 || tool.InputSchema != nil {
				output.WriteString("**Parameters:**\n\n")
				output.WriteString("| Name | Type | Required | Description |\n")
				output.WriteString("|------|------|----------|-------------|\n")

				// Use Parameters array if available
				if len(tool.Parameters) > 0 {
					for _, param := range tool.Parameters {
						required := "No"
						if param.Required {
							required = "Yes"
						}
						output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
							param.Name, param.Type, required, param.Description))
					}
				} else if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
					// Extract required fields
					requiredFields := make(map[string]bool)
					if required, ok := tool.InputSchema["required"].([]interface{}); ok {
						for _, field := range required {
							if fieldName, ok := field.(string); ok {
								requiredFields[fieldName] = true
							}
						}
					}

					// Display properties from schema
					for key, val := range properties {
						propInfo, _ := val.(map[string]interface{})
						desc := ""
						propType := "string" // Default type
						req := "No"

						if requiredFields[key] {
							req = "Yes"
						}

						if propInfo != nil {
							if d, ok := propInfo["description"].(string); ok {
								desc = d
							}
							if t, ok := propInfo["type"].(string); ok {
								propType = t
							}
						}

						output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
							key, propType, req, desc))
					}
				}
				output.WriteString("\n")
			}

			// Keep the schema output for reference
			if tool.InputSchema != nil {
				schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
				output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))
			}

			output.WriteString(fmt.Sprintf("**Usage:** `POST /call/%s/%s`\n\n", serverName, tool.Name))
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// callToolHandler calls a specific tool on a specific server
func (s *MCPService) callToolHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverName := vars["server"]
	toolName := vars["tool"]

	s.mutex.RLock()
	server, exists := s.servers[serverName]
	s.mutex.RUnlock()
	if !exists {
		for name, _server := range s.servers {
			for _, tool := range _server.Tools {
				if toolName == tool.Name {
					serverName = name
					server = _server
					exists = true
					break
				}
			}
			if exists {
				break
			}
		}
	}

	// Always log HTTP requests regardless of debug mode
	log.Printf("HTTP %s %s - Server: %s, Tool: %s", r.Method, r.URL.String(), serverName, toolName)

	if !exists {
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	// Parse arguments
	arguments := make(map[string]interface{})
	debugLog(s.debugMode, "Parsing arguments for tool call")

	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			// Parse JSON body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("ERROR: Failed to read request body for tool %s/%s: %v", serverName, toolName, err)
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}

			if len(body) > 0 {
				if err := json.Unmarshal(body, &arguments); err != nil {
					log.Printf("ERROR: Failed to parse JSON body for tool %s/%s: %v", serverName, toolName, err)
					http.Error(w, "Invalid JSON in request body", http.StatusBadRequest)
					return
				}
			}
		} else {
			// Parse form data
			if err := r.ParseForm(); err != nil {
				log.Printf("ERROR: Failed to parse form data for tool %s/%s: %v", serverName, toolName, err)
				http.Error(w, "Failed to parse form data", http.StatusBadRequest)
				return
			}
			arguments = make(map[string]interface{})
			for key, values := range r.Form {
				if len(values) == 1 {
					// Try to parse as number, otherwise keep as string
					if num, err := strconv.ParseFloat(values[0], 64); err == nil {
						arguments[key] = num
					} else if b, err := strconv.ParseBool(values[0]); err == nil {
						arguments[key] = b
					} else {
						arguments[key] = values[0]
					}
				} else {
					arguments[key] = values
				}
			}
		}
	} else if r.Method == "GET" {
		// Debug log query parameters if debug mode is enabled
		debugLog(s.debugMode, "Query parameters: %v", r.URL.Query())
		// Parse query parameters
		for key, values := range r.URL.Query() {
			if len(values) == 1 {
				// Try to parse as number, otherwise keep as string
				if num, err := strconv.ParseFloat(values[0], 64); err == nil {
					arguments[key] = num
				} else if b, err := strconv.ParseBool(values[0]); err == nil {
					arguments[key] = b
				} else {
					arguments[key] = values[0]
				}
			} else {
				arguments[key] = values
			}
		}
	}

	debugLog(s.debugMode, "Parsed arguments: %v", arguments)

	// Create tool call request
	toolRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
		ID: time.Now().UnixNano(),
	}

	debugLog(s.debugMode, "Calling tool %s on server %s with arguments: %v", toolName, serverName, arguments)

	response, err := s.sendRequest(server, toolRequest)
	if err != nil {
		log.Printf("ERROR: Failed to send request to server %s for tool %s: %v", serverName, toolName, err)
		http.Error(w, fmt.Sprintf("Failed to call tool: %v", err), http.StatusInternalServerError)
		return
	}

	if response.Error != nil {
		log.Printf("ERROR: Tool call to %s/%s failed with RPC error: %v", serverName, toolName, response.Error)
		http.Error(w, fmt.Sprintf("Tool call failed: %v", response.Error), http.StatusBadRequest)
		return
	}

	// Parse and format response
	w.Header().Set("Content-Type", "text/plain")

	resultBytes, _ := json.Marshal(response.Result)
	var toolResult CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
		// Fallback to raw JSON if parsing fails
		w.Write(resultBytes)
		return
	}
	if toolResult.Error != nil {
		emsg := "ERROR: " + toolResult.Error.Message
		log.Printf("ERROR: Tool %s/%s returned error: %s", serverName, toolName, toolResult.Error.Message)
		w.Write([]byte(emsg))
		debugLog(s.debugMode, emsg)
		return
	}

	// Format content as markdown/plaintext
	var output strings.Builder
	for i, content := range toolResult.Content {
		if i > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(content.Text)
	}

	debugLog(s.debugMode, "Response content: %s", output.String())

	log.Printf("SUCCESS: Tool %s/%s completed successfully", serverName, toolName)
	w.Write([]byte(output.String()))
}

// statusHandler returns the status of all servers
func (s *MCPService) statusHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("# MCP Service Status\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString(fmt.Sprintf("Status: Running\n"))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

const (
	version = "1.1.0"
)

func showHelp() {
	fmt.Println("Usage: mai-wmcp [options] \"command1\" \"command2\" ...")
	fmt.Println("Options:")
	fmt.Println("  -v\tShow version information")
	fmt.Println("  -h\tShow this help message")
	fmt.Println("  -b URL\tBase URL to listen on (default: :8989)")
	fmt.Println("  -y\tYolo mode (skip tool confirmations)")
	fmt.Println("  -o FILE\tOutput report to FILE")
	fmt.Println("  -d\tEnable debug logging (shows HTTP requests and JSON payloads)")
	fmt.Println("  -c FILE\tPath to config file (default: ~/.mai-wmcp.json)")
	fmt.Println("  -n\tSkip loading config file")
	fmt.Println("Example: mai-wmcp \"r2pm -r r2mcp\" \"timemcp\"")
	fmt.Println("Example with config: mai-wmcp -c /path/to/config.json")
}

func showVersion() {
	fmt.Printf("mai-wmcp version %s\n", version)
}

// debugLog prints debug logs when debug mode is enabled
func debugLog(debug bool, format string, args ...interface{}) {
	if debug {
		log.Printf("DEBUG: "+format, args...)
	}
}

func main() {
	// Parse command line flags
	baseURL := ":8989"
	yoloMode := false
	outputReport := ""
	debugMode := false
	configPath := ""
	skipConfig := false

	args := os.Args[1:]
	cmdArgs := []string{}

	// Show help if no arguments provided
	if len(args) == 0 {
		showHelp()
		os.Exit(0)
	}

	// Process command line arguments
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if len(arg) > 0 && arg[0] == '-' {
			switch arg {
			case "-v":
				showVersion()
				os.Exit(0)
			case "-h":
				showHelp()
				os.Exit(0)
			case "-y":
				yoloMode = true
			case "-d":
				debugMode = true
			case "-c":
				if i+1 < len(args) {
					configPath = args[i+1]
					i++
				} else {
					fmt.Println("Error: -c requires a file path")
					showHelp()
					os.Exit(1)
				}
			case "-n":
				skipConfig = true
			case "-b":
				if i+1 < len(args) {
					baseURL = args[i+1]
					i++
				} else {
					fmt.Println("Error: -b requires a base URL")
					showHelp()
					os.Exit(1)
				}
			case "-o":
				if i+1 < len(args) {
					outputReport = args[i+1]
					i++
				} else {
					fmt.Println("Error: -o requires a filename")
					showHelp()
					os.Exit(1)
				}
			default:
				fmt.Printf("Unknown option: %s\n", arg)
				showHelp()
				os.Exit(1)
			}
		} else {
			cmdArgs = append(cmdArgs, arg)
		}
	}

	// Load configuration if not skipped
	var config *Config
	var configErr error
	if !skipConfig {
		config, configErr = LoadConfig(configPath)
		if configErr != nil {
			log.Printf("Warning: Failed to load config: %v", configErr)
			config = &Config{MCPServers: make(map[string]MCPServerConfig)}
		}
	} else {
		config = &Config{MCPServers: make(map[string]MCPServerConfig)}
	}

	// Check if we have any commands to run or servers in config
	cmdProvided := len(cmdArgs) > 0
	configServers := len(config.MCPServers) > 0

	if !cmdProvided && !configServers {
		fmt.Println("Error: No MCP commands provided and no servers in config")
		showHelp()
		os.Exit(1)
	}

	service := NewMCPService(yoloMode, outputReport)

	// Set debug flag
	service.debugMode = debugMode

	// Ensure cleanup on exit
	defer service.StopAllServers()

	// Start MCP servers from command line arguments
	if len(cmdArgs) > 0 {
		for _, command := range cmdArgs {
			serverName := getServerNameFromCommand(command)
			if err := service.StartServer(serverName, command); err != nil {
				log.Printf("Failed to start server %s: %v", serverName, err)
				continue
			}
		}
	}

	// Start MCP servers from config
	if !skipConfig && len(config.MCPServers) > 0 {
		StartMCPServersFromConfig(service, config)
	}
	if len(service.servers) == 0 {
		fmt.Println("Error: No MCP servers available")
		os.Exit(1)
	}

	// Setup HTTP routes
	router := mux.NewRouter()

	// List all tools
	router.HandleFunc("/tools", service.listToolsHandler).Methods("GET")
	// JSON list of all tools
	router.HandleFunc("/tools/json", service.jsonToolsHandler).Methods("GET")
	// Quiet list of all tools
	router.HandleFunc("/tools/quiet", service.quietToolsHandler).Methods("GET")
	// Markdown list of all tools
	router.HandleFunc("/tools/markdown", service.markdownToolsHandler).Methods("GET")

	// Prompts endpoints
	router.HandleFunc("/prompts", service.listPromptsHandler).Methods("GET")
	router.HandleFunc("/prompts/json", service.jsonPromptsHandler).Methods("GET")
	router.HandleFunc("/prompts/{prompt}", service.getPromptHandler).Methods("GET", "POST")
	router.HandleFunc("/prompts/{server}/{prompt}", service.getPromptHandler).Methods("GET", "POST")

	// Get service status
	router.HandleFunc("/status", service.statusHandler).Methods("GET")

	// Call a specific tool (old endpoint for backward compatibility)
	router.HandleFunc("/tools/{server}/{tool}", service.callToolHandler).Methods("GET", "POST")
	// Call a specific tool (new endpoint)
	router.HandleFunc("/call/{tool}", service.callToolHandler).Methods("GET", "POST")
	router.HandleFunc("/call/{server}/{tool}", service.callToolHandler).Methods("GET", "POST")

	// Root endpoint with usage info
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		usage := `# MCP REST Bridge

Available endpoints:

- GET /status - Service status
- GET /tools - List all available tools
- GET /tools/json - List all available tools in JSON format
- GET /tools/quiet - List all tools in minimal format
- GET /tools/markdown - List all tools in markdown format
- GET /tools/{server}/{tool}?param=value - Call tool with query parameters (legacy)
- GET /call/{server}/{tool}?param=value - Call tool with query parameters
- GET /call/{tool}?param=value - Call tool on auto-discovered server
- POST /tools/{server}/{tool} - Call tool with JSON body or form data (legacy)
- POST /call/{server}/{tool} - Call tool with JSON body or form data
 - POST /call/{tool} - Call tool with JSON body or form data (auto-discovered server)

Prompts endpoints:
- GET /prompts - List all available prompts
- GET /prompts/json - List all available prompts in JSON format
- GET /prompts/{server}/{prompt} - Get a prompt by name from a server (args as query)
- GET /prompts/{prompt} - Get a prompt by name via auto-discovery
- POST /prompts/{server}/{prompt} - Get a prompt with JSON body of arguments
- POST /prompts/{prompt} - Get a prompt with JSON body (auto-discovery)

 Examples:
 - curl http://localhost:8989/tools
 - curl http://localhost:8989/tools/json
 - curl http://localhost:8989/tools/quiet
 - curl http://localhost:8989/tools/markdown
 - curl http://localhost:8989/tools/server1/mytool?arg1=value1
  - curl -X POST http://localhost:8989/tools/server1/mytool -H "Content-Type: application/json" -d '{"arg1":"value1"}'
  - curl http://localhost:8989/prompts
  - curl http://localhost:8989/prompts/json
  - curl http://localhost:8989/prompts/server1/myPrompt?topic=xyz
  - curl -X POST http://localhost:8989/prompts/server1/myPrompt -H "Content-Type: application/json" -d '{"topic":"xyz"}'
`
		w.Write([]byte(usage))
	}).Methods("GET")

	// Start HTTP server
	if envBaseURL := os.Getenv("MAI_WMCP_BASEURL"); envBaseURL != "" {
		baseURL = envBaseURL
	}

	log.Printf("Starting MCP REST service on %s", baseURL)
	accessAddr := strings.Replace(baseURL, "0.0.0.0", "localhost", 1)
	log.Printf("Access tools at: http://%s/tools", accessAddr)

	if err := http.ListenAndServe(baseURL, router); err != nil {
		log.Fatal("Failed to start HTTP server:", err)
	}
}
