package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/trufae/mai/src/repl/llm"
	mcplib "mai/src/mcps/lib"
)

// ToolConfig holds the configuration for a tool
type ToolConfig struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	IsStreaming bool
	Handler     interface{} // func(args map[string]interface{}) (interface{}, error) or streaming version
}

// StreamingSession represents an ongoing streaming response
type StreamingSession struct {
	ID         string
	Response   string
	Chunks     []string
	CurrentIdx int
	Completed  bool
	CreatedAt  time.Time
}

// Global streaming session manager
var (
	streamingSessions = make(map[string]*StreamingSession)
	streamingMutex    sync.RWMutex
	sessionCounter    int64
)

// cleanupStreamingSessions removes expired streaming sessions
func cleanupStreamingSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		streamingMutex.Lock()
		now := time.Now()
		for id, session := range streamingSessions {
			// Remove sessions older than 30 minutes
			if now.Sub(session.CreatedAt) > 30*time.Minute {
				delete(streamingSessions, id)
			}
		}
		streamingMutex.Unlock()
	}
}

// sendMessageCore implements the shared send_message logic.
func sendMessageCore(repl *REPL, args map[string]interface{}) (mcplib.ToolCallResult, error) {
	message, ok := args["message"].(string)
	if !ok {
		return mcplib.ToolCallResult{IsError: true}, fmt.Errorf("message must be a string")
	}

	systemPrompt, _ := args["system_prompt"].(string)
	stream, _ := args["stream"].(bool)
	pageToken, _ := args["page_token"].(string)

	// continuation
	if pageToken != "" {
		streamingMutex.RLock()
		session, exists := streamingSessions[pageToken]
		streamingMutex.RUnlock()
		if !exists {
			return mcplib.ToolCallResult{IsError: true}, fmt.Errorf("invalid page token")
		}

		streamingMutex.Lock()
		if session.CurrentIdx >= len(session.Chunks) {
			delete(streamingSessions, pageToken)
			streamingMutex.Unlock()
			return mcplib.ToolCallResult{Content: []interface{}{map[string]interface{}{"type": "text", "text": ""}}, IsError: false}, nil
		}
		chunk := session.Chunks[session.CurrentIdx]
		session.CurrentIdx++
		hasMore := session.CurrentIdx < len(session.Chunks)
		streamingMutex.Unlock()

		res := mcplib.ToolCallResult{Content: []interface{}{map[string]interface{}{"type": "text", "text": chunk}}, IsError: false}
		if hasMore {
			res.NextPageToken = pageToken
		}
		return res, nil
	}

	// create client
	client, err := llm.NewLLMClient(repl.buildLLMConfig(), repl.ctx)
	if err != nil {
		return mcplib.ToolCallResult{IsError: true}, fmt.Errorf("failed to create LLM client: %v", err)
	}

	messages := []llm.Message{}
	if systemPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	} else if sp := repl.currentSystemPrompt(); sp != "" {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}
	messages = append(messages, llm.Message{Role: "user", Content: message})

	if stream {
		sessionID := fmt.Sprintf("%d", sessionCounter)
		sessionCounter++
		streamingMutex.Lock()
		streamingSessions[sessionID] = &StreamingSession{ID: sessionID, Chunks: []string{}, CurrentIdx: 0, Completed: false, CreatedAt: time.Now()}
		streamingMutex.Unlock()

		response, err := client.SendMessage(messages, true, nil, nil)
		if err != nil {
			streamingMutex.Lock()
			delete(streamingSessions, sessionID)
			streamingMutex.Unlock()
			return mcplib.ToolCallResult{IsError: true}, err
		}

		chunks := strings.Split(response, ". ")
		for i, chunk := range chunks {
			if i < len(chunks)-1 {
				chunks[i] = chunk + "."
			}
		}

		streamingMutex.Lock()
		session := streamingSessions[sessionID]
		session.Chunks = chunks
		session.Response = response
		session.Completed = true
		streamingMutex.Unlock()

		if len(chunks) > 0 {
			result := mcplib.ToolCallResult{Content: []interface{}{map[string]interface{}{"type": "text", "text": chunks[0]}}, IsError: false}
			if len(chunks) > 1 {
				result.NextPageToken = sessionID
			}
			return result, nil
		}

		streamingMutex.Lock()
		delete(streamingSessions, sessionID)
		streamingMutex.Unlock()
		return mcplib.ToolCallResult{Content: []interface{}{map[string]interface{}{"type": "text", "text": ""}}, IsError: false}, nil
	}

	// non-stream
	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		return mcplib.ToolCallResult{IsError: true}, err
	}
	return mcplib.ToolCallResult{Content: []interface{}{map[string]interface{}{"type": "text", "text": response}}, IsError: false}, nil
}

