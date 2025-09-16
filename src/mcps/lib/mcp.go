package mcplib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	InputSchema   map[string]interface{} `json:"inputSchema"`
	UsageExamples string                 `json:"usageExamples,omitempty"`
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
	prompts      []PromptDefinition
	logFile      io.Writer
}

// ToolHandler is a function that handles a tool call
type ToolHandler func(args map[string]interface{}) (interface{}, error)

// Tool represents a complete tool definition with handler
type Tool struct {
	Name          string
	Description   string
	InputSchema   map[string]interface{}
	UsageExamples string
	Handler       ToolHandler
}

// NewMCPServer creates a new MCP server with the given tools
func NewMCPServer(tools []ToolDefinition) *MCPServer {
	s := &MCPServer{
		tools:        tools,
		toolHandlers: make(map[string]ToolHandler),
		reader:       bufio.NewScanner(os.Stdin),
		writer:       os.Stdout,
	}
	if logfile := os.Getenv("MCPLIB_LOGFILE"); logfile != "" {
		if err := s.SetLogFile(logfile); err != nil {
			log.Printf("Failed to set MCPLIB_LOGFILE: %v", err)
		}
	}
	return s
}

// SetIO allows overriding the server's input/output streams.
// By default the server uses stdin/stdout; calling SetIO enables
// serving MCP over arbitrary io.Readers/Writers (e.g. TCP connections).
func (s *MCPServer) SetIO(r io.Reader, w io.Writer) {
	if r != nil {
		s.reader = bufio.NewScanner(r)
	}
	if w != nil {
		s.writer = w
	}
}

// SetLogFile sets the logfile for appending raw communications.
// Pass an empty string to disable logging.
func (s *MCPServer) SetLogFile(path string) error {
	if path == "" {
		s.logFile = nil
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	s.logFile = file
	return nil
}

// ServeTCP listens on the provided TCP address (host:port), accepts a
// single connection and serves MCP requests over that connection.
// Returns when the connection closes or on error.
func (s *MCPServer) ServeTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	// Use the connection for both read/write
	s.SetIO(conn, conn)
	// This will block until the connection is closed
	s.Start()
	return nil
}

// RegisterTool registers a tool handler for a specific tool name
func (s *MCPServer) RegisterTool(name string, handler ToolHandler) {
	s.toolHandlers[name] = handler
}

// Start starts the MCP server and begins processing requests
func (s *MCPServer) Start() {
	for s.reader.Scan() {
		line := s.reader.Bytes()
		if s.logFile != nil {
			s.logFile.Write(line)
			s.logFile.Write([]byte("\n"))
		}
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
					"tools":   map[string]interface{}{},
					"prompts": map[string]interface{}{},
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
		case "prompts/list":
			s.handlePromptsList(req)
		case "prompts/get":
			s.handlePromptsGet(req)
		case "prompts/apply":
			s.handlePromptsApply(req)
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
	if s.logFile != nil {
		s.logFile.Write(data)
		s.logFile.Write([]byte("\n"))
	}
	fmt.Fprintln(s.writer, string(data))
}

// sendError sends an error JSON-RPC response
func (s *MCPServer) sendError(id interface{}, code int, message string) {
	errObj := RPCError{Code: code, Message: message}
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &errObj}
	data, _ := json.Marshal(resp)
	if s.logFile != nil {
		s.logFile.Write(data)
		s.logFile.Write([]byte("\n"))
	}
	fmt.Fprintln(s.writer, string(data))
}

// -------------------- Prompts support --------------------

// Content represents a piece of message content.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Message represents a chat message used by prompts.
type Message struct {
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}

// PromptDefinition represents a prompt template the server exposes.
type PromptDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Arguments   map[string]interface{} `json:"arguments,omitempty"`
	Messages    []Message              `json:"messages,omitempty"`
}

// SetPrompts configures the server's available prompts.
func (s *MCPServer) SetPrompts(prompts []PromptDefinition) {
	s.prompts = prompts
}

// prompts/list
func (s *MCPServer) handlePromptsList(req JSONRPCRequest) {
	// Return only metadata (name, description, arguments) without message bodies
	type promptMeta struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description,omitempty"`
		Arguments   map[string]interface{} `json:"arguments,omitempty"`
	}
	list := make([]promptMeta, 0, len(s.prompts))
	for _, p := range s.prompts {
		list = append(list, promptMeta{Name: p.Name, Description: p.Description, Arguments: p.Arguments})
	}
	s.sendResult(req.ID, map[string]interface{}{"prompts": list})
}

// prompts/get
func (s *MCPServer) handlePromptsGet(req JSONRPCRequest) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}
	for _, p := range s.prompts {
		if p.Name == params.Name {
			s.sendResult(req.ID, map[string]interface{}{"prompt": p})
			return
		}
	}
	s.sendError(req.ID, -32601, "Prompt not found: "+params.Name)
}

// prompts/apply
func (s *MCPServer) handlePromptsApply(req JSONRPCRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}
	var prompt *PromptDefinition
	for i := range s.prompts {
		if s.prompts[i].Name == params.Name {
			prompt = &s.prompts[i]
			break
		}
	}
	if prompt == nil {
		s.sendError(req.ID, -32601, "Prompt not found: "+params.Name)
		return
	}

	// Apply simple {{var}} template substitution in text content
	applied := make([]Message, 0, len(prompt.Messages))
	for _, m := range prompt.Messages {
		nm := Message{Role: m.Role}
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != "" {
				text := c.Text
				for k, v := range params.Arguments {
					// Replace occurrences of {{key}} with value's string form
					placeholder := "{{" + k + "}}"
					var sval string
					switch t := v.(type) {
					case string:
						sval = t
					default:
						b, _ := json.Marshal(v)
						sval = string(b)
					}
					text = replaceAll(text, placeholder, sval)
				}
				nm.Content = append(nm.Content, Content{Type: "text", Text: text})
			} else {
				nm.Content = append(nm.Content, c)
			}
		}
		applied = append(applied, nm)
	}
	s.sendResult(req.ID, map[string]interface{}{"messages": applied})
}

// tiny helper: strings.ReplaceAll without importing strings again at top-level
func replaceAll(s, old, new string) string {
	// Inline simple implementation to avoid new imports policy churn
	// but we'll still use the standard library via fmt for safety if needed.
	if old == "" || old == new {
		return s
	}
	// Use bytes-based replace for efficiency
	bs := []byte(s)
	bo := []byte(old)
	bn := []byte(new)
	var out []byte
	for {
		i := indexOf(bs, bo)
		if i < 0 {
			out = append(out, bs...)
			break
		}
		out = append(out, bs[:i]...)
		out = append(out, bn...)
		bs = bs[i+len(bo):]
	}
	return string(out)
}

func indexOf(haystack, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	// naive search is fine for small templates
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
