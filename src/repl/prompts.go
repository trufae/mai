package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (r *REPL) loadPrompt(promptName, extra string) (string, error) {
	// Check if it's an MCP prompt (contains '/')
	if strings.Contains(promptName, "/") {
		promptContent, err := GetMCPromptContent(promptName)
		if err != nil {
			return "", err
		}
		expandedInput := promptContent
		if extra != "" {
			expandedInput += "\n\n" + extra
		}
		return expandedInput, nil
	}

	promptPath, err := r.resolvePromptPath(promptName)
	if err == nil {
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

	if r.skillRegistry != nil {
		if skill, ok := r.skillRegistry.GetSkill(promptName); ok {
			expandedInput := skill.Body()
			if extra != "" {
				expandedInput += "\n\n" + extra
			}
			return expandedInput, nil
		}
	}
	return "", err
}

func addPromptNames(names map[string]struct{}, promptDir string) error {
	files, err := os.ReadDir(promptDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".md") {
			baseName := strings.TrimSuffix(file.Name(), ".md")
			names[baseName] = struct{}{}
		}
	}
	return nil
}

// listPrompts lists all .md files in the promptdir, skill-backed markdown, and MCP prompts.
func (r *REPL) listPrompts() ([]string, error) {
	names := make(map[string]struct{})

	promptDir := r.configOptions.Get("dir.prompt")
	if promptDir == "" {
		for _, loc := range []string{
			"./share/mai/prompts",
			"../share/mai/prompts",
		} {
			if _, err := os.Stat(loc); err == nil {
				promptDir = loc
				break
			}
		}
	}
	if promptDir != "" {
		if err := addPromptNames(names, promptDir); err != nil {
			return nil, fmt.Errorf("error reading prompt directory: %w", err)
		}
	}

	if r.skillRegistry != nil {
		for _, skill := range r.skillRegistry.ListSkills() {
			names[skill.Name] = struct{}{}
		}
	}

	mcpPromptsStr, err := GetAvailableMCPrompts(Quiet)
	if err == nil {
		lines := strings.Split(mcpPromptsStr, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && trimmed != "# Prompts Catalog" && strings.Contains(trimmed, "/") {
				names[trimmed] = struct{}{}
			}
		}
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("no prompt or skill markdown found")
	}

	var mdFiles []string
	for name := range names {
		mdFiles = append(mdFiles, name)
	}
	sort.Strings(mdFiles)
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
