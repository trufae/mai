package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OptionType represents the type of a configuration option
type OptionType string

const (
	DefaultModel    string = "gemma3:1b"
	DefaultProvider string = "ollama"
)
const (
	StringOption  OptionType = "string"
	BooleanOption OptionType = "boolean"
	NumberOption  OptionType = "number"
)

// OptionInfo stores metadata about a configuration option
type OptionInfo struct {
	Type        OptionType
	Description string
	Default     string // Default value as a string
}

// OptionChangeCallback is a function that gets called when an option value changes
type OptionChangeCallback func(string)

// ConfigOptions stores the key-value pairs for configuration
type ConfigOptions struct {
	values      map[string]string
	optionInfos map[string]OptionInfo
	initialized bool
	listeners   map[string][]OptionChangeCallback
}

// NewConfigOptions creates and initializes a new ConfigOptions
func NewConfigOptions() *ConfigOptions {
	co := &ConfigOptions{
		values:      make(map[string]string),
		optionInfos: make(map[string]OptionInfo),
		listeners:   make(map[string][]OptionChangeCallback),
	}

	// Define built-in options

	// AI provider options
	co.RegisterOption("ai.baseurl", StringOption, "Custom base URL for API requests", "")
	co.RegisterOption("ai.deterministic", BooleanOption, "Force deterministic output from LLMs", "false")
	co.RegisterOption("ai.provider", StringOption, "AI provider to use", DefaultProvider)
	co.RegisterOption("ai.model", StringOption, "AI model to use", DefaultModel)
	co.RegisterOption("ai.model.embed", StringOption, "AI model to use for embedding tasks", "all-minilm@ollama")
	co.RegisterOption("ai.model.compact", StringOption, "AI model to use for /compact command", "")
	co.RegisterOption("ai.model.tool", StringOption, "AI model to use for tool calling", "")

	// Chat configuration
	co.RegisterOption("chat.aitopic", BooleanOption, "Enable automatic AI-generated session topics", "false")
	co.RegisterOption("chat.autocompact", NumberOption, "Auto-compact conversation when history exceeds threshold (0=off)", "0")
	co.RegisterOption("chat.followup", BooleanOption, "Automatically run #followup after assistant replies", "false")
	co.RegisterOption("chat.format", StringOption, "Chat formatting: tokens, labeled, or plain", "plain")
	co.RegisterOption("chat.log", BooleanOption, "Enable conversation logging", "true")
	// Memory option: load consolidated memory from ~/.config/mai/memory.txt into conversation context
	co.RegisterOption("chat.memory", BooleanOption, "Load memory.txt from ~/.config/mai and include in context", "false")
	co.RegisterOption("chat.replies", BooleanOption, "Include chat replies when building a single prompt", "false")
	co.RegisterOption("chat.save", StringOption, "Session save behavior on exit: always, never, or prompt", "prompt")
	co.RegisterOption("chat.system", BooleanOption, "Include chat system messages when building a single prompt", "true")
	// Number of most recent messages to include when sending to the LLM (0 = all)
	co.RegisterOption("chat.tail", NumberOption, "Number of most recent messages to include when sending to the LLM (0=all)", "0")
	co.RegisterOption("chat.tts", BooleanOption, "Enable text-to-speech for AI responses", "false")
	co.RegisterOption("chat.ttsvoice", StringOption, "Voice to use for text-to-speech", "Mónica")

	// Directory configuration
	co.RegisterOption("dir.prompt", StringOption, "Directory to read prompts from", "")
	co.RegisterOption("dir.promptfile", StringOption, "System prompt file path", "")
	co.RegisterOption("dir.templates", StringOption, "Directory to read templates from", "")

	// HTTP server options
	co.RegisterOption("http.listen", StringOption, "Listen address for the web server (host:port)", "0.0.0.0:9000")
	co.RegisterOption("http.repl", BooleanOption, "Route / commands from web UI through REPL command system", "true")
	co.RegisterOption("http.useragent", StringOption, "Custom user agent for HTTP requests", "mai-repl/1.0")
	co.RegisterOption("http.wwwroot", StringOption, "Directory to serve static web files from", "")

	// LLM interaction options
	// co.RegisterOption("llm.agentfile", StringOption, "Filename to load agent instructions from current or parent directories (empty to disable)", "AGENTS.md")
	co.RegisterOption("llm.agentfile", StringOption, "Filename to load agent instructions from current or parent directories (empty to disable)", "")
	co.RegisterOption("llm.maxtokens", NumberOption, "Maximum tokens for AI response", "5128")
	co.RegisterOption("llm.rawmode", BooleanOption, "Send messages in raw", "false")
	co.RegisterOption("llm.schema", StringOption, "Inline JSON schema to constrain model output", "")
	co.RegisterOption("llm.schemafile", StringOption, "Path to JSON schema file for formatted output", "")
	co.RegisterOption("llm.stream", BooleanOption, "Enable streaming mode", "true")
	co.RegisterOption("llm.systemprompt", StringOption, "System prompt text (overrides systempromptfile)", "")
	co.RegisterOption("llm.systempromptfile", StringOption, "Path to system prompt file (default: ~/.config/mai/systemprompt.md)", "")
	co.RegisterOption("llm.temperature", NumberOption, "Temperature for AI response (0.0-1.0)", "0.7")
	co.RegisterOption("llm.think", BooleanOption, "Enable AI reasoning", "false")
	co.RegisterOption("ui.think", BooleanOption, "Show <think> internal reasoning in output", "true")

	// REPL behavior options
	co.RegisterOption("repl.debug", BooleanOption, "Show internal processing logs", "false")
	co.RegisterOption("ui.demo", BooleanOption, "Enable demo mode with waiting animation", "false")
	co.RegisterOption("repl.history", BooleanOption, "Enable REPL history", "true")
	co.RegisterOption("repl.prompt", StringOption, "Main prompt string for input", ">>>")
	co.RegisterOption("repl.prompt2", StringOption, "Prompt string for heredoc/continuation lines", "...")
	co.RegisterOption("repl.skiprc", BooleanOption, "Skip loading rc file on start", "false")
	co.RegisterOption("repl.skillsdir", StringOption, "Custom directory path for Claude Skills (supports ~ expansion)", "")

	// Screen rendering options
	co.RegisterOption("ui.markdown", BooleanOption, "Enable markdown rendering with colors", "false")
	co.RegisterOption("ui.stats", BooleanOption, "Show time statistics (time to first token, tokens/sec, chars/sec) after LLM responses", "false")
	co.RegisterOption("ui.bgcolor", StringOption, "Background color for the input line (named colors or rgb:RGB)", "")
	co.RegisterOption("ui.fgcolor", StringOption, "Foreground color for the input line text (named colors or rgb:RGB)", "")
	co.RegisterOption("ui.bgline", StringOption, "Background color for the line before the prompt (named colors or rgb:RGB)", "")
	co.RegisterOption("ui.bold", BooleanOption, "Use bold text for the input line", "false")
	co.RegisterOption("ui.fgprompt", StringOption, "Foreground color for the prompt text (named colors or rgb:RGB)", "yellow")
	co.RegisterOption("ui.bgprompt", StringOption, "Background color for the prompt text (named colors or rgb:RGB)", "")

	// Tooling options
	co.RegisterOption("mcp.autoselectprompt", BooleanOption, "Enable MCP prompts selection to choose a plan template for newtools", "false")
	co.RegisterOption("mcp.use", BooleanOption, "Process user input using newtools functions (overrides tools.old)", "false")
	co.RegisterOption("mcp.native", BooleanOption, "Use native tool calling protocol instead of MCP react loop", "false")
	// Unified tool-calling controls
	co.RegisterOption("mcp.grammar", BooleanOption, "Use JSON schema/grammar for tool planning output", "true")
	co.RegisterOption("mcp.display", StringOption, "Tool loop display: verbose, plan, progress, reason, quiet", "verbose")
	co.RegisterOption("mcp.reason", StringOption, "Reasoning level: low, medium, high", "low")
	co.RegisterOption("mcp.timeout", NumberOption, "Timeout in seconds for tool execution", "60")

	// User details options
	co.RegisterOption("user.details", BooleanOption, "Include user details (CWD, username, OS, language, time) in conversation context", "false")
	co.RegisterOption("user.lang", StringOption, "Language preference for user details (defaults to LANG environment variable)", "")

	// Vector database integration
	co.RegisterOption("vdb.use", BooleanOption, "Use mai-vdb tool to get context from vector database", "false")
	co.RegisterOption("vdb.datadir", StringOption, "Directory to search for vector database sources", "")
	co.RegisterOption("vdb.limit", NumberOption, "Limit of entries to be used when calling mai-vdb", "5")

	// MCP integration
	co.RegisterOption("mcp.config", StringOption, "Path to MCP configuration file", "")
	co.RegisterOption("mcp.args", StringOption, "Command-line arguments to pass to mai-wmcp", "")
	co.RegisterOption("mcp.daemon", BooleanOption, "Enable starting the mai-wmcp server", "true")
	co.RegisterOption("mcp.debug", BooleanOption, "Enable debug output for MCP communication between agent, model, and servers", "false")
	co.RegisterOption("mcp.prompt", StringOption, "Custom text to be included in the instructions prompt for react loops", "")
	co.RegisterOption("mcp.toolformat", StringOption, "Tool list format: xml, markdown, simple, quiet, json (empty=auto)", "")
	co.RegisterOption("mcp.baseurl", StringOption, "Base URL for MCP server connection", "http://localhost:8989")

	co.initialized = true

	return co
}

