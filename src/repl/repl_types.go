package main

import (
	"context"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/trufae/mai/src/repl/llm"
)

// Command represents a REPL command with its description and handler
type Command struct {
	Name        string
	Description string
	Handler     func(r *REPL, args []string) (string, error)
}

type REPL struct {
	configOptions    ConfigOptions
	currentClient    *llm.LLMClient
	readline         *ReadLine // Persistent readline instance for input handling
	currentInput     strings.Builder
	cursorPos        int // Current cursor position in the line
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	isStreaming      bool
	isInterrupted    bool
	oldState         *term.State
	completeState    int
	completeOptions  []string
	completePrefix   string
	completeIdx      int    // Current index in completion options
	lastTabInput     string // last input text when Tab was pressed
	messages         []llm.Message
	pendingFiles     []pendingFile      // Files and images to include in the next message
	commands         map[string]Command // Registry of available commands
	skillRegistry    *SkillRegistry     // Registry of available skills
	currentSession   string             // Name of the active chat session
	unsavedTopic     string             // Topic for unsaved session before saving to disk
	initialCommand   string             // Command to execute on startup
	quitAfterActions bool               // Exit after executing initial command
	// Guard to avoid recursive followup execution
	followupInProgress bool
	// Callback to stop demo animation when first token is received
	stopDemoCallback func()
	wmcpProcess      *exec.Cmd
	wmcpPort         int
	mcpProcesses     map[string]*MCPProcess // Track individual MCP processes
	mcpConfig        *MCPConfig             // Current MCP configuration
}

type pendingFile struct {
	filePath string
	content  string
	isImage  bool
	imageB64 string // Base64 encoded image data
}

// MCPConfig represents the MCP servers configuration
type MCPConfig struct {
	Servers map[string]MCPServer `json:"servers"`
}

// MCPServer represents a single MCP server configuration
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

// MCPProcess represents a running MCP process
type MCPProcess struct {
	Name    string
	Process *exec.Cmd
	Port    int
}

type StreamingClient interface {
	StreamChat(ctx context.Context, messages []llm.Message) (<-chan string, <-chan error)
}
