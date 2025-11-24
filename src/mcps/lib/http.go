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
