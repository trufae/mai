package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
)

const (
	version = "1.1.0"
)

func showHelp() {
	fmt.Println(`Usage: mai-wmcp [options] "command1" "command2" ...
Options:
   -v	Show version information
   -h	Show this help message
   -b URL	Base URL to listen on (default: :8989)
   -y	Yolo mode (skip tool confirmations)
   -k	Drunk mode (permissive tool matching and parameter assignment)
   -i	Non-interactive mode (return errors instead of prompting)
   -o FILE	Output report to FILE
   -d	Enable debug logging (shows HTTP requests and JSON payloads)
   -c FILE	Path to config file (default: ~/.mai-wmcp.json)
   -n	Skip loading config file
   -p	Skip loading prompts (only expose tools)
 Example: mai-wmcp "r2pm -r r2mcp" "timemcp"
 Example with config: mai-wmcp -c /path/to/config.json`)
}

func showVersion() {
	fmt.Printf("mai-wmcp version %s\n", version)
}

func main() {
	// Parse command line flags
	configPath := ""
	skipConfig := false

	args := os.Args[1:]

	cmdArgs := []string{}

	// Show help if no arguments provided
	if len(args) == 0 {
		showHelp()
		os.Exit(0)
	}

	// First pass: extract config-related flags
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if len(arg) > 0 && arg[0] == '-' {
			switch arg {
			case "-c":
				if i+1 < len(args) {
					configPath = args[i+1]
					i++
				} else {
					fmt.Println("Error: -c requires a file path")
					showHelp()
					os.Exit(1)
				}
			case "-n":
				skipConfig = true
			}
		}
	}

	// First pass: extract config-related flags
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if len(arg) > 0 && arg[0] == '-' {
			switch arg {
			case "-c":
				if i+1 < len(args) {
					configPath = args[i+1]
					i++
				} else {
					fmt.Println("Error: -c requires a file path")
					showHelp()
					os.Exit(1)
				}
			case "-n":
				skipConfig = true
			}
		}
	}

	// Load configuration if not skipped
	var config *Config
	var configErr error
	if !skipConfig {
		config, configErr = LoadConfig(configPath)
		if configErr != nil {
			log.Printf("Warning: Failed to load config: %v", configErr)
			config = &Config{MCPServers: make(map[string]MCPServerConfig)}
		}
	} else {
		config = &Config{MCPServers: make(map[string]MCPServerConfig)}
	}

	// Set defaults from config
	baseURL := config.MaiOptions.BaseURL
	if baseURL == "" {
		baseURL = ":8989"
	}
	yoloMode := config.MaiOptions.YoloMode
	drunkMode := config.MaiOptions.DrunkMode
	nonInteractiveMode := config.MaiOptions.NonInteractive
	outputReport := config.MaiOptions.OutputReport
	debugMode := config.MaiOptions.DebugMode
	noPromptsMode := config.MaiOptions.NoPrompts

	// Second pass: process other command line arguments (can override config)
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if len(arg) > 0 && arg[0] == '-' {
			switch arg {
			case "-v":
				showVersion()
				os.Exit(0)
			case "-h":
				showHelp()
				os.Exit(0)
			case "-y":
				yoloMode = true
			case "-k":
				drunkMode = true
			case "-i":
				nonInteractiveMode = true
			case "-d":
				debugMode = true
			case "-p":
				noPromptsMode = true
			case "-c":
				// Already handled in first pass
				i++ // Skip the value
			case "-n":
				// Already handled in first pass
			case "-b":
				if i+1 < len(args) {
					baseURL = args[i+1]
					i++
				} else {
					fmt.Println("Error: -b requires a base URL")
					showHelp()
					os.Exit(1)
				}
			case "-o":
				if i+1 < len(args) {
					outputReport = args[i+1]
					i++
				} else {
					fmt.Println("Error: -o requires a filename")
					showHelp()
					os.Exit(1)
				}
			default:
				fmt.Printf("Unknown option: %s\n", arg)
				showHelp()
				os.Exit(1)
			}
		} else {
			cmdArgs = append(cmdArgs, arg)
		}
	}

	// Check if we have any commands to run or servers in config
	cmdProvided := len(cmdArgs) > 0
	configServers := len(config.MCPServers) > 0

	if !cmdProvided && !configServers {
		fmt.Println("Error: No MCP commands provided and no servers in config")
		showHelp()
		os.Exit(1)
	}

	service := NewMCPService(yoloMode, drunkMode, outputReport, noPromptsMode, nonInteractiveMode)

	// Set debug flag
	service.debugMode = debugMode

	// Ensure cleanup on exit
	defer service.StopAllServers()

	// Start MCP servers from command line arguments
	if len(cmdArgs) > 0 {
		for _, command := range cmdArgs {
			serverName := getServerNameFromCommand(command)
			if err := service.StartServer(serverName, command); err != nil {
				log.Printf("Failed to start server %s: %v", serverName, err)
				continue
			}
		}
	}

	// Start MCP servers from config
	if !skipConfig && len(config.MCPServers) > 0 {
		StartMCPServersFromConfig(service, config)
	}
	if len(service.servers) == 0 {
		fmt.Println("Error: No MCP servers available")
		os.Exit(1)
	}

	// Setup HTTP routes
	router := mux.NewRouter()

	// List all tools
	router.HandleFunc("/tools", service.listToolsHandler).Methods("GET")
	// JSON list of all tools
	router.HandleFunc("/tools/json", service.jsonToolsHandler).Methods("GET")
	// Quiet list of all tools
	router.HandleFunc("/tools/quiet", service.quietToolsHandler).Methods("GET")
	// Simple list of all tools (for small models)
	router.HandleFunc("/tools/simple", service.simpleToolsHandler).Methods("GET")
	// Markdown list of all tools
	router.HandleFunc("/tools/markdown", service.markdownToolsHandler).Methods("GET")

	// Prompts endpoints
	router.HandleFunc("/prompts", service.listPromptsHandler).Methods("GET")
	router.HandleFunc("/prompts/json", service.jsonPromptsHandler).Methods("GET")
	router.HandleFunc("/prompts/{prompt}", service.getPromptHandler).Methods("GET", "POST")
	router.HandleFunc("/prompts/{server}/{prompt}", service.getPromptHandler).Methods("GET", "POST")

	// Get service status
	router.HandleFunc("/status", service.statusHandler).Methods("GET")

	// Call a specific tool (old endpoint for backward compatibility)
	router.HandleFunc("/tools/{server}/{tool}", service.callToolHandler).Methods("GET", "POST")
	// Call a specific tool (new endpoint)
	router.HandleFunc("/call/{tool}", service.callToolHandler).Methods("GET", "POST")
	router.HandleFunc("/call/{server}/{tool}", service.callToolHandler).Methods("GET", "POST")

	// Root endpoint with usage info
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
- GET /prompts/{server}/{prompt} - Get a prompt by name from a server (args as query)
- GET /prompts/{prompt} - Get a prompt by name via auto-discovery
- POST /prompts/{server}/{prompt} - Get a prompt with JSON body of arguments
- POST /prompts/{prompt} - Get a prompt with JSON body (auto-discovery)

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
	}).Methods("GET")

	// Start HTTP server
	if envBaseURL := os.Getenv("MAI_WMCP_BASEURL"); envBaseURL != "" {
		baseURL = envBaseURL
	}

	log.Printf("Starting MCP REST service on %s", baseURL)
	accessAddr := strings.Replace(baseURL, "0.0.0.0", "localhost", 1)
	log.Printf("Access tools at: http://%s/tools", accessAddr)

	if err := http.ListenAndServe(baseURL, router); err != nil {
		log.Fatal("Failed to start HTTP server:", err)
	}
}
