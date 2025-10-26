package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GetServerNameFromCommand extracts server name from the command string
func GetServerNameFromCommand(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}

	firstPart := parts[0]
	serverName := firstPart

	if idx := strings.LastIndex(firstPart, "/"); idx != -1 {
		serverName = firstPart[idx+1:]
	}

	return serverName
}

// StartServer starts an MCP server process or connects to HTTP endpoint
func (s *MCPService) StartServer(name, command string) error {
	return s.StartServerWithEnv(name, command, nil)
}

// StartServerWithEnv starts an MCP server process with custom environment variables or connects to HTTP endpoint
func (s *MCPService) StartServerWithEnv(name, command string, env map[string]string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check if this is an HTTP or SSE server
	isHTTP := strings.HasPrefix(command, "http://") || strings.HasPrefix(command, "https://")
	isSSE := strings.HasPrefix(command, "sse://") || strings.HasPrefix(command, "sses://")

	if isHTTP || isSSE {
		// HTTP or SSE server
		server := &MCPServer{
			Name:          name,
			Command:       command,
			URL:           command,
			IsHTTP:        isHTTP,
			IsSSE:         isSSE,
			Tools:         []Tool{},
			Prompts:       []Prompt{},
			Resources:     []Resource{},
			stderrDone:    make(chan struct{}),
			stderrActive:  false, // no stderr for HTTP/SSE
			monitorDone:   make(chan struct{}),
			monitorActive: false, // no monitoring for HTTP/SSE
		}

		s.servers[name] = server

		// For SSE servers, first establish SSE connection to get endpoint URL
		if isSSE {
			endpointURL, err := s.connectSSE(server)
			if err != nil {
				delete(s.servers, name)
				return fmt.Errorf("failed to connect to SSE server: %v", err)
			}
			server.URL = endpointURL
			server.IsHTTP = true // Now treat as HTTP server
			server.IsSSE = false
		}

		// Initialize the server (handshake)
		if err := s.InitializeServer(server); err != nil {
			delete(s.servers, name)
			return fmt.Errorf("failed to initialize server: %v", err)
		}

		// Load tools
		if err := s.loadTools(server); err != nil {
			log.Printf("Warning: failed to load tools for server %s: %v", name, err)
		}

		// Load prompts (best-effort) unless disabled
		if !s.noPrompts {
			if err := s.loadPrompts(server); err != nil {
				log.Printf("Warning: failed to load prompts for server %s: %v", name, err)
			}
		}

		// Load resources (best-effort)
		if err := s.loadResources(server); err != nil {
			log.Printf("Warning: failed to load resources for server %s: %v", name, err)
		}

		if isSSE {
			log.Printf("Connected to SSE MCP server: %s", name)
		} else {
			log.Printf("Connected to HTTP MCP server: %s", name)
		}
		return nil
	}

	// Stdio server
	// Parse command string
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	// Apply custom environment variables if provided
	if env != nil && len(env) > 0 {
		// Start with current environment
		cmdEnv := os.Environ()

		// Add or override with custom variables
		for key, value := range env {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", key, value))
		}

		// Set the environment for the command
		cmd.Env = cmdEnv
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %v", err)
	}

	server := &MCPServer{
		Name:          name,
		Command:       command,
		IsHTTP:        false,
		Process:       cmd,
		Stdin:         stdin,
		Stdout:        stdout,
		Stderr:        stderr,
		Tools:         []Tool{},
		stderrDone:    make(chan struct{}),
		stderrActive:  true,
		monitorDone:   make(chan struct{}),
		monitorActive: true,
	}

	s.servers[name] = server

	// Start a goroutine to handle stderr output
	go s.handleStderr(server)

	// Start a goroutine to monitor the server process
	go s.monitorServer(server)

	// Initialize the server (handshake)
	if err := s.InitializeServer(server); err != nil {
		s.stopServer(server)
		delete(s.servers, name)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	// Load tools
	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for server %s: %v", name, err)
	}

	// Load prompts (best-effort) unless disabled
	if !s.noPrompts {
		if err := s.loadPrompts(server); err != nil {
			log.Printf("Warning: failed to load prompts for server %s: %v", name, err)
		}
	}

	// Load resources (best-effort)
	if err := s.loadResources(server); err != nil {
		log.Printf("Warning: failed to load resources for server %s: %v", name, err)
	}

	log.Printf("Started MCP server: %s", name)
	return nil
}

// InitializeServer performs the MCP handshake
func (s *MCPService) InitializeServer(server *MCPServer) error {
	initRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{},
				"prompts":   map[string]interface{}{},
				"resources": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    "mai-wmcp",
				"version": MaiVersion,
			},
		},
		ID: 1,
	}

	response, err := s.sendRequest(server, initRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("initialization failed: %v", response.Error)
	}

	// Send initialized notification
	initNotification := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]interface{}{},
	}

	// Send notification (no response expected)
	reqBytes, _ := json.Marshal(initNotification)
	server.Stdin.Write(reqBytes)
	server.Stdin.Write([]byte("\n"))

	return nil
}

