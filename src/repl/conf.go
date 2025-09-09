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

	// Enable automatic AI-generated session topics (#topic)
	// Set session save behavior on exit: always, never, or prompt
	co.RegisterOption("aitopic", BooleanOption, "Enable automatic AI-generated session topics", "false")
	co.RegisterOption("baseurl", StringOption, "Custom base URL for API requests", "")
	co.RegisterOption("debug", BooleanOption, "Show internal processing logs", "false")
	co.RegisterOption("deterministic", BooleanOption, "Force deterministic output from LLMs", "false")
	co.RegisterOption("history", BooleanOption, "Enable REPL history", "true")
	co.RegisterOption("skiprc", BooleanOption, "Skip loading rc file on start", "false")

	co.RegisterOption("logging", BooleanOption, "Enable conversation logging", "true")
	co.RegisterOption("markdown", BooleanOption, "Enable markdown rendering with colors", "false")
	co.RegisterOption("max_tokens", NumberOption, "Maximum tokens for AI response", "5128")
	co.RegisterOption("model", StringOption, "AI model to use", "")
	co.RegisterOption("prompt", StringOption, "Main prompt string for input", ">>>")
	co.RegisterOption("promptdir", StringOption, "Directory to read prompts from", "")
	co.RegisterOption("promptfile", StringOption, "System prompt file path", "")
	co.RegisterOption("schema", StringOption, "Inline JSON schema to constrain model output", "")
	co.RegisterOption("schemafile", StringOption, "Path to JSON schema file for formatted output", "")
	co.RegisterOption("provider", StringOption, "AI provider to use", "")
	co.RegisterOption("rawdog", BooleanOption, "Send messages in raw", "false")
	co.RegisterOption("readlineprompt", StringOption, "Prompt string for heredoc/continuation lines", "...")
	co.RegisterOption("reasoning", BooleanOption, "Enable AI reasoning", "false")
	co.RegisterOption("session_save", StringOption, "Session save behavior on exit: always, never, or prompt", "prompt")
	co.RegisterOption("stream", BooleanOption, "Enable streaming mode", "true")
	co.RegisterOption("systemprompt", StringOption, "System prompt text (overrides systempromptfile)", "")
	co.RegisterOption("systempromptfile", StringOption, "Path to system prompt file (default: .mai/systemprompt.md)", "")
	co.RegisterOption("temperature", NumberOption, "Temperature for AI response (0.0-1.0)", "0.7")
	co.RegisterOption("templatedir", StringOption, "Directory to read templates from", "")
	co.RegisterOption("useragent", StringOption, "Custom user agent for HTTP requests", "mai-repl/1.0")
	co.RegisterOption("usetools", BooleanOption, "Process user input using tools.go functions", "false")
	co.RegisterOption("newtools", BooleanOption, "Process user input using newtools functions (overrides usetools)", "false")
	co.RegisterOption("mcpprompts", BooleanOption, "Enable MCP prompts selection to choose a plan template for newtools", "false")
	// Memory option: load consolidated memory from ~/.mai/memory.txt into conversation context
	co.RegisterOption("memory", BooleanOption, "Load memory.txt from ~/.mai and include in context", "false")
	// Followup option: automatically run #followup after assistant replies
	co.RegisterOption("followup", BooleanOption, "Automatically run #followup after assistant replies", "false")

	// Conversation formatting options (used when building a single prompt from history)
	co.RegisterOption("conversation_include_llm", BooleanOption, "Include assistant/LLM messages when building a single prompt", "false")
	co.RegisterOption("conversation_include_system", BooleanOption, "Include system messages when building a single prompt", "true")
	co.RegisterOption("conversation_format", StringOption, "Conversation formatting: tokens, labeled, or plain", "plain")
	co.RegisterOption("conversation_use_last_user", BooleanOption, "Only include the last user message when building a single prompt", "false")

	// Server configuration
	co.RegisterOption("listen", StringOption, "Listen address for the web server (host:port)", "0.0.0.0:9000")

	co.initialized = true

	// Set the global reference to this config
	// TODO rimraf
	globalConfig = co

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
			if normalizedValue != "true" && normalizedValue != "false" {
				return fmt.Errorf("invalid boolean value: %s (must be 'true' or 'false')", value)
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

// Global reference to the configOptions for static functions
var globalConfig *ConfigOptions

// GetAvailableOptions returns a list of all available configuration options
// GetAvailableOptions returns a sorted list of all available configuration options
func GetAvailableOptions() []string {
	opts := make([]string, 0, len(globalConfig.optionInfos))
	for key := range globalConfig.optionInfos {
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
func GetOptionDescription(option string) string {
	if info, exists := globalConfig.GetOptionInfo(option); exists {
		return info.Description
	}
	return "No description available"
}

// GetOptionType returns the type of a given option
// GetOptionType returns the type of a given option
func GetOptionType(option string) OptionType {
	if info, exists := globalConfig.GetOptionInfo(option); exists {
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
	if promptDir := r.configOptions.Get("promptdir"); promptDir != "" {
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
		"./prompts",  // Current directory's prompts folder
		"../prompts", // Parent directory's prompts folder
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

// TODO: move into repl.go?
// handleSetCommand handles the /set command with auto-completion and type validation
func (r *REPL) handleSetCommand(args []string) error {
	// Special handling for specific configOptions
	if len(args) >= 3 {
		val := strings.Join(args[2:], " ")
		switch args[1] {
		case "deterministic":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("deterministic", val)
			return nil
		case "rawdog":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("rawdog", val)
			return nil
		case "promptfile":
			return r.loadSystemPrompt(val)
		case "systemprompt":
			// Store inline system prompt via config API; no local cache
			r.configOptions.Set("systemprompt", val)
			fmt.Printf("System prompt set (%d chars)\r\n", len(val))
			return nil
		case "model":
			return r.setModel(val)
		case "provider":
			provider := strings.ToLower(val)
			return r.setProvider(provider)
		case "conversation_include_llm":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("conversation_include_llm", val)
			return nil
		case "conversation_include_system":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("conversation_include_system", val)
			return nil
		case "conversation_format":
			// Accept plain, labeled, tokens
			valLower := strings.ToLower(val)
			if valLower != "plain" && valLower != "labeled" && valLower != "tokens" {
				// still set, but warn
				fmt.Printf("Warning: unknown conversation_format '%s'\n", val)
			}
			r.configOptions.Set("conversation_format", valLower)
			return nil
		case "conversation_use_last_user":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("conversation_use_last_user", val)
			return nil
		}
	}
	if len(args) < 2 {
		fmt.Print("Usage: /set <option> [value]\r\n")
		fmt.Print("Available options:\r\n")
		for _, option := range GetAvailableOptions() {
			optType := GetOptionType(option)
			fmt.Printf("  %-20s %-15s %s\r\n", option, "("+optType+")", GetOptionDescription(option))
		}
		return nil
	}

	option := args[1]

	if len(args) < 3 {
		// Display current value if no value argument is provided
		value := r.configOptions.Get(option)

		// Get option type and status
		optType := GetOptionType(option)
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

		fmt.Printf("%s = %s (type: %s)\r\n", option, status, optType)
		return nil
	}

	value := args[2]

	// Set the option value with validation
	if err := r.configOptions.Set(option, value); err != nil {
		fmt.Printf("Error: %v\r\n", err)

		// Show expected format for the type
		if optType := GetOptionType(option); optType != "" {
			switch optType {
			case BooleanOption:
				fmt.Print("Boolean options accept: true, false\r\n")
			case NumberOption:
				fmt.Print("Number options accept numeric values\r\n")
			}
		}

		return nil
	}

	// Handle special options that require updating REPL output/state
	switch option {
	case "stream":
		streamStatus := "enabled"
		if !r.configOptions.GetBool("stream") {
			streamStatus = "disabled"
		}
		fmt.Printf("Streaming mode %s\r\n", streamStatus)
	case "conversation_include_llm":
		fmt.Printf("Set %s = %s\r\n", option, value)
	case "reasoning":
		fmt.Printf("Set %s = %s\r\n", option, value)
	case "logging":
		fmt.Printf("Set %s = %s\r\n", option, value)
	case "markdown":
		markdownStatus := "enabled"
		if !r.configOptions.GetBool("markdown") {
			markdownStatus = "disabled"
		}
		fmt.Printf("Markdown rendering %s\r\n", markdownStatus)
	case "usetools":
		toolsStatus := "enabled"
		if !r.configOptions.GetBool("usetools") {
			toolsStatus = "disabled"
		}
		fmt.Printf("Tools processing %s\r\n", toolsStatus)
	case "promptfile", "systemprompt":
		// Already handled above
		return nil
	case "model":
		// Already handled above
		return nil
	case "provider":
		// Already handled above
		return nil
	default:
		fmt.Printf("Set %s = %s\r\n", option, value)
	}

	return nil
}

// handleGetCommand handles the /get command
func (r *REPL) handleGetCommand(args []string) error {
	if len(args) < 2 {
		fmt.Print("Usage: /get <option>\r\n")
		fmt.Print("Available options:\r\n")
		// List all available options and their current values
		for _, option := range GetAvailableOptions() {
			value := r.configOptions.Get(option)
			// optType := GetOptionType(option)

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
			// fmt.Printf("  %-20s %-15s %s\r\n", option, "("+optType+")", GetOptionDescription(option))
			fmt.Printf("  %-20s = %-15s\r\n", option, status)
			// (type: %s) - %s\r\n", option, status, optType, GetOptionDescription(option))
		}
		return nil
	}

	option := args[1]
	value := r.configOptions.Get(option)
	optType := GetOptionType(option)

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

	fmt.Printf("%s = %s (type: %s)\r\n", option, status, optType)
	fmt.Printf("Description: %s\r\n", GetOptionDescription(option))

	return nil
}

// handleUnsetCommand handles the /unset command with auto-completion
func (r *REPL) handleUnsetCommand(args []string) error {
	if len(args) < 2 {
		fmt.Print("Usage: /unset <option>\r\n")
		fmt.Print("Available options:\r\n")
		for _, key := range r.configOptions.GetKeys() {
			if value := r.configOptions.Get(key); value != "" {
				optType := GetOptionType(key)
				fmt.Printf("  %s = %s (type: %s)\r\n", key, value, optType)
			}
		}
		return nil
	}

	option := args[1]

	// Unset the option
	r.configOptions.Unset(option)
	fmt.Printf("Unset %s\r\n", option)

	// Handle special options that require updating REPL output/state
	switch option {
	case "rawdog":
	case "stream":
		streamStatus := "enabled"
		if !r.configOptions.GetBool("stream") {
			streamStatus = "disabled"
		}
		fmt.Printf("Streaming mode %s (reverted to default)\r\n", streamStatus)
	case "conversation_include_llm":
		fmt.Printf("Include replies reverted to default\r\n")
	case "reasoning":
		fmt.Printf("AI reasoning reverted to default\r\n")
	case "logging":
		fmt.Printf("Logging reverted to default\r\n")
	case "markdown":
		fmt.Printf("Markdown rendering reverted to default\r\n")
	case "usetools":
		fmt.Printf("Tools processing reverted to default\r\n")
	case "promptfile", "systemprompt":
		fmt.Print("System prompt removed\r\n")
	case "model":
		fmt.Print("Model setting reverted to default\r\n")
	case "provider":
		fmt.Print("Provider setting reverted to default\r\n")
	case "schema", "schemafile":
		fmt.Print("Schema cleared\r\n")
	default:
		fmt.Printf("Unset %s\r\n", option)
	}

	return nil
}
