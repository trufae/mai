package llm

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadAPIKeysFromFile loads API keys from ~/.config/mai/apikeys.txt
// Format: each line is provider=key, comments start with # or empty lines
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
		// Try = first (new format), then : (old format) for backward compatibility
		var separatorIndex int
		var separator string
		if equalIndex := strings.Index(line, "="); equalIndex != -1 {
			separatorIndex = equalIndex
			separator = "="
		} else if colonIndex := strings.Index(line, ":"); colonIndex != -1 {
			separatorIndex = colonIndex
			separator = ":"
		} else {
			continue // no valid separator found
		}

		provider := strings.TrimSpace(line[:separatorIndex])
		key := strings.TrimSpace(line[separatorIndex+len(separator):])
		if provider != "" && key != "" {
			keys[strings.ToLower(provider)] = key
		}
	}

	return keys
}

// GetAPIKey resolves an API key by checking:
// 1. Environment variable
// 2. ~/.config/mai/apikeys.txt
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
	case "openrouter":
		return "OPENROUTER_API_KEY"
	default:
		return strings.ToUpper(provider) + "_API_KEY"
	}
}
