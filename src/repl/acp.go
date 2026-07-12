package main

// ACP (Agent Client Protocol) sub-agent support for the mai REPL.
//
// This file implements the /acp command, which makes mai act as an ACP
// *client*: external coding agents (gemini, claude, codex, qwen,
// opencode, ...) are spawned as child processes speaking JSON-RPC over
// stdio, prompts are sent to them, and their replies are collected.
// Jobs can run in the background so several agents work in parallel and
// their results are gathered at the end with /acp wait.
//
// Protocol reference: https://agentclientprotocol.com

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ACPAgent describes an external agent that can be driven over ACP
type ACPAgent struct {
	Name        string
	Aliases     []string
	Description string
	Command     string   // binary looked up in PATH
	AltCommands []string // alternative binary names to look up
	Args        []string // arguments that start the agent in ACP mode
	AltArgs     []string // fallback arguments used by older agent releases
	Env         []string // extra KEY=VALUE entries for the child process
	NpxPackage  string   // npm package runnable through npx when no binary is installed
	NpxArgs     []string // arguments appended after the npx package
	Install     string   // how to install the agent
	Adapter     bool     // true when this is an ACP adapter wrapping another CLI
	UserDefined bool     // true when loaded from ~/.config/mai/acp.json
}

// Catalog of known ACP agents, taken from the official registry
// (https://github.com/agentclientprotocol/registry). Users can add or
// override entries in ~/.config/mai/acp.json (/acp edit).
var acpBuiltinAgents = []ACPAgent{
	{
		Name:        "gemini",
		Description: "Google's Gemini CLI",
		Command:     "gemini",
		Args:        []string{"--acp"},
		AltArgs:     []string{"--experimental-acp"},
		NpxPackage:  "@google/gemini-cli",
		NpxArgs:     []string{"--acp"},
		Install:     "npm install -g @google/gemini-cli",
	},
	{
		Name:        "claude",
		Aliases:     []string{"claude-code", "claude-acp"},
		Description: "Anthropic's Claude Code (ACP adapter)",
		Command:     "claude-agent-acp",
		AltCommands: []string{"claude-code-acp"},
		NpxPackage:  "@agentclientprotocol/claude-agent-acp",
		Install:     "npm install -g @agentclientprotocol/claude-agent-acp (wraps the 'claude' CLI, log in with it first)",
		Adapter:     true,
	},
	{
		Name:        "codex",
		Aliases:     []string{"codex-acp", "openai"},
		Description: "OpenAI's Codex CLI (ACP adapter)",
		Command:     "codex-acp",
		NpxPackage:  "@agentclientprotocol/codex-acp",
		Install:     "npm install -g @agentclientprotocol/codex-acp (wraps the 'codex' CLI, log in with it first)",
		Adapter:     true,
	},
	{
		Name:        "qwen",
		Aliases:     []string{"qwen-code"},
		Description: "Alibaba's Qwen Code",
		Command:     "qwen",
		Args:        []string{"--acp"},
		AltArgs:     []string{"--experimental-acp"},
		NpxPackage:  "@qwen-code/qwen-code",
		NpxArgs:     []string{"--acp"},
		Install:     "npm install -g @qwen-code/qwen-code",
	},
	{
		Name:        "opencode",
		Description: "OpenCode, the open source coding agent",
		Command:     "opencode",
		Args:        []string{"acp"},
		Install:     "curl -fsSL https://opencode.ai/install | bash",
	},
	{
		Name:        "goose",
		Description: "Block's local extensible AI agent",
		Command:     "goose",
		Args:        []string{"acp"},
		Install:     "https://block.github.io/goose",
	},
	{
		Name:        "kimi",
		Aliases:     []string{"kimi-cli"},
		Description: "Moonshot AI's Kimi CLI",
		Command:     "kimi",
		Args:        []string{"acp"},
		Install:     "https://github.com/MoonshotAI/kimi-cli",
	},
	{
		Name:        "cursor",
		Aliases:     []string{"cursor-agent"},
		Description: "Cursor's coding agent",
		Command:     "cursor-agent",
		Args:        []string{"acp"},
		Install:     "curl https://cursor.com/install -fsS | bash",
	},
	{
		Name:        "copilot",
		Aliases:     []string{"github-copilot"},
		Description: "GitHub Copilot CLI",
		Command:     "copilot",
		Args:        []string{"--acp"},
		NpxPackage:  "@github/copilot",
		NpxArgs:     []string{"--acp"},
		Install:     "npm install -g @github/copilot",
	},
	{
		Name:        "cline",
		Description: "Cline autonomous coding agent",
		Command:     "cline",
		Args:        []string{"--acp"},
		NpxPackage:  "cline",
		NpxArgs:     []string{"--acp"},
		Install:     "npm install -g cline",
	},
	{
		Name:        "kilo",
		Aliases:     []string{"kilocode"},
		Description: "Kilo open source coding agent",
		Command:     "kilo",
		Args:        []string{"acp"},
		NpxPackage:  "@kilocode/cli",
		NpxArgs:     []string{"acp"},
		Install:     "npm install -g @kilocode/cli",
	},
	{
		Name:        "vibe",
		Aliases:     []string{"mistral", "mistral-vibe"},
		Description: "Mistral's Vibe coding assistant",
		Command:     "vibe-acp",
		Install:     "https://github.com/mistralai/mistral-vibe",
	},
	{
		Name:        "auggie",
		Description: "Augment Code's Auggie CLI",
		Command:     "auggie",
		Args:        []string{"--acp"},
		NpxPackage:  "@augmentcode/auggie",
		NpxArgs:     []string{"--acp"},
		Install:     "npm install -g @augmentcode/auggie",
	},
	{
		Name:        "stakpak",
		Description: "Stakpak DevOps agent",
		Command:     "stakpak",
		Args:        []string{"acp"},
		Install:     "https://github.com/stakpak/agent",
	},
	{
		Name:        "vtcode",
		Description: "VT Code coding agent",
		Command:     "vtcode",
		Args:        []string{"acp"},
		Env:         []string{"VT_ACP_ENABLED=1", "VT_ACP_ZED_ENABLED=1"},
		Install:     "https://github.com/vinhnx/VTCode",
	},
	{
		Name:        "crow",
		Aliases:     []string{"crow-cli"},
		Description: "Minimal ACP-native coding agent",
		Command:     "crow-cli",
		Args:        []string{"acp"},
		Install:     "https://github.com/crow-cli/crow-cli",
	},
	{
		Name:        "mai",
		Aliases:     []string{"mai-acp"},
		Description: "mai itself exposed over ACP (recursive sub-agent)",
		Command:     "mai-acp",
		Install:     "make install (from the mai repository)",
	},
}

