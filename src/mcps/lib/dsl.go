package mcplib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func tokenize(s string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inQuotes := false
	escaped := false
	depth := 0

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
			current.WriteByte(ch)
		case ch == '\\' && inQuotes:
			escaped = true
			current.WriteByte(ch)
		case ch == '"':
			inQuotes = !inQuotes
			current.WriteByte(ch)
		case !inQuotes && (ch == '[' || ch == '{'):
			depth++
			current.WriteByte(ch)
		case !inQuotes && (ch == ']' || ch == '}'):
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unexpected %q", ch)
			}
			current.WriteByte(ch)
		case (ch == ' ' || ch == '\t') && !inQuotes && depth == 0:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if inQuotes {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	if depth != 0 {
		return nil, fmt.Errorf("unterminated JSON value")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}

// RunDSLTests executes DSL commands for testing tools
func RunDSLTests(tools []Tool, dsl string) error {
	contextTools := make([]ToolWithContext, 0, len(tools))
	for _, tool := range tools {
		handler := tool.Handler
		contextTools = append(contextTools, ToolWithContext{
			Name:          tool.Name,
			Description:   tool.Description,
			InputSchema:   tool.InputSchema,
			UsageExamples: tool.UsageExamples,
			Handler: func(_ context.Context, args map[string]interface{}) (interface{}, error) {
				return handler(args)
			},
		})
	}
	return runDSL(context.Background(), contextTools, dsl, true)
}

// RunDSL executes semicolon-separated tool commands using the compact syntax
// "tool key=value key2=value2". Values may be quoted strings, numbers,
// booleans, null, JSON arrays, or JSON objects.
func RunDSL(ctx context.Context, tools []ToolWithContext, dsl string) error {
	return runDSL(ctx, tools, dsl, false)
}

func runDSL(ctx context.Context, tools []ToolWithContext, dsl string, continueOnToolError bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	toolMap := make(map[string]ToolHandlerWithContext, len(tools))
	for _, tool := range tools {
		toolMap[tool.Name] = tool.Handler
	}
	statements, err := splitDSLStatements(dsl)
	if err != nil {
		return err
	}
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		parts, err := tokenize(stmt)
		if err != nil {
			return fmt.Errorf("invalid DSL command: %w", err)
		}
		if len(parts) == 0 {
			continue
		}
		toolName := parts[0]
		handler, exists := toolMap[toolName]
		if !exists {
			return fmt.Errorf("unknown tool: %s", toolName)
		}
		args := make(map[string]interface{})
		for _, part := range parts[1:] {
			key, value, ok := strings.Cut(part, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				return fmt.Errorf("invalid argument %q for %s; expected key=value", part, toolName)
			}
			parsed, err := parseDSLValue(strings.TrimSpace(value))
			if err != nil {
				return fmt.Errorf("invalid %s value for %s: %w", key, toolName, err)
			}
			args[key] = parsed
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := handler(ctx, args)
		if err != nil {
			if continueOnToolError {
				fmt.Fprintf(os.Stderr, "[DSL] %s -> ERROR: %v\n", toolName, err)
				continue
			}
			return fmt.Errorf("%s: %w", toolName, err)
		}
		printDSLResult(result)
	}
	return nil
}

func splitDSLStatements(dsl string) ([]string, error) {
	var statements []string
	var current strings.Builder
	inQuotes := false
	escaped := false
	depth := 0
	for i := 0; i < len(dsl); i++ {
		ch := dsl[i]
		switch {
		case escaped:
			escaped = false
			current.WriteByte(ch)
		case ch == '\\' && inQuotes:
			escaped = true
			current.WriteByte(ch)
		case ch == '"':
			inQuotes = !inQuotes
			current.WriteByte(ch)
		case !inQuotes && (ch == '[' || ch == '{'):
			depth++
			current.WriteByte(ch)
		case !inQuotes && (ch == ']' || ch == '}'):
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unexpected %q", ch)
			}
			current.WriteByte(ch)
		case ch == ';' && !inQuotes && depth == 0:
			statements = append(statements, current.String())
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}
	if inQuotes {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	if depth != 0 {
		return nil, fmt.Errorf("unterminated JSON value")
	}
	statements = append(statements, current.String())
	return statements, nil
}

func parseDSLValue(value string) (interface{}, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "\"") {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	}
	if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		var parsed interface{}
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, err
		}
		return parsed, nil
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		return number, nil
	}
	return value, nil
}

func printDSLResult(result interface{}) {
	switch v := result.(type) {
	case string:
		fmt.Println(v)
	case ToolCallResult:
		if v.StructuredContent != nil {
			if jsonData, err := json.MarshalIndent(v.StructuredContent, "", "  "); err == nil {
				fmt.Println(string(jsonData))
				return
			}
		}
		if v.Content != nil {
			if contentSlice, ok := v.Content.([]interface{}); ok && len(contentSlice) > 0 {
				if textMap, ok := contentSlice[0].(map[string]interface{}); ok {
					if text, ok := textMap["text"].(string); ok {
						fmt.Println(text)
						return
					}
				}
			}
			if jsonData, err := json.MarshalIndent(v.Content, "", "  "); err == nil {
				fmt.Println(string(jsonData))
				return
			}
		}
		fmt.Printf("%+v\n", v)
	case map[string]interface{}:
		if structuredContent, ok := v["structuredContent"]; ok {
			if jsonData, err := json.MarshalIndent(structuredContent, "", "  "); err == nil {
				fmt.Println(string(jsonData))
				return
			}
		}
		if content, ok := v["content"]; ok {
			if contentSlice, ok := content.([]interface{}); ok && len(contentSlice) > 0 {
				if textMap, ok := contentSlice[0].(map[string]interface{}); ok {
					if text, ok := textMap["text"].(string); ok {
						fmt.Println(text)
						return
					}
				}
			}
		}
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