// loadTools loads available tools from the server
func (s *MCPService) loadTools(server *MCPServer) error {
	toolsRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		Params:  map[string]interface{}{},
		ID:      2,
	}

	response, err := s.sendRequest(server, toolsRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("tools/list failed: %v", response.Error)
	}

	// Parse tools response
	resultBytes, _ := json.Marshal(response.Result)
	var toolsResult ToolsListResult
	if err := json.Unmarshal(resultBytes, &toolsResult); err != nil {
		return fmt.Errorf("failed to parse tools response: %v", err)
	}

	// Process tool parameters
	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		tool.Parameters = extractParametersFromSchema(tool.InputSchema)
	}

	server.mutex.Lock()
	server.Tools = toolsResult.Tools
	server.mutex.Unlock()

	log.Printf("Loaded %d tools for server %s", len(toolsResult.Tools), server.Name)
	return nil
}

// loadPrompts loads available prompts from the server
func (s *MCPService) loadPrompts(server *MCPServer) error {
	promptsRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "prompts/list",
		Params:  map[string]interface{}{},
		ID:      3,
	}

	response, err := s.sendRequest(server, promptsRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		// Not all servers implement prompts; don't treat as fatal
		log.Printf("prompts/list failed on %s: %v", server.Name, response.Error)
		return nil
	}

	resultBytes, _ := json.Marshal(response.Result)
	var list PromptsListResult
	if err := json.Unmarshal(resultBytes, &list); err != nil {
		return fmt.Errorf("failed to parse prompts response: %v", err)
	}

	server.mutex.Lock()
	server.Prompts = list.Prompts
	server.mutex.Unlock()

	log.Printf("Loaded %d prompts for server %s", len(list.Prompts), server.Name)
	return nil
}

// loadResources loads available resources from the server
func (s *MCPService) loadResources(server *MCPServer) error {
	resourcesRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "resources/list",
		Params:  map[string]interface{}{},
		ID:      4,
	}

	response, err := s.sendRequest(server, resourcesRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		// Not all servers implement resources; don't treat as fatal
		log.Printf("resources/list failed on %s: %v", server.Name, response.Error)
		return nil
	}

	resultBytes, _ := json.Marshal(response.Result)
	var list ResourcesListResult
	if err := json.Unmarshal(resultBytes, &list); err != nil {
		return fmt.Errorf("failed to parse resources response: %v", err)
	}

	server.mutex.Lock()
	server.Resources = list.Resources
	server.mutex.Unlock()

	log.Printf("Loaded %d resources for server %s", len(list.Resources), server.Name)
	return nil
}

// handleStderr reads from the stderr pipe and logs all messages
func (s *MCPService) handleStderr(server *MCPServer) {
	scanner := bufio.NewScanner(server.Stderr)
	for server.stderrActive && scanner.Scan() {
		text := scanner.Text()
		log.Printf("[%s stderr] %s", server.Name, text)
	}
	close(server.stderrDone)
}

// monitorServer monitors the server process and restarts it if it crashes
func (s *MCPService) monitorServer(server *MCPServer) {
	for server.monitorActive {
		// Wait for the process to exit
		err := server.Process.Wait()
		if !server.monitorActive {
			break
		}

		// Process has exited, log the error
		if err != nil {
			log.Printf("ERROR: MCP server '%s' crashed: %v", server.Name, err)
		} else {
			log.Printf("ERROR: MCP server '%s' exited unexpectedly", server.Name)
		}

		// Wait 1 second before restarting
		time.Sleep(1 * time.Second)

		// Restart the server
		log.Printf("Restarting MCP server '%s'...", server.Name)
		if restartErr := s.restartServer(server); restartErr != nil {
			log.Printf("ERROR: Failed to restart MCP server '%s': %v", server.Name, restartErr)
			// Continue monitoring in case we can restart later
		} else {
			log.Printf("Successfully restarted MCP server '%s'", server.Name)
		}
	}
	close(server.monitorDone)
}