type acpAvailability int

const (
	acpNotInstalled acpAvailability = iota
	acpViaNpx
	acpInstalled
)

// availability reports whether the agent binary is installed, runnable
// through npx, or missing; the resolved binary name is returned too
func (a *ACPAgent) availability() (acpAvailability, string) {
	for _, cmd := range append([]string{a.Command}, a.AltCommands...) {
		if cmd == "" {
			continue
		}
		if _, err := exec.LookPath(cmd); err == nil {
			return acpInstalled, cmd
		}
	}
	if a.NpxPackage != "" {
		if _, err := exec.LookPath("npx"); err == nil {
			return acpViaNpx, ""
		}
	}
	return acpNotInstalled, ""
}

// commandLine resolves the argv needed to launch the agent in ACP mode
func (a *ACPAgent) commandLine() ([]string, error) {
	avail, bin := a.availability()
	switch avail {
	case acpInstalled:
		return append([]string{bin}, a.resolveArgs(bin)...), nil
	case acpViaNpx:
		return append([]string{"npx", "-y", a.NpxPackage}, a.NpxArgs...), nil
	}
	return nil, fmt.Errorf("agent '%s' is not installed. Install it with: %s", a.Name, a.Install)
}

var acpArgsCache sync.Map // binary name -> detected args

// resolveArgs picks between Args and AltArgs by asking the installed
// binary for its help text, so older agent releases keep working
func (a *ACPAgent) resolveArgs(bin string) []string {
	if len(a.AltArgs) == 0 || len(a.Args) == 0 {
		return a.Args
	}
	if cached, ok := acpArgsCache.Load(bin); ok {
		return cached.([]string)
	}
	args := a.Args
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	if err == nil {
		help := string(out)
		if !strings.Contains(help, args[0]+" ") && strings.Contains(help, a.AltArgs[0]+" ") {
			args = a.AltArgs
		}
	}
	acpArgsCache.Store(bin, args)
	return args
}

