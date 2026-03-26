package mcplib

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// ListenConfig represents the parsed configuration from a listen string
type ListenConfig struct {
	Protocol string // "tcp", "http", or "sse"
	Address  string // For TCP: the full host:port string
	Port     string // For HTTP/SSE: the port number
	BasePath string // For HTTP/SSE: the base path (e.g., "/mcp")
}

func parseURLListenConfig(protocol string, listen string) (ListenConfig, error) {
	parts := strings.SplitN(listen, "://", 2)
	if len(parts) != 2 {
		return ListenConfig{}, fmt.Errorf("invalid %s URL format", strings.ToUpper(protocol))
	}
	hostAndPath := parts[1]
	hostPort := hostAndPath
	basePath := "/"
	if idx := strings.Index(hostAndPath, "/"); idx != -1 {
		hostPort = hostAndPath[:idx]
		basePath = hostAndPath[idx:]
	}
	port := "80"
	if idx := strings.LastIndex(hostPort, ":"); idx != -1 {
		port = hostPort[idx+1:]
	}
	return ListenConfig{
		Protocol: protocol,
		Port:     port,
		BasePath: basePath,
	}, nil
}

// ParseListenString parses a listen string into protocol, address/port, and base path
func ParseListenString(listen string) (ListenConfig, error) {
	if listen == "" {
		return ListenConfig{}, fmt.Errorf("empty listen string")
	}

	if strings.HasPrefix(listen, "http://") || strings.HasPrefix(listen, "https://") {
		return parseURLListenConfig("http", listen)
	}
	if strings.HasPrefix(listen, "sse://") {
		return parseURLListenConfig("sse", listen)
	}
	// TCP mode (default)
	return ListenConfig{
		Protocol: "tcp",
		Address:  listen,
	}, nil
}

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

// sseSession keeps the reusable state for an active SSE session.
type sseSession struct {
	bearerToken string
	authResult  *AuthResult
	respChan    chan JSONRPCResponse
}

// messageFramer centralizes JSON-RPC transport framing shared by server and client.
type messageFramer struct {
	input      io.Reader
	writer     io.Writer
	bufr       *bufio.Reader
	useHeaders bool
}

