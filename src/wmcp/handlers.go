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

// writeTextResponse writes a text response
func writeTextResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
}

// setCORSHeaders sets CORS headers for responses
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Native-Tool-Call")
}

// HTTP Handlers

// listToolsHandler returns all tools from all servers
func (s *MCPService) listToolsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var output strings.Builder
	output.WriteString("# Tools Catalog\n\n")

	for _, server := range s.servers {
		server.mutex.RLock()
		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("- ToolName: %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("  Description: %s\n", tool.Description))
			if len(tool.Parameters) > 0 {
				output.WriteString("  Parameters:\n")
				for _, param := range tool.Parameters {
					req := ""
					if param.Required {
						req = " (required)"
					}
					output.WriteString(fmt.Sprintf("  - %s=<value> : %s (%s)%s\n",
						param.Name, param.Description, param.Type, req))
				}
			}
		}
		server.mutex.RUnlock()
	}

	writeTextResponse(w, output.String())
}

// listPromptsHandler returns all prompts from all servers
func (s *MCPService) listPromptsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	s.mutex.RLock()
	defer s.mutex.RUnlock()

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
				output.WriteString("Parameters:\n")
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

	writeTextResponse(w, output.String())
}

// jsonPromptsHandler returns all prompts in JSON grouped by server
func (s *MCPService) jsonPromptsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
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

// quietPromptsHandler returns all prompts in quiet format (just names)
func (s *MCPService) quietPromptsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var output strings.Builder

	for serverName, server := range s.servers {
		server.mutex.RLock()
		for _, prompt := range server.Prompts {
			output.WriteString(fmt.Sprintf("%s/%s\n", serverName, prompt.Name))
		}
		server.mutex.RUnlock()
	}

	writeTextResponse(w, output.String())
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

// listResourcesHandler returns all resources from all servers
func (s *MCPService) listResourcesHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var output strings.Builder
	output.WriteString("# Resources Catalog\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Resources: %d\n\n", len(server.Resources)))

		for _, resource := range server.Resources {
			output.WriteString(fmt.Sprintf("- URI: %s\n", resource.URI))
			output.WriteString(fmt.Sprintf("  Name: %s\n", resource.Name))
			if resource.Description != "" {
				output.WriteString(fmt.Sprintf("  Description: %s\n", resource.Description))
			}
			if resource.MimeType != "" {
				output.WriteString(fmt.Sprintf("  MIME Type: %s\n", resource.MimeType))
			}
			output.WriteString("\n")
		}
		server.mutex.RUnlock()
	}

	writeTextResponse(w, output.String())
}

// jsonResourcesHandler returns all resources in JSON format
func (s *MCPService) jsonResourcesHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	result := make(map[string][]Resource)
	for serverName, server := range s.servers {
		server.mutex.RLock()
		resources := make([]Resource, len(server.Resources))
		copy(resources, server.Resources)
		server.mutex.RUnlock()
		result[serverName] = resources
	}

	json.NewEncoder(w).Encode(result)
}

