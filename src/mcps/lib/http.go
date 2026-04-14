package mcplib

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	errSSEConnectionUnavailable = errors.New("sse connection unavailable")
	errSSEBackpressure          = errors.New("sse response queue full")
	sseResponseEnqueueTimeout   = 5 * time.Second
)

func sameToken(a string, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *MCPServer) enqueueSSEResponse(sessionID string, resp JSONRPCResponse) error {
	s.sseMu.RLock()
	session, exists := s.sseSessions[sessionID]
	s.sseMu.RUnlock()
	if !exists {
		return errSSEConnectionUnavailable
	}
	timer := time.NewTimer(sseResponseEnqueueTimeout)
	defer timer.Stop()
	select {
	case session.respChan <- resp:
		return nil
	case <-timer.C:
		return errSSEBackpressure
	case <-session.done:
		return errSSEConnectionUnavailable
	}
}

func (s *MCPServer) readJSONRPCRequest(w http.ResponseWriter, r *http.Request) (JSONRPCRequest, bool) {
	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, s.maxHTTPRequestBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return JSONRPCRequest{}, false
		}
		http.Error(w, "Bad request", http.StatusBadRequest)
		return JSONRPCRequest{}, false
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return JSONRPCRequest{}, false
	}

	return req, true
}

func bearerTokenFromRequest(r *http.Request) (string, bool) {
	headers := r.Header.Values("Authorization")
	if len(headers) == 0 {
		// Compatibility fallback for clients that send the non-standard Authentication header.
		headers = r.Header.Values("Authentication")
	}

	for _, authHeader := range headers {
		for _, candidate := range strings.Split(authHeader, ",") {
			fields := strings.Fields(strings.TrimSpace(candidate))
			if len(fields) < 2 || !strings.EqualFold(fields[0], "bearer") {
				continue
			}

			token := strings.TrimSpace(strings.Join(fields[1:], " "))
			for len(token) >= 8 && strings.EqualFold(token[:7], "bearer ") {
				token = strings.TrimSpace(token[7:])
			}
			if len(token) >= 2 {
				if (token[0] == '"' && token[len(token)-1] == '"') || (token[0] == '\'' && token[len(token)-1] == '\'') {
					token = token[1 : len(token)-1]
				}
			}
			token = strings.TrimSpace(token)
			if token != "" {
				return token, true
			}
		}
	}

	return "", false
}

