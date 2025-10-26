package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func NewMCPService(yoloMode bool, drunkMode bool, reportFile string, noPrompts bool, nonInteractive bool) *MCPService {
	return &MCPService{
		servers:              make(map[string]*MCPServer),
		yoloMode:             yoloMode,
		drunkMode:            drunkMode,
		noPrompts:            noPrompts,
		nonInteractive:       nonInteractive,
		yoloToolNotFoundMode: false,
		toolPerms:            make(map[string]ToolPermission),
		promptPerms:          make(map[string]PromptPermission),
		reportEnabled:        reportFile != "",
		reportFile:           reportFile,
		report:               Report{Entries: []ReportEntry{}},
	}
}

// handlePromptPermissions handles permission checking for prompt requests
func (s *MCPService) handlePromptPermissions(request JSONRPCRequest) error {
	if s.yoloMode || request.Method != "prompts/get" {
		return nil
	}

	var getPromptParams GetPromptParams
	paramsBytes, _ := json.Marshal(request.Params)
	json.Unmarshal(paramsBytes, &getPromptParams)

	argsJSON, _ := json.Marshal(getPromptParams.Arguments)

	allowed := s.checkPromptPermission(getPromptParams.Name, string(argsJSON))
	if !allowed {
		decision := s.promptPromptDecision(getPromptParams.Name, string(argsJSON))

		switch decision {
		case PromptApprove:
			return nil
		case PromptReject:
			return fmt.Errorf("prompt execution rejected by user")
		case PromptPermitPromptForever, PromptPermitAllPromptsForever:
			s.yoloMode = true
			return nil
		case PromptPermitPromptWithArgsForever, PromptRejectForever:
			s.storePromptPermission(getPromptParams.Name, string(argsJSON), decision)
			if decision == PromptRejectForever {
				return fmt.Errorf("prompt execution rejected by user policy")
			}
		case PromptCustom:
			return fmt.Errorf("PROMPT_CUSTOM_REQUEST")
		case PromptList:
			return fmt.Errorf("PROMPT_LIST_REQUEST")
		}
	}
	return nil
}

