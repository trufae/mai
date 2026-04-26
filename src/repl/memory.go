package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

const (
	memoryFileName           = "MEMORY.md"
	legacyMemoryFileName     = "memory.txt"
	memorySourceMaxChars     = 300000
	memoryMessageMaxChars    = 6000
	memoryRecreateTask       = "Recreate MEMORY.md from saved chat sessions. Use user messages and compact summaries as the source of truth."
	memoryUpdateTask         = "Update MEMORY.md using the existing memory and the new conversation data. Return the complete replacement MEMORY.md."
	memoryCompactRequestOne  = "Please summarize the conversation highlights and relevant annotations."
	memoryCompactRequestTwo  = "Please provide a compact response to my questions and needs."
	defaultMemoryPromptBrief = `You maintain a compact MEMORY.md file for an AI assistant.

Extract only durable information that is useful in future conversations: stable user preferences, dislikes, recurring goals, long-lived project context, decisions already made, communication style, and explicit constraints.

Do not include secrets, credentials, access tokens, private keys, or ephemeral one-off tasks. Do not invent facts. Prefer facts stated by the user over assistant guesses. Keep the file small: target 500-1000 words, use concise Markdown bullets, and avoid transcripts or source citations.

Return only the MEMORY.md contents.`
)

func memorySubcommands() []string {
	return []string{"status", "show", "path", "edit", "update", "recreate", "wipe", "enable", "disable", "help"}
}

func (r *REPL) handleMemoryCommand(args []string) (string, error) {
	if len(args) < 2 || args[1] == "help" {
		return r.memoryHelp(), nil
	}

	action := strings.ToLower(args[1])
	switch action {
	case "status":
		return r.memoryStatus()
	case "path":
		path, _, err := r.memoryPaths()
		if err != nil {
			return "", err
		}
		return path + "\r\n", nil
	case "show", "cat", "view":
		content, path, err := r.readMemoryFile()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			return fmt.Sprintf("No memory file found at %s\r\n", path), nil
		}
		return strings.ReplaceAll(strings.TrimRight(content, "\n"), "\n", "\r\n") + "\r\n", nil
	case "edit":
		path, err := r.ensureMemoryFile()
		if err != nil {
			return "", err
		}
		if err := runEditor(path); err != nil {
			return fmt.Sprintf("Failed to edit memory: %v\r\n", err), nil
		}
		return fmt.Sprintf("Memory edited: %s\r\n", path), nil
	case "update":
		extra := strings.TrimSpace(strings.Join(args[2:], " "))
		path, err := r.updateMemoryFromCurrent(extra)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Memory updated: %s\r\n", path), nil
	case "recreate", "regen", "generate", "create", "rebuild":
		path, err := r.recreateMemoryFromSessions()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Memory recreated: %s\r\n", path), nil
	case "wipe", "clear", "reset":
		return r.wipeMemory()
	case "enable", "on":
		_ = r.configOptions.Set("chat.memory", "true")
		return "Memory context enabled (chat.memory=true)\r\n", nil
	case "disable", "off":
		_ = r.configOptions.Set("chat.memory", "false")
		return "Memory context disabled (chat.memory=false)\r\n", nil
	default:
		return fmt.Sprintf("Unknown memory action: %s\r\n%s", action, r.memoryHelp()), nil
	}
}

func (r *REPL) memoryHelp() string {
	var output strings.Builder
	output.WriteString("Memory management commands:\r\n")
	output.WriteString("  /memory status       - Show memory path, size, and chat.memory state\r\n")
	output.WriteString("  /memory show         - Print MEMORY.md\r\n")
	output.WriteString("  /memory path         - Print the MEMORY.md path\r\n")
	output.WriteString("  /memory edit         - Open MEMORY.md in $EDITOR\r\n")
	output.WriteString("  /memory update [txt] - Update MEMORY.md from the current chat and optional note\r\n")
	output.WriteString("  /memory recreate     - Rebuild MEMORY.md from saved sessions\r\n")
	output.WriteString("  /memory wipe         - Remove MEMORY.md and legacy memory.txt\r\n")
	output.WriteString("  /memory enable       - Set chat.memory=true\r\n")
	output.WriteString("  /memory disable      - Set chat.memory=false\r\n")
	return output.String()
}

func (r *REPL) memoryPaths() (string, string, error) {
	maiDir, err := findMaiDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(maiDir, memoryFileName), filepath.Join(maiDir, legacyMemoryFileName), nil
}

