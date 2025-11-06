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

// createHttpClient creates an HTTP client with debug transport if needed
func createHttpClient(config Config) *http.Client {
	client := &http.Client{}
	if config.Debug {
		client.Transport = createDebugTransport(config)
	}
	return client
}

// makeGetRequest performs a GET request and returns the response body
func makeGetRequest(config Config, url string) ([]byte, error) {
	debugPrint(config, "Making GET request to: %s", url)

	client := createHttpClient(config)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error connecting to mcpd: %v", err)
	}
	defer resp.Body.Close()

	debugPrint(config, "Response status: %s", resp.Status)
	debugPrint(config, "Response headers: %v", resp.Header)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned error: %s\n%s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	if config.Debug {
		debugPrint(config, "Response body: %s", string(body))
	}

	return body, nil
}

// makePostRequest performs a POST request with JSON parameters and returns the response body
func makePostRequest(config Config, url string, params map[string]interface{}) ([]byte, error) {
	bodyBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("error encoding JSON parameters: %v", err)
	}
	debugPrint(config, "JSON request body: %v", params)

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	debugPrint(config, "Making POST request to: %s", url)

	client := createHttpClient(config)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	debugPrint(config, "Response status: %s", resp.Status)
	debugPrint(config, "Response headers: %v", resp.Header)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed: %s\n%s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	if config.Debug {
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, body, "", "  ") == nil {
			debugPrint(config, "Tool response (JSON): %s", prettyJSON.String())
		} else {
			debugPrint(config, "Tool response: %s", string(body))
		}
	}

	return body, nil
}

// formatServerOutput handles the special formatting for listServers response
func formatServerOutput(config Config, body []byte) {
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
		output := string(body)
		if config.MarkdownCode {
			output = "```markdown\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

// formatStandardOutput handles standard JSON/XML/markdown formatting
func formatStandardOutput(config Config, body []byte) {
	if config.JsonOutput {
		fmt.Println(string(body))
	} else if config.XmlOutput {
		output := jsonToXML(string(body))
		fmt.Println(output)
	} else {
		output := string(body)
		if config.MarkdownCode {
			output = "```markdown\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

// formatJsonPrettyOutput handles JSON with pretty printing fallback
func formatJsonPrettyOutput(config Config, body []byte) {
	if config.JsonOutput {
		var jsonData interface{}
		if err := json.Unmarshal(body, &jsonData); err == nil {
			out, _ := json.MarshalIndent(jsonData, "", "  ")
			fmt.Println(string(out))
		} else {
			fmt.Println(string(body))
		}
	} else if config.XmlOutput {
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

// formatContentOutput handles content extraction from JSON responses
func formatContentOutput(config Config, body []byte) {
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

// formatToolOutput handles complex tool response formatting with pagination
func formatToolOutput(config Config, body []byte) {
	if config.JsonOutput {
		fmt.Println(string(body))
	} else if config.XmlOutput {
		output := jsonToXML(string(body))
		fmt.Println(output)
	} else {
		output := string(body)

		// Try to parse body as JSON to extract pagination metadata
		var parsed map[string]interface{}
		if err := json.Unmarshal(body, &parsed); err == nil {
			// If the response contains content (old style) with content array, extract text for display
			if c, ok := parsed["content"]; ok {
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

		// Check if response appears to be JSON and we didn't already convert
		if len(output) > 0 && output[0] == '{' {
			output = jsonToMarkdown(output)
		}

		if config.MarkdownCode {
			output = "```\n" + output + "\n```"
		}
		fmt.Println(output)
	}
}

func listServers(config Config) {
	statusUrl := buildApiUrl(config, "/status")

	body, err := makeGetRequest(config, statusUrl)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	formatServerOutput(config, body)
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

	body, err := makeGetRequest(config, toolsUrl)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	formatStandardOutput(config, body)
}

func callTool(config Config, serverName, toolName string, params map[string]interface{}) {
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

	var body []byte
	var err error
	if len(params) > 0 {
		body, err = makePostRequest(config, toolUrl, params)
	} else {
		body, err = makeGetRequest(config, toolUrl)
	}

	if err != nil {
		if !config.Quiet {
			fmt.Printf("Error calling tool: %v\n", err)
		}
		os.Exit(1)
	}

	formatToolOutput(config, body)
}

func listResources(config Config) {
	var endpoint string
	if config.JsonOutput {
		endpoint = "/resources/json"
	} else {
		endpoint = "/resources"
	}
	url := buildApiUrl(config, endpoint)

	body, err := makeGetRequest(config, url)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	formatJsonPrettyOutput(config, body)
}

func readResource(config Config, serverName, resourceURI string) {
	var endpoint string
	if serverName == "" {
		endpoint = fmt.Sprintf("/resources/%s", resourceURI)
	} else {
		endpoint = fmt.Sprintf("/resources/%s/%s", serverName, resourceURI)
	}
	url := buildApiUrl(config, endpoint)

	body, err := makeGetRequest(config, url)
	if err != nil {
		fmt.Printf("Error reading resource: %v\n", err)
		os.Exit(1)
	}

	formatContentOutput(config, body)
}

func listPrompts(config Config) {
	var endpoint string
	if config.JsonOutput {
		endpoint = "/prompts/json"
	} else if config.Quiet {
		endpoint = "/prompts/quiet"
	} else {
		endpoint = "/prompts"
	}
	url := buildApiUrl(config, endpoint)

	body, err := makeGetRequest(config, url)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if config.Quiet {
		formatStandardOutput(config, body)
	} else {
		formatJsonPrettyOutput(config, body)
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

	var body []byte
	var err error
	if len(params) > 0 {
		body, err = makePostRequest(config, url, params)
	} else {
		body, err = makeGetRequest(config, url)
	}
	if err != nil {
		fmt.Printf("Error calling prompt: %v\n", err)
		os.Exit(1)
	}

	formatContentOutput(config, body)
}
