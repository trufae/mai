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
	co.RegisterOption("debug", BooleanOption, "Show internal processing logs", "false")
	co.RegisterOption("promptdir", StringOption, "Directory to read prompts from", "")
	co.RegisterOption("templatedir", StringOption, "Directory to read templates from", "")
	co.RegisterOption("promptfile", StringOption, "System prompt file path", "")
	co.RegisterOption("systemprompt", StringOption, "System prompt text", "")
	co.RegisterOption("prompt", StringOption, "Main prompt string for input", ">>>")
	co.RegisterOption("readlineprompt", StringOption, "Prompt string for heredoc/continuation lines", "...")
	co.RegisterOption("stream", BooleanOption, "Enable streaming mode", "true")
	co.RegisterOption("include_replies", BooleanOption, "Include assistant replies in context", "true")
	co.RegisterOption("logging", BooleanOption, "Enable conversation logging", "true")
	co.RegisterOption("reasoning", BooleanOption, "Enable AI reasoning", "true")
	co.RegisterOption("markdown", BooleanOption, "Enable markdown rendering with colors", "false")
	co.RegisterOption("max_tokens", NumberOption, "Maximum tokens for AI response", "5128")
	co.RegisterOption("temperature", NumberOption, "Temperature for AI response (0.0-1.0)", "0.7")
	co.RegisterOption("model", StringOption, "AI model to use", "")
	co.RegisterOption("provider", StringOption, "AI provider to use", "")
	co.RegisterOption("baseurl", StringOption, "Custom base URL for API requests", "")
	co.RegisterOption("deterministic", BooleanOption, "Force deterministic output from LLMs", "false")
	co.RegisterOption("usetools", BooleanOption, "Process user input using tools.go functions", "false")
	co.RegisterOption("useragent", StringOption, "Custom user agent for HTTP requests", "mai-repl/1.0")
	co.RegisterOption("history", BooleanOption, "Enable REPL history", "true")
	co.RegisterOption("usemaimd", BooleanOption, "Look for and use MAI.md as a system prompt", "true")

	co.initialized = true

	// Set the global reference to this config
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

// Global reference to the config options for static functions
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
	if promptDir := r.config.options.Get("promptdir"); promptDir != "" {
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

// handleSetCommand handles the /set command with auto-completion and type validation
func (r *REPL) handleSetCommand(args []string) error {
	// Special handling for specific config options
	if len(args) >= 3 {
		switch args[1] {
		case "promptfile":
			// Load system prompt from file
			filePath := args[2]
			return r.loadSystemPrompt(filePath)
		case "systemprompt":
			// Set system prompt directly
			promptText := strings.Join(args[2:], " ")
			r.systemPrompt = promptText
			r.config.options.Set("systemprompt", promptText)
			fmt.Printf("System prompt set (%d chars)\r\n", len(promptText))
			return nil
		case "model":
			// Set model
			model := strings.Join(args[2:], " ")
			return r.setModel(model)
		case "provider":
			// Set provider
			provider := strings.ToLower(args[2])
			return r.setProvider(provider)
		}
	}
	if len(args) < 2 {
		fmt.Print("Usage: /set <option> [value]\r\n")
		fmt.Print("Available options:\r\n")
		for _, option := range GetAvailableOptions() {
			optType := GetOptionType(option)
			fmt.Printf("  %s (%s) - %s\r\n", option, optType, GetOptionDescription(option))
		}
		return nil
	}

	option := args[1]

	if len(args) < 3 {
		// Display current value if no value argument is provided
		value := r.config.options.Get(option)

		// Get option type and status
		optType := GetOptionType(option)
		var status string
		if value == "" {
			status = "not set"

			// Check for default value
			if info, exists := r.config.options.GetOptionInfo(option); exists && info.Default != "" {
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
	if err := r.config.options.Set(option, value); err != nil {
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

	// Handle special options that require updating REPL state
	switch option {
	case "stream":
		r.streamingEnabled = r.config.options.GetBool("stream")
		streamStatus := "enabled"
		if !r.streamingEnabled {
			streamStatus = "disabled"
		}
		fmt.Printf("Streaming mode %s\r\n", streamStatus)
	case "include_replies":
		r.includeReplies = r.config.options.GetBool("include_replies")
		fmt.Printf("Set %s = %s\r\n", option, value)
	case "reasoning":
		r.reasoningEnabled = r.config.options.GetBool("reasoning")
		fmt.Printf("Set %s = %s\r\n", option, value)
	case "logging":
		r.loggingEnabled = r.config.options.GetBool("logging")
		fmt.Printf("Set %s = %s\r\n", option, value)
	case "markdown":
		r.markdownEnabled = r.config.options.GetBool("markdown")
		markdownStatus := "enabled"
		if !r.markdownEnabled {
			markdownStatus = "disabled"
		}
		fmt.Printf("Markdown rendering %s\r\n", markdownStatus)
	case "usetools":
		r.useToolsEnabled = r.config.options.GetBool("usetools")
		toolsStatus := "enabled"
		if !r.useToolsEnabled {
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
			value := r.config.options.Get(option)
			optType := GetOptionType(option)

			var status string
			if value == "" {
				status = "not set"

				// Check for default value
				if info, exists := r.config.options.GetOptionInfo(option); exists && info.Default != "" {
					status = fmt.Sprintf("default: %s", info.Default)
				}
			} else {
				status = value
			}

			fmt.Printf("  %s = %s (type: %s) - %s\r\n",
				option, status, optType, GetOptionDescription(option))
		}
		return nil
	}

	option := args[1]
	value := r.config.options.Get(option)
	optType := GetOptionType(option)

	// Get detailed status
	var status string
	if value == "" {
		status = "not set"

		// Check for default value
		if info, exists := r.config.options.GetOptionInfo(option); exists && info.Default != "" {
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
		for _, key := range r.config.options.GetKeys() {
			if value := r.config.options.Get(key); value != "" {
				optType := GetOptionType(key)
				fmt.Printf("  %s = %s (type: %s)\r\n", key, value, optType)
			}
		}
		return nil
	}

	option := args[1]

	// Unset the option
	r.config.options.Unset(option)
	fmt.Printf("Unset %s\r\n", option)

	// Handle special options that require updating REPL state
	switch option {
	case "stream":
		r.streamingEnabled = r.config.options.GetBool("stream")
		streamStatus := "enabled"
		if !r.streamingEnabled {
			streamStatus = "disabled"
		}
		fmt.Printf("Streaming mode %s (reverted to default)\r\n", streamStatus)
	case "include_replies":
		r.includeReplies = r.config.options.GetBool("include_replies")
		fmt.Printf("Include replies reverted to default\r\n")
	case "reasoning":
		r.reasoningEnabled = r.config.options.GetBool("reasoning")
		fmt.Printf("AI reasoning reverted to default\r\n")
	case "logging":
		r.loggingEnabled = r.config.options.GetBool("logging")
		fmt.Printf("Logging reverted to default\r\n")
	case "markdown":
		r.markdownEnabled = r.config.options.GetBool("markdown")
		fmt.Printf("Markdown rendering reverted to default\r\n")
	case "usetools":
		r.useToolsEnabled = r.config.options.GetBool("usetools")
		fmt.Printf("Tools processing reverted to default\r\n")
	case "promptfile", "systemprompt":
		r.systemPrompt = ""
		fmt.Print("System prompt removed\r\n")
	case "model":
		fmt.Print("Model setting reverted to default\r\n")
	case "provider":
		fmt.Print("Provider setting reverted to default\r\n")
	default:
		fmt.Printf("Unset %s\r\n", option)
	}

	return nil
}