// readResourceHandler reads a specific resource
func (s *MCPService) readResourceHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverName := vars["server"]
	resourceURI := vars["uri"]

	// Always log HTTP requests
	log.Printf("HTTP %s %s - Server: %s, Resource: %s", r.Method, r.URL.String(), serverName, resourceURI)

	s.mutex.RLock()
	server, exists := s.servers[serverName]
	s.mutex.RUnlock()
	if !exists {
		http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
		return
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "resources/read",
		Params: ReadResourceParams{
			URI: resourceURI,
		},
		ID: 5,
	}

	response, err := s.sendRequest(server, req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read resource: %v", err), http.StatusBadRequest)
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
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

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

// simpleToolsHandler returns all tools in a very simple format for small models
func (s *MCPService) simpleToolsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var output strings.Builder

	for serverName, server := range s.servers {
		server.mutex.RLock()
		notFirst := false
		for _, tool := range server.Tools {
			if notFirst {
				output.WriteString("--\n")
			}
			notFirst = true
			output.WriteString(fmt.Sprintf("TOOLNAME: %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("DESCRIPTION: %s\n", tool.Description))

			if len(tool.Parameters) > 0 {
				var mandatory, optional, paramExamples []string

				for _, param := range tool.Parameters {
					paramExamples = append(paramExamples, fmt.Sprintf("%s=<value>", param.Name))
					paramDesc := fmt.Sprintf("%s (%s)", param.Name, param.Type)

					if param.Required {
						mandatory = append(mandatory, paramDesc)
					} else {
						optional = append(optional, paramDesc)
					}
				}

				output.WriteString(fmt.Sprintf("USAGE: %s %s %s\n", serverName, tool.Name, strings.Join(paramExamples, " ")))

				if len(mandatory) > 0 {
					output.WriteString("MANDATORY PARAMS:")
					for _, param := range mandatory {
						output.WriteString(fmt.Sprintf(" %s", param))
					}
					output.WriteString("\n")
				}

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

	writeTextResponse(w, output.String())
}

func (s *MCPService) quietToolsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()

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
			cat := entry.Category
			if cat == "" {
				cat = "Analysis"
			}
			toolsByCategory[cat] = append(toolsByCategory[cat], entry)
		}
		server.mutex.RUnlock()
	}

	var output strings.Builder
	first := true
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
		for _, entry := range entries {
			section.WriteString(fmt.Sprintf("- ToolName: %s\n", entry.Name))
			if entry.Purpose != "" {
				section.WriteString(fmt.Sprintf("  Description: %s", entry.Purpose))
				if entry.WhenToUse != "" {
					section.WriteString(fmt.Sprintf(" (%s)", entry.WhenToUse))
				} else {
					section.WriteString("  WhenToUse: Use when this capability fits the request\n")
				}
				section.WriteString("\n")
			} else {
				section.WriteString("  Purpose: (no description provided)\n")
			}
			if len(entry.Args) > 0 {
				section.WriteString("  Parameters:\n")
				for _, arg := range entry.Args {
					section.WriteString(formatQuietArgument(arg))
					section.WriteByte('\n')
				}
			}
			section.WriteByte('\n')
		}

		if !first {
			output.WriteByte('\n')
		}
		output.WriteString(strings.TrimRight(section.String(), "\n"))
		first = false
	}

	writeTextResponse(w, strings.TrimRight(output.String(), "\n"))
}

// markdownToolsHandler returns all tools from all servers in markdown format
func (s *MCPService) markdownToolsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

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

			if len(tool.Parameters) > 0 || tool.InputSchema != nil {
				output.WriteString("**Parameters:**\n\n")
				output.WriteString("| Name | Type | Required | Description |\n")
				output.WriteString("|------|------|----------|-------------|\n")

				if len(tool.Parameters) > 0 {
					for _, param := range tool.Parameters {
						req := "No"
						if param.Required {
							req = "Yes"
						}
						output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
							param.Name, param.Type, req, param.Description))
					}
				} else if props, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
					reqFields := make(map[string]bool)
					if req, ok := tool.InputSchema["required"].([]interface{}); ok {
						for _, f := range req {
							if fn, ok := f.(string); ok {
								reqFields[fn] = true
							}
						}
					}

					for key, val := range props {
						propInfo := val.(map[string]interface{})
						desc := ""
						propType := "string"
						req := "No"

						if reqFields[key] {
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
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		// Return empty response for OPTIONS requests
		w.WriteHeader(http.StatusOK)
		return
	}

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

	// Check if native tool calling format is requested
	nativeFormat := r.URL.Query().Get("native") == "true" || r.Header.Get("X-Native-Tool-Call") == "true"

	resultBytes, _ := json.Marshal(response.Result)
	var toolResult CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
		// Fallback to raw JSON if parsing fails
		if nativeFormat {
			w.Header().Set("Content-Type", "application/json")
			w.Write(resultBytes)
		} else {
			w.Header().Set("Content-Type", "text/plain")
			w.Write(resultBytes)
		}
		return
	}
	if toolResult.Error != nil {
		emsg := "ERROR: " + toolResult.Error.Message
		log.Printf("ERROR: Tool %s/%s returned error: %s", serverName, toolName, toolResult.Error.Message)
		if nativeFormat {
			w.Header().Set("Content-Type", "application/json")
			errorResponse := map[string]interface{}{
				"error": toolResult.Error.Message,
			}
			json.NewEncoder(w).Encode(errorResponse)
		} else {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(emsg))
		}
		debugLog(s.debugMode, emsg)
		return
	}

	if nativeFormat {
		// For native tool calling, return the structured result as JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toolResult)
	} else {
		// Parse and format response
		w.Header().Set("Content-Type", "text/plain")

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
}

// statusHandler returns the status of all servers
func (s *MCPService) statusHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var output strings.Builder
	output.WriteString("# MCP Service Status\n\n")

	for serverName, server := range s.servers {
		server.mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
		output.WriteString("Status: Running\n")
		output.WriteString(fmt.Sprintf("Tools: %d\n", len(server.Tools)))
		output.WriteString(fmt.Sprintf("Prompts: %d\n", len(server.Prompts)))
		output.WriteString(fmt.Sprintf("Resources: %d\n\n", len(server.Resources)))
		server.mutex.RUnlock()
	}

	writeTextResponse(w, output.String())
}

// openapiHandler returns the OpenAPI specification
func (s *MCPService) openapiHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("HTTP %s %s", r.Method, r.URL.String())

	// Set CORS headers for all requests
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Native-Tool-Call")

	if r.Method == "OPTIONS" {
		// Return empty response for OPTIONS requests
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	spec := s.generateOpenAPISpec()
	json.NewEncoder(w).Encode(spec)
}

