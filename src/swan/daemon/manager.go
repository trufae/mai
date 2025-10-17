package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"mai/src/swan/config"
)

// AgentProcess represents a running agent process
type AgentProcess struct {
	Name      string                     `json:"name"`
	PID       int                        `json:"pid"`
	Port      int                        `json:"port"`
	Config    config.ResolvedAgentConfig `json:"config"`
	StartTime time.Time                  `json:"start_time"`
}

// DaemonManager manages agent processes
type DaemonManager struct {
	config     *config.SwanConfig
	agents     map[string]*AgentProcess
	agentsFile string
	mu         sync.RWMutex
	nextPort   int
}

// NewDaemonManager creates a new daemon manager
func NewDaemonManager(cfg *config.SwanConfig) *DaemonManager {
	agentsFile := filepath.Join(cfg.WorkDir, "tmp", "swan_agents.json")
	return &DaemonManager{
		config:     cfg,
		agents:     make(map[string]*AgentProcess),
		agentsFile: agentsFile,
		nextPort:   9000, // Start from port 9000
	}
}

// LoadAgents loads agent state from file
func (dm *DaemonManager) LoadAgents() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	data, err := os.ReadFile(dm.agentsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file yet
		}
		return err
	}

	var agents map[string]*AgentProcess
	if err := json.Unmarshal(data, &agents); err != nil {
		return err
	}

	// Check which agents are still running
	for name, agent := range agents {
		if dm.isProcessRunning(agent.PID) {
			dm.agents[name] = agent
			if agent.Port >= dm.nextPort {
				dm.nextPort = agent.Port + 1
			}
		}
	}

	return nil
}

// SaveAgents saves agent state to file
func (dm *DaemonManager) SaveAgents() error {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	data, err := json.MarshalIndent(dm.agents, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(dm.agentsFile, data, 0644)
}

// StartAgent starts a new agent process
func (dm *DaemonManager) StartAgent(agentConfig config.AgentConfig) error {
	// Resolve the agent config
	resolved, err := dm.config.ResolveAgentConfig(&agentConfig)
	if err != nil {
		return fmt.Errorf("failed to resolve agent config: %v", err)
	}

	return dm.startResolvedAgent(*resolved)
}

// StartResolvedAgent starts an agent with fully resolved config
func (dm *DaemonManager) StartResolvedAgent(resolved config.ResolvedAgentConfig) error {
	return dm.startResolvedAgent(resolved)
}

// startResolvedAgent starts an agent with fully resolved config
func (dm *DaemonManager) startResolvedAgent(resolved config.ResolvedAgentConfig) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if _, exists := dm.agents[resolved.Name]; exists {
		return fmt.Errorf("agent %s already running", resolved.Name)
	}

	// Find available port
	port := resolved.Port
	if port == 0 {
		port = dm.findAvailablePort()
		if port == 0 {
			return fmt.Errorf("no available port found")
		}
	}

	// Build MAI command arguments
	args := []string{
		"-M", // MCP mode
		"-p", resolved.Provider,
		"-m", resolved.Model,
		"-b", fmt.Sprintf("http://127.0.0.1:%d", port), // Bind to localhost:port
	}

	// Add base URL if specified
	if resolved.BaseURL != "" {
		args = append(args, "-b", resolved.BaseURL)
	}

	// Add MCP servers
	if len(resolved.MCPs) > 0 {
		args = append(args, "-c", "mcp.use=true")
	}
	for _, mcp := range resolved.MCPs {
		args = append(args, "-c", fmt.Sprintf("mcp.server=%s", mcp.Name))
		// Add MCP-specific config if available
		for key, value := range mcp.Config {
			args = append(args, "-c", fmt.Sprintf("mcp.%s.%s=%v", mcp.Name, key, value))
		}
	}

	// Add custom prompts
	for _, prompt := range resolved.Prompts {
		if prompt.Type == "system" {
			args = append(args, "-c", fmt.Sprintf("llm.systemprompt=%s", prompt.Content))
		} else {
			args = append(args, "-c", fmt.Sprintf("llm.%s=%s", prompt.Type, prompt.Content))
		}
	}

	// Start the process
	cmd := exec.Command("mai-repl", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}

	// Redirect output to files
	logFile := filepath.Join(dm.config.WorkDir, "tmp", fmt.Sprintf("%s.log", resolved.Name))
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	cmd.Stdout = log
	cmd.Stderr = log

	if err := cmd.Start(); err != nil {
		log.Close()
		return fmt.Errorf("failed to start agent: %v", err)
	}

	// Create agent process record
	agent := &AgentProcess{
		Name:      resolved.Name,
		PID:       cmd.Process.Pid,
		Port:      port,
		Config:    resolved,
		StartTime: time.Now(),
	}

	dm.agents[resolved.Name] = agent

	// Save state
	if err := dm.SaveAgents(); err != nil {
		// Log but don't fail
		fmt.Printf("Warning: failed to save agents: %v\n", err)
	}

	go func() {
		// Wait for process to finish and clean up
		cmd.Wait()
		log.Close()
		dm.mu.Lock()
		delete(dm.agents, resolved.Name)
		dm.mu.Unlock()
		dm.SaveAgents()
	}()

	return nil
}