// Streaming wrapper for MCP server
func sendMessageStreamingHandler(repl *REPL) func(args map[string]interface{}, sendChunk func(mcplib.ToolCallResult) error) (mcplib.ToolCallResult, error) {
	return func(args map[string]interface{}, sendChunk func(mcplib.ToolCallResult) error) (mcplib.ToolCallResult, error) {
		return sendMessageCore(repl, args)
	}
}

// DSL wrapper used by getREPLTools
func sendMessageDSLHandler(repl *REPL) func(args map[string]interface{}) (interface{}, error) {
	return func(args map[string]interface{}) (interface{}, error) {
		res, err := sendMessageCore(repl, args)
		if err != nil {
			return nil, err
		}
		out := map[string]interface{}{"content": res.Content, "isError": res.IsError}
		if res.NextPageToken != "" {
			out["next_page_token"] = res.NextPageToken
		}
		return out, nil
	}
}

// getREPLToolConfigs returns all tool configurations for mai-repl
func getREPLToolConfigs(repl *REPL) []ToolConfig {
	// TODO: implement with all tools
	return []ToolConfig{}
}

// StartMCPServer starts the MCP server with all mai-repl tools
func StartMCPServer(repl *REPL) {
	// Start cleanup goroutine for streaming sessions
	go cleanupStreamingSessions()

	// Define all the tools
	tools := []mcplib.ToolDefinition{
		{
			Name:        "send_message",
			Description: "Send a message to the AI model and get a response",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to send to the AI",
					},
					"system_prompt": map[string]interface{}{
						"type":        "string",
						"description": "Optional system prompt to use for this message",
					},
					"stream": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to stream the response in chunks",
						"default":     false,
					},
					"page_token": map[string]interface{}{
						"type":        "string",
						"description": "Token for continuing a streaming response",
					},
				},
				"required": []string{"message"},
			},
		},
		{
			Name:        "get_config",
			Description: "Get the value of a configuration option",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "The configuration key to get",
					},
				},
				"required": []string{"key"},
			},
		},
		{
			Name:        "set_config",
			Description: "Set a configuration option",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "The configuration key to set",
					},
					"value": map[string]interface{}{
						"description": "The value to set",
					},
				},
				"required": []string{"key", "value"},
			},
		},
		{
			Name:        "list_config",
			Description: "Get all configuration settings",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},

		{
			Name:        "execute_command",
			Description: "Execute a mai-repl command",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The command to execute (without the leading slash)",
					},
					"args": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Arguments for the command",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "load_prompt",
			Description: "Load and use a prompt from the prompts directory",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the prompt file (without .md extension)",
					},
					"extra": map[string]interface{}{
						"type":        "string",
						"description": "Additional text to append to the prompt",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "list_prompts",
			Description: "List all available prompts",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "use_template",
			Description: "Use a template with variable substitution",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the template file",
					},
					"variables": map[string]interface{}{
						"type":        "object",
						"description": "Key-value pairs for template variables",
					},
				},
				"required": []string{"name", "variables"},
			},
		},
		{
			Name:        "list_templates",
			Description: "List all available templates",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "add_file",
			Description: "Add a file to be included in the next message",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to add",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "add_image",
			Description: "Add an image to be included in the next message",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the image file to add",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "clear_files",
			Description: "Clear all pending files",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "clear_images",
			Description: "Clear all pending images",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "get_conversation",
			Description: "Get the current conversation history",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"format": map[string]interface{}{
						"type":        "string",
						"description": "Format: 'list' for summary or 'full' for complete messages",
						"enum":        []string{"list", "full"},
						"default":     "list",
					},
				},
			},
		},
		{
			Name:        "clear_conversation",
			Description: "Clear the conversation history",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "save_session",
			Description: "Save the current conversation to a session file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name for the session file",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "load_session",
			Description: "Load a conversation from a session file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the session file to load",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "list_sessions",
			Description: "List all saved sessions",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "execute_shell",
			Description: "Execute a shell command",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute",
					},
				},
				"required": []string{"command"},
			},
		},

		{
			Name:        "get_models",
			Description: "Get available models with metadata (id, description, current)",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "list_providers",
			Description: "List available AI providers",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "set_provider",
			Description: "Set the AI provider",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"provider": map[string]interface{}{
						"type":        "string",
						"description": "The provider to set (ollama, openai, claude, etc.)",
					},
				},
				"required": []string{"provider"},
			},
		},
		{
			Name:        "set_model",
			Description: "Set the AI model",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"model": map[string]interface{}{
						"type":        "string",
						"description": "The model to set",
					},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "get_last_reply",
			Description: "Get the last assistant reply",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "cancel_request",
			Description: "Cancel the current AI request",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}

	// Create MCP server
	server := mcplib.NewMCPServer(tools)

	// Register streaming tool handler for send_message
	server.RegisterStreamingTool("send_message", sendMessageStreamingHandler(repl))

	server.RegisterTool("get_config", func(args map[string]interface{}) (interface{}, error) {
		key, ok := args["key"].(string)
		if !ok {
			return nil, fmt.Errorf("key must be a string")
		}
		value := repl.configOptions.Get(key)
		return map[string]interface{}{"key": key, "value": value}, nil
	})

	server.RegisterTool("set_config", func(args map[string]interface{}) (interface{}, error) {
		key, ok := args["key"].(string)
		if !ok {
			return nil, fmt.Errorf("key must be a string")
		}
		value := args["value"]
		var strValue string
		switch v := value.(type) {
		case string:
			strValue = v
		case bool:
			if v {
				strValue = "true"
			} else {
				strValue = "false"
			}
		case float64:
			strValue = fmt.Sprintf("%g", v)
		default:
			strValue = fmt.Sprintf("%v", v)
		}
		err := repl.configOptions.Set(key, strValue)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("Set %s = %s", key, strValue), nil
	})

	server.RegisterTool("list_config", func(args map[string]interface{}) (interface{}, error) {
		options := repl.configOptions.GetAvailableOptions()
		result := make(map[string]interface{})
		for _, key := range options {
			val := repl.configOptions.Get(key)
			info, _ := repl.configOptions.GetOptionInfo(key)
			switch info.Type {
			case BooleanOption:
				result[key] = repl.configOptions.GetBool(key)
			case NumberOption:
				num, _ := repl.configOptions.GetNumber(key)
				result[key] = num
			default:
				result[key] = val
			}
		}
		return result, nil
	})

	server.RegisterTool("execute_command", func(args map[string]interface{}) (interface{}, error) {
		command, ok := args["command"].(string)
		if !ok {
			return nil, fmt.Errorf("command must be a string")
		}

		var cmdArgs []string
		if args["args"] != nil {
			if argsSlice, ok := args["args"].([]interface{}); ok {
				for _, arg := range argsSlice {
					if strArg, ok := arg.(string); ok {
						cmdArgs = append(cmdArgs, strArg)
					}
				}
			}
		}

		// Capture command output by redirecting stdout
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create pipe: %v", err)
		}
		os.Stdout = w

		// Execute the command
		cmdErr := repl.handleCommand("/"+command, "", "")

		// Restore stdout
		w.Close()
		os.Stdout = oldStdout

		// Read the captured output
		output, readErr := io.ReadAll(r)
		r.Close()

		if cmdErr != nil {
			return nil, cmdErr
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read command output: %v", readErr)
		}

		return string(output), nil
	})

	server.RegisterTool("load_prompt", func(args map[string]interface{}) (interface{}, error) {
		name, ok := args["name"].(string)
		if !ok {
			return nil, fmt.Errorf("name must be a string")
		}
		extra, _ := args["extra"].(string)

		expandedInput, err := repl.loadPrompt(name, extra)
		if err != nil {
			return nil, err
		}

		// Send to AI
		err = repl.sendToAI(expandedInput, "", "", true, false)
		if err != nil {
			return nil, err
		}

		content, err := repl.getLastAssistantReply()
		if err != nil {
			return "Prompt loaded and sent successfully", nil
		}
		return content, nil
	})

	server.RegisterTool("list_prompts", func(args map[string]interface{}) (interface{}, error) {
		prompts, err := repl.listPrompts()
		if err != nil {
			return nil, err
		}
		return prompts, nil
	})

	server.RegisterTool("use_template", func(args map[string]interface{}) (interface{}, error) {
		name, ok := args["name"].(string)
		if !ok {
			return nil, fmt.Errorf("name must be a string")
		}

		variables, ok := args["variables"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("variables must be an object")
		}

		// Convert variables to key=value format
		var keyValues []string
		for key, value := range variables {
			keyValues = append(keyValues, fmt.Sprintf("%s=%v", key, value))
		}

		// Execute template command
		cmdArgs := []string{name}
		cmdArgs = append(cmdArgs, keyValues...)

		err := repl.handleTemplateSlashCommand(cmdArgs)
		if err != nil {
			return nil, err
		}

		content, err := repl.getLastAssistantReply()
		if err != nil {
			return "Template used successfully", nil
		}
		return content, nil
	})

	server.RegisterTool("list_templates", func(args map[string]interface{}) (interface{}, error) {
		// This would need to be implemented in REPL
		return []string{}, nil // Placeholder
	})

	server.RegisterTool("add_file", func(args map[string]interface{}) (interface{}, error) {
		path, ok := args["path"].(string)
		if !ok {
			return nil, fmt.Errorf("path must be a string")
		}

		message, err := repl.addFile(path)
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(message), nil
	})

	server.RegisterTool("add_image", func(args map[string]interface{}) (interface{}, error) {
		path, ok := args["path"].(string)
		if !ok {
			return nil, fmt.Errorf("path must be a string")
		}

		message, err := repl.addImage(path)
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(message), nil
	})

	server.RegisterTool("clear_files", func(args map[string]interface{}) (interface{}, error) {
		message, err := repl.clearPendingFiles()
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(message), nil
	})

	server.RegisterTool("clear_images", func(args map[string]interface{}) (interface{}, error) {
		message, err := repl.clearPendingImages()
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(message), nil
	})

	server.RegisterTool("get_conversation", func(args map[string]interface{}) (interface{}, error) {
		format, _ := args["format"].(string)
		if format == "full" {
			return repl.displayFullConversationLog(), nil
		}
		return repl.displayConversationLog(), nil
	})

	server.RegisterTool("clear_conversation", func(args map[string]interface{}) (interface{}, error) {
		repl.messages = []llm.Message{}
		return "Conversation cleared", nil
	})

	server.RegisterTool("save_session", func(args map[string]interface{}) (interface{}, error) {
		name, ok := args["name"].(string)
		if !ok {
			return nil, fmt.Errorf("name must be a string")
		}

		err := repl.saveSession(name)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("Session saved as %s", name), nil
	})

	server.RegisterTool("load_session", func(args map[string]interface{}) (interface{}, error) {
		name, ok := args["name"].(string)
		if !ok {
			return nil, fmt.Errorf("name must be a string")
		}

		err := repl.loadSession(name)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("Session %s loaded", name), nil
	})

	server.RegisterTool("list_sessions", func(args map[string]interface{}) (interface{}, error) {
		output, err := repl.listSessions()
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(output), nil
	})

	server.RegisterTool("execute_shell", func(args map[string]interface{}) (interface{}, error) {
		command, ok := args["command"].(string)
		if !ok {
			return nil, fmt.Errorf("command must be a string")
		}

		err := repl.executeShellCommand(command)
		if err != nil {
			return nil, err
		}
		return "Command executed successfully", nil
	})

	server.RegisterTool("get_models", func(args map[string]interface{}) (interface{}, error) {
		// Create client
		client, err := llm.NewLLMClient(repl.buildLLMConfig(), repl.ctx)
		if err != nil {
			// Return minimal shape on error
			return []map[string]interface{}{
				{"id": "model1", "description": "", "current": false},
				{"id": "model2", "description": "", "current": false},
			}, nil
		}

		currentModel := repl.configOptions.Get("ai.model")
		provider := repl.configOptions.Get("ai.provider")

		// Check for API keys and return dummy if not available
		switch provider {
		case "openai":
			if os.Getenv("OPENAI_API_KEY") == "" {
				return []map[string]interface{}{
					{"id": "gpt-4o", "description": "GPT-4 Optimized", "current": "gpt-4o" == currentModel},
					{"id": "gpt-4", "description": "GPT-4", "current": "gpt-4" == currentModel},
					{"id": "gpt-3.5-turbo", "description": "GPT-3.5 Turbo", "current": "gpt-3.5-turbo" == currentModel},
				}, nil
			}
		case "claude":
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				return []map[string]interface{}{
					{"id": "claude-3-5-sonnet-20241022", "description": "Claude 3.5 Sonnet", "current": "claude-3-5-sonnet-20241022" == currentModel},
					{"id": "claude-3-haiku-20240307", "description": "Claude 3 Haiku", "current": "claude-3-haiku-20240307" == currentModel},
				}, nil
			}
			// Add more providers as needed
		}

		// Fetch models
		models, err := client.ListModels()
		if err != nil {
			return []map[string]interface{}{
				{"id": "model1", "description": "", "current": false},
				{"id": "model2", "description": "", "current": false},
			}, nil
		}

		out := make([]map[string]interface{}, 0, len(models))
		for _, m := range models {
			desc := m.Description
			if desc == "" {
				desc = m.Name
			}
			out = append(out, map[string]interface{}{
				"id":          m.ID,
				"description": desc,
				"current":     m.ID == currentModel,
			})
		}
		// Fallback dummy if provider returns none
		if len(out) == 0 {
			out = []map[string]interface{}{
				{"id": "model1", "description": "", "current": currentModel == "model1"},
				{"id": "model2", "description": "", "current": currentModel == "model2"},
			}
		}
		return out, nil
	})

	server.RegisterTool("list_providers", func(args map[string]interface{}) (interface{}, error) {
		providers := llm.GetValidProvidersList()

		available := make([]string, 0, len(providers))
		for _, provider := range providers {
			if repl.isProviderAvailable(provider) {
				available = append(available, provider)
			}
		}
		return available, nil
	})

	server.RegisterTool("set_provider", func(args map[string]interface{}) (interface{}, error) {
		provider, ok := args["provider"].(string)
		if !ok {
			return nil, fmt.Errorf("provider must be a string")
		}

		err := repl.setProvider(provider)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("Provider set to %s", provider), nil
	})

	server.RegisterTool("set_model", func(args map[string]interface{}) (interface{}, error) {
		model, ok := args["model"].(string)
		if !ok {
			return nil, fmt.Errorf("model must be a string")
		}

		err := repl.setModel(model)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("Model set to %s", model), nil
	})

	server.RegisterTool("get_last_reply", func(args map[string]interface{}) (interface{}, error) {
		content, err := repl.getLastAssistantReply()
		if err != nil {
			return nil, err
		}
		return content, nil
	})

	server.RegisterTool("cancel_request", func(args map[string]interface{}) (interface{}, error) {
		// Cancel any ongoing streaming sessions
		streamingMutex.Lock()
		for id := range streamingSessions {
			delete(streamingSessions, id)
		}
		streamingMutex.Unlock()

		repl.cancel()
		repl.ctx, repl.cancel = context.WithCancel(context.Background())
		return "Request cancelled", nil
	})

	// Start the server
	fmt.Fprintf(os.Stderr, "Starting MCP server for mai-repl...\n")
	server.Start()
}

