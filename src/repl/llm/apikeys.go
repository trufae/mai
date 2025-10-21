package llm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadAPIKeysFromFile loads API keys from ~/.config/mai/apikeys.txt
// Format: each line is provider:key, comments start with # or empty lines
func loadAPIKeysFromFile() map[string]string {
	keys := make(map[string]string)

	home, err := os.UserHomeDir()
	if err != nil {
		return keys
	}

	filePath := filepath.Join(home, ".config", "mai", "apikeys.txt")
	file, err := os.Open(filePath)
	if err != nil {
		return keys
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if colonIndex := strings.Index(line, ":"); colonIndex != -1 {
			provider := strings.TrimSpace(line[:colonIndex])
			key := strings.TrimSpace(line[colonIndex+1:])
			if provider != "" && key != "" {
				keys[strings.ToLower(provider)] = key
			}
		}
	}

	return keys
}

// GetAPIKey resolves an API key by checking:
// 1. Environment variable
// 2. ~/.config/mai/apikeys.txt
// 3. Deprecated ~/.r2ai.provider-key (with warning)
func GetAPIKey(provider string) string {
	// Check environment variable first
	envVar := getEnvVarForProvider(provider)
	if v := os.Getenv(envVar); v != "" {
		return strings.TrimSpace(v)
	}

	// Load keys from apikeys.txt
	keys := loadAPIKeysFromFile()
	if key, ok := keys[strings.ToLower(provider)]; ok {
		return key
	}

	// Fallback to deprecated file with warning
	oldFile := getOldKeyFile(provider)
	if oldFile != "" {
		if data, err := os.ReadFile(oldFile); err == nil {
			s := strings.TrimSpace(string(data))
			if s != "" {
				fmt.Fprintf(os.Stderr, "Warning: API key for %s loaded from deprecated %s.\n", provider, oldFile)
				fmt.Fprintf(os.Stderr, "Please migrate to ~/.config/mai/apikeys.txt format: add '%s:%s' to the file.\n", provider, s)
				return s
			}
		}
	}

	return ""
}

// getEnvVarForProvider returns the environment variable name for a provider
func getEnvVarForProvider(provider string) string {
	switch strings.ToLower(provider) {
	case "openai":
		return "OPENAI_API_KEY"
	case "claude", "anthropic":
		return "CLAUDE_API_KEY"
	case "gemini", "google":
		return "GEMINI_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	case "bedrock", "aws":
		return "AWS_ACCESS_KEY_ID"
	case "ollama":
		return "OLLAMA_API_KEY"
	default:
		return strings.ToUpper(provider) + "_API_KEY"
	}
}

// getOldKeyFile returns the old deprecated key file path for a provider
func getOldKeyFile(provider string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	var filename string
	switch strings.ToLower(provider) {
	case "openai":
		filename = ".r2ai.openai-key"
	case "claude", "anthropic":
		filename = ".r2ai.anthropic-key"
	case "gemini", "google":
		filename = ".r2ai.gemini-key"
	case "mistral":
		filename = ".r2ai.mistral-key"
	case "deepseek":
		filename = ".r2ai.deepseek-key"
	case "xai":
		filename = ".r2ai.xai-key"
	case "bedrock", "aws":
		filename = ".r2ai.bedrock-key"
	case "ollama":
		filename = ".r2ai.ollama-key"
	default:
		return ""
	}

	return filepath.Join(home, filename)
}
