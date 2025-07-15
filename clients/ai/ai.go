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
)

type Config struct {
	OpenAPIHost string
	OpenAPIPort string
	OllamaHost  string
	OllamaPort  string
	OllamaModel string
	GeminiKey   string
	GeminiModel string
	OpenAIKey   string
	OpenAIModel string
	ClaudeKey   string
	ClaudeModel string
	DeepSeekKey string
	DeepSeekModel string
	ShowScissors bool
	API         string
}

type ClaudeRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
}

type OpenAIRequest struct {
	Model              string    `json:"model"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	Messages           []Message `json:"messages"`
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

type OpenAPIResponse struct {
	Content string `json:"content"`
}

func loadConfig() *Config {
	config := &Config{
		OpenAPIHost: getEnvOrDefault("OPENAPI_HOST", "localhost"),
		OpenAPIPort: getEnvOrDefault("OPENAPI_PORT", "8080"),
		OllamaHost:  getEnvOrDefault("OLLAMA_HOST", "localhost"),
		OllamaPort:  getEnvOrDefault("OLLAMA_PORT", "11434"),
		OllamaModel: getEnvOrDefault("OLLAMA_MODEL", "llama3.2:1b"),
		GeminiModel: "gemini-1.5-flash",
		OpenAIModel: "gpt-4o",
		ClaudeModel: "claude-3-5-sonnet-20241022",
		DeepSeekModel: "claude-3-5-sonnet-20241022",
		ShowScissors: true,
		API:         getEnvOrDefault("SHAI_API", "claude"),
	}

	// Load API keys from files
	if key := readKeyFile("~/.r2ai.gemini-key"); key != "" {
		config.GeminiKey = key
	}
	if key := readKeyFile("~/.r2ai.openai-key"); key != "" {
		config.OpenAIKey = key
	}
	if key := readKeyFile("~/.r2ai.anthropic-key"); key != "" {
		config.ClaudeKey = key
	}
	if key := readKeyFile("~/.r2ai.deepseek-key"); key != "" {
		config.DeepSeekKey = key
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
		fmt.Println("------------8<------------")
	}
}

func makeRequest(method, url string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
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
		"Content-Type":       "application/json",
		"anthropic-version":  "2023-06-01",
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
		Model:              config.OpenAIModel,
		MaxCompletionTokens: 5128,
		Messages:           []Message{{Role: "user", Content: input}},
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
		return err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	url := fmt.Sprintf("http://%s:%s/api/chat", config.OllamaHost, config.OllamaPort)
	
	printScissors(config)
	
	respBody, err := makeRequest("POST", url, headers, jsonData)
	if err != nil {
		return err
	}

	var response OllamaResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
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
SHAI_API = ollama | gemini | claude | openai
OLLAMA_MODEL=hf.co/mradermacher/salamandra-7b-instruct-aina-hack-GGUF:salamandra-7b-instruct-aina-hack.Q4_K_M.gguf
OLLAMA_HOST=localhost
OLLAMA_PORT=11434
GEMINI_KEY=~/.r2ai-gemini.key
OPENAI_KEY=~/.r2ai-openai.key
CLAUDE_KEY=~/.r2ai-anthropic.key
DEEPSEEK_KEY=~/.r2ai-deepseek.key
CLAUDE_MODEL=claude-3-5-sonnet-20241022
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
	default:
		err = callClaude(config, input)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
