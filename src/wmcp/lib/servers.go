package wmcplib

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

// StartServer starts an MCP server process or connects to HTTP endpoint
func (s *MCPService) StartServer(name, command string) error {
	return s.StartServerWithEnvAndTools(name, command, nil, nil, false)
}

// StartServerWithEnv starts an MCP server process with custom environment variables or connects to HTTP endpoint
func (s *MCPService) StartServerWithEnv(name, command string, env map[string]string) error {
	return s.StartServerWithEnvAndTools(name, command, env, nil, false)
}

// StartServerWithEnvAndTools starts an MCP server process with custom environment variables and tool filtering
func (s *MCPService) StartServerWithEnvAndTools(name, command string, env map[string]string, enabledTools map[string]bool, sessionMode bool) error {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	isHTTP := strings.HasPrefix(command, "http://") || strings.HasPrefix(command, "https://")
	isSSE := strings.HasPrefix(command, "sse://") || strings.HasPrefix(command, "sses://")

	if isHTTP && strings.HasSuffix(command, "/sse") {
		isSSE = true
		isHTTP = false
	}

	if isHTTP || isSSE {
		server := &MCPServer{
			Name:          name,
			Command:       command,
			URL:           command,
			IsHTTP:        isHTTP,
			IsSSE:         isSSE,
			Tools:         []Tool{},
			Prompts:       []Prompt{},
			Resources:     []Resource{},
			EnabledTools:  enabledTools,
			UseSession:    sessionMode,
			stderrDone:    make(chan struct{}),
			stderrActive:  false,
			monitorDone:   make(chan struct{}),
			monitorActive: false,
		}

		s.Servers[name] = server

		server.sseResponseChan = make(chan *JSONRPCResponse, 10)
		server.sseRequestID = make(chan string, 1)

		if isSSE {
			endpointURL, err := s.connectSSE(server)
			if err != nil {
				delete(s.Servers, name)
				return fmt.Errorf("failed to connect to SSE server: %v", err)
			}
			if server.UseSession && strings.Contains(endpointURL, "session_id=") {
				u, _ := url.Parse(endpointURL)
				if u != nil {
					sessionID := u.Query().Get("session_id")
					if sessionID != "" {
						server.SessionID = sessionID
						debugLog(s.DebugMode, "Extracted session ID: %s", sessionID)
					}
				}
			}
			if strings.HasPrefix(endpointURL, "/") {
				baseURL := server.URL
				if strings.HasSuffix(baseURL, "/sse") {
					baseURL = strings.TrimSuffix(baseURL, "/sse")
				} else if strings.HasPrefix(baseURL, "sse://") {
					baseURL = strings.Replace(baseURL, "sse://", "http://", 1)
				} else if strings.HasPrefix(baseURL, "sses://") {
					baseURL = strings.Replace(baseURL, "sses://", "https://", 1)
				}
				endpointURL = baseURL + endpointURL
			}
			server.URL = endpointURL
			server.IsHTTP = true
			server.IsSSE = false
			server.SSEConnected = true

			go s.listenSSEResponses(server)
		}

		if err := s.InitializeServer(server); err != nil {
			delete(s.Servers, name)
			return fmt.Errorf("failed to initialize server: %v", err)
		}

		if err := s.loadTools(server); err != nil {
			log.Printf("Warning: failed to load tools for server %s: %v", name, err)
		}

		if !s.NoPrompts {
			if err := s.loadPrompts(server); err != nil {
				log.Printf("Warning: failed to load prompts for server %s: %v", name, err)
			}
		}

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

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	if len(env) > 0 {
		cmdEnv := os.Environ()
		for key, value := range env {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", key, value))
		}
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
		EnabledTools:  enabledTools,
		UseSession:    sessionMode,
		stderrDone:    make(chan struct{}),
		stderrActive:  true,
		monitorDone:   make(chan struct{}),
		monitorActive: true,
	}

	s.Servers[name] = server

	go s.handleStderr(server)
	go s.monitorServer(server)

	if err := s.InitializeServer(server); err != nil {
		s.stopServer(server)
		delete(s.Servers, name)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for server %s: %v", name, err)
	}

	if !s.NoPrompts {
		if err := s.loadPrompts(server); err != nil {
			log.Printf("Warning: failed to load prompts for server %s: %v", name, err)
		}
	}

	if err := s.loadResources(server); err != nil {
		log.Printf("Warning: failed to load resources for server %s: %v", name, err)
	}

	log.Printf("Started MCP server: %s", name)
	return nil
}

// InitializeServer performs the MCP handshake
func (s *MCPService) InitializeServer(server *MCPServer) error {
	clientCapabilities := map[string]interface{}{
		"experimental": map[string]interface{}{},
		"prompts":      map[string]interface{}{"listChanged": false},
		"resources":    map[string]interface{}{"subscribe": false, "listChanged": false},
		"tools":        map[string]interface{}{"listChanged": false},
	}

	initRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]interface{}{"name": "mai-wmcp", "version": MaiVersion},
			"capabilities":    clientCapabilities,
		},
		ID: "1",
	}

	response, err := s.SendRequest(server, initRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("initialization failed: %v", response.Error)
	}

	initNotification := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  map[string]interface{}{},
	}

	if server.IsHTTP {
		reqBytes, _ := json.Marshal(initNotification)
		httpReq, err := http.NewRequest("POST", server.URL, bytes.NewBuffer(reqBytes))
		if err != nil {
			return fmt.Errorf("failed to create notification request: %v", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json, text/event-stream")

		if server.UseSession {
			server.Mutex.RLock()
			sessionID := server.SessionID
			server.Mutex.RUnlock()
			if sessionID != "" {
				httpReq.Header.Set("Mcp-Session-Id", sessionID)
			}
		}

		if token := s.GetBearerToken(server); token != "" {
			httpReq.Header.Set("Authorization", "Bearer "+token)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(httpReq)
		if err != nil {
			return fmt.Errorf("notification request failed: %v", err)
		}
		if server.UseSession {
			if newSessionID := resp.Header.Get("Mcp-Session-Id"); newSessionID != "" {
				server.Mutex.Lock()
				server.SessionID = newSessionID
				server.Mutex.Unlock()
			}
		}
		resp.Body.Close()
	} else {
		reqBytes, _ := json.Marshal(initNotification)
		server.Stdin.Write(reqBytes)
		server.Stdin.Write([]byte("\n"))
	}

	return nil
}

// loadTools loads available tools from the server
func (s *MCPService) loadTools(server *MCPServer) error {
	toolsRequest := JSONRPCRequest{JSONRPC: "2.0", Method: "tools/list", Params: map[string]interface{}{}, ID: "2"}

	response, err := s.SendRequest(server, toolsRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return fmt.Errorf("tools/list failed: %v", response.Error)
	}

	resultBytes, _ := json.Marshal(response.Result)
	var toolsResult ToolsListResult
	if err := json.Unmarshal(resultBytes, &toolsResult); err != nil {
		return fmt.Errorf("failed to parse tools response: %v", err)
	}

	for i := range toolsResult.Tools {
		tool := &toolsResult.Tools[i]
		tool.Parameters = ExtractParametersFromSchema(tool.InputSchema)
	}

	var filteredTools []Tool
	if len(server.EnabledTools) == 0 {
		filteredTools = toolsResult.Tools
	} else {
		for _, tool := range toolsResult.Tools {
			if enabled, exists := server.EnabledTools[tool.Name]; exists && enabled {
				filteredTools = append(filteredTools, tool)
			}
		}
	}

	server.Mutex.Lock()
	server.Tools = filteredTools
	server.Mutex.Unlock()

	log.Printf("Loaded %d tools for server %s", len(filteredTools), server.Name)
	return nil
}

// loadPrompts loads available prompts from the server
func (s *MCPService) loadPrompts(server *MCPServer) error {
	promptsRequest := JSONRPCRequest{JSONRPC: "2.0", Method: "prompts/list", Params: map[string]interface{}{}, ID: "3"}

	response, err := s.SendRequest(server, promptsRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		log.Printf("prompts/list failed on %s: %v", server.Name, response.Error)
		return nil
	}

	resultBytes, _ := json.Marshal(response.Result)
	var list PromptsListResult
	if err := json.Unmarshal(resultBytes, &list); err != nil {
		return fmt.Errorf("failed to parse prompts response: %v", err)
	}

	server.Mutex.Lock()
	server.Prompts = list.Prompts
	server.Mutex.Unlock()

	log.Printf("Loaded %d prompts for server %s", len(list.Prompts), server.Name)
	return nil
}

// loadResources loads available resources from the server
func (s *MCPService) loadResources(server *MCPServer) error {
	resourcesRequest := JSONRPCRequest{JSONRPC: "2.0", Method: "resources/list", Params: map[string]interface{}{}, ID: "4"}

	response, err := s.SendRequest(server, resourcesRequest)
	if err != nil {
		return err
	}

	if response.Error != nil {
		log.Printf("resources/list failed on %s: %v", server.Name, response.Error)
		return nil
	}

	resultBytes, _ := json.Marshal(response.Result)
	var list ResourcesListResult
	if err := json.Unmarshal(resultBytes, &list); err != nil {
		return fmt.Errorf("failed to parse resources response: %v", err)
	}

	server.Mutex.Lock()
	server.Resources = list.Resources
	server.Mutex.Unlock()

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
		err := server.Process.Wait()
		if !server.monitorActive {
			break
		}

		if err != nil {
			log.Printf("ERROR: MCP server '%s' crashed: %v", server.Name, err)
		} else {
			log.Printf("ERROR: MCP server '%s' exited unexpectedly", server.Name)
		}

		time.Sleep(1 * time.Second)

		log.Printf("Restarting MCP server '%s'...", server.Name)
		if restartErr := s.restartServer(server); restartErr != nil {
			log.Printf("ERROR: Failed to restart MCP server '%s': %v", server.Name, restartErr)
		} else {
			log.Printf("Successfully restarted MCP server '%s'", server.Name)
		}
	}
	close(server.monitorDone)
}

// restartServer restarts a crashed MCP server
func (s *MCPService) restartServer(server *MCPServer) error {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	server.stderrActive = false
	server.monitorActive = false

	if server.Stdin != nil {
		server.Stdin.Close()
	}
	if server.Stdout != nil {
		server.Stdout.Close()
	}
	if server.Stderr != nil {
		server.Stderr.Close()
	}

	<-server.stderrDone
	<-server.monitorDone

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

	server.stderrDone = make(chan struct{})
	server.monitorDone = make(chan struct{})

	server.Process = cmd
	server.Stdin = stdin
	server.Stdout = stdout
	server.Stderr = stderr

	server.stderrActive = true
	server.monitorActive = true

	go s.handleStderr(server)
	go s.monitorServer(server)

	if err := s.InitializeServer(server); err != nil {
		s.stopServer(server)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for restarted server %s: %v", server.Name, err)
	}

	if !s.NoPrompts {
		if err := s.loadPrompts(server); err != nil {
			log.Printf("Warning: failed to load prompts for restarted server %s: %v", server.Name, err)
		}
	}

	if err := s.loadResources(server); err != nil {
		log.Printf("Warning: failed to load resources for restarted server %s: %v", server.Name, err)
	}

	return nil
}

// stopServer stops an MCP server
func (s *MCPService) stopServer(server *MCPServer) {
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

		<-server.stderrDone
		<-server.monitorDone
	}
}

