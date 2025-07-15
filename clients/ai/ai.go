package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	ShowScissors  bool
	API           string
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
	Stream   bool      `json:"stream"`
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
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
	Role    string `json:"role"`
	Content string `json:"content"`
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

type MistralResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
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
		OllamaModel:   getEnvOrDefault("OLLAMA_MODEL", "llama3.2:1b"),
		GeminiModel:   "gemini-1.5-flash",
		OpenAIModel:   "gpt-4o",
		ClaudeModel:   "claude-3-5-sonnet-20241022",
		DeepSeekModel: "claude-3-5-sonnet-20241022",
		MistralModel:  "mistral-large-latest",
		ShowScissors:  true,
		API:           getEnvOrDefault("AI", "claude"),
		GeminiKey:     os.Getenv("GEMINI_API_KEY"),
		OpenAIKey:     os.Getenv("OPENAI_API_KEY"),
		ClaudeKey:     os.Getenv("CLAUDE_API_KEY"),
		DeepSeekKey:   os.Getenv("DEEPSEEK_API_KEY"),
		MistralKey:    os.Getenv("MISTRAL_API_KEY"),
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

	input.WriteString("<INPUT>\n")

	// Read from stdin
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input.WriteString(scanner.Text())
		input.WriteString("\n")
	}

	input.WriteString("</INPUT>")

	return input.String()
}

func printScissors(config *Config) {
	if config.ShowScissors {
		fmt.Println("\n------------8<------------")
	}
}

func makeRequest(method, url string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating HTTP request: %v\n", err)
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	fmt.Fprintf(os.Stderr, "Sending %s request to %s\n", method, url)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP request failed: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	fmt.Fprintf(os.Stderr, "Response status code: %d\n", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Non-200 status code: %d %s\n", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response body: %v\n", err)
		return nil, err
	}

	return respBody, nil
}

func callClaude(config *Config, input string) error {
	request := ClaudeRequest{
		Model:     config.ClaudeModel,
		MaxTokens: 5128,
		Messages:  []Message{{Role: "user", Content: input}},
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

	respBody, err := makeRequest("POST", "https://api.anthropic.com/v1/messages", headers, jsonData)
	if err != nil {
		return err
	}

	var response ClaudeResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Content) > 0 {
		fmt.Print(response.Content[0].Text)
	}

	printScissors(config)
	return nil
}

func callOpenAI(config *Config, input string) error {
	request := OpenAIRequest{
		Model:               config.OpenAIModel,
		MaxCompletionTokens: 5128,
		Messages:            []Message{{Role: "user", Content: input}},
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

	respBody, err := makeRequest("POST", "https://api.openai.com/v1/chat/completions", headers, jsonData)
	if err != nil {
		return err
	}

	var response OpenAIResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Choices) > 0 {
		fmt.Print(response.Choices[0].Message.Content)
	}

	printScissors(config)
	return nil
}

func callOllama(config *Config, input string) error {
	request := OllamaRequest{
		Stream:   false,
		Model:    config.OllamaModel,
		Messages: []Message{{Role: "user", Content: input}},
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

	respBody, err := makeRequest("POST", url, headers, jsonData)
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

	fmt.Print(response.Message.Content)

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

	respBody, err := makeRequest("POST", url, headers, jsonData)
	if err != nil {
		return err
	}

	var response GeminiResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		fmt.Print(response.Candidates[0].Content.Parts[0].Text)
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

	respBody, err := makeRequest("POST", "https://api.deepseek.com/chat/completions", headers, jsonData)
	if err != nil {
		return err
	}

	var response DeepSeekResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Choices) > 0 {
		fmt.Print(response.Choices[0].Message.Content)
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

	respBody, err := makeRequest("POST", "https://api.mistral.ai/v1/chat/completions", headers, jsonData)
	if err != nil {
		return err
	}

	var response MistralResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if len(response.Choices) > 0 {
		fmt.Print(response.Choices[0].Message.Content)
	}

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

	respBody, err := makeRequest("POST", url, headers, jsonData)
	if err != nil {
		return err
	}

	var response OpenAPIResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	fmt.Print(response.Content)

	printScissors(config)
	return nil
}

func showHelp() {
	fmt.Print(`$ ai [--] | [-h] | [prompt] < INPUT
-h = show this help message
-- = don't display the ---8<--- lines in the output
AI= ollama | gemini | deepseek | claude | openai | mistral
OLLAMA_MODEL=mannix/jan-nano:latest
OLLAMA_HOST=localhost
OLLAMA_PORT=11434
GEMINI_API_KEY=(or set in ~/.r2ai.gemini-key)
OPENAI_API_KEY=(or set in ~/.r2ai.openai-key)
CLAUDE_API_KEY=(or set in ~/.r2ai.anthropic-key)
DEEPSEEK_API_KEY=(or set in ~/.r2ai.deepseek-key)
MISTRAL_API_KEY=(or set in ~/.r2ai.mistral-key)
# Model Selection
OLLAMA_MODEL=gemma3:1b
CLAUDE_MODEL=claude-3-5-sonnet-20241022
MISTRAL_MODEL=mistral-large-latest
`)
}

func main() {
	args := os.Args[1:]

	if len(args) > 0 && args[0] == "-h" {
		showHelp()
		return
	}

	config := loadConfig()

	if len(args) > 0 && args[0] == "--" {
		config.ShowScissors = false
		args = args[1:]
	}

	input := readInput(args)

	var err error
	switch strings.ToLower(config.API) {
	case "gemini", "google":
		err = callGemini(config, input)
	case "deepseek":
		err = callDeepSeek(config, input)
	case "openapi":
		err = callOpenAPI(config, input)
	case "claude":
		err = callClaude(config, input)
	case "ollama":
		err = callOllama(config, input)
	case "openai":
		err = callOpenAI(config, input)
	case "mistral":
		err = callMistral(config, input)
	default:
		err = callClaude(config, input)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling %s API: %v\n", config.API, err)
		if config.API == "ollama" {
			fmt.Fprintf(os.Stderr, "Ollama troubleshooting tips:\n")
			fmt.Fprintf(os.Stderr, "1. Check if Ollama is running: ps aux | grep ollama\n")
			fmt.Fprintf(os.Stderr, "2. Verify Ollama server is accessible at %s:%s\n", config.OllamaHost, config.OllamaPort)
			fmt.Fprintf(os.Stderr, "3. Confirm model '%s' is available: ollama list\n", config.OllamaModel)
			fmt.Fprintf(os.Stderr, "4. Try pulling the model: ollama pull %s\n", config.OllamaModel)
		}
		os.Exit(1)
	}
}
