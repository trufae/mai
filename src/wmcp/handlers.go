package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// normalizeToolName normalizes a tool name for drunk mode comparison
func normalizeToolName(name string) string {
	// Remove underscores and convert to lowercase
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

// findBestToolMatch tries to resolve a requested tool name to an actual tool
// name from the provided slice. Matching is strict by default; when drunk
// mode is enabled it will try normalized equality and fuzzy substring-based
// matches. It returns the matched tool name and true, or empty/false when
// nothing matched.
func findBestToolMatch(tools []Tool, requested string, drunk bool) (string, bool) {
	// Fast path: exact match
	for _, t := range tools {
		if t.Name == requested {
			return t.Name, true
		}
	}

	if !drunk {
		return "", false
	}

	reqNorm := normalizeToolName(requested)
	bestScore := 1 << 60
	bestName := ""

	for _, t := range tools {
		act := t.Name
		actNorm := normalizeToolName(act)

		// normalized exact
		if actNorm == reqNorm {
			return act, true
		}

		// prefer matches where one contains the other; shorter difference is better
		if strings.Contains(actNorm, reqNorm) {
			score := 100 + (len(actNorm) - len(reqNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}
		if strings.Contains(reqNorm, actNorm) {
			score := 200 + (len(reqNorm) - len(actNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}

		// fallback: prefix/suffix heuristics
		if strings.HasPrefix(actNorm, reqNorm) || strings.HasSuffix(actNorm, reqNorm) {
			score := 300 + (len(actNorm) - len(reqNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}
		if strings.HasPrefix(reqNorm, actNorm) || strings.HasSuffix(reqNorm, actNorm) {
			score := 400 + (len(reqNorm) - len(actNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}
	}

	if bestName != "" {
		return bestName, true
	}
	return "", false
}

// debugLog prints debug logs when debug mode is enabled
func debugLog(debug bool, format string, args ...interface{}) {
	if debug {
		log.Printf("DEBUG: "+format, args...)
	}
}

// HTTP Handlers

// listToolsHandler returns all tools from all servers
func (s *MCPService) listToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("# Tools Catalog\n\n")

	for _ /*serverName */, server := range s.servers {
		// output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		server.mutex.RLock()
		// output.WriteString(fmt.Sprintf("Executable: `%s`\n", server.Command))
		// output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))

		for _, tool := range server.Tools {
			// output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			// output.WriteString(fmt.Sprintf("ToolName: %s/%s\n", serverName, tool.Name))
			output.WriteString(fmt.Sprintf("ToolName: %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("Description: %s\n", tool.Description))
			if tool.InputSchema != nil {
				// schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
				// output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))

				// Print CLI-style arguments list
				// Use the prepared Parameters array if available
				if len(tool.Parameters) > 0 {
					output.WriteString("Arguments:\n")
					for _, param := range tool.Parameters {
						// Format: name=<value> : description (type) [required]
						reqText := ""
						if param.Required {
							reqText = " [required]"
						}
						output.WriteString(fmt.Sprintf("- %s=<value> : %s (%s)%s\n",
							param.Name, param.Description, param.Type, reqText))
					}
				} else {
					//		output.WriteString("Arguments: None\n")
				}
			}
			/*
				// Construct usage example with parameters if available
				if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok && len(properties) > 0 {
					// Build URL with query parameters
					var params []string
					for key, _ := range properties {
						params = append(params, fmt.Sprintf("%s=value", key))
					}
					paramString := strings.Join(params, " ")
					output.WriteString(fmt.Sprintf("Usage: `mai-tool call %s/%s %s`\n\n", serverName, tool.Name, paramString))
					// output.WriteString(fmt.Sprintf("**Usage:** `GET /call/%s/%s?%s`\n\n", serverName, tool.Name, paramString))
				} else {
					output.WriteString(fmt.Sprintf("Usage: `mai-tool call %s %s`\n\n", serverName, tool.Name))
					// output.WriteString(fmt.Sprintf("**Usage:** `GET /call/%s/%s`\n\n", serverName, tool.Name))
				}
			*/
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// listPromptsHandler returns all prompts from all servers
func (s *MCPService) listPromptsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("# Prompts Catalog\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		for _, prompt := range server.Prompts {
			output.WriteString(fmt.Sprintf("PromptName: %s/%s\n", serverName, prompt.Name))
			if prompt.Description != "" {
				output.WriteString(fmt.Sprintf("Description: %s\n", prompt.Description))
			}
			if len(prompt.Arguments) > 0 {
				output.WriteString("Arguments:\n")
				for _, a := range prompt.Arguments {
					req := ""
					if a.Required {
						req = " [required]"
					}
					typ := a.Type
					if typ == "" {
						typ = "string"
					}
					output.WriteString(fmt.Sprintf("- %s=<%s> : %s%s\n", a.Name, typ, a.Description, req))
				}
			}
			output.WriteString("\n")
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// jsonPromptsHandler returns all prompts in JSON grouped by server
func (s *MCPService) jsonPromptsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	result := make(map[string][]Prompt)
	for serverName, server := range s.servers {
		server.mutex.RLock()
		prompts := make([]Prompt, len(server.Prompts))
		copy(prompts, server.Prompts)
		server.mutex.RUnlock()
		result[serverName] = prompts
	}

	json.NewEncoder(w).Encode(result)
}

// getPromptHandler calls prompts/get on a server (or auto-discovers by prompt name)
func (s *MCPService) getPromptHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverName := vars["server"]
	promptName := vars["prompt"]

	// Handle prompt names with server prefix (e.g., "server.prompt_name")
	if serverName == "" && strings.Contains(promptName, ".") {
		parts := strings.SplitN(promptName, ".", 2)
		if len(parts) == 2 {
			serverName = parts[0]
			promptName = parts[1]
		}
	}

	// Check for custom prompt
	customPrompt := r.URL.Query().Get("custom_prompt")
	if customPrompt != "" {
		// Return custom prompt as JSON response
		customResult := GetPromptResult{
			Messages: []PromptMessage{
				{
					Role: "user",
					Content: []PromptMessageContent{
						{
							Type: "text",
							Text: customPrompt,
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(customResult)
		return
	}

	// Always log HTTP requests
	log.Printf("HTTP %s %s - Server: %s, Prompt: %s", r.Method, r.URL.String(), serverName, promptName)

	s.mutex.RLock()
	server, exists := s.servers[serverName]
	s.mutex.RUnlock()
	if !exists {
		// Try to auto-discover by prompt name
		for name, srv := range s.servers {
			srv.mutex.RLock()
			for _, p := range srv.Prompts {
				if p.Name == promptName {
					serverName = name
					server = srv
					exists = true
					break
				}
			}
			srv.mutex.RUnlock()
			if exists {
				break
			}
		}
	}

	if !exists {
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	// Parse arguments (similar to tools)
	arguments := make(map[string]interface{})
	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > 0 {
				if err := json.Unmarshal(body, &arguments); err != nil {
					http.Error(w, "Invalid JSON in request body", http.StatusBadRequest)
					return
				}
			}
		} else {
			r.ParseForm()
			for key, values := range r.Form {
				if len(values) == 1 {
					if num, err := strconv.ParseFloat(values[0], 64); err == nil {
						arguments[key] = num
					} else if b, err := strconv.ParseBool(values[0]); err == nil {
						arguments[key] = b
					} else {
						arguments[key] = values[0]
					}
				} else {
					arguments[key] = values
				}
			}
		}
	} else if r.Method == "GET" {
		for key, values := range r.URL.Query() {
			if len(values) == 1 {
				if num, err := strconv.ParseFloat(values[0], 64); err == nil {
					arguments[key] = num
				} else if b, err := strconv.ParseBool(values[0]); err == nil {
					arguments[key] = b
				} else {
					arguments[key] = values[0]
				}
			} else {
				arguments[key] = values
			}
		}
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "prompts/get",
		Params: GetPromptParams{
			Name:      promptName,
			Arguments: arguments,
		},
		ID: 4,
	}

	response, err := s.sendRequest(server, req)
	if err != nil {
		// Check for special prompt actions
		if err.Error() == "PROMPT_CUSTOM_REQUEST" {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Please provide your custom prompt content in the request body or as a query parameter 'custom_prompt'."))
			return
		} else if err.Error() == "PROMPT_LIST_REQUEST" {
			s.listPromptsHandler(w, r)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to get prompt: %v", err), http.StatusBadRequest)
		return
	}

	if response.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Return result as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response.Result)
}

// jsonToolsHandler returns all tools from all servers in JSON format
func (s *MCPService) jsonToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	res := make(map[string][]Tool)
	for serverName, server := range s.servers {
		server.mutex.RLock()
		// Make sure all tools have their Parameters populated from InputSchema
		tools := make([]Tool, len(server.Tools))
		copy(tools, server.Tools)

		// Ensure Parameters are populated for JSON output
		for i := range tools {
			if len(tools[i].Parameters) == 0 && tools[i].InputSchema != nil {
				tools[i].Parameters = extractParametersFromSchema(tools[i].InputSchema)
			}
		}

		res[serverName] = tools
		server.mutex.RUnlock()
	}
	// json.NewEncoder(w).Encode(res)

	jsonBytes, err := json.Marshal(res) // compact JSON, no newline
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonBytes)
}

// quietToolsHandler returns all tools from all servers in a minimally formatted plain text
type quietToolEntry struct {
	Server    string
	Name      string
	Purpose   string
	WhenToUse string
	Category  string
	Args      []ToolParameter
}

// simpleToolsHandler returns all tools in a very simple format for small models
func (s *MCPService) simpleToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("Available tools:\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		for _, tool := range server.Tools {
			// Simple format: TOOLNAME: description
			output.WriteString(fmt.Sprintf("TOOLNAME: %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("DESCRIPTION: %s\n", tool.Description))

			// Add usage example if tool has parameters
			if len(tool.Parameters) > 0 {
				var mandatory []string
				var optional []string
				var paramExamples []string

				for _, param := range tool.Parameters {
					paramExample := fmt.Sprintf("%s=<value>", param.Name)
					paramExamples = append(paramExamples, paramExample)

					paramDesc := fmt.Sprintf("%s (%s)", param.Name, param.Type)

					if param.Required {
						mandatory = append(mandatory, paramDesc)
					} else {
						optional = append(optional, paramDesc)
					}
				}

				// Add usage example
				paramString := strings.Join(paramExamples, " ")
				output.WriteString(fmt.Sprintf("USAGE: %s %s %s\n", serverName, tool.Name, paramString))

				// Add mandatory parameters
				if len(mandatory) > 0 {
					output.WriteString("MANDATORY PARAMS:")
					for _, param := range mandatory {
						output.WriteString(fmt.Sprintf(" %s", param))
					}
					output.WriteString("\n")
				}

				// Add optional parameters
				if len(optional) > 0 {
					output.WriteString("OPTIONAL PARAMS:")
					for _, param := range optional {
						output.WriteString(fmt.Sprintf(" %s", param))
					}
					output.WriteString("\n")
				}
			}
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

func (s *MCPService) quietToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	categoryOrder := []string{"File", "Analysis", "Inspection", "Metadata", "Editing"}
	toolsByCategory := make(map[string][]quietToolEntry)
	serverNames := make([]string, 0, len(s.servers))
	for serverName := range s.servers {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)

	for _, serverName := range serverNames {
		server := s.servers[serverName]
		server.mutex.RLock()
		for _, tool := range server.Tools {
			entry := buildQuietToolEntry(serverName, tool)
			category := entry.Category
			if category == "" {
				category = "Analysis"
			}
			toolsByCategory[category] = append(toolsByCategory[category], entry)
		}
		server.mutex.RUnlock()
	}

	var output strings.Builder
	firstCategory := true
	for _, category := range categoryOrder {
		entries := toolsByCategory[category]
		if len(entries) == 0 {
			continue
		}

		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Server == entries[j].Server {
				return entries[i].Name < entries[j].Name
			}
			return entries[i].Server < entries[j].Server
		})

		var section strings.Builder
		section.WriteString(fmt.Sprintf("## %s\n\n", category))

		for _, entry := range entries {
			section.WriteString(fmt.Sprintf("ToolName: %s/%s\n", entry.Server, entry.Name))
			if entry.Purpose != "" {
				section.WriteString(fmt.Sprintf("Purpose: %s\n", entry.Purpose))
			} else {
				section.WriteString("Purpose: (no description provided)\n")
			}
			if entry.WhenToUse != "" {
				section.WriteString(fmt.Sprintf("WhenToUse: %s\n", entry.WhenToUse))
			} else {
				section.WriteString("WhenToUse: Use when this capability fits the request\n")
			}
			if len(entry.Args) == 0 {
				section.WriteString("Arguments: (none)\n")
			} else {
				section.WriteString("Arguments:\n")
				for _, arg := range entry.Args {
					section.WriteString(formatQuietArgument(arg))
					section.WriteByte('\n')
				}
			}
			section.WriteByte('\n')
		}

		sectionStr := strings.TrimRight(section.String(), "\n")
		if !firstCategory {
			output.WriteByte('\n')
		}
		output.WriteString(sectionStr)
		firstCategory = false
	}

	result := strings.TrimRight(output.String(), "\n")
	w.Write([]byte(result))
}

func buildQuietToolEntry(serverName string, tool Tool) quietToolEntry {
	purpose, whenHint := sanitizeToolDescription(tool.Description)
	params := tool.Parameters
	if len(params) == 0 && tool.InputSchema != nil {
		params = extractParametersFromSchema(tool.InputSchema)
	}
	arguments := make([]ToolParameter, len(params))
	copy(arguments, params)
	sort.Slice(arguments, func(i, j int) bool { return arguments[i].Name < arguments[j].Name })

	entry := quietToolEntry{
		Server:    serverName,
		Name:      tool.Name,
		Purpose:   purpose,
		WhenToUse: formatWhenToUse(purpose, whenHint),
		Category:  categorizeTool(tool.Name, purpose),
		Args:      arguments,
	}
	return entry
}

func sanitizeToolDescription(desc string) (string, string) {
	if desc == "" {
		return "", ""
	}
	clean := desc
	var hints []string
	for {
		start := strings.Index(clean, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(clean[start:], "</think>")
		if end == -1 {
			clean = clean[:start]
			break
		}
		end += start
		thinkText := clean[start+len("<think>") : end]
		if trimmed := strings.TrimSpace(compactSpaces(thinkText)); trimmed != "" {
			hints = append(hints, trimmed)
		}
		clean = clean[:start] + clean[end+len("</think>"):]
	}

	purpose := strings.TrimSpace(compactSpaces(clean))
	whenHint := strings.TrimSpace(compactSpaces(strings.Join(hints, " ")))
	return purpose, whenHint
}

func formatWhenToUse(purpose, hint string) string {
	if hint != "" {
		return compactSpaces(hint)
	}
	return deriveWhenFromPurpose(purpose)
}

func deriveWhenFromPurpose(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return "Use when this capability fits the request"
	}
	words := strings.Fields(purpose)
	if len(words) == 0 {
		return "Use when this capability fits the request"
	}
	first := strings.ToLower(words[0])
	switch {
	case strings.HasSuffix(first, "ies") && len(first) > 3:
		first = first[:len(first)-3] + "y"
	case strings.HasSuffix(first, "ses") || strings.HasSuffix(first, "xes") || strings.HasSuffix(first, "zes") || strings.HasSuffix(first, "ches") || strings.HasSuffix(first, "shes"):
		first = first[:len(first)-2]
	case strings.HasSuffix(first, "es") && len(first) > 2:
		first = first[:len(first)-1]
	case strings.HasSuffix(first, "s") && len(first) > 1:
		first = first[:len(first)-1]
	}
	rest := strings.TrimSpace(strings.TrimPrefix(purpose, words[0]))
	if rest == "" {
		return compactSpaces(fmt.Sprintf("Use to %s", first))
	}
	return compactSpaces(fmt.Sprintf("Use to %s %s", first, strings.TrimSpace(rest)))
}

func formatQuietArgument(arg ToolParameter) string {
	name := strings.TrimSpace(arg.Name)
	if name == "" {
		name = "argument"
	}
	typeLabel := strings.TrimSpace(compactSpaces(arg.Type))
	if typeLabel == "" {
		typeLabel = "value"
	}
	requiredLabel := "optional"
	if arg.Required {
		requiredLabel = "required"
	}
	desc := strings.TrimSpace(compactSpaces(arg.Description))
	if desc != "" {
		return fmt.Sprintf("- %s=<%s> (%s) : %s", name, typeLabel, requiredLabel, desc)
	}
	return fmt.Sprintf("- %s=<%s> (%s)", name, typeLabel, requiredLabel)
}

func categorizeTool(name, description string) string {
	text := strings.ToLower(name + " " + description)
	if containsAny(text, []string{"write", "rename", "set", "update", "replace", "apply", "append", "delete", "remove", "create", "format", "patch", "edit", "modify", "use ", "use_", "toggle", "enable", "disable"}) {
		return "Editing"
	}
	if containsAny(text, []string{"file", "path", "directory", "folder", "filesystem"}) {
		return "File"
	}
	if containsAny(text, []string{"metadata", "status", "config", "capability", "version", "schema", "info"}) {
		return "Metadata"
	}
	if containsAny(text, []string{"list", "analy", "scan", "find", "search", "discover", "enumerate", "xref", "graph", "map"}) {
		return "Analysis"
	}
	if containsAny(text, []string{"show", "display", "get", "dump", "peek", "inspect", "view", "read", "print", "describe", "explain", "decompil", "disassembl"}) {
		return "Inspection"
	}
	return "Analysis"
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func compactSpaces(input string) string {
	if input == "" {
		return ""
	}
	return strings.Join(strings.Fields(input), " ")
}

// markdownToolsHandler returns all tools from all servers in markdown format
func (s *MCPService) markdownToolsHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/markdown")

	var output strings.Builder
	output.WriteString("# Tools Catalog\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))

		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("**Description:** %s\n\n", tool.Description))

			// Add parameters section with type and required information
			if len(tool.Parameters) > 0 || tool.InputSchema != nil {
				output.WriteString("**Parameters:**\n\n")
				output.WriteString("| Name | Type | Required | Description |\n")
				output.WriteString("|------|------|----------|-------------|\n")

				// Use Parameters array if available
				if len(tool.Parameters) > 0 {
					for _, param := range tool.Parameters {
						required := "No"
						if param.Required {
							required = "Yes"
						}
						output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
							param.Name, param.Type, required, param.Description))
					}
				} else if properties, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
					// Extract required fields
					requiredFields := make(map[string]bool)
					if required, ok := tool.InputSchema["required"].([]interface{}); ok {
						for _, field := range required {
							if fieldName, ok := field.(string); ok {
								requiredFields[fieldName] = true
							}
						}
					}

					// Display properties from schema
					for key, val := range properties {
						propInfo, _ := val.(map[string]interface{})
						desc := ""
						propType := "string" // Default type
						req := "No"

						if requiredFields[key] {
							req = "Yes"
						}

						if propInfo != nil {
							if d, ok := propInfo["description"].(string); ok {
								desc = d
							}
							if t, ok := propInfo["type"].(string); ok {
								propType = t
							}
						}

						output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
							key, propType, req, desc))
					}
				}
				output.WriteString("\n")
			}

			// Keep the schema output for reference
			if tool.InputSchema != nil {
				schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
				output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))
			}

			output.WriteString(fmt.Sprintf("**Usage:** `POST /call/%s/%s`\n\n", serverName, tool.Name))
		}
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}

// callToolHandler calls a specific tool on a specific server
func (s *MCPService) callToolHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverName := vars["server"]
	toolName := vars["tool"]

	// Handle tool names with server prefix (e.g., "mai-mcp-wttr.get_weather")
	if serverName == "" && strings.Contains(toolName, ".") {
		parts := strings.SplitN(toolName, ".", 2)
		if len(parts) == 2 {
			serverName = parts[0]
			toolName = parts[1]
		}
	}

	// Check for nonInteractive query parameter
	nonInteractiveParam := r.URL.Query().Get("nonInteractive")
	requestNonInteractive := strings.ToLower(nonInteractiveParam) == "true"

	s.mutex.RLock()
	server, exists := s.servers[serverName]
	s.mutex.RUnlock()
	if !exists {
		// Try to discover the server/tool by name. When drunkMode is enabled
		// allow fuzzy matches via findBestToolMatch.
		for name, _server := range s.servers {
			if matched, ok := findBestToolMatch(_server.Tools, toolName, s.drunkMode); ok {
				serverName = name
				server = _server
				// Update toolName to the actual resolved tool name
				toolName = matched
				exists = true
				break
			}
		}
	}

	// Always log HTTP requests regardless of debug mode
	log.Printf("HTTP %s %s - Server: %s, Tool: %s", r.Method, r.URL.String(), serverName, toolName)

	// Parse arguments up front so they can be reused if the tool needs to be adjusted
	arguments := make(map[string]interface{})
	debugLog(s.debugMode, "Parsing arguments for tool call")

	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			// Parse JSON body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("ERROR: Failed to read request body for tool %s/%s: %v", serverName, toolName, err)
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}

			if len(body) > 0 {
				if err := json.Unmarshal(body, &arguments); err != nil {
					log.Printf("ERROR: Failed to parse JSON body for tool %s/%s: %v", serverName, toolName, err)
					http.Error(w, "Invalid JSON in request body", http.StatusBadRequest)
					return
				}
			}
		} else {
			// Parse form data
			if err := r.ParseForm(); err != nil {
				log.Printf("ERROR: Failed to parse form data for tool %s/%s: %v", serverName, toolName, err)
				http.Error(w, "Failed to parse form data", http.StatusBadRequest)
				return
			}
			for key, values := range r.Form {
				if len(values) == 1 {
					// Try to parse as number, otherwise keep as string
					if num, err := strconv.ParseFloat(values[0], 64); err == nil {
						arguments[key] = num
					} else if b, err := strconv.ParseBool(values[0]); err == nil {
						arguments[key] = b
					} else {
						arguments[key] = values[0]
					}
				} else {
					arguments[key] = values
				}
			}
		}
	} else {
		if err := r.ParseForm(); err != nil && err != http.ErrNotMultipart {
			log.Printf("ERROR: Failed to parse query for tool %s/%s: %v", serverName, toolName, err)
			http.Error(w, "Failed to parse query parameters", http.StatusBadRequest)
			return
		}
		debugLog(s.debugMode, "Query parameters: %v", r.URL.Query())
		for key, values := range r.Form {
			if len(values) == 1 {
				if num, err := strconv.ParseFloat(values[0], 64); err == nil {
					arguments[key] = num
				} else if b, err := strconv.ParseBool(values[0]); err == nil {
					arguments[key] = b
				} else {
					arguments[key] = values[0]
				}
			} else {
				arguments[key] = values
			}
		}
	}

	if !exists {
		// Prompt the user for what to do when the requested tool/server isn't found
		var decision YoloDecision
		if requestNonInteractive {
			decision = YoloToolNotFound
		} else {
			decision = s.promptToolNotFoundDecision(toolName)
		}

		switch decision {
		case YoloToolNotFound:
			http.Error(w, fmt.Sprintf("Tool '%s' not found", toolName), http.StatusNotFound)
			return
		case YoloCustomResponse:
			fmt.Print("Enter your custom response: ")
			reader := bufio.NewReader(os.Stdin)
			customResponse, _ := reader.ReadString('\n')
			customResponse = strings.TrimSpace(customResponse)
			if customResponse == "" {
				http.Error(w, fmt.Sprintf("Tool '%s' not found", toolName), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(customResponse))
			return
		case YoloModify:
			// Show available tools and prompt the user to choose a replacement
			fmt.Println("\nAvailable tools:")
			s.mutex.RLock()
			for _, srv := range s.servers {
				srv.mutex.RLock()
				for _, t := range srv.Tools {
					fmt.Printf("  %s - %s\n", t.Name, t.Description)
				}
				srv.mutex.RUnlock()
			}
			s.mutex.RUnlock()

			callParams := CallToolParams{Name: toolName, Arguments: arguments}
			newParams, err := s.promptModifyTool(&callParams)
			if err != nil {
				if errors.Is(err, errToolModificationCancelled) {
					http.Error(w, "tool execution cancelled by user", http.StatusBadRequest)
					return
				}
				http.Error(w, fmt.Sprintf("failed to adjust tool request: %v", err), http.StatusBadRequest)
				return
			}

			callParams = *newParams
			toolName = callParams.Name
			if callParams.Arguments != nil {
				arguments = callParams.Arguments
			} else {
				arguments = make(map[string]interface{})
			}

			s.mutex.RLock()
			found := false
			for name, srv := range s.servers {
				if matched, ok := findBestToolMatch(srv.Tools, toolName, s.drunkMode); ok {
					serverName = name
					server = srv
					toolName = matched
					exists = true
					found = true
					break
				}
			}
			s.mutex.RUnlock()

			if !found {
				http.Error(w, fmt.Sprintf("tool '%s' not found", toolName), http.StatusNotFound)
				return
			}
		case YoloGuideModel:
			guideMsg := fmt.Sprintf("Tool '%s' not found. Provide clearer tool name or use available tools list.", toolName)
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(guideMsg))
			return
		default:
			http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
			return
		}
	}

	debugLog(s.debugMode, "Parsed arguments: %v", arguments)

	// Apply drunk mode parameter assignment if enabled
	if s.drunkMode && len(arguments) > 0 {
		// Find the tool to get its parameters
		var foundTool *Tool
		server.mutex.RLock()
		for _, tool := range server.Tools {
			if tool.Name == toolName {
				foundTool = &tool
				break
			}
		}
		server.mutex.RUnlock()

		if foundTool != nil && len(foundTool.Parameters) > 0 {
			// Get all argument keys and order them deterministically.
			// Numeric keys ("0","1",...) are sorted by numeric order and used first
			// to support positional parameters from clients. Non-numeric keys are
			// then appended in lexicographical order.
			numericKeys := make([]int, 0)
			numericMap := make(map[int]string)
			nonNumericKeys := make([]string, 0)

			for k := range arguments {
				if i, err := strconv.Atoi(k); err == nil {
					numericKeys = append(numericKeys, i)
					numericMap[i] = k
				} else {
					nonNumericKeys = append(nonNumericKeys, k)
				}
			}
			sort.Ints(numericKeys)
			sort.Strings(nonNumericKeys)

			argKeys := make([]string, 0, len(arguments))
			for _, i := range numericKeys {
				argKeys = append(argKeys, numericMap[i])
			}
			for _, k := range nonNumericKeys {
				argKeys = append(argKeys, k)
			}

			if len(argKeys) == 1 && len(foundTool.Parameters) > 0 {
				// Single argument: assign to first parameter
				firstParam := foundTool.Parameters[0]
				newArgs := make(map[string]interface{})
				newArgs[firstParam.Name] = arguments[argKeys[0]]
				arguments = newArgs
				debugLog(s.debugMode, "Drunk mode: assigned single arg to first param %s", firstParam.Name)
			} else {
				// Multiple arguments: assign in order to parameters, filling gaps
				newArgs := make(map[string]interface{})
				argIndex := 0
				for _, param := range foundTool.Parameters {
					if argIndex < len(argKeys) {
						newArgs[param.Name] = arguments[argKeys[argIndex]]
						argIndex++
					}
				}
				arguments = newArgs
				debugLog(s.debugMode, "Drunk mode: reassigned args in order to params")
			}
		}
	}

	// Create tool call request
	toolRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
		ID: time.Now().UnixNano(),
	}

	debugLog(s.debugMode, "Calling tool %s on server %s with arguments: %v", toolName, serverName, arguments)

	response, err := s.sendRequest(server, toolRequest)
	if err != nil {
		log.Printf("ERROR: Failed to send request to server %s for tool %s: %v", serverName, toolName, err)
		http.Error(w, fmt.Sprintf("Failed to call tool: %v", err), http.StatusInternalServerError)
		return
	}

	if response.Error != nil {
		log.Printf("ERROR: Tool call to %s/%s failed with RPC error: %v", serverName, toolName, response.Error)
		http.Error(w, fmt.Sprintf("Tool call failed: %v", response.Error), http.StatusBadRequest)
		return
	}

	// Parse and format response
	w.Header().Set("Content-Type", "text/plain")

	resultBytes, _ := json.Marshal(response.Result)
	var toolResult CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
		// Fallback to raw JSON if parsing fails
		w.Write(resultBytes)
		return
	}
	if toolResult.Error != nil {
		emsg := "ERROR: " + toolResult.Error.Message
		log.Printf("ERROR: Tool %s/%s returned error: %s", serverName, toolName, toolResult.Error.Message)
		w.Write([]byte(emsg))
		debugLog(s.debugMode, emsg)
		return
	}

	// Format content as markdown/plaintext
	var output strings.Builder
	for i, content := range toolResult.Content {
		if i > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(content.Text)
	}

	// If pagination metadata present, report pages left
	pagesLeft := 0
	if toolResult.TotalPages > 0 && toolResult.Page > 0 {
		pagesLeft = toolResult.TotalPages - toolResult.Page
		if pagesLeft < 0 {
			pagesLeft = 0
		}
	}
	if pagesLeft > 0 {
		output.WriteString(fmt.Sprintf("\n\nPages left: %d", pagesLeft))
		if toolResult.NextPageToken != "" {
			output.WriteString(fmt.Sprintf(" (next_page_token: %s)", toolResult.NextPageToken))
		}
	}

	debugLog(s.debugMode, "Response content: %s", output.String())

	log.Printf("SUCCESS: Tool %s/%s completed successfully", serverName, toolName)
	w.Write([]byte(output.String()))
}

// statusHandler returns the status of all servers
func (s *MCPService) statusHandler(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "text/plain")

	var output strings.Builder
	output.WriteString("# MCP Service Status\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString(fmt.Sprintf("Status: Running\n"))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))
		server.mutex.RUnlock()
	}

	w.Write([]byte(output.String()))
}
