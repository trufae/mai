package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

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
	} else if config.XmlOutput {
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

		jsonStr, _ := json.Marshal(servers)
		output := jsonToXML(string(jsonStr))
		fmt.Println(output)
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
		fmt.Println(string(body))
		/*
			// Output is already in JSON format
			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, body, "", "  "); err != nil {
				fmt.Println(string(body))
			} else {
				fmt.Println(prettyJSON.String())
			}
		*/
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
		fmt.Println(string(body))
		/*
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
		*/
	} else if config.XmlOutput {
		// Convert JSON to XML
		output := jsonToXML(string(body))
		fmt.Println(output)
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

func listResources(config Config) {
	var endpoint string
	if config.JsonOutput {
		endpoint = "/resources/json"
	} else {
		endpoint = "/resources"
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
	} else if config.XmlOutput {
		// Convert JSON to XML
		output := jsonToXML(string(body))
		fmt.Println(output)
	} else {
		output := string(body)
		if config.MarkdownCode {
			output = "```\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

func readResource(config Config, serverName, resourceURI string) {
	var endpoint string
	if serverName == "" {
		endpoint = fmt.Sprintf("/resources/%s", resourceURI)
	} else {
		endpoint = fmt.Sprintf("/resources/%s/%s", serverName, resourceURI)
	}
	url := buildApiUrl(config, endpoint)

	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}

	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("Error reading resource: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Resource read failed: %s\n", resp.Status)
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
	} else if config.XmlOutput {
		// Convert JSON to XML
		output := jsonToXML(string(body))
		fmt.Println(output)
		return
	}

	output := string(body)

	// Try to parse body as JSON to extract content
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err == nil {
		// If the response contains contents array, extract text for display
		if contents, ok := parsed["contents"].([]interface{}); ok && len(contents) > 0 {
			if content, ok := contents[0].(map[string]interface{}); ok {
				if text, ok := content["text"].(string); ok {
					output = text
				} else if blob, ok := content["blob"].(string); ok {
					output = blob
				}
			}
		}
	}

	if len(output) > 0 && (output[0] == '{' || output[0] == '[') {
		output = jsonToMarkdown(output)
	}
	if config.MarkdownCode {
		output = "```\n" + output + "\n```"
	}
	fmt.Println(output)
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
	} else if config.XmlOutput {
		// Convert JSON to XML
		output := jsonToXML(string(body))
		fmt.Println(output)
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
	} else if config.XmlOutput {
		// Convert JSON to XML
		output := jsonToXML(string(body))
		fmt.Println(output)
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
