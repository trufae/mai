package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

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
	ShowScissors  bool
	PROVIDER      string
	NoStream      bool
	ImagePath     string         // Path to image to send with the message
	BaseURL       string         // Base URL to connect to LLM API
	UserAgent     string         // User agent for HTTP requests
	IsStdinMode   bool           // Whether running in stdin mode
	SkipRcFile    bool           // Whether to skip loading ~/.mairc
	options       *ConfigOptions // Configuration options
}

// runStdinMode handles sending messages to LLM in stdin mode.
func runStdinMode(config *Config, args []string) {
	input := readInput(args)

	// Create LLM client
	client, err := NewLLMClient(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing LLM client: %v\n", err)
		os.Exit(1)
	}

	// Prepare messages from input
	messages := PrepareMessages(input)

	// Prepare image if specified
	var images []string
	if config.ImagePath != "" {
		imageData, err := os.ReadFile(config.ImagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading image file: %v\n", err)
			os.Exit(1)
		}

		encoded := base64.StdEncoding.EncodeToString(imageData)
		mimeType := http.DetectContentType(imageData)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
		images = append(images, dataURI)
		fmt.Fprintf(os.Stderr, "Attaching image: %s (%d bytes)\n", config.ImagePath, len(imageData))
	}

	// Send to LLM without streaming (for stdin mode)
	res, err := client.SendMessageWithImages(messages, false, images)
	if err != nil {
		fmt.Fprintf(os.Stderr, "REPL error: %v\n", err)
	}
	fmt.Println(res)
}

type ClaudeRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
}

type OpenAIRequest struct {
	Model               string    `json:"model"`
	MaxCompletionTokens int       `json:"max_completion_tokens"`
	Messages            []Message `json:"messages"`
}

type OllamaRequest struct {
	Stream   bool               `json:"stream"`
	Model    string             `json:"model"`
	Messages []Message          `json:"messages"`
	Options  map[string]float64 `json:"options,omitempty"`
}

type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}

type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type DeepSeekRequest struct {
	Model    string    `json:"model"`
	Stream   string    `json:"stream"`
	Messages []Message `json:"messages"`
}

type OpenAPIRequest struct {
	Prompt string `json:"prompt"`
}

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ClaudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type OllamaResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

type DeepSeekResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type MistralRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
}

type BedrockRequest struct {
	ModelId         string                 `json:"modelId"`
	InferenceParams BedrockInferenceParams `json:"inferenceParams"`
	Input           BedrockInput           `json:"input"`
}

type BedrockInferenceParams struct {
	MaxTokens   int     `json:"maxTokenCount"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"topP"`
}

type BedrockInput struct {
	Messages []Message `json:"messages"`
}

type MistralResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type BedrockResponse struct {
	Output struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"output"`
}

type OpenAPIResponse struct {
	Content string `json:"content"`
}

func loadConfig() *Config {
	config := &Config{
		OpenAPIHost:   getEnvOrDefault("OPENAPI_HOST", "localhost"),
		OpenAPIPort:   getEnvOrDefault("OPENAPI_PORT", "8080"),
		OllamaHost:    getEnvOrDefault("OLLAMA_HOST", "localhost"),
		OllamaPort:    getEnvOrDefault("OLLAMA_PORT", "11434"),
		OllamaModel:   getEnvOrDefault("OLLAMA_MODEL", "gemma3:1b"),
		GeminiModel:   getEnvOrDefault("GEMINI_MODEL", "gemini-1.5-flash"),
		OpenAIModel:   getEnvOrDefault("OPENAI_MODEL", "gpt-4o"),
		ClaudeModel:   getEnvOrDefault("CLAUDE_MODEL", "claude-3-5-sonnet-20241022"),
		DeepSeekModel: getEnvOrDefault("DEEPSEEK_MODEL", "deepseek-chat"),
		MistralModel:  getEnvOrDefault("MISTRAL_MODEL", "mistral-large-latest"),
		BedrockModel:  getEnvOrDefault("BEDROCK_MODEL", "anthropic.claude-3-5-sonnet-v1"),
		BedrockRegion: getEnvOrDefault("AWS_REGION", "us-west-2"),
		ShowScissors:  false,
		PROVIDER:      getEnvOrDefault("MAI_PROVIDER", "ollama"),
		GeminiKey:     os.Getenv("GEMINI_API_KEY"),
		OpenAIKey:     os.Getenv("OPENAI_API_KEY"),
		ClaudeKey:     os.Getenv("CLAUDE_API_KEY"),
		DeepSeekKey:   os.Getenv("DEEPSEEK_API_KEY"),
		MistralKey:    os.Getenv("MISTRAL_API_KEY"),
		BedrockKey:    os.Getenv("AWS_ACCESS_KEY_ID"),
		BaseURL:       getEnvOrDefault("MAI_BASEURL", ""),
		UserAgent:     getEnvOrDefault("MAI_USERAGENT", "mai-repl/1.0"),
		NoStream:      false,
		options:       NewConfigOptions(), // Initialize configuration options
	}

	// Load API keys from files if environment variables are not set
	if config.GeminiKey == "" {
		if key := readKeyFile("~/.r2ai.gemini-key"); key != "" {
			config.GeminiKey = key
		}
	}
	if config.OpenAIKey == "" {
		if key := readKeyFile("~/.r2ai.openai-key"); key != "" {
			config.OpenAIKey = key
		}
	}
	if config.ClaudeKey == "" {
		if key := readKeyFile("~/.r2ai.anthropic-key"); key != "" {
			config.ClaudeKey = key
		}
	}
	if config.DeepSeekKey == "" {
		if key := readKeyFile("~/.r2ai.deepseek-key"); key != "" {
			config.DeepSeekKey = key
		}
	}
	if config.MistralKey == "" {
		if key := readKeyFile("~/.r2ai.mistral-key"); key != "" {
			config.MistralKey = key
		}
	}
	if config.BedrockKey == "" {
		if key := readKeyFile("~/.r2ai.bedrock-key"); key != "" {
			config.BedrockKey = key
		}
	}

	return config
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func readKeyFile(path string) string {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(homeDir, path[1:])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readInput(args []string) string {
	var input strings.Builder

	// Add command line arguments
	if len(args) > 0 {
		input.WriteString(strings.Join(args, " "))
		input.WriteString("\n")
	}

	// input.WriteString("<INPUT>\n")

	// Read from stdin
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input.WriteString(scanner.Text())
		input.WriteString("\n")
	}

	// input.WriteString("</INPUT>")

	return input.String()
}

func printScissors(config *Config) {
	if config.ShowScissors {
		fmt.Print("\r\n------------8<------------\r\n")
	}
}

func makeRequest(method, url string, headers map[string]string, body []byte, config *Config) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating HTTP request: %v\n", err)
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Set User-Agent header if specified
	if config != nil && config.UserAgent != "" {
		req.Header.Set("User-Agent", config.UserAgent)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// fmt.Fprintf(os.Stderr, "Sending %s request to %s\n", method, url)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP request failed: %v\n\r", err)
		return nil, err
	}
	defer resp.Body.Close()

	// fmt.Fprintf(os.Stderr, "Response status code: %d\n", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Non-200 status code: %d %s\n\r", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response body: %v\n\r", err)
		return nil, err
	}

	return respBody, nil
}

func callClaude(config *Config, input string) error {
	// Parse input to check if it contains a system prompt
	systemPrompt := ""
	userPrompt := input

	// Simplified parsing to extract system prompt if it's at the beginning
	if strings.HasPrefix(input, "<system>\n") {
		parts := strings.SplitN(input, "</system>\n", 2)
		if len(parts) == 2 {
			systemPrompt = strings.TrimPrefix(parts[0], "<system>\n")
			userPrompt = parts[1]
		}
	}

	messages := []Message{}

	// Add system message if present
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: userPrompt})

	request := ClaudeRequest{
		Model:     config.ClaudeModel,
		MaxTokens: 5128,
		Messages:  messages,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": "2023-06-01",
		"x-api-key":         config.ClaudeKey,
	}

	printScissors(config)

	respBody, err := makeRequest("POST", "https://api.anthropic.com/v1/messages", headers, jsonData, config)
	if err != nil {
		return err
	}

	var response ClaudeResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Content) > 0 {
		// Replace \n with \r\n in the response
		fmt.Print(strings.ReplaceAll(response.Content[0].Text, "\n", "\r\n"))
	}

	printScissors(config)
	return nil
}

func callOpenAI(config *Config, input string) error {
	// Parse input to check if it contains a system prompt
	systemPrompt := ""
	userPrompt := input

	// Simplified parsing to extract system prompt if it's at the beginning
	if strings.HasPrefix(input, "<system>\n") {
		parts := strings.SplitN(input, "</system>\n", 2)
		if len(parts) == 2 {
			systemPrompt = strings.TrimPrefix(parts[0], "<system>\n")
			userPrompt = parts[1]
		}
	}

	messages := []Message{}

	// Add system message if present
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: userPrompt})

	request := OpenAIRequest{
		Model:               config.OpenAIModel,
		MaxCompletionTokens: 5128,
		Messages:            messages,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + config.OpenAIKey,
	}

	printScissors(config)

	respBody, err := makeRequest("POST", "https://api.openai.com/v1/chat/completions", headers, jsonData, config)
	if err != nil {
		return err
	}

	var response OpenAIResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Choices) > 0 {
		// Replace \n with \r\n in the response
		fmt.Print(strings.ReplaceAll(response.Choices[0].Message.Content, "\n", "\r\n"))
	}

	printScissors(config)
	return nil
}

func callOllama(config *Config, input string) error {
	// Parse input to check if it contains a system prompt
	systemPrompt := ""
	userPrompt := input

	// Simplified parsing to extract system prompt if it's at the beginning
	if strings.HasPrefix(input, "<system>\n") {
		parts := strings.SplitN(input, "</system>\n", 2)
		if len(parts) == 2 {
			systemPrompt = strings.TrimPrefix(parts[0], "<system>\n")
			userPrompt = parts[1]
		}
	}

	messages := []Message{}

	// Add system message if present
	if systemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}

	// Add user message
	messages = append(messages, Message{Role: "user", Content: userPrompt})

	request := OllamaRequest{
		Stream:   false,
		Model:    config.OllamaModel,
		Messages: messages,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling Ollama request: %v\n", err)
		return err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("http://%s:%s/api/chat", config.OllamaHost, config.OllamaPort)
	fmt.Fprintf(os.Stderr, "Connecting to Ollama at: %s\n", url)
	fmt.Fprintf(os.Stderr, "Using model: %s\n", config.OllamaModel)

	printScissors(config)

	respBody, err := makeRequest("POST", url, headers, jsonData, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to Ollama API: %v\n", err)
		fmt.Fprintf(os.Stderr, "Check if Ollama server is running at %s:%s\n", config.OllamaHost, config.OllamaPort)
		return err
	}

	if len(respBody) == 0 {
		fmt.Fprintf(os.Stderr, "Error: Received empty response from Ollama API\n")
		return fmt.Errorf("empty response from Ollama API")
	}

	fmt.Fprintf(os.Stderr, "Response received from Ollama (%d bytes)\n", len(respBody))

	var response OllamaResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshaling Ollama response: %v\n", err)
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(respBody))
		return err
	}

	if response.Message.Content == "" {
		fmt.Fprintf(os.Stderr, "Warning: Empty content in Ollama response\n")
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(respBody))
	}

	// Replace \n with \r\n in the response
	fmt.Print(strings.ReplaceAll(response.Message.Content, "\n", "\r\n"))

	printScissors(config)
	return nil
}

func callGemini(config *Config, input string) error {
	request := GeminiRequest{
		Contents: []GeminiContent{{
			Parts: []GeminiPart{{Text: input}},
		}},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", config.GeminiKey)

	printScissors(config)

	respBody, err := makeRequest("POST", url, headers, jsonData, config)
	if err != nil {
		return err
	}

	var response GeminiResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		// Replace \n with \r\n in the response
		fmt.Print(strings.ReplaceAll(response.Candidates[0].Content.Parts[0].Text, "\n", "\r\n"))
	}

	printScissors(config)
	return nil
}

func callDeepSeek(config *Config, input string) error {
	request := DeepSeekRequest{
		Model:    "deepseek-chat",
		Stream:   "false",
		Messages: []Message{{Role: "user", Content: input}},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Authorization": "Bearer " + config.DeepSeekKey,
		"Content-Type":  "application/json",
	}

	printScissors(config)

	respBody, err := makeRequest("POST", "https://api.deepseek.com/chat/completions", headers, jsonData, config)
	if err != nil {
		return err
	}

	var response DeepSeekResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Choices) > 0 {
		// Replace \n with \r\n in the response
		fmt.Print(strings.ReplaceAll(response.Choices[0].Message.Content, "\n", "\r\n"))
	}

	printScissors(config)
	return nil
}

func callMistral(config *Config, input string) error {
	request := MistralRequest{
		Model:     config.MistralModel,
		MaxTokens: 5128,
		Messages:  []Message{{Role: "user", Content: input}},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Authorization": "Bearer " + config.MistralKey,
		"Content-Type":  "application/json",
	}

	printScissors(config)

	respBody, err := makeRequest("POST", "https://api.mistral.ai/v1/chat/completions", headers, jsonData, config)
	if err != nil {
		return err
	}

	var response MistralResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Choices) > 0 {
		// Replace \n with \r\n in the response
		fmt.Print(strings.ReplaceAll(response.Choices[0].Message.Content, "\n", "\r\n"))
	}

	printScissors(config)
	return nil
}

func callBedrock(config *Config, input string) error {
	request := BedrockRequest{
		ModelId: config.BedrockModel,
		InferenceParams: BedrockInferenceParams{
			MaxTokens:   5128,
			Temperature: 0.7,
			TopP:        0.9,
		},
		Input: BedrockInput{
			Messages: []Message{{Role: "user", Content: input}},
		},
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	// Bedrock requires AWS signature auth, so we'll use AWS endpoint format
	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke",
		config.BedrockRegion, config.BedrockModel)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"X-Amz-Access-Token": config.BedrockKey,
	}

	printScissors(config)

	respBody, err := makeRequest("POST", url, headers, jsonData, config)
	if err != nil {
		return err
	}

	var response BedrockResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	// Replace \n with \r\n in the response
	fmt.Print(strings.ReplaceAll(response.Output.Message.Content, "\n", "\r\n"))

	printScissors(config)
	return nil
}

func callOpenAPI(config *Config, input string) error {
	request := OpenAPIRequest{
		Prompt: input,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("http://%s:%s/completion", config.OpenAPIHost, config.OpenAPIPort)

	printScissors(config)

	respBody, err := makeRequest("POST", url, headers, jsonData, config)
	if err != nil {
		return err
	}

	var response OpenAPIResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	// Replace \n with \r\n in the response
	fmt.Print(strings.ReplaceAll(response.Content, "\n", "\r\n"))

	printScissors(config)
	return nil
}

func showEnvHelp() {
	fmt.Print(`
MAI_PROVIDER=[ollama | gemini | deepseek | claude | openai | mistral | bedrock]
MAI_MODEL=[modelname]
MAI_BASEURL=[custom API base URL (e.g., https://api.moonshot.ai/anthropic)]
MAI_USERAGENT=[custom user agent string for HTTP requests]

Local:

OLLAMA_HOST=localhost
OLLAMA_PORT=11434
OLLAMA_MODEL=gemma3:1b
OLLAMA_MODEL=mannix/jan-nano:latest

Other:

GEMINI_API_KEY=(or set in ~/.r2ai.gemini-key)
OPENAI_API_KEY=(or set in ~/.r2ai.openai-key)
CLAUDE_API_KEY=(or set in ~/.r2ai.anthropic-key)
DEEPSEEK_API_KEY=(or set in ~/.r2ai.deepseek-key)
MISTRAL_API_KEY=(or set in ~/.r2ai.mistral-key)

Model Selection:

OPENAI_MODEL=o4-mini
CLAUDE_MODEL=claude-3-5-sonnet-20241022
MISTRAL_MODEL=mistral-large-latest

Bedrock-Specific:

AWS_ACCESS_KEY_ID=(or set in ~/.r2ai.bedrock-key)
AWS_REGION=us-west-2
BEDROCK_MODEL=anthropic.claude-3-5-sonnet-v1
`)
}
func showHelp() {
	fmt.Print(`$ mai-repl [--] | [-h] | [prompt] < INPUT
--               stdin mode (see -r)
-1               don't stream response, print once at the end
-a <string>      set the user agent for HTTP requests
-b <url>         specify a custom base URL for API requests
-c <key=value>   set configuration option
-h               show this help message
-H               show environment variables help (same as -hh)
-i <path>        attach an image to send to the model
-m <model>       select the model for the given provider
-n               do not load ~/.mairc and disable REPL history
-p <provider>    select the provider to use
-t               enable tools processing
Files:
~/.mairc           : script to be loaded before the repl is shown
~/.mai/history.json: REPL command history file (JSON array)
~/.mai/chat         : storage for chat session files
./prompts          : directory containing custom prompts
`)
}

// setModelForProvider sets the appropriate model field in the config based on the provider
func setModelForProvider(config *Config, model string) {
	// Get the current provider in lowercase for easier comparison
	provider := strings.ToLower(config.PROVIDER)

	switch provider {
	case "ollama":
		config.OllamaModel = model
		fmt.Fprintf(os.Stderr, "Setting Ollama model to %s\n", model)
	case "openai":
		config.OpenAIModel = model
		fmt.Fprintf(os.Stderr, "Setting OpenAI model to %s\n", model)
	case "claude":
		config.ClaudeModel = model
		fmt.Fprintf(os.Stderr, "Setting Claude model to %s\n", model)
	case "gemini", "google":
		config.GeminiModel = model
		fmt.Fprintf(os.Stderr, "Setting Gemini model to %s\n", model)
	case "mistral":
		config.MistralModel = model
		fmt.Fprintf(os.Stderr, "Setting Mistral model to %s\n", model)
	case "deepseek":
		config.DeepSeekModel = model
		fmt.Fprintf(os.Stderr, "Setting DeepSeek model to %s\n", model)
	case "bedrock", "aws":
		config.BedrockModel = model
		fmt.Fprintf(os.Stderr, "Setting Bedrock model to %s\n", model)
	default:
		fmt.Fprintf(os.Stderr, "Warning: Unknown provider '%s', cannot set model\n", provider)
	}
}

func main() {
	args := os.Args[1:]

	if len(args) > 0 && args[0] == "-h" {
		showHelp()
		return
	}
	if len(args) > 0 && (args[0] == "-hh" || args[0] == "-H") {
		showEnvHelp()
		return
	}

	config := loadConfig()

	// For backwards compatibility - check if API env var is set
	if apiVal := os.Getenv("API"); apiVal != "" && os.Getenv("MAI_PROVIDER") == "" {
		config.PROVIDER = apiVal
	}
	if defaultModel := os.Getenv("MAI_MODEL"); defaultModel != "" {
		setModelForProvider(config, defaultModel)
	}
	replMode := false

	// Process command line flags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n":
			// Skip loading ~/.mairc and disable REPL history
			config.options.Set("history", "false")
			config.SkipRcFile = true
			args = append(args[:i], args[i+1:]...)
			i--
			/*
				case "-s":
					config.ShowScissors = true
					args = append(args[:i], args[i+1:]...)
					i--
			*/
		case "-t":
			// Set usetools to true
			config.options.Set("usetools", "true")
			args = append(args[:i], args[i+1:]...)
			i--
		case "-1":
			config.NoStream = true
			args = append(args[:i], args[i+1:]...)
			i--
		case "-i":
			if i+1 < len(args) {
				config.ImagePath = args[i+1]
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -i requires an image path argument\n")
				os.Exit(1)
			}
		case "-p":
			if i+1 < len(args) {
				config.PROVIDER = args[i+1]
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -p requires a provider argument\n")
				os.Exit(1)
			}
		case "-r":
			replMode = true
		case "-b":
			if i+1 < len(args) {
				config.BaseURL = args[i+1]
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -b requires a base URL argument\n")
				os.Exit(1)
			}
		case "-m":
			if i+1 < len(args) {
				setModelForProvider(config, args[i+1])
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -m requires a model argument\n")
				os.Exit(1)
			}
		case "-a":
			if i+1 < len(args) {
				config.UserAgent = args[i+1]
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -a requires a user agent string\n")
				os.Exit(1)
			}
		case "-c":
			if i+1 < len(args) {
				configArg := args[i+1]
				// Parse config option in format key=value
				parts := strings.SplitN(configArg, "=", 2)
				if len(parts) != 2 {
					fmt.Fprintf(os.Stderr, "Error: -c requires format 'key=value'\n")
					os.Exit(1)
				}
				key, value := parts[0], parts[1]
				// Set the option in config
				config.options.Set(key, value)
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -c requires a config key=value pair\n")
				os.Exit(1)
			}
		}
	}

	// Check for REPL mode: interactive terminal or explicit -r flag
	stdinIsTerminal := term.IsTerminal(int(os.Stdin.Fd()))
	if replMode || stdinIsTerminal {
		// Not stdin mode, will load .mairc
		config.IsStdinMode = false

		repl, err := NewREPL(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing REPL: %v\n", err)
			os.Exit(1)
		}

		// TODO: use MAI_COLORS ?
		//	repl.config.options.Set("markdown", "false")
		if err := repl.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "REPL error: %v\n", err)
			os.Exit(1)
		}
	} else {
		config.IsStdinMode = true
		runStdinMode(config, args)
	}
}
