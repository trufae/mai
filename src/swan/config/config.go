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
	Providers    []ProviderConfig   `yaml:"providers"`
	MCPs         []MCPConfig        `yaml:"mcps"`
	Prompts      []PromptConfig     `yaml:"prompts"`
	Agents       []AgentConfig      `yaml:"agents"`
	Orchestrator OrchestratorConfig `yaml:"orchestrator"`
	SwanPrompts  SwanPromptsConfig  `yaml:"swan_prompts,omitempty"`
}

// ProviderConfig defines a provider/model/baseurl combination
type ProviderConfig struct {
	Name      string `yaml:"name"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	BaseURL   string `yaml:"baseurl,omitempty"`
	Immutable bool   `yaml:"immutable,omitempty"`
}

// MCPConfig defines an MCP server with its configuration
type MCPConfig struct {
	Name      string                 `yaml:"name"`
	Type      string                 `yaml:"type"` // e.g., "code", "shell", "websearch"
	Config    map[string]interface{} `yaml:"config,omitempty"`
	Immutable bool                   `yaml:"immutable,omitempty"`
}

// PromptConfig defines a prompt template
type PromptConfig struct {
	Name      string `yaml:"name"`
	Content   string `yaml:"content"`
	Type      string `yaml:"type,omitempty"` // e.g., "system", "user"
	Immutable bool   `yaml:"immutable,omitempty"`
}

// AgentConfig defines configuration for each agent
type AgentConfig struct {
	Name      string   `yaml:"name"`
	Provider  string   `yaml:"provider,omitempty"`  // Reference to ProviderConfig name
	Model     string   `yaml:"model,omitempty"`     // Can be overridden
	BaseURL   string   `yaml:"baseurl,omitempty"`   // Can be overridden
	MCPs      []string `yaml:"mcps,omitempty"`      // List of MCPConfig names
	Prompts   []string `yaml:"prompts,omitempty"`   // List of PromptConfig names
	Port      int      `yaml:"port,omitempty"`      // Will be assigned dynamically if not set
	Dynamic   bool     `yaml:"dynamic,omitempty"`   // If true, can be modified by SWAN
	Immutable bool     `yaml:"immutable,omitempty"` // If true, cannot be modified by SWAN
}

// OrchestratorConfig defines orchestrator settings
type OrchestratorConfig struct {
	Port       int    `yaml:"port"`
	ListenAddr string `yaml:"listen_addr"`
	VDBPath    string `yaml:"vdb_path,omitempty"`
}

// SwanPromptsConfig defines SWAN's own prompts that can be modified over time
type SwanPromptsConfig struct {
	Rules       string `yaml:"rules,omitempty"`
	Reasoning   string `yaml:"reasoning,omitempty"`
	QualityEval string `yaml:"quality_eval,omitempty"`
	Competition string `yaml:"competition,omitempty"`
	Learning    string `yaml:"learning,omitempty"`
	Evolution   string `yaml:"evolution,omitempty"`
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

	// Set default SWAN prompts if not provided
	if config.SwanPrompts.Rules == "" {
		config.SwanPrompts.Rules = `You are SWAN, an intelligent multi-agent orchestration system. Your core rules:
1. Always prioritize quality over speed, but balance both metrics
2. Learn from mistakes and continuously improve decision making
3. Cache high-quality responses from slow models to accelerate future queries
4. Run evaluation competitions for important decisions
5. Enable inter-agent communication for collaborative learning
6. Maintain network knowledge files for each agent
7. Evolve your own prompts and configurations autonomously`
	}

	if config.SwanPrompts.Reasoning == "" {
		config.SwanPrompts.Reasoning = `When making decisions, consider:
- Historical performance metrics (time, quality, success rate)
- Query similarity to past tasks
- Agent specialization and capabilities
- Current system load and resource availability
- Cached responses availability
- Network knowledge from inter-agent interactions
- Recent evaluation competition results`
	}

	if config.SwanPrompts.QualityEval == "" {
		config.SwanPrompts.QualityEval = `Evaluate response quality based on:
- Accuracy and correctness (0.4 weight)
- Completeness and comprehensiveness (0.3 weight)
- Clarity and readability (0.2 weight)
- Efficiency and conciseness (0.1 weight)
- Score from 0.0 to 1.0, where 0.8+ is high quality`
	}

	if config.SwanPrompts.Competition == "" {
		config.SwanPrompts.Competition = `For evaluation competitions:
1. Select 2-3 agents with different characteristics (speed vs accuracy)
2. Give them identical tasks
3. Compare responses using quality evaluation criteria
4. Record winner and reasoning
5. Update agent performance metrics
6. Consider changing agent configurations based on results`
	}

	if config.SwanPrompts.Learning == "" {
		config.SwanPrompts.Learning = `Learning process:
1. Record all task executions with metrics
2. Detect mistakes and log corrections
3. Update agent performance profiles
4. Store successful patterns in VDB cache
5. Share learnings through inter-agent communication
6. Evolve prompts and configurations based on experience`
	}

	if config.SwanPrompts.Evolution == "" {
		config.SwanPrompts.Evolution = `Autonomous evolution guidelines:
1. Monitor system performance trends
2. Identify bottlenecks and improvement opportunities
3. Modify agent configurations for better performance
4. Update your own prompts based on learning
5. Add new agents when needed
6. Remove underperforming agents
7. Document all changes and their rationale`
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

// GetProvider returns a ProviderConfig by name
func (c *SwanConfig) GetProvider(name string) (*ProviderConfig, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return &p, true
		}
	}
	return nil, false
}

// GetMCP returns an MCPConfig by name
func (c *SwanConfig) GetMCP(name string) (*MCPConfig, bool) {
	for _, m := range c.MCPs {
		if m.Name == name {
			return &m, true
		}
	}
	return nil, false
}

// GetPrompt returns a PromptConfig by name
func (c *SwanConfig) GetPrompt(name string) (*PromptConfig, bool) {
	for _, p := range c.Prompts {
		if p.Name == name {
			return &p, true
		}
	}
	return nil, false
}

// ResolveAgentConfig resolves an AgentConfig by expanding references
func (c *SwanConfig) ResolveAgentConfig(agent *AgentConfig) (*ResolvedAgentConfig, error) {
	resolved := &ResolvedAgentConfig{
		Name:    agent.Name,
		Port:    agent.Port,
		Dynamic: agent.Dynamic,
	}

	// Resolve provider
	if agent.Provider != "" {
		provider, exists := c.GetProvider(agent.Provider)
		if !exists {
			return nil, fmt.Errorf("provider %s not found", agent.Provider)
		}
		resolved.Provider = provider.Provider
		resolved.Model = provider.Model
		resolved.BaseURL = provider.BaseURL
	} else {
		resolved.Provider = agent.Provider
		resolved.Model = agent.Model
		resolved.BaseURL = agent.BaseURL
	}

	// Resolve MCPs
	for _, mcpName := range agent.MCPs {
		mcp, exists := c.GetMCP(mcpName)
		if !exists {
			return nil, fmt.Errorf("MCP %s not found", mcpName)
		}
		resolved.MCPs = append(resolved.MCPs, *mcp)
	}

	// Resolve prompts
	for _, promptName := range agent.Prompts {
		prompt, exists := c.GetPrompt(promptName)
		if !exists {
			return nil, fmt.Errorf("prompt %s not found", promptName)
		}
		resolved.Prompts = append(resolved.Prompts, *prompt)
	}

	return resolved, nil
}

// ResolvedAgentConfig is the fully resolved configuration for an agent
type ResolvedAgentConfig struct {
	Name      string
	Provider  string
	Model     string
	BaseURL   string
	MCPs      []MCPConfig
	Prompts   []PromptConfig
	Port      int
	Dynamic   bool
	Immutable bool
}

// IsImmutable checks if any part of the config is immutable
func (c *SwanConfig) IsImmutable(agent *AgentConfig) bool {
	if agent.Immutable {
		return true
	}

	// Check if provider is immutable
	if provider, exists := c.GetProvider(agent.Provider); exists && provider.Immutable {
		return true
	}

	// Check if any MCP is immutable
	for _, mcpName := range agent.MCPs {
		if mcp, exists := c.GetMCP(mcpName); exists && mcp.Immutable {
			return true
		}
	}

	// Check if any prompt is immutable
	for _, promptName := range agent.Prompts {
		if prompt, exists := c.GetPrompt(promptName); exists && prompt.Immutable {
			return true
		}
	}

	return false
}
