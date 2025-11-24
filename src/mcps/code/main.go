package main

import (
	"flag"
	"log"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port or http://host:port/path (optional) serve MCP over TCP or HTTP")
	flag.Parse()

	codeService := NewCodeService()

	// Get all tools from the service
	tools := codeService.GetTools()

	// Create tool definitions for server initialization
	var toolDefs []mcplib.ToolDefinition
	for _, tool := range tools {
		toolDefs = append(toolDefs, mcplib.ToolDefinition{
			Name:          tool.Name,
			Description:   tool.Description,
			InputSchema:   tool.InputSchema,
			UsageExamples: tool.UsageExamples,
		})
	}

	// Initialize the server with tool definitions
	server := mcplib.NewMCPServer(toolDefs)

	// Register all tool handlers
	for _, tool := range tools {
		server.RegisterTool(tool.Name, tool.Handler)
	}

	// Start the server - this will block until the server is stopped
	if *listen != "" {
		config, err := mcplib.ParseListenString(*listen)
		if err != nil {
			log.Fatalln("ParseListenString:", err)
		}
		if config.Protocol == "http" {
			if err := server.ServeHTTP(config.Port, config.BasePath, false, ""); err != nil {
				log.Fatalln("ServeHTTP:", err)
			}
		} else {
			// TCP mode (default)
			if err := server.ServeTCP(config.Address); err != nil {
				log.Fatalln("ServeTCP:", err)
			}
		}
	} else {
		server.Start()
	}
}
