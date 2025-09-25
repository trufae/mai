package main

import (
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
// AITODO move into files.go
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

// AITODO move into files.go
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

// AITODO move into files.go
func (r *REPL) loadAgentsFile() error {
	fname := r.configOptions.Get("agentsfile")
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
	_ = r.configOptions.Set("systemprompt", combined)
	return nil
}
