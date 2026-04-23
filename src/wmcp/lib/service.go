package wmcplib

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"
)

// Options controls how NewMCPService constructs an MCPService.
type Options struct {
	YoloMode       bool
	DrunkMode      bool
	ReportFile     string
	NoPrompts      bool
	NonInteractive bool
	SessionMode    bool
	DebugMode      bool
	ProxyToolsMode bool
	Prompter       Prompter
}

// NewMCPService creates a new MCPService. A nil opts.Prompter is legal but
// means the service will reject interactive decisions; the mai-wmcp binary
// supplies a StdinPrompter, and mai-repl supplies its TUI prompter.
func NewMCPService(opts Options) *MCPService {
	return &MCPService{
		Servers:        make(map[string]*MCPServer),
		YoloMode:       opts.YoloMode,
		DrunkMode:      opts.DrunkMode,
		NoPrompts:      opts.NoPrompts,
		NonInteractive: opts.NonInteractive,
		SessionMode:    opts.SessionMode,
		DebugMode:      opts.DebugMode,
		ProxyToolsMode: opts.ProxyToolsMode,
		prompter:       opts.Prompter,
		toolPerms:      make(map[string]ToolPermission),
		promptPerms:    make(map[string]PromptPermission),
		reportEnabled:  opts.ReportFile != "",
		reportFile:     opts.ReportFile,
		report:         Report{Entries: []ReportEntry{}},
	}
}

// SetPrompter replaces the service's Prompter. Useful when the transport
// owning the service needs to rebind prompting (e.g. after the TUI resets).
func (s *MCPService) SetPrompter(p Prompter) { s.prompter = p }

// Prompter returns the current Prompter, or nil.
func (s *MCPService) GetPrompter() Prompter { return s.prompter }