// readNextMessage reads the next JSON-RPC message body, supporting both
// MCP/LSP-style header framing (Content-Length) and newline-delimited JSON.
func (f *messageFramer) readNextMessage() ([]byte, error) {
	if f.input == nil {
		f.input = os.Stdin
	}
	if f.bufr == nil {
		f.bufr = bufio.NewReader(f.input)
	}
	var headers []string
	firstLine, err := f.bufr.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(firstLine) == 0 {
			return nil, io.EOF
		}
		return nil, err
	}
	tl := strings.TrimRight(firstLine, "\r\n")
	lower := strings.ToLower(tl)
	if strings.Contains(lower, ":") && (strings.HasPrefix(lower, "content-length:") || strings.HasPrefix(lower, "content-type:")) {
		headers = append(headers, tl)
		for {
			h, err := f.bufr.ReadString('\n')
			if err != nil {
				return nil, err
			}
			th := strings.TrimRight(h, "\r\n")
			if strings.TrimSpace(th) == "" {
				break
			}
			headers = append(headers, th)
		}
		n := -1
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
		f.useHeaders = true
		body := make([]byte, n)
		if _, err := io.ReadFull(f.bufr, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	return []byte(strings.TrimRight(firstLine, "\r\n")), nil
}

// writeFramed writes JSON payload using header framing if enabled, else newline JSON.
func (f messageFramer) writeFramed(data []byte) {
	if f.useHeaders {
		header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
		_, _ = io.WriteString(f.writer, header)
		_, _ = f.writer.Write(data)
		return
	}
	_, _ = fmt.Fprintln(f.writer, string(data))
}

// MCPServer represents an MCP server that can handle requests
type MCPServer struct {
	tools                  []ToolDefinition
	toolHandlers           map[string]ToolHandler
	toolHandlersWithCtx    map[string]ToolHandlerWithContext
	streamingHandlers      map[string]StreamingToolHandler
	input                  io.Reader
	reader                 *bufio.Scanner
	writer                 io.Writer
	prompts                []PromptDefinition
	resources              []ResourceDefinition
	resourceHandlers       map[string]ResourceHandler
	logFile                io.Writer
	bufr                   *bufio.Reader
	useHeaders             bool
	authEnabled            bool
	sseConnections         map[string]chan JSONRPCResponse // SSE connection management
	sseSessions            map[string]*sseSession          // Reusable SSE session state
	sseMu                  sync.RWMutex                    // Protects SSE connection/session state
	currentCtx             context.Context                 // Current request context (for stdio mode)
	authenticator          AuthenticatorFunc               // Optional token validator/transformer
	verbose                bool                            // Enable verbose logging for HTTP mode
	responseMode           ResponseMode                    // Controls content/structuredContent in responses
	maxHTTPRequestBodySize int64                           // Maximum allowed HTTP request body size in bytes
}

// ToolHandler is a function that handles a tool call (legacy, no context)
type ToolHandler func(args map[string]interface{}) (interface{}, error)

// ToolHandlerWithContext is a function that handles a tool call with context
type ToolHandlerWithContext func(ctx context.Context, args map[string]interface{}) (interface{}, error)

// AuthenticatorFunc is a callback that validates/transforms an incoming bearer token.
// It receives the request context and raw token from the Authorization header and should return:
// - (*AuthResult, nil) on success: auth state will be attached to the request context
// - (nil, error) on failure: request will be rejected with 401
// This allows custom authentication logic (e.g., validating against external service,
// exchanging public tokens for internal tokens, etc.)
type AuthenticatorFunc func(ctx context.Context, token string) (*AuthResult, error)

// LegacyAuthenticatorFunc is the older authenticator signature without context.
// Use SetAuthenticatorWithContext for new integrations.
type LegacyAuthenticatorFunc func(token string) (string, error)

// AuthContextValue describes a context key/value pair to attach after authentication.
type AuthContextValue struct {
	Key   any
	Value any
}

// AuthResult contains the auth-derived values that should be attached to the request context.
// APIToken is also exported via GetAPIToken for tool handlers that consume bearer tokens.
type AuthResult struct {
	APIToken      string
	ContextValues []AuthContextValue
}

// ResponseMode controls how tool results are formatted in responses
type ResponseMode int

const (
	ResponseModeContent    ResponseMode = iota // Only content (text) - default for backwards compatibility
	ResponseModeStructured                     // Only structuredContent (JSON object)
	ResponseModeBoth                           // Both content and structuredContent
)

const defaultMaxHTTPRequestBodySize int64 = 1024 * 1024

// ToolCallResult is a convenience type for handlers to return structured content
type ToolCallResult struct {
	Content           interface{} `json:"content,omitempty"`
	StructuredContent interface{} `json:"structuredContent,omitempty"`
	IsError           bool        `json:"isError,omitempty"`
	Page              int         `json:"page,omitempty"`
	TotalPages        int         `json:"totalPages,omitempty"`
	NextPageToken     string      `json:"next_page_token,omitempty"`
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

// ToolWithContext represents a complete tool definition with context-aware handler
type ToolWithContext struct {
	Name          string
	Description   string
	InputSchema   map[string]interface{}
	UsageExamples string
	Handler       ToolHandlerWithContext
}

// ResourceDefinition represents a resource that can be read by the MCP
type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceReadParams represents the parameters for a resource read request
type ResourceReadParams struct {
	URI string `json:"uri"`
}

// ResourceHandler is a function that handles a resource read request
type ResourceHandler func(uri string) (interface{}, error)

// Resource represents a complete resource definition with handler
type Resource struct {
	URI         string
	Name        string
	Description string
	MimeType    string
	Handler     ResourceHandler
}

// NewMCPServer creates a new MCP server with the given tools
func NewMCPServer(tools []ToolDefinition) *MCPServer {
	s := &MCPServer{
		tools:                  tools,
		toolHandlers:           make(map[string]ToolHandler),
		toolHandlersWithCtx:    make(map[string]ToolHandlerWithContext),
		streamingHandlers:      make(map[string]StreamingToolHandler),
		resourceHandlers:       make(map[string]ResourceHandler),
		resources:              make([]ResourceDefinition, 0),
		sseConnections:         make(map[string]chan JSONRPCResponse),
		sseSessions:            make(map[string]*sseSession),
		currentCtx:             context.Background(),
		maxHTTPRequestBodySize: defaultMaxHTTPRequestBodySize,
	}
	if logfile := os.Getenv("MCPLIB_LOGFILE"); logfile != "" {
		if err := s.SetLogFile(logfile); err != nil {
			log.Printf("Failed to set MCPLIB_LOGFILE: %v", err)
		}
	}
	return s
}

// processPromptsList handles prompts/list
func (s *MCPServer) processPromptsList(req JSONRPCRequest) JSONRPCResponse {
	type promptMeta struct {
		Name        string           `json:"name"`
		Description string           `json:"description,omitempty"`
		Arguments   []PromptArgument `json:"arguments,omitempty"`
	}
	list := make([]promptMeta, 0, len(s.prompts))
	for _, p := range s.prompts {
		list = append(list, promptMeta{Name: p.Name, Description: p.Description, Arguments: p.Arguments})
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{"prompts": list},
	}
}

// processPromptsGet handles prompts/get
func (s *MCPServer) processPromptsGet(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params"},
		}
	}
	for _, p := range s.prompts {
		if p.Name == params.Name {
			messages, err := ApplyPromptDefinition(p, params.Arguments)
			if err != nil {
				return JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &RPCError{Code: -32603, Message: "failed to render prompt: " + err.Error()},
				}
			}
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"description": p.Description,
					"messages":    messages,
				},
			}
		}
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &RPCError{Code: -32601, Message: "Prompt not found: " + params.Name},
	}
}