// handleToolPermissions handles tool permissions and not found logic
func (s *MCPService) handleToolPermissions(request JSONRPCRequest) (*JSONRPCResponse, error) {
	if s.yoloMode || request.Method != "tools/call" {
		return nil, nil
	}

	var callParams CallToolParams
	paramsBytes, _ := json.Marshal(request.Params)
	json.Unmarshal(paramsBytes, &callParams)

	if !s.isToolAvailable(callParams.Name) {
		if s.yoloToolNotFoundMode {
			return nil, fmt.Errorf("tool '%s' does not exist", callParams.Name)
		}

		decision := s.promptToolNotFoundDecision(callParams.Name)

		switch decision {
		case YoloToolNotFound:
			return nil, fmt.Errorf("tool '%s' does not exist", callParams.Name)
		case YoloCustomResponse:
			fmt.Print("Enter your custom response: ")
			reader := bufio.NewReader(os.Stdin)
			customResponse, _ := reader.ReadString('\n')
			customResponse = strings.TrimSpace(customResponse)
			if customResponse == "" {
				return nil, fmt.Errorf("tool '%s' does not exist", callParams.Name)
			}
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				Result: CallToolResult{
					Content: []Content{{Type: "text", Text: customResponse}},
				},
				ID: request.ID,
			}, nil
		case YoloModify:
			fmt.Println("\nAvailable tools:")
			s.mutex.RLock()
			toolCount := 0
			for _, srv := range s.servers {
				srv.mutex.RLock()
				for _, tool := range srv.Tools {
					fmt.Printf("  %s - %s\n", tool.Name, tool.Description)
					toolCount++
				}
				srv.mutex.RUnlock()
			}
			s.mutex.RUnlock()

			if toolCount == 0 {
				return nil, fmt.Errorf("tool '%s' does not exist and no alternatives available", callParams.Name)
			}

			newParams, err := s.promptModifyTool(&callParams)
			if err != nil {
				if errors.Is(err, errToolModificationCancelled) {
					return nil, fmt.Errorf("tool execution cancelled by user")
				}
				return nil, fmt.Errorf("failed to parse modified params: %v", err)
			}

			callParams = *newParams
			request.Params = callParams

			if !s.isToolAvailable(callParams.Name) {
				return nil, fmt.Errorf("modified tool '%s' also does not exist", callParams.Name)
			}
		case YoloGuideModel:
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				Result: CallToolResult{
					Content: []Content{{Type: "text", Text: "The tool you requested doesn't exist. Please check the available tools list."}},
				},
				ID: request.ID,
			}, nil
		case YoloAlwaysRespondToolNotFound:
			s.yoloToolNotFoundMode = true
			return nil, fmt.Errorf("tool '%s' does not exist", callParams.Name)
		}
	}

	paramsJSON, _ := json.Marshal(callParams.Arguments)

	allowed := s.checkToolPermission(callParams.Name, string(paramsJSON))
	if !allowed {
		decision := s.promptYoloDecision(callParams.Name, string(paramsJSON))

		switch decision {
		case YoloApprove:
			return nil, nil
		case YoloReject:
			return nil, fmt.Errorf("tool execution rejected by user")
		case YoloPermitToolForever, YoloPermitAllToolsForever:
			s.yoloMode = true
			return nil, nil
		case YoloPermitToolWithParamsForever, YoloRejectForever:
			s.storeToolPermission(callParams.Name, string(paramsJSON), decision)
			if decision == YoloRejectForever {
				return nil, fmt.Errorf("tool execution rejected by user policy")
			}
		case YoloModify:
			newCallParams, err := s.promptModifyTool(&callParams)
			if err != nil {
				if errors.Is(err, errToolModificationCancelled) {
					return nil, fmt.Errorf("tool execution cancelled by user")
				}
				return nil, fmt.Errorf("failed to parse modified params: %v", err)
			}
			callParams = *newCallParams
			request.Params = callParams
			paramsJSON, _ = json.Marshal(callParams.Arguments)
			allowed = s.checkToolPermission(callParams.Name, string(paramsJSON))
			if !allowed {
				decision2 := s.promptYoloDecision(callParams.Name, string(paramsJSON))
				switch decision2 {
				case YoloApprove:
					return nil, nil
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
					return nil, fmt.Errorf("multiple modifications not supported in one prompt")
				case YoloCustomToolResponse:
					fmt.Print("Enter your custom response: ")
					reader := bufio.NewReader(os.Stdin)
					customResponse, _ := reader.ReadString('\n')
					customResponse = strings.TrimSpace(customResponse)
					if customResponse == "" {
						return nil, fmt.Errorf("tool execution rejected by user")
					}
					return &JSONRPCResponse{
						JSONRPC: "2.0",
						Result: CallToolResult{
							Content: []Content{{Type: "text", Text: customResponse}},
						},
						ID: request.ID,
					}, nil
				}
			}
		case YoloCustomToolResponse:
			fmt.Print("Enter your custom response: ")
			reader := bufio.NewReader(os.Stdin)
			customResponse, _ := reader.ReadString('\n')
			customResponse = strings.TrimSpace(customResponse)
			if customResponse == "" {
				return nil, fmt.Errorf("tool execution rejected by user")
			}
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				Result: CallToolResult{
					Content: []Content{{Type: "text", Text: customResponse}},
				},
				ID: request.ID,
			}, nil
		}
	}
	return nil, nil
}