// acpUserAgent is a user-defined agent entry in ~/.config/mai/acp.json
type acpUserAgent struct {
	Description string            `json:"description,omitempty"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Install     string            `json:"install,omitempty"`
}

type acpUserConfig struct {
	Agents map[string]acpUserAgent `json:"agents"`
}

func acpConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}
	configDir := filepath.Join(home, ".config", "mai")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}
	return filepath.Join(configDir, "acp.json"), nil
}

func loadACPUserAgents() []ACPAgent {
	path, err := acpConfigPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg acpUserConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse %s: %v\r\n", path, err)
		return nil
	}
	var agents []ACPAgent
	for name, ua := range cfg.Agents {
		if ua.Command == "" {
			continue
		}
		env := make([]string, 0, len(ua.Env))
		for k, v := range ua.Env {
			env = append(env, k+"="+v)
		}
		sort.Strings(env)
		desc := ua.Description
		if desc == "" {
			desc = "user-defined ACP agent"
		}
		install := ua.Install
		if install == "" {
			install = "defined in " + path
		}
		agents = append(agents, ACPAgent{
			Name:        name,
			Description: desc,
			Command:     ua.Command,
			Args:        ua.Args,
			Env:         env,
			Install:     install,
			UserDefined: true,
		})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents
}

// allACPAgents merges the builtin catalog with user-defined agents;
// a user entry with the same name overrides the builtin one
func allACPAgents() []ACPAgent {
	agents := make([]ACPAgent, len(acpBuiltinAgents))
	copy(agents, acpBuiltinAgents)
	for _, ua := range loadACPUserAgents() {
		replaced := false
		for i := range agents {
			if strings.EqualFold(agents[i].Name, ua.Name) {
				agents[i] = ua
				replaced = true
				break
			}
		}
		if !replaced {
			agents = append(agents, ua)
		}
	}
	return agents
}

func findACPAgent(name string) *ACPAgent {
	agents := allACPAgents()
	for i := range agents {
		if strings.EqualFold(agents[i].Name, name) {
			return &agents[i]
		}
		for _, alias := range agents[i].Aliases {
			if strings.EqualFold(alias, name) {
				return &agents[i]
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// JSON-RPC client over stdio

type acpRPCMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *acpRPCError    `json:"error,omitempty"`
}

type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// acpTail keeps the last few KB written to it, used to capture stderr
type acpTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *acpTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8192 {
		t.buf = t.buf[len(t.buf)-8192:]
	}
	return len(p), nil
}

func (t *acpTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

type acpClient struct {
	agentName  string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	stderr     *acpTail
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	pending    map[string]chan *acpRPCMsg
	nextID     int64
	sessionID  string
	permission string // allow, reject or auto
	debug      bool
	onChunk    func(text string)
	onToolCall func(title string)
	fullMu     sync.Mutex
	fullMsgs   strings.Builder
	readerDone chan struct{}
}

func startACPClient(agent *ACPAgent, permission string, debug bool) (*acpClient, error) {
	cmdline, err := agent.commandLine()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	if len(agent.Env) > 0 {
		cmd.Env = append(os.Environ(), agent.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := &acpTail{}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %v", cmdline[0], err)
	}
	c := &acpClient{
		agentName:  agent.Name,
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdout),
		stderr:     tail,
		pending:    make(map[string]chan *acpRPCMsg),
		permission: permission,
		debug:      debug,
		readerDone: make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// readMessage reads one JSON-RPC message, supporting both newline
// delimited JSON (the ACP default) and LSP-style Content-Length framing
func (c *acpClient) readMessage() ([]byte, error) {
	firstLine, err := c.stdout.ReadString('\n')
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
			header, err := c.stdout.ReadString('\n')
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
		body := make([]byte, length)
		if _, err := io.ReadFull(c.stdout, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	return []byte(line), nil
}

func (c *acpClient) readLoop() {
	defer close(c.readerDone)
	for {
		data, err := c.readMessage()
		if err != nil {
			c.failPending(err)
			return
		}
		if len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		if c.debug {
			fmt.Fprintf(os.Stderr, "[acp:%s] <- %s\r\n", c.agentName, string(data))
		}
		var msg acpRPCMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			go c.handleAgentRequest(&msg)
		case msg.Method != "":
			c.handleAgentNotification(&msg)
		case len(msg.ID) > 0:
			c.deliver(&msg)
		}
	}
}

func (c *acpClient) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		ch <- &acpRPCMsg{Error: &acpRPCError{Code: -32000, Message: fmt.Sprintf("agent exited: %v", err)}}
		delete(c.pending, id)
	}
}

func (c *acpClient) deliver(msg *acpRPCMsg) {
	key := string(bytes.TrimSpace(msg.ID))
	c.pendingMu.Lock()
	ch, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.pendingMu.Unlock()
	if ok {
		ch <- msg
	}
}

func (c *acpClient) writeJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if c.debug {
		fmt.Fprintf(os.Stderr, "[acp:%s] -> %s\r\n", c.agentName, string(data))
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	_, err = c.stdin.Write([]byte("\n"))
	return err
}

func (c *acpClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c.pendingMu.Lock()
	c.nextID++
	id := c.nextID
	key := strconv.FormatInt(id, 10)
	ch := make(chan *acpRPCMsg, 1)
	c.pending[key] = ch
	c.pendingMu.Unlock()

	req := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeJSON(req); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, key)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s", msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func (c *acpClient) notify(method string, params interface{}) error {
	req := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	if params != nil {
		req["params"] = params
	}
	return c.writeJSON(req)
}

func (c *acpClient) respond(id json.RawMessage, result interface{}, rpcErr *acpRPCError) {
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	_ = c.writeJSON(resp)
}

// handleAgentNotification consumes session/update notifications and
// accumulates streamed agent output
func (c *acpClient) handleAgentNotification(msg *acpRPCMsg) {
	if msg.Method != "session/update" {
		return
	}
	var p struct {
		Update struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       json.RawMessage `json:"content"`
			Title         string          `json:"title"`
		} `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	switch p.Update.SessionUpdate {
	case "agent_message_chunk":
		if text := acpContentText(p.Update.Content); text != "" && c.onChunk != nil {
			c.onChunk(text)
		}
	case "agent_message":
		if text := acpContentText(p.Update.Content); text != "" {
			c.fullMu.Lock()
			c.fullMsgs.WriteString(text)
			c.fullMu.Unlock()
		}
	case "tool_call":
		if p.Update.Title != "" && c.onToolCall != nil {
			c.onToolCall(p.Update.Title)
		}
	}
}