// StopAgent stops an agent process
func (dm *DaemonManager) StopAgent(name string) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	agent, exists := dm.agents[name]
	if !exists {
		return fmt.Errorf("agent %s not found", name)
	}

	if err := dm.killProcess(agent.PID); err != nil {
		return fmt.Errorf("failed to stop agent: %v", err)
	}

	delete(dm.agents, name)
	return dm.SaveAgents()
}

// ListAgents returns list of running agents
func (dm *DaemonManager) ListAgents() map[string]*AgentProcess {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	result := make(map[string]*AgentProcess)
	for k, v := range dm.agents {
		result[k] = v
	}
	return result
}

// GetAgent returns agent info by name
func (dm *DaemonManager) GetAgent(name string) (*AgentProcess, bool) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	agent, exists := dm.agents[name]
	return agent, exists
}

// findAvailablePort finds an available TCP port
func (dm *DaemonManager) findAvailablePort() int {
	for port := dm.nextPort; port < dm.nextPort+1000; port++ {
		if dm.isPortAvailable(port) {
			dm.nextPort = port + 1
			return port
		}
	}
	return 0
}

// isPortAvailable checks if a port is available
func (dm *DaemonManager) isPortAvailable(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return true // Port is available
	}
	conn.Close()
	return false
}

// isProcessRunning checks if a process is still running
func (dm *DaemonManager) isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// killProcess kills a process and its children
func (dm *DaemonManager) killProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	// Send SIGTERM first
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	// Wait a bit for graceful shutdown
	time.Sleep(2 * time.Second)

	// If still running, force kill
	if dm.isProcessRunning(pid) {
		return process.Kill()
	}

	return nil
}

// StartAllAgents loads all agents from config (no processes started)
func (dm *DaemonManager) StartAllAgents() error {
	for _, agentConfig := range dm.config.Agents {
		resolved, err := dm.config.ResolveAgentConfig(&agentConfig)
		if err != nil {
			fmt.Printf("Warning: failed to resolve agent %s: %v\n", agentConfig.Name, err)
			continue
		}
		agent := &AgentProcess{
			Name:      resolved.Name,
			PID:       0, // No process
			Port:      0, // No port
			Config:    *resolved,
			StartTime: time.Now(),
		}
		dm.agents[resolved.Name] = agent
	}
	return nil
}

// CreateDynamicAgent creates a new agent with specified provider, MCPs, and prompts
func (dm *DaemonManager) CreateDynamicAgent(name string, providerName string, mcpNames []string, promptNames []string) error {
	agentConfig := config.AgentConfig{
		Name:     name,
		Provider: providerName,
		MCPs:     mcpNames,
		Prompts:  promptNames,
		Dynamic:  true,
	}

	return dm.StartAgent(agentConfig)
}

// StopAllAgents stops all running agents
func (dm *DaemonManager) StopAllAgents() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var errors []error
	for name := range dm.agents {
		if err := dm.StopAgent(name); err != nil {
			errors = append(errors, fmt.Errorf("failed to stop %s: %v", name, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors stopping agents: %v", errors)
	}

	return nil
}
