package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

// registerChatCommands registers conversation management commands
func registerChatCommands(r *REPL) {
	// Conversation management commands
	r.commands["/chat"] = Command{
		Name:        "/chat",
		Description: "Manage conversation (save, load, clear, list, log, undo, compact)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleChatCommand(args)
		},
	}
	// Session management commands
	r.commands["/session"] = Command{
		Name:        "/session",
		Description: "Manage chat sessions (new, list, use, del, purge)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleSessionCommand(args)
		},
	}

	r.commands["/cancel"] = Command{
		Name:        "/cancel",
		Description: "Cancel current request",
		Handler: func(r *REPL, args []string) (string, error) {
			r.cancel()
			r.ctx, r.cancel = context.WithCancel(context.Background())
			return "", nil
		},
	}

	r.commands["/clear"] = Command{
		Name:        "/clear",
		Description: "Clear conversation messages",
		Handler: func(r *REPL, args []string) (string, error) {
			r.messages = []llm.Message{}
			return "Conversation messages cleared\r\n", nil
		},
	}

	r.commands["\"\""] = Command{
		Name:        "_",
		Description: "Print the last assistant reply",
		Handler: func(r *REPL, args []string) (string, error) {
			err := r.sendToAI("", "", "", true, false)
			return "", err
		},
	}
	// Last reply command
	r.commands["_"] = Command{
		Name:        "_",
		Description: "Print the last assistant reply",
		Handler: func(r *REPL, args []string) (string, error) {
			content, err := r.getLastAssistantReply()
			if err != nil {
				return fmt.Sprintf("%v\r\n", err), nil
			}

			// Return the content with markdown rendering if enabled
			if r.configOptions.GetBool("ui.markdown") {
				return llm.RenderMarkdown(content) + "\r\n", nil
			} else {
				// Replace single newlines with \r\n for proper terminal display
				content = strings.ReplaceAll(content, "\n", "\r\n")
				return content + "\r\n", nil
			}
		},
	}
}

// handleChatCommand handles the /chat command and its subcommands
func (r *REPL) handleChatCommand(args []string) (string, error) {
	// Show help if no arguments provided
	if len(args) < 2 {
		var output strings.Builder
		output.WriteString("Chat conversation management commands:\r\n")
		output.WriteString("  /chat save [name] - Save conversation to a session file\r\n")
		output.WriteString("  /chat load <name> - Load conversation from a session file\r\n")
		output.WriteString("  /chat sessions    - List all saved sessions\r\n")
		output.WriteString("  /chat clear       - Clear conversation messages\r\n")
		output.WriteString("  /chat list        - Display conversation messages (truncated)\r\n")
		output.WriteString("  /chat log         - Display full conversation with preserved formatting\r\n")
		output.WriteString("  /chat undo [N]    - Remove last or Nth message\r\n")
		output.WriteString("  /chat compact     - Compact conversation into a single message\r\n")
		return output.String(), nil
	}

	// Handle subcommands
	action := args[1]
	switch action {
	case "save":
		var sessionName string
		if len(args) > 2 {
			sessionName = args[2]
		} else {
			sessionName = time.Now().Format("20060102150405")
		}
		return "", r.saveSession(sessionName)
	case "load":
		if len(args) < 3 {
			return "Usage: /chat load <name>\r\n", nil
		}
		return "", r.loadSession(args[2])
	case "sessions":
		output, err := r.listSessions()
		if err != nil {
			return "", err
		}
		return output, nil
	case "clear":
		r.messages = []llm.Message{}
		return "Conversation messages cleared\r\n", nil
	case "list":
		output := r.displayConversationLog()
		return output, nil
	case "log":
		output := r.displayFullConversationLog()
		return output, nil
	case "undo":
		if len(args) > 2 {
			// Parse the index argument
			r.undoMessageByIndex(args[2])
		} else {
			// Default behavior - remove the last message
			r.undoLastMessage()
		}
		return "", nil
	case "compact":
		return "", r.handleCompactCommand()
	case "memory":
		// Generate or manage consolidated memory file
		if len(args) < 3 || args[2] == "generate" {
			return "", r.generateMemory()
		}
		if args[2] == "show" {
			maiDir, err := findMaiDir()
			if err != nil {
				return fmt.Sprintf("Cannot find mai directory: %v\r\n", err), nil
			}
			memFile := filepath.Join(maiDir, "memory.txt")
			b, err := os.ReadFile(memFile)
			if err != nil {
				return fmt.Sprintf("Cannot read memory file: %v\r\n", err), nil
			}
			return fmt.Sprintf("%s\r\n", string(b)), nil
		}
		if args[2] == "clear" {
			maiDir, err := findMaiDir()
			if err != nil {
				return fmt.Sprintf("Cannot find mai directory: %v\r\n", err), nil
			}
			memFile := filepath.Join(maiDir, "memory.txt")
			_ = os.Remove(memFile)
			return "Memory file removed\r\n", nil
		}
		return "Usage: /chat memory [generate|show|clear]\r\n", nil
	default:
		return fmt.Sprintf("Unknown action: %s\r\nAvailable actions: save, load, sessions, clear, list, log, undo, compact\r\n", action), nil
	}
}