func (c *acpClient) fullMessages() string {
	c.fullMu.Lock()
	defer c.fullMu.Unlock()
	return c.fullMsgs.String()
}

// acpContentText extracts the text from an ACP content block (or list)
func acpContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var single struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &single); err == nil && single.Text != "" {
		return single.Text
	}
	var many []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &many); err == nil {
		var sb strings.Builder
		for _, block := range many {
			sb.WriteString(block.Text)
		}
		return sb.String()
	}
	return ""
}

// handleAgentRequest answers requests initiated by the agent
func (c *acpClient) handleAgentRequest(msg *acpRPCMsg) {
	switch msg.Method {
	case "session/request_permission":
		c.handlePermissionRequest(msg)
	case "fs/read_text_file":
		c.handleReadTextFile(msg)
	default:
		c.respond(msg.ID, nil, &acpRPCError{Code: -32601, Message: "method not supported by mai: " + msg.Method})
	}
}

type acpPermOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

func (c *acpClient) handlePermissionRequest(msg *acpRPCMsg) {
	var p struct {
		ToolCall struct {
			Title string `json:"title"`
			Kind  string `json:"kind"`
		} `json:"toolCall"`
		Options []acpPermOption `json:"options"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	if len(p.Options) == 0 {
		c.respond(msg.ID, map[string]interface{}{"outcome": map[string]interface{}{"outcome": "cancelled"}}, nil)
		return
	}
	allow := false
	switch c.permission {
	case "allow":
		allow = true
	case "reject":
		allow = false
	default: // auto: allow read-only tool kinds, reject the rest
		switch p.ToolCall.Kind {
		case "read", "search", "fetch", "think":
			allow = true
		}
	}
	optionID := pickACPPermissionOption(p.Options, allow)
	if c.debug {
		verdict := "reject"
		if allow {
			verdict = "allow"
		}
		fmt.Fprintf(os.Stderr, "[acp:%s] permission '%s' (%s) -> %s\r\n", c.agentName, p.ToolCall.Title, p.ToolCall.Kind, verdict)
	}
	c.respond(msg.ID, map[string]interface{}{"outcome": map[string]interface{}{"outcome": "selected", "optionId": optionID}}, nil)
}

func pickACPPermissionOption(options []acpPermOption, allow bool) string {
	prefs := []string{"reject_once", "reject_always"}
	if allow {
		prefs = []string{"allow_once", "allow_always"}
	}
	for _, kind := range prefs {
		for _, o := range options {
			if o.Kind == kind {
				return o.OptionID
			}
		}
	}
	if allow {
		return options[0].OptionID
	}
	return options[len(options)-1].OptionID
}

func (c *acpClient) handleReadTextFile(msg *acpRPCMsg) {
	var p struct {
		Path  string `json:"path"`
		Line  int    `json:"line"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil || p.Path == "" {
		c.respond(msg.ID, nil, &acpRPCError{Code: -32602, Message: "invalid params"})
		return
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		c.respond(msg.ID, nil, &acpRPCError{Code: -32603, Message: err.Error()})
		return
	}
	text := string(data)
	if p.Line > 0 || p.Limit > 0 {
		lines := strings.Split(text, "\n")
		start := 0
		if p.Line > 0 {
			start = p.Line - 1
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if p.Limit > 0 && start+p.Limit < end {
			end = start + p.Limit
		}
		text = strings.Join(lines[start:end], "\n")
	}
	c.respond(msg.ID, map[string]interface{}{"content": text}, nil)
}

func (c *acpClient) initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": 1,
		"clientInfo":      map[string]interface{}{"name": "mai", "version": Version},
		"clientCapabilities": map[string]interface{}{
			"fs":       map[string]interface{}{"readTextFile": true, "writeTextFile": false},
			"terminal": false,
		},
	}
	_, err := c.call(ctx, "initialize", params)
	return err
}

