package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (r *REPL) handleInput() error {
	input, err := r.readLine()
	fmt.Print("\x1b[0m") // Reset color after input
	if err != nil {
		return err
	}

	skipMessage := strings.HasPrefix(input, " ")

	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	// Handle verbatim inputs
	var isVerbatim bool
	if len(input) >= 2 {
		if input[0] == '\'' && input[len(input)-1] == '\'' {
			input = input[1 : len(input)-1]
			isVerbatim = true
		}
	}

	// Handle redirection if not verbatim and not shell ($) mode.
	// Shell mode ('$') has its own redirection parsing in handleShellInput,
	// so avoid stripping redirections here which would prevent shell-mode
	// handlers from seeing them.
	var redirectType, redirectTarget string
	if !isVerbatim && !strings.HasPrefix(input, "$") {
		if idx := strings.LastIndex(input, " > "); idx != -1 {
			redirectType = "file"
			redirectTarget = strings.TrimSpace(input[idx+3:])
			input = strings.TrimSpace(input[:idx])
		} else if idx := strings.LastIndex(input, " | "); idx != -1 {
			redirectType = "pipe"
			redirectTarget = strings.TrimSpace(input[idx+3:])
			input = strings.TrimSpace(input[:idx])
		}
	}

	// Handle commands (slash- and dot-prefixed, plus '_' for last reply)
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, ".") || input == "_" {
		// Add to history
		r.addToHistory(input)
		err = r.handleCommand(input, redirectType, redirectTarget)
	} else if strings.HasPrefix(input, "#") {
		// Add to history (also added in handlePromptCommand, but keep here for consistency)
		r.addToHistory(input)
		err = r.handlePromptCommand(input)
	} else if strings.HasPrefix(input, "%") {
		// Add to history
		r.addToHistory(input)
		err = r.handleTemplateCommand(input)
	} else if strings.HasPrefix(input, "?") {
		// Add to history (also added in handlePromptCommand, but keep here for consistency)
		r.addToHistory(input)
		err = r.handleCommand("/help", "", "")
	} else if strings.HasPrefix(input, "!") {
		// Add to history
		r.addToHistory(input)
		err = r.executeShellCommand(input[1:])
	} else if strings.HasPrefix(input, "$") {
		// Add to history
		r.addToHistory(input)
		err = r.handleShellInput(input[1:])
	} else {
		// Add to history
		r.addToHistory(input)
		err = r.sendToAI(input, redirectType, redirectTarget, true, false)
	}

	if skipMessage {
		r.handleCommand("/chat undo", "", "")
		r.handleCommand("/chat undo", "", "")
	}
	return err
}

func (r *REPL) readLine() (string, error) {
	// Ensure we have a readline instance
	if r.readline == nil {
		readLine, err := NewReadLine()
		if err != nil {
			return "", fmt.Errorf("failed to initialize readline: %v", err)
		}
		r.readline = readLine
	}

	// Set the interrupt function to handle Ctrl+C
	r.readline.SetInterruptFunc(r.interruptResponse)

	// Main input loop
	for {
		// Read the line of input
		input, err := r.readline.Read()
		if err != nil {
			return "", err
		}

		// Handle tab completion
		if input == "\t" {
			// Get current content from readline
			currentContent := r.readline.GetContent()

			// Update REPL's cursor position from readline's cursor position
			r.cursorPos = r.readline.GetCursorPos()

			// Set up a builder for tab completion
			var line strings.Builder
			line.WriteString(currentContent)

			// Handle tab completion
			r.handleTabCompletion(&line)

			// Get the updated content
			completedContent := line.String()

			// Only update if content changed
			if completedContent != currentContent {
				r.readline.SetContent(completedContent)
			}

			// If ui.bgline is set, move cursor up 1 line to avoid double printing
			if r.configOptions.Get("ui.bgline") != "" {
				fmt.Print("\x1b[1A")
			}
			continue
		}
		// Return the input
		return input, nil
	}
}

