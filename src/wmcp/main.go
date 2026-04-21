package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	wmcplib "wmcplib"

	"github.com/gorilla/mux"
)

func showHelp() {
	fmt.Println(`Usage: mai-wmcp [options] "server1" "server2" ...
  Options:
     -b URL   Base URL to listen on (default: :8989)
     -c FILE  Path to config file (default: ~/.config/mai/mcps.json)
     -C JSON  Config as JSON string (alternative to -c)
     -d       Enable debug logging (shows HTTP requests and JSON payloads)
     -E       Edit the config file (~/.config/mai/mcps.json)
     -h       Show this help message
     -i       Non-interactive mode (return errors instead of prompting)
     -j       Print tools, prompts, and resources in JSON format (with -t)
     -k       Drunk mode (permissive tool matching and parameter assignment)
     -n       Skip loading config file
     -o FILE  Output report to FILE
     -p       Skip loading prompts (only expose tools)
     -s       Enable session ID tracking (disabled by default to prevent SSE hijacking)
     -S       Serve MCP JSON-RPC over stdio instead of HTTP
     -t       Load MCP servers and list tools, prompts, and resources, then quit
     -v       Show version information
     -y       Yolo mode (skip tool confirmations)
  Examples:
    Local servers: mai-wmcp -y "r2pm -r r2mcp" "timemcp"
    HTTP servers: mai-wmcp "https://api.example.com/mcp"
    SSE servers: mai-wmcp "sse://api.example.com/mcp"
    Config file: mai-wmcp -c /path/to/config.json
    Config JSON: mai-wmcp -C '{"mcpServers":{"myserver":{"type":"stdio","command":"mycommand"}}}'
    List mode: mai-wmcp -t "r2pm -r r2mcp" "timemcp"
    HTTP/SSE servers use bearer auth from MAI_MCP_AUTH_<DOMAIN> env vars (domain sanitized)`)
}

func showVersion() {
	fmt.Printf("mai-wmcp version %s\n", wmcplib.MaiVersion)
}

