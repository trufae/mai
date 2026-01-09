package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"

	"github.com/trufae/mai/src/repl/art"
	"github.com/trufae/mai/src/repl/llm"
)

// parseShellArgs parses a string into shell-like arguments, handling quotes
func parseShellArgs(s string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case !inQuotes && (c == '"' || c == '\''):
			inQuotes = true
			quoteChar = c
		case inQuotes && c == quoteChar:
			inQuotes = false
			quoteChar = 0
		case !inQuotes && c == ' ':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// getMCPSConfigPath returns the path to the MCP servers configuration file
func getMCPSConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}
	configDir := filepath.Join(home, ".config", "mai")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}
	return filepath.Join(configDir, "mcps.json"), nil
}

// loadMCPConfig loads the MCP configuration from the config file
func (r *REPL) loadMCPConfig() error {
	configPath, err := getMCPSConfigPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default config if it doesn't exist
		r.mcpConfig = &MCPConfig{
			Servers: make(map[string]MCPServer),
		}
		return r.saveMCPConfig()
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read MCP config: %v", err)
	}

	r.mcpConfig = &MCPConfig{}
	if err := json.Unmarshal(data, r.mcpConfig); err != nil {
		return fmt.Errorf("failed to parse MCP config: %v", err)
	}

	return nil
}

// saveMCPConfig saves the MCP configuration to the config file
func (r *REPL) saveMCPConfig() error {
	configPath, err := getMCPSConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(r.mcpConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal MCP config: %v", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// findAvailablePort finds an available port starting from the given port
func findAvailablePort(startPort int) (int, error) {
	for port := startPort; port < startPort+100; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
		if err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports found")
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

// buildLLMConfig constructs a provider config from environment defaults and current options.
// This avoids storing a persistent config in the REPL and ensures providers
// always receive up-to-date settings (provider, model, schema, headers, etc.).
func (r *REPL) buildLLMConfig() *llm.Config {
	return r.buildLLMConfigForTask("")
}

// buildLLMConfigForTask constructs a provider config for a specific task.
// If task is empty, it uses the default model and provider.
func (r *REPL) buildLLMConfigForTask(task string) *llm.Config {
	cfg := loadConfig()
	// Apply current options into the provider config (provider, model, baseurl, toggles, schema)
	applyConfigOptionsToLLMConfigForTask(cfg, &r.configOptions, task)
	// Respect REPL streaming option
	cfg.NoStream = !r.configOptions.GetBool("llm.stream")
	// Set demo mode option
	cfg.DemoMode = r.configOptions.GetBool("ui.demo")
	return cfg
}

// AskYesNo prompts the user with a yes/no question, defaulting to 'y' or 'n'.
// Returns true for yes, false for no.
func AskYesNo(question string, defaultVal rune) bool {
	// Normalize default and validate
	dv := unicode.ToLower(defaultVal)
	if dv != 'y' && dv != 'n' {
		panic("default value must be 'y' or 'n'")
	}

	var defaultText string
	if dv == 'y' {
		defaultText = "[Y/n]"
	} else {
		defaultText = "[y/N]"
	}

	fmt.Printf("%s %s ", question, defaultText)

	fd := int(os.Stdin.Fd())
	// If stdin is not a terminal, fall back to the default choice instead of panicking
	if !term.IsTerminal(fd) {
		fmt.Println()
		return dv == 'n'
	}

	// Put terminal in raw mode; if this fails, fall back to default
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: unable to set terminal raw mode: %v\n", err)
		fmt.Println()
		return dv == 'y'
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	// Read one byte
	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return dv == 'y'
	}

	c := buf[0]
	if c == '\r' || c == '\n' { // Enter pressed -> use default
		return dv == 'y'
	}

	c = byte(unicode.ToLower(rune(c)))
	return c == 'y'
}

// handleMCPCommand handles the /mcp command and its subcommands
func (r *REPL) handleMCPCommand(args []string) (string, error) {
	// Load MCP config if not loaded
	if r.mcpConfig == nil {
		if err := r.loadMCPConfig(); err != nil {
			return fmt.Sprintf("Failed to load MCP config: %v\r\n", err), nil
		}
	}

	// Show help if no arguments provided
	if len(args) < 2 {
		var output strings.Builder
		output.WriteString("MCP server management commands:\r\n")
		output.WriteString("  /mcp start [server]   - Start MCP server(s) (all enabled if no server specified)\r\n")
		output.WriteString("  /mcp stop [server]    - Stop MCP server(s) (all if no server specified)\r\n")
		output.WriteString("  /mcp restart [server] - Restart MCP server(s)\r\n")
		output.WriteString("  /mcp enable <server>  - Enable MCP server\r\n")
		output.WriteString("  /mcp disable <server> - Disable MCP server\r\n")
		output.WriteString("  /mcp status           - Show status of MCP servers\r\n")
		output.WriteString("  /mcp edit             - Edit MCP configuration file\r\n")
		return output.String(), nil
	}

	action := args[1]
	switch action {
	case "start":
		return r.handleMCPStart(args[2:])
	case "stop":
		return r.handleMCPStop(args[2:])
	case "restart":
		return r.handleMCPRestart(args[2:])
	case "enable":
		return r.handleMCPEnable(args[2:])
	case "disable":
		return r.handleMCPDisable(args[2:])
	case "status":
		return r.handleMCPStatus()
	case "edit":
		return r.handleMCPEdit()
	default:
		return fmt.Sprintf("Unknown MCP action: %s\r\n", action), nil
	}
}

// handleMCPStart starts MCP servers
func (r *REPL) handleMCPStart(servers []string) (string, error) {
	var output strings.Builder

	if len(servers) == 0 {
		// Start all enabled servers
		for name, server := range r.mcpConfig.Servers {
			if server.Enabled {
				if err := r.startMCPServer(name); err != nil {
					output.WriteString(fmt.Sprintf("Failed to start %s: %v\r\n", name, err))
				} else {
					output.WriteString(fmt.Sprintf("Started %s\r\n", name))
				}
			}
		}
	} else {
		// Start specific servers
		for _, name := range servers {
			if _, exists := r.mcpConfig.Servers[name]; !exists {
				output.WriteString(fmt.Sprintf("Server %s not found\r\n", name))
				continue
			}
			if err := r.startMCPServer(name); err != nil {
				output.WriteString(fmt.Sprintf("Failed to start %s: %v\r\n", name, err))
			} else {
				output.WriteString(fmt.Sprintf("Started %s\r\n", name))
			}
		}
	}

	if output.Len() == 0 {
		return "No servers to start\r\n", nil
	}
	return output.String(), nil
}

// handleMCPStop stops MCP servers
func (r *REPL) handleMCPStop(servers []string) (string, error) {
	var output strings.Builder

	if len(servers) == 0 {
		// Stop all mai-wmcp processes (since they persist across sessions)
		if err := exec.Command("pkill", "-f", "mai-wmcp").Run(); err != nil {
			output.WriteString(fmt.Sprintf("Failed to stop mai-wmcp processes: %v\r\n", err))
		} else {
			output.WriteString("Stopped all MCP servers\r\n")
		}
		// Clear process records
		r.mcpProcesses = make(map[string]*MCPProcess)
	} else {
		// For specific servers, we can't easily identify which mai-wmcp process serves which server
		// So we'll stop all and let the user restart specific ones
		output.WriteString("Stopping specific servers not yet implemented. Use '/mcp stop' to stop all.\r\n")
	}

	return output.String(), nil
}

// handleMCPRestart restarts MCP servers
func (r *REPL) handleMCPRestart(servers []string) (string, error) {
	var output strings.Builder

	if len(servers) == 0 {
		// Restart all running servers
		for name := range r.mcpProcesses {
			if err := r.restartMCPServer(name); err != nil {
				output.WriteString(fmt.Sprintf("Failed to restart %s: %v\r\n", name, err))
			} else {
				output.WriteString(fmt.Sprintf("Restarted %s\r\n", name))
			}
		}
	} else {
		// Restart specific servers
		for _, name := range servers {
			if err := r.restartMCPServer(name); err != nil {
				output.WriteString(fmt.Sprintf("Failed to restart %s: %v\r\n", name, err))
			} else {
				output.WriteString(fmt.Sprintf("Restarted %s\r\n", name))
			}
		}
	}

	if output.Len() == 0 {
		return "No servers to restart\r\n", nil
	}
	return output.String(), nil
}

// handleMCPEnable enables MCP servers
func (r *REPL) handleMCPEnable(servers []string) (string, error) {
	if len(servers) == 0 {
		return "Usage: /mcp enable <server>\r\n", nil
	}

	var output strings.Builder
	for _, name := range servers {
		if server, exists := r.mcpConfig.Servers[name]; exists {
			server.Enabled = true
			r.mcpConfig.Servers[name] = server
			output.WriteString(fmt.Sprintf("Enabled %s\r\n", name))
		} else {
			output.WriteString(fmt.Sprintf("Server %s not found\r\n", name))
		}
	}

	if err := r.saveMCPConfig(); err != nil {
		return fmt.Sprintf("Failed to save config: %v\r\n", err), nil
	}

	return output.String(), nil
}

// handleMCPDisable disables MCP servers
func (r *REPL) handleMCPDisable(servers []string) (string, error) {
	if len(servers) == 0 {
		return "Usage: /mcp disable <server>\r\n", nil
	}

	var output strings.Builder
	for _, name := range servers {
		if server, exists := r.mcpConfig.Servers[name]; exists {
			server.Enabled = false
			r.mcpConfig.Servers[name] = server
			// Also stop if running
			r.stopMCPServer(name)
			output.WriteString(fmt.Sprintf("Disabled %s\r\n", name))
		} else {
			output.WriteString(fmt.Sprintf("Server %s not found\r\n", name))
		}
	}

	if err := r.saveMCPConfig(); err != nil {
		return fmt.Sprintf("Failed to save config: %v\r\n", err), nil
	}

	return output.String(), nil
}

// handleMCPStatus shows MCP server status
func (r *REPL) handleMCPStatus() (string, error) {
	var output strings.Builder
	// output.WriteString("MCP Servers Status:\r\n")
	// output.WriteString("==================\r\n")

	if len(r.mcpConfig.Servers) == 0 {
		output.WriteString("No MCP servers configured\r\n")
		return output.String(), nil
	}

	// Check if there are any mai-wmcp processes running
	hasRunningProcesses := false
	if output2, err := exec.Command("pgrep", "-f", "mai-wmcp").Output(); err == nil && len(output2) > 0 {
		hasRunningProcesses = true
	}

	for name, server := range r.mcpConfig.Servers {
		status := "stopped"
		port := ""

		// Check if we have a process record and it's still running
		if process, exists := r.mcpProcesses[name]; exists && process.Process != nil {
			if process.Process.ProcessState == nil || !process.Process.ProcessState.Exited() {
				status = "running"
				port = fmt.Sprintf(" (port %d)", process.Port)
			}
		} else if server.Enabled && hasRunningProcesses {
			// Assume enabled servers are running if we have mai-wmcp processes
			status = "running"
		}

		enabled := "disabled"
		if server.Enabled {
			enabled = "enabled"
		}

		output.WriteString(fmt.Sprintf("%s: %s, %s%s\r\n", name, status, enabled, port))
	}

	return output.String(), nil
}

// handleMCPEdit opens the MCP config file for editing
func (r *REPL) handleMCPEdit() (string, error) {
	configPath, err := getMCPSConfigPath()
	if err != nil {
		return fmt.Sprintf("Failed to get config path: %v\r\n", err), nil
	}

	// Ensure the config file is indented before editing
	if err := r.saveMCPConfig(); err != nil {
		return fmt.Sprintf("Failed to indent config: %v\r\n", err), nil
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "nano" // fallback editor
		}
	}

	cmd := exec.Command(editor, configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("Failed to open editor: %v\r\n", err), nil
	}

	// Reload config after editing
	if err := r.loadMCPConfig(); err != nil {
		return fmt.Sprintf("Failed to reload config: %v\r\n", err), nil
	}

	return "Config updated\r\n", nil
}

// startMCPServer starts a single MCP server
func (r *REPL) startMCPServer(name string) error {
	server, exists := r.mcpConfig.Servers[name]
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	// Check if we already have a process record for this server
	// Since MCP servers persist across sessions, we allow "restarting" them
	// but we'll use a different port if needed
	if process, exists := r.mcpProcesses[name]; exists && process.Process != nil {
		// Check if the process is still running
		if process.Process.ProcessState == nil || !process.Process.ProcessState.Exited() {
			return fmt.Errorf("server %s already running on port %d", name, process.Port)
		}
		// Process has exited, clean up the record
		delete(r.mcpProcesses, name)
	}

	// Find available port
	port, err := findAvailablePort(8989)
	if err != nil {
		return fmt.Errorf("failed to find available port: %v", err)
	}

	// Build the full command string for the MCP server
	cmdParts := []string{server.Command}
	cmdParts = append(cmdParts, server.Args...)
	cmdStr := formatCommandString(cmdParts)

	// Build mai-wmcp arguments

	// YOLO
	args := []string{"-b", fmt.Sprintf(":%d", port), "-i", "-y", cmdStr}
	// args := []string{"-b", fmt.Sprintf(":%d", port), cmdStr}

	// Create environment
	env := os.Environ()
	for key, value := range server.Env {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	cmd := exec.Command("mai-wmcp", args...)

	// Capture output for debugging
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %v", err)
	}

	// Give the process a moment to start up
	time.Sleep(500 * time.Millisecond)

	// Check if process is still running
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return fmt.Errorf("process exited immediately")
	}

	// Check if the process is actually running by trying to find it
	if cmd.Process == nil {
		return fmt.Errorf("process not started")
	}

	// Store process info (even though the process will persist independently)
	r.mcpProcesses[name] = &MCPProcess{
		Name:    name,
		Process: cmd,
		Port:    port,
	}

	return nil
}

