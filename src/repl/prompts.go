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
	promptDir := r.configOptions.Get("promptdir")
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
	if sp := r.configOptions.Get("systemprompt"); sp != "" {
		return sp
	}
	// 2. Prompt file path
	var path string
	if p := r.configOptions.Get("promptfile"); p != "" {
		path = p
	} else if p := r.configOptions.Get("systempromptfile"); p != "" {
		path = p
	} else {
		// 3. Default .mai/systemprompt.md
		if d, err := findMaiDir(); err == nil {
			candidate := filepath.Join(d, "systemprompt.md")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				_ = r.configOptions.Set("systempromptfile", path)
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
