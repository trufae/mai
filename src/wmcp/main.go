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
	Name    string
	Command string
	Process *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.ReadCloser
	Stderr  io.ReadCloser
	Tools   []Tool
	mutex   sync.RWMutex
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
	lastPart := parts[len(parts)-1]
	serverName := lastPart
	if idx := strings.LastIndex(lastPart, "/"); idx != -1 {
		serverName = lastPart[idx+1:]
	}
	return serverName
}

// StartServer starts an MCP server process
func (s *MCPService) StartServer(name, command string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Parse command string
	parts := strings.Fields(command)
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

	server := &MCPServer{
		Name:    name,
		Command: command,
		Process: cmd,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		Tools:   []Tool{},
	}

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
				"tools": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    "ai-mcpd",
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
	default:
		fmt.Println("Invalid option, defaulting to reject")
		return YoloReject
	}
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
			}
		}
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Send request
	if _, err := server.Stdin.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("failed to write request: %v", err)
	}
	if _, err := server.Stdin.Write([]byte("\n")); err != nil {
		return nil, fmt.Errorf("failed to write newline: %v", err)
	}

	// Read response
	scanner := bufio.NewScanner(server.Stdout)
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read response")
	}

	// Get the response bytes
	responseBytes := scanner.Bytes()

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
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Add to report if this is a tool call
	if s.reportEnabled && request.Method == "tools/call" {
		var toolParams CallToolParams
		paramsBytes, _ := json.Marshal(request.Params)
		json.Unmarshal(paramsBytes, &toolParams)

		s.addReportEntry(server.Name, toolParams.Name, toolParams.Arguments, response.Result, nil)
	}

	return &response, nil
}