// stopMCPServer stops a single MCP server
func (r *REPL) stopMCPServer(name string) error {
	process, exists := r.mcpProcesses[name]
	if !exists || process.Process == nil {
		return fmt.Errorf("server %s not running", name)
	}

	if process.Process.Process != nil {
		if err := process.Process.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %v", err)
		}
		process.Process.Wait()
	}

	delete(r.mcpProcesses, name)
	return nil
}

// restartMCPServer restarts a single MCP server
func (r *REPL) restartMCPServer(name string) error {
	if err := r.stopMCPServer(name); err != nil {
		// If it wasn't running, that's ok
		if !strings.Contains(err.Error(), "not running") {
			return err
		}
	}

	return r.startMCPServer(name)
}

// ensureWMCPStarted lazily starts agent or daemon wmcp if not already running and mcp.use is enabled
func (r *REPL) ensureWMCPStarted() error {
	// Only auto-start if mcp.use is enabled
	if !r.configOptions.GetBool("mcp.use") {
		return nil
	}

	// If wmcp is already running, nothing to do
	if r.wmcpProcess != nil {
		return nil
	}

	// Try to start agent wmcp if configured
	if r.agentConfig != nil && len(r.agentConfig.MCPS) > 0 {
		if err := r.startAgentWMCP(); err != nil {
			return fmt.Errorf("failed to start agent wmcp: %v", err)
		}
		return nil
	}

	// Try to start daemon wmcp if configured
	if r.configOptions.GetBool("mcp.daemon") {
		var wmcpArgs []string
		if v := r.configOptions.Get("mcp.config"); v != "" {
			wmcpArgs = []string{"-c", v}
		} else if v := r.configOptions.Get("mcp.args"); v != "" {
			wmcpArgs = parseShellArgs(v)
		}

		if len(wmcpArgs) > 0 {
			listener, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				return fmt.Errorf("error finding random port for wmcp: %v", err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			r.wmcpPort = port
			os.Setenv("MAI_WMCP_BASEURL", fmt.Sprintf("localhost:%d", port))
			os.Setenv("MAI_TOOL_BASEURL", fmt.Sprintf("http://localhost:%d", port))
			wmcpArgs = append(wmcpArgs, "-b", fmt.Sprintf("localhost:%d", port))
			cmd := exec.Command("mai-wmcp", wmcpArgs...)
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("error starting wmcp: %v", err)
			}
			r.wmcpProcess = cmd
		}
	}

	return nil
}