// applyDrunkMode applies drunk mode parameter reassignment for tool calls
func (s *MCPService) applyDrunkMode(request *JSONRPCRequest) {
	if request.Method != "tools/call" {
		return
	}

	var callParams CallToolParams
	paramsBytes, _ := json.Marshal(request.Params)
	json.Unmarshal(paramsBytes, &callParams)

	arguments := callParams.Arguments
	if len(arguments) == 0 {
		return
	}

	// Find the tool
	var foundTool *Tool
	for _, srv := range s.servers {
		srv.mutex.RLock()
		for _, tool := range srv.Tools {
			if tool.Name == callParams.Name {
				foundTool = &tool
				break
			}
		}
		srv.mutex.RUnlock()
		if foundTool != nil {
			break
		}
	}

	if foundTool == nil || len(foundTool.Parameters) == 0 {
		return
	}

	// Reassign arguments
	numericKeys := make([]int, 0)
	numericMap := make(map[int]string)
	nonNumericKeys := make([]string, 0)

	for k := range arguments {
		if i, err := strconv.Atoi(k); err == nil {
			numericKeys = append(numericKeys, i)
			numericMap[i] = k
		} else {
			nonNumericKeys = append(nonNumericKeys, k)
		}
	}
	sort.Ints(numericKeys)
	sort.Strings(nonNumericKeys)

	argKeys := make([]string, 0, len(arguments))
	for _, i := range numericKeys {
		argKeys = append(argKeys, numericMap[i])
	}
	for _, k := range nonNumericKeys {
		argKeys = append(argKeys, k)
	}

	if len(argKeys) == 1 && len(foundTool.Parameters) > 0 {
		firstParam := foundTool.Parameters[0]
		newArgs := make(map[string]interface{})
		newArgs[firstParam.Name] = arguments[argKeys[0]]
		callParams.Arguments = newArgs
		debugLog(s.debugMode, "Drunk mode: assigned single arg to first param %s", firstParam.Name)
	} else {
		newArgs := make(map[string]interface{})
		argIndex := 0
		for _, param := range foundTool.Parameters {
			if argIndex < len(argKeys) {
				newArgs[param.Name] = arguments[argKeys[argIndex]]
				argIndex++
			}
		}
		callParams.Arguments = newArgs
		debugLog(s.debugMode, "Drunk mode: reassigned args in order to params")
	}

	request.Params = callParams
}

// sendStdioRequest sends the request to stdio server
func (s *MCPService) sendStdioRequest(server *MCPServer, reqBytes []byte) error {
	// Check if server process is still running
	if server.Process.ProcessState != nil {
		log.Printf("ERROR: Server %s process has exited with state: %v", server.Name, server.Process.ProcessState)
		return fmt.Errorf("server process has exited")
	}
	if server.Process.Process != nil {
		debugLog(s.debugMode, "Server %s process PID: %d", server.Name, server.Process.Process.Pid)
	}

	// Send request
	if _, err := server.Stdin.Write(reqBytes); err != nil {
		log.Printf("ERROR: Failed to write request to server %s stdin: %v", server.Name, err)
		return fmt.Errorf("failed to write request: %v", err)
	}
	if _, err := server.Stdin.Write([]byte("\n")); err != nil {
		log.Printf("ERROR: Failed to write newline to server %s stdin: %v", server.Name, err)
		return fmt.Errorf("failed to write newline: %v", err)
	}
	return nil
}

// readStdioResponse reads the response from stdio server with timeout
func (s *MCPService) readStdioResponse(server *MCPServer) ([]byte, error) {
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
	select {
	case result := <-resultChan:
		if !result.ok {
			if result.err != nil {
				log.Printf("ERROR: Scanner error while reading response from server %s: %v", server.Name, result.err)
			} else {
				log.Printf("ERROR: No response received from server %s (EOF or empty)", server.Name)
			}
			return nil, fmt.Errorf("failed to read response")
		}
		return result.bytes, nil
	case <-time.After(timeout):
		log.Printf("ERROR: Timeout waiting for response from server %s after %v", server.Name, timeout)
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

// sendRequest sends a JSONRPC request to the server and returns the response
func (s *MCPService) sendRequest(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	if server.IsHTTP {
		return s.sendHTTPRequest(server, request)
	}

	// Handle prompt permissions
	if err := s.handlePromptPermissions(request); err != nil {
		return nil, err
	}

	// Handle tool permissions and not found
	permResponse, permErr := s.handleToolPermissions(request)
	if permErr != nil {
		return permResponse, permErr
	}

	// Apply drunk mode if enabled
	if s.drunkMode && request.Method == "tools/call" {
		s.applyDrunkMode(&request)
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	debugLog(s.debugMode, "Sending JSONRPC request to server %s: %s", server.Name, string(reqBytes))

	err = s.sendStdioRequest(server, reqBytes)
	if err != nil {
		return nil, err
	}

	debugLog(s.debugMode, "Request sent to server %s, waiting for response", server.Name)

	responseBytes, err := s.readStdioResponse(server)
	if err != nil {
		return nil, err
	}

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

// isToolAvailable checks if a tool is available in any server
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

// addReportEntry adds an entry to the report
func (s *MCPService) addReportEntry(serverName, toolName string, params, result interface{}, err error) {
	if !s.reportEnabled {
		return
	}
	entry := ReportEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Server:    serverName,
		Tool:      toolName,
		Params:    params,
		Result:    result,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	s.report.Entries = append(s.report.Entries, entry)
}
