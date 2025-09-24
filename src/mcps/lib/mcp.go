package mcplib

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
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
	bufr         *bufio.Reader
	useHeaders   bool
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
		bufr:         bufio.NewReader(os.Stdin),
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
		s.bufr = bufio.NewReader(r)
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
	for {
		payload, err := s.readNextMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			// Cannot parse a request ID here; send generic parse error with null id
			s.sendError(nil, -32700, "Parse error: "+err.Error())
			continue
		}
		if s.logFile != nil && len(payload) > 0 {
			s.logFile.Write(payload)
			s.logFile.Write([]byte("\n"))
		}
		var req JSONRPCRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			s.sendError(nil, -32700, "Parse error: invalid JSON")
			continue
		}

		switch req.Method {
		case "initialize":
			// Try to echo client's protocolVersion if provided
			var initParams struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &initParams)
			proto := initParams.ProtocolVersion
			if proto == "" {
				proto = "2024-11-05"
			}
			// Advertise capabilities for tools and prompts
			caps := map[string]interface{}{"tools": map[string]interface{}{}}
			if len(s.prompts) > 0 {
				caps["prompts"] = map[string]interface{}{}
			} else {
				// Many clients still tolerate declaring prompts capability even if empty
				caps["prompts"] = map[string]interface{}{}
			}
			s.sendResult(req.ID, map[string]interface{}{
				"protocolVersion": proto,
				"capabilities":    caps,
				// Some clients expect serverInfo; include for compatibility
				"serverInfo": map[string]interface{}{
					"name":    "nsmcp",
					"version": "0.1.0",
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
}

// readNextMessage reads the next JSON-RPC message body, supporting both
// MCP/LSP-style header framing (Content-Length) and newline-delimited JSON.
func (s *MCPServer) readNextMessage() ([]byte, error) {
	if s.bufr == nil {
		s.bufr = bufio.NewReader(os.Stdin)
	}
	// Read header lines if present (order-agnostic)
	var headers []string
	// Peek one line to decide framing
	firstLine, err := s.bufr.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(firstLine) == 0 {
			return nil, io.EOF
		}
		// If we couldn't read a full line, propagate error
		if err != nil {
			return nil, err
		}
	}
	tl := strings.TrimRight(firstLine, "\r\n")
	lower := strings.ToLower(tl)
	if strings.Contains(lower, ":") && (strings.HasPrefix(lower, "content-length:") || strings.HasPrefix(lower, "content-type:")) {
		headers = append(headers, tl)
		// Keep reading headers until blank line
		for {
			h, err := s.bufr.ReadString('\n')
			if err != nil {
				return nil, err
			}
			th := strings.TrimRight(h, "\r\n")
			if strings.TrimSpace(th) == "" {
				break
			}
			headers = append(headers, th)
		}
		// Find Content-Length (case-insensitive)
		var n int = -1
		for _, h := range headers {
			parts := strings.SplitN(h, ":", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.TrimSpace(strings.ToLower(parts[0])) == "content-length" {
				v := strings.TrimSpace(parts[1])
				nn, e := strconv.Atoi(v)
				if e == nil && nn >= 0 {
					n = nn
					break
				}
			}
		}
		if n < 0 {
			return nil, fmt.Errorf("missing Content-Length header")
		}
		s.useHeaders = true
		body := make([]byte, n)
		if _, err := io.ReadFull(s.bufr, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	// Not header-framed; treat first line as JSON payload (newline-delimited JSON)
	return []byte(strings.TrimRight(firstLine, "\r\n")), nil
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
	var textOut string
	switch v := result.(type) {
	case string:
		textOut = v
	default:
		// Stringify non-string results as JSON to keep 'text' a string
		if b, e := json.MarshalIndent(v, "", "  "); e == nil {
			textOut = string(b)
		} else {
			textOut = fmt.Sprintf("%v", v)
		}
	}
	s.sendResult(req.ID, map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": textOut,
			},
		},
		"isError": false,
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
	s.writeFramed(data)
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
	s.writeFramed(data)
}

// writeFramed writes JSON payload using header framing if enabled, else newline JSON.
func (s *MCPServer) writeFramed(data []byte) {
	if s.useHeaders {
		header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
		io.WriteString(s.writer, header)
		s.writer.Write(data)
		return
	}
	// newline-delimited fallback
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

// PromptArgument represents an argument for a prompt
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptDefinition represents a prompt template the server exposes.
type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Messages    []Message        `json:"messages,omitempty"`
}

// SetPrompts configures the server's available prompts.
func (s *MCPServer) SetPrompts(prompts []PromptDefinition) {
	s.prompts = prompts
}

// prompts/list
func (s *MCPServer) handlePromptsList(req JSONRPCRequest) {
	// Return only metadata (name, description, arguments) without message bodies
	type promptMeta struct {
		Name        string           `json:"name"`
		Description string           `json:"description,omitempty"`
		Arguments   []PromptArgument `json:"arguments,omitempty"`
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
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}
	for _, p := range s.prompts {
		if p.Name == params.Name {
			messages, err := ApplyPromptDefinition(p, params.Arguments)
			if err != nil {
				s.sendError(req.ID, -32603, "failed to render prompt: "+err.Error())
				return
			}
			s.sendResult(req.ID, map[string]interface{}{
				"description": p.Description,
				"messages":    messages,
			})
			return
		}
	}
	s.sendError(req.ID, -32601, "Prompt not found: "+params.Name)
}

// prompts/apply
func (s *MCPServer) handlePromptsApply(req JSONRPCRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}
	for _, p := range s.prompts {
		if p.Name == params.Name {
			messages, err := ApplyPromptDefinition(p, params.Arguments)
			if err != nil {
				s.sendError(req.ID, -32603, "failed to render prompt: "+err.Error())
				return
			}
			s.sendResult(req.ID, map[string]interface{}{
				"description": p.Description,
				"messages":    messages,
			})
			return
		}
	}
	s.sendError(req.ID, -32601, "Prompt not found: "+params.Name)
}