// startAgentWMCP starts mai-wmcp with agent-specific configuration
func (r *REPL) startAgentWMCP() error {
	// Build agent-specific config with only the required MCP servers
	agentConfig := map[string]interface{}{
		"mcpServers": make(map[string]interface{}),
		"maiOptions": map[string]interface{}{
			"baseURL":        ":0", // Use port 0 for auto-assignment
			"yoloMode":       true,
			"nonInteractive": true,
		},
	}

	mcpServers := agentConfig["mcpServers"].(map[string]interface{})

	// Add only the agent's required MCP servers
	for _, mcpName := range r.agentConfig.MCPS {
		if server, exists := r.mcpConfig.Servers[mcpName]; exists {
			mcpServers[mcpName] = map[string]interface{}{
				"type":    "stdio",
				"command": server.Command,
				"args":    server.Args,
				"env":     server.Env,
			}
		} else {
			return fmt.Errorf("agent MCP server %s not found in config", mcpName)
		}
	}

	// Convert to JSON
	configJSON, err := json.Marshal(agentConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal agent config: %v", err)
	}

	// Set environment variable for wmcp
	os.Setenv("MAI_AGENT_CONFIG", string(configJSON))

	// Find available port for wmcp
	port, err := findAvailablePort(8989)
	if err != nil {
		return fmt.Errorf("failed to find available port for wmcp: %v", err)
	}

	// Start mai-wmcp with the agent config
	cmd := exec.Command("mai-wmcp", "-b", fmt.Sprintf("localhost:%d", port), "-n") // -n to skip config file loading
	cmd.Env = append(os.Environ(), fmt.Sprintf("MAI_AGENT_CONFIG=%s", string(configJSON)))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mai-wmcp: %v", err)
	}

	// Store the wmcp process
	r.wmcpProcess = cmd
	r.wmcpPort = port

	return nil
}

// generateMemory walks over all saved chat sessions, summarizes them using the memory prompt, and writes the consolidated memory file to the mai directory
func (r *REPL) generateMemory() error {
	maiDir, err := findMaiDir()
	if err != nil {
		return err
	}
	chatDir := filepath.Join(maiDir, "chats")
	files, err := os.ReadDir(chatDir)
	if err != nil {
		return fmt.Errorf("cannot read chat directory: %v", err)
	}

	var combined strings.Builder
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(chatDir, file.Name()))
		if err != nil {
			continue
		}
		var sess sessionData
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		sessionName := strings.TrimSuffix(file.Name(), ".json")
		combined.WriteString("Session: " + sessionName + "\n")
		for _, m := range sess.Messages {
			role := m.Role
			content := fmt.Sprintf("%v", m.Content)
			combined.WriteString(fmt.Sprintf("%s: %s\n", role, content))
		}
		combined.WriteString("\n---\n\n")
	}

	if combined.Len() == 0 {
		return fmt.Errorf("no conversation data found in %s", chatDir)
	}

	// Load memory prompt template
	promptPath, err := r.resolvePromptPath("memory.md")
	promptContent := ""
	if err == nil {
		if b, err := os.ReadFile(promptPath); err == nil {
			promptContent = string(b)
		}
	}

	client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	messages := []llm.Message{}
	if promptContent != "" {
		messages = append(messages, llm.Message{Role: "system", Content: promptContent})
	}
	messages = append(messages, llm.Message{Role: "user", Content: combined.String()})

	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to generate memory: %v", err)
	}

	memFile := filepath.Join(maiDir, "memory.txt")
	if err := os.WriteFile(memFile, []byte(response), 0644); err != nil {
		return fmt.Errorf("cannot write memory file: %v", err)
	}

	fmt.Printf("Memory written to %s\r\n", memFile)
	return nil
}

// getVDBContext executes mai-vdb with the configured directory and current message
// Returns the context output to be used as [CONTEXT] for the LLM
func (r *REPL) getVDBContext(message string) (string, error) {
	vdbDir := r.configOptions.Get("vdb.datadir")
	if vdbDir == "" {
		return "", fmt.Errorf("vdb.datadir not configured")
	}

	// Execute mai-vdb command
	vdbLimitNum, err := r.configOptions.GetNumber("vdb.limit")
	if err != nil {
		vdbLimitNum = 5 // fallback to default
	}
	vdbLimit := fmt.Sprintf("%.0f", vdbLimitNum)
	cmd := exec.Command("mai-vdb", "-s", vdbDir, "-n", vdbLimit, message)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to execute mai-vdb: %v", err)
	}

	return string(output), nil
}

// handleScriptCommand executes a script file containing REPL commands
func (r *REPL) handleScriptCommand(scriptPath string) error {
	// Expand ~ to home directory
	if strings.HasPrefix(scriptPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		scriptPath = filepath.Join(homeDir, scriptPath[1:])
	}

	// Read the script file
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script file: %v", err)
	}

	// Split into lines and execute each command
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		fmt.Printf("> %s\n", line)
		err := r.handleCommand(line, "", "")
		if err != nil {
			return fmt.Errorf("error executing command '%s': %v", line, err)
		}
	}

	return nil
}

func (r *REPL) substituteInput(input string) (string, error) {
	processedInput, err := ExecuteCommandSubstitution(input)
	if err != nil {
		return "", fmt.Errorf("command substitution failed: %v", err)
	}
	input = processedInput

	// Process backtick substitutions
	processedInput, err = ExecuteBacktickSubstitution(input, r)
	if err != nil {
		return "", fmt.Errorf("backtick substitution failed: %v", err)
	}
	input = processedInput

	// Process environment variable substitutions
	processedInput, err = ExecuteEnvVarSubstitution(input)
	if err != nil {
		return "", fmt.Errorf("environment variable substitution failed: %v", err)
	}
	input = processedInput

	// Process @mentions in the input
	enhancedInput := r.processAtMentions(input)

	/*
		// Process pending files and incorporate them into the input
		var images []string // For storing base64 encoded images for Ollama
		if len(r.pendingFiles) > 0 {
			// Add file contents to the input
			enhancedInput += "\n\n"

			for _, file := range r.pendingFiles {
				if strings.Contains(file.filePath, "://") {
					enhancedInput += fmt.Sprintf("URL Link: `%s`\n", file.filePath)
				} else if file.isImage {
					// For images, we'll collect them separately for providers that support image attachments
					images = append(images, file.imageB64)
					enhancedInput += fmt.Sprintf("[Image attached: %s]\n", filepath.Base(file.filePath))
				} else {
					// For regular files, add the content
					enhancedInput += fmt.Sprintf("File content from %s:\n```\n%s\n```\n\n",
						file.filePath, file.content)
				}
			}

			// Clear pending files after use
			r.pendingFiles = []pendingFile{}
		}
	*/
	input = enhancedInput

	return input, nil
}