func (c *acpClient) newSession(ctx context.Context, cwd string) error {
	params := map[string]interface{}{"cwd": cwd, "mcpServers": []interface{}{}}
	result, err := c.call(ctx, "session/new", params)
	if err != nil {
		return err
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &res); err != nil || res.SessionID == "" {
		return errors.New("agent did not return a session id")
	}
	c.sessionID = res.SessionID
	return nil
}

func (c *acpClient) prompt(ctx context.Context, text string) (string, error) {
	params := map[string]interface{}{
		"sessionId": c.sessionID,
		"prompt":    []interface{}{map[string]interface{}{"type": "text", "text": text}},
	}
	result, err := c.call(ctx, "session/prompt", params)
	if err != nil {
		if ctx.Err() != nil && c.sessionID != "" {
			_ = c.notify("session/cancel", map[string]interface{}{"sessionId": c.sessionID})
		}
		return "", err
	}
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(result, &res)
	return res.StopReason, nil
}

func (c *acpClient) close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	select {
	case <-c.readerDone:
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		select {
		case <-c.readerDone:
		case <-time.After(2 * time.Second):
		}
	}
	_ = c.cmd.Wait()
}

// ---------------------------------------------------------------------------
// Job management

type acpJob struct {
	ID     int
	Agent  string
	Prompt string

	mu        sync.Mutex
	status    string // running, done, error or cancelled
	output    strings.Builder
	note      string
	lastTool  string
	started   time.Time
	ended     time.Time
	collected bool // result already shown by /acp wait

	cancel context.CancelFunc
	done   chan struct{}
}

func (j *acpJob) appendOutput(text string) {
	j.mu.Lock()
	j.output.WriteString(text)
	j.mu.Unlock()
}

func (j *acpJob) setTool(title string) {
	j.mu.Lock()
	j.lastTool = title
	j.mu.Unlock()
}

func (j *acpJob) finish(status, note string) {
	j.mu.Lock()
	j.status = status
	j.note = note
	j.ended = time.Now()
	j.mu.Unlock()
}

type acpJobState struct {
	status   string
	output   string
	note     string
	lastTool string
	duration time.Duration
}

func (j *acpJob) snapshot() acpJobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	duration := time.Since(j.started)
	if !j.ended.IsZero() {
		duration = j.ended.Sub(j.started)
	}
	return acpJobState{
		status:   j.status,
		output:   j.output.String(),
		note:     j.note,
		lastTool: j.lastTool,
		duration: duration,
	}
}

var (
	acpJobsMu   sync.Mutex
	acpJobsList []*acpJob
	acpJobSeq   int
)

