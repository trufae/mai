package main

import (
	"fmt"
	"os"
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