// buildUserDetails creates a string with user context information
func (r *REPL) buildUserDetails() string {
	if !r.configOptions.GetBool("user.details") {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	if username == "" {
		username = "unknown"
	}

	osName := runtime.GOOS

	lang := r.configOptions.Get("user.lang")
	if lang == "" {
		lang = os.Getenv("LANG")
	}
	if lang == "" {
		lang = "unknown"
	}

	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04:05 MST")

	return fmt.Sprintf("Current Working Directory: %s\nUsername: %s\nOperating System: %s\nLanguage: %s\nCurrent Time/Date/Timezone: %s",
		cwd, username, osName, lang, timeStr)
}

func (r *REPL) sendToAI(input string, redirectType string, redirectTarget string, processSubstitutions bool, forceDisableStreaming bool) error {
	r.mu.Lock()
	r.isStreaming = redirectType == ""
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.isStreaming = false
		r.currentClient = nil
		r.mu.Unlock()
	}()

	if processSubstitutions {
		processedInput, err := r.substituteInput(input)
		if err != nil {
			return err
		}
		input = processedInput
	}

	// Create client
	client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}
	r.mu.Lock()
	r.currentClient = client
	r.mu.Unlock()

	// Add system prompt if present
	messages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}

	// Add Claude Skills metadata if available
	if skillsPrompt := r.buildSkillsPrompt(); skillsPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: skillsPrompt})
	}

	// Add user details if enabled
	if userDetails := r.buildUserDetails(); userDetails != "" {
		messages = append(messages, llm.Message{Role: "system", Content: "USER CONTEXT:\n" + userDetails})
	}

	// If memory option is enabled, load consolidated memory and include as system context
	if r.configOptions.GetBool("chat.memory") {
		maiDir, err := findMaiDir()
		if err == nil {
			memFile := filepath.Join(maiDir, "memory.txt")
			if b, err := os.ReadFile(memFile); err == nil && len(b) > 0 {
				messages = append(messages, llm.Message{Role: "system", Content: "MEMORY:\n" + string(b)})
			}
		}
	}

	var vdbContext string
	var vdbErr error
	// If vdb option is enabled, get context from vector database and include as system context
	if r.configOptions.GetBool("vdb.use") {
		vdbContext, vdbErr = r.getVDBContext(input)
		if vdbErr == nil && vdbContext != "" {
			messages = append(messages, llm.Message{Role: "user", Content: "[CONTEXT]\n" + vdbContext + "\n[/CONTEXT]"})
		} else if vdbErr != nil {
			fmt.Fprintf(os.Stderr, "VDB context error: %v\n", vdbErr)
		}
	}

	// Handle conversation history based on logging and reply settings
	if r.configOptions.GetBool("chat.log") {
		// When logging is enabled, use normal message history behavior
		if r.configOptions.GetBool("chat.replies") {
			// Include all messages
			messages = append(messages, r.messages...)
		} else {
			// Include only user messages
			for _, msg := range r.messages {
				if msg.Role == "user" {
					messages = append(messages, msg)
				} else {
					msg2 := msg
					msg2.Content = ""
					// include empty response from the llm
					messages = append(messages, msg2)
				}
			}
		}
	} else {
		// When logging is disabled, we don't append any previous messages
	}

	if r.configOptions.GetBool("mcp.use") {
		// Lazily start wmcp on first use if not already running
		if err := r.ensureWMCPStarted(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to start wmcp: %v\n", err)
		}

		StartTimer()
		tool, err := r.ReactLoop(messages, input)
		if err != nil {
			return fmt.Errorf("tool execution failed: %v", err)
		}
		input = tool
		fmt.Println("(tools) loop finished.")
		StopTimer()
	}

	// Before adding the user message, optionally auto-compact the conversation
	// when the `autocompact` option is enabled and the chat history is large.
	// If `autocompact` is non-zero and there are more than 5 messages, run
	// the compact operation which will replace the conversation with a
	// compact summary produced by the AI.
	if ac, err := r.configOptions.GetNumber("chat.autocompact"); err == nil {
		if ac != 0 && len(r.messages) > 5 {
			if err := r.handleCompactCommand(); err != nil {
				// Log compact errors but continue sending the message
				fmt.Fprintf(os.Stderr, "Auto-compact failed: %v\n", err)
			}
		}
	}

	// Add user message with enhanced input
	// Store the original input (with commands) for display in message history,
	// but use the processed input (with command output) for sending to the AI
	userMessage := llm.Message{Role: "user", Content: input}

	// Handle conversation history based on logging settings
	if r.configOptions.GetBool("chat.log") {
		// Save the user message to conversation history when logging is enabled.
		// NOTE: VDB context (when enabled) is included in the API call below
		// but must NOT be stored in the persistent chat log. Do not append
		// vdbContext to r.messages so it remains out of the saved conversation.
		r.messages = append(r.messages, userMessage)
	} else {
		// When logging is disabled, keep just the current user message in memory.
		// VDB context is still sent to the LLM but not stored in r.messages.
		r.messages = []llm.Message{userMessage}
	}

	// Set default topic from first user message for unsaved sessions
	if r.currentSession == "" && r.unsavedTopic == "" {
		// Use the first few words of the message as the session topic
		words := strings.Fields(userMessage.Content.(string))
		snippetWords := words
		if len(words) > 5 {
			snippetWords = words[:5]
		}
		r.unsavedTopic = strings.Join(snippetWords, " ")
	}

	// If reasoning is disabled, append /no_think to the last message sent to the LLM
	if !r.configOptions.GetBool("llm.think") && r.configOptions.GetBool("llm.rawmode") && !r.configOptions.GetBool("ui.think") {
		// Create a copy of the messages for the API call with /no_think appended
		messagesCopy := make([]llm.Message, len(messages))
		copy(messagesCopy, messages)

		disable_reasoning := "\n# Reasoning\nDo /nothink /no_think\nUse Reasoning: low\n\n"
		// Append the user message with /no_think to the copy
		messagesCopy = append(messagesCopy, llm.Message{Role: "user", Content: input + disable_reasoning})
		messages = messagesCopy
	} else {
		// Add the original user message
		messages = append(messages, userMessage)
	}

	// Do not start the demo animation here. The animation will be started
	// only when a streaming provider emits a <think> tag. This avoids
	// creating the scroller for responses that do not contain internal
	// reasoning blocks.

	// Send message with streaming based on REPL settings, but disable if redirected
	streamEnabled := r.configOptions.GetBool("llm.stream") && redirectType == "" && !forceDisableStreaming

	// Reset the markdown processor state before starting a new streaming session
	if streamEnabled && r.configOptions.GetBool("ui.markdown") {
		llm.ResetStreamRenderer()
	}

	var images []string // base64 encoded images

	for _, file := range r.pendingFiles {
		if file.isImage {
			images = append(images, file.imageB64)
		}
	}

	// If demo mode is active, let the LLM client notify the demo stop callback
	// as soon as the first streaming token arrives. We set the callback on the
	// client so it will be embedded into the request context used by providers.
	if r.configOptions.GetBool("ui.demo") && client != nil {
		client.SetResponseStopCallback(r.stopDemoCallback)
		defer client.SetResponseStopCallback(nil)

		// Also set demo callbacks so streaming parsers can emit tokens and phase
		// updates. The llm package exposes SetDemoPhaseCallback and
		// SetDemoTokenCallback which we can use here.
		llm.SetDemoPhaseCallback(func(phase string) {
			// Update the demo action label and ensure the scroller is running.
			if phase == "" {
				art.StopLoop()
			} else {
				art.StartLoop(phase)
			}
		})
		defer llm.SetDemoPhaseCallback(nil)

		llm.SetDemoTokenCallback(func(phase string, token string) {
			if !r.configOptions.GetBool("ui.demo") {
				return
			}
			// Feed text into the demo scroller; filtering/newline removal is
			// handled by llm.EmitDemoTokens
			art.FeedText(token)
		})
		defer llm.SetDemoTokenCallback(nil)
	}

	// Start the demo animation immediately; it will remain visible until
	// the first token arrives. If the first token is not a <think> tag the
	// streaming parser will invoke the stop callback to stop the animation.
	if r.configOptions.GetBool("ui.demo") && client != nil {
		art.StartLoop("Thinking...")
	}

	response, err := client.SendMessage(messages, streamEnabled, images, nil)

	// Stop the animation after SendMessage returns (for non-streaming)
	// For streaming, the animation will be stopped when the first token arrives.
	if r.configOptions.GetBool("ui.demo") && !streamEnabled {
		art.StopLoop()
	}

	// Handle the assistant's response based on logging settings
	if err == nil && response != "" {
		// Handle redirection
		if redirectType == "file" {
			// Write response to file
			err = os.WriteFile(redirectTarget, []byte(response), 0644)
			if err != nil {
				return fmt.Errorf("failed to write to file %s: %v", redirectTarget, err)
			}
			fmt.Printf("Response written to %s\r\n", redirectTarget)
		} else if redirectType == "pipe" {
			// Pipe response to command. Attach command stdout/stderr to the
			// current terminal so interactive tools (like `less`) can operate
			// normally. Write the AI response to the command's stdin.
			cmd := exec.Command("/bin/sh", "-c", redirectTarget)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			stdinPipe, err := cmd.StdinPipe()
			if err != nil {
				return fmt.Errorf("failed to create stdin pipe: %v", err)
			}

			// Start the command
			err = cmd.Start()
			if err != nil {
				return fmt.Errorf("failed to start command %s: %v", redirectTarget, err)
			}

			// Write the response into the command's stdin and close it
			_, err = io.WriteString(stdinPipe, response)
			_ = stdinPipe.Close()
			if err != nil {
				// If writing fails, still wait for process and return error
				_ = cmd.Wait()
				return fmt.Errorf("failed to write to command %s stdin: %v", redirectTarget, err)
			}

			// Wait for the command to finish
			err = cmd.Wait()
			if err != nil {
				return fmt.Errorf("command %s failed: %v", redirectTarget, err)
			}
		} else {
			// Normal output
			if !streamEnabled {
				// Handle <think> regions based on ui.think option
				out := response
				if !r.configOptions.GetBool("ui.think") {
					out = llm.FilterOutThinkForOutput(out)
					out = strings.TrimLeft(out, " \t\r\n")
				}
				if r.configOptions.GetBool("ui.markdown") {
					// Use markdown formatting
					fmt.Print(llm.RenderMarkdown(out))
				} else {
					// Use standard formatting
					fmt.Println(strings.ReplaceAll(out, "\n", "\r\n"))
				}
			}
		}

		// Create assistant message
		assistantMessage := llm.Message{Role: "assistant", Content: response}

		if r.configOptions.GetBool("chat.log") {
			// Save to conversation history when logging is enabled
			r.messages = append(r.messages, assistantMessage)
		} else {
			// When logging is disabled, keep just the current exchange
			r.messages = []llm.Message{userMessage, assistantMessage}
		}

		// Handle TTS if enabled
		if r.configOptions.GetBool("chat.tts") {
			voice := r.configOptions.Get("chat.ttsvoice")
			if voice == "" {
				voice = "Mónica"
			}
			Speak(response, voice)
		}

		// If followup is enabled, run the #followup prompt once asynchronously
		if r.configOptions.GetBool("chat.followup") {
			r.mu.Lock()
			if !r.followupInProgress {
				r.followupInProgress = true
				r.mu.Unlock()
				go func() {
					defer func() {
						r.mu.Lock()
						r.followupInProgress = false
						r.mu.Unlock()
					}()
					// Call the prompt handler for #followup; ignore errors but print them
					if err := r.handlePromptCommand("#followup"); err != nil {
						fmt.Printf("Followup error: %v\r\n", err)
					}
				}()
			} else {
				r.mu.Unlock()
			}
		}
	}

	// Ensure the demo animation is stopped before returning to the readline prompt
	art.StopLoop()

	// Use carriage return only so we don't create an extra blank line
	fmt.Print("\r")
	return err
}

