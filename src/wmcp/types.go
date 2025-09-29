package main

import (
	"io"
	"os/exec"
	"sync"
)

// JSONRPC structures
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      interface{} `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// MCP Tool structures

// ToolParameter represents a parameter for a tool
type ToolParameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Parameters  []ToolParameter        `json:"parameters,omitempty"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// MCP Prompt structures (MCP Prompts API)
// PromptArgument represents a parameter for a prompt
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt represents a single prompt available on the server
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptsListResult is the result for prompts/list
type PromptsListResult struct {
	Prompts []Prompt `json:"prompts"`
}

// GetPromptParams is the params object for prompts/get
type GetPromptParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// PromptMessageContent models a prompt message's content item
type PromptMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// PromptMessage models a message returned by prompts/get
type PromptMessage struct {
	Role    string                 `json:"role"`
	Content []PromptMessageContent `json:"content"`
}

// GetPromptResult is the result for prompts/get
type GetPromptResult struct {
	Messages []PromptMessage `json:"messages"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type CallToolResult struct {
	Content []Content      `json:"content,omitempty"`
	Error   *CallToolError `json:"error,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Yolo prompt decision type
type YoloDecision int

const (
	YoloApprove YoloDecision = iota
	YoloReject
	YoloPermitToolForever
	YoloPermitToolWithParamsForever
	YoloRejectForever
	YoloPermitAllToolsForever
	YoloModify
	YoloToolNotFound
	YoloCustomResponse
	YoloGuideModel
	YoloCustomToolResponse
)

// Prompt decision type
type PromptDecision int

const (
	PromptApprove PromptDecision = iota
	PromptReject
	PromptPermitPromptForever
	PromptPermitPromptWithArgsForever
	PromptRejectForever
	PromptPermitAllPromptsForever
	PromptCustom
	PromptList
)

// Tool permission record
type ToolPermission struct {
	ToolName   string
	Parameters string // JSON string of parameters for exact matching
	Approved   bool
}

// Prompt permission record
type PromptPermission struct {
	PromptName string
	Arguments  string // JSON string of arguments for exact matching
	Approved   bool
}

// ReportEntry represents a single entry in the report
type ReportEntry struct {
	Timestamp string      `json:"timestamp"`
	Server    string      `json:"server"`
	Tool      string      `json:"tool"`
	Params    interface{} `json:"params"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// Report represents a collection of tool executions
type Report struct {
	Entries []ReportEntry `json:"entries"`
}

// MCP Server represents a running MCP server process
type MCPServer struct {
	Name          string
	Command       string
	Process       *exec.Cmd
	Stdin         io.WriteCloser
	Stdout        io.ReadCloser
	Stderr        io.ReadCloser
	Tools         []Tool
	Prompts       []Prompt
	mutex         sync.RWMutex
	stderrDone    chan struct{}
	stderrActive  bool
	monitorDone   chan struct{}
	monitorActive bool
}

// MCPService manages multiple MCP servers
type MCPService struct {
	servers         map[string]*MCPServer
	mutex           sync.RWMutex
	yoloMode        bool
	drunkMode       bool
	debugMode       bool
	toolPerms       map[string]ToolPermission // Map tool name or tool+params hash to permission
	toolPermsLock   sync.RWMutex
	promptPerms     map[string]PromptPermission // Map prompt name or prompt+args hash to permission
	promptPermsLock sync.RWMutex
	reportEnabled   bool
	reportFile      string
	report          Report
	reportLock      sync.RWMutex
}