// RegisterOption registers a new configuration option with type information
func (c *ConfigOptions) RegisterOption(name string, optType OptionType, description, defaultValue string) {
	c.optionInfos[name] = OptionInfo{
		Type:        optType,
		Description: description,
		Default:     defaultValue,
	}

	// If we're already initialized and this is a new option, set the default value
	if c.initialized && c.Get(name) == "" && defaultValue != "" {
		c.Set(name, defaultValue)
	}
}

// GetOptionInfo returns type information for an option
func (c *ConfigOptions) GetOptionInfo(key string) (OptionInfo, bool) {
	info, exists := c.optionInfos[key]
	return info, exists
}

// Get retrieves a configuration value as string
func (c *ConfigOptions) Get(key string) string {
	if value, exists := c.values[key]; exists && value != "" {
		return value
	}

	// Return default if defined
	if info, exists := c.optionInfos[key]; exists {
		return info.Default
	}

	return ""
}

// GetBool retrieves a configuration value as boolean
func (c *ConfigOptions) GetBool(key string) bool {
	value := strings.ToLower(c.Get(key))
	return value == "true" || value == "yes" || value == "1" || value == "on"
}

// GetNumber retrieves a configuration value as float64
func (c *ConfigOptions) GetNumber(key string) (float64, error) {
	value := c.Get(key)
	if value == "" {
		return 0, nil
	}
	return strconv.ParseFloat(value, 64)
}