// processPromptsApply handles prompts/apply
func (s *MCPServer) processPromptsApply(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params"},
		}
	}
	for _, p := range s.prompts {
		if p.Name == params.Name {
			messages, err := ApplyPromptDefinition(p, params.Arguments)
			if err != nil {
				return JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &RPCError{Code: -32603, Message: "failed to render prompt: " + err.Error()},
				}
			}
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"description": p.Description,
					"messages":    messages,
				},
			}
		}
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &RPCError{Code: -32601, Message: "Prompt not found: " + params.Name},
	}
}

// processResourcesList handles resources/list
func (s *MCPServer) processResourcesList(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"resources": s.resources,
		},
	}
}

// processResourcesRead handles resources/read
func (s *MCPServer) processResourcesRead(req JSONRPCRequest) JSONRPCResponse {
	var params ResourceReadParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.URI == "" {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params"},
		}
	}
	handler, exists := s.resourceHandlers[params.URI]
	if !exists {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "Resource not found: " + params.URI},
		}
	}
	content, err := handler(params.URI)
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32000, Message: err.Error()},
		}
	}
	var contents []interface{}
	switch v := content.(type) {
	case string:
		contents = []interface{}{map[string]interface{}{
			"uri":      params.URI,
			"mimeType": "text/plain",
			"text":     v,
		}}
	case []byte:
		contents = []interface{}{map[string]interface{}{
			"uri":      params.URI,
			"mimeType": "application/octet-stream",
			"blob":     string(v),
		}}
	default:
		if b, e := json.MarshalIndent(v, "", "  "); e == nil {
			contents = []interface{}{map[string]interface{}{
				"uri":      params.URI,
				"mimeType": "application/json",
				"text":     string(b),
			}}
		} else {
			contents = []interface{}{map[string]interface{}{
				"uri":      params.URI,
				"mimeType": "text/plain",
				"text":     fmt.Sprintf("%v", v),
			}}
		}
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"contents": contents,
		},
	}
}

// SetIO allows overriding the server's input/output streams.
// By default the server uses stdin/stdout; calling SetIO enables
// serving MCP over arbitrary io.Readers/Writers (e.g. TCP connections).
func (s *MCPServer) SetIO(r io.Reader, w io.Writer) {
	if r != nil {
		s.input = r
		s.reader = bufio.NewScanner(r)
		s.bufr = bufio.NewReader(r)
	}
	if w != nil {
		s.writer = w
	}
}

