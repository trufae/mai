package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

// jsonToMarkdown converts a JSON string to a simple markdown representation
func jsonToMarkdown(jsonStr string) string {
	var data interface{}
	err := json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		return jsonStr // Return original if not valid JSON
	}

	return formatJSON(data, 0)
}

// jsonToXML converts a JSON string to XML
func jsonToXML(jsonStr string) string {
	var data interface{}
	err := json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		return jsonStr // Return original if not valid JSON
	}

	return formatXML(data, "root")
}

// formatXML recursively formats JSON data as XML
func formatXML(data interface{}, tag string) string {
	var sb strings.Builder

	switch v := data.(type) {
	case map[string]interface{}:
		sb.WriteString("<" + tag + ">")
		for key, value := range v {
			sb.WriteString(formatXML(value, key))
		}
		sb.WriteString("</" + tag + ">")
	case []interface{}:
		for _, item := range v {
			sb.WriteString(formatXML(item, tag))
		}
	default:
		sb.WriteString("<" + tag + ">")
		sb.WriteString(fmt.Sprintf("%v", v))
		sb.WriteString("</" + tag + ">")
	}

	return sb.String()
}

// formatJSON recursively formats JSON data as markdown text
func formatJSON(data interface{}, indent int) string {
	var sb strings.Builder

	switch v := data.(type) {
	case map[string]interface{}:
		// If empty object
		if len(v) == 0 {
			return "{}"
		}

		// Process each key-value pair in the object
		for key, value := range v {
			if key != "text" && key != "type" && key != "content" {
				// sb.WriteString(indentStr)
				sb.WriteString(key)
				sb.WriteString(": ")
			}

			// Format the value based on its type
			switch val := value.(type) {
			case map[string]interface{}, []interface{}:
				// For nested objects and arrays, add newline and format with increased indent
				sb.WriteString(formatJSON(val, indent+1))
			default:
				// For primitive values, format inline
				if val != "text" {
					sb.WriteString(fmt.Sprintf("%v\n", val))
				}
			}
		}
	case []interface{}:
		// If empty array
		if len(v) == 0 {
			return "[]"
		}

		// Process each item in the array
		for _, item := range v {
			// sb.WriteString(indentStr)
			// sb.WriteString("- ")

			// Format the item based on its type
			switch val := item.(type) {
			case map[string]interface{}, []interface{}:
				// For nested objects and arrays, add newline and format with increased indent
				sb.WriteString(formatJSON(val, indent+1))
			default:
				// For primitive values, format inline
				sb.WriteString(fmt.Sprintf("%v ", val))
			}
		}
	default:
		// Handle primitive types
		sb.WriteString(fmt.Sprintf("%v", v))
	}

	return sb.String()
}

// Config holds the application configuration
type Config struct {
	BaseURL      string
	JsonOutput   bool
	XmlOutput    bool
	MarkdownCode bool
	Quiet        bool
	Simple       bool
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
	fmt.Println("  -h            Show this help message")
	fmt.Println("\nCommands:")
	fmt.Println("  list                           List all available tools")
	fmt.Println("  servers                        List all available servers")
	fmt.Println("  call <server> <tool> [params]  Call a specific tool")
	fmt.Println("  prompts [list]                 List all available prompts")
	fmt.Println("  prompts get <server>/<name>    Render a prompt (accepts params)")
	fmt.Println("\nExamples:")
	fmt.Println("  mai-tool list")
	fmt.Println("  mai-tool -j list")
	fmt.Println("  mai-tool call server1 mytool param1=value1 param2=value2")
	fmt.Println("  mai-tool call server1/mytool param1=value1 param2=value2")
	fmt.Println("  mai-tool call server1 mytool \"text=value with spaces\"")
	fmt.Println("  mai-tool prompts list")
	fmt.Println("  mai-tool prompts get server1/welcome topic=onboarding")
	fmt.Println("  MAI_TOOL_BASEURL=http://remote:9000 mai-tool list")
}

func parseParams(args []string) map[string]interface{} {
	params := make(map[string]interface{})

	// Support both named parameters (name=value) and positional arguments.
	// Positional arguments (without '=') are encoded as numeric keys: "0", "1", ...
	posIndex := 0
	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) == 2 {
			val := parts[1]
			// Try parse JSON value for complex types
			if strings.HasPrefix(val, "{") || strings.HasPrefix(val, "[") {
				var vv interface{}
				if err := json.Unmarshal([]byte(val), &vv); err == nil {
					params[parts[0]] = vv
					continue
				}
			}
			// Try number
			if num, err := strconv.ParseFloat(val, 64); err == nil {
				// If integer-like, store as int
				if float64(int64(num)) == num {
					params[parts[0]] = int(num)
				} else {
					params[parts[0]] = num
				}
				continue
			}
			// Try bool
			if b, err := strconv.ParseBool(val); err == nil {
				params[parts[0]] = b
				continue
			}
			params[parts[0]] = val
		} else {
			// positional
			params[fmt.Sprintf("%d", posIndex)] = arg
			posIndex++
		}
	}

	return params
}