// Set stores a configuration value
// Returns an error if the value doesn't match the expected type
func (c *ConfigOptions) Set(key, value string) error {
	oldValue := c.Get(key)

	// Check if the option is registered and validate based on type
	if info, exists := c.optionInfos[key]; exists {
		switch info.Type {
		case BooleanOption:
			normalizedValue := strings.ToLower(value)
			// Accept '1' and '0' as aliases for true/false
			if normalizedValue == "1" {
				normalizedValue = "true"
			} else if normalizedValue == "0" {
				normalizedValue = "false"
			}
			if normalizedValue != "true" && normalizedValue != "false" {
				return fmt.Errorf("invalid boolean value: %s (must be 'true', 'false', '1' or '0')", value)
			}
			c.values[key] = normalizedValue
		case NumberOption:
			_, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return fmt.Errorf("invalid number value: %s", value)
			}
			c.values[key] = value
		default: // StringOption or unknown type
			c.values[key] = value
		}
	} else {
		// For unregistered options, just store as string
		c.values[key] = value
	}

	// If the value has changed, notify listeners
	if oldValue != value {
		c.notifyListeners(key, value)
	}

	return nil
}

// Unset removes a configuration value
func (c *ConfigOptions) Unset(key string) {
	oldValue := c.Get(key)
	delete(c.values, key)

	// Notify listeners with the default value
	defaultValue := ""
	if info, exists := c.optionInfos[key]; exists {
		defaultValue = info.Default
	}

	if oldValue != defaultValue {
		c.notifyListeners(key, defaultValue)
	}
}

// GetKeys returns a list of all configuration keys that have values set
func (c *ConfigOptions) GetKeys() []string {
	keys := make([]string, 0, len(c.values))
	for key := range c.values {
		keys = append(keys, key)
	}
	return keys
}

// GetAvailableOptions returns a list of all available configuration options
// GetAvailableOptions returns a sorted list of all available configuration options
func (c *ConfigOptions) GetAvailableOptions() []string {
	if c == nil {
		return nil
	}

	opts := make([]string, 0, len(c.optionInfos))
	for key := range c.optionInfos {
		opts = append(opts, key)
	}
	sort.Strings(opts)
	return opts
}

// RegisterOptionListener adds a listener function that will be called when an option's value changes
func (c *ConfigOptions) RegisterOptionListener(key string, callback OptionChangeCallback) {
	// Create listeners array if it doesn't exist
	if c.listeners[key] == nil {
		c.listeners[key] = make([]OptionChangeCallback, 0)
	}

	// Add the new listener
	c.listeners[key] = append(c.listeners[key], callback)
}

