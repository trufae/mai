package wmcplib

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// AggregatedNameSeparator is the delimiter used to combine server and tool
// (or prompt / resource) names when the bridge exposes a single MCP view of
// all child servers.
const AggregatedNameSeparator = "::"

// EnsureSessionID lazily allocates a session identifier for the bridge.
func (s *MCPService) EnsureSessionID() string {
	s.sessionLock.Lock()
	defer s.sessionLock.Unlock()

	if s.sessionID == "" {
		s.sessionID = fmt.Sprintf("mai-wmcp-%d", time.Now().UnixNano())
	}
	return s.sessionID
}

// SnapshotServers returns a sorted list of server names together with a copy
// of the servers map. Callers can iterate without holding the service lock.
func (s *MCPService) SnapshotServers() ([]string, map[string]*MCPServer) {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()

	names := make([]string, 0, len(s.Servers))
	snapshot := make(map[string]*MCPServer, len(s.Servers))
	for name, server := range s.Servers {
		names = append(names, name)
		snapshot[name] = server
	}
	sort.Strings(names)
	return names, snapshot
}

// AggregateToolList returns the combined tool list across all servers with
// names qualified by server.
func (s *MCPService) AggregateToolList() []Tool {
	names, snapshot := s.SnapshotServers()
	tools := make([]Tool, 0)
	for _, name := range names {
		server := snapshot[name]
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			copyTool := tool
			copyTool.Name = fmt.Sprintf("%s%s%s", name, AggregatedNameSeparator, tool.Name)
			if len(copyTool.Parameters) == 0 && copyTool.InputSchema != nil {
				copyTool.Parameters = ExtractParametersFromSchema(copyTool.InputSchema)
			}
			tools = append(tools, copyTool)
		}
		server.Mutex.RUnlock()
	}
	return tools
}

// AggregatePromptList returns the combined prompt list across all servers.
func (s *MCPService) AggregatePromptList() []Prompt {
	names, snapshot := s.SnapshotServers()
	prompts := make([]Prompt, 0)
	for _, name := range names {
		server := snapshot[name]
		server.Mutex.RLock()
		for _, prompt := range server.Prompts {
			copyPrompt := prompt
			copyPrompt.Name = fmt.Sprintf("%s%s%s", name, AggregatedNameSeparator, prompt.Name)
			prompts = append(prompts, copyPrompt)
		}
		server.Mutex.RUnlock()
	}
	return prompts
}

// AggregateResourceList returns the combined resource list across all servers.
func (s *MCPService) AggregateResourceList() []Resource {
	names, snapshot := s.SnapshotServers()
	resources := make([]Resource, 0)
	for _, name := range names {
		server := snapshot[name]
		server.Mutex.RLock()
		for _, res := range server.Resources {
			copyRes := res
			copyRes.URI = fmt.Sprintf("%s%s%s", name, AggregatedNameSeparator, res.URI)
			resources = append(resources, copyRes)
		}
		server.Mutex.RUnlock()
	}
	return resources
}

func splitAggregatedIdentifier(value string) (string, string, bool) {
	if value == "" {
		return "", "", false
	}
	idx := strings.Index(value, AggregatedNameSeparator)
	if idx < 0 {
		return "", value, false
	}
	return value[:idx], value[idx+len(AggregatedNameSeparator):], true
}

func (s *MCPService) matchToolOnServer(server *MCPServer, name string) string {
	server.Mutex.RLock()
	defer server.Mutex.RUnlock()

	for _, tool := range server.Tools {
		if tool.Name == name {
			return tool.Name
		}
	}

	if !s.DrunkMode {
		return ""
	}

	if matched, ok := findBestToolMatch(server.Tools, name, true); ok {
		return matched
	}
	return ""
}

func promptExistsOnServer(server *MCPServer, name string) bool {
	server.Mutex.RLock()
	defer server.Mutex.RUnlock()

	for _, prompt := range server.Prompts {
		if prompt.Name == name {
			return true
		}
	}
	return false
}

func resourceExistsOnServer(server *MCPServer, uri string) bool {
	server.Mutex.RLock()
	defer server.Mutex.RUnlock()

	for _, res := range server.Resources {
		if res.URI == uri {
			return true
		}
	}
	return false
}

