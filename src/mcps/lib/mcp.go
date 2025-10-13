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
	tools             []ToolDefinition
	toolHandlers      map[string]ToolHandler
	streamingHandlers map[string]StreamingToolHandler
	reader            *bufio.Scanner
	writer            io.Writer
	prompts           []PromptDefinition
	logFile           io.Writer
	bufr              *bufio.Reader
	useHeaders        bool
}

// ToolHandler is a function that handles a tool call
type ToolHandler func(args map[string]interface{}) (interface{}, error)

// ToolCallResult is a convenience type for handlers to return structured content
type ToolCallResult struct {
	Content       interface{} `json:"content,omitempty"`
	IsError       bool        `json:"isError,omitempty"`
	Page          int         `json:"page,omitempty"`
	TotalPages    int         `json:"totalPages,omitempty"`
	NextPageToken string      `json:"next_page_token,omitempty"`
}

// StreamingToolHandler is a special handler that can return streaming results
type StreamingToolHandler func(args map[string]interface{}, sendChunk func(ToolCallResult) error) (ToolCallResult, error)

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
		tools:             tools,
		toolHandlers:      make(map[string]ToolHandler),
		streamingHandlers: make(map[string]StreamingToolHandler),
		reader:            bufio.NewScanner(os.Stdin),
		writer:            os.Stdout,
		bufr:              bufio.NewReader(os.Stdin),
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

// RegisterStreamingTool registers a streaming tool handler for a specific tool name
func (s *MCPServer) RegisterStreamingTool(name string, handler StreamingToolHandler) {
	s.streamingHandlers[name] = handler
}

// handleStreamingCall handles streaming tool calls
func (s *MCPServer) handleStreamingCall(req JSONRPCRequest, params ToolCallParams, handler StreamingToolHandler) {
	// For streaming, we send the result directly
	result, err := handler(params.Arguments, func(chunk ToolCallResult) error {
		// For now, just send the chunk as the result
		// In a real streaming implementation, this would send multiple responses
		out := map[string]interface{}{"isError": chunk.IsError}
		if chunk.Content != nil {
			out["content"] = chunk.Content
		}
		if chunk.NextPageToken != "" {
			out["next_page_token"] = chunk.NextPageToken
		}
		s.sendResult(req.ID, out)
		return nil
	})
	if err != nil {
		s.sendError(req.ID, -32000, err.Error())
		return
	}

	// Send the final result
	out := map[string]interface{}{"isError": result.IsError}
	if result.Content != nil {
		out["content"] = result.Content
	}
	if result.NextPageToken != "" {
		out["next_page_token"] = result.NextPageToken
	}
	s.sendResult(req.ID, out)
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

	// Check for streaming handler first
	if streamingHandler, exists := s.streamingHandlers[params.Name]; exists {
		s.handleStreamingCall(req, params, streamingHandler)
		return
	}

	// Fall back to regular handler
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

	// Allow handlers to return rich MCP tool results so servers can include
	// attachments/resources in their tool responses. Accepted return types:
	// - string: treated as simple text content (backwards compatible)
	// - ToolCallResult: library-native rich result
	// - map[string]interface{} that already contains a "content" key: passed through

	switch v := result.(type) {
	case string:
		s.sendResult(req.ID, map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": v}},
			"isError": false,
		})
	case ToolCallResult:
		// If handler returned the convenience struct, forward as-is
		out := map[string]interface{}{"isError": v.IsError}
		if v.Content != nil {
			out["content"] = v.Content
		}
		s.sendResult(req.ID, out)
	case map[string]interface{}:
		// If the handler already returned a map with content, pass-through
		if _, ok := v["content"]; ok {
			// Ensure isError present
			if _, eok := v["isError"]; !eok {
				v["isError"] = false
			}
			s.sendResult(req.ID, v)
			return
		}
		// Fallback: stringify the map into a text block
		if b, e := json.MarshalIndent(v, "", "  "); e == nil {
			s.sendResult(req.ID, map[string]interface{}{
				"content": []interface{}{map[string]interface{}{"type": "text", "text": string(b)}},
				"isError": false,
			})
			return
		}
		s.sendResult(req.ID, map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": fmt.Sprintf("%v", v)}},
			"isError": false,
		})
	default:
		// Stringify unknown results as JSON to keep 'text' a string
		if b, e := json.MarshalIndent(v, "", "  "); e == nil {
			s.sendResult(req.ID, map[string]interface{}{
				"content": []interface{}{map[string]interface{}{"type": "text", "text": string(b)}},
				"isError": false,
			})
			return
		}
		s.sendResult(req.ID, map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": fmt.Sprintf("%v", v)}},
			"isError": false,
		})
	}
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