// notifyListeners calls all registered listeners for a specific key
func (c *ConfigOptions) notifyListeners(key, value string) {
	if listeners, ok := c.listeners[key]; ok {
		for _, listener := range listeners {
			listener(value)
		}
	}
}

// GetOptionDescription returns a description for the given option
// GetOptionDescription returns a description for the given option
func (c *ConfigOptions) GetOptionDescription(option string) string {
	if c == nil {
		return ""
	}

	if info, exists := c.GetOptionInfo(option); exists {
		return info.Description
	}
	return "No description available"
}

// GetOptionType returns the type of a given option
// GetOptionType returns the type of a given option
func (c *ConfigOptions) GetOptionType(option string) OptionType {
	if c == nil {
		return StringOption
	}

	if info, exists := c.GetOptionInfo(option); exists {
		return info.Type
	}
	return StringOption
}

// resolvePromptPath resolves the path to a prompt file
// It checks in the promptdir configuration if set, otherwise it tries common locations
func (r *REPL) resolvePromptPath(promptName string) (string, error) {
	// If the prompt path is absolute or contains path separators, use it directly
	if filepath.IsAbs(promptName) || strings.ContainsAny(promptName, "/\\") {
		return promptName, nil
	}

	// First try the promptdir configuration if set
	if promptDir := r.configOptions.Get("dir.prompt"); promptDir != "" {
		promptPath := filepath.Join(promptDir, promptName)
		// Try with and without .md extension
		if _, err := os.Stat(promptPath); err == nil {
			return promptPath, nil
		}
		if _, err := os.Stat(promptPath + ".md"); err == nil {
			return promptPath + ".md", nil
		}
	}

	// Next, try common locations for prompts
	commonLocations := []string{
		"./share/mai/prompts",  // Current directory's prompts folder
		"../share/mai/prompts", // Parent directory's prompts folder
	}
	// Add user config prompts directory
	if home, err := os.UserHomeDir(); err == nil {
		userPrompts := filepath.Join(home, ".config", "mai", "prompts")
		commonLocations = append(commonLocations, userPrompts)
	}

	// Try each location
	for _, location := range commonLocations {
		promptPath := filepath.Join(location, promptName)
		// Try with and without .md extension
		if _, err := os.Stat(promptPath); err == nil {
			return promptPath, nil
		}
		if _, err := os.Stat(promptPath + ".md"); err == nil {
			return promptPath + ".md", nil
		}
	}

	return "", fmt.Errorf("prompt not found: %s", promptName)
}

// parseRGBColor parses rgb:RGB format (3 hex chars) and returns ANSI code parameters
func parseRGBColor(color string) (string, bool) {
	if !strings.HasPrefix(color, "rgb:") || len(color) != 7 {
		return "", false
	}
	hexStr := color[4:]
	if len(hexStr) != 3 {
		return "", false
	}
	var r, g, b int
	for i, c := range hexStr {
		var val int
		switch {
		case c >= '0' && c <= '9':
			val = int(c - '0')
		case c >= 'a' && c <= 'f':
			val = 10 + int(c-'a')
		case c >= 'A' && c <= 'F':
			val = 10 + int(c-'A')
		default:
			return "", false
		}
		val *= 17
		switch i {
		case 0:
			r = val
		case 1:
			g = val
		case 2:
			b = val
		}
	}
	return fmt.Sprintf("%d;%d;%d", r, g, b), true
}

// handleInvalidConfigKey generates an error message for invalid configuration keys with suggestions
func (r *REPL) handleInvalidConfigKey(key string) string {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Error: configuration key '%s' does not exist\r\n", key))

	// Suggest similar keys
	var suggestions []string
	for _, k := range r.configOptions.GetAvailableOptions() {
		if strings.Contains(k, key) || strings.Contains(key, k) {
			suggestions = append(suggestions, k)
		}
	}
	if len(suggestions) == 0 {
		// Find keys with common prefix
		for _, k := range r.configOptions.GetAvailableOptions() {
			if strings.HasPrefix(k, strings.Split(key, ".")[0]+".") {
				suggestions = append(suggestions, k)
			}
		}
	}
	if len(suggestions) > 0 {
		output.WriteString("Did you mean one of these?\r\n")
		for _, sug := range suggestions {
			output.WriteString(fmt.Sprintf("  %s\r\n", sug))
		}
	} else {
		output.WriteString("Use '/set' or '/get' without arguments to list all available options.\r\n")
	}
	return output.String()
}