// StopAllServers stops all MCP servers
func (s *MCPService) StopAllServers() {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	for name, server := range s.Servers {
		s.stopServer(server)
		log.Printf("Stopped MCP server: %s", name)
	}
}

// connectSSE establishes an SSE connection and returns the endpoint URL
func (s *MCPService) connectSSE(server *MCPServer) (string, error) {
	sseURL := strings.Replace(server.URL, "sse://", "http://", 1)
	sseURL = strings.Replace(sseURL, "sses://", "https://", 1)

	baseURL := sseURL
	if strings.HasSuffix(sseURL, "/sse") {
		baseURL = strings.TrimSuffix(sseURL, "/sse")
		sseURL = baseURL + "/sse"
	}

	server.SSEURL = sseURL

	debugLog(s.DebugMode, "Connecting to SSE endpoint: %s", sseURL)

	req, err := http.NewRequest("GET", sseURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create SSE request: %v", err)
	}

	if token := s.GetBearerToken(server); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		debugLog(s.DebugMode, "Using bearer token for SSE connection")
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("SSE connection failed: %v", err)
	}

	server.Stdout = resp.Body

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		debugLog(s.DebugMode, "SSE line: %s", line)

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if strings.HasPrefix(data, "{\"endpoint\":\"") {
				var eventData map[string]string
				if err := json.Unmarshal([]byte(data), &eventData); err != nil {
					return "", fmt.Errorf("failed to parse SSE endpoint data: %v", err)
				}
				if endpoint, ok := eventData["endpoint"]; ok {
					debugLog(s.DebugMode, "Received SSE endpoint: %s", endpoint)
					return endpoint, nil
				}
			} else if strings.HasPrefix(data, "/") {
				debugLog(s.DebugMode, "Received SSE endpoint: %s", data)
				return data, nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading SSE stream: %v", err)
	}

	return "", fmt.Errorf("no endpoint received from SSE server")
}

