package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadRCFile loads and processes commands from the 'rc' file in the project or home .mai directory
func (r *REPL) loadRCFile() error {
	// Load commands from the 'rc' file in the project or home .mai directory
	maiDir, err := findMaiDir()
	if err != nil {
		return err
	}
	rcFilePath := filepath.Join(maiDir, "rc")
	if _, err := os.Stat(rcFilePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("error checking rc file: %v", err)
	}
	content, err := os.ReadFile(rcFilePath)
	if err != nil {
		return fmt.Errorf("failed to read rc file: %v", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "/") {
			continue
		}
		if err := r.handleCommand(line); err != nil {
			fmt.Printf("Error in rc file %s: %v\r\n", rcFilePath, err)
		}
	}
	return nil
}

// findMaiMD is no longer used; system prompt file loading is handled dynamically

// findMaiDir searches for a .mai directory from the current directory up to root,
// and returns it, or falls back to $HOME/.mai if none found.
func findMaiDir() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %v", err)
	}
	for {
		candidate := filepath.Join(currentDir, ".mai")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %v", err)
	}
	return filepath.Join(homeDir, ".mai"), nil
}

func findFileUpwards(filename string) (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %v", err)
	}
	for {
		candidate := filepath.Join(currentDir, filename)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}
	return "", nil
}

func (r *REPL) loadAgentsFile() error {
	fname := r.configOptions.Get("llm.agentfile")
	if fname == "" {
		return nil
	}
	var path string
	if filepath.IsAbs(fname) || strings.ContainsAny(fname, "/\\") {
		if _, err := os.Stat(fname); err == nil {
			path = fname
		} else {
			return nil
		}
	} else {
		found, err := findFileUpwards(fname)
		if err != nil || found == "" {
			return nil
		}
		path = found
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading agents file %s: %v\n", path, err)
		return err
	}
	content := string(b)
	prefix := "These are the instructions to follow by the agent: "
	combined := prefix + content
	existing := r.currentSystemPrompt()
	if existing != "" {
		combined = combined + "\n\n" + existing
	}
	_ = r.configOptions.Set("llm.systemprompt", combined)
	return nil
}

func (r *REPL) saveHistory() error {
	if !r.configOptions.GetBool("repl.history") {
		return nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home directory: %v", err)
	}
	historyFile := filepath.Join(homeDir, ".mai", "history.json")

	// Overwrite history file with updated history
	history := r.readline.GetHistory()
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal history: %v", err)
	}
	return os.WriteFile(historyFile, data, 0644)
}

// loadReplHistory reads the history file and loads entries into readline's history
func (r *REPL) loadReplHistory() error {
	if !r.configOptions.GetBool("repl.history") {
		return nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home directory: %v", err)
	}
	historyFile := filepath.Join(homeDir, ".mai", "history.json")
	data, err := os.ReadFile(historyFile)
	if err != nil {
		// Nothing to load if file doesn't exist or cannot be read
		return nil
	}
	var history []string
	if err := json.Unmarshal(data, &history); err != nil {
		return fmt.Errorf("cannot unmarshal history: %v", err)
	}
	for _, entry := range history {
		r.readline.AddToHistory(entry)
	}
	return nil
}

func (r *REPL) setupHistory() error {
	if !r.configOptions.GetBool("repl.history") {
		return nil
	}
	// Determine the .mai directory for history/chat storage: search project dirs or fallback to home
	maiDir, err := findMaiDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(maiDir); os.IsNotExist(err) {
		if err := os.MkdirAll(maiDir, 0755); err != nil {
			return fmt.Errorf("cannot create %s: %v", maiDir, err)
		}
	}
	chatDir := filepath.Join(maiDir, "chat")
	if _, err := os.Stat(chatDir); os.IsNotExist(err) {
		if err := os.MkdirAll(chatDir, 0755); err != nil {
			return fmt.Errorf("cannot create %s: %v", chatDir, err)
		}
	}
	historyFile := filepath.Join(maiDir, "history.json")
	if _, err := os.Stat(historyFile); os.IsNotExist(err) {
		if _, err := os.Create(historyFile); err != nil {
			return fmt.Errorf("cannot create %s: %v", historyFile, err)
		}
	}
	return nil
}