// handleSetCommand handles the /set command with auto-completion and type validation
func (r *REPL) handleSetCommand(args []string) (string, error) {
	// Join all args after the command into a single input string. This
	// ensures values containing spaces or uses like "key= value" are
	// handled consistently instead of relying on tokenization.
	var input string
	if len(args) >= 2 {
		input = strings.TrimSpace(strings.Join(args[1:], " "))
	} else {
		input = ""
	}

	if input == "" {
		var output strings.Builder
		output.WriteString("Usage: /set <option> [value] or /set <option>=<value>\r\n")
		output.WriteString("Available options:\r\n")
		for _, option := range r.configOptions.GetAvailableOptions() {
			optType := r.configOptions.GetOptionType(option)
			output.WriteString(fmt.Sprintf("  %-20s %-15s %s\r\n", option, "("+optType+")", r.configOptions.GetOptionDescription(option)))
		}
		return output.String(), nil
	}

	var key, value string
	// Prefer explicit key=value syntax if present in the joined input
	if idx := strings.Index(input, "="); idx != -1 {
		key = strings.TrimSpace(input[:idx])
		value = strings.TrimSpace(input[idx+1:])
		// Treat trailing '=' with no value as an unset request
		if value == "" {
			return r.handleUnsetCommand([]string{"/unset", key})
		}
	} else {
		// No '=' in input: first token is the key, remainder is the value
		parts := strings.Fields(input)
		key = strings.TrimSpace(parts[0])
		if len(parts) >= 2 {
			value = strings.TrimSpace(strings.Join(parts[1:], " "))
		} else {
			value = ""
		}
	}

	// Check if the option exists (do this early to avoid showing "not set" for invalid keys)
	if _, exists := r.configOptions.GetOptionInfo(key); !exists {
		// Special case: if key ends with '.', list all keys that start with that prefix
		if strings.HasSuffix(key, ".") {
			var output strings.Builder
			prefix := key
			output.WriteString(fmt.Sprintf("Configuration options starting with '%s':\r\n", prefix))
			found := false
			for _, opt := range r.configOptions.GetAvailableOptions() {
				if strings.HasPrefix(opt, prefix) {
					val := r.configOptions.Get(opt)
					var status string
					if val == "" {
						status = "not set"
						if info, exists := r.configOptions.GetOptionInfo(opt); exists && info.Default != "" {
							status = fmt.Sprintf("default: %s", info.Default)
						}
					} else {
						status = val
					}
					optType := r.configOptions.GetOptionType(opt)
					output.WriteString(fmt.Sprintf("  %-20s = %-15s (type: %s)\r\n", opt, status, optType))
					found = true
				}
			}
			if !found {
				output.WriteString(fmt.Sprintf("No configuration options found starting with '%s'\r\n", prefix))
			}
			return output.String(), nil
		}

		// Invalid key
		return r.handleInvalidConfigKey(key), nil
	}

	// If key ends with '.', list all keys that start with that prefix (for valid keys)
	if strings.HasSuffix(key, ".") {
		var output strings.Builder
		prefix := key
		output.WriteString(fmt.Sprintf("Configuration options starting with '%s':\r\n", prefix))
		found := false
		for _, opt := range r.configOptions.GetAvailableOptions() {
			if strings.HasPrefix(opt, prefix) {
				val := r.configOptions.Get(opt)
				var status string
				if val == "" {
					status = "not set"
					if info, exists := r.configOptions.GetOptionInfo(opt); exists && info.Default != "" {
						status = fmt.Sprintf("default: %s", info.Default)
					}
				} else {
					status = val
				}
				optType := r.configOptions.GetOptionType(opt)
				output.WriteString(fmt.Sprintf("  %-20s = %-15s (type: %s)\r\n", opt, status, optType))
				found = true
			}
		}
		if !found {
			output.WriteString(fmt.Sprintf("No configuration options found starting with '%s'\r\n", prefix))
		}
		return output.String(), nil
	}

	if value == "" {
		// Display current value if no value provided
		val := r.configOptions.Get(key)
		optType := r.configOptions.GetOptionType(key)
		var status string
		if val == "" {
			status = "not set"
			if info, exists := r.configOptions.GetOptionInfo(key); exists && info.Default != "" {
				status = fmt.Sprintf("default: %s", info.Default)
			}
		} else {
			status = val
		}
		return fmt.Sprintf("%s = %s (type: %s)\r\n", key, status, optType), nil
	}

	// Handle special options
	switch key {
	case "ai.deterministic":
		r.configOptions.Set("ai.deterministic", value)
		return "", nil
	case "llm.rawmode":
		r.configOptions.Set("llm.rawmode", value)
		return "", nil
	case "dir.promptfile":
		return "", r.loadSystemPrompt(value)
	case "llm.systemprompt":
		r.configOptions.Set("llm.systemprompt", value)
		return "", nil
	case "ai.model":
		return "", r.setModel(value)
	case "ai.provider":
		provider := strings.ToLower(value)
		return "", r.setProvider(provider)
	case "chat.replies":
		r.configOptions.Set("chat.replies", value)
		return "", nil
	case "chat.system":
		r.configOptions.Set("chat.system", value)
		return "", nil
	case "chat.format":
		valLower := strings.ToLower(value)
		if valLower != "plain" && valLower != "labeled" && valLower != "tokens" {
			fmt.Printf("Warning: unknown chat.format '%s'\n", value)
		}
		r.configOptions.Set("chat.format", valLower)
		return "", nil
	case "mcp.reason":
		valLower := strings.ToLower(value)
		if valLower != "low" && valLower != "medium" && valLower != "high" {
			return fmt.Sprintf("Error: invalid value '%s' for mcp.reason. Must be one of: low, medium, high\r\n", value), nil
		}
		r.configOptions.Set("mcp.reason", valLower)
		return "", nil
	}

	// Set the option value with validation
	if err := r.configOptions.Set(key, value); err != nil {
		var output strings.Builder
		output.WriteString(fmt.Sprintf("Error: %v\r\n", err))

		// Show expected format for the type
		if optType := r.configOptions.GetOptionType(key); optType != "" {
			switch optType {
			case BooleanOption:
				output.WriteString("Boolean options accept: true, false\r\n")
			case NumberOption:
				output.WriteString("Number options accept numeric values\r\n")
			}
		}

		return output.String(), nil
	}

	// Handle special options that require updating REPL output/state
	var output strings.Builder
	switch key {
	case "llm.stream":
		streamStatus := "enabled"
		if !r.configOptions.GetBool("llm.stream") {
			streamStatus = "disabled"
		}
		output.WriteString(fmt.Sprintf("Streaming mode %s\r\n", streamStatus))
	case "chat.replies":
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", key, value))
	case "llm.think":
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", key, value))
	case "chat.log":
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", key, value))
	case "ui.bgcolor":
		if value != "" && !strings.HasPrefix(value, "rgb:") {
			validColors := []string{"black", "red", "green", "yellow", "blue", "dark-blue", "magenta", "cyan", "white", "grey", "bright-black", "bright-red", "bright-green", "bright-yellow", "bright-blue", "bright-magenta", "bright-cyan", "bright-white", "orange", "violet", "pink", "purple", "brown"}
			isValid := false
			for _, c := range validColors {
				if value == c {
					isValid = true
					break
				}
			}
			if !isValid {
				return fmt.Sprintf("Error: invalid color '%s'. Valid named colors: %s or rgb:RGB\r\n", value, strings.Join(validColors, ", ")), nil
			}
		} else if strings.HasPrefix(value, "rgb:") {
			if _, ok := parseRGBColor(value); !ok {
				return fmt.Sprintf("Error: invalid RGB format '%s'. Use rgb:RGB with RGB as 3 hex chars (0-F)\r\n", value), nil
			}
		}
	case "ui.fgcolor":
		if value != "" && !strings.HasPrefix(value, "rgb:") {
			validFgColors := []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white", "grey", "bright-black", "bright-red", "bright-green", "bright-yellow", "bright-blue", "bright-magenta", "bright-cyan", "bright-white", "orange", "violet", "pink", "purple", "brown"}
			isValid := false
			for _, c := range validFgColors {
				if value == c {
					isValid = true
					break
				}
			}
			if !isValid {
				return fmt.Sprintf("Error: invalid color '%s'. Valid named colors: %s or rgb:RGB\r\n", value, strings.Join(validFgColors, ", ")), nil
			}
		} else if strings.HasPrefix(value, "rgb:") {
			if _, ok := parseRGBColor(value); !ok {
				return fmt.Sprintf("Error: invalid RGB format '%s'. Use rgb:RGB with RGB as 3 hex chars (0-F)\r\n", value), nil
			}
		}
	case "ui.fgprompt":
		if value != "" && !strings.HasPrefix(value, "rgb:") {
			validFgColors := []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white", "grey", "bright-black", "bright-red", "bright-green", "bright-yellow", "bright-blue", "bright-magenta", "bright-cyan", "bright-white", "orange", "violet", "pink", "purple", "brown"}
			isValid := false
			for _, c := range validFgColors {
				if value == c {
					isValid = true
					break
				}
			}
			if !isValid {
				return fmt.Sprintf("Error: invalid color '%s'. Valid named colors: %s or rgb:RGB\r\n", value, strings.Join(validFgColors, ", ")), nil
			}
		} else if strings.HasPrefix(value, "rgb:") {
			if _, ok := parseRGBColor(value); !ok {
				return fmt.Sprintf("Error: invalid RGB format '%s'. Use rgb:RGB with RGB as 3 hex chars (0-F)\r\n", value), nil
			}
		}
	case "ui.bgprompt":
		if value != "" && !strings.HasPrefix(value, "rgb:") {
			validColors := []string{"black", "red", "green", "yellow", "blue", "dark-blue", "magenta", "cyan", "white", "grey", "bright-black", "bright-red", "bright-green", "bright-yellow", "bright-blue", "bright-magenta", "bright-cyan", "bright-white", "orange", "violet", "pink", "purple", "brown"}
			isValid := false
			for _, c := range validColors {
				if value == c {
					isValid = true
					break
				}
			}
			if !isValid {
				return fmt.Sprintf("Error: invalid color '%s'. Valid named colors: %s or rgb:RGB\r\n", value, strings.Join(validColors, ", ")), nil
			}
		} else if strings.HasPrefix(value, "rgb:") {
			if _, ok := parseRGBColor(value); !ok {
				return fmt.Sprintf("Error: invalid RGB format '%s'. Use rgb:RGB with RGB as 3 hex chars (0-F)\r\n", value), nil
			}
		}
	case "dir.promptfile", "llm.systemprompt":
		// Already handled above
		return output.String(), nil
	case "ai.model":
		// Already handled above
		return output.String(), nil
	case "ai.provider":
		// Already handled above
		return output.String(), nil
	}

	return output.String(), nil
}

