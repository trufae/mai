package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	wmcplib "wmcplib"

	"github.com/gorilla/mux"
)

func writeTextResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Native-Tool-Call")
}

// writeProxyToolsText renders the two proxy tools in the plain-text catalog
// format used by listToolsHandler / simpleToolsHandler / quietToolsHandler.
func writeProxyToolsText(output *strings.Builder) {
	for _, tool := range wmcplib.ProxyTools() {
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
}

func listToolsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		setCORSHeaders(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		var output strings.Builder
		output.WriteString("# Tools Catalog\n\n")

		if s.ProxyToolsMode {
			writeProxyToolsText(&output)
			writeTextResponse(w, output.String())
			return
		}

		for _, server := range s.Servers {
			server.Mutex.RLock()
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
			server.Mutex.RUnlock()
		}

		writeTextResponse(w, output.String())
	}
}

func listPromptsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		var output strings.Builder
		output.WriteString("# Prompts Catalog\n\n")

		for serverName, server := range s.Servers {
			server.Mutex.RLock()
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
			server.Mutex.RUnlock()
		}

		writeTextResponse(w, output.String())
	}
}

func jsonPromptsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		w.Header().Set("Content-Type", "application/json")

		result := make(map[string][]wmcplib.Prompt)
		for serverName, server := range s.Servers {
			server.Mutex.RLock()
			prompts := make([]wmcplib.Prompt, len(server.Prompts))
			copy(prompts, server.Prompts)
			server.Mutex.RUnlock()
			result[serverName] = prompts
		}

		json.NewEncoder(w).Encode(result)
	}
}

func quietPromptsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		var output strings.Builder

		for serverName, server := range s.Servers {
			server.Mutex.RLock()
			for _, prompt := range server.Prompts {
				output.WriteString(fmt.Sprintf("%s/%s\n", serverName, prompt.Name))
			}
			server.Mutex.RUnlock()
		}

		writeTextResponse(w, output.String())
	}
}

func getPromptHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serverName := vars["server"]
		promptName := vars["prompt"]

		if serverName == "" && strings.Contains(promptName, ".") {
			parts := strings.SplitN(promptName, ".", 2)
			if len(parts) == 2 {
				serverName = parts[0]
				promptName = parts[1]
			}
		}

		customPrompt := r.URL.Query().Get("custom_prompt")
		if customPrompt != "" {
			customResult := wmcplib.GetPromptResult{
				Messages: []wmcplib.PromptMessage{
					{
						Role: "user",
						Content: []wmcplib.PromptMessageContent{
							{Type: "text", Text: customPrompt},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(customResult)
			return
		}

		log.Printf("HTTP %s %s - Server: %s, Prompt: %s", r.Method, r.URL.String(), serverName, promptName)

		s.Mutex.RLock()
		server, exists := s.Servers[serverName]
		s.Mutex.RUnlock()
		if !exists {
			for name, srv := range s.Servers {
				srv.Mutex.RLock()
				for _, p := range srv.Prompts {
					if p.Name == promptName {
						serverName = name
						server = srv
						exists = true
						break
					}
				}
				srv.Mutex.RUnlock()
				if exists {
					break
				}
			}
		}

		if !exists {
			http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
			return
		}

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

		req := wmcplib.JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "prompts/get",
			Params:  wmcplib.GetPromptParams{Name: promptName, Arguments: arguments},
			ID:      4,
		}

		response, err := s.SendRequest(server, req)
		if err != nil {
			if err.Error() == "PROMPT_CUSTOM_REQUEST" {
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte("Please provide your custom prompt content in the request body or as a query parameter 'custom_prompt'."))
				return
			} else if err.Error() == "PROMPT_LIST_REQUEST" {
				listPromptsHandler(s).ServeHTTP(w, r)
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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response.Result)
	}
}

func listResourcesHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		var output strings.Builder
		output.WriteString("# Resources Catalog\n\n")

		for serverName, server := range s.Servers {
			server.Mutex.RLock()
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
			server.Mutex.RUnlock()
		}

		writeTextResponse(w, output.String())
	}
}

func jsonResourcesHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		w.Header().Set("Content-Type", "application/json")

		result := make(map[string][]wmcplib.Resource)
		for serverName, server := range s.Servers {
			server.Mutex.RLock()
			resources := make([]wmcplib.Resource, len(server.Resources))
			copy(resources, server.Resources)
			server.Mutex.RUnlock()
			result[serverName] = resources
		}

		json.NewEncoder(w).Encode(result)
	}
}

func readResourceHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serverName := vars["server"]
		resourceURI := vars["uri"]

		log.Printf("HTTP %s %s - Server: %s, Resource: %s", r.Method, r.URL.String(), serverName, resourceURI)

		s.Mutex.RLock()
		server, exists := s.Servers[serverName]
		s.Mutex.RUnlock()
		if !exists {
			http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
			return
		}

		req := wmcplib.JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "resources/read",
			Params:  wmcplib.ReadResourceParams{URI: resourceURI},
			ID:      5,
		}

		response, err := s.SendRequest(server, req)
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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response.Result)
	}
}

func jsonToolsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		setCORSHeaders(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		res := make(map[string][]wmcplib.Tool)
		if s.ProxyToolsMode {
			res["proxy"] = wmcplib.ProxyTools()
			jsonBytes, err := json.Marshal(res)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write(jsonBytes)
			return
		}
		for serverName, server := range s.Servers {
			server.Mutex.RLock()
			tools := make([]wmcplib.Tool, len(server.Tools))
			copy(tools, server.Tools)
			for i := range tools {
				if len(tools[i].Parameters) == 0 && tools[i].InputSchema != nil {
					tools[i].Parameters = wmcplib.ExtractParametersFromSchema(tools[i].InputSchema)
				}
			}
			res[serverName] = tools
			server.Mutex.RUnlock()
		}

		jsonBytes, err := json.Marshal(res)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(jsonBytes)
	}
}

func simpleToolsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		setCORSHeaders(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		var output strings.Builder

		if s.ProxyToolsMode {
			writeProxyToolsSimple(&output)
			writeTextResponse(w, output.String())
			return
		}

		for serverName, server := range s.Servers {
			server.Mutex.RLock()
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
			server.Mutex.RUnlock()
		}

		writeTextResponse(w, output.String())
	}
}

// writeProxyToolsSimple renders the two proxy tools in the "simple" text
// format used by simpleToolsHandler.
func writeProxyToolsSimple(output *strings.Builder) {
	notFirst := false
	for _, tool := range wmcplib.ProxyTools() {
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
			output.WriteString(fmt.Sprintf("USAGE: proxy %s %s\n", tool.Name, strings.Join(paramExamples, " ")))
			if len(mandatory) > 0 {
				output.WriteString("MANDATORY PARAMS:")
				for _, param := range mandatory {
					output.WriteString(" " + param)
				}
				output.WriteString("\n")
			}
			if len(optional) > 0 {
				output.WriteString("OPTIONAL PARAMS:")
				for _, param := range optional {
					output.WriteString(" " + param)
				}
				output.WriteString("\n")
			}
		}
	}
}

func quietToolsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		setCORSHeaders(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		if s.ProxyToolsMode {
			var output strings.Builder
			for _, tool := range wmcplib.ProxyTools() {
				output.WriteString(fmt.Sprintf("- ToolName: %s\n", tool.Name))
				output.WriteString(fmt.Sprintf("  Description: %s\n", tool.Description))
				if len(tool.Parameters) > 0 {
					output.WriteString("  Parameters:\n")
					for _, p := range tool.Parameters {
						req := ""
						if p.Required {
							req = " [required]"
						}
						output.WriteString(fmt.Sprintf("  - %s=<%s> : %s%s\n", p.Name, p.Type, p.Description, req))
					}
				}
			}
			writeTextResponse(w, strings.TrimRight(output.String(), "\n"))
			return
		}

		categoryOrder := []string{"File", "Analysis", "Inspection", "Metadata", "Editing"}
		toolsByCategory := make(map[string][]wmcplib.QuietToolEntry)
		serverNames := make([]string, 0, len(s.Servers))
		for serverName := range s.Servers {
			serverNames = append(serverNames, serverName)
		}
		sort.Strings(serverNames)

		for _, serverName := range serverNames {
			server := s.Servers[serverName]
			server.Mutex.RLock()
			for _, tool := range server.Tools {
				entry := wmcplib.BuildQuietToolEntry(serverName, tool)
				cat := entry.Category
				if cat == "" {
					cat = "Analysis"
				}
				toolsByCategory[cat] = append(toolsByCategory[cat], entry)
			}
			server.Mutex.RUnlock()
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
						section.WriteString(wmcplib.FormatQuietArgument(arg))
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
}

func markdownToolsHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		setCORSHeaders(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		w.Header().Set("Content-Type", "text/markdown")

		var output strings.Builder
		output.WriteString("# Tools Catalog\n\n")

		if s.ProxyToolsMode {
			output.WriteString("## Proxy Tools Mode\n\n")
			output.WriteString("Only two virtual tools are exposed; they gate access to the real underlying tools.\n\n")
			for _, tool := range wmcplib.ProxyTools() {
				output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
				output.WriteString(fmt.Sprintf("**Description:** %s\n\n", tool.Description))
				if len(tool.Parameters) > 0 {
					output.WriteString("**Parameters:**\n\n")
					output.WriteString("| Name | Type | Required | Description |\n")
					output.WriteString("|------|------|----------|-------------|\n")
					for _, p := range tool.Parameters {
						req := "No"
						if p.Required {
							req = "Yes"
						}
						output.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", p.Name, p.Type, req, p.Description))
					}
					output.WriteString("\n")
				}
				if tool.InputSchema != nil {
					schemaBytes, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
					output.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", string(schemaBytes)))
				}
				output.WriteString(fmt.Sprintf("**Usage:** `POST /call/%s`\n\n", tool.Name))
			}
			w.Write([]byte(output.String()))
			return
		}

		for serverName, server := range s.Servers {
			server.Mutex.RLock()
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
			server.Mutex.RUnlock()
		}

		w.Write([]byte(output.String()))
	}
}

