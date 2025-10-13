package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/trufae/mai/src/repl/llm"
	mcplib "mai/src/mcps/lib"
)

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
						"type":        "string",
						"description": "The value to set",
					},
				},
				"required": []string{"key", "value"},
			},
		},
		{
			Name:        "list_config",
			Description: "List all available configuration options",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "get_settings",
			Description: "Get all configuration settings",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "set_setting",
			Description: "Set a configuration setting",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The setting name",
					},
					"value": map[string]interface{}{
						"description": "The setting value",
					},
				},
				"required": []string{"name", "value"},
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
			Name:        "list_models",
			Description: "List available models for the current provider",
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
	server.RegisterStreamingTool("send_message", func(args map[string]interface{}, sendChunk func(mcplib.ToolCallResult) error) (mcplib.ToolCallResult, error) {
		message, ok := args["message"].(string)
		if !ok {
			return mcplib.ToolCallResult{IsError: true}, fmt.Errorf("message must be a string")
		}

		systemPrompt, _ := args["system_prompt"].(string)
		stream, _ := args["stream"].(bool)
		pageToken, _ := args["page_token"].(string)

		// Check if this is a continuation of a streaming session
		if pageToken != "" {
			streamingMutex.RLock()
			session, exists := streamingSessions[pageToken]
			streamingMutex.RUnlock()

			if !exists {
				return mcplib.ToolCallResult{IsError: true}, fmt.Errorf("invalid page token")
			}

			// Return next chunk
			streamingMutex.Lock()
			if session.CurrentIdx >= len(session.Chunks) {
				// No more chunks
				delete(streamingSessions, pageToken)
				streamingMutex.Unlock()
				return mcplib.ToolCallResult{
					Content: []interface{}{map[string]interface{}{"type": "text", "text": ""}},
					IsError: false,
				}, nil
			}

			chunk := session.Chunks[session.CurrentIdx]
			session.CurrentIdx++
			hasMore := session.CurrentIdx < len(session.Chunks)
			streamingMutex.Unlock()

			result := mcplib.ToolCallResult{
				Content: []interface{}{map[string]interface{}{"type": "text", "text": chunk}},
				IsError: false,
			}
			if hasMore {
				result.NextPageToken = pageToken
			}
			return result, nil
		}

		// Create LLM client
		client, err := llm.NewLLMClient(repl.buildLLMConfig())
		if err != nil {
			return mcplib.ToolCallResult{IsError: true}, fmt.Errorf("failed to create LLM client: %v", err)
		}

		// Prepare messages
		messages := []llm.Message{}
		if systemPrompt != "" {
			messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
		} else if sp := repl.currentSystemPrompt(); sp != "" {
			messages = append(messages, llm.Message{Role: "system", Content: sp})
		}
		messages = append(messages, llm.Message{Role: "user", Content: message})

		if stream {
			// For streaming, we need to capture chunks
			sessionID := fmt.Sprintf("%d", sessionCounter)
			sessionCounter++

			streamingMutex.Lock()
			streamingSessions[sessionID] = &StreamingSession{
				ID:         sessionID,
				Chunks:     []string{},
				CurrentIdx: 0,
				Completed:  false,
				CreatedAt:  time.Now(),
			}
			streamingMutex.Unlock()

			// Send message with streaming
			response, err := client.SendMessage(messages, true, nil)
			if err != nil {
				streamingMutex.Lock()
				delete(streamingSessions, sessionID)
				streamingMutex.Unlock()
				return mcplib.ToolCallResult{IsError: true}, err
			}

			// Split response into chunks (simple approach - split by sentences)
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

			// Return first chunk
			if len(chunks) > 0 {
				result := mcplib.ToolCallResult{
					Content: []interface{}{map[string]interface{}{"type": "text", "text": chunks[0]}},
					IsError: false,
				}
				if len(chunks) > 1 {
					result.NextPageToken = sessionID
				}
				return result, nil
			}

			// Empty response
			streamingMutex.Lock()
			delete(streamingSessions, sessionID)
			streamingMutex.Unlock()
			return mcplib.ToolCallResult{
				Content: []interface{}{map[string]interface{}{"type": "text", "text": ""}},
				IsError: false,
			}, nil
		} else {
			// Non-streaming response
			response, err := client.SendMessage(messages, false, nil)
			if err != nil {
				return mcplib.ToolCallResult{IsError: true}, err
			}

			return mcplib.ToolCallResult{
				Content: []interface{}{map[string]interface{}{"type": "text", "text": response}},
				IsError: false,
			}, nil
		}
	})

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
		value, ok := args["value"].(string)
		if !ok {
			return nil, fmt.Errorf("value must be a string")
		}
		repl.configOptions.Set(key, value)
		return fmt.Sprintf("Set %s = %s", key, value), nil
	})

	server.RegisterTool("list_config", func(args map[string]interface{}) (interface{}, error) {
		options := repl.configOptions.GetAvailableOptions()
		result := make(map[string]interface{})
		for _, key := range options {
			result[key] = repl.configOptions.Get(key)
		}
		return result, nil
	})

	server.RegisterTool("get_settings", func(args map[string]interface{}) (interface{}, error) {
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

	server.RegisterTool("set_setting", func(args map[string]interface{}) (interface{}, error) {
		name, ok := args["name"].(string)
		if !ok {
			return nil, fmt.Errorf("name must be string")
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
		err := repl.configOptions.Set(name, strValue)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("Set %s = %s", name, strValue), nil
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

		// Execute the command
		err := repl.handleCommand("/"+command, "", "")
		if err != nil {
			return nil, err
		}
		return "Command executed successfully", nil
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

	server.RegisterTool("list_models", func(args map[string]interface{}) (interface{}, error) {
		// Create client
		client, err := llm.NewLLMClient(repl.buildLLMConfig())
		if err != nil {
			// Return dummy list on error
			return []string{"model1", "model2"}, nil
		}

		// Get models from the provider
		models, err := client.ListModels()
		if err != nil {
			// Return dummy list on error
			return []string{"model1", "model2"}, nil
		}

		// Extract model IDs
		modelIDs := make([]string, len(models))
		for i, model := range models {
			modelIDs[i] = model.ID
		}

		if len(modelIDs) == 0 {
			// Return dummy list if empty
			return []string{"model1", "model2"}, nil
		}

		return modelIDs, nil
	})

	server.RegisterTool("list_providers", func(args map[string]interface{}) (interface{}, error) {
		validProviders := repl.getValidProviders()

		// Extract provider names and sort them
		providers := make([]string, 0, len(validProviders))
		for provider := range validProviders {
			// Skip aliases (like "google" for "gemini" and "aws" for "bedrock")
			if provider == "google" || provider == "aws" {
				continue
			}
			// Only include available providers
			if repl.isProviderAvailable(provider) {
				providers = append(providers, provider)
			}
		}
		sort.Strings(providers)
		return providers, nil
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
