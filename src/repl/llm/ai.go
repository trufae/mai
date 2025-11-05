package llm

// ToolCall represents a tool call in the native tool calling protocol
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction represents the function part of a tool call
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// OpenAITool represents a tool in OpenAI's tool calling format
type OpenAITool struct {
	Type     string             `json:"type"`
	Function OpenAIToolFunction `json:"function"`
}

// OpenAIToolFunction represents the function part of an OpenAI tool
type OpenAIToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Message represents a chat message with a role and content.
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
	// Optional images attached to this message (base64 or URLs depending on provider)
	Images []string `json:"images,omitempty"`
	// Tool calls for native tool calling protocol
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Tool call ID for tool result messages
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Agent represents an AI agent configuration
type Agent struct {
	Description     string            `json:"description"`
	SystemPrompt    string            `json:"systemPrompt,omitempty"`
	MCPS            []string          `json:"mcps,omitempty"`
	Tools           AgentTools        `json:"tools,omitempty"`
	Config          map[string]string `json:"config,omitempty"`
	DefaultProvider string            `json:"defaultProvider,omitempty"`
	DefaultModel    string            `json:"defaultModel,omitempty"`
}

// AgentTools defines tool access for an agent
type AgentTools struct {
	Allowed   []string `json:"allowed,omitempty"`
	Forbidden []string `json:"forbidden,omitempty"`
	Yolo      []string `json:"yolo,omitempty"`
}

// Config holds configuration values for LLM providers.
type Config struct {
	PROVIDER  string
	Model     string
	NoStream  bool
	ImagePath string
	BaseURL   string
	UserAgent string

	IsStdinMode      bool
	SkipRcFile       bool
	InitialCommand   string
	QuitAfterActions bool
	Deterministic    bool
	Markdown         bool
	Rawdog           bool

	// DemoMode enables the simple waiting animation in the REPL when set.
	DemoMode bool

	// Optional structured output schema support
	// When set, providers should constrain output to this JSON schema.
	Schema map[string]interface{}

	// Optional system prompt overrides. When set, providers and callers
	// should prefer these over the repository or home `.config/mai/systemprompt.md`.
	SystemPrompt     string
	SystemPromptFile string

	// PromptFile is a user-level prompt that should be included into the
	// user message (equivalent to using `#promptname` in the REPL).
	PromptFile string

	// Debug enables printing of raw messages sent to the provider for
	// troubleshooting and visibility into what the model actually receives.
	Debug bool

	// Conversation formatting options for providers that need a single prompt
	// when using structured output schemas. These allow constructing a single
	// prompt from the conversation history instead of taking a single message.
	// If left at their zero values, existing behavior is preserved.
	ConversationIncludeLLM    bool   // include assistant/LLM messages when building the prompt
	ConversationIncludeSystem bool   // include system messages when building the prompt
	ConversationFormat        string // "tokens", "labeled", or "plain"
	ConversationUseLastUser   bool   // if true, only include the last user message (and system messages if enabled)
	// ConversationMessageLimit controls how many recent messages are sent to the LLM.
	// If zero, all messages are sent. If >0, only the last N messages are included.
	ConversationMessageLimit int

	// MCP (Model Context Protocol) options for tool usage
	UseMCP     bool
	MCPNative  bool
	MCPGrammar bool
	MCPDisplay string
	MCPReason  string
	MCPTimeout int
	MCPDebug   bool
	MCPBaseURL string

	// ThinkHide controls whether content inside <think>...</think> tags is hidden
	// should be hidden from internal messages and the user terminal.
	// When true, think regions are removed from displayed output. When
	// false, think regions are left intact (except leading think blocks
	// which are trimmed at the start of responses).
	ThinkHide bool

	// ShowTPS enables displaying time statistics (time to first token,
	// generation time, tokens/second, chars/second) after LLM responses.
	ShowTPS bool

	// Agent configuration for when using named agents
	AgentName   string
	AgentConfig *Agent
}