// proxyCallHandler services /call/{tool} when ProxyToolsMode is on. It
// accepts only the two virtual tool names; everything else is a 404. Results
// are delivered via ProcessMCPRequest so the HTTP and JSON-RPC transports
// stay behaviorally identical.
func proxyCallHandler(s *wmcplib.MCPService, w http.ResponseWriter, r *http.Request, toolName string) {
	if !wmcplib.IsProxyToolName(toolName) {
		http.Error(w, fmt.Sprintf("proxy-tools mode: only '%s' and '%s' are exposed; got '%s'",
			wmcplib.ProxyToolSearchName, wmcplib.ProxyToolCallName, toolName), http.StatusNotFound)
		return
	}

	arguments := make(map[string]interface{})
	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > 0 {
				if err := json.Unmarshal(body, &arguments); err != nil {
					http.Error(w, "invalid JSON in request body", http.StatusBadRequest)
					return
				}
			}
		} else {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "failed to parse form data", http.StatusBadRequest)
				return
			}
			for key, values := range r.Form {
				if len(values) == 1 {
					arguments[key] = values[0]
				} else {
					arguments[key] = values
				}
			}
		}
	} else {
		if err := r.ParseForm(); err != nil && err != http.ErrNotMultipart {
			http.Error(w, "failed to parse query parameters", http.StatusBadRequest)
			return
		}
		for key, values := range r.Form {
			if len(values) == 1 {
				arguments[key] = values[0]
			} else {
				arguments[key] = values
			}
		}
	}

	req := wmcplib.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params:  wmcplib.CallToolParams{Name: toolName, Arguments: arguments},
		ID:      time.Now().UnixNano(),
	}
	resp, _ := s.ProcessMCPRequest(req)
	if resp == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if resp.Error != nil {
		http.Error(w, fmt.Sprintf("tool call failed: %v", resp.Error), http.StatusBadRequest)
		return
	}

	nativeFormat := r.URL.Query().Get("native") == "true" || r.Header.Get("X-Native-Tool-Call") == "true"

	resultBytes, _ := json.Marshal(resp.Result)
	var toolResult wmcplib.CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
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
		if nativeFormat {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"error": toolResult.Error.Message})
		} else {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("ERROR: " + toolResult.Error.Message))
		}
		return
	}

	if nativeFormat {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toolResult)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	var output strings.Builder
	for i, c := range toolResult.Content {
		if i > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(c.Text)
	}
	w.Write([]byte(output.String()))
}

