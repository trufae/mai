package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "0.1.0"

type repeatFlag []string

func (r *repeatFlag) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatFlag) Set(value string) error {
	if !strings.Contains(value, "=") {
		return fmt.Errorf("expected key=value")
	}
	*r = append(*r, value)
	return nil
}

type config struct {
	replPath        string
	provider        string
	model           string
	baseURL         string
	mcpConfig       string
	enableMCP       bool
	mcpYolo         bool
	debug           bool
	extraConfig     []string
	providerOptions []string
	modelOptions    []string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type framer struct {
	input      *bufio.Reader
	output     io.Writer
	useHeaders bool
	mu         sync.Mutex
}

func newFramer(r io.Reader, w io.Writer) *framer {
	return &framer{input: bufio.NewReader(r), output: w}
}

func (f *framer) readMessage() ([]byte, error) {
	firstLine, err := f.input.ReadString('\n')
	if err != nil {
		if err == io.EOF && len(firstLine) == 0 {
			return nil, io.EOF
		}
		return nil, err
	}

	line := strings.TrimRight(firstLine, "\r\n")
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "content-length:") || strings.HasPrefix(lower, "content-type:") {
		headers := []string{line}
		for {
			header, err := f.input.ReadString('\n')
			if err != nil {
				return nil, err
			}
			header = strings.TrimRight(header, "\r\n")
			if strings.TrimSpace(header) == "" {
				break
			}
			headers = append(headers, header)
		}
		length := -1
		for _, header := range headers {
			key, value, ok := strings.Cut(header, ":")
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(key), "content-length") {
				n, err := strconv.Atoi(strings.TrimSpace(value))
				if err == nil && n >= 0 {
					length = n
					break
				}
			}
		}
		if length < 0 {
			return nil, errors.New("missing Content-Length header")
		}
		f.useHeaders = true
		body := make([]byte, length)
		if _, err := io.ReadFull(f.input, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	return []byte(line), nil
}

func (f *framer) writeJSON(value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.useHeaders {
		if _, err := fmt.Fprintf(f.output, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
			return err
		}
		_, err = f.output.Write(data)
		return err
	}
	_, err = fmt.Fprintln(f.output, string(data))
	return err
}

type turn struct {
	Role    string
	Content string
}

type session struct {
	ID          string
	CWD         string
	Protocol    int
	Provider    string
	Model       string
	BaseURL     string
	MCPConfig   string
	tempFiles   []string
	history     []turn
	cancel      context.CancelFunc
	running     bool
	cancelled   bool
	activeStart time.Time
	mu          sync.Mutex
}

type server struct {
	cfg      config
	framer   *framer
	sessions map[string]*session
	nextID   int64
	protocol int
	mu       sync.Mutex
}

type newSessionParams struct {
	CWD        string                   `json:"cwd"`
	MCPServers []map[string]interface{} `json:"mcpServers"`
}

type promptParams struct {
	SessionID string                   `json:"sessionId"`
	Prompt    []map[string]interface{} `json:"prompt"`
}

type setConfigParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	ID        string `json:"id"`
	Value     string `json:"value"`
}

type cancelParams struct {
	SessionID string `json:"sessionId"`
}

type maiMCPConfig struct {
	Servers map[string]maiMCPServer `json:"servers"`
}

type maiMCPServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

