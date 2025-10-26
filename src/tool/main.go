package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func parseFlags() Config {
	baseURL := flag.String("b", "", "Base URL where mcpd is running (overrides MAI_TOOL_BASEURL env var)")
	jsonOutput := flag.Bool("j", false, "Output in JSON format")
	xmlOutput := flag.Bool("x", false, "Output in XML format")
	markdownCode := flag.Bool("m", false, "Wrap markdown output in code blocks")
	quiet := flag.Bool("q", false, "Suppress non-essential output")
	simple := flag.Bool("s", false, "Use simple output format (for small models)")
	debug := flag.Bool("d", false, "Enable debug mode to show HTTP requests and JSON payloads")
	help := flag.Bool("h", false, "Show help message")

	flag.Parse()

	if *help {
		printUsage()
		os.Exit(0)
	}

	// Determine base URL: flag takes precedence, then env var, then default
	finalBaseURL := *baseURL
	if finalBaseURL == "" {
		if envURL := os.Getenv("MAI_TOOL_BASEURL"); envURL != "" {
			finalBaseURL = envURL
		} else {
			finalBaseURL = "http://localhost:8989"
		}
	}

	return Config{
		BaseURL:      finalBaseURL,
		JsonOutput:   *jsonOutput,
		XmlOutput:    *xmlOutput,
		MarkdownCode: *markdownCode,
		Quiet:        *quiet,
		Simple:       *simple,
		Debug:        *debug,
	}
}

func printUsage() {
	fmt.Println("Usage: mai-tool [options] <command>")
	fmt.Println("\nOptions:")
	fmt.Println("  -b <url>      Base URL where mcpd is running (default: http://localhost:8989)")
	fmt.Println("                Can also be set with MAI_TOOL_BASEURL environment variable")
	fmt.Println("  -j            Output in JSON format")
	fmt.Println("  -x            Output in XML format")
	fmt.Println("  -m            Wrap markdown output in code blocks")
	fmt.Println("  -q            Suppress non-essential output")
	fmt.Println("  -s            Use simple output format (for small models)")
	fmt.Println("  -d            Enable debug mode to show HTTP requests and JSON payloads")
	fmt.Println("  -h            Show help message")
	fmt.Println("\nCommands:")
	fmt.Println("  list                           List all available tools")
	fmt.Println("  servers                        List all available servers")
	fmt.Println("  call <server> <tool> [params]  Call a specific tool")
	fmt.Println("  prompts [list]                 List all available prompts")
	fmt.Println("  prompts get <server>/<name>    Render a prompt (accepts params)")
	fmt.Println("  resources [list]               List all available resources")
	fmt.Println("  resources read <server>/<uri>  Read a resource by URI")
}

func main() {
	// Parse command line flags
	config := parseFlags()

	// Process commands
	if len(flag.Args()) == 0 {
		// No command provided, show usage
		printUsage()
		os.Exit(1)
	}

	command := flag.Args()[0]
	switch command {
	case "list":
		listTools(config)
	case "prompts":
		// Subcommands: list, get
		if len(flag.Args()) < 2 || flag.Args()[1] == "list" {
			listPrompts(config)
			break
		}
		if flag.Args()[1] == "get" || flag.Args()[1] == "show" {
			if len(flag.Args()) < 3 {
				fmt.Println("Error: 'prompts get' requires a prompt name (optionally server/prompt)")
				fmt.Println("Usage: mai-tool prompts get <server>/<prompt>|<prompt> [param=value] ...")
				os.Exit(1)
			}
			arg := flag.Args()[2]
			var serverName string
			var promptName string
			if strings.Contains(arg, "/") {
				parts := strings.SplitN(arg, "/", 2)
				serverName = parts[0]
				promptName = parts[1]
			} else {
				promptName = arg
			}
			params := parseParams(flag.Args()[3:])
			getPrompt(config, serverName, promptName, params)
			break
		}
		fmt.Printf("Unknown 'prompts' subcommand: %s\n", flag.Args()[1])
		os.Exit(1)
	case "resources":
		// Subcommands: list, read
		if len(flag.Args()) < 2 || flag.Args()[1] == "list" {
			listResources(config)
			break
		}
		if flag.Args()[1] == "read" || flag.Args()[1] == "get" {
			if len(flag.Args()) < 3 {
				fmt.Println("Error: 'resources read' requires a resource URI (optionally server/uri)")
				fmt.Println("Usage: mai-tool resources read <server>/<uri>|<uri>")
				os.Exit(1)
			}
			arg := flag.Args()[2]
			var serverName string
			var resourceURI string
			if strings.Contains(arg, "/") && !strings.HasPrefix(arg, "file://") && !strings.HasPrefix(arg, "http://") && !strings.HasPrefix(arg, "https://") {
				parts := strings.SplitN(arg, "/", 2)
				serverName = parts[0]
				resourceURI = parts[1]
			} else {
				resourceURI = arg
			}
			readResource(config, serverName, resourceURI)
			break
		}
		fmt.Printf("Unknown 'resources' subcommand: %s\n", flag.Args()[1])
		os.Exit(1)
	case "call":
		nargs := len(flag.Args())
		if nargs < 2 {
			fmt.Println("Error: 'call' requires server and tool name")
			fmt.Println("Usage: mai-tool call <server> <tool> [param1=value1] [param2=value2] ...")
			fmt.Println("Usage: mai-tool call <server>/<tool> [param1=value1] [param2=value2] ...")
			os.Exit(1)
		}
		var serverName string
		var toolName string
		var params map[string]interface{}
		arg1 := flag.Args()[1]
		if strings.Contains(arg1, "/") {
			slicedText := strings.SplitN(arg1, "/", 2)
			serverName = slicedText[0]
			toolName = slicedText[1]
			params = parseParams(flag.Args()[2:])
		} else {
			serverName = ""
			toolName = arg1
			// When server is not provided, args are: call <tool> [params...]
			// so parameters start at index 2
			params = parseParams(flag.Args()[2:])
			/*
				if nargs < 3 {
					fmt.Println("Error: 'call' requires server and tool name")
					fmt.Println("Usage: mai-tool call <server> <tool> [param1=value1] [param2=value2] ...")
					os.Exit(1)
				}
				serverName = flag.Args()[1]
				toolName = flag.Args()[2]
				params = parseParams(flag.Args()[3:])
			*/
		}
		callTool(config, serverName, toolName, params)
	case "servers":
		listServers(config)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}