func (s *MCPServer) authorizeToken(ctx context.Context, rawToken string) (*AuthResult, error) {
	if s.authEnabled && s.authenticator == nil {
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

// ServeHTTP starts an HTTP server on the specified port with optional Bearer authentication.
// When authEnabled is true, token verification is delegated to the configured authenticator.
func (s *MCPServer) ServeHTTP(port string, basePath string, authEnabled bool) error {
	s.authEnabled = authEnabled
	if basePath == "" {
		basePath = "/"
	}
	http.HandleFunc(basePath, s.httpHandler)
	log.Printf("Starting HTTP server on port %s with base path %s", port, basePath)
	return http.ListenAndServe(":"+port, nil)
}

// ServeSSE starts an HTTP server with Server-Sent Events support for MCP.
// When authEnabled is true, each request must provide a Bearer token accepted by the authenticator.
func (s *MCPServer) ServeSSE(port string, basePath string, authEnabled bool) error {
	s.authEnabled = authEnabled
	basePath = strings.TrimRight(basePath, "/")

	ssePath := basePath + "/sse"
	mcpPath := basePath + "/mcp"

	http.HandleFunc(ssePath, func(w http.ResponseWriter, r *http.Request) {
		s.sseHandler(w, r, mcpPath)
	})
	http.HandleFunc(mcpPath, s.sseMCPHandler)

	log.Printf("Starting SSE server on port %s with SSE path %s and MCP path %s", port, ssePath, mcpPath)
	return http.ListenAndServe(":"+port, nil)
}

// ListenAndServe starts the MCP server based on the listen string.
// It supports TCP (default), HTTP, and SSE protocols.
// For HTTP and SSE protocols, authEnabled controls Bearer token authentication.
func (s *MCPServer) ListenAndServe(listen string, authEnabled bool) error {
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
		return s.ServeHTTP(config.Port, config.BasePath, authEnabled)
	case "sse":
		return s.ServeSSE(config.Port, config.BasePath, authEnabled)
	default: // "tcp"
		return s.ServeTCP(config.Address)
	}
}

// sseHandler handles SSE connections
func (s *MCPServer) sseHandler(w http.ResponseWriter, r *http.Request, mcpPath string) {
	if s.httpSecurityCheck(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var authResult *AuthResult
	var rawToken string
	if token, ok := bearerTokenFromRequest(r); ok {
		rawToken = token
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

	if s.httpSecurity.MaxSessions > 0 {
		s.sseMu.RLock()
		n := len(s.sseSessions)
		s.sseMu.RUnlock()
		if n >= s.httpSecurity.MaxSessions {
			http.Error(w, "Too Many Sessions", http.StatusServiceUnavailable)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	respChan := make(chan JSONRPCResponse, 100)
	session := &sseSession{
		bearerToken: rawToken,
		authResult:  authResult,
		respChan:    respChan,
		done:        make(chan struct{}),
	}
	if s.httpSecurity.SessionTimeout > 0 {
		session.timer = time.AfterFunc(s.httpSecurity.SessionTimeout, func() {
			session.doneOnce.Do(func() { close(session.done) })
		})
	}
	s.sseMu.Lock()
	s.sseSessions[sessionID] = session
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseSessions, sessionID)
		s.sseMu.Unlock()
		session.doneOnce.Do(func() { close(session.done) })
		if session.timer != nil {
			session.timer.Stop()
		}
	}()

	endpointEvent := "event: endpoint\ndata: " + mcpPath + "\n\n"
	if _, err := w.Write([]byte(endpointEvent)); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	for {
		select {
		case resp := <-respChan:
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
		case <-session.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// sseMCPHandler handles MCP requests over HTTP for SSE connections
func (s *MCPServer) sseMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.httpSecurityCheck(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	var authResult *AuthResult
	var rawToken string
	if token, ok := bearerTokenFromRequest(r); ok {
		rawToken = token
		var err error
		authResult, err = s.authorizeToken(ctx, rawToken)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	req, ok := s.readJSONRPCRequest(w, r)
	if !ok {
		return
	}

	sessionID := r.Header.Get("X-SSE-Session-ID")
	if sessionID == "" {
		if s.authEnabled && authResult == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
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

	if rawToken != "" {
		if session.bearerToken != "" && !sameToken(rawToken, session.bearerToken) {
			http.Error(w, "Session token mismatch", http.StatusUnauthorized)
			return
		}
		s.sseMu.Lock()
		if current, ok := s.sseSessions[sessionID]; ok {
			current.bearerToken = rawToken
			current.authResult = authResult
		}
		s.sseMu.Unlock()
	}

	if authResult != nil {
		ctx = authResult.Apply(ctx)
	} else if session.authResult != nil {
		ctx = session.authResult.Apply(ctx)
	} else if s.authEnabled {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if session.timer != nil && s.httpSecurity.SessionTimeout > 0 {
		session.timer.Reset(s.httpSecurity.SessionTimeout)
	}
	resp := s.processRequestWithContext(ctx, req)
	if resp.ID != nil {
		if err := s.enqueueSSEResponse(sessionID, resp); err != nil {
			switch {
			case errors.Is(err, errSSEConnectionUnavailable):
				http.Error(w, "SSE session is not connected", http.StatusGone)
			case errors.Is(err, errSSEBackpressure):
				log.Printf("Dropping SSE response for session %s: response queue full", sessionID)
				http.Error(w, "SSE response queue full", http.StatusServiceUnavailable)
			default:
				http.Error(w, "Failed to enqueue SSE response", http.StatusInternalServerError)
			}
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// httpHandler handles HTTP requests for MCP
func (s *MCPServer) httpHandler(w http.ResponseWriter, r *http.Request) {
	if s.httpSecurityCheck(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	hasToken := false
	tokenPreview := "NoAuth"
	if rawToken, ok := bearerTokenFromRequest(r); ok {
		hasToken = true
		tokenPreview = "Authorized"
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

	req, ok := s.readJSONRPCRequest(w, r)
	if !ok {
		return
	}

	if s.verbose {
		authInfo := "no-token"
		if hasToken {
			authInfo = "token=" + tokenPreview
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