// sendHTTPRequestViaSSE sends a request via HTTP and gets response from SSE stream
func (s *MCPService) sendHTTPRequestViaSSE(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	requestID := fmt.Sprintf("%v", request.ID)

	debugLog(s.DebugMode, "Sending SSE request to %s (ID: %s): %s", server.URL, requestID, string(reqBytes))

	httpReq, err := http.NewRequest("POST", server.URL, bytes.NewBuffer(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	if server.UseSession {
		server.Mutex.RLock()
		sessionID := server.SessionID
		server.Mutex.RUnlock()
		if sessionID != "" {
			httpReq.Header.Set("Mcp-Session-Id", sessionID)
			httpReq.Header.Set("X-SSE-Session-ID", sessionID)
		}
	}

	if token := s.GetBearerToken(server); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
		debugLog(s.DebugMode, "Using bearer token for %s", server.URL)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}

	respBytes, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	debugLog(s.DebugMode, "HTTP response status: %d, body: %s", resp.StatusCode, string(respBytes))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(respBytes, &response); err != nil {
		debugLog(s.DebugMode, "Direct response parse failed, waiting for SSE response")

		timeout := 30 * time.Second
		select {
		case response := <-server.sseResponseChan:
			debugLog(s.DebugMode, "Received SSE response for ID %v", response.ID)
			return response, nil
		case <-time.After(timeout):
			return nil, fmt.Errorf("timeout waiting for SSE response, HTTP body: %s", string(respBytes))
		}
	}

	return &response, nil
}

// listenSSEResponses listens for responses from the already-established SSE connection
func (s *MCPService) listenSSEResponses(server *MCPServer) {
	if server.Stdout == nil {
		log.Printf("ERROR: SSE listener: no SSE connection available")
		return
	}

	debugLog(s.DebugMode, "SSE listener using existing connection, waiting for responses")

	scanner := bufio.NewScanner(server.Stdout)
	var dataBuffer bytes.Buffer
	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if dataBuffer.Len() > 0 {
				dataBuffer.WriteByte('\n')
			}
			dataBuffer.WriteString(data)
		}

		if line == "" && dataBuffer.Len() > 0 {
			payload := dataBuffer.String()
			dataBuffer.Reset()

			debugLog(s.DebugMode, "SSE listener received event=%s data=%s", currentEvent, payload)

			if currentEvent == "message" || currentEvent == "" {
				var response JSONRPCResponse
				if err := json.Unmarshal([]byte(payload), &response); err != nil {
					if currentEvent == "message" {
						debugLog(s.DebugMode, "SSE listener: failed to unmarshal message: %v", err)
					}
					if currentEvent == "" && strings.HasPrefix(payload, "{") {
						debugLog(s.DebugMode, "SSE listener: failed to unmarshal: %v", err)
					}
					continue
				}

				if response.JSONRPC != "" {
					select {
					case server.sseResponseChan <- &response:
						debugLog(s.DebugMode, "SSE listener: sent response for ID %v", response.ID)
					default:
						debugLog(s.DebugMode, "SSE listener: channel full, dropped response")
					}
				}
			} else if currentEvent == "endpoint" {
				if server.UseSession && strings.Contains(payload, "session_id=") {
					u, _ := url.Parse(payload)
					if u != nil {
						newSessionID := u.Query().Get("session_id")
						if newSessionID != "" && newSessionID != server.SessionID {
							server.Mutex.Lock()
							server.SessionID = newSessionID
							server.Mutex.Unlock()
							debugLog(s.DebugMode, "SSE listener: updated session ID to %s", newSessionID)
						}
					}
				}
			}
			currentEvent = ""
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("ERROR: SSE listener scanner error: %v", err)
	}
}