func buildApiUrl(config Config, path string) string {
	url := config.BaseURL + path
	if config.Debug {
		fmt.Fprintf(os.Stderr, "DEBUG: Request URL: %s\n", url)
	}
	return url
}

func listPrompts(config Config) {
	var endpoint string
	if config.JsonOutput {
		endpoint = "/prompts/json"
	} else {
		endpoint = "/prompts"
	}
	url := buildApiUrl(config, endpoint)

	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}

	resp, err := client.Get(url)
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
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			out, _ := json.MarshalIndent(jsonData, "", "  ")
			fmt.Println(string(out))
		} else {
			fmt.Println(string(body))
		}
	} else {
		output := string(body)
		if config.MarkdownCode {
			output = "```\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

func getPrompt(config Config, serverName, promptName string, params map[string]interface{}) {
	var endpoint string
	if serverName == "" {
		endpoint = fmt.Sprintf("/prompts/%s", promptName)
	} else {
		endpoint = fmt.Sprintf("/prompts/%s/%s", serverName, promptName)
	}
	url := buildApiUrl(config, endpoint)

	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}

	var resp *http.Response
	var err error
	if len(params) > 0 {
		bodyBytes, err := json.Marshal(params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON parameters: %v\n", err)
			os.Exit(1)
		}
		req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
	} else {
		resp, err = client.Get(url)
	}
	if err != nil {
		fmt.Printf("Error calling prompt: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Prompt get failed: %s\n", resp.Status)
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
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			out, _ := json.MarshalIndent(jsonData, "", "  ")
			fmt.Println(string(out))
		} else {
			out, _ := json.MarshalIndent(map[string]string{"text": string(body)}, "", "  ")
			fmt.Println(string(out))
		}
		return
	}

	output := string(body)
	if len(output) > 0 && (output[0] == '{' || output[0] == '[') {
		output = jsonToMarkdown(output)
	}
	if config.MarkdownCode {
		output = "```\n" + output + "\n```"
	}
	fmt.Println(output)
}

// createDebugTransport returns an http.RoundTripper that logs requests and responses
func createDebugTransport(config Config) http.RoundTripper {
	return &debugTransport{
		config:    config,
		transport: http.DefaultTransport,
	}
}

// debugTransport implements http.RoundTripper interface
type debugTransport struct {
	config    Config
	transport http.RoundTripper
}

// RoundTrip logs the request and response for debugging
func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Log request details
	debugPrint(d.config, "HTTP Request: %s %s", req.Method, req.URL.String())
	debugPrint(d.config, "Request headers: %v", req.Header)

	// Execute the request
	resp, err := d.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Log response details
	debugPrint(d.config, "Response status: %s", resp.Status)
	debugPrint(d.config, "Response headers: %v", resp.Header)

	return resp, nil
}

// debugPrint outputs debug information if debug mode is enabled
func debugPrint(config Config, format string, args ...interface{}) {
	if config.Debug {
		// Check if any argument is a map, slice, or struct that should be pretty printed
		formattedArgs := make([]interface{}, len(args))
		for i, arg := range args {
			switch v := arg.(type) {
			case map[string]interface{}, []interface{}, map[string]string:
				// Pretty print JSON objects
				b, err := json.MarshalIndent(v, "", "  ")
				if err == nil {
					formattedArgs[i] = "\n" + string(b)
				} else {
					formattedArgs[i] = v
				}
			case http.Header:
				// Format HTTP headers nicely
				var sb strings.Builder
				sb.WriteString("\n")
				for k, vals := range v {
					fmt.Fprintf(&sb, "  %s: %s\n", k, strings.Join(vals, ", "))
				}
				formattedArgs[i] = sb.String()
			default:
				formattedArgs[i] = arg
			}
		}

		fmt.Fprintf(os.Stderr, "DEBUG: "+format+"\n", formattedArgs...)
	}
}

