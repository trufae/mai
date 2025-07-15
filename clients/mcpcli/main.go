package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Tool represents an MCP tool
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ServerTools represents tools available on a specific server
type ServerTools map[string][]Tool

// Config holds the application configuration
type Config struct {
	Host         string
	Port         string
	JsonOutput   bool
	MarkdownCode bool
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
	case "call":
		if len(flag.Args()) < 3 {
			fmt.Println("Error: 'call' requires server and tool name")
			fmt.Println("Usage: mcpcli call <server> <tool> [param1=value1] [param2=value2] ...")
			os.Exit(1)
		}
		serverName := flag.Args()[1]
		toolName := flag.Args()[2]
		params := parseParams(flag.Args()[3:])
		callTool(config, serverName, toolName, params)
	case "servers":
		listServers(config)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func parseFlags() Config {
	host := flag.String("h", "localhost", "Host where mcpd is running")
	port := flag.String("p", "8080", "Port where mcpd is running")
	jsonOutput := flag.Bool("j", false, "Output in JSON format")
	markdownCode := flag.Bool("m", false, "Wrap markdown output in code blocks")

	flag.Parse()

	return Config{
		Host:         *host,
		Port:         *port,
		JsonOutput:   *jsonOutput,
		MarkdownCode: *markdownCode,
	}
}

func printUsage() {
	fmt.Println("Usage: mcpcli [options] <command>")
	fmt.Println("\nOptions:")
	fmt.Println("  -h <host>  Host where mcpd is running (default: localhost)")
	fmt.Println("  -p <port>  Port where mcpd is running (default: 8080)")
	fmt.Println("  -j         Output in JSON format")
	fmt.Println("  -m         Wrap markdown output in code blocks")
	fmt.Println("\nCommands:")
	fmt.Println("  list                           List all available tools")
	fmt.Println("  servers                        List all available servers")
	fmt.Println("  call <server> <tool> [params]  Call a specific tool")
	fmt.Println("\nExamples:")
	fmt.Println("  mcpcli list")
	fmt.Println("  mcpcli -j list")
	fmt.Println("  mcpcli call server1 mytool param1=value1 param2=value2")
	fmt.Println("  mcpcli call server1 mytool \"text=value with spaces\"")
}

func parseParams(args []string) map[string]string {
	params := make(map[string]string)
	
	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) == 2 {
			params[parts[0]] = parts[1]
		}
	}
	
	return params
}

func buildApiUrl(config Config, path string) string {
	return fmt.Sprintf("http://%s:%s%s", config.Host, config.Port, path)
}

func listServers(config Config) {
	// Get the status endpoint which lists servers
	statusUrl := buildApiUrl(config, "/status")
	resp, err := http.Get(statusUrl)
	if err != nil {
		fmt.Printf("Error connecting to mcpd: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Server returned error: %s\n", resp.Status)
		body, _ := io.ReadAll(resp.Body)
		fmt.Println(string(body))
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}

	// For JSON output, convert markdown to JSON
	if config.JsonOutput {
		lines := strings.Split(string(body), "\n")
		servers := make(map[string]map[string]string)
		var currentServer string

		for _, line := range lines {
			if strings.HasPrefix(line, "## Server: ") {
				currentServer = strings.TrimPrefix(line, "## Server: ")
				servers[currentServer] = make(map[string]string)
			} else if currentServer != "" && strings.Contains(line, ": ") {
				parts := strings.SplitN(line, ": ", 2)
				if len(parts) == 2 {
					key := strings.ToLower(parts[0])
					value := strings.Trim(parts[1], "`")
					servers[currentServer][key] = value
				}
			}
		}

		jsonOutput, _ := json.MarshalIndent(servers, "", "  ")
		fmt.Println(string(jsonOutput))
	} else {
		// Return as markdown
		output := string(body)
		if config.MarkdownCode {
			output = "```markdown\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

func listTools(config Config) {
	var endpoint string
	if config.JsonOutput {
		endpoint = "/tools/json"
	} else {
		endpoint = "/tools"
	}

	toolsUrl := buildApiUrl(config, endpoint)
	resp, err := http.Get(toolsUrl)
	if err != nil {
		fmt.Printf("Error connecting to mcpd: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Server returned error: %s\n", resp.Status)
		body, _ := io.ReadAll(resp.Body)
		fmt.Println(string(body))
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}

	if config.JsonOutput {
		// Output is already in JSON format
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, body, "", "  "); err != nil {
			fmt.Println(string(body))
		} else {
			fmt.Println(prettyJSON.String())
		}
	} else {
		// Output is in markdown format
		output := string(body)
		if config.MarkdownCode {
			output = "```markdown\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

func callTool(config Config, serverName, toolName string, params map[string]string) {
	// Build the URL for the tool
	toolUrl := buildApiUrl(config, fmt.Sprintf("/tools/%s/%s", serverName, toolName))
	
	// Check if we have parameters
	if len(params) > 0 {
		queryParams := url.Values{}
		for k, v := range params {
			queryParams.Add(k, v)
		}
		
		// Add query string to the URL
		toolUrl = toolUrl + "?" + queryParams.Encode()
	}
	
	// Make the request
	resp, err := http.Get(toolUrl)
	if err != nil {
		fmt.Printf("Error calling tool: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Tool call failed: %s\n", resp.Status)
		body, _ := io.ReadAll(resp.Body)
		fmt.Println(string(body))
		os.Exit(1)
	}
	
	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		os.Exit(1)
	}
	
	// If json output is requested, try to convert the output to JSON
	if config.JsonOutput {
		// Try to parse as JSON first
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			// It was already valid JSON
			jsonOutput, _ := json.MarshalIndent(jsonData, "", "  ")
			fmt.Println(string(jsonOutput))
		} else {
			// It wasn't JSON, create a JSON object with a text field
			jsonOutput, _ := json.MarshalIndent(map[string]string{"text": string(body)}, "", "  ")
			fmt.Println(string(jsonOutput))
		}
	} else {
		// Output as plain text or markdown
		output := string(body)
		if config.MarkdownCode {
			// Wrap in markdown code block
			output = "```\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}