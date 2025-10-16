package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

// sessionData holds messages plus session-specific settings saved to disk.
type sessionData struct {
	Messages []llm.Message `json:"messages"`
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	BaseURL  string        `json:"baseurl"`
}

// handleSessionCommand handles the /session command and its subcommands.
func (r *REPL) handleSessionCommand(args []string) (string, error) {
	if len(args) < 2 {
		var output strings.Builder
		output.WriteString("Session management commands:\r\n")
		output.WriteString("  /session new      - Start a new session (save current if non-empty)\r\n")
		output.WriteString("  /session list     - List all saved sessions\r\n")
		output.WriteString("  /session show <name> - Display full conversation with preserved formatting for the given session\r\n")
		output.WriteString("  /session use <name> - Switch to the given session\r\n")
		output.WriteString("  /session del <name> - Delete the given session\r\n")
		output.WriteString("  /session purge    - Delete all saved sessions\r\n")
		output.WriteString("  /session topic [t] - Show or set session topic\r\n")
		output.WriteString("  /session aitopic  - Generate AI session topic and set unsaved topic\r\n")
		return output.String(), nil
	}

	action := args[1]
	switch action {
	case "new":
		if len(r.messages) == 0 {
			return "", nil
		}

		name := r.currentSession
		if name == "" {
			name = time.Now().Format("20060102150405")
		}
		if err := r.saveSession(name); err != nil {
			return "", err
		}
		r.messages = []llm.Message{}
		r.currentSession = name
		r.unsavedTopic = ""
		return fmt.Sprintf("Started new session '%s'\r\n", name), nil
	case "list":
		output, err := r.listSessions()
		if err != nil {
			return "", err
		}
		return output, nil
	case "show":
		if len(args) < 3 {
			return "Usage: /session show <session-name>\r\n", nil
		}
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Sprintf("Cannot get home directory: %v\r\n", err), nil
		}
		sessionFile := filepath.Join(homeDir, ".mai", "chat", args[2]+".json")
		data, err := os.ReadFile(sessionFile)
		if err != nil {
			return fmt.Sprintf("Cannot read session file: %v\r\n", err), nil
		}
		var sess sessionData
		if err := json.Unmarshal(data, &sess); err != nil {
			return fmt.Sprintf("Cannot parse session data: %v\r\n", err), nil
		}
		origMsgs := r.messages
		r.messages = sess.Messages
		output := r.displayFullConversationLog()
		r.messages = origMsgs
		return output, nil
	case "use":
		if len(args) < 3 {
			return "Usage: /session use <session-name>\r\n", nil
		}
		if err := r.loadSession(args[2]); err != nil {
			return "", err
		}
		r.currentSession = args[2]
		r.unsavedTopic = ""
		return "", nil
	case "del":
		if len(args) < 3 {
			return "Usage: /session del <session-name>\r\n", nil
		}
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot get home directory: %v\r\n", err)
		}
		chatDir := filepath.Join(homeDir, ".mai", "chat")
		sessionFile := filepath.Join(chatDir, args[2]+".json")
		topicFile := filepath.Join(chatDir, args[2]+".topic")
		if err := os.Remove(sessionFile); err != nil {
			return fmt.Sprintf("Error deleting session: %v\r\n", err), nil
		}
		_ = os.Remove(topicFile)
		return fmt.Sprintf("Deleted session '%s'\r\n", args[2]), nil
	case "topic":
		if len(args) > 2 {
			topic := strings.Join(args[2:], " ")
			if r.currentSession == "" {
				r.unsavedTopic = topic
			} else {
				r.setSessionTopic(r.currentSession, topic)
			}
			return "", nil
		} else {
			if r.currentSession == "" {
				return fmt.Sprintf("Current session topic: %s\r\n", r.unsavedTopic), nil
			} else {
				return fmt.Sprintf("Current session topic: %s\r\n", r.getSessionTopic(r.currentSession)), nil
			}
		}
	case "purge":
		return "", r.purgeSessions()
	case "aitopic":
		topic, err := r.generateAndSetTopic()
		if err != nil {
			return fmt.Sprintf("Error generating AI topic: %v\r\n", err), nil
		} else {
			return fmt.Sprintf("AI session topic: %s\r\n", topic), nil
		}
	default:
		return fmt.Sprintf("Unknown session action: %s\r\n", action), nil
	}
	return "", nil
}