func (r *REPL) ensureMemoryFile() (string, error) {
	path, legacyPath, err := r.memoryPaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if legacy, err := os.ReadFile(legacyPath); err == nil && strings.TrimSpace(string(legacy)) != "" {
		if err := os.WriteFile(path, legacy, 0644); err != nil {
			return "", err
		}
		return path, nil
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func (r *REPL) readMemoryFile() (string, string, error) {
	path, legacyPath, err := r.memoryPaths()
	if err != nil {
		return "", "", err
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data), path, nil
	} else if !os.IsNotExist(err) {
		return "", path, err
	}
	if data, err := os.ReadFile(legacyPath); err == nil {
		return string(data), legacyPath, nil
	} else if !os.IsNotExist(err) {
		return "", legacyPath, err
	}
	return "", path, nil
}

func (r *REPL) loadMemoryContext() (string, error) {
	content, _, err := r.readMemoryFile()
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil
	}
	return "<MEMORY>\n" + content + "\n</MEMORY>", nil
}

func (r *REPL) appendMemoryContext(messages []llm.Message) []llm.Message {
	if !r.configOptions.GetBool("chat.memory") {
		return messages
	}
	memory, err := r.loadMemoryContext()
	if err != nil || memory == "" {
		return messages
	}
	return append(messages, llm.Message{Role: "system", Content: memory})
}

func (r *REPL) memoryStatus() (string, error) {
	path, legacyPath, err := r.memoryPaths()
	if err != nil {
		return "", err
	}

	var output strings.Builder
	fmt.Fprintf(&output, "chat.memory = %s\r\n", r.configOptions.Get("chat.memory"))
	fmt.Fprintf(&output, "path        = %s\r\n", path)

	if info, err := os.Stat(path); err == nil {
		fmt.Fprintf(&output, "size        = %d bytes\r\n", info.Size())
		fmt.Fprintf(&output, "modified    = %s\r\n", info.ModTime().Format(time.RFC3339))
		return output.String(), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if info, err := os.Stat(legacyPath); err == nil {
		fmt.Fprintf(&output, "legacy      = %s (%d bytes)\r\n", legacyPath, info.Size())
		output.WriteString("status      = using legacy memory.txt until MEMORY.md is created\r\n")
		return output.String(), nil
	}

	output.WriteString("status      = no memory file\r\n")
	return output.String(), nil
}

func (r *REPL) wipeMemory() (string, error) {
	path, legacyPath, err := r.memoryPaths()
	if err != nil {
		return "", err
	}
	removed := false
	for _, candidate := range []string{path, legacyPath} {
		if err := os.Remove(candidate); err == nil {
			removed = true
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if !removed {
		return "No memory file to wipe\r\n", nil
	}
	return "Memory wiped\r\n", nil
}

func (r *REPL) updateMemoryFromCurrent(extra string) (string, error) {
	if len(r.messages) == 0 && extra == "" {
		return "", fmt.Errorf("no current conversation to update memory from")
	}

	existing, _, err := r.readMemoryFile()
	if err != nil {
		return "", err
	}

	var source strings.Builder
	source.WriteString("# Existing MEMORY.md\n\n")
	if strings.TrimSpace(existing) == "" {
		source.WriteString("(empty)\n\n")
	} else {
		source.WriteString(strings.TrimSpace(existing))
		source.WriteString("\n\n")
	}
	source.WriteString("# New Conversation\n\n")
	source.WriteString(r.serializeMessagesForMemory(r.messagesForLog(), true))
	if extra != "" {
		source.WriteString("\n# User Note\n\n")
		source.WriteString(extra)
		source.WriteString("\n")
	}

	return r.writeGeneratedMemory(memoryUpdateTask, source.String())
}

func (r *REPL) recreateMemoryFromSessions() (string, error) {
	source, count, err := r.savedSessionsMemorySource()
	if err != nil {
		return "", err
	}
	if count == 0 {
		return "", fmt.Errorf("no saved session messages found")
	}
	return r.writeGeneratedMemory(memoryRecreateTask, source)
}

func (r *REPL) writeGeneratedMemory(task, source string) (string, error) {
	prompt := r.memoryPrompt(task)
	client, err := llm.NewLLMClient(r.buildLLMConfigForTask("compact"), r.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %v", err)
	}

	messages := []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: source},
	}
	response, err := client.SendMessage(messages, false, nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate memory: %v", err)
	}

	memory := cleanMemoryResponse(response)
	if memory == "" {
		return "", fmt.Errorf("model returned empty memory")
	}

	path, _, err := r.memoryPaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(memory+"\n"), 0644); err != nil {
		return "", fmt.Errorf("cannot write memory file: %v", err)
	}
	return path, nil
}