func (s *MCPServer) ensureDefaultIO() {
	if s.input == nil {
		s.input = os.Stdin
	}
	if s.writer == nil {
		s.writer = os.Stdout
	}
	if s.bufr == nil {
		s.bufr = bufio.NewReader(s.input)
	}
	if s.reader == nil {
		s.reader = bufio.NewScanner(s.input)
	}
}

func (s *MCPServer) newConnectionServer(rw io.ReadWriter) *MCPServer {
	connServer := &MCPServer{
		tools:                  s.tools,
		toolHandlers:           s.toolHandlers,
		toolHandlersWithCtx:    s.toolHandlersWithCtx,
		streamingHandlers:      s.streamingHandlers,
		prompts:                s.prompts,
		resources:              s.resources,
		resourceHandlers:       s.resourceHandlers,
		logFile:                s.logFile,
		authEnabled:            s.authEnabled,
		sseConnections:         make(map[string]chan JSONRPCResponse),
		sseSessions:            make(map[string]*sseSession),
		currentCtx:             s.currentCtx,
		authenticator:          s.authenticator,
		verbose:                s.verbose,
		responseMode:           s.responseMode,
		maxHTTPRequestBodySize: s.maxHTTPRequestBodySize,
	}
	connServer.SetIO(rw, rw)
	return connServer
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

// ServeTCP listens on the provided TCP address (host:port) and serves MCP
// requests for each accepted TCP connection until the listener fails.
func (s *MCPServer) ServeTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(conn net.Conn) {
			defer func() { _ = conn.Close() }()
			s.newConnectionServer(conn).Start()
		}(conn)
	}
}

// RegisterTool registers a tool handler for a specific tool name (legacy, no context)
func (s *MCPServer) RegisterTool(name string, handler ToolHandler) {
	s.toolHandlers[name] = handler
}

// RegisterToolWithContext registers a context-aware tool handler for a specific tool name
func (s *MCPServer) RegisterToolWithContext(name string, handler ToolHandlerWithContext) {
	s.toolHandlersWithCtx[name] = handler
}

// SetContext sets the default context for stdio mode (used to inject env token)
func (s *MCPServer) SetContext(ctx context.Context) {
	s.currentCtx = ctx
}

// SetAuthenticator sets a custom authentication callback that validates/transforms
// incoming bearer tokens. This compatibility wrapper adapts the legacy signature.
// Use SetAuthenticatorWithContext for request-scoped authentication flows.
func (s *MCPServer) SetAuthenticator(fn LegacyAuthenticatorFunc) {
	if fn == nil {
		s.authenticator = nil
		return
	}
	s.authenticator = func(ctx context.Context, token string) (*AuthResult, error) {
		internalToken, err := fn(token)
		if err != nil {
			return nil, err
		}
		return &AuthResult{APIToken: internalToken}, nil
	}
}

// SetAuthenticatorWithContext sets a custom authentication callback that validates/transforms
// incoming bearer tokens. The callback receives the request context and raw token and returns either:
// - (*AuthResult, nil): auth state is attached to the request context for tool handlers
// - (nil, error): the request is rejected with 401 Unauthorized
// This enables custom auth flows like validating against external services or
// exchanging public tokens for internal API tokens.
func (s *MCPServer) SetAuthenticatorWithContext(fn AuthenticatorFunc) {
	s.authenticator = fn
}

// SetVerbose enables or disables verbose logging for HTTP mode
func (s *MCPServer) SetVerbose(verbose bool) {
	s.verbose = verbose
}

// SetMaxHTTPRequestBodySize sets the maximum accepted HTTP request body size in bytes.
// Non-positive values restore the default limit of 1 MiB.
func (s *MCPServer) SetMaxHTTPRequestBodySize(limit int64) {
	if limit <= 0 {
		s.maxHTTPRequestBodySize = defaultMaxHTTPRequestBodySize
		return
	}
	s.maxHTTPRequestBodySize = limit
}

// SetResponseMode sets how tool results are formatted in responses:
// - ResponseModeContent: only content (text) - default for backwards compatibility
// - ResponseModeStructured: only structuredContent (JSON object)
// - ResponseModeBoth: both content and structuredContent
func (s *MCPServer) SetResponseMode(mode ResponseMode) {
	s.responseMode = mode
}