// getREPLTools returns all mai-repl tools as mcplib.Tool slice for DSL usage
func getREPLTools(repl *REPL) []mcplib.Tool {
	return []mcplib.Tool{
		{
			Name:        "send_message",
			Description: "Send a message to the AI model and get a response",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to send to the AI",
					},
					"system_prompt": map[string]interface{}{
						"type":        "string",
						"description": "Optional system prompt to use for this message",
					},
					"stream": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to stream the response in chunks",
						"default":     false,
					},
					"page_token": map[string]interface{}{
						"type":        "string",
						"description": "Token for continuing a streaming response",
					},
				},
				"required": []string{"message"},
			},
			Handler: sendMessageDSLHandler(repl),
		},
		{
			Name:        "get_config",
			Description: "Get the value of a configuration option",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "The configuration key to get",
					},
				},
				"required": []string{"key"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				key, ok := args["key"].(string)
				if !ok {
					return nil, fmt.Errorf("key must be a string")
				}
				value := repl.configOptions.Get(key)
				return map[string]interface{}{"key": key, "value": value}, nil
			},
		},
		{
			Name:        "set_config",
			Description: "Set a configuration option",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "The configuration key to set",
					},
					"value": map[string]interface{}{
						"description": "The value to set",
					},
				},
				"required": []string{"key", "value"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				key, ok := args["key"].(string)
				if !ok {
					return nil, fmt.Errorf("key must be a string")
				}
				value := args["value"]
				var strValue string
				switch v := value.(type) {
				case string:
					strValue = v
				case bool:
					if v {
						strValue = "true"
					} else {
						strValue = "false"
					}
				case float64:
					strValue = fmt.Sprintf("%g", v)
				default:
					strValue = fmt.Sprintf("%v", v)
				}
				err := repl.configOptions.Set(key, strValue)
				if err != nil {
					return nil, err
				}
				return fmt.Sprintf("Set %s = %s", key, strValue), nil
			},
		},
		{
			Name:        "list_config",
			Description: "Get all configuration settings",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				options := repl.configOptions.GetAvailableOptions()
				result := make(map[string]interface{})
				for _, key := range options {
					val := repl.configOptions.Get(key)
					info, _ := repl.configOptions.GetOptionInfo(key)
					switch info.Type {
					case BooleanOption:
						result[key] = repl.configOptions.GetBool(key)
					case NumberOption:
						num, _ := repl.configOptions.GetNumber(key)
						result[key] = num
					default:
						result[key] = val
					}
				}
				return result, nil
			},
		},
		{
			Name:        "execute_command",
			Description: "Execute a mai-repl command",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The command to execute (without the leading slash)",
					},
					"args": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Arguments for the command",
					},
				},
				"required": []string{"command"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				command, ok := args["command"].(string)
				if !ok {
					return nil, fmt.Errorf("command must be a string")
				}

				// Execute the command
				err := repl.handleCommand("/"+command, "", "")
				if err != nil {
					return nil, err
				}
				return "Command executed successfully", nil
			},
		},
		{
			Name:        "load_prompt",
			Description: "Load and use a prompt from the prompts directory",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the prompt file (without .md extension)",
					},
					"extra": map[string]interface{}{
						"type":        "string",
						"description": "Additional text to append to the prompt",
					},
				},
				"required": []string{"name"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				name, ok := args["name"].(string)
				if !ok {
					return nil, fmt.Errorf("name must be a string")
				}
				extra, _ := args["extra"].(string)

				expandedInput, err := repl.loadPrompt(name, extra)
				if err != nil {
					return nil, err
				}

				// Send to AI
				err = repl.sendToAI(expandedInput, "", "", true, false)
				if err != nil {
					return nil, err
				}

				content, err := repl.getLastAssistantReply()
				if err != nil {
					return "Prompt loaded and sent successfully", nil
				}
				return content, nil
			},
		},
		{
			Name:        "list_prompts",
			Description: "List all available prompts",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				prompts, err := repl.listPrompts()
				if err != nil {
					return nil, err
				}
				return prompts, nil
			},
		},
		{
			Name:        "use_template",
			Description: "Use a template with variable substitution",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the template file",
					},
					"variables": map[string]interface{}{
						"type":        "object",
						"description": "Key-value pairs for template variables",
					},
				},
				"required": []string{"name", "variables"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				name, ok := args["name"].(string)
				if !ok {
					return nil, fmt.Errorf("name must be a string")
				}

				variables, ok := args["variables"].(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("variables must be an object")
				}

				// Convert variables to key=value format
				var keyValues []string
				for key, value := range variables {
					keyValues = append(keyValues, fmt.Sprintf("%s=%v", key, value))
				}

				// Execute template command
				cmdArgs := []string{name}
				cmdArgs = append(cmdArgs, keyValues...)

				err := repl.handleTemplateSlashCommand(cmdArgs)
				if err != nil {
					return nil, err
				}

				content, err := repl.getLastAssistantReply()
				if err != nil {
					return "Template used successfully", nil
				}
				return content, nil
			},
		},
		{
			Name:        "list_templates",
			Description: "List all available templates",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				// This would need to be implemented in REPL
				return []string{}, nil // Placeholder
			},
		},
		{
			Name:        "add_file",
			Description: "Add a file to be included in the next message",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to add",
					},
				},
				"required": []string{"path"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				path, ok := args["path"].(string)
				if !ok {
					return nil, fmt.Errorf("path must be a string")
				}

				message, err := repl.addFile(path)
				if err != nil {
					return nil, err
				}
				return strings.TrimSpace(message), nil
			},
		},
		{
			Name:        "add_image",
			Description: "Add an image to be included in the next message",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the image file to add",
					},
				},
				"required": []string{"path"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				path, ok := args["path"].(string)
				if !ok {
					return nil, fmt.Errorf("path must be a string")
				}

				message, err := repl.addImage(path)
				if err != nil {
					return nil, err
				}
				return strings.TrimSpace(message), nil
			},
		},
		{
			Name:        "clear_files",
			Description: "Clear all pending files",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				message, err := repl.clearPendingFiles()
				if err != nil {
					return nil, err
				}
				return strings.TrimSpace(message), nil
			},
		},
		{
			Name:        "clear_images",
			Description: "Clear all pending images",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				message, err := repl.clearPendingImages()
				if err != nil {
					return nil, err
				}
				return strings.TrimSpace(message), nil
			},
		},
		{
			Name:        "get_conversation",
			Description: "Get the current conversation history",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"format": map[string]interface{}{
						"type":        "string",
						"description": "Format: 'list' for summary or 'full' for complete messages",
						"enum":        []string{"list", "full"},
						"default":     "list",
					},
				},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				format, _ := args["format"].(string)
				if format == "full" {
					return repl.displayFullConversationLog(), nil
				}
				return repl.displayConversationLog(), nil
			},
		},
		{
			Name:        "clear_conversation",
			Description: "Clear the conversation history",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				repl.messages = []llm.Message{}
				return "Conversation cleared", nil
			},
		},
		{
			Name:        "save_session",
			Description: "Save the current conversation to a session file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name for the session file",
					},
				},
				"required": []string{"name"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				name, ok := args["name"].(string)
				if !ok {
					return nil, fmt.Errorf("name must be a string")
				}

				err := repl.saveSession(name)
				if err != nil {
					return nil, err
				}
				return fmt.Sprintf("Session saved as %s", name), nil
			},
		},
		{
			Name:        "load_session",
			Description: "Load a conversation from a session file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the session file to load",
					},
				},
				"required": []string{"name"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				name, ok := args["name"].(string)
				if !ok {
					return nil, fmt.Errorf("name must be a string")
				}

				err := repl.loadSession(name)
				if err != nil {
					return nil, err
				}
				return fmt.Sprintf("Session %s loaded", name), nil
			},
		},
		{
			Name:        "list_sessions",
			Description: "List all saved sessions",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				output, err := repl.listSessions()
				if err != nil {
					return nil, err
				}
				return strings.TrimSpace(output), nil
			},
		},
		{
			Name:        "execute_shell",
			Description: "Execute a shell command",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute",
					},
				},
				"required": []string{"command"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				command, ok := args["command"].(string)
				if !ok {
					return nil, fmt.Errorf("command must be a string")
				}

				err := repl.executeShellCommand(command)
				if err != nil {
					return nil, err
				}
				return "Command executed successfully", nil
			},
		},
		{
			Name:        "get_models",
			Description: "Get available models with metadata (id, description, current)",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				// Create client
				client, err := llm.NewLLMClient(repl.buildLLMConfig(), repl.ctx)
				if err != nil {
					// Return minimal shape on error
					return []map[string]interface{}{
						{"id": "model1", "description": "", "current": false},
						{"id": "model2", "description": "", "current": false},
					}, nil
				}

				currentModel := repl.configOptions.Get("ai.model")
				provider := repl.configOptions.Get("ai.provider")

				// Check for API keys and return dummy if not available
				switch provider {
				case "openai":
					if os.Getenv("OPENAI_API_KEY") == "" {
						return []map[string]interface{}{
							{"id": "gpt-4o", "description": "GPT-4 Optimized", "current": "gpt-4o" == currentModel},
							{"id": "gpt-4", "description": "GPT-4", "current": "gpt-4" == currentModel},
							{"id": "gpt-3.5-turbo", "description": "GPT-3.5 Turbo", "current": "gpt-3.5-turbo" == currentModel},
						}, nil
					}
				case "claude":
					if os.Getenv("ANTHROPIC_API_KEY") == "" {
						return []map[string]interface{}{
							{"id": "claude-3-5-sonnet-20241022", "description": "Claude 3.5 Sonnet", "current": "claude-3-5-sonnet-20241022" == currentModel},
							{"id": "claude-3-haiku-20240307", "description": "Claude 3 Haiku", "current": "claude-3-haiku-20240307" == currentModel},
						}, nil
					}
					// Add more providers as needed
				}

				// Fetch models
				models, err := client.ListModels()
				if err != nil {
					return []map[string]interface{}{
						{"id": "model1", "description": "", "current": false},
						{"id": "model2", "description": "", "current": false},
					}, nil
				}

				out := make([]map[string]interface{}, 0, len(models))
				for _, m := range models {
					desc := m.Description
					if desc == "" {
						desc = m.Name
					}
					out = append(out, map[string]interface{}{
						"id":          m.ID,
						"description": desc,
						"current":     m.ID == currentModel,
					})
				}
				// Fallback dummy if provider returns none
				if len(out) == 0 {
					out = []map[string]interface{}{
						{"id": "model1", "description": "", "current": currentModel == "model1"},
						{"id": "model2", "description": "", "current": currentModel == "model2"},
					}
				}
				return out, nil
			},
		},
		{
			Name:        "list_providers",
			Description: "List available AI providers",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				providers := llm.GetValidProvidersList()
				available := make([]string, 0, len(providers))
				for _, provider := range providers {
					if repl.isProviderAvailable(provider) {
						available = append(available, provider)
					}
				}
				return available, nil
			},
		},
		{
			Name:        "set_provider",
			Description: "Set the AI provider",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"provider": map[string]interface{}{
						"type":        "string",
						"description": "The provider to set (ollama, openai, claude, etc.)",
					},
				},
				"required": []string{"provider"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				provider, ok := args["provider"].(string)
				if !ok {
					return nil, fmt.Errorf("provider must be a string")
				}

				err := repl.setProvider(provider)
				if err != nil {
					return nil, err
				}
				return fmt.Sprintf("Provider set to %s", provider), nil
			},
		},
		{
			Name:        "set_model",
			Description: "Set the AI model",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"model": map[string]interface{}{
						"type":        "string",
						"description": "The model to set",
					},
				},
				"required": []string{"model"},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				model, ok := args["model"].(string)
				if !ok {
					return nil, fmt.Errorf("model must be a string")
				}

				err := repl.setModel(model)
				if err != nil {
					return nil, err
				}
				return fmt.Sprintf("Model set to %s", model), nil
			},
		},
		{
			Name:        "get_last_reply",
			Description: "Get the last assistant reply",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				content, err := repl.getLastAssistantReply()
				if err != nil {
					return nil, err
				}
				return content, nil
			},
		},
		{
			Name:        "cancel_request",
			Description: "Cancel the current AI request",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			Handler: func(args map[string]interface{}) (interface{}, error) {
				// Cancel any ongoing streaming sessions
				streamingMutex.Lock()
				for id := range streamingSessions {
					delete(streamingSessions, id)
				}
				streamingMutex.Unlock()

				repl.cancel()
				repl.ctx, repl.cancel = context.WithCancel(context.Background())
				return "Request cancelled", nil
			},
		},
	}
}
