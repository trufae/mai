package main

import (
	"mcplib"
)

func main() {
	timeService := NewTimeService()

	// Get all tools from the service
	tools := timeService.GetTools()

	// Create tool definitions for server initialization
	var toolDefs []mcplib.ToolDefinition
	for _, tool := range tools {
		toolDefs = append(toolDefs, mcplib.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	// Initialize the server with tool definitions
	server := mcplib.NewMCPServer(toolDefs)

	// Register all tool handlers
	for _, tool := range tools {
		server.RegisterTool(tool.Name, tool.Handler)
	}

	// Start the server - this will block until the server is stopped
	server.Start()
}