// authenticate validates/transforms a token using the authenticator callback.
// If no authenticator is set, the raw token is forwarded as the API token.
func (s *MCPServer) authenticate(ctx context.Context, token string) (*AuthResult, error) {
	if s.authenticator == nil {
		return &AuthResult{APIToken: token}, nil
	}
	return s.authenticator(ctx, token)
}

// RegisterStreamingTool registers a streaming tool handler for a specific tool name
func (s *MCPServer) RegisterStreamingTool(name string, handler StreamingToolHandler) {
	s.streamingHandlers[name] = handler
}

// RegisterResource registers a resource handler for a specific URI
func (s *MCPServer) RegisterResource(uri string, handler ResourceHandler) {
	s.resourceHandlers[uri] = handler
}

// SetResources configures the server's available resources
func (s *MCPServer) SetResources(resources []ResourceDefinition) {
	if resources == nil {
		// Never store nil; keep it as an empty slice so it encodes to [] not null
		s.resources = make([]ResourceDefinition, 0)
		return
	}
	s.resources = resources
}

// Start starts the MCP server and begins processing requests
func (s *MCPServer) Start() {
	s.ensureDefaultIO()
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
			if _, err := s.logFile.Write(append(payload, '\n')); err != nil {
				log.Printf("Failed to write to log file: %v", err)
			}
		}
		var req JSONRPCRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			s.sendError(nil, -32700, "Parse error: invalid JSON")
			continue
		}

		resp := s.processRequest(req)
		if resp.ID != nil {
			s.sendResponse(resp)
		}
	}
}

func (s *MCPServer) messageFramer() messageFramer {
	return messageFramer{
		input:      s.input,
		writer:     s.writer,
		bufr:       s.bufr,
		useHeaders: s.useHeaders,
	}
}

// readNextMessage reads the next JSON-RPC message body, supporting both
// MCP/LSP-style header framing (Content-Length) and newline-delimited JSON.
func (s *MCPServer) readNextMessage() ([]byte, error) {
	framer := s.messageFramer()
	body, err := framer.readNextMessage()
	s.bufr = framer.bufr
	s.useHeaders = framer.useHeaders
	return body, err
}

// sendError sends an error JSON-RPC response
func (s *MCPServer) sendError(id interface{}, code int, message string) {
	errObj := RPCError{Code: code, Message: message}
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &errObj}
	data, _ := json.Marshal(resp)
	if s.logFile != nil {
		if _, err := s.logFile.Write(append(data, '\n')); err != nil {
			log.Printf("Failed to write to log file: %v", err)
		}
	}
	s.writeFramed(data)
}

// writeFramed writes JSON payload using header framing if enabled, else newline JSON.
func (s *MCPServer) writeFramed(data []byte) {
	s.messageFramer().writeFramed(data)
}

// sendResponse sends a JSON-RPC response
func (s *MCPServer) sendResponse(resp JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		// Fallback
		s.sendError(nil, -32700, "Failed to marshal response")
		return
	}
	if s.logFile != nil {
		if _, err := s.logFile.Write(append(data, '\n')); err != nil {
			log.Printf("Failed to write to log file: %v", err)
		}
	}
	s.writeFramed(data)
}

// -------------------- MCP Client support --------------------

// MCPClient represents an MCP client that can call tools
type MCPClient struct {
	input      io.Reader
	writer     io.Writer
	reader     *bufio.Scanner
	bufr       *bufio.Reader
	requestID  int
	useHeaders bool
}

// NewMCPClient creates a new MCP client with stdin/stdout
func NewMCPClient() *MCPClient {
	return &MCPClient{
		input:     os.Stdin,
		writer:    os.Stdout,
		reader:    bufio.NewScanner(os.Stdin),
		bufr:      bufio.NewReader(os.Stdin),
		requestID: 1,
	}
}