func (r *REPL) handleTabCompletion(line *strings.Builder) {
	// Capture original input and ensure lastTabInput is updated after completion
	origInput := line.String()
	defer func() {
		r.lastTabInput = line.String()
	}()
	input := origInput

	// Fresh vs cycling: reset if first tab or input changed since last tab press
	if r.completeState == 0 || origInput != r.lastTabInput {
		r.completeState = 0
		r.completeIdx = 0
		r.completeOptions = nil
		r.completePrefix = ""
	}

	// Check if input contains @ for file path completion
	if strings.Contains(input, "@") {
		// Find the position of @ in the input
		pos := strings.LastIndex(input, "@")

		// Get the prefix (text before @) and the partial path (text after @)
		prefix := input[:pos]
		partialPath := input[pos+1:]

		// Only attempt path completion if we're at or after the @ character
		if r.cursorPos >= pos {
			r.handleAtFilePathCompletion(line, prefix, partialPath)
			return
		}
	}

	// Check if we need to complete a file path for a command that accepts a file
	fileParts := strings.SplitN(input, " ", 2)
	if len(fileParts) == 2 && (fileParts[0] == "/image" || fileParts[0] == "/file" || fileParts[0] == "/template" || fileParts[0] == ".") {
		r.handleFilePathCompletion(line, fileParts[0], fileParts[1])
		return
	}

	// Check for /set dir.promptfile and dir.prompt value completion
	setParts := strings.SplitN(input, " ", 3)
	if len(setParts) >= 2 && setParts[0] == "/set" {
		if len(setParts) == 3 {
			switch setParts[1] {
			case "dir.promptfile":
				// Complete file paths for dir.promptfile
				r.handleFilePathCompletion(line, "/set dir.promptfile", setParts[2])
				return
			case "dir.prompt":
				// Complete directory paths for dir.prompt
				r.handleDirectoryCompletion(line, "/set dir.prompt", setParts[2])
				return
			case "repl.skillsdir":
				// Complete directory paths for repl.skillsdir
				r.handleDirectoryCompletion(line, "/set repl.skillsdir", setParts[2])
				return
			}
		}
		// Fallthrough for option completion
	}

	// Check for /set, /get, and /unset option completion
	configParts := strings.SplitN(input, " ", 2)
	if len(configParts) == 2 && (configParts[0] == "/set" || configParts[0] == "/get" || configParts[0] == "/unset") {
		r.handleOptionCompletion(line, configParts[0], configParts[1])
		return
	}

	// Handle tab completion for /chat subcommands
	chatParts := strings.SplitN(input, " ", 3)
	if strings.HasPrefix(input, "/chat ") && len(chatParts) >= 2 {
		if len(chatParts) == 2 {
			// Complete /chat subcommands
			subcmd := chatParts[1]
			r.handleChatSubcommandCompletion(line, subcmd)
			return
		} else if len(chatParts) == 3 && (chatParts[1] == "save" || chatParts[1] == "load") {
			// Complete file paths for save/load
			r.handleFilePathCompletion(line, "/chat "+chatParts[1], chatParts[2])
			return
		}
	}

	// Handle tab completion for /session subcommands
	sessionParts := strings.SplitN(input, " ", 3)
	if strings.HasPrefix(input, "/session ") && len(sessionParts) >= 2 {
		if len(sessionParts) == 2 {
			// Complete /session subcommands
			subcmd := sessionParts[1]
			r.handleSessionSubcommandCompletion(line, subcmd)
			return
		} else if len(sessionParts) == 3 && (sessionParts[1] == "use" || sessionParts[1] == "del" || sessionParts[1] == "show") {
			// Complete session names for use/del
			r.handleSessionNameCompletion(line, "/session "+sessionParts[1], sessionParts[2])
			return
		}
	}

	// Only handle tab completion at the beginning of the line for commands
	if !(strings.HasPrefix(input, "/") || strings.HasPrefix(input, "#") || strings.HasPrefix(input, "%")) {
		return
	}

	// Prompt command completion for commands like "#<tab>"
	if strings.HasPrefix(input, "#") {
		needFreshOptions := false
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		if needFreshOptions {
			// Determine prompt directory
			promptDir := r.configOptions.Get("dir.prompt")
			if promptDir == "" {
				for _, loc := range []string{"./share/mai/prompts", "../share/mai/prompts"} {
					if _, err := os.Stat(loc); err == nil {
						promptDir = loc
						break
					}
				}
				if promptDir == "" {
					return
				}
			}
			// Read prompt files
			files, err := os.ReadDir(promptDir)
			if err != nil {
				return
			}
			var allPrompts []string
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
					name := strings.TrimSuffix(f.Name(), ".md")
					allPrompts = append(allPrompts, "#"+name)
				}
			}
			sort.Strings(allPrompts)
			r.completePrefix = input
			r.completeOptions = nil
			for _, p := range allPrompts {
				if strings.HasPrefix(p, input) {
					r.completeOptions = append(r.completeOptions, p)
				}
			}
			if len(r.completeOptions) == 0 {
				return
			}
			r.completeState = 1
			r.completeIdx = 0
			first := r.completeOptions[0]
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(first)
			line.Reset()
			line.WriteString(first)
			r.cursorPos = line.Len()
		} else {
			if len(r.completeOptions) <= 1 {
				return
			}
			current := line.String()
			r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
			next := r.completeOptions[r.completeIdx]
			for i := 0; i < len(current); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(next)
			line.Reset()
			line.WriteString(next)
			r.cursorPos = line.Len()
		}
		return
	}

	// Template command completion for commands like "%<tab>"
	if strings.HasPrefix(input, "%") {
		needFreshOptions := false
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		if needFreshOptions {
			// Determine template directory
			templDir := r.configOptions.Get("dir.templates")
			if templDir == "" {
				for _, loc := range []string{"./share/mai/templates", "../share/mai/templates"} {
					if _, err := os.Stat(loc); err == nil {
						templDir = loc
						break
					}
				}
				if templDir == "" {
					return
				}
			}
			files, err := os.ReadDir(templDir)
			if err != nil {
				return
			}
			var allTemps []string
			for _, f := range files {
				if !f.IsDir() {
					base := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
					allTemps = append(allTemps, "%"+base)
				}
			}
			sort.Strings(allTemps)
			r.completePrefix = input
			r.completeOptions = nil
			for _, t := range allTemps {
				if strings.HasPrefix(t, input) {
					r.completeOptions = append(r.completeOptions, t)
				}
			}
			if len(r.completeOptions) == 0 {
				return
			}
			r.completeState = 1
			r.completeIdx = 0
			first := r.completeOptions[0]
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(first)
			line.Reset()
			line.WriteString(first)
			r.cursorPos = line.Len()
		} else {
			if len(r.completeOptions) <= 1 {
				return
			}
			current := line.String()
			r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
			next := r.completeOptions[r.completeIdx]
			for i := 0; i < len(current); i++ {
				fmt.Print("\b \b")
			}
			fmt.Print(next)
			line.Reset()
			line.WriteString(next)
			r.cursorPos = line.Len()
		}
		return
	}

	// Command completion for commands like "/no<tab>"
	if strings.HasPrefix(input, "/") {
		// Check if we need to generate fresh completion options
		needFreshOptions := false

		// In these cases we need to generate fresh options:
		// 1. First tab press
		// 2. No options available
		// 3. Back to original command prefix
		if r.completeState == 0 ||
			len(r.completeOptions) == 0 ||
			r.completePrefix == "" ||
			input == r.completePrefix {
			needFreshOptions = true
		}

		// Do we need to regenerate the completion options?
		if needFreshOptions {
			// Collect all commands from registry
			allCommands := []string{}
			for cmdName := range r.commands {
				allCommands = append(allCommands, cmdName)
			}

			// Sort alphabetically for consistent order
			sort.Strings(allCommands)

			// Store the original prefix for future reference
			r.completePrefix = input

			// Find all commands that match our prefix
			r.completeOptions = []string{}
			for _, cmd := range allCommands {
				if strings.HasPrefix(cmd, input) {
					r.completeOptions = append(r.completeOptions, cmd)
				}
			}

			// No matches found
			if len(r.completeOptions) == 0 {
				return
			}

			// Update completion state
			r.completeState = 1 // Entering tab cycle mode
			r.completeIdx = 0   // Start with first option

			// Show first match
			firstMatch := r.completeOptions[0]

			// Clear current input
			for i := 0; i < len(input); i++ {
				fmt.Print("\b \b")
			}

			// Show the match
			fmt.Print(firstMatch)
			line.Reset()
			line.WriteString(firstMatch)
			r.cursorPos = line.Len()
		} else {
			// We're cycling through options with subsequent tab presses
			// Make sure we have multiple options to cycle through
			if len(r.completeOptions) <= 1 {
				return
			}

			// Get current input text
			currentText := line.String()

			// Advance to next option
			r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
			nextOption := r.completeOptions[r.completeIdx]

			// Clear current line
			for i := 0; i < len(currentText); i++ {
				fmt.Print("\b \b")
			}

			// Show next option
			fmt.Print(nextOption)
			line.Reset()
			line.WriteString(nextOption)
			r.cursorPos = line.Len()
		}
		return // Command completion handled
	}
}