// Legacy function kept for compatibility
func (r *REPL) supportsStreaming() bool {
	// Check if streaming mode is enabled in REPL
	if !r.configOptions.GetBool("llm.stream") {
		return false
	}
	// Check if API supports streaming
	provider := strings.ToLower(r.configOptions.Get("ai.provider"))
	return provider != "bedrock"
}

// Legacy function kept for compatibility
func (r *REPL) regularResponse(input string) error {
	// Create messages
	messages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}
	messages = append(messages, llm.Message{Role: "user", Content: input})

	// Create client and send message
	client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Print prompt for AI response
	fmt.Print("\r\nAI: ")

	// Send message without streaming
	_, err = client.SendMessage(messages, false, nil, nil)

	fmt.Print("\r\n")
	return err
}

// getLastAssistantReply returns the content of the last assistant reply in the conversation
func (r *REPL) getLastAssistantReply() (string, error) {
	// Iterate backwards through messages to find the last assistant message
	for i := len(r.messages) - 1; i >= 0; i-- {
		if r.messages[i].Role == "assistant" {
			return r.messages[i].Content.(string), nil
		}
	}
	return "", fmt.Errorf("no assistant replies found in conversation history")
}

// handleShellInput processes input starting with '$' as hybrid AI/shell mode
func (r *REPL) handleShellInput(input string) error {
	// Handle redirection first, before substitutions
	var redirectType, redirectTarget string
	if idx := strings.LastIndex(input, ">"); idx != -1 {
		redirectType = "file"
		redirectTarget = strings.TrimSpace(input[idx+1:])
		input = strings.TrimSpace(input[:idx])
	} else if idx := strings.LastIndex(input, "|"); idx != -1 {
		redirectType = "pipe"
		redirectTarget = strings.TrimSpace(input[idx+1:])
		input = strings.TrimSpace(input[:idx])
	}

	// Check if this is command mode (contains /) or AI mode
	if strings.Contains(input, "/") {
		// Command mode: execute commands and handle output
		// Process slash substitutions
		processedInput, err := ExecuteSlashSubstitution(input, r)
		if err != nil {
			return fmt.Errorf("slash substitution failed: %v", err)
		}
		input = processedInput

		// If pipe, execute the command on the current input
		if redirectType == "pipe" {
			cmd := exec.Command("/bin/sh", "-c", redirectTarget)
			cmd.Stdin = strings.NewReader(input)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err = cmd.Run()
			if err != nil {
				return fmt.Errorf("pipe command failed: %v", err)
			}
			input = ""
			redirectType = ""
		}

		// Check for backticks
		hasBackticks := strings.Contains(input, "`")

		// Process backtick substitutions
		processedInput, err = ExecuteBacktickSubstitution(input, r)
		if err != nil {
			return fmt.Errorf("backtick substitution failed: %v", err)
		}
		input = processedInput

		// If backticks, send to AI; otherwise, handle output directly
		if hasBackticks {
			return r.sendToAI(input, redirectType, redirectTarget, false, true)
		} else {
			if redirectType == "file" {
				err = os.WriteFile(redirectTarget, []byte(input), 0644)
				if err != nil {
					return fmt.Errorf("failed to write to file %s: %v", redirectTarget, err)
				}
				fmt.Printf("Output written to %s\r\n", redirectTarget)
			} else {
				fmt.Print(input)
			}
			return nil
		}
	} else {
		// AI mode: send input to AI with redirection on response
		// Process backtick substitutions
		processedInput, err := ExecuteBacktickSubstitution(input, r)
		if err != nil {
			return fmt.Errorf("backtick substitution failed: %v", err)
		}
		input = processedInput

		// Send to AI with redirection
		return r.sendToAI(input, redirectType, redirectTarget, false, true)
	}
}

// handleNormalInput processes regular input (not starting with '$')
func (r *REPL) handleNormalInput(input string) error {
	// Handle verbatim inputs
	if len(input) >= 2 {
		if input[0] == '\'' && input[len(input)-1] == '\'' {
			input = input[1 : len(input)-1]
		}
	}

	// For normal input, skip backtick processing
	return r.sendToAI(input, "", "", false, false)
}

// handleSlurpCommand reads from stdin until EOF (Ctrl+D) and returns the content
func (r *REPL) handleSlurpCommand() error {
	// Save the current terminal state
	oldState, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to get terminal state: %v", err)
	}

	// Restore the terminal to normal mode so we can read multiline text
	term.Restore(int(os.Stdin.Fd()), oldState)

	fmt.Println("Enter your text (press Ctrl+D when finished):")

	// Read from stdin until EOF
	var content strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		content.WriteString(scanner.Text())
		content.WriteString("\n")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		// Make terminal raw again
		MakeRawPreserveNewline(int(os.Stdin.Fd()))
		return fmt.Errorf("error reading input: %v", err)
	}

	// Make terminal raw again
	MakeRawPreserveNewline(int(os.Stdin.Fd()))

	// Get the content
	input := content.String()

	if input == "" {
		fmt.Println("No input provided.")
		return nil
	}

	// Send the input to the AI
	return r.sendToAI(input, "", "", true, false)
}

// initCommands initializes the command registry with all available commands