func (r *REPL) getSessionTopic(sessionName string) string {
	if sessionName == "" {
		return ""
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	topicFile := filepath.Join(homeDir, ".mai", "chat", sessionName+".topic")
	if _, err := os.Stat(topicFile); os.IsNotExist(err) {
		return ""
	}
	content, err := os.ReadFile(topicFile)
	if err != nil {
		return ""
	}
	return string(content)
}

func (r *REPL) setSessionTopic(sessionName string, topic string) {
	if sessionName == "" {
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	topicFile := filepath.Join(homeDir, ".mai", "chat", sessionName+".topic")
	if err := os.WriteFile(topicFile, []byte(topic), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing topic file: %v\n", err)
	}
}

// handleSessionSubcommandCompletion handles tab completion for /session subcommands.
func (r *REPL) handleSessionSubcommandCompletion(line *strings.Builder, subcmd string) {
	subcommands := []string{"new", "list", "show", "use", "del", "purge", "topic", "aitopic"}
	sort.Strings(subcommands)

	if r.completeState == 0 || len(r.completeOptions) == 0 || r.completePrefix != "/session " {
		r.completePrefix = "/session "
		r.completeOptions = nil
		for _, sc := range subcommands {
			if strings.HasPrefix(sc, subcmd) {
				r.completeOptions = append(r.completeOptions, r.completePrefix+sc)
			}
		}
		if len(r.completeOptions) == 0 {
			return
		}
		r.completeState = 1
		r.completeIdx = 0
	}

	if len(r.completeOptions) > 0 {
		current := line.String()
		next := r.completeOptions[r.completeIdx]
		for i := 0; i < len(current); i++ {
			fmt.Print("\b \b")
		}
		fmt.Print(next)
		line.Reset()
		line.WriteString(next)
		r.cursorPos = line.Len()
		r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
	}
}

// handleSessionNameCompletion handles tab completion for session names.
func (r *REPL) handleSessionNameCompletion(line *strings.Builder, command, partialName string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	chatDir := filepath.Join(homeDir, ".mai", "chat")

	if r.completeState == 0 || !strings.HasPrefix(line.String(), r.completePrefix) {
		files, err := os.ReadDir(chatDir)
		if err != nil {
			return
		}

		r.completeOptions = nil
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
				sessionName := strings.TrimSuffix(file.Name(), ".json")
				if strings.HasPrefix(sessionName, partialName) {
					r.completeOptions = append(r.completeOptions, command+" "+sessionName)
				}
			}
		}
		if len(r.completeOptions) == 0 {
			return
		}
		sort.Strings(r.completeOptions)
		r.completeState = 1
		r.completeIdx = 0
		r.completePrefix = command + " " + partialName
	}

	if len(r.completeOptions) > 0 {
		current := line.String()
		next := r.completeOptions[r.completeIdx]
		for i := 0; i < len(current); i++ {
			fmt.Print("\b \b")
		}
		fmt.Print(next)
		line.Reset()
		line.WriteString(next)
		r.cursorPos = line.Len()
		r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
	}
}

func (r *REPL) saveSession(sessionName string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home directory: %v", err)
	}
	sessionFile := filepath.Join(homeDir, ".mai", "chat", sessionName+".json")
	topicFile := filepath.Join(homeDir, ".mai", "chat", sessionName+".topic")

	sess := sessionData{
		Messages: r.messages,
		Provider: r.configOptions.Get("ai.provider"),
		Model:    r.configOptions.Get("ai.model"),
		BaseURL:  r.configOptions.Get("ai.baseurl"),
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal session: %v", err)
	}
	if err := os.WriteFile(sessionFile, data, 0644); err != nil {
		return fmt.Errorf("cannot write session file: %v", err)
	}

	if r.configOptions.GetBool("chat.aitopic") {
		full, err := r.generateAndSetTopic()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating topic: %v\n", err)
		} else {
			fmt.Println(full)
			if err := os.WriteFile(topicFile, []byte(r.unsavedTopic), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing topic file: %v\n", err)
			}
		}
	} else if r.unsavedTopic != "" {
		if err := os.WriteFile(topicFile, []byte(r.unsavedTopic), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing topic file: %v\n", err)
		}
	}

	r.unsavedTopic = ""
	fmt.Printf("Session saved to %s\n\r", sessionFile)
	return nil
}