// handleGetCommand handles the /get command
func (r *REPL) handleGetCommand(args []string) (string, error) {
	var output strings.Builder
	if len(args) < 2 {
		output.WriteString("Usage: /get <option>\r\n")
		output.WriteString("Available options:\r\n")
		// List all available options and their current values
		for _, option := range r.configOptions.GetAvailableOptions() {
			value := r.configOptions.Get(option)
			// optType := r.configOptions.GetOptionType(option)

			var status string
			if value == "" {
				status = "not set"

				// Check for default value
				if info, exists := r.configOptions.GetOptionInfo(option); exists && info.Default != "" {
					status = fmt.Sprintf("default: %s", info.Default)
				}
			} else {
				status = value
			}
			// fmt.Printf("  %-20s %-15s %s\r\n", option, "("+optType+")", r.configOptions.GetOptionDescription(option))
			output.WriteString(fmt.Sprintf("  %-20s = %-15s\r\n", option, status))
			// (type: %s) - %s\r\n", option, status, optType, r.configOptions.GetOptionDescription(option))
		}
		return output.String(), nil
	}

	option := args[1]

	// If option ends with '.', list all keys that start with that prefix
	if strings.HasSuffix(option, ".") {
		prefix := option
		output.WriteString(fmt.Sprintf("Configuration options starting with '%s':\r\n", prefix))
		found := false
		for _, opt := range r.configOptions.GetAvailableOptions() {
			if strings.HasPrefix(opt, prefix) {
				value := r.configOptions.Get(opt)
				var status string
				if value == "" {
					status = "not set"
					if info, exists := r.configOptions.GetOptionInfo(opt); exists && info.Default != "" {
						status = fmt.Sprintf("default: %s", info.Default)
					}
				} else {
					status = value
				}
				optType := r.configOptions.GetOptionType(opt)
				output.WriteString(fmt.Sprintf("  %-20s = %-15s (type: %s)\r\n", opt, status, optType))
				found = true
			}
		}
		if !found {
			output.WriteString(fmt.Sprintf("No configuration options found starting with '%s'\r\n", prefix))
		}
		return output.String(), nil
	}

	// Check if the option exists
	if _, exists := r.configOptions.GetOptionInfo(option); !exists {
		return r.handleInvalidConfigKey(option), nil
	}

	value := r.configOptions.Get(option)
	optType := r.configOptions.GetOptionType(option)

	// Get detailed status
	var status string
	if value == "" {
		status = "not set"

		// Check for default value
		if info, exists := r.configOptions.GetOptionInfo(option); exists && info.Default != "" {
			status = fmt.Sprintf("default: %s", info.Default)
		}
	} else {
		status = value
	}

	output.WriteString(fmt.Sprintf("%s = %s (type: %s)\r\n", option, status, optType))
	output.WriteString(fmt.Sprintf("Description: %s\r\n", r.configOptions.GetOptionDescription(option)))

	return output.String(), nil
}