// handleTemplateSlashCommand handles the /template command for template filling
func (r *REPL) handleTemplateSlashCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("Usage: /template <file> [key=value ...] or /template <file> -  (use - as file to read template from stdin)\n\r")
	}

	templateFile := args[1]
	var keyValues []string
	interactive := false

	if len(args) > 2 {
		if args[2] == "-" {
			interactive = true
		} else {
			keyValues = args[2:]
		}
	}

	// Read template content
	var templateContent []byte
	var err error
	if templateFile == "-" {
		// Read from stdin
		templateContent, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read template from stdin: %v", err)
		}
	} else {
		// Read from file
		templatePath, err := r.resolveTemplatePath(templateFile)
		if err != nil {
			return fmt.Errorf("template file not found: %v", err)
		}

		templateContent, err = os.ReadFile(templatePath)
		if err != nil {
			return fmt.Errorf("failed to read template file: %v", err)
		}
	}

	// Parse key=value pairs
	vars := make(map[string]string)
	for _, kv := range keyValues {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid key=value format: %s", kv)
		}
		key, value := parts[0], parts[1]

		// Handle @file slurping
		if strings.HasPrefix(value, "@") {
			filePath := value[1:]
			if strings.HasPrefix(filePath, "~") {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home directory: %v", err)
				}
				filePath = filepath.Join(homeDir, filePath[1:])
			}
			content, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %v", filePath, err)
			}
			value = string(content)
		}

		vars[key] = value
	}

	// Find all {key} placeholders
	re := regexp.MustCompile(`\{([^}]+)\}`)
	matches := re.FindAllStringSubmatch(string(templateContent), -1)

	// Collect required variables
	requiredVars := make(map[string]bool)
	for _, match := range matches {
		requiredVars[match[1]] = true
	}

	// Check for missing variables
	var missingVars []string
	for varName := range requiredVars {
		if _, exists := vars[varName]; !exists {
			missingVars = append(missingVars, varName)
		}
	}

	// Handle missing variables
	if len(missingVars) > 0 {
		if interactive {
			// Interactive mode: prompt for missing vars
			prompt := r.configOptions.Get("repl.prompt")

			p := r.readline.defaultPrompt
			r.readline.defaultPrompt = "?"
			for _, varName := range missingVars {
				fmt.Printf("%s\n\r%s ", varName, prompt)
				response, err := r.readline.Read()
				fmt.Print("\033[0m")
				if err != nil {
					r.readline.defaultPrompt = p
					return fmt.Errorf("error reading input: %v", err)
				}
				vars[varName] = response
			}
			r.readline.defaultPrompt = p
		} else {
			// Not interactive: show error listing all required vars
			var allRequired []string
			for varName := range requiredVars {
				allRequired = append(allRequired, varName)
			}
			sort.Strings(allRequired)
			return fmt.Errorf("missing required template variables. All required variables: %s", strings.Join(allRequired, ", "))
		}
	}

	// Replace placeholders
	result := string(templateContent)
	for key, value := range vars {
		placeholder := "{" + key + "}"
		result = strings.ReplaceAll(result, placeholder, value)
	}

	// Send to AI
	return r.sendToAI(result, "", "", true, false)
}

// handlePromptCommand handles the # command for prompt expansion
func (r *REPL) handlePromptCommand(input string) error {
	// Split the input into command and arguments
	parts := strings.SplitN(input, " ", 2)
	promptName := parts[0][1:] // Remove the # prefix

	if promptName == "" {
		prompts, err := r.listPrompts()
		if err != nil {
			fmt.Printf("%v\r\n", err)
			return nil
		}
		fmt.Printf("Available prompts (use # followed by name):\r\n")
		for _, name := range prompts {
			fmt.Printf("  %s\r\n", name)
		}
		return nil
	}

	// Load the prompt file content and send to AI
	var extra string
	if len(parts) > 1 {
		extra = parts[1]
	}
	expandedInput, err := r.loadPrompt(promptName, extra)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}
	return r.sendToAI(expandedInput, "", "", true, false)
}

// executeLLMQueryWithoutStreaming executes an LLM query without streaming and returns the result
func (r *REPL) executeLLMQueryWithoutStreaming(query string) (string, error) {
	// Create a new client for this query
	client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Process the query similar to sendToAI but without streaming
	// Process command substitutions in the input
	processedQuery, err := ExecuteCommandSubstitution(query)
	if err != nil {
		return "", fmt.Errorf("command substitution failed: %v", err)
	}

	// Process environment variable substitutions
	processedQuery, err = ExecuteEnvVarSubstitution(processedQuery)
	if err != nil {
		return "", fmt.Errorf("environment variable substitution failed: %v", err)
	}

	// Build the messages array
	messages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}

	// Add conversation history if we should include replies
	if r.configOptions.GetBool("chat.replies") && len(r.messages) > 0 {
		messages = append(messages, r.messages...)
	}

	// Add the user query
	messages = append(messages, llm.Message{Role: "user", Content: processedQuery})

	// Call the LLM with streaming disabled
	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %v", err)
	}

	// Return the response
	return response, nil
}

// executeShellCommand executes a shell command and returns its output
func (r *REPL) executeShellCommand(cmdString string) error {
	// Trim leading/trailing whitespace
	cmdString = strings.TrimSpace(cmdString)
	if cmdString == "" {
		return nil
	}

	// Handle special case for cd command - change working directory
	if strings.HasPrefix(cmdString, "cd ") {
		dir := strings.TrimSpace(strings.TrimPrefix(cmdString, "cd "))
		// Expand ~ to home directory if present
		if strings.HasPrefix(dir, "~") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting home directory: %v\r\n", err)
				return nil
			}
			dir = filepath.Join(homeDir, dir[1:])
		}

		// Change directory
		err := os.Chdir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error changing directory: %v\r\n", err)
		} else {
			cwd, _ := os.Getwd()
			fmt.Printf("Changed directory to: %s\r\n", cwd)
		}
		return nil
	}

	// For other commands, run with inherited stdout/stderr
	cmd := exec.Command("sh", "-c", cmdString)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// displayConversationLog prints the current conversation messages
func (r *REPL) displayConversationLog() string {
	var output strings.Builder
	if len(r.messages) == 0 {
		output.WriteString("No conversation messages yet\r\n")
		return output.String()
	}

	output.WriteString("Conversation log:\r\n")
	output.WriteString("-----------------\r\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)

		output.WriteString(fmt.Sprintf("[%d] %s: ", i+1, role))

		// For log display, use a larger truncation limit
		content := msg.Content.(string)
		if len(content) > 100 {
			content = content[:97] + "..."
		}

		// Replace newlines with space for compact display
		content = strings.ReplaceAll(content, "\n", " ")

		output.WriteString(fmt.Sprintf("%s\r\n", content))
	}

	output.WriteString(fmt.Sprintf("Total messages: %d\r\n", len(r.messages)))
	output.WriteString(fmt.Sprintf("Settings: replies=%t, streaming=%t, reasoning=%t, logging=%t\r\n",
		r.configOptions.GetBool("chat.replies"),
		r.configOptions.GetBool("llm.stream"),
		r.configOptions.GetBool("llm.think"),
		r.configOptions.GetBool("chat.log")))

	// Display pending files if any
	if len(r.pendingFiles) > 0 {
		output.WriteString("\r\nPending files for next message:\r\n")
		imageCount := 0
		fileCount := 0

		for _, file := range r.pendingFiles {
			if file.isImage {
				imageCount++
				output.WriteString(fmt.Sprintf(" - Image: %s\r\n", file.filePath))
			} else {
				fileCount++
				output.WriteString(fmt.Sprintf(" - File: %s\r\n", file.filePath))
			}
		}

		output.WriteString(fmt.Sprintf("Total pending: %d images, %d files\r\n", imageCount, fileCount))
	}
	return output.String()
}

// displayFullConversationLog prints the complete conversation without truncating or filtering
func (r *REPL) displayFullConversationLog() string {
	var output strings.Builder
	if len(r.messages) == 0 {
		output.WriteString("No conversation messages yet\r\n")
		return output.String()
	}

	output.WriteString("# Full conversation log:\r\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)

		output.WriteString(fmt.Sprintf("\r\n## [%d] %s:\r\n", i+1, role))

		// Print the full content with preserved formatting
		// Apply markdown rendering if enabled
		if r.configOptions.GetBool("ui.markdown") {
			output.WriteString(fmt.Sprintf("%s\r\n", llm.RenderMarkdown(msg.Content.(string))))
		} else {
			// Replace single newlines with \r\n for proper terminal display
			content := strings.ReplaceAll(msg.Content.(string), "\n", "\r\n")
			output.WriteString(fmt.Sprintf("%s\r\n", content))
		}
		output.WriteString("--------------------\r\n")
	}

	output.WriteString(fmt.Sprintf("\r\nTotal messages: %d\r\n", len(r.messages)))
	return output.String()
}

