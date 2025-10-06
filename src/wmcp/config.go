package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MaiOptions represents configuration options for the mai-wmcp service
type MaiOptions struct {
	BaseURL      string `json:"baseURL,omitempty"`
	YoloMode     bool   `json:"yoloMode,omitempty"`
	OutputReport string `json:"outputReport,omitempty"`
	DebugMode    bool   `json:"debugMode,omitempty"`
	DrunkMode    bool   `json:"drunkMode,omitempty"`
	NoPrompts    bool   `json:"noPrompts,omitempty"`
}

// Config represents the main configuration structure
type Config struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	MaiOptions MaiOptions                 `json:"maiOptions,omitempty"`
}

// MCPServerConfig represents the configuration for a single MCP server
type MCPServerConfig struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// LoadConfig loads the configuration from a file
func LoadConfig(configPath string) (*Config, error) {
	// If configPath is empty, use default path
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %v", err)
		}
		configPath = filepath.Join(home, ".mai-wmcp.json")
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
		if server.Type != "stdio" {
			return nil, fmt.Errorf("server %s: only 'stdio' type is supported", name)
		}
		if server.Command == "" {
			return nil, fmt.Errorf("server %s: command cannot be empty", name)
		}
	}

	return &config, nil
}

// BuildServerCommands converts the config into command strings to start MCP servers
func (c *Config) BuildServerCommands() map[string]string {
	commands := make(map[string]string)

	for name, server := range c.MCPServers {
		// Build the command string
		cmdParts := []string{server.Command}
		cmdParts = append(cmdParts, server.Args...)
		cmdStr := formatCommandString(cmdParts)

		commands[name] = cmdStr
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

// StartMCPServersFromConfig starts MCP servers from the given config
func StartMCPServersFromConfig(service *MCPService, config *Config) {
	// Build the commands map
	commands := config.BuildServerCommands()

	// Start each server with its environment variables
	for name, cmdStr := range commands {
		serverConfig := config.MCPServers[name]

		// Set environment variables for this server
		if serverConfig.Env != nil && len(serverConfig.Env) > 0 {
			// Create a new command with environment variables
			if err := service.StartServerWithEnv(name, cmdStr, serverConfig.Env); err != nil {
				fmt.Printf("Failed to start server %s: %v\n", name, err)
			}
		} else {
			// Use the standard start method
			if err := service.StartServer(name, cmdStr); err != nil {
				fmt.Printf("Failed to start server %s: %v\n", name, err)
			}
		}
	}
}
