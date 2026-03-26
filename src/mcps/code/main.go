package main

import (
	"flag"
	"log"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port, http://host:port/path, or sse://host:port/path (optional) serve MCP over TCP, HTTP, or SSE")
	flag.Parse()

	codeService := NewCodeService()

	// Get all tools from the service
	tools := codeService.GetTools()
	server := mcplib.NewMCPServerFromTools(tools)

	// Start the server - this will block until the server is stopped
	if err := server.ListenAndServe(*listen, false); err != nil {
		log.Fatalln("ListenAndServe:", err)
	}
}