// restartServer restarts a crashed MCP server
func (s *MCPService) restartServer(server *MCPServer) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Stop existing goroutines and close pipes
	server.stderrActive = false
	server.monitorActive = false

	// Close existing pipes if they exist
	if server.Stdin != nil {
		server.Stdin.Close()
	}
	if server.Stdout != nil {
		server.Stdout.Close()
	}
	if server.Stderr != nil {
		server.Stderr.Close()
	}

	// Wait for goroutines to finish
	<-server.stderrDone
	<-server.monitorDone

	// Parse command string
	parts := strings.Fields(server.Command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %v", err)
	}

	// Recreate channels
	server.stderrDone = make(chan struct{})
	server.monitorDone = make(chan struct{})

	// Update server with new process and pipes
	server.Process = cmd
	server.Stdin = stdin
	server.Stdout = stdout
	server.Stderr = stderr

	// Reset monitoring flags
	server.stderrActive = true
	server.monitorActive = true

	// Start new goroutines for stderr and monitoring
	go s.handleStderr(server)
	go s.monitorServer(server)

	// Re-initialize the server (handshake)
	if err := s.InitializeServer(server); err != nil {
		s.stopServer(server)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	// Re-load tools
	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for restarted server %s: %v", server.Name, err)
	}

	// Re-load prompts unless disabled
	if !s.noPrompts {
		if err := s.loadPrompts(server); err != nil {
			log.Printf("Warning: failed to load prompts for restarted server %s: %v", server.Name, err)
		}
	}

	// Re-load resources
	if err := s.loadResources(server); err != nil {
		log.Printf("Warning: failed to load resources for restarted server %s: %v", server.Name, err)
	}

	return nil
}

// stopServer stops an MCP server
func (s *MCPService) stopServer(server *MCPServer) {
	// Mark handlers as inactive
	server.stderrActive = false
	server.monitorActive = false

	if !server.IsHTTP {
		if server.Process != nil {
			server.Process.Process.Kill()
			server.Process.Wait()
		}
		if server.Stdin != nil {
			server.Stdin.Close()
		}
		if server.Stdout != nil {
			server.Stdout.Close()
		}
		if server.Stderr != nil {
			server.Stderr.Close()
		}

		// Wait for goroutines to finish
		<-server.stderrDone
		<-server.monitorDone
	}
}

// StopAllServers stops all MCP servers
func (s *MCPService) StopAllServers() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for name, server := range s.servers {
		s.stopServer(server)
		log.Printf("Stopped MCP server: %s", name)
	}
}

// connectSSE establishes an SSE connection and returns the endpoint URL
func (s *MCPService) connectSSE(server *MCPServer) (string, error) {
	// Convert sse:// to http:// for the connection
	sseURL := strings.Replace(server.URL, "sse://", "http://", 1)
	sseURL = strings.Replace(sseURL, "sses://", "https://", 1)

	debugLog(s.debugMode, "Connecting to SSE endpoint: %s", sseURL)

	req, err := http.NewRequest("GET", sseURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create SSE request: %v", err)
	}

	// Add bearer token if available
	if token := s.GetBearerToken(server); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		debugLog(s.debugMode, "Using bearer token for SSE connection")
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("SSE connection failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SSE connection failed with status %d", resp.StatusCode)
	}

	// Read SSE events to find the endpoint
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		debugLog(s.debugMode, "SSE line: %s", line)

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if strings.HasPrefix(data, "{\"endpoint\":\"") {
				// Parse the endpoint from the JSON data
				var eventData map[string]string
				if err := json.Unmarshal([]byte(data), &eventData); err != nil {
					return "", fmt.Errorf("failed to parse SSE endpoint data: %v", err)
				}
				if endpoint, ok := eventData["endpoint"]; ok {
					debugLog(s.debugMode, "Received SSE endpoint: %s", endpoint)
					return endpoint, nil
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading SSE stream: %v", err)
	}

	return "", fmt.Errorf("no endpoint received from SSE server")
}

// sendHTTPRequest sends a JSONRPC request to an HTTP MCP server
func (s *MCPService) sendHTTPRequest(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	debugLog(s.debugMode, "Sending HTTP request to %s: %s", server.URL, string(reqBytes))

	httpReq, err := http.NewRequest("POST", server.URL, bytes.NewBuffer(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Add bearer token if available
	if token := s.GetBearerToken(server); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
		debugLog(s.debugMode, "Using bearer token for %s", server.URL)
	} else {
		debugLog(s.debugMode, "No bearer token found for %s", server.URL)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	debugLog(s.debugMode, "Received HTTP response from %s: %s", server.URL, string(respBytes))

	var response JSONRPCResponse
	if err := json.Unmarshal(respBytes, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return &response, nil
}

// GetBearerToken gets the bearer token for an HTTP server from environment variables
func (s *MCPService) GetBearerToken(server *MCPServer) string {
	if !server.IsHTTP {
		return ""
	}

	// Parse the URL to get the domain
	u, err := url.Parse(server.URL)
	if err != nil {
		return ""
	}

	// Sanitize domain: replace dots and hyphens with underscores, uppercase
	domain := strings.ReplaceAll(u.Host, ".", "_")
	domain = strings.ReplaceAll(domain, "-", "_")
	envVar := "MAI_MCP_AUTH_" + strings.ToUpper(domain)

	return os.Getenv(envVar)
}
