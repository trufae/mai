package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Tool represents an MCP tool
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Arguments   map[string]interface{} `json:"arguments"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// ServerTools represents tools available on a specific server
type ServerTools map[string][]Tool

// ToolResponse represents a response from a tool execution
type ToolResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error,omitempty"`
}

// Config holds the application configuration
type Config struct {
	Host         string
	Port         string
	JsonOutput   bool
	MarkdownCode bool
	Quiet        bool
	Debug        bool
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
	host := flag.String("host", "localhost", "Host where mcpd is running")
	port := flag.String("p", "8080", "Port where mcpd is running")
	jsonOutput := flag.Bool("j", false, "Output in JSON format")
	markdownCode := flag.Bool("m", false, "Wrap markdown output in code blocks")
	quiet := flag.Bool("q", false, "Suppress non-essential output")
	debug := flag.Bool("d", false, "Enable debug mode to show HTTP requests and JSON payloads")
	help := flag.Bool("h", false, "Show help message")

	flag.Parse()

	if *help {
		printUsage()
		os.Exit(0)
	}

	return Config{
		Host:         *host,
		Port:         *port,
		JsonOutput:   *jsonOutput,
		MarkdownCode: *markdownCode,
		Quiet:        *quiet,
		Debug:        *debug,
	}
}

func printUsage() {
	fmt.Println("Usage: mcpcli [options] <command>")
	fmt.Println("\nOptions:")
	fmt.Println("  --host <host>  Host where mcpd is running (default: localhost)")
	fmt.Println("  -p <port>     Port where mcpd is running (default: 8080)")
	fmt.Println("  -j            Output in JSON format")
	fmt.Println("  -m            Wrap markdown output in code blocks")
	fmt.Println("  -q            Suppress non-essential output")
	fmt.Println("  -d            Enable debug mode to show HTTP requests and JSON payloads")
	fmt.Println("  -h            Show this help message")
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
	url := fmt.Sprintf("http://%s:%s%s", config.Host, config.Port, path)
	if config.Debug {
		fmt.Fprintf(os.Stderr, "DEBUG: Request URL: %s\n", url)
	}
	return url
}

// debugPrint outputs debug information if debug mode is enabled
func debugPrint(config Config, format string, args ...interface{}) {
	if config.Debug {
		fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", args...)
	}
}

func listServers(config Config) {
	// Get the status endpoint which lists servers
	statusUrl := buildApiUrl(config, "/status")

	debugPrint(config, "Making GET request to: %s", statusUrl)
	resp, err := http.Get(statusUrl)
	if err != nil {
		fmt.Printf("Error connecting to mcpd: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	debugPrint(config, "Response status: %s", resp.Status)
	debugPrint(config, "Response headers: %v", resp.Header)

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

	if config.Debug {
		debugPrint(config, "Response body: %s", string(body))
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
	} else if config.Quiet {
		endpoint = "/tools/quiet"
	} else {
		endpoint = "/tools"
	}

	toolsUrl := buildApiUrl(config, endpoint)

	debugPrint(config, "Making GET request to: %s", toolsUrl)
	resp, err := http.Get(toolsUrl)
	if err != nil {
		fmt.Printf("Error connecting to mcpd: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	debugPrint(config, "Response status: %s", resp.Status)
	debugPrint(config, "Response headers: %v", resp.Header)

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

	if config.Debug {
		debugPrint(config, "Response body: %s", string(body))
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
	var resp *http.Response
	var requestErr error

	// Standard tool call for other tools
	var endpoint string
	if config.Quiet {
		endpoint = fmt.Sprintf("/tools/quiet/%s/%s", serverName, toolName)
	} else {
		endpoint = fmt.Sprintf("/tools/%s/%s", serverName, toolName)
	}

	// Build the tool URL
	toolUrl := buildApiUrl(config, endpoint)
	fmt.Println (toolUrl)

	// Prepare parameters as query params
	queryParams := make([]string, 0, len(params))
	for k, v := range params {
		queryParams = append(queryParams, fmt.Sprintf("%s=%s", k, v))
	}

	if len(queryParams) > 0 {
		toolUrl = toolUrl + "?" + strings.Join(queryParams, "&")
	}

	// Debug logging for parameters
	if config.Debug {
		debugPrint(config, "Query parameters: %v", queryParams)
	}

	// Make GET request
	debugPrint(config, "Making GET request to: %s", toolUrl)
	resp, requestErr = http.Get(toolUrl)

	// Handle request errors
	if requestErr != nil {
		if !config.Quiet {
			fmt.Printf("Error calling tool: %v\n", requestErr)
		}
		os.Exit(1)
	}
	defer resp.Body.Close()

	debugPrint(config, "Response status: %s", resp.Status)
	debugPrint(config, "Response headers: %v", resp.Header)

	if resp.StatusCode != http.StatusOK {
		if !config.Quiet {
			fmt.Printf("Tool call failed: %s\n", resp.Status)
			body, _ := io.ReadAll(resp.Body)
			fmt.Println(string(body))
		}
		os.Exit(1)
	}

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if !config.Quiet {
			fmt.Printf("Error reading response: %v\n", err)
		}
		os.Exit(1)
	}

	// Debug logging for response
	if config.Debug {
		// Try to pretty print JSON response
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, body, "", "  ") == nil {
			debugPrint(config, "Tool response (JSON): %s", prettyJSON.String())
		} else {
			// If not valid JSON, print as string
			debugPrint(config, "Tool response: %s", string(body))
		}
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
