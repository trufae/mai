package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// JSONRPC structures
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      interface{} `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// MCP Tool structures
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []Content `json:"content"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCP Server represents a running MCP server process
type MCPServer struct {
	Name    string
	Command string
	Process *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.ReadCloser
	Stderr  io.ReadCloser
	Tools   []Tool
	mutex   sync.RWMutex
}

// MCPService manages multiple MCP servers
type MCPService struct {
	servers map[string]*MCPServer
	mutex   sync.RWMutex
}

func NewMCPService() *MCPService {
	return &MCPService{
		servers: make(map[string]*MCPServer),
	}
}

// StartServer starts an MCP server process
func (s *MCPService) StartServer(name, command string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Parse command string
	parts := strings.Fields(command)
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

	server := &MCPServer{
		Name:    name,
		Command: command,
		Process: cmd,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		Tools:   []Tool{},
	}

	s.servers[name] = server

	// Initialize the server (handshake)
	if err := s.initializeServer(server); err != nil {
		s.stopServer(server)
		delete(s.servers, name)
		return fmt.Errorf("failed to initialize server: %v", err)
	}

	// Load tools
	if err := s.loadTools(server); err != nil {
		log.Printf("Warning: failed to load tools for server %s: %v", name, err)
	}

	log.Printf("Started MCP server: %s", name)
	return nil
}

// initializeServer performs the MCP handshake
func (s *MCPService) initializeServer(server *MCPServer) error {
	initRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    "mcp-rest-bridge",
				"version": "1.0.0",
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

	server.mutex.Lock()
	server.Tools = toolsResult.Tools
	server.mutex.Unlock()

	log.Printf("Loaded %d tools for server %s", len(toolsResult.Tools), server.Name)
	return nil
}

// sendRequest sends a JSONRPC request to the server and returns the response
func (s *MCPService) sendRequest(server *MCPServer, request JSONRPCRequest) (*JSONRPCResponse, error) {
	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Send request
	if _, err := server.Stdin.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("failed to write request: %v", err)
	}
	if _, err := server.Stdin.Write([]byte("\n")); err != nil {
		return nil, fmt.Errorf("failed to write newline: %v", err)
	}

	// Read response
	scanner := bufio.NewScanner(server.Stdout)
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read response")
	}

	var response JSONRPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	return &response, nil
}

// stopServer stops an MCP server
func (s *MCPService) stopServer(server *MCPServer) {
	if server.Process != nil {
		server.Process.Process.Kill()
		server.Process.Wait()
	}
	server.Stdin.Close()
	server.Stdout.Close()
	server.Stderr.Close()
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

// HTTP Handlers

// listToolsHandler returns all tools from all servers
func (s *MCPService) listToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")
	
	var output strings.Builder
	output.WriteString("# MCP Tools\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))

		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("**Description:** %s\n\n", tool.Description))
			
			if tool.InputSchema != nil {
				schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
				output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))
			}
			
			output.WriteString(fmt.Sprintf("**Usage:** `POST /tools/%s/%s`\n\n", serverName, tool.Name))
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// callToolHandler calls a specific tool on a specific server
func (s *MCPService) callToolHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverName := vars["server"]
	toolName := vars["tool"]

	s.mutex.RLock()
	server, exists := s.servers[serverName]
	s.mutex.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	// Parse arguments
	var arguments map[string]interface{}
	
	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			// Parse JSON body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			
			if len(body) > 0 {
				if err := json.Unmarshal(body, &arguments); err != nil {
					http.Error(w, "Invalid JSON in request body", http.StatusBadRequest)
					return
				}
			}
		} else {
			// Parse form data
			r.ParseForm()
			arguments = make(map[string]interface{})
			for key, values := range r.Form {
				if len(values) == 1 {
					// Try to parse as number, otherwise keep as string
					if num, err := strconv.ParseFloat(values[0], 64); err == nil {
						arguments[key] = num
					} else if b, err := strconv.ParseBool(values[0]); err == nil {
						arguments[key] = b
					} else {
						arguments[key] = values[0]
					}
				} else {
					arguments[key] = values
				}
			}
		}
	} else if r.Method == "GET" {
		// Parse query parameters
		arguments = make(map[string]interface{})
		for key, values := range r.URL.Query() {
			if len(values) == 1 {
				// Try to parse as number, otherwise keep as string
				if num, err := strconv.ParseFloat(values[0], 64); err == nil {
					arguments[key] = num
				} else if b, err := strconv.ParseBool(values[0]); err == nil {
					arguments[key] = b
				} else {
					arguments[key] = values[0]
				}
			} else {
				arguments[key] = values
			}
		}
	}

	// Create tool call request
	toolRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
		ID: time.Now().UnixNano(),
	}

	response, err := s.sendRequest(server, toolRequest)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to call tool: %v", err), http.StatusInternalServerError)
		return
	}

	if response.Error != nil {
		http.Error(w, fmt.Sprintf("Tool call failed: %v", response.Error), http.StatusBadRequest)
		return
	}

	// Parse and format response
	w.Header().Set("Content-Type", "text/plain")
	
	resultBytes, _ := json.Marshal(response.Result)
	var toolResult CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
		// Fallback to raw JSON if parsing fails
		w.Write(resultBytes)
		return
	}

	// Format content as markdown/plaintext
	var output strings.Builder
	for i, content := range toolResult.Content {
		if i > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(content.Text)
	}

	w.Write([]byte(output.String()))
}

// statusHandler returns the status of all servers
func (s *MCPService) statusHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")
	
	var output strings.Builder
	output.WriteString("# MCP Service Status\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString(fmt.Sprintf("Status: Running\n"))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: ./mcpd \"command1\" \"command2\" ...")
		fmt.Println("Example: ./mcpd \"r2pm -r r2mcp\" \"timemcp\"")
		os.Exit(1)
	}

	service := NewMCPService()
	
	// Ensure cleanup on exit
	defer service.StopAllServers()

	// Start all MCP servers
	for i, command := range os.Args[1:] {
		serverName := fmt.Sprintf("server%d", i+1)
		if err := service.StartServer(serverName, command); err != nil {
			log.Printf("Failed to start server %s: %v", serverName, err)
			continue
		}
	}

	// Setup HTTP routes
	router := mux.NewRouter()
	
	// List all tools
	router.HandleFunc("/tools", service.listToolsHandler).Methods("GET")
	
	// Get service status
	router.HandleFunc("/status", service.statusHandler).Methods("GET")
	
	// Call a specific tool
	router.HandleFunc("/tools/{server}/{tool}", service.callToolHandler).Methods("GET", "POST")
	
	// Root endpoint with usage info
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		usage := `# MCP REST Bridge

Available endpoints:

- GET /status - Service status
- GET /tools - List all available tools
- GET /tools/{server}/{tool}?param=value - Call tool with query parameters
- POST /tools/{server}/{tool} - Call tool with JSON body or form data

Examples:
- curl http://localhost:8080/tools
- curl http://localhost:8080/tools/server1/mytool?arg1=value1
- curl -X POST http://localhost:8080/tools/server1/mytool -H "Content-Type: application/json" -d '{"arg1":"value1"}'
`
		w.Write([]byte(usage))
	}).Methods("GET")

	// Start HTTP server
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	log.Printf("Starting MCP REST service on port %s", port)
	log.Printf("Access tools at: http://localhost:%s/tools", port)
	
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal("Failed to start HTTP server:", err)
	}
}
