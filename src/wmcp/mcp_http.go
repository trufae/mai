package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

const aggregatedNameSeparator = "::"

func (s *MCPService) registerMCPRoutes(router *mux.Router) {
	router.HandleFunc("/", s.mcpJSONRPCHandler).Methods("POST")
}

func writeJSONRPCResponse(w http.ResponseWriter, sessionID string, resp *JSONRPCResponse) {
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	w.Header().Set("Content-Type", "application/json")

	if resp == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal jsonrpc response", http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(data); err != nil {
		log.Printf("ERROR: failed to write JSONRPC response: %v", err)
	}
}

func writeJSONRPCError(w http.ResponseWriter, sessionID string, id interface{}, code int, message string) {
	writeJSONRPCResponse(w, sessionID, &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: RPCError{
			Code:    code,
			Message: message,
		},
	})
}

func (s *MCPService) mcpJSONRPCHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := s.ensureSessionID()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONRPCError(w, sessionID, nil, -32700, "failed to read request body")
		return
	}

	payload := strings.TrimSpace(string(body))
	if payload == "" {
		writeJSONRPCError(w, sessionID, nil, -32700, "empty request body")
		return
	}

	if strings.HasPrefix(payload, "[") {
		writeJSONRPCError(w, sessionID, nil, -32600, "batch requests not supported")
		return
	}

	var request JSONRPCRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		writeJSONRPCError(w, sessionID, nil, -32700, "invalid json")
		return
	}

	response, notification := s.processMCPRequest(request)
	if notification {
		if sessionID != "" {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if response == nil {
		if sessionID != "" {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	writeJSONRPCResponse(w, sessionID, response)
}

func (s *MCPService) ensureSessionID() string {
	s.sessionLock.Lock()
	defer s.sessionLock.Unlock()

	if s.sessionID == "" {
		s.sessionID = fmt.Sprintf("mai-wmcp-%d", time.Now().UnixNano())
	}
	return s.sessionID
}

func (s *MCPService) snapshotServers() ([]string, map[string]*MCPServer) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	names := make([]string, 0, len(s.servers))
	snapshot := make(map[string]*MCPServer, len(s.servers))
	for name, server := range s.servers {
		names = append(names, name)
		snapshot[name] = server
	}
	sort.Strings(names)
	return names, snapshot
}

func (s *MCPService) aggregateToolList() []Tool {
	names, snapshot := s.snapshotServers()
	tools := make([]Tool, 0)
	for _, name := range names {
		server := snapshot[name]
		server.mutex.RLock()
		for _, tool := range server.Tools {
			copyTool := tool
			copyTool.Name = fmt.Sprintf("%s%s%s", name, aggregatedNameSeparator, tool.Name)
			if len(copyTool.Parameters) == 0 && copyTool.InputSchema != nil {
				copyTool.Parameters = extractParametersFromSchema(copyTool.InputSchema)
			}
			tools = append(tools, copyTool)
		}
		server.mutex.RUnlock()
	}
	return tools
}

func (s *MCPService) aggregatePromptList() []Prompt {
	names, snapshot := s.snapshotServers()
	prompts := make([]Prompt, 0)
	for _, name := range names {
		server := snapshot[name]
		server.mutex.RLock()
		for _, prompt := range server.Prompts {
			copyPrompt := prompt
			copyPrompt.Name = fmt.Sprintf("%s%s%s", name, aggregatedNameSeparator, prompt.Name)
			prompts = append(prompts, copyPrompt)
		}
		server.mutex.RUnlock()
	}
	return prompts
}

func (s *MCPService) aggregateResourceList() []Resource {
	names, snapshot := s.snapshotServers()
	resources := make([]Resource, 0)
	for _, name := range names {
		server := snapshot[name]
		server.mutex.RLock()
		for _, res := range server.Resources {
			copyRes := res
			copyRes.URI = fmt.Sprintf("%s%s%s", name, aggregatedNameSeparator, res.URI)
			resources = append(resources, copyRes)
		}
		server.mutex.RUnlock()
	}
	return resources
}

func splitAggregatedIdentifier(value string) (string, string, bool) {
	if value == "" {
		return "", "", false
	}
	idx := strings.Index(value, aggregatedNameSeparator)
	if idx < 0 {
		return "", value, false
	}
	return value[:idx], value[idx+len(aggregatedNameSeparator):], true
}

func (s *MCPService) matchToolOnServer(server *MCPServer, name string) string {
	server.mutex.RLock()
	defer server.mutex.RUnlock()

	for _, tool := range server.Tools {
		if tool.Name == name {
			return tool.Name
		}
	}

	if !s.drunkMode {
		return ""
	}

	if matched, ok := findBestToolMatch(server.Tools, name, true); ok {
		return matched
	}
	return ""
}

func promptExistsOnServer(server *MCPServer, name string) bool {
	server.mutex.RLock()
	defer server.mutex.RUnlock()

	for _, prompt := range server.Prompts {
		if prompt.Name == name {
			return true
		}
	}
	return false
}

func resourceExistsOnServer(server *MCPServer, uri string) bool {
	server.mutex.RLock()
	defer server.mutex.RUnlock()

	for _, res := range server.Resources {
		if res.URI == uri {
			return true
		}
	}
	return false
}

func (s *MCPService) resolveTool(name string) (*MCPServer, string, error) {
	if name == "" {
		return nil, "", fmt.Errorf("tool name is required")
	}

	names, snapshot := s.snapshotServers()
	if len(snapshot) == 0 {
		return nil, "", fmt.Errorf("no MCP servers loaded")
	}

	if serverName, toolName, ok := splitAggregatedIdentifier(name); ok {
		server, exists := snapshot[serverName]
		if !exists {
			return nil, "", fmt.Errorf("server '%s' not found", serverName)
		}
		matched := s.matchToolOnServer(server, toolName)
		if matched == "" {
			return nil, "", fmt.Errorf("tool '%s' not found on server '%s'", toolName, serverName)
		}
		return server, matched, nil
	}

	var matchedServer *MCPServer
	var matchedTool string
	for _, serverName := range names {
		server := snapshot[serverName]
		server.mutex.RLock()
		for _, tool := range server.Tools {
			if tool.Name == name {
				if matchedServer != nil {
					server.mutex.RUnlock()
					return nil, "", fmt.Errorf("tool '%s' is available on multiple servers; prefix with server name", name)
				}
				matchedServer = server
				matchedTool = tool.Name
				break
			}
		}
		server.mutex.RUnlock()
	}

	if matchedServer != nil {
		return matchedServer, matchedTool, nil
	}

	if s.drunkMode {
		var bestServer *MCPServer
		var bestTool string
		matches := 0
		for _, serverName := range names {
			server := snapshot[serverName]
			server.mutex.RLock()
			matched, ok := findBestToolMatch(server.Tools, name, true)
			server.mutex.RUnlock()
			if ok {
				matches++
				if matches == 1 {
					bestServer = server
					bestTool = matched
				} else {
					return nil, "", fmt.Errorf("tool '%s' matches multiple servers; prefix with server name", name)
				}
			}
		}
		if matches == 1 {
			return bestServer, bestTool, nil
		}
	}

	return nil, "", fmt.Errorf("tool '%s' not found", name)
}

func (s *MCPService) resolvePrompt(name string) (*MCPServer, string, error) {
	if s.noPrompts {
		return nil, "", fmt.Errorf("prompts support not enabled")
	}
	if name == "" {
		return nil, "", fmt.Errorf("prompt name is required")
	}

	names, snapshot := s.snapshotServers()
	if len(snapshot) == 0 {
		return nil, "", fmt.Errorf("no MCP servers loaded")
	}

	if serverName, promptName, ok := splitAggregatedIdentifier(name); ok {
		server, exists := snapshot[serverName]
		if !exists {
			return nil, "", fmt.Errorf("server '%s' not found", serverName)
		}
		if promptName == "" {
			return nil, "", fmt.Errorf("prompt name is required")
		}
		if promptExistsOnServer(server, promptName) {
			return server, promptName, nil
		}
		return nil, "", fmt.Errorf("prompt '%s' not found on server '%s'", promptName, serverName)
	}

	var matchedServer *MCPServer
	var matchedPrompt string
	for _, serverName := range names {
		server := snapshot[serverName]
		server.mutex.RLock()
		for _, prompt := range server.Prompts {
			if prompt.Name == name {
				if matchedServer != nil {
					server.mutex.RUnlock()
					return nil, "", fmt.Errorf("prompt '%s' is available on multiple servers; prefix with server name", name)
				}
				matchedServer = server
				matchedPrompt = prompt.Name
				break
			}
		}
		server.mutex.RUnlock()
	}

	if matchedServer != nil {
		return matchedServer, matchedPrompt, nil
	}

	return nil, "", fmt.Errorf("prompt '%s' not found", name)
}

func (s *MCPService) resolveResource(uri string) (*MCPServer, string, error) {
	if uri == "" {
		return nil, "", fmt.Errorf("resource URI is required")
	}

	names, snapshot := s.snapshotServers()
	if len(snapshot) == 0 {
		return nil, "", fmt.Errorf("no MCP servers loaded")
	}

	if serverName, rawURI, ok := splitAggregatedIdentifier(uri); ok {
		server, exists := snapshot[serverName]
		if !exists {
			return nil, "", fmt.Errorf("server '%s' not found", serverName)
		}
		if rawURI == "" {
			return nil, "", fmt.Errorf("resource URI is required")
		}
		if resourceExistsOnServer(server, rawURI) {
			return server, rawURI, nil
		}
		return nil, "", fmt.Errorf("resource '%s' not found on server '%s'", rawURI, serverName)
	}

	if len(snapshot) == 1 {
		server := snapshot[names[0]]
		if resourceExistsOnServer(server, uri) {
			return server, uri, nil
		}
		return nil, "", fmt.Errorf("resource '%s' not found", uri)
	}

	return nil, "", fmt.Errorf("resource '%s' is ambiguous; prefix with server name", uri)
}

func decodeJSONRPCParams(src interface{}, dest interface{}) error {
	if src == nil {
		return nil
	}

	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, dest)
}

func (s *MCPService) processMCPRequest(req JSONRPCRequest) (*JSONRPCResponse, bool) {
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: RPCError{
				Code:    -32600,
				Message: "invalid jsonrpc version",
			},
		}, false
	}

	if req.Method == "" {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: RPCError{
				Code:    -32600,
				Message: "method is required",
			},
		}, false
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := decodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   RPCError{Code: -32602, Message: "invalid params"},
			}, false
		}
		protocol := params.ProtocolVersion
		if protocol == "" {
			protocol = "2024-11-05"
		}
		capabilities := map[string]interface{}{
			"tools": map[string]interface{}{},
		}
		if !s.noPrompts {
			capabilities["prompts"] = map[string]interface{}{}
		}
		if len(s.aggregateResourceList()) > 0 {
			capabilities["resources"] = map[string]interface{}{}
		} else {
			capabilities["resources"] = map[string]interface{}{}
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": protocol,
				"capabilities":    capabilities,
				"serverInfo": map[string]interface{}{
					"name":    "mai-wmcp-bridge",
					"version": MaiVersion,
				},
			},
		}, false
	case "notifications/initialized":
		return nil, true
	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.aggregateToolList(),
			},
		}, false
	case "tools/call":
		var params CallToolParams
		if err := decodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   RPCError{Code: -32602, Message: "invalid params"},
			}, false
		}
		if params.Name == "" {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   RPCError{Code: -32602, Message: "tool name is required"},
			}, false
		}
		server, toolName, err := s.resolveTool(params.Name)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: err.Error()}}, false
		}
		forward := JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "tools/call",
			Params: CallToolParams{
				Name:      toolName,
				Arguments: params.Arguments,
			},
			ID: req.ID,
		}
		forwardResp, forwardErr := s.sendRequest(server, forward)
		if forwardErr != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: forwardErr.Error()}}, false
		}
		if forwardResp.Error != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: forwardResp.Error}, false
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: forwardResp.Result}, false
	case "prompts/list":
		if s.noPrompts {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32601, Message: "prompts support not enabled"}}, false
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"prompts": s.aggregatePromptList(),
			},
		}, false
	case "prompts/get", "prompts/apply":
		if s.noPrompts {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32601, Message: "prompts support not enabled"}}, false
		}
		var params GetPromptParams
		if err := decodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "invalid params"}}, false
		}
		if params.Name == "" {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "prompt name is required"}}, false
		}
		server, promptName, err := s.resolvePrompt(params.Name)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: err.Error()}}, false
		}
		params.Name = promptName
		forwardResp, forwardErr := s.sendRequest(server, JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  req.Method,
			Params:  params,
			ID:      req.ID,
		})
		if forwardErr != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: forwardErr.Error()}}, false
		}
		if forwardResp.Error != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: forwardResp.Error}, false
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: forwardResp.Result}, false
	case "resources/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"resources": s.aggregateResourceList(),
			},
		}, false
	case "resources/read":
		var params ReadResourceParams
		if err := decodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "invalid params"}}, false
		}
		if params.URI == "" {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "resource URI is required"}}, false
		}
		server, rawURI, err := s.resolveResource(params.URI)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: err.Error()}}, false
		}
		forwardResp, forwardErr := s.sendRequest(server, JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "resources/read",
			Params:  ReadResourceParams{URI: rawURI},
			ID:      req.ID,
		})
		if forwardErr != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: forwardErr.Error()}}, false
		}
		if forwardResp.Error != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: forwardResp.Error}, false
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: forwardResp.Result}, false
	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}, false
	}
}
