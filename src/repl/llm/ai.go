package llm

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
	GeminiKey     string
	OpenAIKey     string
	ClaudeKey     string
	DeepSeekKey   string
	MistralKey    string
	BedrockKey    string
	BedrockRegion string
	XAIKey        string
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

	// Optional structured output schema support
	// When set, providers should constrain output to this JSON schema.
	Schema map[string]interface{}

	// Conversation formatting options for providers that need a single prompt
	// when using structured output schemas. These allow constructing a single
	// prompt from the conversation history instead of taking a single message.
	// If left at their zero values, existing behavior is preserved.
	ConversationIncludeLLM    bool   // include assistant/LLM messages when building the prompt
	ConversationIncludeSystem bool   // include system messages when building the prompt
	ConversationFormat        string // "tokens", "labeled", or "plain"
	ConversationUseLastUser   bool   // if true, only include the last user message (and system messages if enabled)

}