// ResolveTool resolves a (possibly aggregated) tool name back to its owning
// server and real tool name.
func (s *MCPService) ResolveTool(name string) (*MCPServer, string, error) {
	if name == "" {
		return nil, "", fmt.Errorf("tool name is required")
	}

	names, snapshot := s.SnapshotServers()
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
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			if tool.Name == name {
				if matchedServer != nil {
					server.Mutex.RUnlock()
					return nil, "", fmt.Errorf("tool '%s' is available on multiple servers; prefix with server name", name)
				}
				matchedServer = server
				matchedTool = tool.Name
				break
			}
		}
		server.Mutex.RUnlock()
	}

	if matchedServer != nil {
		return matchedServer, matchedTool, nil
	}

	if s.DrunkMode {
		var bestServer *MCPServer
		var bestTool string
		matches := 0
		for _, serverName := range names {
			server := snapshot[serverName]
			server.Mutex.RLock()
			matched, ok := findBestToolMatch(server.Tools, name, true)
			server.Mutex.RUnlock()
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

// ResolvePrompt resolves a (possibly aggregated) prompt name.
func (s *MCPService) ResolvePrompt(name string) (*MCPServer, string, error) {
	if s.NoPrompts {
		return nil, "", fmt.Errorf("prompts support not enabled")
	}
	if name == "" {
		return nil, "", fmt.Errorf("prompt name is required")
	}

	names, snapshot := s.SnapshotServers()
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
		server.Mutex.RLock()
		for _, prompt := range server.Prompts {
			if prompt.Name == name {
				if matchedServer != nil {
					server.Mutex.RUnlock()
					return nil, "", fmt.Errorf("prompt '%s' is available on multiple servers; prefix with server name", name)
				}
				matchedServer = server
				matchedPrompt = prompt.Name
				break
			}
		}
		server.Mutex.RUnlock()
	}

	if matchedServer != nil {
		return matchedServer, matchedPrompt, nil
	}

	return nil, "", fmt.Errorf("prompt '%s' not found", name)
}

// ResolveResource resolves a (possibly aggregated) resource URI.
func (s *MCPService) ResolveResource(uri string) (*MCPServer, string, error) {
	if uri == "" {
		return nil, "", fmt.Errorf("resource URI is required")
	}

	names, snapshot := s.SnapshotServers()
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

// DecodeJSONRPCParams JSON-decodes an arbitrary params value into dest.
func DecodeJSONRPCParams(src interface{}, dest interface{}) error {
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

// ProcessMCPRequest is the in-process JSON-RPC entry point. It accepts an
// incoming request addressed to the bridge itself and returns the response
// that should be written back. The boolean return value is true when the
// request was a notification (no response needed).
//
// This is the method any transport (HTTP, stdio, direct function call from
// mai-repl) should call after decoding the incoming JSON.
func (s *MCPService) ProcessMCPRequest(req JSONRPCRequest) (*JSONRPCResponse, bool) {
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: -32600, Message: "invalid jsonrpc version"},
		}, false
	}

	if req.Method == "" {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: -32600, Message: "method is required"},
		}, false
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := DecodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "invalid params"}}, false
		}
		protocol := params.ProtocolVersion
		if protocol == "" {
			protocol = "2024-11-05"
		}
		capabilities := map[string]interface{}{
			"tools": map[string]interface{}{},
		}
		if !s.NoPrompts {
			capabilities["prompts"] = map[string]interface{}{}
		}
		capabilities["resources"] = map[string]interface{}{}
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
			Result:  map[string]interface{}{"tools": s.AggregateToolList()},
		}, false
	case "tools/call":
		var params CallToolParams
		if err := DecodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "invalid params"}}, false
		}
		if params.Name == "" {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "tool name is required"}}, false
		}
		server, toolName, err := s.ResolveTool(params.Name)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: err.Error()}}, false
		}
		forward := JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "tools/call",
			Params:  CallToolParams{Name: toolName, Arguments: params.Arguments},
			ID:      req.ID,
		}
		forwardResp, forwardErr := s.SendRequest(server, forward)
		if forwardErr != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: forwardErr.Error()}}, false
		}
		if forwardResp.Error != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: forwardResp.Error}, false
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: forwardResp.Result}, false
	case "prompts/list":
		if s.NoPrompts {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32601, Message: "prompts support not enabled"}}, false
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"prompts": s.AggregatePromptList()},
		}, false
	case "prompts/get", "prompts/apply":
		if s.NoPrompts {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32601, Message: "prompts support not enabled"}}, false
		}
		var params GetPromptParams
		if err := DecodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "invalid params"}}, false
		}
		if params.Name == "" {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "prompt name is required"}}, false
		}
		server, promptName, err := s.ResolvePrompt(params.Name)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: err.Error()}}, false
		}
		params.Name = promptName
		forwardResp, forwardErr := s.SendRequest(server, JSONRPCRequest{
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
			Result:  map[string]interface{}{"resources": s.AggregateResourceList()},
		}, false
	case "resources/read":
		var params ReadResourceParams
		if err := DecodeJSONRPCParams(req.Params, &params); err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "invalid params"}}, false
		}
		if params.URI == "" {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32602, Message: "resource URI is required"}}, false
		}
		server, rawURI, err := s.ResolveResource(params.URI)
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: RPCError{Code: -32000, Message: err.Error()}}, false
		}
		forwardResp, forwardErr := s.SendRequest(server, JSONRPCRequest{
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