// stopServer stops an MCP server
func (s *MCPService) stopServer(server *MCPServer) {
	if server.Process != nil {
		server.Process.Process.Kill()
		server.Process.Wait()
	}
	server.Stdin.Close()
	server.Stdout.Close()
	server.Stderr.Close()
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
	output.WriteString("# MCP Tools\n\n")

	for serverName, server := range s.servers {
		// output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		server.mutex.RLock()
		// output.WriteString(fmt.Sprintf("Executable: `%s`\n", server.Command))
		// output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))

		for _, tool := range server.Tools {
			// output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("ToolName: %s/%s\n", serverName, tool.Name))
			output.WriteString(fmt.Sprintf("Description: %s\n", tool.Description))
			if tool.InputSchema != nil {
				// schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
				// output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))

				// Print CLI-style arguments list
				output.WriteString("Arguments:\n")
				// Use the prepared Parameters array if available
				if len(tool.Parameters) > 0 {
					for _, param := range tool.Parameters {
						// Format: name=<value> : description (type) [required]
						reqText := ""
						if param.Required {
							reqText = " [required]"
						}
						output.WriteString(fmt.Sprintf("- %s=<value> : %s (%s)%s\n",
							param.Name, param.Description, param.Type, reqText))
					}
				} else if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
					// Fallback to the old way if Parameters is empty
					for key, val := range properties {
						propInfo, _ := val.(map[string]interface{})
						desc := ""
						if propInfo != nil {
							if d, ok := propInfo["description"].(string); ok {
								desc = " : " + d
							}
						}
						output.WriteString(fmt.Sprintf("- %s=<value>%s\n", key, desc))
					}
				}
				output.WriteString("\n")
			}
			// Construct usage example with parameters if available
			if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok && len(properties) > 0 {
				// Build URL with query parameters
				var params []string
				for key, _ := range properties {
					params = append(params, fmt.Sprintf("%s=value", key))
				}
				paramString := strings.Join(params, " ")
				output.WriteString(fmt.Sprintf("Usage: `mai-tool call %s %s %s`\n\n", serverName, tool.Name, paramString))
				// output.WriteString(fmt.Sprintf("**Usage:** `GET /call/%s/%s?%s`\n\n", serverName, tool.Name, paramString))
			} else {
				output.WriteString(fmt.Sprintf("Usage: `mai-tool call %s %s`\n\n", serverName, tool.Name))
				// output.WriteString(fmt.Sprintf("**Usage:** `GET /call/%s/%s`\n\n", serverName, tool.Name))
			}
			output.WriteString("----\n")
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
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
	json.NewEncoder(w).Encode(res)
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
			output.WriteString(fmt.Sprintf("%s %s", serverName, tool.Name))
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
	output.WriteString("# MCP Tools\n\n")

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
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	// Parse arguments
	arguments := make(map[string]interface{})

	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			// Parse JSON body
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
			// Parse form data
			r.ParseForm()
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

	response, err := s.sendRequest(server, toolRequest)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to call tool: %v", err), http.StatusInternalServerError)
		return
	}

	if response.Error != nil {
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
	version = "1.0.0"
)

func showHelp() {
	fmt.Println("Usage: ./mcpd [options] \"command1\" \"command2\" ...")
	fmt.Println("Options:")
	fmt.Println("  -v\tShow version information")
	fmt.Println("  -h\tShow this help message")
	fmt.Println("  -p PORT\tPort to listen on (default: 8080)")
	fmt.Println("  -y\tYolo mode (skip tool confirmations)")
	fmt.Println("  -o FILE\tOutput report to FILE")
	fmt.Println("  -d\tEnable debug logging (shows HTTP requests and JSON payloads)")
	fmt.Println("Example: ./mcpd \"r2pm -r r2mcp\" \"timemcp\"")
}

func showVersion() {
	fmt.Printf("mcpd version %s\n", version)
}

// debugLog prints debug logs when debug mode is enabled
func debugLog(debug bool, format string, args ...interface{}) {
	if debug {
		log.Printf("DEBUG: "+format, args...)
	}
}

func main() {
	// Parse command line flags
	port := "8080"
	yoloMode := false
	outputReport := ""
	debugMode := false

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
			case "-p":
				if i+1 < len(args) {
					port = args[i+1]
					i++
				} else {
					fmt.Println("Error: -p requires a port number")
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

	// Check if we have any commands to run
	if len(cmdArgs) == 0 {
		fmt.Println("Error: No MCP commands provided")
		showHelp()
		os.Exit(1)
	}

	service := NewMCPService(yoloMode, outputReport)

	// Set debug flag
	service.debugMode = debugMode

	// Ensure cleanup on exit
	defer service.StopAllServers()

	// Start all MCP servers
	// Only start servers if we have commands to run
	if len(cmdArgs) > 0 {
		for _, command := range cmdArgs {
			serverName := getServerNameFromCommand(command)
			if err := service.StartServer(serverName, command); err != nil {
				log.Printf("Failed to start server %s: %v", serverName, err)
				continue
			}
		}
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

	// Get service status
	router.HandleFunc("/status", service.statusHandler).Methods("GET")

	// Call a specific tool (old endpoint for backward compatibility)
	router.HandleFunc("/tools/{server}/{tool}", service.callToolHandler).Methods("GET", "POST")
	// Call a specific tool (new endpoint)
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
- POST /tools/{server}/{tool} - Call tool with JSON body or form data (legacy)
- POST /call/{server}/{tool} - Call tool with JSON body or form data

Examples:
- curl http://localhost:8080/tools
- curl http://localhost:8080/tools/json
- curl http://localhost:8080/tools/quiet
- curl http://localhost:8080/tools/markdown
- curl http://localhost:8080/tools/server1/mytool?arg1=value1
- curl -X POST http://localhost:8080/tools/server1/mytool -H "Content-Type: application/json" -d '{"arg1":"value1"}'
`
		w.Write([]byte(usage))
	}).Methods("GET")

	// Start HTTP server
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	log.Printf("Starting MCP REST service on port %s", port)
	log.Printf("Access tools at: http://localhost:%s/tools", port)

	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal("Failed to start HTTP server:", err)
	}
}
