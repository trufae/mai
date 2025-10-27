package main

import (
	"encoding/json"
	"fmt"
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

func buildApiUrl(config Config, path string) string {
	url := config.BaseURL + path
	if config.Debug {
		fmt.Fprintf(os.Stderr, "DEBUG: Request URL: %s\n", url)
	}
	return url
}

func parseParams(args []string) map[string]interface{} {
	// Special case: if there's exactly one argument and it starts with '{',
	// treat it as a JSON object containing all parameters
	if len(args) == 1 && strings.HasPrefix(args[0], "{") {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(args[0]), &params); err == nil {
			return params
		}
		// If JSON parsing fails, fall back to normal parsing
	}

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