func listMCPData(service *wmcplib.MCPService, jsonOutput bool) {
	service.Mutex.RLock()
	defer service.Mutex.RUnlock()

	if jsonOutput {
		result := make(map[string]interface{})
		tools := make(map[string][]wmcplib.Tool)
		prompts := make(map[string][]wmcplib.Prompt)
		resources := make(map[string][]wmcplib.Resource)

		for serverName, server := range service.Servers {
			server.Mutex.RLock()
			serverTools := make([]wmcplib.Tool, len(server.Tools))
			copy(serverTools, server.Tools)
			for i := range serverTools {
				if len(serverTools[i].Parameters) == 0 && serverTools[i].InputSchema != nil {
					serverTools[i].Parameters = wmcplib.ExtractParametersFromSchema(serverTools[i].InputSchema)
				}
			}
			tools[serverName] = serverTools

			serverPrompts := make([]wmcplib.Prompt, len(server.Prompts))
			copy(serverPrompts, server.Prompts)
			prompts[serverName] = serverPrompts

			serverResources := make([]wmcplib.Resource, len(server.Resources))
			copy(serverResources, server.Resources)
			resources[serverName] = serverResources
			server.Mutex.RUnlock()
		}

		result["tools"] = tools
		result["prompts"] = prompts
		result["resources"] = resources

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			fmt.Printf("Error marshaling JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(jsonBytes))
	} else {
		var output strings.Builder
		output.WriteString("# MCP Data\n\n")

		output.WriteString("## Tools\n\n")
		for _, server := range service.Servers {
			server.Mutex.RLock()
			for _, tool := range server.Tools {
				output.WriteString(fmt.Sprintf("ToolName: %s\n", tool.Name))
				output.WriteString(fmt.Sprintf("Description: %s\n", tool.Description))
				if len(tool.Parameters) > 0 {
					output.WriteString("Parameters:\n")
					for _, param := range tool.Parameters {
						req := ""
						if param.Required {
							req = " (required)"
						}
						output.WriteString(fmt.Sprintf("  - %s=<value> : %s (%s)%s\n",
							param.Name, param.Description, param.Type, req))
					}
				}
				output.WriteString("\n")
			}
			server.Mutex.RUnlock()
		}

		output.WriteString("## Prompts\n\n")
		for _, server := range service.Servers {
			server.Mutex.RLock()
			for _, prompt := range server.Prompts {
				output.WriteString(fmt.Sprintf("PromptName: %s\n", prompt.Name))
				if prompt.Description != "" {
					output.WriteString(fmt.Sprintf("Description: %s\n", prompt.Description))
				}
				if len(prompt.Arguments) > 0 {
					output.WriteString("Parameters:\n")
					for _, a := range prompt.Arguments {
						req := ""
						if a.Required {
							req = " [required]"
						}
						typ := a.Type
						if typ == "" {
							typ = "string"
						}
						output.WriteString(fmt.Sprintf("- %s=<%s> : %s%s\n", a.Name, typ, a.Description, req))
					}
				}
				output.WriteString("\n")
			}
			server.Mutex.RUnlock()
		}

		output.WriteString("## Resources\n\n")
		for _, server := range service.Servers {
			server.Mutex.RLock()
			for _, resource := range server.Resources {
				output.WriteString(fmt.Sprintf("URI: %s\n", resource.URI))
				output.WriteString(fmt.Sprintf("Name: %s\n", resource.Name))
				if resource.Description != "" {
					output.WriteString(fmt.Sprintf("Description: %s\n", resource.Description))
				}
				if resource.MimeType != "" {
					output.WriteString(fmt.Sprintf("MIME Type: %s\n", resource.MimeType))
				}
				output.WriteString("\n")
			}
			server.Mutex.RUnlock()
		}

		fmt.Print(output.String())
	}
}

func main() {
	configPath := ""
	configJSON := ""
	skipConfig := false
	toolsList := false
	jsonOutput := false
	stdioMode := false

	args := os.Args[1:]

	cmdArgs := []string{}

	var config *wmcplib.Config
	var configErr error

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
			case "-C":
				if i+1 < len(args) {
					configJSON = args[i+1]
					i++
				} else {
					fmt.Println("Error: -C requires a JSON string")
					showHelp()
					os.Exit(1)
				}
			case "-n":
				skipConfig = true
			case "-t":
				toolsList = true
			case "-j":
				if !toolsList {
					fmt.Println("Error: -j can only be used with -t")
					showHelp()
					os.Exit(1)
				}
				jsonOutput = true
			}
		}
	}

	if !skipConfig {
		if envConfigJSON := os.Getenv("MAI_AGENT_CONFIG"); envConfigJSON != "" {
			config, configErr = wmcplib.LoadConfigFromJSON(envConfigJSON)
			if configErr == nil {
				log.Printf("Loaded config from MAI_AGENT_CONFIG environment variable")
			}
		}

		if (configErr != nil || config == nil || len(config.MCPServers) == 0) && configJSON != "" {
			config, configErr = wmcplib.LoadConfigFromJSON(configJSON)
			if configErr == nil {
				log.Printf("Loaded config from -C flag")
			}
		}

		if configErr != nil || config == nil || len(config.MCPServers) == 0 {
			config, configErr = wmcplib.LoadConfig(configPath)
			if configErr != nil || config == nil || len(config.MCPServers) == 0 {
				if configPath != "" {
					if _, err := os.Stat(configPath); err == nil {
						config, configErr = wmcplib.LoadMAIConfig(configPath)
						if configErr == nil {
							log.Printf("Loaded MAI config from %s", configPath)
						}
					}
				}
				if configErr != nil || config == nil || len(config.MCPServers) == 0 {
					home, err := os.UserHomeDir()
					if err == nil {
						maiConfigPath := filepath.Join(home, ".config", "mai", "mcps.json")
						if _, err := os.Stat(maiConfigPath); err == nil {
							config, configErr = wmcplib.LoadMAIConfig(maiConfigPath)
							if configErr == nil {
								log.Printf("Loaded config from %s", maiConfigPath)
							}
						}
					}
				}
				if configErr != nil {
					log.Printf("Warning: Failed to load config: %v", configErr)
					config = &wmcplib.Config{MCPServers: make(map[string]wmcplib.MCPServerConfig)}
				}
			}
		}
	} else {
		config = &wmcplib.Config{MCPServers: make(map[string]wmcplib.MCPServerConfig)}
	}

	baseURL := ":8989"
	if config != nil && config.MaiOptions.BaseURL != "" {
		baseURL = config.MaiOptions.BaseURL
	}
	yoloMode := false
	drunkMode := false
	nonInteractiveMode := false
	outputReport := ""
	debugMode := false
	noPromptsMode := false
	sessionMode := false
	if config != nil {
		yoloMode = config.MaiOptions.YoloMode
		drunkMode = config.MaiOptions.DrunkMode
		nonInteractiveMode = config.MaiOptions.NonInteractive
		outputReport = config.MaiOptions.OutputReport
		debugMode = config.MaiOptions.DebugMode
		noPromptsMode = config.MaiOptions.NoPrompts
		sessionMode = config.MaiOptions.SessionMode
	}

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
			case "-E":
				home, err := os.UserHomeDir()
				if err != nil {
					fmt.Println("Error: cannot get home directory")
					os.Exit(1)
				}
				configFile := filepath.Join(home, ".config", "mai", "mcps.json")
				dir := filepath.Dir(configFile)
				if err := os.MkdirAll(dir, 0755); err != nil {
					fmt.Printf("Error creating config directory: %v\n", err)
					os.Exit(1)
				}
				editor := os.Getenv("EDITOR")
				if editor == "" {
					if runtime.GOOS == "windows" {
						editor = "notepad"
					} else {
						editor = "vim"
					}
				}
				cmd := exec.Command(editor, configFile)
				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Printf("Error running editor: %v\n", err)
					os.Exit(1)
				}
				os.Exit(0)
			case "-p":
				noPromptsMode = true
			case "-s":
				sessionMode = true
			case "-S":
				stdioMode = true
			case "-c":
				i++
			case "-C":
				i++
			case "-n":
			case "-t":
			case "-j":
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

	if toolsList && len(cmdArgs) > 0 {
		skipConfig = true
	}

	if len(cmdArgs) > 0 && configPath == "" && !skipConfig {
		allURLs := true
		for _, arg := range cmdArgs {
			if !strings.HasPrefix(arg, "http://") && !strings.HasPrefix(arg, "https://") && !strings.HasPrefix(arg, "sse://") && !strings.HasPrefix(arg, "sses://") {
				allURLs = false
				break
			}
		}
		if allURLs {
			skipConfig = true
		}
	}

	cmdProvided := len(cmdArgs) > 0
	configServers := config != nil && len(config.MCPServers) > 0

	if !cmdProvided && !configServers {
		fmt.Println("Error: No MCP commands provided and no servers in config")
		showHelp()
		os.Exit(1)
	}

	service := wmcplib.NewMCPService(wmcplib.Options{
		YoloMode:       yoloMode,
		DrunkMode:      drunkMode,
		ReportFile:     outputReport,
		NoPrompts:      noPromptsMode,
		NonInteractive: nonInteractiveMode,
		SessionMode:    sessionMode,
		DebugMode:      debugMode,
		Prompter:       wmcplib.NewStdinPrompter(),
	})

	defer service.StopAllServers()

	if len(cmdArgs) > 0 {
		for _, command := range cmdArgs {
			serverName := wmcplib.GetServerNameFromCommand(command)
			if err := service.StartServer(serverName, command); err != nil {
				log.Printf("Failed to start server %s: %v", serverName, err)
				continue
			}
		}
	}

	if !skipConfig && config != nil && len(config.MCPServers) > 0 {
		wmcplib.StartMCPServersFromConfig(service, config)
	}
	if len(service.Servers) == 0 {
		if toolsList {
			fmt.Println("Error: No MCP servers available to list")
			os.Exit(1)
		}
		fmt.Println("Error: No MCP servers available")
		os.Exit(1)
	}

	if toolsList {
		listMCPData(service, jsonOutput)
		os.Exit(0)
	}

	if stdioMode {
		runStdioBridge(service)
		return
	}

	router := mux.NewRouter()

	registerMCPRoutes(router, service)

	router.HandleFunc("/tools", listToolsHandler(service)).Methods("GET", "OPTIONS")
	router.HandleFunc("/tools/json", jsonToolsHandler(service)).Methods("GET", "OPTIONS")
	router.HandleFunc("/tools/quiet", quietToolsHandler(service)).Methods("GET", "OPTIONS")
	router.HandleFunc("/tools/simple", simpleToolsHandler(service)).Methods("GET", "OPTIONS")
	router.HandleFunc("/tools/markdown", markdownToolsHandler(service)).Methods("GET", "OPTIONS")

	router.HandleFunc("/prompts", listPromptsHandler(service)).Methods("GET")
	router.HandleFunc("/prompts/json", jsonPromptsHandler(service)).Methods("GET")
	router.HandleFunc("/prompts/quiet", quietPromptsHandler(service)).Methods("GET")
	router.HandleFunc("/prompts/{prompt}", getPromptHandler(service)).Methods("GET", "POST")
	router.HandleFunc("/prompts/{server}/{prompt}", getPromptHandler(service)).Methods("GET", "POST")

	router.HandleFunc("/resources", listResourcesHandler(service)).Methods("GET")
	router.HandleFunc("/resources/json", jsonResourcesHandler(service)).Methods("GET")
	router.HandleFunc("/resources/{server}/{uri}", readResourceHandler(service)).Methods("GET")

	router.HandleFunc("/status", statusHandler(service)).Methods("GET")

	router.HandleFunc("/openapi.json", openapiHandler(service)).Methods("GET", "OPTIONS")

	router.HandleFunc("/tools/{server}/{tool}", callToolHandler(service)).Methods("GET", "POST", "OPTIONS")
	router.HandleFunc("/call/{tool}", callToolHandler(service)).Methods("GET", "POST", "OPTIONS")
	router.HandleFunc("/call/{server}/{tool}", callToolHandler(service)).Methods("GET", "POST", "OPTIONS")
	router.HandleFunc("/v1/tool/{tool}", callToolHandler(service)).Methods("GET", "POST", "OPTIONS")

	router.HandleFunc("/", rootHandler).Methods("GET")

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
