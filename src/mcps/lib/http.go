package mcplib

import (
	"context"
	"crypto/subtle"
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
	if strings.TrimSpace(s.authFile) == "" {
		s.authTokens = make(map[string]bool)
		return nil
	}
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

func (s *MCPServer) isValidToken(candidate string) bool {
	if len(s.authTokens) == 0 {
		return false
	}

	match := 0
	candidateBytes := []byte(candidate)
	for token := range s.authTokens {
		match |= subtle.ConstantTimeCompare([]byte(token), candidateBytes)
	}
	return match == 1
}

func sameToken(a string, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *MCPServer) authorizeToken(ctx context.Context, rawToken string) (*AuthResult, error) {
	if !s.authEnabled {
		authResult, err := s.authenticate(ctx, rawToken)
		if err != nil {
			return nil, fmt.Errorf("unauthorized")
		}
		if authResult == nil {
			return nil, fmt.Errorf("unauthorized")
		}
		return authResult, nil
	}

	if len(s.authTokens) > 0 && !s.isValidToken(rawToken) {
		return nil, fmt.Errorf("unauthorized")
	}

	if len(s.authTokens) == 0 && s.authenticator == nil {
		return nil, fmt.Errorf("unauthorized")
	}

	authResult, err := s.authenticate(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("unauthorized")
	}
	if authResult == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	return authResult, nil
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

// ListenAndServe starts the MCP server based on the listen string.
// It supports TCP (default), HTTP, and SSE protocols.
// For HTTP and SSE protocols, authEnabled and authFile control Bearer token authentication.
func (s *MCPServer) ListenAndServe(listen string, authEnabled bool, authFile string) error {
	if listen == "" {
		// Default stdin/stdout mode
		s.Start()
		return nil
	}

	config, err := ParseListenString(listen)
	if err != nil {
		return err
	}

	switch config.Protocol {
	case "http":
		return s.ServeHTTP(config.Port, config.BasePath, authEnabled, authFile)
	case "sse":
		return s.ServeSSE(config.Port, config.BasePath, authEnabled, authFile)
	default: // "tcp"
		return s.ServeTCP(config.Address)
	}
}

// sseHandler handles SSE connections
func (s *MCPServer) sseHandler(w http.ResponseWriter, r *http.Request) {
	var authResult *AuthResult
	var rawToken string
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "bearer ") {
		rawToken = authHeader[7:]
		var err error
		authResult, err = s.authorizeToken(r.Context(), rawToken)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	} else if s.authEnabled {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "Missing sessionId parameter", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")

	respChan := make(chan JSONRPCResponse, 100)
	s.sseMu.Lock()
	s.sseConnections[sessionID] = respChan
	s.sseSessions[sessionID] = &sseSession{bearerToken: rawToken, authResult: authResult, respChan: respChan}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseConnections, sessionID)
		delete(s.sseSessions, sessionID)
		s.sseMu.Unlock()
		close(respChan)
	}()

	endpointEvent := "event: endpoint\ndata: /mcp\n\n"
	if _, err := w.Write([]byte(endpointEvent)); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

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

	ctx := r.Context()
	var authResult *AuthResult
	var rawToken string
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "bearer ") {
		rawToken = authHeader[7:]
		var err error
		authResult, err = s.authorizeToken(ctx, rawToken)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	} else if s.authEnabled {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024) // 10MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	sessionID := r.Header.Get("X-SSE-Session-ID")
	if sessionID == "" {
		if authResult != nil {
			ctx = authResult.Apply(ctx)
		}
		resp := s.processRequestWithContext(ctx, req)
		if resp.ID == nil {
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
		_, _ = w.Write(respData)
		return
	}

	s.sseMu.RLock()
	session, exists := s.sseSessions[sessionID]
	s.sseMu.RUnlock()

	if !exists {
		http.Error(w, "Invalid session ID", http.StatusBadRequest)
		return
	}

	if session.bearerToken != "" && !sameToken(rawToken, session.bearerToken) {
		http.Error(w, "Session token mismatch", http.StatusUnauthorized)
		return
	}

	if authResult != nil {
		s.sseMu.Lock()
		if current, ok := s.sseSessions[sessionID]; ok {
			current.authResult = authResult
		}
		s.sseMu.Unlock()
		ctx = authResult.Apply(ctx)
	} else if session.authResult != nil {
		ctx = session.authResult.Apply(ctx)
	}

	resp := s.processRequestWithContext(ctx, req)
	if resp.ID != nil {
		s.sseMu.RLock()
		respChan, exists := s.sseConnections[sessionID]
		s.sseMu.RUnlock()
		if exists {
			select {
			case respChan <- resp:
			default:
			}
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// httpHandler handles HTTP requests for MCP
func (s *MCPServer) httpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	authHeader := r.Header.Get("Authorization")
	hasToken := false
	tokenPreview := ""
	if authHeader != "" && len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "bearer ") {
		rawToken := authHeader[7:]
		hasToken = true
		if len(rawToken) > 8 {
			tokenPreview = rawToken[:4] + "..." + rawToken[len(rawToken)-4:]
		} else if len(rawToken) > 0 {
			tokenPreview = rawToken[:1] + "..."
		}
		authResult, err := s.authorizeToken(ctx, rawToken)
		if err != nil {
			if s.verbose {
				log.Printf("[HTTP] Unauthorized: token rejected")
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		ctx = authResult.Apply(ctx)
	} else if s.authEnabled {
		if s.verbose {
			log.Printf("[HTTP] Unauthorized: no Bearer token provided")
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024) // 10MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if s.verbose {
		authInfo := "no-token"
		if hasToken {
			authInfo = "token=" + tokenPreview
		} else if authHeader != "" {
			authInfo = fmt.Sprintf("invalid-auth-header=%q", authHeader)
		}
		toolName := ""
		toolArgs := ""
		if req.Method == "tools/call" && req.Params != nil {
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if paramsBytes, err := json.Marshal(req.Params); err == nil {
				if json.Unmarshal(paramsBytes, &params) == nil {
					toolName = params.Name
					if params.Arguments != nil {
						if argsBytes, err := json.Marshal(params.Arguments); err == nil {
							toolArgs = string(argsBytes)
						}
					}
				}
			}
		}
		if toolName != "" {
			log.Printf("[HTTP] %s %s method=%s tool=%s args=%s %s", r.Method, r.URL.Path, req.Method, toolName, toolArgs, authInfo)
		} else {
			log.Printf("[HTTP] %s %s method=%s %s", r.Method, r.URL.Path, req.Method, authInfo)
		}
	}

	resp := s.processRequestWithContext(ctx, req)
	if resp.ID == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	respData, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if s.verbose {
		if resp.Error != nil {
			log.Printf("[HTTP] Response ERROR: %s", resp.Error.Message)
		} else if resp.Result != nil {
			respStr, _ := json.Marshal(resp.Result)
			if len(respStr) > 200 {
				log.Printf("[HTTP] Response OK: %s...", string(respStr[:200]))
			} else {
				log.Printf("[HTTP] Response OK: %s", string(respStr))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respData)
}
