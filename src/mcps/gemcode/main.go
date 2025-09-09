package main

import (
	"mcplib"
)

func main() {
	gemcodeService := NewGemCodeService()

	// Get all tools from the service
	tools := gemcodeService.GetTools()

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

	// Register prompts exposed by this coding agent
	server.SetPrompts(getSamplePrompts())

	// Start the server - this will block until the server is stopped
	server.Start()
}