// writeFramed writes JSON payload using header framing.
func (s *MCPServer) writeFramed(data []byte) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	io.WriteString(s.writer, header)
	s.writer.Write(data)
}

// -------------------- MCP Client support --------------------

// MCPClient represents an MCP client that can call tools
type MCPClient struct {
	writer     io.Writer
	reader     *bufio.Scanner
	bufr       *bufio.Reader
	requestID  int
	useHeaders bool
}

// NewMCPClient creates a new MCP client with stdin/stdout
func NewMCPClient() *MCPClient {
	return &MCPClient{
		writer:    os.Stdout,
		reader:    bufio.NewScanner(os.Stdin),
		bufr:      bufio.NewReader(os.Stdin),
		requestID: 1,
	}
}

// SetIO allows overriding the client's input/output streams
func (c *MCPClient) SetIO(r io.Reader, w io.Writer) {
	if r != nil {
		c.reader = bufio.NewScanner(r)
		c.bufr = bufio.NewReader(r)
	}
	if w != nil {
		c.writer = w
	}
}

// Initialize sends the initialize request and waits for response
func (c *MCPClient) Initialize() error {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.requestID,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion": "2024-11-05"}`),
	}
	c.requestID++

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.writeFramed(data)

	// Read response
	respData, err := c.readNextMessage()
	if err != nil {
		return err
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("failed to parse JSON response: %v", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	// Send initialized notification
	notif := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	notifData, err := json.Marshal(notif)
	if err != nil {
		return err
	}
	c.writeFramed(notifData)

	return nil
}

// ListTools sends the tools/list request and returns available tools
func (c *MCPClient) ListTools() ([]ToolDefinition, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.requestID,
		Method:  "tools/list",
	}
	c.requestID++

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	c.writeFramed(data)

	// Read response
	respData, err := c.readNextMessage()
	if err != nil {
		return nil, err
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	// Parse tools from result
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid tools/list response")
	}

	toolsData, ok := result["tools"]
	if !ok {
		return nil, fmt.Errorf("no tools in response")
	}

	toolsJSON, err := json.Marshal(toolsData)
	if err != nil {
		return nil, err
	}

	var tools []ToolDefinition
	if err := json.Unmarshal(toolsJSON, &tools); err != nil {
		return nil, err
	}

	return tools, nil
}

// CallTool sends a tools/call request and returns the result
func (c *MCPClient) CallTool(name string, args map[string]interface{}) (ToolCallResult, error) {
	params := ToolCallParams{
		Name:      name,
		Arguments: args,
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return ToolCallResult{IsError: true}, err
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.requestID,
		Method:  "tools/call",
		Params:  paramsJSON,
	}
	c.requestID++

	data, err := json.Marshal(req)
	if err != nil {
		return ToolCallResult{IsError: true}, err
	}
	c.writeFramed(data)

	// Read response
	respData, err := c.readNextMessage()
	if err != nil {
		return ToolCallResult{IsError: true}, err
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return ToolCallResult{IsError: true}, err
	}

	if resp.Error != nil {
		return ToolCallResult{IsError: true}, fmt.Errorf("tool call error: %s", resp.Error.Message)
	}

	// Parse result
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		return ToolCallResult{IsError: true}, fmt.Errorf("invalid tool call response")
	}

	var toolResult ToolCallResult
	if isError, ok := result["isError"].(bool); ok {
		toolResult.IsError = isError
	}
	if content, ok := result["content"]; ok {
		toolResult.Content = content
	}
	if nextPageToken, ok := result["next_page_token"].(string); ok {
		toolResult.NextPageToken = nextPageToken
	}

	return toolResult, nil
}

// readNextMessage reads the next JSON-RPC message body
func (c *MCPClient) readNextMessage() ([]byte, error) {
	if c.bufr == nil {
		c.bufr = bufio.NewReader(os.Stdin)
	}
	// Read header lines if present
	var headers []string
	firstLine, err := c.bufr.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(firstLine) == 0 {
			return nil, io.EOF
		}
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
			h, err := c.bufr.ReadString('\n')
			if err != nil {
				return nil, err
			}
			th := strings.TrimRight(h, "\r\n")
			if strings.TrimSpace(th) == "" {
				break
			}
			headers = append(headers, th)
		}
		// Find Content-Length
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
		c.useHeaders = true
		body := make([]byte, n)
		if _, err := io.ReadFull(c.bufr, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	// Not header-framed; treat first line as JSON payload
	return []byte(strings.TrimRight(firstLine, "\r\n")), nil
}

// writeFramed writes JSON payload using header framing
func (c *MCPClient) writeFramed(data []byte) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	io.WriteString(c.writer, header)
	c.writer.Write(data)
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
