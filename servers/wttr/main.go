package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool definition for tools/list
var toolsList = []interface{}{map[string]interface{}{
	"name":        "getWeather",
	"description": "Get weather for a location",
	"inputSchema": map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The location to get weather for",
			},
		},
		"required": []string{"location"},
	},
}}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(req.ID, -32700, "Parse error: invalid JSON")
			continue
		}

		switch req.Method {
		case "initialize":
			sendResult(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
			})
		case "notifications/initialized":
			// This is a notification, no response needed
			// Just acknowledge it silently
			continue
		case "tools/list":
			// Return tools wrapped in proper MCP format
			sendResult(req.ID, map[string]interface{}{
				"tools": toolsList,
			})
		case "tools/call":
			handleCall(req)
		default:
			sendError(req.ID, -32601, "Method not found: "+req.Method)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatalln("Error reading stdin:", err)
	}
}

func handleCall(req JSONRPCRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		sendError(req.ID, -32602, "Invalid params")
		return
	}
	if params.Name != "getWeather" {
		sendError(req.ID, -32601, "Tool not found: "+params.Name)
		return
	}
	locRaw, ok := params.Arguments["location"].(string)
	if !ok || locRaw == "" {
		sendError(req.ID, -32602, "Missing or invalid 'location' argument")
		return
	}
	weather, err := fetchWeather(locRaw)
	if err != nil {
		sendError(req.ID, -32000, "Weather fetch error: "+err.Error())
		return
	}
	
	// Return proper MCP tools/call response format
	sendResult(req.ID, map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": weather,
			},
		},
	})
}

func fetchWeather(location string) (string, error) {
	esc := url.QueryEscape(location)
	
	// Create a new request with the curl User-Agent
	// req, err := http.NewRequest("GET", fmt.Sprintf("https://wttr.in/%s?1FqT", esc), nil)
	req, err := http.NewRequest("GET", fmt.Sprintf("https://wttr.in/%s?format=%%l:+%%m+%%c+%%C+%%t+%%f'", esc), nil)
	if err != nil {
		return "", err
	}
	
	// Set User-Agent to match curl's
	req.Header.Set("User-Agent", "curl/8.1.2")
	req.Header.Set("Accept", "*/*")
	
	// Use the default client to send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func sendResult(id interface{}, result interface{}) {
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}

func sendError(id interface{}, code int, message string) {
	errObj := RPCError{Code: code, Message: message}
	resp := JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &errObj}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}
