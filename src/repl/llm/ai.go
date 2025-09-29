package llm

import (
	"os"
	"path/filepath"
	"strings"
)

// Message represents a chat message with a role and content.
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
	// Optional images attached to this message (base64 or URLs depending on provider)
	Images []string `json:"images,omitempty"`
}

// Config holds configuration values for LLM providers.
type Config struct {
	OpenAPIHost   string
	OpenAPIPort   string
	OllamaHost    string
	OllamaPort    string
	BedrockRegion string
	PROVIDER      string
	Model         string
	NoStream      bool
	ImagePath     string
	BaseURL       string
	UserAgent     string

	IsStdinMode   bool
	SkipRcFile    bool
	Deterministic bool
	Markdown      bool
	Rawdog        bool

	// DemoMode enables the simple waiting animation in the REPL when set.
	DemoMode bool

	// Optional structured output schema support
	// When set, providers should constrain output to this JSON schema.
	Schema map[string]interface{}

	// Optional system prompt overrides. When set, providers and callers
	// should prefer these over the repository or home `.mai/systemprompt.md`.
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
	MCPGrammar bool
	MCPDisplay string
	MCPReason  string
	MCPTimeout int
	MCPDebug   bool
	MCPBaseURL string
}

// GetAPIKey resolves an API key by checking an environment variable first,
// then falling back to reading the first available file from the provided list.
// Filenames may include a leading '~' which will be expanded to the user home.
func GetAPIKey(envVar string, filenames ...string) string {
	if v := os.Getenv(envVar); v != "" {
		return strings.TrimSpace(v)
	}
	for _, fn := range filenames {
		if fn == "" {
			continue
		}
		// Expand ~ to home directory
		if strings.HasPrefix(fn, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				fn = filepath.Join(home, fn[1:])
			}
		}
		data, err := os.ReadFile(fn)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s != "" {
			return s
		}
	}
	return ""
}
