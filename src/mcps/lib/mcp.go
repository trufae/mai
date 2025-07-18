package mcplib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolDefinition represents a tool that can be used by the MCP
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolCallParams represents the parameters for a tool call
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// MCPServer represents an MCP server that can handle requests
type MCPServer struct {
	tools        []ToolDefinition
	toolHandlers map[string]ToolHandler
	reader       *bufio.Scanner
	writer       io.Writer
}

// ToolHandler is a function that handles a tool call
type ToolHandler func(args map[string]interface{}) (interface{}, error)

// NewMCPServer creates a new MCP server with the given tools
func NewMCPServer(tools []ToolDefinition) *MCPServer {
	return &MCPServer{
		tools:        tools,
		toolHandlers: make(map[string]ToolHandler),
		reader:       bufio.NewScanner(os.Stdin),
		writer:       os.Stdout,
	}
}

// RegisterTool registers a tool handler for a specific tool name
func (s *MCPServer) RegisterTool(name string, handler ToolHandler) {
	s.toolHandlers[name] = handler
}

// Start starts the MCP server and begins processing requests
func (s *MCPServer) Start() {
	for s.reader.Scan() {
		line := s.reader.Bytes()
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(req.ID, -32700, "Parse error: invalid JSON")
			continue
		}

		switch req.Method {
		case "initialize":
			s.sendResult(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
			})
		case "notifications/initialized":
			// This is a notification, no response needed
			continue
		case "tools/list":
			// Return tools wrapped in proper MCP format
			s.sendResult(req.ID, map[string]interface{}{
				"tools": s.tools,
			})
		case "tools/call":
			s.handleCall(req)
		default:
			s.sendError(req.ID, -32601, "Method not found: "+req.Method)
		}
	}
	if err := s.reader.Err(); err != nil {
		log.Fatalln("Error reading stdin:", err)
	}
}

// handleCall handles a tools/call request
func (s *MCPServer) handleCall(req JSONRPCRequest) {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}

	handler, exists := s.toolHandlers[params.Name]
	if !exists {
		s.sendError(req.ID, -32601, "Tool not found: "+params.Name)
		return
	}

	result, err := handler(params.Arguments)
	if err != nil {
		s.sendError(req.ID, -32000, err.Error())
		return
	}

	// Return proper MCP tools/call response format
	s.sendResult(req.ID, map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": result,
			},
		},
	})
}

// sendResult sends a successful JSON-RPC response
func (s *MCPServer) sendResult(id interface{}, result interface{}) {
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	fmt.Fprintln(s.writer, string(data))
}

// sendError sends an error JSON-RPC response
func (s *MCPServer) sendError(id interface{}, code int, message string) {
	errObj := RPCError{Code: code, Message: message}
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &errObj}
	data, _ := json.Marshal(resp)
	fmt.Fprintln(s.writer, string(data))
}
