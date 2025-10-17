package mcp

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ToolInfo represents information about an MCP tool
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPServerInfo represents information about an MCP server
type MCPServerInfo struct {
	Name  string     `json:"name"`
	Tools []ToolInfo `json:"tools"`
}

// InspectMCPServer inspects an MCP server and returns its tools
func InspectMCPServer(serverName string) (*MCPServerInfo, error) {
	// Run mai-wmcp -tj <server>
	cmd := exec.Command("mai-wmcp", "-tj", serverName)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run mai-wmcp: %v", err)
	}

	// Parse JSON output
	var tools []ToolInfo
	if err := json.Unmarshal(output, &tools); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	return &MCPServerInfo{
		Name:  serverName,
		Tools: tools,
	}, nil
}

// GetToolNames returns a list of tool names for an MCP server
func (info *MCPServerInfo) GetToolNames() []string {
	names := make([]string, len(info.Tools))
	for i, tool := range info.Tools {
		names[i] = tool.Name
	}
	return names
}

// FilterToolsByCapability filters tools based on capability keywords
func (info *MCPServerInfo) FilterToolsByCapability(capability string) []ToolInfo {
	var filtered []ToolInfo
	lowerCap := strings.ToLower(capability)

	for _, tool := range info.Tools {
		descLower := strings.ToLower(tool.Description)
		if strings.Contains(descLower, lowerCap) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

// SuggestPseudoMCPs suggests pseudo-MCPs based on tool capabilities
func (info *MCPServerInfo) SuggestPseudoMCPs() map[string][]string {
	pseudoMCPs := make(map[string][]string)

	// Define capability categories
	categories := map[string][]string{
		"code":   {"code", "programming", "syntax", "debug", "compile"},
		"search": {"search", "find", "query", "lookup"},
		"file":   {"file", "read", "write", "edit", "create"},
		"shell":  {"shell", "command", "execute", "run"},
		"web":    {"web", "http", "url", "browse", "scrape"},
		"time":   {"time", "date", "calendar", "schedule"},
		"math":   {"math", "calculate", "compute", "equation"},
	}

	for category, keywords := range categories {
		var tools []string
		for _, tool := range info.Tools {
			descLower := strings.ToLower(tool.Description)
			for _, keyword := range keywords {
				if strings.Contains(descLower, keyword) {
					tools = append(tools, tool.Name)
					break
				}
			}
		}
		if len(tools) > 0 {
			pseudoMCPs[category] = tools
		}
	}

	return pseudoMCPs
}
