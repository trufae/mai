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

	// AI provider options
	co.RegisterOption("ai.baseurl", StringOption, "Custom base URL for API requests", "")
	co.RegisterOption("ai.deterministic", BooleanOption, "Force deterministic output from LLMs", "false")
	co.RegisterOption("ai.provider", StringOption, "AI provider to use", "")

	// Chat configuration
	co.RegisterOption("chat.aitopic", BooleanOption, "Enable automatic AI-generated session topics", "false")
	co.RegisterOption("chat.autocompact", NumberOption, "Auto-compact conversation when history exceeds threshold (0=off)", "0")
	co.RegisterOption("chat.followup", BooleanOption, "Automatically run #followup after assistant replies", "false")
	co.RegisterOption("chat.format", StringOption, "Chat formatting: tokens, labeled, or plain", "plain")
	co.RegisterOption("chat.log", BooleanOption, "Enable conversation logging", "true")
	// Memory option: load consolidated memory from ~/.mai/memory.txt into conversation context
	co.RegisterOption("chat.memory", BooleanOption, "Load memory.txt from ~/.mai and include in context", "false")
	co.RegisterOption("ai.model", StringOption, "AI model to use", "")
	co.RegisterOption("chat.replies", BooleanOption, "Include chat replies when building a single prompt", "false")
	co.RegisterOption("chat.save", StringOption, "Session save behavior on exit: always, never, or prompt", "prompt")
	co.RegisterOption("chat.system", BooleanOption, "Include chat system messages when building a single prompt", "true")
	// Number of most recent messages to include when sending to the LLM (0 = all)
	co.RegisterOption("chat.tail", NumberOption, "Number of most recent messages to include when sending to the LLM (0=all)", "0")

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
	co.RegisterOption("llm.systempromptfile", StringOption, "Path to system prompt file (default: .mai/systemprompt.md)", "")
	co.RegisterOption("llm.temperature", NumberOption, "Temperature for AI response (0.0-1.0)", "0.7")
	co.RegisterOption("llm.think", BooleanOption, "Enable AI reasoning", "false")

	// REPL behavior options
	co.RegisterOption("repl.debug", BooleanOption, "Show internal processing logs", "false")
	co.RegisterOption("repl.demo", BooleanOption, "Enable demo mode with waiting animation", "false")
	co.RegisterOption("repl.history", BooleanOption, "Enable REPL history", "true")
	co.RegisterOption("repl.prompt", StringOption, "Main prompt string for input", ">>>")
	co.RegisterOption("repl.prompt2", StringOption, "Prompt string for heredoc/continuation lines", "...")
	co.RegisterOption("repl.skiprc", BooleanOption, "Skip loading rc file on start", "false")

	// Screen rendering options
	co.RegisterOption("scr.markdown", BooleanOption, "Enable markdown rendering with colors", "false")

	// Tooling options
	co.RegisterOption("tools.old", BooleanOption, "Process user input using tools.go functions", "false")
	co.RegisterOption("tools.prompts", BooleanOption, "Enable MCP prompts selection to choose a plan template for newtools", "true")
	co.RegisterOption("tools.use", BooleanOption, "Process user input using newtools functions (overrides tools.old)", "false")

	// User details options
	co.RegisterOption("user.details", BooleanOption, "Include user details (CWD, username, OS, language, time) in conversation context", "false")
	co.RegisterOption("user.lang", StringOption, "Language preference for user details (defaults to LANG environment variable)", "")

	// Vector database integration
	co.RegisterOption("vdb", BooleanOption, "Use mai-vdb tool to get context from vector database", "false")
	co.RegisterOption("vdb.datadir", StringOption, "Directory to search for vector database sources", "")
	co.RegisterOption("vdb.limit", NumberOption, "Limit of entries to be used when calling mai-vdb", "5")

	// MCP integration
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
func (r *REPL) handleSetCommand(args []string) (string, error) {
	// Special handling for specific configOptions
	if len(args) >= 3 {
		val := strings.Join(args[2:], " ")
		switch args[1] {
		case "ai.deterministic":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("ai.deterministic", val)
			return "", nil
		case "llm.rawmode":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("llm.rawmode", val)
			return "", nil
		case "dir.promptfile":
			return "", r.loadSystemPrompt(val)
		case "llm.systemprompt":
			// Store inline system prompt via config API; no local cache
			r.configOptions.Set("llm.systemprompt", val)
			return "", nil
		case "ai.model":
			return "", r.setModel(val)
		case "ai.provider":
			provider := strings.ToLower(val)
			return "", r.setProvider(provider)
		case "chat.replies":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("chat.replies", val)
			return "", nil
		case "chat.system":
			// Update option; REPL binds keep provider config in sync
			r.configOptions.Set("chat.system", val)
			return "", nil
		case "chat.format":
			// Accept plain, labeled, tokens
			valLower := strings.ToLower(val)
			if valLower != "plain" && valLower != "labeled" && valLower != "tokens" {
				// still set, but warn
				fmt.Printf("Warning: unknown chat.format '%s'\n", val)
			}
			r.configOptions.Set("chat.format", valLower)
			return "", nil

		}
	}
	if len(args) < 2 {
		var output strings.Builder
		output.WriteString("Usage: /set <option> [value]\r\n")
		output.WriteString("Available options:\r\n")
		for _, option := range r.configOptions.GetAvailableOptions() {
			optType := r.configOptions.GetOptionType(option)
			output.WriteString(fmt.Sprintf("  %-20s %-15s %s\r\n", option, "("+optType+")", r.configOptions.GetOptionDescription(option)))
		}
		return output.String(), nil
	}

	option := args[1]

	// If option ends with '.', list all keys that start with that prefix
	if strings.HasSuffix(option, ".") {
		var output strings.Builder
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

	if len(args) < 3 {
		// Display current value if no value argument is provided
		value := r.configOptions.Get(option)

		// Get option type and status
		optType := r.configOptions.GetOptionType(option)
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

		return fmt.Sprintf("%s = %s (type: %s)\r\n", option, status, optType), nil
	}

	value := args[2]

	// Set the option value with validation
	if err := r.configOptions.Set(option, value); err != nil {
		var output strings.Builder
		output.WriteString(fmt.Sprintf("Error: %v\r\n", err))

		// Show expected format for the type
		if optType := r.configOptions.GetOptionType(option); optType != "" {
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
	switch option {
	case "llm.stream":
		streamStatus := "enabled"
		if !r.configOptions.GetBool("llm.stream") {
			streamStatus = "disabled"
		}
		output.WriteString(fmt.Sprintf("Streaming mode %s\r\n", streamStatus))
	case "chat.replies":
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", option, value))
	case "llm.think":
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", option, value))
	case "chat.log":
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", option, value))
	case "scr.markdown":
		markdownStatus := "enabled"
		if !r.configOptions.GetBool("scr.markdown") {
			markdownStatus = "disabled"
		}
		output.WriteString(fmt.Sprintf("Markdown rendering %s\r\n", markdownStatus))
	case "tools.old":
		toolsStatus := "enabled"
		if !r.configOptions.GetBool("tools.old") {
			toolsStatus = "disabled"
		}
		output.WriteString(fmt.Sprintf("Tools processing %s\r\n", toolsStatus))
	case "dir.promptfile", "llm.systemprompt":
		// Already handled above
		return output.String(), nil
	case "ai.model":
		// Already handled above
		return output.String(), nil
	case "ai.provider":
		// Already handled above
		return output.String(), nil
	default:
		output.WriteString(fmt.Sprintf("Set %s = %s\r\n", option, value))
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
	case "scr.markdown":
		output.WriteString("Markdown rendering reverted to default\r\n")
	case "tools.old":
		output.WriteString("Tools processing reverted to default\r\n")
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
