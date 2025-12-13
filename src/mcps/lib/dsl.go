package mcplib

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RunDSLTests executes DSL commands for testing tools
func RunDSLTests(tools []Tool, dsl string) error {
	// Create a map of tool names to handlers for quick lookup
	toolMap := make(map[string]ToolHandler)
	for _, tool := range tools {
		toolMap[tool.Name] = tool.Handler
	}

	// Split DSL by semicolons and execute each statement
	statements := strings.Split(dsl, ";")
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// Parse statement: toolName key1=val1 key2=val2 ...
		parts := strings.Fields(stmt)
		if len(parts) == 0 {
			continue
		}

		toolName := parts[0]
		handler, exists := toolMap[toolName]
		if !exists {
			return fmt.Errorf("unknown tool: %s", toolName)
		}

		// Parse arguments
		args := make(map[string]interface{})
		for _, part := range parts[1:] {
			if strings.Contains(part, "=") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) == 2 {
					key := strings.TrimSpace(kv[0])
					val := strings.TrimSpace(kv[1])

					// Handle quoted strings
					if strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") && len(val) >= 2 {
						val = val[1 : len(val)-1]
					}

					// Try to parse as number or boolean
					if numVal, err := strconv.ParseFloat(val, 64); err == nil {
						args[key] = numVal
					} else if val == "true" {
						args[key] = true
					} else if val == "false" {
						args[key] = false
					} else {
						args[key] = val
					}
				}
			}
		}

		// Execute the tool
		result, err := handler(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DSL] %s -> ERROR: %v\n", toolName, err)
			continue
		}

		// Print result
		switch v := result.(type) {
		case string:
			fmt.Println(v)
		case map[string]interface{}:
			if content, ok := v["content"]; ok {
				if contentSlice, ok := content.([]interface{}); ok && len(contentSlice) > 0 {
					if textMap, ok := contentSlice[0].(map[string]interface{}); ok {
						if text, ok := textMap["text"].(string); ok {
							fmt.Println(text)
							continue
						}
					}
				}
			}
			// Fallback: marshal to JSON
			if jsonData, err := json.MarshalIndent(v, "", "  "); err == nil {
				fmt.Println(string(jsonData))
			} else {
				fmt.Println(v)
			}
		default:
			if jsonData, err := json.MarshalIndent(v, "", "  "); err == nil {
				fmt.Println(string(jsonData))
			} else {
				fmt.Println(v)
			}
		}
	}

	return nil
}