// undoLastMessage removes the last message from the conversation history
func (r *REPL) undoLastMessage() {
	if len(r.messages) == 0 {
		fmt.Print("No messages to undo\r\n")
		return
	}
	// Remove the last message
	r.messages = r.messages[:len(r.messages)-1]
}

// undoMessageByIndex removes a specific message by its 1-based index
func (r *REPL) undoMessageByIndex(indexStr string) {
	if len(r.messages) == 0 {
		fmt.Print("No messages to undo\r\n")
		return
	}

	// Parse the index
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		fmt.Printf("Invalid index: %s. Please provide a number.\r\n", indexStr)
		return
	}

	// Convert from 1-based (displayed to user) to 0-based (array index)
	index--

	// Check if the index is valid
	if index < 0 || index >= len(r.messages) {
		fmt.Printf("Invalid index: %d. Valid range is 1-%d.\r\n", index+1, len(r.messages))
		return
	}

	// Get the message being removed for display
	msg := r.messages[index]
	role := formatRole(msg.Role)
	content := truncateContent(msg.Content.(string))

	// Remove the message using slice operations
	r.messages = append(r.messages[:index], r.messages[index+1:]...)

	fmt.Printf("Removed message %d (%s: %s)\r\n", index+1, role, content)
	fmt.Printf("Remaining messages: %d\r\n", len(r.messages))
}

// Helper function to format role for display
func formatRole(role string) string {
	if len(role) > 0 {
		return strings.ToUpper(role[:1]) + role[1:]
	}
	return role
}

// Helper function to truncate and format content for display
func truncateContent(content string) string {
	if len(content) > 30 {
		content = content[:27] + "..."
	}
	return strings.ReplaceAll(content, "\n", " ")
}

// extractAtMentionFilenames scans input text for @filename mentions,
// supporting path separators and escaped spaces in filenames.
func extractAtMentionFilenames(input string) []string {
	var filenames []string
	r := []rune(input)
	for i := 0; i < len(r); i++ {
		if r[i] == '@' {
			i++
			var sb strings.Builder
			for i < len(r) {
				if r[i] == '\\' && i+1 < len(r) && r[i+1] == ' ' {
					sb.WriteRune(' ')
					i += 2
				} else if unicode.IsSpace(r[i]) {
					break
				} else {
					sb.WriteRune(r[i])
					i++
				}
			}
			if sb.Len() > 0 {
				filenames = append(filenames, sb.String())
			}
		}
	}
	return filenames
}

// processAtMentions extracts words starting with @ from input text,
// checks if they correspond to existing files, and returns the enhanced prompt
func (r *REPL) processAtMentions(input string) string {
	filenames := extractAtMentionFilenames(input)
	if len(filenames) == 0 {
		return input // No @mentions found, return original input
	}

	// Process each @mention
	var fileContents []string
	var processedFiles []string

	for _, filename := range filenames {
		// Check if the file exists in the current directory
		if _, err := os.Stat(filename); err == nil {
			// File exists, read its content
			content, err := os.ReadFile(filename)
			if err == nil {
				// Format the content with markdown
				fileContent := fmt.Sprintf("\n\n## File: %s\n\n```\n%s\n```", filename, string(content))
				fileContents = append(fileContents, fileContent)
				processedFiles = append(processedFiles, filename)
			}
		}
	}

	// If no valid files were found, return the original input
	if len(fileContents) == 0 {
		return input
	}

	// Notify the user about processed @mentions
	if len(processedFiles) > 0 {
		fmt.Print("\r\nProcessed @mentions: ")
		for i, file := range processedFiles {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s", file)
		}
		fmt.Print("\r\n")
	}

	// Append all file contents to the original input
	enhancedInput := input
	for _, content := range fileContents {
		enhancedInput += content
	}

	return enhancedInput
}

// processIncludeStatements processes include directives in prompt content.
// Lines starting with '@' are treated as file paths to include.
func (r *REPL) processIncludeStatements(content, baseDir string) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@") {
			incPath := strings.TrimSpace(trimmed[1:])
			target := incPath
			if !filepath.IsAbs(incPath) && baseDir != "" {
				target = filepath.Join(baseDir, incPath)
			}
			if data, err := os.ReadFile(target); err != nil {
				fmt.Fprintf(os.Stderr, "Error including file %s: %v\n", target, err)
				out = append(out, line)
			} else {
				out = append(out, string(data))
			}
		} else {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// autoDetectPromptDir attempts to find a prompts directory relative to the executable path
// and sets the promptdir config variable if found
func (r *REPL) autoDetectPromptDir() {
	r.autoDetectDirectory("dir.prompt", "share/mai/prompts", true)
}

// showCurrentModel displays the current model based on the provider
func (r *REPL) showCurrentModel() {
	provider := r.configOptions.Get("ai.provider")
	model := r.configOptions.Get("ai.model")
	if provider == "" {
		fmt.Printf("Current model: %s\r\n", model)
		return
	}
	fmt.Printf("Current model: %s (provider: %s)\r\n", model, provider)
}

// setModel changes the model for the current provider
func (r *REPL) setModel(model string) error {
	r.configOptions.Set("ai.model", model)
	return nil
}

// getModel returns the model and provider for a specific task
// If the task-specific model contains "@", it splits into model@provider
// Otherwise, it uses the default ai.provider
func (r *REPL) getModel(task string) (model, provider string) {
	var modelKey string
	switch task {
	case "embed":
		modelKey = "ai.model.embed"
	case "compact":
		modelKey = "ai.model.compact"
	case "tool":
		modelKey = "ai.model.tool"
	default:
		modelKey = "ai.model"
	}

	modelValue := r.configOptions.Get(modelKey)
	if modelValue == "" {
		// Fallback to default model
		modelValue = r.configOptions.Get("ai.model")
	}

	// Check if model contains "@" to specify provider
	if strings.Contains(modelValue, "@") {
		parts := strings.SplitN(modelValue, "@", 2)
		model = parts[0]
		provider = parts[1]
	} else {
		model = modelValue
		provider = r.configOptions.Get("ai.provider")
	}

	return model, provider
}

// showCurrentProvider displays the current provider
func (r *REPL) showCurrentProvider() {
	fmt.Printf("Current provider: %s\r\n", r.configOptions.Get("ai.provider"))
	// Also show the current model for this provider
	r.showCurrentModel()
}

// isProviderAvailable checks if a provider is available by creating a temporary config and provider instance
func (r *REPL) isProviderAvailable(provider string) bool {
	// Create a temporary config for this provider
	cfg := loadConfig()
	// Apply current options but override the provider
	applyConfigOptionsToLLMConfig(cfg, &r.configOptions)
	cfg.PROVIDER = provider

	// Create provider instance
	prov, err := llm.CreateProvider(cfg, context.Background())
	if err != nil {
		return false
	}

	// Check if provider implements IsAvailable
	if availableProvider, ok := prov.(interface{ IsAvailable() bool }); ok {
		return availableProvider.IsAvailable()
	}

	// Fallback: assume available if we can create the provider
	return true
}

// listProviders displays all available providers
func (r *REPL) listProviders() (string, error) {
	providers := llm.GetValidProvidersList()
	currentInput := r.configOptions.Get("ai.provider")
	currentProvider := strings.ToLower(currentInput)
	if canonical, ok := llm.CanonicalProviderName(currentInput); ok {
		currentProvider = canonical
	}

	var output strings.Builder
	output.WriteString("Available providers:\r\n")
	for _, provider := range providers {
		// Check if provider is available
		isAvailable := r.isProviderAvailable(provider)
		var emoji string
		if isAvailable {
			emoji = "\033[92m✅\033[0m" // Green checkmark
		} else {
			emoji = "\033[91m❌\033[0m" // Red X
		}

		if provider == currentProvider {
			output.WriteString(fmt.Sprintf("%s * %s (current)\r\n", emoji, provider))
		} else {
			output.WriteString(fmt.Sprintf("%s   %s\r\n", emoji, provider))
		}
	}

	output.WriteString("\r\nUse '/set ai.provider <name>' to change the current provider\r\n")
	return output.String(), nil
}

// setProvider changes the current provider
func (r *REPL) setProvider(provider string) error {
	canonical, ok := llm.CanonicalProviderName(provider)
	if !ok {
		fmt.Fprintf(os.Stderr, "Invalid provider: %s\n", provider)
		fmt.Fprintf(os.Stderr, "Valid providers: %s\n", llm.GetValidProvidersDisplay())
		return nil
	}

	// Update the provider in the configOptions
	r.configOptions.Set("ai.provider", canonical)

	// Prints removed to avoid interfering with MCP protocol

	return nil
}

// resolveDefaultModelForProvider returns the provider's default model using current settings
func (r *REPL) resolveDefaultModelForProvider(provider string) string {
	cfg := r.buildLLMConfig()
	cfg.PROVIDER = provider
	client, err := llm.NewLLMClient(cfg, context.Background())
	if err != nil || client == nil {
		return ""
	}
	return client.DefaultModel()
}

// listModels fetches and displays available models for the current provider
func (r *REPL) listModels() (string, error) {
	var output strings.Builder

	// Create client
	client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %v", err)
	}

	output.WriteString(fmt.Sprintf("Fetching available models for %s...\r\n", r.configOptions.Get("ai.provider")))

	// Get models from the provider
	models, err := client.ListModels()
	if err != nil {
		return "", fmt.Errorf("failed to fetch models: %v", err)
	}

	if len(models) == 0 {
		output.WriteString("No models available for this provider\r\n")
		return output.String(), nil
	}

	// Display models
	output.WriteString(fmt.Sprintf("Available %s models:\r\n", r.configOptions.Get("ai.provider")))
	output.WriteString("-----------------------\r\n")

	// Get current model for highlighting
	currentModel := r.getCurrentModelForProvider()

	// Format and display each model
	for i, model := range models {
		// Add indicator for current model
		current := ""
		if model.ID == currentModel {
			current = " (current)"
		}

		// Display model with description if available
		if model.Description != "" {
			output.WriteString(fmt.Sprintf("[%d] %s%s - %s\r\n", i+1, model.ID, current, model.Description))
		} else {
			output.WriteString(fmt.Sprintf("[%d] %s%s\r\n", i+1, model.ID, current))
		}
	}

	output.WriteString(fmt.Sprintf("Total models: %d\r\n", len(models)))
	output.WriteString("Use '/set ai.model <model-id>' to change the model\r\n")

	return output.String(), nil
}