// SetIO allows overriding the client's input/output streams
func (c *MCPClient) SetIO(r io.Reader, w io.Writer) {
	if r != nil {
		c.input = r
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
	if structuredContent, ok := result["structuredContent"]; ok {
		toolResult.StructuredContent = structuredContent
	}
	if nextPageToken, ok := result["next_page_token"].(string); ok {
		toolResult.NextPageToken = nextPageToken
	}

	return toolResult, nil
}

func (c *MCPClient) messageFramer() messageFramer {
	return messageFramer{
		input:      c.input,
		writer:     c.writer,
		bufr:       c.bufr,
		useHeaders: c.useHeaders,
	}
}

// readNextMessage reads the next JSON-RPC message body
func (c *MCPClient) readNextMessage() ([]byte, error) {
	framer := c.messageFramer()
	body, err := framer.readNextMessage()
	c.bufr = framer.bufr
	c.useHeaders = framer.useHeaders
	return body, err
}

// writeFramed writes JSON payload using header framing if enabled
func (c *MCPClient) writeFramed(data []byte) {
	c.messageFramer().writeFramed(data)
}

// ListResources sends the resources/list request and returns available resources
func (c *MCPClient) ListResources() ([]ResourceDefinition, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.requestID,
		Method:  "resources/list",
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
		return nil, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}

	// Parse resources from result
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid resources/list response")
	}

	resourcesData, ok := result["resources"]
	if !ok {
		return nil, fmt.Errorf("no resources in response")
	}

	resourcesJSON, err := json.Marshal(resourcesData)
	if err != nil {
		return nil, err
	}

	var resources []ResourceDefinition
	if err := json.Unmarshal(resourcesJSON, &resources); err != nil {
		return nil, err
	}

	return resources, nil
}

// ReadResource sends a resources/read request and returns the resource content
func (c *MCPClient) ReadResource(uri string) ([]interface{}, error) {
	params := ResourceReadParams{URI: uri}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.requestID,
		Method:  "resources/read",
		Params:  paramsJSON,
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
		return nil, fmt.Errorf("failed to parse JSON response: %v", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("resource read error: %s", resp.Error.Message)
	}

	// Parse contents from result
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid resource read response")
	}

	contentsData, ok := result["contents"]
	if !ok {
		return nil, fmt.Errorf("no contents in response")
	}

	contentsJSON, err := json.Marshal(contentsData)
	if err != nil {
		return nil, err
	}

	var contents []interface{}
	if err := json.Unmarshal(contentsJSON, &contents); err != nil {
		return nil, err
	}

	return contents, nil
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

// processRequest processes a JSON-RPC request and returns the response
func (s *MCPServer) processRequest(req JSONRPCRequest) JSONRPCResponse {
	return s.processRequestWithContext(s.currentCtx, req)
}

// processRequestWithContext processes a JSON-RPC request with context and returns the response
func (s *MCPServer) processRequestWithContext(ctx context.Context, req JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &initParams)
		proto := initParams.ProtocolVersion
		if proto == "" {
			proto = "2024-11-05"
		}
		caps := map[string]interface{}{"tools": map[string]interface{}{}}
		if len(s.prompts) > 0 {
			caps["prompts"] = map[string]interface{}{}
		} else {
			caps["prompts"] = map[string]interface{}{}
		}
		if len(s.resources) > 0 {
			caps["resources"] = map[string]interface{}{}
		} else {
			caps["resources"] = map[string]interface{}{}
		}
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": proto,
				"capabilities":    caps,
				"serverInfo": map[string]interface{}{
					"name":    "nsmcp",
					"version": "0.1.0",
				},
			},
		}
	case "notifications/initialized":
		return JSONRPCResponse{JSONRPC: "2.0"} // No ID, no response
	case "tools/list":
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.tools,
			},
		}
	case "tools/call":
		return s.processCallWithContext(ctx, req)
	case "prompts/list":
		return s.processPromptsList(req)
	case "prompts/get":
		return s.processPromptsGet(req)
	case "prompts/apply":
		return s.processPromptsApply(req)
	case "resources/list":
		return s.processResourcesList(req)
	case "resources/read":
		return s.processResourcesRead(req)
	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32601,
				Message: "Method not found: " + req.Method,
			},
		}
	}
}

// processCall handles a tools/call request
func (s *MCPServer) processCall(req JSONRPCRequest) JSONRPCResponse {
	return s.processCallWithContext(s.currentCtx, req)
}