func (r *REPL) addToHistory(input string) {
	r.readline.AddToHistory(input)
}

func (r *REPL) handleAtFilePathCompletion(line *strings.Builder, prefix, partialPath string) {
	// Normalize backslashes to forward slashes for consistent path handling
	partialPath = strings.ReplaceAll(partialPath, "\\", "/")

	// Handle special case where partialPath is empty
	if partialPath == "" {
		partialPath = "."
	}

	// Expand ~ to home directory if present
	if strings.HasPrefix(partialPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			partialPath = filepath.Join(homeDir, partialPath[1:])
		}
	}

	// First tab press - generate options
	if r.completeState == 0 {
		// Get the directory and file prefix
		dir, filePrefix := filepath.Split(partialPath)

		// If no directory specified, use current directory
		if dir == "" {
			dir = "."
		} else if !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "./") && !strings.HasPrefix(dir, "../") {
			// Handle relative paths that don't start with ./ or ../
			dir = "." + string(filepath.Separator) + dir
		}

		// Make sure dir ends with separator for directory operations
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}

		// Read the directory
		files, err := os.ReadDir(dir)
		if err != nil {
			// Cannot read directory - just return without changing anything
			return
		}

		// Find matching files at current level only
		r.completeOptions = nil
		for _, file := range files {
			name := file.Name()
			// Only show files that match the prefix
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(filePrefix)) {
				pathToAdd := dir + name
				// Add separator if it's a directory
				if file.IsDir() {
					pathToAdd += string(filepath.Separator)
				}
				r.completeOptions = append(r.completeOptions, pathToAdd)
			}
		}

		// Sort options alphabetically for consistent behavior
		sort.Strings(r.completeOptions)

		// If no matches, do nothing
		if len(r.completeOptions) == 0 {
			return
		}

		// Set up completion state
		r.completeState = 1
		r.completePrefix = prefix + "@"
		r.completeIdx = 0 // Start with first option

		// Show first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + r.completeOptions[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Simple cycling through options
		r.completeIdx = (r.completeIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[r.completeIdx]

		// Clear current line
		currentInput := line.String()
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
		r.cursorPos = line.Len()
	}
}

