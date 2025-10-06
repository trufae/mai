package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port (optional) serve MCP over TCP")
	workdirFlag := flag.String("workdir", "", "(optional) working directory for repository operations (or set PANCODE_WORKDIR)")
	sandboxFlag := flag.String("sandboxdir", "", "(optional) sandbox directory for ephemeral files (or set PANCODE_SANDBOXDIR)")
	flag.Parse()

	// Determine effective directories: flags override env vars
	workdir := *workdirFlag
	if workdir == "" {
		if env := os.Getenv("PANCODE_WORKDIR"); env != "" {
			workdir = env
		}
	}
	sandbox := *sandboxFlag
	if sandbox == "" {
		if env := os.Getenv("PANCODE_SANDBOXDIR"); env != "" {
			sandbox = env
		}
	}

	if err := InitSandbox(workdir, sandbox); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize sandbox: %v\n", err)
		os.Exit(2)
	}

	pancodeService := NewPanCodeService()

	// Get all tools from the service
	tools := pancodeService.GetTools()

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
	if *listen != "" {
		if err := server.ServeTCP(*listen); err != nil {
			log.Fatalln("ServeTCP:", err)
		}
	} else {
		server.Start()
	}
}