func main() {
	cfg := parseFlags()
	s := &server{
		cfg:      cfg,
		framer:   newFramer(os.Stdin, os.Stdout),
		sessions: make(map[string]*session),
		protocol: 1,
	}
	if err := s.serve(); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "mai-acp: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var extra repeatFlag
	cfg := config{
		replPath:  os.Getenv("MAI_ACP_REPL"),
		provider:  getenv("MAI_PROVIDER", "ollama"),
		model:     getenv("MAI_MODEL", "gemma3:1b"),
		baseURL:   os.Getenv("MAI_BASEURL"),
		mcpConfig: os.Getenv("MAI_ACP_MCP_CONFIG"),
		mcpYolo:   true,
	}
	if cfg.replPath == "" {
		cfg.replPath = resolveDefaultREPL()
	}

	flag.StringVar(&cfg.replPath, "repl", cfg.replPath, "path to mai-repl")
	flag.StringVar(&cfg.provider, "p", cfg.provider, "initial MAI provider")
	flag.StringVar(&cfg.provider, "provider", cfg.provider, "initial MAI provider")
	flag.StringVar(&cfg.model, "m", cfg.model, "initial MAI model")
	flag.StringVar(&cfg.model, "model", cfg.model, "initial MAI model")
	flag.StringVar(&cfg.baseURL, "b", cfg.baseURL, "custom provider base URL")
	flag.StringVar(&cfg.baseURL, "base-url", cfg.baseURL, "custom provider base URL")
	flag.StringVar(&cfg.mcpConfig, "mcp-config", cfg.mcpConfig, "MAI MCP config file to use when the ACP client does not provide MCP servers")
	flag.BoolVar(&cfg.enableMCP, "t", false, "enable MAI MCP tools using the configured MCP file")
	flag.BoolVar(&cfg.enableMCP, "mcp", false, "enable MAI MCP tools using the configured MCP file")
	flag.BoolVar(&cfg.mcpYolo, "mcp-yolo", cfg.mcpYolo, "approve MCP tool calls without interactive prompts")
	flag.BoolVar(&cfg.debug, "debug", false, "write debug logs to stderr")
	providers := flag.String("providers", os.Getenv("MAI_ACP_PROVIDERS"), "comma-separated provider values to expose through ACP")
	models := flag.String("models", os.Getenv("MAI_ACP_MODELS"), "comma-separated model values to expose through ACP")
	showVersion := flag.Bool("v", false, "show version")
	flag.Var(&extra, "c", "extra mai-repl config option as key=value; can be repeated")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	cfg.extraConfig = []string(extra)
	cfg.providerOptions = splitCSV(*providers)
	cfg.modelOptions = splitCSV(*models)
	return cfg
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func resolveDefaultREPL() string {
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		candidate := filepath.Join(filepath.Dir(exe), "..", "repl", executableName("mai-repl"))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return executableName("mai-repl")
}

func executableName(name string) string {
	if strings.EqualFold(os.Getenv("GOOS"), "windows") || strings.HasSuffix(os.Args[0], ".exe") {
		return name + ".exe"
	}
	return name
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (s *server) serve() error {
	for {
		body, err := s.framer.readMessage()
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			s.writeError(nil, -32700, "parse error", err.Error())
			continue
		}
		if req.JSONRPC != "" && req.JSONRPC != "2.0" {
			s.writeError(req.ID, -32600, "invalid jsonrpc version", nil)
			continue
		}
		if req.Method == "" {
			s.writeError(req.ID, -32600, "method is required", nil)
			continue
		}

		if !hasID(req.ID) {
			s.handleNotification(req)
			continue
		}

		if req.Method == "session/prompt" {
			go s.handlePrompt(req)
			continue
		}

		result, rpcErr := s.handleRequest(req)
		if rpcErr != nil {
			s.writeRPCError(req.ID, rpcErr)
			continue
		}
		s.writeResponse(req.ID, result)
	}
}

func hasID(id json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(id))
	return trimmed != "" && trimmed != "null"
}

func (s *server) handleRequest(req rpcRequest) (interface{}, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params), nil
	case "authenticate", "auth/login", "logout", "auth/logout":
		return map[string]interface{}{}, nil
	case "session/new":
		return s.handleNewSession(req.Params)
	case "session/set_config_option":
		return s.handleSetConfig(req.Params)
	case "session/close":
		return s.handleCloseSession(req.Params)
	case "session/list":
		return s.handleListSessions(), nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

func (s *server) handleNotification(req rpcRequest) {
	switch req.Method {
	case "session/cancel":
		var params cancelParams
		if decodeParams(req.Params, &params) == nil {
			s.cancelSession(params.SessionID)
		}
	case "$/cancel_request", "initialized":
		return
	default:
		if s.cfg.debug {
			fmt.Fprintf(os.Stderr, "mai-acp: ignored notification %s\n", req.Method)
		}
	}
}

