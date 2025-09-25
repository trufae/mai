package main

import (
	"fmt"
	"os"
)

func (r *REPL) loadPrompt(promptName, extra string) error {
	promptPath, err := r.resolvePromptPath(promptName)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}

	promptContent, err := os.ReadFile(promptPath)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}

	expandedInput := string(promptContent)
	if extra != "" {
		expandedInput += "\n\n" + extra
	}

	return expandedInput
}

// listPrompts lists all .md files in the promptdir
// AITODO: return an array of strings with the available prompts. update all the callers accordingly
func (r *REPL) listPrompts() error {
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
			fmt.Print("No prompt directory found. Set one with /set promptdir <path>\r\n")
			return nil
		}
	}

	// List all .md files in the directory
	files, err := os.ReadDir(promptDir)
	if err != nil {
		fmt.Printf("Error reading prompt directory: %v\r\n", err)
		return nil
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
		fmt.Printf("No prompt files (.md) found in %s\r\n", promptDir)
		return nil
	}

	fmt.Printf("Available prompts (use # followed by name):\r\n")
	for _, file := range mdFiles {
		fmt.Printf("  %s\r\n", file)
	}

	return nil
}
