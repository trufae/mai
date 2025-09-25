package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (r *REPL) loadPrompt(promptName, extra string) (string, error) {
	promptPath, err := r.resolvePromptPath(promptName)
	if err != nil {
		return "", err
	}

	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		return "", err
	}

	expandedInput := string(promptContent)
	if extra != "" {
		expandedInput += "\n\n" + extra
	}

	return expandedInput, nil
}

// listPrompts lists all .md files in the promptdir
func (r *REPL) listPrompts() ([]string, error) {
	// Get the prompt directory from config
	promptDir := r.configOptions.Get("dir.prompt")
	if promptDir == "" {
		// Try common locations
		commonLocations := []string{
			"./prompts",
			"../prompts",
		}

		found := false
		for _, loc := range commonLocations {
			if _, err := os.Stat(loc); err == nil {
				promptDir = loc
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("No prompt directory found. Set one with /set promptdir <path>")
		}
	}

	// List all .md files in the directory
	files, err := os.ReadDir(promptDir)
	if err != nil {
		return nil, fmt.Errorf("Error reading prompt directory: %w", err)
	}

	// Filter for .md files and display
	mdFiles := []string{}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".md") {
			baseName := strings.TrimSuffix(file.Name(), ".md")
			mdFiles = append(mdFiles, baseName)
		}
	}

	if len(mdFiles) == 0 {
		return nil, fmt.Errorf("No prompt files (.md) found in %s", promptDir)
	}

	return mdFiles, nil
}

// currentSystemPrompt resolves the active system prompt from config options.
// Priority: explicit text (systemprompt) > promptfile > systempromptfile > default .mai/systemprompt.md
func (r *REPL) currentSystemPrompt() string {
	// 1. Inline system prompt text
	if sp := r.configOptions.Get("llm.systemprompt"); sp != "" {
		return sp
	}
	// 2. Prompt file path
	var path string
	if p := r.configOptions.Get("dir.promptfile"); p != "" {
		path = p
	} else if p := r.configOptions.Get("llm.systempromptfile"); p != "" {
		path = p
	} else {
		// 3. Default .mai/systemprompt.md
		if d, err := findMaiDir(); err == nil {
			candidate := filepath.Join(d, "systemprompt.md")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				_ = r.configOptions.Set("llm.systempromptfile", path)
			}
		}
	}
	if path == "" {
		return ""
	}
	if content, err := os.ReadFile(path); err == nil {
		text := string(content)
		text = r.processIncludeStatements(text, filepath.Dir(path))
		return text
	}
	return ""
}

// loadSystemPrompt loads a system prompt from a file and updates the config
func (r *REPL) loadSystemPrompt(path string) error {
	// Expand ~ to home directory if present
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %v", err)
		}
		path = filepath.Join(homeDir, path[1:])
	}

	// Update the promptfile configuration
	r.configOptions.Set("dir.promptfile", path)

	// Try to read to provide feedback, but don't cache the content
	if content, err := os.ReadFile(path); err == nil {
		fmt.Printf("System prompt loaded from %s (%d bytes)\r\n", path, len(content))
	} else {
		fmt.Printf("System prompt set to %s (failed to read: %v)\r\n", path, err)
	}
	return nil
}
