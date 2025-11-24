package mcplib

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// loadAuthTokens loads bearer tokens from the auth file
func (s *MCPServer) loadAuthTokens() error {
	data, err := os.ReadFile(s.authFile)
	if err != nil {
		return fmt.Errorf("failed to read auth file: %v", err)
	}
	s.authTokens = make(map[string]bool)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		token := strings.TrimSpace(line)
		if token != "" {
			s.authTokens[token] = true
		}
	}
	return nil
}

// ServeHTTP starts an HTTP server on the specified port with optional Bearer authentication
func (s *MCPServer) ServeHTTP(port string, basePath string, authEnabled bool, authFile string) error {
	s.authEnabled = authEnabled
	s.authFile = authFile
	if authEnabled {
		if err := s.loadAuthTokens(); err != nil {
			return err
		}
	}
	if basePath == "" {
		basePath = "/"
	}
	http.HandleFunc(basePath, s.httpHandler)
	log.Printf("Starting HTTP server on port %s with base path %s", port, basePath)
	return http.ListenAndServe(":"+port, nil)
}

// ServeSSE starts an HTTP server with Server-Sent Events support for MCP
func (s *MCPServer) ServeSSE(port string, basePath string, authEnabled bool, authFile string) error {
	s.authEnabled = authEnabled
	s.authFile = authFile
	if authEnabled {
		if err := s.loadAuthTokens(); err != nil {
			return err
		}
	}
	if basePath == "" {
		basePath = "/"
	}

	// SSE endpoint
	ssePath := basePath + "/sse"
	mcpPath := basePath + "/mcp"

	http.HandleFunc(ssePath, s.sseHandler)
	http.HandleFunc(mcpPath, s.sseMCPHandler)

	log.Printf("Starting SSE server on port %s with SSE path %s and MCP path %s", port, ssePath, mcpPath)
	return http.ListenAndServe(":"+port, nil)
}

// sseHandler handles SSE connections
func (s *MCPServer) sseHandler(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !s.authTokens[token] {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Get session ID from query parameter
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "Missing sessionId parameter", http.StatusBadRequest)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")

	// Create a channel for this connection
	respChan := make(chan JSONRPCResponse, 100)
	s.sseConnections[sessionID] = respChan

	// Clean up on disconnect
	defer func() {
		delete(s.sseConnections, sessionID)
		close(respChan)
	}()

	// Send endpoint event
	endpointEvent := fmt.Sprintf("event: endpoint\ndata: /mcp\n\n")
	if _, err := w.Write([]byte(endpointEvent)); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Listen for responses to send
	for resp := range respChan {
		respData, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		event := fmt.Sprintf("event: message\ndata: %s\n\n", respData)
		if _, err := w.Write([]byte(event)); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// sseMCPHandler handles MCP requests over HTTP for SSE connections
func (s *MCPServer) sseMCPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authEnabled {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !s.authTokens[token] {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Get session ID from header
	sessionID := r.Header.Get("X-SSE-Session-ID")
	if sessionID == "" {
		// For non-SSE requests, respond directly
		resp := s.processRequest(req)
		if resp.ID == nil {
			// Notification, no response
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respData, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respData)
		return
	}

	// For SSE connections, send response through the channel
	resp := s.processRequest(req)
	if resp.ID != nil {
		if respChan, exists := s.sseConnections[sessionID]; exists {
			select {
			case respChan <- resp:
			default:
				// Channel full, drop the response
			}
		}
	}

	// Acknowledge the request
	w.WriteHeader(http.StatusAccepted)
}

// httpHandler handles HTTP requests for MCP
func (s *MCPServer) httpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authEnabled {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !s.authTokens[token] {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	resp := s.processRequest(req)
	if resp.ID == nil {
		// Notification, no response
		w.WriteHeader(http.StatusNoContent)
		return
	}
	respData, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}
