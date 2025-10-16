package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// findDirectory attempts to find a directory relative to the executable path
// and returns the path if found, or empty string if not found
func (r *REPL) findDirectory(dirName string) string {
	// Get the executable path
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}

	// Follow symlink if the executable is a symlink
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath // Fall back to the original path
	}

	// Get the directory containing the executable
	execDir := filepath.Dir(realPath)

	// Start searching from the executable directory and go up to root
	currentDir := execDir
	for {
		// Check if the target directory exists in the current directory
		targetDir := filepath.Join(currentDir, dirName)
		if _, err := os.Stat(targetDir); err == nil {
			// Found the target directory
			return targetDir
		}

		// Move up one directory
		parentDir := filepath.Dir(currentDir)

		// Stop if we've reached the root directory
		if parentDir == currentDir {
			break
		}

		// Continue with the parent directory
		currentDir = parentDir
	}

	return ""
}

// autoDetectDirectory attempts to find a directory relative to the executable path
// and sets the specified config option if found
func (r *REPL) autoDetectDirectory(configKey, dirName string, verbose bool) {
	// Skip if the config option is already set
	if r.configOptions.Get(configKey) != "" {
		return
	}

	// Use findDirectory to locate the directory
	if foundDir := r.findDirectory(dirName); foundDir != "" {
		r.configOptions.Set(configKey, foundDir)
	} else if verbose {
		fmt.Printf("Warning: Could not find directory '%s' relative to executable\r\n", dirName)
	}
}

// handleTemplateCommand handles the % command for template expansion
func (r *REPL) handleTemplateCommand(input string) error {
	// Split the input into command and arguments
	parts := strings.SplitN(input, " ", 2)
	templateName := parts[0][1:] // Remove the % prefix

	// If no template name is provided, list all template files from templatedir
	if templateName == "" {
		return r.listTemplates()
	}

	// Load the template file content
	templatePath, err := r.resolveTemplatePath(templateName)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}

	// Read the template file content
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		fmt.Printf("Error: %v\r\n", err)
		return nil
	}

	// Process the template content with user input for placeholders
	processedContent, err := r.processTemplate(string(templateContent))
	if err != nil {
		fmt.Printf("Error processing template: %v\r\n", err)
		return nil
	}

	// Append any additional user input if provided
	expandedInput := processedContent
	if len(parts) > 1 && parts[1] != "" {
		expandedInput += "\n\n" + parts[1]
	}

	// Send processed content to AI
	return r.sendToAI(expandedInput, "", "", true, false)
}

// processTemplate processes template text by:
// 1. Finding bracketed placeholders [text] and prompting the user to fill them in
// 2. Executing command substitutions $(command) and replacing with command output
// 3. Substituting environment variables ${VAR_NAME} with their values
func (r *REPL) processTemplate(templateText string) (string, error) {
	// First, process command substitutions
	processed, err := ExecuteCommandSubstitution(templateText)
	if err != nil {
		return "", fmt.Errorf("command substitution failed: %v", err)
	}

	// Process environment variable substitutions
	processed, err = ExecuteEnvVarSubstitution(processed)
	if err != nil {
		return "", fmt.Errorf("environment variable substitution failed: %v", err)
	}

	// Regular expression to find text inside brackets [...]
	re := regexp.MustCompile(`\[(.*?)\]`)
	result := processed

	// Find all matches for bracketed placeholders
	matches := re.FindAllStringSubmatch(processed, -1)

	// Track processed placeholders to avoid duplicate prompts
	processedPlaceholders := make(map[string]string)

	// Process each match
	for _, match := range matches {
		placeholder := match[0] // The full placeholder with brackets [text]
		question := match[1]    // Just the text inside brackets

		// Check if we've already processed this placeholder
		if response, exists := processedPlaceholders[placeholder]; exists {
			// Replace all occurrences of this placeholder with the same response
			result = strings.ReplaceAll(result, placeholder, response)
			continue
		}

		// Prompt the user with the text from inside the brackets
		prompt := r.configOptions.Get("repl.prompt")
		if prompt == "" {
			prompt = ">>>"
		}
		fmt.Printf("%s\n\r%s ", question, prompt)

		p := r.readline.defaultPrompt
		r.readline.defaultPrompt = "?"
		// Read user response
		response, err := r.readline.Read()
		r.readline.defaultPrompt = p
		fmt.Print("\033[0m")
		if err != nil {
			return "", fmt.Errorf("error reading input: %v", err)
		}

		// Store the response for this placeholder
		processedPlaceholders[placeholder] = response

		// Replace the placeholder with the user's response
		result = strings.ReplaceAll(result, placeholder, response)
	}

	return result, nil
}