func (s *server) handleInitialize(params json.RawMessage) interface{} {
	protocol := protocolFromParams(params)
	if protocol >= 2 {
		protocol = 2
	} else {
		protocol = 1
	}

	s.mu.Lock()
	s.protocol = protocol
	s.mu.Unlock()

	if protocol >= 2 {
		return map[string]interface{}{
			"protocolVersion": protocol,
			"info": map[string]interface{}{
				"name":    "mai-acp",
				"version": version,
			},
			"capabilities": map[string]interface{}{
				"session": map[string]interface{}{
					"mcp": map[string]interface{}{
						"stdio": map[string]interface{}{},
						"http":  map[string]interface{}{},
					},
					"additionalDirectories": map[string]interface{}{},
					"close":                 map[string]interface{}{},
				},
			},
			"authMethods": []interface{}{},
		}
	}

	return map[string]interface{}{
		"protocolVersion": protocol,
		"agentCapabilities": map[string]interface{}{
			"loadSession": false,
			"promptCapabilities": map[string]interface{}{
				"image":           false,
				"audio":           false,
				"embeddedContext": false,
			},
			"mcpCapabilities": map[string]interface{}{
				"http": true,
				"sse":  true,
			},
			"sessionCapabilities": map[string]interface{}{
				"additionalDirectories": map[string]interface{}{},
				"close":                 map[string]interface{}{},
			},
			"auth": map[string]interface{}{},
		},
		"authMethods": []interface{}{},
		"agentInfo": map[string]interface{}{
			"name":    "mai-acp",
			"version": version,
		},
	}
}

func protocolFromParams(params json.RawMessage) int {
	var raw map[string]interface{}
	if err := decodeParams(params, &raw); err != nil {
		return 1
	}
	switch v := raw["protocolVersion"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 1
}

func (s *server) handleNewSession(params json.RawMessage) (interface{}, *rpcError) {
	var req newSessionParams
	if err := decodeParams(params, &req); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("mai-acp-%d-%d", time.Now().UnixNano(), s.nextID)
	protocol := s.protocol
	s.mu.Unlock()

	sess := &session{
		ID:        id,
		CWD:       cwd,
		Protocol:  protocol,
		Provider:  s.cfg.provider,
		Model:     s.cfg.model,
		BaseURL:   s.cfg.baseURL,
		MCPConfig: s.cfg.mcpConfig,
	}

	if len(req.MCPServers) > 0 {
		path, err := writeMCPConfig(id, req.MCPServers)
		if err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid mcpServers", Data: err.Error()}
		}
		sess.MCPConfig = path
		sess.tempFiles = append(sess.tempFiles, path)
	}

	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	return map[string]interface{}{
		"sessionId":     id,
		"configOptions": s.sessionConfigOptions(sess),
	}, nil
}