// handlePromptPermissions handles permission checking for prompt requests
func (s *MCPService) handlePromptPermissions(request JSONRPCRequest) error {
	if s.YoloMode || request.Method != "prompts/get" {
		return nil
	}

	var getPromptParams GetPromptParams
	paramsBytes, _ := json.Marshal(request.Params)
	json.Unmarshal(paramsBytes, &getPromptParams)

	argsJSON, _ := json.Marshal(getPromptParams.Arguments)

	if s.checkPromptPermission(getPromptParams.Name, string(argsJSON)) {
		return nil
	}

	decision := s.promptPromptDecision(getPromptParams.Name, string(argsJSON))

	switch decision {
	case PromptApprove:
		return nil
	case PromptReject:
		return fmt.Errorf("prompt execution rejected by user")
	case PromptPermitPromptForever, PromptPermitAllPromptsForever:
		s.YoloMode = true
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
	return nil
}

// handleToolPermissions handles tool permissions and not-found logic.
func (s *MCPService) handleToolPermissions(request JSONRPCRequest) (*JSONRPCResponse, error) {
	if s.YoloMode || request.Method != "tools/call" {
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
			customResponse, err := s.readCustomResponse("Enter your custom response: ")
			if err != nil || customResponse == "" {
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
			newParams, err := s.promptModifyTool(&callParams)
			if err != nil {
				if errors.Is(err, ErrPromptCancelled) {
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

	if s.checkToolPermission(callParams.Name, string(paramsJSON)) {
		return nil, nil
	}

	decision := s.promptYoloDecision(callParams.Name, string(paramsJSON))

	switch decision {
	case YoloApprove:
		return nil, nil
	case YoloReject:
		return nil, fmt.Errorf("tool execution rejected by user")
	case YoloPermitToolForever, YoloPermitAllToolsForever:
		s.YoloMode = true
		return nil, nil
	case YoloPermitToolWithParamsForever, YoloRejectForever:
		s.storeToolPermission(callParams.Name, string(paramsJSON), decision)
		if decision == YoloRejectForever {
			return nil, fmt.Errorf("tool execution rejected by user policy")
		}
	case YoloModify:
		newCallParams, err := s.promptModifyTool(&callParams)
		if err != nil {
			if errors.Is(err, ErrPromptCancelled) {
				return nil, fmt.Errorf("tool execution cancelled by user")
			}
			return nil, fmt.Errorf("failed to parse modified params: %v", err)
		}
		callParams = *newCallParams
		request.Params = callParams
		paramsJSON, _ = json.Marshal(callParams.Arguments)
		if s.checkToolPermission(callParams.Name, string(paramsJSON)) {
			return nil, nil
		}
		decision2 := s.promptYoloDecision(callParams.Name, string(paramsJSON))
		switch decision2 {
		case YoloApprove:
			return nil, nil
		case YoloReject:
			return nil, fmt.Errorf("tool execution rejected by user")
		case YoloPermitToolForever, YoloPermitAllToolsForever:
			s.YoloMode = true
		case YoloPermitToolWithParamsForever, YoloRejectForever:
			s.storeToolPermission(callParams.Name, string(paramsJSON), decision2)
			if decision2 == YoloRejectForever {
				return nil, fmt.Errorf("tool execution rejected by user policy")
			}
		case YoloModify:
			return nil, fmt.Errorf("multiple modifications not supported in one prompt")
		case YoloCustomToolResponse:
			customResponse, err := s.readCustomResponse("Enter your custom response: ")
			if err != nil || customResponse == "" {
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
	case YoloCustomToolResponse:
		customResponse, err := s.readCustomResponse("Enter your custom response: ")
		if err != nil || customResponse == "" {
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

	return nil, nil
}

// ApplyDrunkMode reassigns arguments for tools/call when drunk mode is on.
// Exported so HTTP / repl front-ends can run it once they have the concrete
// tool catalog available (same as the old applyDrunkMode behaviour).
func (s *MCPService) ApplyDrunkMode(request *JSONRPCRequest) {
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

	var foundTool *Tool
	for _, srv := range s.Servers {
		srv.Mutex.RLock()
		for _, tool := range srv.Tools {
			if tool.Name == callParams.Name {
				foundTool = &tool
				break
			}
		}
		srv.Mutex.RUnlock()
		if foundTool != nil {
			break
		}
	}

	if foundTool == nil || len(foundTool.Parameters) == 0 {
		return
	}

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
		debugLog(s.DebugMode, "Drunk mode: assigned single arg to first param %s", firstParam.Name)
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
		debugLog(s.DebugMode, "Drunk mode: reassigned args in order to params")
	}

	request.Params = callParams
}

// sendStdioRequest sends the request to a stdio server
func (s *MCPService) sendStdioRequest(server *MCPServer, reqBytes []byte) error {
	if server.Process.ProcessState != nil {
		log.Printf("ERROR: Server %s process has exited with state: %v", server.Name, server.Process.ProcessState)
		return fmt.Errorf("server process has exited")
	}
	if server.Process.Process != nil {
		debugLog(s.DebugMode, "Server %s process PID: %d", server.Name, server.Process.Process.Pid)
	}

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

// readStdioResponse reads the response from a stdio server with a timeout
func (s *MCPService) readStdioResponse(server *MCPServer) ([]byte, error) {
	type scanResult struct {
		ok    bool
		bytes []byte
		err   error
	}

	resultChan := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(server.Stdout)
		buf := make([]byte, 10*1024*1024)
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

// SendRequest sends a JSONRPC request to the target server (stdio or HTTP)
// and returns the response. It enforces permissions and drunk-mode rewriting
// before the request is sent downstream.
func (s *MCPService) SendRequest(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	if server.IsHTTP {
		return s.sendHTTPRequest(server, request)
	}

	if err := s.handlePromptPermissions(request); err != nil {
		return nil, err
	}

	permResponse, permErr := s.handleToolPermissions(request)
	if permErr != nil {
		return permResponse, permErr
	}

	if s.DrunkMode && request.Method == "tools/call" {
		s.ApplyDrunkMode(&request)
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	debugLog(s.DebugMode, "Sending JSONRPC request to server %s: %s", server.Name, string(reqBytes))

	if err := s.sendStdioRequest(server, reqBytes); err != nil {
		return nil, err
	}

	debugLog(s.DebugMode, "Request sent to server %s, waiting for response", server.Name)

	responseBytes, err := s.readStdioResponse(server)
	if err != nil {
		return nil, err
	}

	debugLog(s.DebugMode, "Received raw response from server %s: %s", server.Name, string(responseBytes))

	if s.DebugMode {
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, responseBytes, "", "  ") == nil {
			debugLog(s.DebugMode, "Received JSONRPC response from %s: %s", server.Name, prettyJSON.String())
		} else {
			debugLog(s.DebugMode, "Received JSONRPC response from %s: %s", server.Name, string(responseBytes))
		}
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		log.Printf("ERROR: Failed to unmarshal response from server %s: %v, raw response: %s", server.Name, err, string(responseBytes))
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if request.Method == "tools/call" {
		var toolParams CallToolParams
		paramsBytes, _ := json.Marshal(request.Params)
		json.Unmarshal(paramsBytes, &toolParams)

		log.Printf("MCP tool executed - Server: %s, Tool: %s, Params: %s",
			server.Name, toolParams.Name, string(paramsBytes))

		if s.reportEnabled {
			s.addReportEntry(server.Name, toolParams.Name, toolParams.Arguments, response.Result, nil)
		}
	}

	return &response, nil
}

// isToolAvailable checks if a tool is available in any server
func (s *MCPService) isToolAvailable(toolName string) bool {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()

	for _, server := range s.Servers {
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			if tool.Name == toolName {
				server.Mutex.RUnlock()
				return true
			}
		}
		server.Mutex.RUnlock()
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