// getCurrentModelForProvider returns the current model ID for the active provider
func (r *REPL) getCurrentModelForProvider() string {
	return r.configOptions.Get("ai.model")
}

// handleCompactCommand processes the /compact command
// It loads the compact.txt prompt and submits the entire conversation history
// to the AI, then replaces all messages with the AI's response

func (r *REPL) handleCompactCommand() error {
	// Check if there are enough messages to compact
	if len(r.messages) < 2 {
		fmt.Print("Not enough messages to compact. Need at least one exchange.\r\n")
		return nil
	}

	// Try to find the compact prompt using resolvePromptPath
	promptPath, err := r.resolvePromptPath("compact")
	if err != nil {
		return fmt.Errorf("failed to find compact prompt: %v", err)
	}

	// Load the compact prompt from file
	compactPrompt, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("failed to read compact prompt: %v", err)
	}

	// Create a serialized version of the conversation for the AI
	var conversationText strings.Builder
	conversationText.WriteString("# Conversation History\n\n")

	for i, msg := range r.messages {
		role := formatRole(msg.Role)
		conversationText.WriteString(fmt.Sprintf("## %s %d:\n\n%s\n\n", role, i+1, msg.Content.(string)))
	}

	// Create a new message with the compact prompt and conversation history
	compactMessage := llm.Message{
		Role:    "user",
		Content: string(compactPrompt) + "\n\n" + conversationText.String(),
	}

	// Save original messages for recovery if needed
	originalMessages := r.messages

	// Replace messages with just the compact message
	r.messages = []llm.Message{compactMessage}

	fmt.Print("Compacting conversation...\r\n")

	// Create client and send message
	client, err := llm.NewLLMClient(r.buildLLMConfigForTask("compact"), r.ctx)
	if err != nil {
		// Restore original messages on error
		r.messages = originalMessages
		return fmt.Errorf("failed to create LLM client: %v", err)
	}

	// Prepare messages for the API
	apiMessages := []llm.Message{}
	if sp := r.currentSystemPrompt(); sp != "" {
		apiMessages = append(apiMessages, llm.Message{Role: "system", Content: sp})
	}
	apiMessages = append(apiMessages, compactMessage)

	// Send the message to the AI (non-streaming mode for this operation)
	response, err := client.SendMessage(apiMessages, false, nil, nil)
	if err != nil {
		// Restore original messages on error
		r.messages = originalMessages
		return fmt.Errorf("failed to compact conversation: %v", err)
	}

	// Create the assistant response message
	assistantMessage := llm.Message{Role: "assistant", Content: response}

	// Replace the conversation with just the compact message and response
	r.messages = []llm.Message{
		llm.Message{Role: "user", Content: "Please provide a compact response to my questions and needs."},
		assistantMessage,
	}

	fmt.Print("Conversation compacted successfully.\r\n")

	return nil
}

// handleToolCommand executes the mai-tool command with the given arguments
func (r *REPL) handleToolCommand(args []string) (string, error) {
	if len(args) < 2 {
		tools, err := GetAvailableToolsWithStatus(r.configOptions, r.agentConfig)
		if err != nil {
			return "", err
		}
		return tools + "\n", nil
	}
	// Execute mai-tool directly with the provided arguments
	cmd := exec.Command("mai-tool", args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mai-tool execution failed: %v\n%s", err, string(output))
	}
	return string(output), nil
}

// saveConversation saves the current conversation to a JSON file
func (r *REPL) saveConversation(path string) error {
	// Expand ~ to home directory if present
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Create simplified conversation data struct
	conversationData := struct {
		SystemPrompt string        `json:"system_prompt,omitempty"`
		Messages     []llm.Message `json:"messages"`
	}{
		SystemPrompt: r.currentSystemPrompt(),
		Messages:     r.messages,
	}

	// Convert to JSON
	jsonData, err := json.MarshalIndent(conversationData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation data: %v", err)
	}

	// Write to file
	if err := os.WriteFile(path, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write conversation to file: %v", err)
	}

	fmt.Printf("Conversation saved to %s (%d messages)\r\n", path, len(r.messages))
	return nil
}

// loadConversation loads a conversation from a JSON file
func (r *REPL) loadConversation(path string) error {
	// Expand ~ to home directory if present
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read conversation file: %v", err)
	}

	// Try to parse as the current format first
	var conversationData struct {
		SystemPrompt string        `json:"system_prompt"`
		Messages     []llm.Message `json:"messages"`
	}

	if err := json.Unmarshal(data, &conversationData); err != nil {
		// Try parsing legacy format that included provider and model
		var legacyData struct {
			SystemPrompt string        `json:"system_prompt"`
			Messages     []llm.Message `json:"messages"`
			Provider     string        `json:"provider"`
			Model        string        `json:"model"`
		}

		if err := json.Unmarshal(data, &legacyData); err != nil {
			return fmt.Errorf("failed to parse conversation file: %v", err)
		}

		// Copy data from legacy format
		conversationData.SystemPrompt = legacyData.SystemPrompt
		conversationData.Messages = legacyData.Messages
	}

	// Update REPL with loaded data
	if conversationData.SystemPrompt != "" {
		_ = r.configOptions.Set("llm.systemprompt", conversationData.SystemPrompt)
	} else {
		r.configOptions.Unset("llm.systemprompt")
	}
	r.messages = conversationData.Messages

	fmt.Printf("Conversation loaded from %s (%d messages)\r\n", path, len(r.messages))
	if conversationData.SystemPrompt != "" {
		fmt.Print("System prompt loaded\r\n")
	}
	return nil
}

/// utils

var startTime time.Time

func StartTimer() {
	startTime = time.Now()
}

func StopTimer() {
	elapsed := time.Since(startTime)
	minutes := int(elapsed.Minutes())      // Get the elapsed minutes
	seconds := int(elapsed.Seconds()) % 60 // Get the remaining seconds
	fmt.Printf("⏳ Elapsed time: %d minutes and %d seconds\n", minutes, seconds)
}