func writeMCPConfig(sessionID string, servers []map[string]interface{}) (string, error) {
	cfg := maiMCPConfig{Servers: make(map[string]maiMCPServer)}
	for i, raw := range servers {
		name := stringField(raw, "name")
		if name == "" {
			name = fmt.Sprintf("mcp%d", i+1)
		}
		serverType := stringField(raw, "type")
		command := stringField(raw, "command")
		url := stringField(raw, "url")
		if serverType == "" {
			if url != "" {
				serverType = "http"
			} else {
				serverType = "stdio"
			}
		}
		switch serverType {
		case "stdio":
			if command == "" {
				return "", fmt.Errorf("%s: stdio server requires command", name)
			}
		case "http", "sse":
			if url == "" {
				return "", fmt.Errorf("%s: %s server requires url", name, serverType)
			}
		default:
			return "", fmt.Errorf("%s: unsupported MCP transport %q", name, serverType)
		}

		cfg.Servers[name] = maiMCPServer{
			Type:    serverType,
			Command: command,
			Args:    stringSlice(raw["args"]),
			URL:     url,
			Env:     envMap(raw["env"]),
			Enabled: true,
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), sessionID+"-mcps.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func stringField(raw map[string]interface{}, key string) string {
	value, _ := raw[key].(string)
	return value
}

func stringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func envMap(value interface{}) map[string]string {
	if value == nil {
		return nil
	}
	out := make(map[string]string)
	switch env := value.(type) {
	case map[string]interface{}:
		for k, v := range env {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	case []interface{}:
		for _, item := range env {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			name := stringField(obj, "name")
			value := stringField(obj, "value")
			if name != "" {
				out[name] = value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *server) handleSetConfig(params json.RawMessage) (interface{}, *rpcError) {
	var req setConfigParams
	if err := decodeParams(params, &req); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
	}
	configID := req.ConfigID
	if configID == "" {
		configID = req.ID
	}
	sess := s.getSession(req.SessionID)
	if sess == nil {
		return nil, &rpcError{Code: -32602, Message: "unknown sessionId"}
	}

	sess.mu.Lock()
	switch configID {
	case "provider":
		sess.Provider = req.Value
	case "model":
		sess.Model = req.Value
	default:
		sess.mu.Unlock()
		return nil, &rpcError{Code: -32602, Message: "unknown config option: " + configID}
	}
	sess.mu.Unlock()

	return map[string]interface{}{"configOptions": s.sessionConfigOptions(sess)}, nil
}

func (s *server) handleCloseSession(params json.RawMessage) (interface{}, *rpcError) {
	var req cancelParams
	if err := decodeParams(params, &req); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
	}
	s.cancelSession(req.SessionID)

	s.mu.Lock()
	sess := s.sessions[req.SessionID]
	delete(s.sessions, req.SessionID)
	s.mu.Unlock()

	if sess != nil {
		for _, path := range sess.tempFiles {
			_ = os.Remove(path)
		}
	}
	return map[string]interface{}{}, nil
}

func (s *server) handleListSessions() interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]map[string]interface{}, 0, len(s.sessions))
	for _, sess := range s.sessions {
		items = append(items, map[string]interface{}{
			"sessionId": sess.ID,
			"cwd":       sess.CWD,
			"title":     sess.ID,
		})
	}
	return map[string]interface{}{"sessions": items}
}

func (s *server) handlePrompt(req rpcRequest) {
	var params promptParams
	if err := decodeParams(req.Params, &params); err != nil {
		s.writeError(req.ID, -32602, "invalid params", err.Error())
		return
	}
	sess := s.getSession(params.SessionID)
	if sess == nil {
		s.writeError(req.ID, -32602, "unknown sessionId", nil)
		return
	}

	userText := strings.TrimSpace(promptText(params.Prompt))
	if userText == "" {
		s.writeError(req.ID, -32602, "prompt is empty", nil)
		return
	}

	if sess.Protocol >= 2 {
		s.writeResponse(req.ID, map[string]interface{}{})
		s.sendSessionUpdate(sess, map[string]interface{}{"sessionUpdate": "state_update", "state": "running"})
		s.sendSessionUpdate(sess, map[string]interface{}{
			"sessionUpdate": "user_message",
			"messageId":     messageID(sess.ID, "user"),
			"content":       []interface{}{textBlock(userText)},
		})
	}

	output, runErr, cancelled := s.runPrompt(sess, userText)
	stopReason := "end_turn"
	if cancelled {
		stopReason = "cancelled"
	} else if runErr != nil {
		stopReason = "refusal"
	}

	if runErr == nil && !cancelled {
		sess.mu.Lock()
		sess.history = append(sess.history, turn{Role: "user", Content: userText}, turn{Role: "assistant", Content: output})
		sess.mu.Unlock()
	}

	if sess.Protocol >= 2 {
		if runErr != nil && output == "" {
			output = runErr.Error()
		}
		if output != "" {
			s.sendSessionUpdate(sess, map[string]interface{}{
				"sessionUpdate": "agent_message",
				"messageId":     messageID(sess.ID, "assistant"),
				"content":       []interface{}{textBlock(output)},
			})
		}
		s.sendSessionUpdate(sess, map[string]interface{}{"sessionUpdate": "state_update", "state": "idle", "stopReason": stopReason})
		return
	}

	if runErr != nil && !cancelled {
		s.writeError(req.ID, -32000, runErr.Error(), nil)
		return
	}
	if output != "" {
		s.sendSessionUpdate(sess, map[string]interface{}{
			"sessionUpdate": "agent_message_chunk",
			"content":       textBlock(output),
		})
	}
	s.writeResponse(req.ID, map[string]interface{}{"stopReason": stopReason})
}

