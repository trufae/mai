package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

// registerChatCommands registers conversation management commands
func registerChatCommands(r *REPL) {
	// Conversation management commands
	r.commands["/chat"] = Command{
		Name:        "/chat",
		Description: "Manage conversation (save, load, clear, list, log, undo, compact, bgcompact)",
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
	r.commands["/memory"] = Command{
		Name:        "/memory",
		Description: "Manage MEMORY.md (status, show, edit, update, recreate, wipe)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleMemoryCommand(args)
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
		output.WriteString("  /chat compact [text] - Compact conversation; optional text is appended to the compact prompt\r\n")
		output.WriteString("  /chat bgcompact [text] - Compact conversation in the background\r\n")
		output.WriteString("  /memory ...       - Manage long-term MEMORY.md\r\n")
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
		extra := ""
		if len(args) > 2 {
			extra = strings.Join(args[2:], " ")
		}
		return "", r.handleCompactCommand(extra)
	case "bgcompact":
		extra := ""
		if len(args) > 2 {
			extra = strings.Join(args[2:], " ")
		}
		return r.startBackgroundCompact(extra)
	case "memory":
		memoryArgs := append([]string{"/memory"}, args[2:]...)
		return r.handleMemoryCommand(memoryArgs)
	default:
		return fmt.Sprintf("Unknown action: %s\r\nAvailable actions: save, load, sessions, clear, list, log, undo, compact, bgcompact, memory\r\n", action), nil
	}
}

func (r *REPL) startBackgroundCompact(extra string) (string, error) {
	r.mu.Lock()
	if r.bgCompactInProgress {
		r.mu.Unlock()
		return "Background compact already running\r\n", nil
	}
	r.bgCompactInProgress = true
	r.mu.Unlock()

	r.requestMu.Lock()
	rawSnapshot := append([]llm.Message(nil), r.messages...)
	logSnapshot := r.messagesForLog()
	r.requestMu.Unlock()

	if len(logSnapshot) < 2 {
		r.mu.Lock()
		r.bgCompactInProgress = false
		r.mu.Unlock()
		return "Not enough messages to compact. Need at least one exchange.\r\n", nil
	}

	go r.runBackgroundCompact(rawSnapshot, logSnapshot, extra)
	return "Background compact started\r\n", nil
}

func (r *REPL) runBackgroundCompact(rawSnapshot, logSnapshot []llm.Message, extra string) {
	defer func() {
		r.mu.Lock()
		r.bgCompactInProgress = false
		r.mu.Unlock()
	}()

	compacted, err := r.compactMessages(context.Background(), logSnapshot, extra)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\r\nBackground compact failed: %v\r\n", err)
		return
	}

	r.requestMu.Lock()
	defer r.requestMu.Unlock()

	if !messagesHavePrefix(r.messages, rawSnapshot) {
		fmt.Fprintf(os.Stderr, "\r\nBackground compact skipped: conversation changed before merge\r\n")
		return
	}

	suffix := append([]llm.Message(nil), r.messages[len(rawSnapshot):]...)
	r.messages = append(append([]llm.Message(nil), compacted...), suffix...)
	fmt.Fprintf(os.Stderr, "\r\nBackground compact completed\r\n")
}

func messagesHavePrefix(messages, prefix []llm.Message) bool {
	if len(messages) < len(prefix) {
		return false
	}
	return reflect.DeepEqual(messages[:len(prefix)], prefix)
}