// listTemplates lists all files in the templatedir
func (r *REPL) listTemplates() error {
	// Get the template directory from config
	templateDir := r.configOptions.Get("dir.templates")
	if templateDir == "" {
		// Try to find templates directory relative to executable
		templateDir = r.findDirectory("templates")
		if templateDir == "" {
			fmt.Print("No template directory found. Set one with /set templatedir <path>\r\n")
			return nil
		}
	}

	// List all files in the directory
	files, err := os.ReadDir(templateDir)
	if err != nil {
		fmt.Printf("Error reading template directory: %v\r\n", err)
		return nil
	}

	// Filter for template files and display
	templateFiles := []string{}
	for _, file := range files {
		if !file.IsDir() {
			// Get the base name without extension
			baseName := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
			templateFiles = append(templateFiles, baseName)
		}
	}

	if len(templateFiles) == 0 {
		fmt.Printf("No template files found in %s\r\n", templateDir)
		return nil
	}

	fmt.Printf("Available templates (use %% followed by name):\r\n")
	for _, file := range templateFiles {
		fmt.Printf("  %s\r\n", file)
	}

	return nil
}

// resolveTemplatePath resolves the path to a template file
// It checks in the templatedir configuration if set, otherwise it tries common locations
func (r *REPL) resolveTemplatePath(templateName string) (string, error) {
	// If the template path is absolute or contains path separators, use it directly
	if filepath.IsAbs(templateName) || strings.ContainsAny(templateName, "/\\") {
		return templateName, nil
	}

	// First try the templatedir configuration if set
	if templateDir := r.configOptions.Get("dir.templates"); templateDir != "" {
		templatePath := filepath.Join(templateDir, templateName)

		// Try with file as is
		if _, err := os.Stat(templatePath); err == nil {
			return templatePath, nil
		}

		// Try with common extensions
		commonExtensions := []string{".md", ".txt", ".template"}
		for _, ext := range commonExtensions {
			if _, err := os.Stat(templatePath + ext); err == nil {
				return templatePath + ext, nil
			}
		}
	}

	// Next, try to find templates directory relative to executable
	if templateDir := r.findDirectory("templates"); templateDir != "" {
		templatePath := filepath.Join(templateDir, templateName)

		// Try with file as is
		if _, err := os.Stat(templatePath); err == nil {
			return templatePath, nil
		}

		// Try with common extensions
		commonExtensions := []string{".md", ".txt", ".template"}
		for _, ext := range commonExtensions {
			if _, err := os.Stat(templatePath + ext); err == nil {
				return templatePath + ext, nil
			}
		}
	}

	return "", fmt.Errorf("template not found: %s", templateName)
}

// autoDetectTemplateDir attempts to find a templates directory relative to the executable path
// and sets the templatedir config variable if found
func (r *REPL) autoDetectTemplateDir() {
	r.autoDetectDirectory("dir.templates", "templates", false)
}

// autoDetectWwwRoot attempts to find a www directory relative to the executable path
// and sets the wwwroot config variable if found
func (r *REPL) autoDetectWwwRoot() {
	r.autoDetectDirectory("http.wwwroot", filepath.Join("doc", "www"), false)
}