// processCallWithContext handles a tools/call request with context
func (s *MCPServer) processCallWithContext(ctx context.Context, req JSONRPCRequest) JSONRPCResponse {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params"},
		}
	}
	if _, exists := s.streamingHandlers[params.Name]; exists {
		// Streaming not supported over HTTP
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32000, Message: "Streaming not supported over HTTP"},
		}
	}
	// Check context-aware handler first
	if handlerCtx, exists := s.toolHandlersWithCtx[params.Name]; exists {
		result, err := handlerCtx(ctx, params.Arguments)
		return s.formatToolResult(req.ID, result, err)
	}
	// Fall back to legacy handler
	handler, exists := s.toolHandlers[params.Name]
	if !exists {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "Tool not found: " + params.Name},
		}
	}
	result, err := handler(params.Arguments)
	return s.formatToolResult(req.ID, result, err)
}

// formatToolResult formats the tool result into a JSON-RPC response
func (s *MCPServer) formatToolResult(id interface{}, result interface{}, err error) JSONRPCResponse {
	if err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &RPCError{Code: -32000, Message: err.Error()},
		}
	}
	switch v := result.(type) {
	case string:
		return s.buildToolResponse(id, v, v, false)
	case ToolCallResult:
		out := map[string]interface{}{"isError": v.IsError}
		if s.responseMode == ResponseModeStructured || s.responseMode == ResponseModeBoth {
			if v.StructuredContent != nil {
				out["structuredContent"] = v.StructuredContent
			} else if v.Content != nil {
				out["structuredContent"] = v.Content
			}
		}
		if s.responseMode == ResponseModeContent || s.responseMode == ResponseModeBoth {
			if v.Content != nil {
				out["content"] = v.Content
			}
		}
		if s.responseMode == ResponseModeContent && v.Content == nil && v.StructuredContent != nil {
			if b, e := json.MarshalIndent(v.StructuredContent, "", "  "); e == nil {
				out["content"] = []interface{}{map[string]interface{}{"type": "text", "text": string(b)}}
			}
		}
		if v.NextPageToken != "" {
			out["next_page_token"] = v.NextPageToken
		}
		if v.Page > 0 {
			out["page"] = v.Page
		}
		if v.TotalPages > 0 {
			out["totalPages"] = v.TotalPages
		}
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  out,
		}
	case map[string]interface{}:
		if content, ok := v["content"]; ok {
			if _, eok := v["isError"]; !eok {
				v["isError"] = false
			}
			if structuredContent, sok := v["structuredContent"]; sok {
				return s.buildToolResponseFromParts(id, content, structuredContent, false)
			}
			return s.buildToolResponseFromParts(id, content, v, false)
		}
		if b, e := json.MarshalIndent(v, "", "  "); e == nil {
			return s.buildToolResponse(id, string(b), v, false)
		}
		return s.buildToolResponse(id, fmt.Sprintf("%v", v), v, false)
	default:
		if b, e := json.MarshalIndent(v, "", "  "); e == nil {
			return s.buildToolResponse(id, string(b), v, false)
		}
		return s.buildToolResponse(id, fmt.Sprintf("%v", v), v, false)
	}
}

// buildToolResponse creates a tool response with content and/or structuredContent based on responseMode
func (s *MCPServer) buildToolResponse(id interface{}, textContent string, structuredData interface{}, isError bool) JSONRPCResponse {
	content := []interface{}{map[string]interface{}{"type": "text", "text": textContent}}
	return s.buildToolResponseFromParts(id, content, structuredData, isError)
}

// buildToolResponseFromParts creates a tool response from pre-built content and structuredContent
func (s *MCPServer) buildToolResponseFromParts(id interface{}, content interface{}, structuredData interface{}, isError bool) JSONRPCResponse {
	out := map[string]interface{}{"isError": isError}
	switch s.responseMode {
	case ResponseModeStructured:
		out["structuredContent"] = structuredData
	case ResponseModeBoth:
		out["content"] = content
		out["structuredContent"] = structuredData
	default: // ResponseModeContent
		out["content"] = content
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  out,
	}
}