// generateOpenAPISpec generates the OpenAPI specification dynamically
func (s *MCPService) generateOpenAPISpec() map[string]interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Build simple description focusing on available tools
	description := "MCP Server API\n\n"
	description += "**Available Tools:**\n"

	if len(s.servers) == 0 {
		description += "- No tools currently available\n"
	} else {
		for _, server := range s.servers {
			server.mutex.RLock()
			for _, tool := range server.Tools {
				description += fmt.Sprintf("- **%s**: %s\n", tool.Name, tool.Description)
			}
			server.mutex.RUnlock()
		}
	}

	spec := map[string]interface{}{
		"openapi": "3.1.0",
		"info": map[string]interface{}{
			"title":       "MCP Server API",
			"description": description,
			"version":     "1.0.0",
		},
		"servers": []map[string]interface{}{
			{
				"url":         "http://localhost:8989",
				"description": "Local MCP server",
			},
		},
		"paths":      s.generatePaths(),
		"components": s.generateComponents(),
	}

	return spec
}

// generatePaths generates the paths section of the OpenAPI spec
func (s *MCPService) generatePaths() map[string]interface{} {
	paths := make(map[string]interface{})

	// Tool calling endpoints - these are dynamic based on available tools
	for serverName, server := range s.servers {
		server.mutex.RLock()
		for _, tool := range server.Tools {
			// Tool endpoint following tool server standard
			path := fmt.Sprintf("/%s", tool.Name)
			paths[path] = s.generateToolOperation(serverName, tool)
		}
		server.mutex.RUnlock()
	}

	return paths
}

// generateToolOperation generates an OpenAPI operation for a specific tool
func (s *MCPService) generateToolOperation(serverName string, tool Tool) map[string]interface{} {
	operation := map[string]interface{}{
		"summary":     tool.Name,
		"description": tool.Description,
		"operationId": fmt.Sprintf("tool_%s_post", tool.Name),
		"requestBody": map[string]interface{}{
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{
					"schema": map[string]interface{}{
						"$ref": fmt.Sprintf("#/components/schemas/%s_form_model", tool.Name),
					},
				},
			},
			"required": true,
		},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{
				"description": "Successful Response",
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{
						"schema": map[string]interface{}{
							"title": fmt.Sprintf("Response Tool %s Post", tool.Name),
						},
					},
				},
			},
			"422": map[string]interface{}{
				"description": "Validation Error",
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{
						"schema": map[string]interface{}{
							"$ref": "#/components/schemas/HTTPValidationError",
						},
					},
				},
			},
		},
	}

	return map[string]interface{}{
		"post": operation,
	}
}

// generateComponents generates the components section of the OpenAPI spec
func (s *MCPService) generateComponents() map[string]interface{} {
	components := map[string]interface{}{
		"schemas": map[string]interface{}{
			"HTTPValidationError": map[string]interface{}{
				"properties": map[string]interface{}{
					"detail": map[string]interface{}{
						"items": map[string]interface{}{
							"$ref": "#/components/schemas/ValidationError",
						},
						"type":  "array",
						"title": "Detail",
					},
				},
				"type":  "object",
				"title": "HTTPValidationError",
			},
			"ValidationError": map[string]interface{}{
				"properties": map[string]interface{}{
					"loc": map[string]interface{}{
						"items": map[string]interface{}{
							"anyOf": []map[string]interface{}{
								{"type": "string"},
								{"type": "integer"},
							},
						},
						"type":  "array",
						"title": "Location",
					},
					"msg": map[string]interface{}{
						"type":  "string",
						"title": "Message",
					},
					"type": map[string]interface{}{
						"type":  "string",
						"title": "Error Type",
					},
				},
				"type":     "object",
				"required": []string{"loc", "msg", "type"},
				"title":    "ValidationError",
			},
		},
	}

	// Add schemas for each tool
	schemas := components["schemas"].(map[string]interface{})
	for _, server := range s.servers {
		server.mutex.RLock()
		for _, tool := range server.Tools {
			if len(tool.Parameters) > 0 {
				properties := make(map[string]interface{})
				required := []string{}

				for _, param := range tool.Parameters {
					paramSchema := map[string]interface{}{
						"type":        param.Type,
						"title":       strings.Title(strings.ReplaceAll(param.Name, "_", " ")),
						"description": param.Description,
					}

					if param.Type == "array" {
						paramSchema["items"] = map[string]interface{}{
							"type": "string",
						}
					}

					properties[param.Name] = paramSchema
					if param.Required {
						required = append(required, param.Name)
					}
				}

				schema := map[string]interface{}{
					"properties": properties,
					"type":       "object",
					"title":      fmt.Sprintf("%s_form_model", tool.Name),
				}

				if len(required) > 0 {
					schema["required"] = required
				}

				schemas[fmt.Sprintf("%s_form_model", tool.Name)] = schema
			}
		}
		server.mutex.RUnlock()
	}

	return components
}