// sendHTTPRequest sends a JSONRPC request to an HTTP MCP server
func (s *MCPService) sendHTTPRequest(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	if server.SSEConnected && server.sseResponseChan != nil {
		return s.sendHTTPRequestViaSSE(server, request)
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	debugLog(s.DebugMode, "Sending HTTP request to %s: %s", server.URL, string(reqBytes))

	httpReq, err := http.NewRequest("POST", server.URL, bytes.NewBuffer(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	if server.UseSession {
		server.Mutex.RLock()
		sessionID := server.SessionID
		server.Mutex.RUnlock()
		if sessionID != "" {
			httpReq.Header.Set("Mcp-Session-Id", sessionID)
			httpReq.Header.Set("X-SSE-Session-ID", sessionID)
		}
	}

	if token := s.GetBearerToken(server); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
		debugLog(s.DebugMode, "Using bearer token for %s", server.URL)
	} else {
		debugLog(s.DebugMode, "No bearer token found for %s", server.URL)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	if server.UseSession {
		if newSessionID := resp.Header.Get("Mcp-Session-Id"); newSessionID != "" {
			server.Mutex.Lock()
			server.SessionID = newSessionID
			server.Mutex.Unlock()
		}
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		var dataBuffer bytes.Buffer

		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				if dataBuffer.Len() > 0 {
					dataBuffer.WriteByte('\n')
				}
				dataBuffer.WriteString(strings.TrimPrefix(line, "data: "))
			}

			if line == "" && dataBuffer.Len() > 0 {
				payload := dataBuffer.String()
				debugLog(s.DebugMode, "Received HTTP SSE payload from %s: %s", server.URL, payload)

				var response JSONRPCResponse
				if err := json.Unmarshal([]byte(payload), &response); err != nil {
					return nil, fmt.Errorf("failed to unmarshal SSE response: %v", err)
				}

				return &response, nil
			}
		}

		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("failed to read SSE response: %v", err)
		}

		if dataBuffer.Len() > 0 {
			payload := dataBuffer.String()
			debugLog(s.DebugMode, "Received HTTP SSE payload from %s: %s", server.URL, payload)

			var response JSONRPCResponse
			if err := json.Unmarshal([]byte(payload), &response); err != nil {
				return nil, fmt.Errorf("failed to unmarshal SSE response: %v", err)
			}

			return &response, nil
		}

		return nil, fmt.Errorf("no data in SSE response")
	}

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	debugLog(s.DebugMode, "Received HTTP response from %s: %s", server.URL, string(respBytes))

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

	u, err := url.Parse(server.URL)
	if err != nil {
		return ""
	}

	domain := strings.ReplaceAll(u.Host, ".", "_")
	domain = strings.ReplaceAll(domain, "-", "_")
	envVar := "MAI_MCP_AUTH_" + strings.ToUpper(domain)

	return os.Getenv(envVar)
}