func (r *REPL) loadSession(sessionName string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home directory: %v", err)
	}
	sessionFile := filepath.Join(homeDir, ".mai", "chat", sessionName+".json")

	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("cannot read session file: %v", err)
	}

	var sess sessionData
	if err := json.Unmarshal(data, &sess); err != nil {
		return fmt.Errorf("cannot unmarshal session: %v", err)
	}
	r.messages = sess.Messages
	r.configOptions.Set("ai.provider", sess.Provider)
	r.configOptions.Set("ai.model", sess.Model)
	r.configOptions.Set("ai.baseurl", sess.BaseURL)
	fmt.Printf("Session '%s' loaded (provider=%s, model=%s, baseurl=%s)\r\n", sessionName, sess.Provider, sess.Model, sess.BaseURL)
	return nil
}

func (r *REPL) listSessions() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get home directory: %v", err)
	}
	chatDir := filepath.Join(homeDir, ".mai", "chat")

	files, err := os.ReadDir(chatDir)
	if err != nil {
		return "", fmt.Errorf("cannot read chat directory: %v", err)
	}

	type sessionEntry struct {
		name  string
		info  os.FileInfo
		topic []byte
		time  time.Time
	}

	var sessions []sessionEntry
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		sessionName := strings.TrimSuffix(file.Name(), ".json")
		topicFile := filepath.Join(chatDir, sessionName+".topic")
		topic, err := os.ReadFile(topicFile)
		if err != nil {
			topic = []byte("-")
		}
		parsedTime, err := time.Parse("05041502012006", sessionName)
		if err != nil {
			parsedTime = time.Time{}
		}
		sessions = append(sessions, sessionEntry{name: sessionName, info: info, topic: topic, time: parsedTime})
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].time.Equal(sessions[j].time) {
			return sessions[i].name < sessions[j].name
		}
		return sessions[i].time.Before(sessions[j].time)
	})

	var output strings.Builder
	output.WriteString("Available sessions:\r\n")
	for _, s := range sessions {
		output.WriteString(fmt.Sprintf("  %s (%d bytes) - %s\r\n", s.name, s.info.Size(), string(s.topic)))
	}
	return output.String(), nil
}

func (r *REPL) purgeSessions() error {
	fmt.Print("Are you sure you want to delete all saved sessions? (y/N) ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(input)) != "y" {
		fmt.Print("Session purge cancelled.\n\r")
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home directory: %v", err)
	}
	chatDir := filepath.Join(homeDir, ".mai", "chat")

	files, err := os.ReadDir(chatDir)
	if err != nil {
		return fmt.Errorf("cannot read chat directory: %v", err)
	}

	for _, file := range files {
		if !file.IsDir() {
			os.Remove(filepath.Join(chatDir, file.Name()))
		}
	}

	fmt.Print("All saved sessions have been deleted.\n\r")
	return nil
}

func (r *REPL) generateTopic() (string, error) {
	if len(r.messages) == 0 {
		return "", fmt.Errorf("no messages in conversation")
	}
	lastMessage := r.messages[len(r.messages)-1].Content.(string)
	prompt := "Summarize the following text in a few words:\n\n" + lastMessage

	client, err := llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %v", err)
	}

	messages := []llm.Message{{Role: "user", Content: prompt}}
	response, err := client.SendMessage(messages, false, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate topic: %v", err)
	}
	return strings.TrimSpace(response), nil
}

func (r *REPL) generateAndSetTopic() (string, error) {
	full, err := r.generateTopic()
	if err != nil {
		return "", err
	}

	first := full
	if idx := strings.IndexByte(full, '\n'); idx != -1 {
		first = full[:idx]
	}
	r.unsavedTopic = first
	return full, nil
}