func (r *REPL) memoryPrompt(task string) string {
	prompt := defaultMemoryPromptBrief
	if path, err := r.resolvePromptPath("memory"); err == nil {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			prompt = string(data)
		}
	}
	return strings.TrimSpace(prompt) + "\n\nTask: " + task
}

func (r *REPL) savedSessionsMemorySource() (string, int, error) {
	maiDir, err := findMaiDir()
	if err != nil {
		return "", 0, err
	}
	chatDir := filepath.Join(maiDir, "chats")
	files, err := os.ReadDir(chatDir)
	if err != nil {
		return "", 0, fmt.Errorf("cannot read chat directory: %v", err)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	var source strings.Builder
	source.WriteString("# Saved Session Memory Sources\n\n")
	count := 0
	truncated := false

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		sessionSource, sessionCount := r.sessionMemorySource(chatDir, strings.TrimSuffix(file.Name(), ".json"))
		if sessionCount == 0 {
			continue
		}
		if source.Len()+len(sessionSource) > memorySourceMaxChars {
			truncated = true
			break
		}
		source.WriteString(sessionSource)
		count += sessionCount
	}

	if truncated {
		source.WriteString("\n[Additional saved session content was omitted because the memory source was too large.]\n")
	}
	return source.String(), count, nil
}

func (r *REPL) sessionMemorySource(chatDir, sessionName string) (string, int) {
	data, err := os.ReadFile(filepath.Join(chatDir, sessionName+".json"))
	if err != nil {
		return "", 0
	}
	var sess sessionData
	if err := json.Unmarshal(data, &sess); err != nil {
		return "", 0
	}

	var session strings.Builder
	session.WriteString("## Session " + sessionName + "\n\n")
	if topic, err := os.ReadFile(filepath.Join(chatDir, sessionName+".topic")); err == nil {
		if t := strings.TrimSpace(string(topic)); t != "" && t != "-" {
			session.WriteString("Topic: " + t + "\n\n")
		}
	}

	count := 0
	for i, msg := range sess.Messages {
		msg = r.messageForLog(msg)
		content := trimMemorySourceText(msg.Content)
		if content == "" {
			continue
		}

		role := strings.ToLower(msg.Role)
		switch {
		case role == "user":
			if isMemoryCompactRequest(content) {
				continue
			}
			fmt.Fprintf(&session, "User: %s\n\n", content)
			count++
		case role == "assistant" && isMemoryCompactSummary(sess.Messages, i):
			fmt.Fprintf(&session, "Compact summary: %s\n\n", content)
			count++
		}
	}

	if count == 0 {
		return "", 0
	}
	session.WriteString("---\n\n")
	return session.String(), count
}

func (r *REPL) serializeMessagesForMemory(messages []llm.Message, userAndAssistant bool) string {
	var output strings.Builder
	for _, msg := range messages {
		msg = r.messageForLog(msg)
		content := trimMemorySourceText(msg.Content)
		if content == "" {
			continue
		}
		role := strings.ToLower(msg.Role)
		if userAndAssistant {
			if role != "user" && role != "assistant" {
				continue
			}
		} else if role != "user" {
			continue
		}
		fmt.Fprintf(&output, "%s: %s\n\n", formatRole(role), content)
	}
	if output.Len() == 0 {
		return "(no messages)\n"
	}
	return output.String()
}

func isMemoryCompactSummary(messages []llm.Message, index int) bool {
	if index <= 0 || index >= len(messages) {
		return false
	}
	prev := messages[index-1]
	return strings.EqualFold(prev.Role, "user") && isMemoryCompactRequest(prev.Content)
}

func isMemoryCompactRequest(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, memoryCompactRequestOne) || strings.HasPrefix(content, memoryCompactRequestTwo)
}

func trimMemorySourceText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(content) <= memoryMessageMaxChars {
		return content
	}
	return strings.TrimSpace(content[:memoryMessageMaxChars]) + "\n[truncated]"
}

func cleanMemoryResponse(response string) string {
	response = strings.TrimSpace(llm.FilterOutThinkForOutput(response))
	lines := strings.Split(response, "\n")
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		for i := len(lines) - 1; i > 0; i-- {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				response = strings.Join(lines[1:i], "\n")
				break
			}
		}
	}
	return strings.TrimSpace(response)
}

func runEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}

	parts := parseShellArgs(editor)
	if len(parts) == 0 {
		return fmt.Errorf("empty EDITOR")
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
