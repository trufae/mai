package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"

	wmcplib "wmcplib"
)

// runStdioBridge serves MCP JSON-RPC over stdin/stdout (one JSON object per
// line). It is the alternative transport to the HTTP server and is what
// mai-repl spawns when its mcp.transport option is set to "stdio".
//
// Notifications produce no reply; everything else gets one line of response.
// The function returns when stdin closes.
func runStdioBridge(service *wmcplib.MCPService) {
	// Log output goes to stderr so it doesn't corrupt the JSON-RPC stream.
	log.SetOutput(os.Stderr)

	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var request wmcplib.JSONRPCRequest
		if err := json.Unmarshal(line, &request); err != nil {
			errResp := wmcplib.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   wmcplib.RPCError{Code: -32700, Message: "invalid json"},
			}
			data, _ := json.Marshal(errResp)
			writer.Write(data)
			writer.WriteByte('\n')
			writer.Flush()
			continue
		}

		response, notification := service.ProcessMCPRequest(request)
		if notification {
			continue
		}
		if response == nil {
			continue
		}

		data, err := json.Marshal(response)
		if err != nil {
			log.Printf("stdio bridge: failed to marshal response: %v", err)
			continue
		}
		if _, err := writer.Write(data); err != nil {
			log.Printf("stdio bridge: write error: %v", err)
			return
		}
		if err := writer.WriteByte('\n'); err != nil {
			return
		}
		if err := writer.Flush(); err != nil {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("stdio bridge: scanner error: %v", err)
	}
}