func callToolHandler(s *wmcplib.MCPService) http.HandlerFunc {
	debugLog := func(format string, args ...interface{}) {
		if s.DebugMode {
			log.Printf("DEBUG: "+format, args...)
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		setCORSHeaders(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		vars := mux.Vars(r)
		serverName := vars["server"]
		toolName := vars["tool"]

		if serverName == "" && strings.Contains(toolName, ".") {
			parts := strings.SplitN(toolName, ".", 2)
			if len(parts) == 2 {
				serverName = parts[0]
				toolName = parts[1]
			}
		}

		if s.ProxyToolsMode {
			proxyCallHandler(s, w, r, toolName)
			return
		}

		nonInteractiveParam := r.URL.Query().Get("nonInteractive")
		requestNonInteractive := strings.ToLower(nonInteractiveParam) == "true"

		s.Mutex.RLock()
		server, exists := s.Servers[serverName]
		s.Mutex.RUnlock()
		if !exists {
			for name, _server := range s.Servers {
				if matched, ok := wmcplib.FindBestToolMatch(_server.Tools, toolName, s.DrunkMode); ok {
					serverName = name
					server = _server
					toolName = matched
					exists = true
					break
				}
			}
		}

		arguments := make(map[string]interface{})
		debugLog("Parsing arguments for tool call")

		if r.Method == "POST" {
			contentType := r.Header.Get("Content-Type")
			if strings.Contains(contentType, "application/json") {
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
				if err := r.ParseForm(); err != nil {
					log.Printf("ERROR: Failed to parse form data for tool %s/%s: %v", serverName, toolName, err)
					http.Error(w, "Failed to parse form data", http.StatusBadRequest)
					return
				}
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
		} else {
			if err := r.ParseForm(); err != nil && err != http.ErrNotMultipart {
				log.Printf("ERROR: Failed to parse query for tool %s/%s: %v", serverName, toolName, err)
				http.Error(w, "Failed to parse query parameters", http.StatusBadRequest)
				return
			}
			debugLog("Query parameters: %v", r.URL.Query())
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
			var decision wmcplib.YoloDecision
			if requestNonInteractive {
				decision = wmcplib.YoloToolNotFound
			} else {
				decision = s.AskToolNotFound(toolName)
			}

			switch decision {
			case wmcplib.YoloToolNotFound:
				http.Error(w, fmt.Sprintf("Tool '%s' not found", toolName), http.StatusNotFound)
				return
			case wmcplib.YoloCustomResponse:
				customResponse, err := s.ReadCustomResponse("Enter your custom response: ")
				if err != nil || customResponse == "" {
					http.Error(w, fmt.Sprintf("Tool '%s' not found", toolName), http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(customResponse))
				return
			case wmcplib.YoloModify:
				callParams := wmcplib.CallToolParams{Name: toolName, Arguments: arguments}
				newParams, err := s.ModifyTool(&callParams)
				if err != nil {
					if errors.Is(err, wmcplib.ErrPromptCancelled) {
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

				s.Mutex.RLock()
				found := false
				for name, srv := range s.Servers {
					if matched, ok := wmcplib.FindBestToolMatch(srv.Tools, toolName, s.DrunkMode); ok {
						serverName = name
						server = srv
						toolName = matched
						exists = true
						found = true
						break
					}
				}
				s.Mutex.RUnlock()

				if !found {
					http.Error(w, fmt.Sprintf("tool '%s' not found", toolName), http.StatusNotFound)
					return
				}
			case wmcplib.YoloGuideModel:
				guideMsg := fmt.Sprintf("Tool '%s' not found. Provide clearer tool name or use available tools list.", toolName)
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(guideMsg))
				return
			default:
				http.Error(w, fmt.Sprintf("Server '%s' not found", serverName), http.StatusNotFound)
				return
			}
		}

		debugLog("Parsed arguments: %v", arguments)

		if s.DrunkMode && len(arguments) > 0 {
			var foundTool *wmcplib.Tool
			server.Mutex.RLock()
			for _, tool := range server.Tools {
				if tool.Name == toolName {
					t := tool
					foundTool = &t
					break
				}
			}
			server.Mutex.RUnlock()

			if foundTool != nil && len(foundTool.Parameters) > 0 {
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
					firstParam := foundTool.Parameters[0]
					newArgs := make(map[string]interface{})
					newArgs[firstParam.Name] = arguments[argKeys[0]]
					arguments = newArgs
					debugLog("Drunk mode: assigned single arg to first param %s", firstParam.Name)
				} else {
					newArgs := make(map[string]interface{})
					argIndex := 0
					for _, param := range foundTool.Parameters {
						if argIndex < len(argKeys) {
							newArgs[param.Name] = arguments[argKeys[argIndex]]
							argIndex++
						}
					}
					arguments = newArgs
					debugLog("Drunk mode: reassigned args in order to params")
				}
			}
		}

		toolRequest := wmcplib.JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "tools/call",
			Params:  wmcplib.CallToolParams{Name: toolName, Arguments: arguments},
			ID:      time.Now().UnixNano(),
		}

		debugLog("Calling tool %s on server %s with arguments: %v", toolName, serverName, arguments)

		response, err := s.SendRequest(server, toolRequest)
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

		nativeFormat := r.URL.Query().Get("native") == "true" || r.Header.Get("X-Native-Tool-Call") == "true"

		resultBytes, _ := json.Marshal(response.Result)
		var toolResult wmcplib.CallToolResult
		if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
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
				errorResponse := map[string]interface{}{"error": toolResult.Error.Message}
				json.NewEncoder(w).Encode(errorResponse)
			} else {
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(emsg))
			}
			debugLog(emsg)
			return
		}

		if nativeFormat {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(toolResult)
		} else {
			w.Header().Set("Content-Type", "text/plain")

			var output strings.Builder
			for i, content := range toolResult.Content {
				if i > 0 {
					output.WriteString("\n\n")
				}
				output.WriteString(content.Text)
			}

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

			debugLog("Response content: %s", output.String())

			log.Printf("SUCCESS: Tool %s/%s completed successfully", serverName, toolName)
			w.Write([]byte(output.String()))
		}
	}
}

func statusHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())
		s.Mutex.RLock()
		defer s.Mutex.RUnlock()

		var output strings.Builder
		output.WriteString("# MCP Service Status\n\n")

		for serverName, server := range s.Servers {
			server.Mutex.RLock()
			output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
			output.WriteString(fmt.Sprintf("Command: `%s`\n", server.Command))
			output.WriteString("Status: Running\n")
			output.WriteString(fmt.Sprintf("Tools: %d\n", len(server.Tools)))
			output.WriteString(fmt.Sprintf("Prompts: %d\n", len(server.Prompts)))
			output.WriteString(fmt.Sprintf("Resources: %d\n\n", len(server.Resources)))
			server.Mutex.RUnlock()
		}

		writeTextResponse(w, output.String())
	}
}

func openapiHandler(s *wmcplib.MCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("HTTP %s %s", r.Method, r.URL.String())

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Native-Tool-Call")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		spec := generateOpenAPISpec(s)
		json.NewEncoder(w).Encode(spec)
	}
}

func generateOpenAPISpec(s *wmcplib.MCPService) map[string]interface{} {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()

	description := "MCP Server API\n\n"
	description += "**Available Tools:**\n"

	if len(s.Servers) == 0 {
		description += "- No tools currently available\n"
	} else {
		for _, server := range s.Servers {
			server.Mutex.RLock()
			for _, tool := range server.Tools {
				description += fmt.Sprintf("- **%s**: %s\n", tool.Name, tool.Description)
			}
			server.Mutex.RUnlock()
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
			{"url": "http://localhost:8989", "description": "Local MCP server"},
		},
		"paths":      generateOpenAPIPaths(s),
		"components": generateOpenAPIComponents(s),
	}

	return spec
}

func generateOpenAPIPaths(s *wmcplib.MCPService) map[string]interface{} {
	paths := make(map[string]interface{})

	if s.ProxyToolsMode {
		for _, tool := range wmcplib.ProxyTools() {
			path := fmt.Sprintf("/v1/tool/%s", tool.Name)
			paths[path] = generateOpenAPIToolOperation("proxy", tool)
		}
		return paths
	}

	for serverName, server := range s.Servers {
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			path := fmt.Sprintf("/v1/tool/%s", tool.Name)
			paths[path] = generateOpenAPIToolOperation(serverName, tool)
		}
		server.Mutex.RUnlock()
	}

	return paths
}

func generateOpenAPIToolOperation(serverName string, tool wmcplib.Tool) map[string]interface{} {
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

	return map[string]interface{}{"post": operation}
}

func generateOpenAPIComponents(s *wmcplib.MCPService) map[string]interface{} {
	components := map[string]interface{}{
		"schemas": map[string]interface{}{
			"HTTPValidationError": map[string]interface{}{
				"properties": map[string]interface{}{
					"detail": map[string]interface{}{
						"items": map[string]interface{}{"$ref": "#/components/schemas/ValidationError"},
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

	schemas := components["schemas"].(map[string]interface{})

	addToolSchema := func(tool wmcplib.Tool) {
		if len(tool.Parameters) == 0 {
			return
		}
		properties := make(map[string]interface{})
		required := []string{}
		for _, param := range tool.Parameters {
			paramSchema := map[string]interface{}{
				"type":        param.Type,
				"title":       strings.Title(strings.ReplaceAll(param.Name, "_", " ")),
				"description": param.Description,
			}
			if param.Type == "array" {
				paramSchema["items"] = map[string]interface{}{"type": "string"}
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

	if s.ProxyToolsMode {
		for _, tool := range wmcplib.ProxyTools() {
			addToolSchema(tool)
		}
		return components
	}

	for _, server := range s.Servers {
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			addToolSchema(tool)
		}
		server.Mutex.RUnlock()
	}

	return components
}
