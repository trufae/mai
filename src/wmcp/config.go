package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// MaiOptions represents configuration options for the mai-wmcp service
type MaiOptions struct {
	BaseURL        string `json:"baseURL,omitempty"`
	YoloMode       bool   `json:"yoloMode,omitempty"`
	OutputReport   string `json:"outputReport,omitempty"`
	DebugMode      bool   `json:"debugMode,omitempty"`
	DrunkMode      bool   `json:"drunkMode,omitempty"`
	NoPrompts      bool   `json:"noPrompts,omitempty"`
	NonInteractive bool   `json:"nonInteractive,omitempty"`
}

// Config represents the main configuration structure
type Config struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	MaiOptions MaiOptions                 `json:"maiOptions,omitempty"`
}

// MCPServerConfig represents the configuration for a single MCP server
type MCPServerConfig struct {
	Type    string            `json:"type"`              // "stdio", "http", or "sse"
	Command string            `json:"command,omitempty"` // for stdio type
	Args    []string          `json:"args,omitempty"`    // for stdio type
	URL     string            `json:"url,omitempty"`     // for http or sse type
	Env     map[string]string `json:"env,omitempty"`
	Tools   map[string]bool   `json:"tools,omitempty"` // Tool name -> enabled status
}

// LoadConfig loads the configuration from a file
func LoadConfig(configPath string) (*Config, error) {
	// If configPath is empty, return empty config (no default loading)
	if configPath == "" {
		return &Config{MCPServers: make(map[string]MCPServerConfig)}, nil
	}

	// Check if the file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{MCPServers: make(map[string]MCPServerConfig)}, nil
	}

	// Read the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	// Parse the JSON
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	// Set defaults for MaiOptions if not specified
	if config.MaiOptions.BaseURL == "" {
		config.MaiOptions.BaseURL = ":8989"
	}

	// Validate the config
	for name, server := range config.MCPServers {
		if server.Type != "stdio" && server.Type != "http" && server.Type != "sse" {
			return nil, fmt.Errorf("server %s: type must be 'stdio', 'http', or 'sse'", name)
		}
		if server.Type == "stdio" && server.Command == "" {
			return nil, fmt.Errorf("server %s: command cannot be empty for stdio type", name)
		}
		if (server.Type == "http" || server.Type == "sse") && server.URL == "" {
			return nil, fmt.Errorf("server %s: url cannot be empty for %s type", name, server.Type)
		}
	}

	return &config, nil
}

// BuildServerCommands converts the config into command strings or URLs to start MCP servers
func (c *Config) BuildServerCommands() map[string]string {
	commands := make(map[string]string)

	for name, server := range c.MCPServers {
		if server.Type == "http" || server.Type == "sse" {
			commands[name] = server.URL
		} else {
			// Build the command string
			cmdParts := []string{server.Command}
			cmdParts = append(cmdParts, server.Args...)
			cmdStr := formatCommandString(cmdParts)
			commands[name] = cmdStr
		}
	}

	return commands
}

// formatCommandString formats a command and its arguments as a single string
func formatCommandString(parts []string) string {
	var result string

	for i, part := range parts {
		if i > 0 {
			result += " "
		}
		// Quote arguments with spaces
		if containsSpace(part) {
			result += fmt.Sprintf("\"%s\"", part)
		} else {
			result += part
		}
	}

	return result
}

// containsSpace checks if a string contains any space characters
func containsSpace(s string) bool {
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

// MAIConfig represents the MAI-specific MCP configuration format
type MAIConfig struct {
	Servers map[string]MAIServer `json:"servers"`
}

// MAIServer represents a server in the MAI config format
type MAIServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
	Tools   map[string]bool   `json:"tools,omitempty"` // Tool name -> enabled status
}

// LoadMAIConfig loads configuration from MAI's mcps.json format
func LoadMAIConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read MAI config file: %v", err)
	}

	var maiConfig MAIConfig
	if err := json.Unmarshal(data, &maiConfig); err != nil {
		return nil, fmt.Errorf("failed to parse MAI config file: %v", err)
	}

	// Convert to wmcp Config format
	config := &Config{
		MCPServers: make(map[string]MCPServerConfig),
		MaiOptions: MaiOptions{
			BaseURL:        ":8989",
			YoloMode:       false,
			NonInteractive: true, // Default to non-interactive for MAI integration
		},
	}

	for name, server := range maiConfig.Servers {
		if !server.Enabled {
			continue // Skip disabled servers
		}

		config.MCPServers[name] = MCPServerConfig{
			Type:    "stdio",
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
			Tools:   server.Tools,
		}
	}

	return config, nil
}

// StartMCPServersFromConfig starts MCP servers from the given config
func StartMCPServersFromConfig(service *MCPService, config *Config) {
	// Build the commands map
	commands := config.BuildServerCommands()

	// Start each server with its environment variables
	for name, cmdStr := range commands {
		serverConfig := config.MCPServers[name]

		// Start server with environment variables and tool filtering
		if err := service.StartServerWithEnvAndTools(name, cmdStr, serverConfig.Env, serverConfig.Tools); err != nil {
			fmt.Printf("Failed to start server %s: %v\n", name, err)
		}
	}
}