// handleUnsetCommand handles the /unset command with auto-completion
func (r *REPL) handleUnsetCommand(args []string) (string, error) {
	var output strings.Builder
	if len(args) < 2 {
		output.WriteString("Usage: /unset <option>\r\n")
		output.WriteString("Available options:\r\n")
		for _, key := range r.configOptions.GetKeys() {
			if value := r.configOptions.Get(key); value != "" {
				optType := r.configOptions.GetOptionType(key)
				output.WriteString(fmt.Sprintf("  %s = %s (type: %s)\r\n", key, value, optType))
			}
		}
		return output.String(), nil
	}

	option := args[1]

	// Unset the option
	r.configOptions.Unset(option)
	output.WriteString(fmt.Sprintf("Unset %s\r\n", option))

	// Handle special options that require updating REPL output/state
	switch option {
	case "llm.rawmode":
	case "llm.stream":
		streamStatus := "enabled"
		if !r.configOptions.GetBool("llm.stream") {
			streamStatus = "disabled"
		}
		output.WriteString(fmt.Sprintf("Streaming mode %s (reverted to default)\r\n", streamStatus))
	case "chat.replies":
		output.WriteString("Include replies reverted to default\r\n")
	case "llm.think":
		output.WriteString("AI reasoning reverted to default\r\n")
	case "chat.log":
		output.WriteString("Logging reverted to default\r\n")
	case "ui.markdown":
		output.WriteString("Markdown rendering reverted to default\r\n")
	case "ui.bgcolor":
		output.WriteString("Input line background color reverted to default\r\n")
	case "ui.fgcolor":
		output.WriteString("Input line foreground color reverted to default\r\n")
	case "ui.fgprompt":
		output.WriteString("Prompt foreground color reverted to default\r\n")
	case "ui.bgprompt":
		output.WriteString("Prompt background color reverted to default\r\n")
	case "ui.bold":
		output.WriteString("Bold text for input line reverted to default\r\n")
	case "dir.promptfile", "llm.systemprompt":
		output.WriteString("System prompt removed\r\n")
	case "ai.model":
		output.WriteString("Model setting reverted to default\r\n")
	case "ai.provider":
		output.WriteString("Provider setting reverted to default\r\n")
	case "llm.schema", "llm.schemafile":
		output.WriteString("Schema cleared\r\n")
	default:
		output.WriteString(fmt.Sprintf("Unset %s\r\n", option))
	}

	return output.String(), nil
}

