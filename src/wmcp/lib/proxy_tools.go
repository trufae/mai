package wmcplib

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ProxyToolSearchName is the name of the virtual tool used to discover the
// real underlying tools when ProxyToolsMode is enabled.
const ProxyToolSearchName = "search-tools"

// ProxyToolCallName is the name of the virtual tool used to invoke a real
// underlying tool when ProxyToolsMode is enabled.
const ProxyToolCallName = "call-tool"

// ProxyTools returns the two virtual tools (search-tools, call-tool) that the
// bridge exposes in place of the full aggregated catalog when
// ProxyToolsMode is on. Agents see only these; they discover real tools by
// calling search-tools and invoke them through call-tool.
func ProxyTools() []Tool {
	searchSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Space/comma separated keywords to match tool names and descriptions. An empty query returns every tool.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of tools to return (default 20, 0 = unlimited).",
			},
		},
		"required": []interface{}{"query"},
	}
	callSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Tool name as reported by search-tools (e.g. 'server::tool').",
			},
			"arguments": map[string]interface{}{
				"type":                 "object",
				"description":          "Arguments object for the underlying tool. Keys match the target tool's inputSchema properties.",
				"additionalProperties": true,
			},
		},
		"required": []interface{}{"name"},
	}
	return []Tool{
		{
			Name:        ProxyToolSearchName,
			Description: "Search the catalog of real tools by keyword. Returns full tool definitions (name, description, inputSchema, parameters) for every match so you can then invoke them via call-tool.",
			InputSchema: searchSchema,
			Parameters: []ToolParameter{
				{Name: "query", Description: "Space/comma separated keywords. Empty string returns everything.", Type: "string", Required: true},
				{Name: "limit", Description: "Maximum number of tools to return (default 20, 0 = unlimited).", Type: "integer", Required: false},
			},
		},
		{
			Name:        ProxyToolCallName,
			Description: "Invoke one of the real underlying tools by name with a JSON arguments object. Use search-tools first to obtain the exact name and parameter schema.",
			InputSchema: callSchema,
			Parameters: []ToolParameter{
				{Name: "name", Description: "Tool name as reported by search-tools.", Type: "string", Required: true},
				{Name: "arguments", Description: "Arguments object for the target tool.", Type: "object", Required: false},
			},
		},
	}
}

// splitSearchQuery tokenizes a free-form query using whitespace and common
// separators (comma, semicolon, pipe). Empty tokens are dropped.
func splitSearchQuery(q string) []string {
	if strings.TrimSpace(q) == "" {
		return nil
	}
	fields := strings.FieldsFunc(q, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', ';', '|':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// SearchAggregatedTools filters the aggregated tool list using the supplied
// query and returns matches ranked by how many tokens they hit. An empty
// query returns every aggregated tool. A non-positive limit is treated as
// "no limit".
func (s *MCPService) SearchAggregatedTools(query string, limit int) []Tool {
	all := s.AggregateToolList()
	tokens := splitSearchQuery(query)

	if len(tokens) == 0 {
		if limit > 0 && len(all) > limit {
			return all[:limit]
		}
		return all
	}

	type scored struct {
		tool  Tool
		score int
	}
	matches := make([]scored, 0, len(all))
	for _, t := range all {
		haystack := strings.ToLower(t.Name + "\n" + t.Description)
		score := 0
		for _, tok := range tokens {
			if strings.Contains(haystack, tok) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{tool: t, score: score})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].tool.Name < matches[j].tool.Name
	})

	if limit <= 0 {
		limit = len(matches)
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}

	out := make([]Tool, len(matches))
	for i := range matches {
		out[i] = matches[i].tool
	}
	return out
}

// IsProxyToolName reports whether name is one of the virtual proxy tools.
func IsProxyToolName(name string) bool {
	return name == ProxyToolSearchName || name == ProxyToolCallName
}

// coerceSearchLimit accepts the various JSON-decoded numeric types and
// returns an int limit. Unknown types fall back to the default.
func coerceSearchLimit(v interface{}, defaultLimit int) int {
	switch n := v.(type) {
	case nil:
		return defaultLimit
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case string:
		if n == "" {
			return defaultLimit
		}
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return defaultLimit
}

// HandleProxySearchTools executes the search-tools virtual tool and returns
// a CallToolResult whose single text-content entry is the JSON-encoded list
// of matching Tool definitions. The ID is copied from the caller.
func (s *MCPService) HandleProxySearchTools(arguments map[string]interface{}) CallToolResult {
	query, _ := arguments["query"].(string)
	limit := coerceSearchLimit(arguments["limit"], 20)

	matches := s.SearchAggregatedTools(query, limit)

	payload := map[string]interface{}{
		"query":   query,
		"count":   len(matches),
		"tools":   matches,
		"total":   len(s.AggregateToolList()),
		"limited": limit > 0 && len(matches) == limit,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return CallToolResult{Error: &CallToolError{Message: fmt.Sprintf("failed to encode tool search result: %v", err)}}
	}
	return CallToolResult{
		Content: []Content{{Type: "text", Text: string(data)}},
	}
}

// ExtractProxyCallArguments pulls out the (name, arguments) pair from a
// call-tool invocation. Callers supply the raw "arguments" map delivered by
// MCP.
func ExtractProxyCallArguments(arguments map[string]interface{}) (string, map[string]interface{}, error) {
	name, _ := arguments["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("call-tool: 'name' is required")
	}
	if IsProxyToolName(name) {
		return "", nil, fmt.Errorf("call-tool: refusing to recursively invoke '%s'", name)
	}
	var inner map[string]interface{}
	switch v := arguments["arguments"].(type) {
	case nil:
		inner = map[string]interface{}{}
	case map[string]interface{}:
		inner = v
	default:
		return "", nil, fmt.Errorf("call-tool: 'arguments' must be an object")
	}
	return name, inner, nil
}