func promptText(blocks []map[string]interface{}) string {
	var out strings.Builder
	for _, block := range blocks {
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		switch block["type"] {
		case "text":
			out.WriteString(stringField(block, "text"))
		case "resource_link":
			name := stringField(block, "name")
			uri := stringField(block, "uri")
			if name != "" {
				out.WriteString(name)
				out.WriteString(": ")
			}
			out.WriteString(uri)
		case "resource":
			out.WriteString(resourceText(block))
		default:
			data, _ := json.Marshal(block)
			out.Write(data)
		}
	}
	return out.String()
}

func resourceText(block map[string]interface{}) string {
	resource, _ := block["resource"].(map[string]interface{})
	if resource == nil {
		resource, _ = block["contents"].(map[string]interface{})
	}
	if resource == nil {
		data, _ := json.Marshal(block)
		return string(data)
	}
	if text := stringField(resource, "text"); text != "" {
		return text
	}
	data, _ := json.Marshal(resource)
	return string(data)
}

func textBlock(text string) map[string]interface{} {
	return map[string]interface{}{"type": "text", "text": text}
}

func messageID(sessionID, role string) string {
	return fmt.Sprintf("%s-%s-%d", sessionID, role, time.Now().UnixNano())
}

func (s *server) runPrompt(sess *session, userText string) (string, error, bool) {
	sess.mu.Lock()
	if sess.running {
		sess.mu.Unlock()
		return "", errors.New("session is already running"), false
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
	sess.running = true
	sess.cancelled = false
	sess.activeStart = time.Now()
	input := buildPrompt(sess.history, userText)
	cwd := sess.CWD
	provider := sess.Provider
	model := sess.Model
	baseURL := sess.BaseURL
	mcpConfig := sess.MCPConfig
	sess.mu.Unlock()
	args := s.replArgs(provider, model, baseURL, mcpConfig)

	defer func() {
		sess.mu.Lock()
		sess.cancel = nil
		sess.running = false
		sess.mu.Unlock()
	}()

	cmd := exec.CommandContext(ctx, s.cfg.replPath, args...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if s.cfg.debug {
		fmt.Fprintf(os.Stderr, "mai-acp: running %s %s\n", s.cfg.replPath, strings.Join(args, " "))
	}
	err := cmd.Run()

	sess.mu.Lock()
	cancelled := sess.cancelled || ctx.Err() == context.Canceled
	sess.mu.Unlock()

	out := strings.TrimSpace(stdout.String())
	errText := strings.TrimSpace(stderr.String())
	if err != nil {
		if cancelled {
			return out, err, true
		}
		if errText != "" {
			return out, fmt.Errorf("%v: %s", err, errText), false
		}
		return out, err, false
	}
	return out, nil, cancelled
}

func buildPrompt(history []turn, userText string) string {
	if len(history) == 0 {
		return userText
	}
	var out strings.Builder
	out.WriteString("Continue the conversation below and answer the current user request.\n\n<conversation>\n")
	for _, turn := range history {
		switch turn.Role {
		case "assistant":
			out.WriteString("Assistant: ")
		default:
			out.WriteString("User: ")
		}
		out.WriteString(turn.Content)
		out.WriteString("\n\n")
	}
	out.WriteString("</conversation>\n\nCurrent user request:\n")
	out.WriteString(userText)
	return out.String()
}

func (s *server) replArgs(provider, model, baseURL, mcpConfig string) []string {
	useMCP := s.cfg.enableMCP || mcpConfig != ""
	args := []string{"-1"}
	if useMCP {
		args = append(args, "-t")
		if mcpConfig != "" {
			args = append(args, "-c", "mcp.config="+mcpConfig)
		}
		args = append(args, "-c", "mcp.transport=embed")
		args = append(args, "-c", "mcp.display=quiet")
		if s.cfg.mcpYolo {
			args = append(args, "-c", "mcp.yolo=true")
		}
	}
	for _, kv := range s.cfg.extraConfig {
		args = append(args, "-c", kv)
	}
	if provider != "" {
		args = append(args, "-p", provider)
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	if baseURL != "" {
		args = append(args, "-b", baseURL)
	}
	return args
}

func (s *server) cancelSession(sessionID string) {
	sess := s.getSession(sessionID)
	if sess == nil {
		return
	}
	sess.mu.Lock()
	sess.cancelled = true
	cancel := sess.cancel
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *server) getSession(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *server) sessionConfigOptions(sess *session) []map[string]interface{} {
	sess.mu.Lock()
	provider := sess.Provider
	model := sess.Model
	protocol := sess.Protocol
	sess.mu.Unlock()

	if provider == "" {
		provider = "ollama"
	}
	if model == "" {
		model = "gemma3:1b"
	}

	idKey := "id"
	if protocol >= 2 {
		idKey = "configId"
	}
	return []map[string]interface{}{
		{
			idKey:          "provider",
			"name":         "Provider",
			"description":  "MAI provider used by mai-repl",
			"category":     "model_config",
			"type":         "select",
			"currentValue": provider,
			"options":      optionValues(providerOptions(s.cfg.providerOptions, provider)),
		},
		{
			idKey:          "model",
			"name":         "Model",
			"description":  "MAI model passed to mai-repl",
			"category":     "model",
			"type":         "select",
			"currentValue": model,
			"options":      optionValues(modelOptions(s.cfg.modelOptions, model)),
		},
	}
}

func providerOptions(extra []string, current string) []string {
	defaults := []string{
		"ollama", "lmstudio", "openai", "shimmy", "openrouter", "claude",
		"gemini", "mistral", "deepseek", "bedrock", "xai", "ollamacloud",
		"opencode", "openapi", "llamacpp",
	}
	return unique(append(append(defaults, extra...), current))
}

func modelOptions(extra []string, current string) []string {
	defaults := []string{"gemma3:1b", "gpt-4o-mini", "gpt-4o", "claude-3-5-sonnet-20241022"}
	return unique(append(append(defaults, extra...), current))
}

func optionValues(values []string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(values))
	for _, value := range values {
		out = append(out, map[string]interface{}{"value": value, "name": value})
	}
	return out
}

func unique(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (s *server) sendSessionUpdate(sess *session, update map[string]interface{}) {
	s.writeNotification("session/update", map[string]interface{}{
		"sessionId": sess.ID,
		"update":    update,
	})
}

func decodeParams(params json.RawMessage, dest interface{}) error {
	if len(params) == 0 || strings.TrimSpace(string(params)) == "null" {
		return nil
	}
	return json.Unmarshal(params, dest)
}

func (s *server) writeResponse(id json.RawMessage, result interface{}) {
	if err := s.framer.writeJSON(rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Result: result}); err != nil {
		fmt.Fprintf(os.Stderr, "mai-acp: write response: %v\n", err)
	}
}

func (s *server) writeError(id json.RawMessage, code int, message string, data interface{}) {
	s.writeRPCError(id, &rpcError{Code: code, Message: message, Data: data})
}

func (s *server) writeRPCError(id json.RawMessage, errObj *rpcError) {
	if err := s.framer.writeJSON(rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Error: errObj}); err != nil {
		fmt.Fprintf(os.Stderr, "mai-acp: write error: %v\n", err)
	}
}

func (s *server) writeNotification(method string, params interface{}) {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if err := s.framer.writeJSON(msg); err != nil {
		fmt.Fprintf(os.Stderr, "mai-acp: write notification: %v\n", err)
	}
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if !hasID(id) {
		return json.RawMessage("null")
	}
	return id
}
