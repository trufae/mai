package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SwanConfig represents the main configuration for SWAN
type SwanConfig struct {
	WorkDir      string             `yaml:"work_dir"`
	Agents       []AgentConfig      `yaml:"agents"`
	Orchestrator OrchestratorConfig `yaml:"orchestrator"`
}

// AgentConfig defines configuration for each agent
type AgentConfig struct {
	Name     string            `yaml:"name"`
	Provider string            `yaml:"provider"`
	Model    string            `yaml:"model"`
	MCP      []string          `yaml:"mcp,omitempty"`     // List of MCP servers to connect
	Prompts  map[string]string `yaml:"prompts,omitempty"` // Custom prompts
	Port     int               `yaml:"port,omitempty"`    // Will be assigned dynamically if not set
}

// OrchestratorConfig defines orchestrator settings
type OrchestratorConfig struct {
	Port       int    `yaml:"port"`
	ListenAddr string `yaml:"listen_addr"`
	VDBPath    string `yaml:"vdb_path,omitempty"`
}

// LoadConfig loads the SWAN configuration from swan.yaml
func LoadConfig(configPath string) (*SwanConfig, error) {
	if configPath == "" {
		configPath = "swan.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %v", configPath, err)
	}

	var config SwanConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	// Set defaults
	if config.WorkDir == "" {
		config.WorkDir = "./work"
	}
	if config.Orchestrator.Port == 0 {
		config.Orchestrator.Port = 8080
	}
	if config.Orchestrator.ListenAddr == "" {
		config.Orchestrator.ListenAddr = "0.0.0.0"
	}
	if config.Orchestrator.VDBPath == "" {
		config.Orchestrator.VDBPath = filepath.Join(config.WorkDir, "vdb")
	}

	// Ensure work directory exists
	if err := os.MkdirAll(config.WorkDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %v", err)
	}

	// Ensure tmp directory exists
	tmpDir := filepath.Join(config.WorkDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create tmp directory: %v", err)
	}

	return &config, nil
}

// SaveConfig saves the configuration back to file
func SaveConfig(config *SwanConfig, configPath string) error {
	if configPath == "" {
		configPath = "swan.yaml"
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	return os.WriteFile(configPath, data, 0644)
}
