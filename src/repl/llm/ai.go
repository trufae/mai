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
	OllamaModel   string
	GeminiKey     string
	GeminiModel   string
	OpenAIKey     string
	OpenAIModel   string
	ClaudeKey     string
	ClaudeModel   string
	DeepSeekKey   string
	DeepSeekModel string
	MistralKey    string
	MistralModel  string
	BedrockKey    string
	BedrockModel  string
	BedrockRegion string
	PROVIDER      string
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
}
