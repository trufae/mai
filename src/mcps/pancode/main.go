package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port, http://host:port/path, or sse://host:port/path (optional) serve MCP over TCP, HTTP, or SSE")
	workdirFlag := flag.String("workdir", "", "(optional) working directory for repository operations (or set PANCODE_WORKDIR)")
	sandboxFlag := flag.String("sandboxdir", "", "(optional) sandbox directory for ephemeral files (or set PANCODE_SANDBOXDIR)")
	listTools := flag.Bool("t", false, "list all available tools and exit")
	minimalMode := flag.Bool("m", false, "enable only minimum necessary tools for coding agent")
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

	pancodeService := NewPanCodeService(*minimalMode)

	// Get all tools from the service
	tools := pancodeService.GetTools()

	if *listTools {
		fmt.Println("Available tools:")
		for _, tool := range tools {
			fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
		}
		os.Exit(0)
	}

	server := mcplib.NewMCPServerFromTools(tools)

	// Register prompts exposed by this coding agent
	server.SetPrompts(getSamplePrompts())

	// Start the server - this will block until the server is stopped
	if err := server.ListenAndServe(*listen, false); err != nil {
		log.Fatalln("ListenAndServe:", err)
	}
}
