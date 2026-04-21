package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	wmcplib "wmcplib"

	"github.com/gorilla/mux"
)

// registerMCPRoutes registers the single JSON-RPC endpoint used by MCP
// clients (the bridge exposes a single aggregated view of all child servers).
func registerMCPRoutes(router *mux.Router, service *wmcplib.MCPService) {
	router.HandleFunc("/", mcpJSONRPCHandler(service)).Methods("POST")
}

func writeJSONRPCResponse(w http.ResponseWriter, sessionID string, resp *wmcplib.JSONRPCResponse) {
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	w.Header().Set("Content-Type", "application/json")

	if resp == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal jsonrpc response", http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(data); err != nil {
		log.Printf("ERROR: failed to write JSONRPC response: %v", err)
	}
}

func writeJSONRPCError(w http.ResponseWriter, sessionID string, id interface{}, code int, message string) {
	writeJSONRPCResponse(w, sessionID, &wmcplib.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   wmcplib.RPCError{Code: code, Message: message},
	})
}

func mcpJSONRPCHandler(service *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := ""
		if service.SessionMode {
			sessionID = service.EnsureSessionID()
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONRPCError(w, sessionID, nil, -32700, "failed to read request body")
			return
		}

		payload := strings.TrimSpace(string(body))
		if payload == "" {
			writeJSONRPCError(w, sessionID, nil, -32700, "empty request body")
			return
		}

		if strings.HasPrefix(payload, "[") {
			writeJSONRPCError(w, sessionID, nil, -32600, "batch requests not supported")
			return
		}

		var request wmcplib.JSONRPCRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			writeJSONRPCError(w, sessionID, nil, -32700, "invalid json")
			return
		}

		response, notification := service.ProcessMCPRequest(request)
		if notification {
			if sessionID != "" {
				w.Header().Set("Mcp-Session-Id", sessionID)
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		if response == nil {
			if sessionID != "" {
				w.Header().Set("Mcp-Session-Id", sessionID)
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		writeJSONRPCResponse(w, sessionID, response)
	}
}

// rootHandler serves the human-readable help page at "/".
func rootHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Native-Tool-Call")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	usage := `# MCP REST Bridge

Available endpoints:

- GET /status - Service status
- GET /tools - List all available tools
- GET /tools/json - List all available tools in JSON format
- GET /tools/quiet - List all tools in minimal format
- GET /tools/markdown - List all tools in markdown format
- GET /tools/{server}/{tool}?param=value - Call tool with query parameters (legacy)
- GET /call/{server}/{tool}?param=value - Call tool with query parameters
- GET /call/{tool}?param=value - Call tool on auto-discovered server
- POST /tools/{server}/{tool} - Call tool with JSON body or form data (legacy)
- POST /call/{server}/{tool} - Call tool with JSON body or form data
- POST /call/{tool} - Call tool with JSON body or form data (auto-discovered server)

  Prompts endpoints:
  - GET /prompts - List all available prompts
  - GET /prompts/json - List all available prompts in JSON format
  - GET /prompts/quiet - List all available prompts in quiet format (names only)
  - GET /prompts/{server}/{prompt} - Get a prompt by name from a server (args as query)
  - GET /prompts/{prompt} - Get a prompt by name via auto-discovery
  - POST /prompts/{server}/{prompt} - Get a prompt with JSON body of arguments
  - POST /prompts/{prompt} - Get a prompt with JSON body (auto-discovery)

 Resources endpoints:
 - GET /resources - List all available resources
 - GET /resources/json - List all available resources in JSON format
 - GET /resources/{server}/{uri} - Read a resource by URI from a server

 Examples:
 - curl http://localhost:8989/tools
 - curl http://localhost:8989/tools/json
 - curl http://localhost:8989/tools/quiet
 - curl http://localhost:8989/tools/markdown
 - curl http://localhost:8989/tools/server1/mytool?arg1=value1
 - curl -X POST http://localhost:8989/tools/server1/mytool -H "Content-Type: application/json" -d '{"arg1":"value1"}'
 - curl http://localhost:8989/prompts
 - curl http://localhost:8989/prompts/json
 - curl http://localhost:8989/prompts/server1/myPrompt?topic=xyz
 - curl -X POST http://localhost:8989/prompts/server1/myPrompt -H "Content-Type: application/json" -d '{"topic":"xyz"}'
`
	w.Write([]byte(usage))
}