func (r *REPL) handleChatSubcommandCompletion(line *strings.Builder, partialCmd string) {
	// Available chat subcommands
	subcommands := []string{"save", "load", "clear", "list", "log", "undo", "compact"}

	// Filter subcommands by the partial input
	var filteredCommands []string
	for _, cmd := range subcommands {
		if strings.HasPrefix(cmd, partialCmd) {
			filteredCommands = append(filteredCommands, cmd)
		}
	}

	// If no matches, return
	if len(filteredCommands) == 0 {
		return
	}

	// If this is the first tab press, set the state and show the first match
	if r.completeState == 0 {
		r.completeState = 1
		r.completeOptions = filteredCommands
		r.completePrefix = "/chat "

		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + filteredCommands[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option
		currentInput := line.String()
		currentCmd := strings.TrimPrefix(currentInput, r.completePrefix)

		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentCmd {
				currentIdx = i
				break
			}
		}

		// Get next option
		nextIdx := (currentIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[nextIdx]

		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

func (r *REPL) handleDirectoryCompletion(line *strings.Builder, cmd, partialPath string) {
	// Expand ~ to home directory if present
	if strings.HasPrefix(partialPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			partialPath = filepath.Join(homeDir, partialPath[1:])
		}
	}

	// If this is the first tab press, find matching directories
	if r.completeState == 0 {
		// Get the directory and file prefix
		dir, prefix := filepath.Split(partialPath)

		// If no directory specified, use current directory
		if dir == "" {
			dir = "."
		} else if !filepath.IsAbs(dir) && !strings.HasPrefix(partialPath, "./") && !strings.HasPrefix(partialPath, "../") {
			// Handle relative paths that don't start with ./ or ../
			dir = "." + string(filepath.Separator) + dir
		}

		// Make sure dir ends with separator
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}

		// Read the directory
		files, err := os.ReadDir(dir)
		if err != nil {
			return // Cannot read directory
		}

		// Find matching directories only
		r.completeOptions = nil
		for _, file := range files {
			name := file.Name()
			if strings.HasPrefix(name, prefix) && file.IsDir() {
				// Add separator for directories
				name += string(filepath.Separator)
				r.completeOptions = append(r.completeOptions, dir+name)
			}
		}

		// If no matches, do nothing
		if len(r.completeOptions) == 0 {
			return
		}

		r.completeState = 1
		r.completePrefix = cmd + " "

		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + r.completeOptions[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option
		currentInput := line.String()
		currentPath := strings.TrimPrefix(currentInput, r.completePrefix)

		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentPath {
				currentIdx = i
				break
			}
		}

		// Get next option
		nextIdx := (currentIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[nextIdx]

		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

func (r *REPL) handleFilePathCompletion(line *strings.Builder, cmd, partialPath string) {
	// Expand ~ to home directory if present
	if strings.HasPrefix(partialPath, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			partialPath = filepath.Join(homeDir, partialPath[1:])
		}
	}

	// If this is the first tab press, find matching files
	if r.completeState == 0 {
		// Get the directory and file prefix
		dir, prefix := filepath.Split(partialPath)

		// If no directory specified, use current directory
		if dir == "" {
			dir = "."
		} else if !filepath.IsAbs(dir) && !strings.HasPrefix(partialPath, "./") && !strings.HasPrefix(partialPath, "../") {
			// Handle relative paths that don't start with ./ or ../
			dir = "." + string(filepath.Separator) + dir
		}

		// Make sure dir ends with separator
		if !strings.HasSuffix(dir, string(filepath.Separator)) {
			dir += string(filepath.Separator)
		}

		// Read the directory
		files, err := os.ReadDir(dir)
		if err != nil {
			return // Cannot read directory
		}

		// Find matching files
		r.completeOptions = nil
		for _, file := range files {
			name := file.Name()
			if strings.HasPrefix(name, prefix) {
				// Add separator if it's a directory
				if file.IsDir() {
					name += string(filepath.Separator)
				}
				r.completeOptions = append(r.completeOptions, dir+name)
			}
		}

		// If no matches, do nothing
		if len(r.completeOptions) == 0 {
			return
		}

		r.completeState = 1
		r.completePrefix = cmd + " "

		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + r.completeOptions[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option
		currentInput := line.String()
		currentPath := strings.TrimPrefix(currentInput, r.completePrefix)

		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentPath {
				currentIdx = i
				break
			}
		}

		// Get next option
		nextIdx := (currentIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[nextIdx]

		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}

func (r *REPL) handleOptionCompletion(line *strings.Builder, cmd, partialOption string) {
	var options []string

	if cmd == "/set" || cmd == "/get" {
		// For /set and /get, show all available options
		options = r.configOptions.GetAvailableOptions()
	} else if cmd == "/unset" {
		// For /unset, show only options that are currently set
		options = r.configOptions.GetKeys()
	}

	// Filter options by the partial input
	var filteredOptions []string
	for _, opt := range options {
		if strings.HasPrefix(opt, partialOption) {
			filteredOptions = append(filteredOptions, opt)
		}
	}

	// If no matches, return
	if len(filteredOptions) == 0 {
		return
	}

	// If this is the first tab press, set the state and show the first match
	if r.completeState == 0 {
		r.completeState = 1
		r.completeOptions = filteredOptions
		r.completePrefix = cmd + " "

		// Replace current input with the first match
		currentInput := line.String()
		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Get the first match
		firstMatch := r.completePrefix + filteredOptions[0]

		// Print and set the first match
		fmt.Print(firstMatch)
		line.Reset()
		line.WriteString(firstMatch)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	} else {
		// Subsequent tab presses - cycle through options
		if len(r.completeOptions) <= 1 {
			return
		}

		// Find current option
		currentInput := line.String()
		currentOption := strings.TrimPrefix(currentInput, r.completePrefix)

		// Find current index
		currentIdx := -1
		for i, opt := range r.completeOptions {
			if opt == currentOption {
				currentIdx = i
				break
			}
		}

		// Get next option
		nextIdx := (currentIdx + 1) % len(r.completeOptions)
		nextOption := r.completePrefix + r.completeOptions[nextIdx]

		// Clear current line
		for i := 0; i < len(currentInput); i++ {
			fmt.Print("\b \b")
		}

		// Print next option
		fmt.Print(nextOption)
		line.Reset()
		line.WriteString(nextOption)
		// Update cursor position to end of line
		r.cursorPos = line.Len()
	}
}
