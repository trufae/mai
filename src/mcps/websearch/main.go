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
	enableOllama := flag.Bool("ollama", false, "enable Ollama search provider")
	enableDuckDuckGo := flag.Bool("duckduckgo", false, "enable DuckDuckGo search provider")
	enableWikipedia := flag.Bool("wikipedia", false, "enable Wikipedia search provider")
	enableSearxng := flag.Bool("searxng", false, "enable Searxng search provider")
	allProviders := flag.Bool("all-providers", false, "search with all enabled providers instead of just the first one")
	flag.Parse()

	webSearchService := NewWebSearchService(*allProviders)

	// Enable requested providers
	if *enableOllama {
		if err := webSearchService.EnableProvider("ollama"); err != nil {
			log.Fatalf("Failed to enable Ollama provider: %v", err)
		}
	}

	if *enableDuckDuckGo {
		if err := webSearchService.EnableProvider("duckduckgo"); err != nil {
			log.Fatalf("Failed to enable DuckDuckGo provider: %v", err)
		}
	}

	if *enableWikipedia {
		if err := webSearchService.EnableProvider("wikipedia"); err != nil {
			log.Fatalf("Failed to enable Wikipedia provider: %v", err)
		}
	}

	if *enableSearxng {
		if err := webSearchService.EnableProvider("searxng"); err != nil {
			log.Fatalf("Failed to enable Searxng provider: %v", err)
		}
	}

	// Check if any providers are enabled and working
	enabledProviders := webSearchService.GetEnabledProviders()
	if len(enabledProviders) == 0 {
		fmt.Fprintf(os.Stderr, "Error: No search providers are enabled or working.\n")
		fmt.Fprintf(os.Stderr, "Available providers:\n")
		fmt.Fprintf(os.Stderr, "  -ollama: Enable Ollama search (requires OLLAMA_API_KEY environment variable)\n")
		fmt.Fprintf(os.Stderr, "  -duckduckgo: Enable DuckDuckGo search (no API key required)\n")
		fmt.Fprintf(os.Stderr, "  -wikipedia: Enable Wikipedia search (no API key required)\n")
		fmt.Fprintf(os.Stderr, "  -searxng: Enable Searxng search (requires Searxng instance at localhost:8888 or SEARXNG_API_URL)\n")
		fmt.Fprintf(os.Stderr, "\nExample: ./websearch -ollama -duckduckgo -wikipedia -searxng\n")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Enabled providers: %v\n", enabledProviders)

	// Get all tools from the service
	tools := webSearchService.GetTools()

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
		if err := server.ServeTCP(*listen); err != nil {
			log.Fatalln("ServeTCP:", err)
		}
	} else {
		server.Start()
	}
}