func listServers(config Config) {
	// Get the status endpoint which lists servers
	statusUrl := buildApiUrl(config, "/status")

	debugPrint(config, "Making GET request to: %s", statusUrl)

	// Create HTTP client with debug transport if needed
	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}

	// Execute the request
	resp, err := client.Get(statusUrl)
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
	if config.JsonOutput || config.XmlOutput {
		endpoint = "/tools/json"
	} else if config.Simple {
		endpoint = "/tools/simple"
	} else if config.Quiet {
		endpoint = "/tools/quiet"
	} else {
		endpoint = "/tools"
	}

	toolsUrl := buildApiUrl(config, endpoint)

	debugPrint(config, "Making GET request to: %s", toolsUrl)

	// Create HTTP client with debug transport if needed
	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}

	// Execute the request
	resp, err := client.Get(toolsUrl)
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
	} else if config.XmlOutput {
		// Convert JSON to XML
		output := jsonToXML(string(body))
		fmt.Println(output)
	} else {
		// Output is in markdown format
		output := string(body)
		if config.MarkdownCode {
			output = "```markdown\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

func callTool(config Config, serverName, toolName string, params map[string]interface{}) {
	var resp *http.Response
	var requestErr error
	// fmt.Println("server: "+serverName)
	// fmt.Println("tool: "+toolName)

	// If serverName contains a slash, it's in the format "server/tool"
	if strings.Contains(serverName, "/") {
		// Split serverName into server and tool parts
		parts := strings.SplitN(serverName, "/", 2)
		// Override serverName with just the server part
		serverName = parts[0]
		// Use the tool part from serverName and ignore the separate toolName parameter
		toolName = parts[1]
	}

	// Standard tool call for other tools
	var endpoint string
	if serverName == "" {
		endpoint = fmt.Sprintf("/call/%s", toolName)
	} else {
		// Always use /call endpoint for tool calls
		endpoint = fmt.Sprintf("/call/%s/%s", serverName, toolName)
	}

	// Build the tool URL
	toolUrl := buildApiUrl(config, endpoint)
	// fmt.Println (toolUrl)

	// Create HTTP client with debug transport if needed
	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}
	if len(params) > 0 {
		// JSON POST request with arguments in body for multiline support
		bodyBytes, err := json.Marshal(params)
		if err != nil {
			if !config.Quiet {
				fmt.Fprintf(os.Stderr, "Error encoding JSON parameters: %v\n", err)
			}
			os.Exit(1)
		}
		debugPrint(config, "JSON request body: %v", params)
		req, err := http.NewRequest("POST", toolUrl, bytes.NewReader(bodyBytes))
		if err != nil {
			if !config.Quiet {
				fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			}
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/json")
		debugPrint(config, "Making POST request to: %s", toolUrl)
		resp, requestErr = client.Do(req)
	} else {
		// Fallback to GET request for no parameters
		debugPrint(config, "Making GET request to: %s", toolUrl)
		resp, requestErr = client.Get(toolUrl)
	}

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
		// Log detailed response information
		debugPrint(config, "Response content type: %s", resp.Header.Get("Content-Type"))
		debugPrint(config, "Response content length: %d", resp.ContentLength)

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

		// Try to parse body as JSON to extract pagination metadata
		var parsed map[string]interface{}
		if err := json.Unmarshal(body, &parsed); err == nil {
			// If the response contains content (old style) with content array, extract text for display
			if c, ok := parsed["content"]; ok {
				// If content is array of {type,text}
				if arr, ok := c.([]interface{}); ok {
					var b strings.Builder
					for i, it := range arr {
						if m, mok := it.(map[string]interface{}); mok {
							if t, tok := m["text"].(string); tok {
								if i > 0 {
									b.WriteString("\n\n")
								}
								b.WriteString(t)
							}
						}
					}
					output = b.String()
				}
			}
			// Compute pages left if present
			page := 0
			total := 0
			if v, ok := parsed["page"]; ok {
				switch vv := v.(type) {
				case float64:
					page = int(vv)
				case int:
					page = vv
				}
			}
			if v, ok := parsed["totalPages"]; ok {
				switch vv := v.(type) {
				case float64:
					total = int(vv)
				case int:
					total = vv
				}
			}
			if total > 0 && page > 0 && total > page {
				pagesLeft := total - page
				output = output + fmt.Sprintf("\n\nPages left: %d", pagesLeft)
				if token, ok := parsed["next_page_token"].(string); ok && token != "" {
					output = output + fmt.Sprintf(" (next_page_token: %s)", token)
				}
			}
		}

		// Check if response appears to be JSON (starts with '{') and we didn't already convert
		if len(output) > 0 && output[0] == '{' {
			// Convert JSON to markdown format
			output = jsonToMarkdown(output)
		}

		if config.MarkdownCode {
			// Wrap in markdown code block
			output = "```\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}