// handleEnvCommand handles the /env command with the same syntax as /set
func (r *REPL) handleEnvCommand(args []string) (string, error) {
	// Join all args after the command into a single input string. This
	// ensures values containing spaces or uses like "key= value" are
	// handled consistently instead of relying on tokenization.
	var input string
	if len(args) >= 2 {
		input = strings.TrimSpace(strings.Join(args[1:], " "))
	} else {
		input = ""
	}

	if input == "" {
		var output strings.Builder
		output.WriteString("Usage: /env <variable> [value] or /env <variable>=<value>\r\n")
		output.WriteString("Environment variables:\r\n")

		// Get all environment variables and sort them
		envVars := os.Environ()
		sort.Strings(envVars)

		for _, envVar := range envVars {
			parts := strings.SplitN(envVar, "=", 2)
			if len(parts) == 2 {
				key := parts[0]
				value := parts[1]
				// Truncate long values for display
				if len(value) > 50 {
					value = value[:47] + "..."
				}
				output.WriteString(fmt.Sprintf("  %-20s = %s\r\n", key, value))
			}
		}
		return output.String(), nil
	}

	var key, value string
	// Prefer explicit key=value syntax if present in the joined input
	if idx := strings.Index(input, "="); idx != -1 {
		key = strings.TrimSpace(input[:idx])
		value = strings.TrimSpace(input[idx+1:])
		// Treat trailing '=' with no value as an unset request
		if value == "" {
			os.Unsetenv(key)
			return fmt.Sprintf("Unset environment variable %s\r\n", key), nil
		}
	} else {
		// No '=' in input: first token is the key, remainder is the value
		parts := strings.Fields(input)
		key = strings.TrimSpace(parts[0])
		if len(parts) >= 2 {
			value = strings.TrimSpace(strings.Join(parts[1:], " "))
		} else {
			value = ""
		}
	}

	if value == "" {
		// Display current value if no value provided
		val := os.Getenv(key)
		if val == "" {
			return fmt.Sprintf("%s = not set\r\n", key), nil
		}
		return fmt.Sprintf("%s = %s\r\n", key, val), nil
	}

	// Set the environment variable
	os.Setenv(key, value)
	return fmt.Sprintf("Set %s = %s\r\n", key, value), nil
}