func newACPJob(agent, prompt string, cancel context.CancelFunc) *acpJob {
	acpJobsMu.Lock()
	defer acpJobsMu.Unlock()
	acpJobSeq++
	job := &acpJob{
		ID:      acpJobSeq,
		Agent:   agent,
		Prompt:  prompt,
		status:  "running",
		started: time.Now(),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	acpJobsList = append(acpJobsList, job)
	return job
}

func findACPJob(id int) *acpJob {
	acpJobsMu.Lock()
	defer acpJobsMu.Unlock()
	for _, job := range acpJobsList {
		if job.ID == id {
			return job
		}
	}
	return nil
}

func (r *REPL) acpTimeout() time.Duration {
	secs, err := strconv.Atoi(r.configOptions.Get("acp.timeout"))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// startACPJob launches an agent in a goroutine and returns its job handle
func (r *REPL) startACPJob(agent *ACPAgent, prompt string) (*acpJob, error) {
	if _, err := agent.commandLine(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if t := r.acpTimeout(); t > 0 {
		ctx, cancel = context.WithTimeout(ctx, t)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	job := newACPJob(agent.Name, prompt, cancel)
	permission := r.configOptions.Get("acp.permission")
	debug := r.configOptions.GetBool("acp.debug")
	agentCopy := *agent
	go runACPJob(ctx, cancel, job, &agentCopy, permission, debug)
	return job, nil
}

func runACPJob(ctx context.Context, cancel context.CancelFunc, job *acpJob, agent *ACPAgent, permission string, debug bool) {
	defer close(job.done)
	defer cancel()

	client, err := startACPClient(agent, permission, debug)
	if err != nil {
		job.finish("error", err.Error())
		return
	}
	defer client.close()
	client.onChunk = job.appendOutput
	client.onToolCall = job.setTool

	if err := client.initialize(ctx); err != nil {
		job.finish("error", acpErrorDetail("initialize failed", err, client))
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		job.finish("error", fmt.Sprintf("cannot get working directory: %v", err))
		return
	}
	if err := client.newSession(ctx, cwd); err != nil {
		job.finish("error", acpErrorDetail("session/new failed (the agent may require logging in with its own CLI first)", err, client))
		return
	}
	stopReason, err := client.prompt(ctx, job.Prompt)
	if err != nil {
		switch {
		case ctx.Err() == context.Canceled:
			job.finish("cancelled", "")
		case ctx.Err() == context.DeadlineExceeded:
			job.finish("error", "timed out, raise the acp.timeout config option if needed")
		default:
			job.finish("error", acpErrorDetail("prompt failed", err, client))
		}
		return
	}
	// Fall back to whole-message updates when the agent sent no chunks
	if job.snapshot().output == "" {
		if full := client.fullMessages(); full != "" {
			job.appendOutput(full)
		}
	}
	switch stopReason {
	case "", "end_turn":
		job.finish("done", "")
	case "cancelled":
		job.finish("cancelled", "")
	default:
		job.finish("done", "stopReason: "+stopReason)
	}
}

func acpErrorDetail(prefix string, err error, client *acpClient) string {
	msg := fmt.Sprintf("%s: %v", prefix, err)
	if tail := strings.TrimSpace(client.stderr.String()); tail != "" {
		lines := strings.Split(tail, "\n")
		if len(lines) > 5 {
			lines = lines[len(lines)-5:]
		}
		msg += " | stderr: " + strings.Join(lines, " | ")
	}
	return msg
}

// ---------------------------------------------------------------------------
// /acp command

func registerACPOptions(co *ConfigOptions) {
	co.RegisterOption("acp.permission", StringOption, "Reply to ACP agent permission requests: allow, reject or auto (allow read-only tools)", "auto")
	co.RegisterOption("acp.timeout", NumberOption, "Seconds to wait for an ACP sub-agent prompt (0 = no timeout)", "600")
	co.RegisterOption("acp.debug", BooleanOption, "Log ACP JSON-RPC traffic to stderr", "false")
}

func registerACPCommands(r *REPL) {
	r.commands["/acp"] = Command{
		Name:        "/acp",
		Description: "Run ACP sub-agents (gemini, claude, codex, ...) and manage their jobs",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleACPCommand(args)
		},
	}
}

func acpUsage() string {
	var output strings.Builder
	output.WriteString("ACP sub-agent commands:\r\n")
	output.WriteString("  /acp list                 - List known ACP agents and their availability\r\n")
	output.WriteString("  /acp info <agent>         - Show agent details and install instructions\r\n")
	output.WriteString("  /acp run <agent> <prompt> - Run a prompt on an agent and wait for the reply\r\n")
	output.WriteString("  /acp bg <agent> <prompt>  - Run a prompt in the background (parallel job)\r\n")
	output.WriteString("  /acp jobs                 - List jobs and their status\r\n")
	output.WriteString("  /acp output <id>          - Show the (partial) output of a job\r\n")
	output.WriteString("  /acp wait [id ...]        - Wait for all (or the given) jobs and collect results\r\n")
	output.WriteString("  /acp kill <id|all>        - Cancel running job(s)\r\n")
	output.WriteString("  /acp edit                 - Edit user-defined agents (~/.config/mai/acp.json)\r\n")
	output.WriteString("Config: acp.permission (allow|reject|auto), acp.timeout, acp.debug\r\n")
	return output.String()
}

func (r *REPL) handleACPCommand(args []string) (string, error) {
	if len(args) < 2 {
		return acpUsage(), nil
	}
	switch args[1] {
	case "help", "-h":
		return acpUsage(), nil
	case "list", "ls":
		return r.acpList()
	case "info":
		if len(args) < 3 {
			return "Usage: /acp info <agent>\r\n", nil
		}
		return r.acpInfo(args[2])
	case "run":
		return r.acpRun(args[2:], false)
	case "bg", "spawn":
		return r.acpRun(args[2:], true)
	case "jobs", "ps", "status":
		return acpJobsTable(), nil
	case "output", "out", "result":
		if len(args) < 3 {
			return "Usage: /acp output <id>\r\n", nil
		}
		return r.acpOutput(args[2])
	case "wait", "collect":
		return r.acpWait(args[2:])
	case "kill", "stop", "cancel":
		if len(args) < 3 {
			return "Usage: /acp kill <id|all>\r\n", nil
		}
		return acpKill(args[2])
	case "edit":
		return r.acpEdit()
	default:
		// convenience shortcut: /acp <agent> <prompt> behaves like /acp run
		if len(args) > 2 && findACPAgent(args[1]) != nil {
			return r.acpRun(args[1:], false)
		}
		return fmt.Sprintf("Unknown acp action: %s\r\n%s", args[1], acpUsage()), nil
	}
}

func (r *REPL) acpList() (string, error) {
	var output strings.Builder
	output.WriteString("ACP agents (✅ installed, 📦 runnable via npx, ❌ not installed):\r\n")
	for _, agent := range allACPAgents() {
		avail, _ := agent.availability()
		var emoji string
		switch avail {
		case acpInstalled:
			emoji = "\033[92m✅\033[0m"
		case acpViaNpx:
			emoji = "\033[93m📦\033[0m"
		default:
			emoji = "\033[91m❌\033[0m"
		}
		var tags []string
		if agent.Adapter {
			tags = append(tags, "adapter")
		}
		if agent.UserDefined {
			tags = append(tags, "user")
		}
		tagStr := ""
		if len(tags) > 0 {
			tagStr = " [" + strings.Join(tags, ", ") + "]"
		}
		fmt.Fprintf(&output, "%s %-10s - %s%s\r\n", emoji, agent.Name, agent.Description, tagStr)
	}
	output.WriteString("\r\nUse '/acp run <agent> <prompt>' to launch one, '/acp info <agent>' for details\r\n")
	return output.String(), nil
}

func (r *REPL) acpInfo(name string) (string, error) {
	agent := findACPAgent(name)
	if agent == nil {
		return fmt.Sprintf("Unknown ACP agent: %s (see /acp list)\r\n", name), nil
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Name:        %s\r\n", agent.Name)
	if len(agent.Aliases) > 0 {
		fmt.Fprintf(&output, "Aliases:     %s\r\n", strings.Join(agent.Aliases, ", "))
	}
	fmt.Fprintf(&output, "Description: %s\r\n", agent.Description)
	kind := "native ACP agent"
	if agent.Adapter {
		kind = "ACP adapter"
	}
	if agent.UserDefined {
		kind += " (user-defined)"
	}
	fmt.Fprintf(&output, "Type:        %s\r\n", kind)
	avail, _ := agent.availability()
	switch avail {
	case acpInstalled:
		cmdline, _ := agent.commandLine()
		fmt.Fprintf(&output, "Status:      installed\r\n")
		fmt.Fprintf(&output, "Command:     %s\r\n", strings.Join(cmdline, " "))
	case acpViaNpx:
		fmt.Fprintf(&output, "Status:      not installed, will run through npx (%s)\r\n", agent.NpxPackage)
	default:
		fmt.Fprintf(&output, "Status:      not installed\r\n")
	}
	fmt.Fprintf(&output, "Install:     %s\r\n", agent.Install)
	return output.String(), nil
}

func (r *REPL) acpRun(args []string, background bool) (string, error) {
	if len(args) < 2 {
		return "Usage: /acp run <agent> <prompt>\r\n", nil
	}
	agent := findACPAgent(args[0])
	if agent == nil {
		return fmt.Sprintf("Unknown ACP agent: %s (see /acp list)\r\n", args[0]), nil
	}
	prompt := strings.Join(args[1:], " ")
	job, err := r.startACPJob(agent, prompt)
	if err != nil {
		return fmt.Sprintf("%v\r\n", err), nil
	}
	if background {
		return fmt.Sprintf("[%d] %s job started in the background (see /acp jobs)\r\n", job.ID, agent.Name), nil
	}
	fmt.Printf("Running %s (job %d)...\r\n", agent.Name, job.ID)
	<-job.done
	return r.acpJobReport(job, false), nil
}

// acpJobReport formats the final output of a finished job
func (r *REPL) acpJobReport(job *acpJob, withHeader bool) string {
	st := job.snapshot()
	var output strings.Builder
	if withHeader {
		fmt.Fprintf(&output, "── job %d (%s) ── %s in %s\r\n", job.ID, job.Agent, st.status, st.duration.Truncate(100*time.Millisecond))
	}
	text := st.output
	if text != "" {
		if r.configOptions.GetBool("ui.markdown") {
			text = r.renderMarkdown(text)
		}
		output.WriteString(strings.ReplaceAll(text, "\n", "\r\n"))
		if !strings.HasSuffix(text, "\n") {
			output.WriteString("\r\n")
		}
	}
	switch {
	case st.status == "error":
		fmt.Fprintf(&output, "[%s job %d error] %s\r\n", job.Agent, job.ID, st.note)
	case st.status == "cancelled":
		fmt.Fprintf(&output, "[%s job %d cancelled]\r\n", job.Agent, job.ID)
	case st.note != "":
		fmt.Fprintf(&output, "[%s job %d: %s]\r\n", job.Agent, job.ID, st.note)
	case st.output == "":
		fmt.Fprintf(&output, "[%s job %d finished with no output]\r\n", job.Agent, job.ID)
	}
	return output.String()
}

func acpJobsTable() string {
	acpJobsMu.Lock()
	jobs := append([]*acpJob(nil), acpJobsList...)
	acpJobsMu.Unlock()
	if len(jobs) == 0 {
		return "No ACP jobs. Start one with '/acp bg <agent> <prompt>'\r\n"
	}
	var output strings.Builder
	output.WriteString("ACP jobs:\r\n")
	for _, job := range jobs {
		st := job.snapshot()
		prompt := job.Prompt
		if len(prompt) > 40 {
			prompt = prompt[:40] + "..."
		}
		fmt.Fprintf(&output, "  %3d  %-10s %-9s %8s  %s\r\n", job.ID, job.Agent, st.status, st.duration.Truncate(100*time.Millisecond), prompt)
		if st.status == "running" && st.lastTool != "" {
			fmt.Fprintf(&output, "       ↳ %s\r\n", st.lastTool)
		}
	}
	return output.String()
}

func (r *REPL) acpOutput(arg string) (string, error) {
	id, err := strconv.Atoi(arg)
	if err != nil {
		return fmt.Sprintf("Invalid job id: %s\r\n", arg), nil
	}
	job := findACPJob(id)
	if job == nil {
		return fmt.Sprintf("No such ACP job: %d\r\n", id), nil
	}
	st := job.snapshot()
	if st.status == "running" {
		partial := st.output
		if partial == "" {
			return fmt.Sprintf("Job %d (%s) is still running with no output yet\r\n", job.ID, job.Agent), nil
		}
		return fmt.Sprintf("Job %d (%s) is still running, partial output:\r\n%s\r\n", job.ID, job.Agent, strings.ReplaceAll(partial, "\n", "\r\n")), nil
	}
	return r.acpJobReport(job, true), nil
}

// acpWait waits for the given jobs (or every job whose result was not
// collected yet) and returns their outputs
func (r *REPL) acpWait(args []string) (string, error) {
	var jobs []*acpJob
	if len(args) == 0 {
		acpJobsMu.Lock()
		for _, job := range acpJobsList {
			job.mu.Lock()
			pending := !job.collected
			job.mu.Unlock()
			if pending {
				jobs = append(jobs, job)
			}
		}
		acpJobsMu.Unlock()
		if len(jobs) == 0 {
			return "No ACP job results to collect\r\n", nil
		}
	} else {
		for _, arg := range args {
			id, err := strconv.Atoi(arg)
			if err != nil {
				return fmt.Sprintf("Invalid job id: %s\r\n", arg), nil
			}
			job := findACPJob(id)
			if job == nil {
				return fmt.Sprintf("No such ACP job: %d\r\n", id), nil
			}
			jobs = append(jobs, job)
		}
	}
	fmt.Printf("Collecting %d ACP job(s)...\r\n", len(jobs))
	var output strings.Builder
	for _, job := range jobs {
		<-job.done
		output.WriteString(r.acpJobReport(job, true))
		job.mu.Lock()
		job.collected = true
		job.mu.Unlock()
	}
	return output.String(), nil
}

func acpKill(arg string) (string, error) {
	if arg == "all" {
		acpJobsMu.Lock()
		jobs := append([]*acpJob(nil), acpJobsList...)
		acpJobsMu.Unlock()
		count := 0
		for _, job := range jobs {
			if job.snapshot().status == "running" {
				job.cancel()
				count++
			}
		}
		return fmt.Sprintf("Cancelling %d ACP job(s)\r\n", count), nil
	}
	id, err := strconv.Atoi(arg)
	if err != nil {
		return fmt.Sprintf("Invalid job id: %s\r\n", arg), nil
	}
	job := findACPJob(id)
	if job == nil {
		return fmt.Sprintf("No such ACP job: %d\r\n", id), nil
	}
	if job.snapshot().status != "running" {
		return fmt.Sprintf("Job %d is not running\r\n", id), nil
	}
	job.cancel()
	return fmt.Sprintf("Cancelling job %d (%s)\r\n", job.ID, job.Agent), nil
}

func (r *REPL) acpEdit() (string, error) {
	path, err := acpConfigPath()
	if err != nil {
		return fmt.Sprintf("Failed to get config path: %v\r\n", err), nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		sample := acpUserConfig{Agents: map[string]acpUserAgent{
			"example": {
				Description: "Example user-defined ACP agent",
				Command:     "some-acp-agent",
				Args:        []string{"--acp"},
				Env:         map[string]string{},
			},
		}}
		data, _ := json.MarshalIndent(sample, "", "  ")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Sprintf("Failed to create %s: %v\r\n", path, err), nil
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "nano"
		}
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("Failed to open editor: %v\r\n", err), nil
	}
	return fmt.Sprintf("ACP agents config: %s\r\n", path), nil
}
