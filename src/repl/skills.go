package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill represents a Claude Skill with its metadata and content
type Skill struct {
	Name        string
	Description string
	Path        string // Full path to the skill directory
	Metadata    map[string]string
	Content     string // Full content of SKILL.md
}

// SkillRegistry manages all available skills
type SkillRegistry struct {
	skills map[string]*Skill
}

// NewSkillRegistry creates a new skill registry
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{
		skills: make(map[string]*Skill),
	}
}

// LoadSkills discovers and loads all skills from the specified skills directory
func (sr *SkillRegistry) LoadSkills(skillsDir string) error {
	// Use default directory if none specified
	if skillsDir == "" {
		var err error
		skillsDir, err = getDefaultSkillsDir()
		if err != nil {
			return fmt.Errorf("failed to find default skills directory: %v", err)
		}
	}

	// Check if directory exists
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		// Create the directory if it doesn't exist
		if err := os.MkdirAll(skillsDir, 0755); err != nil {
			return fmt.Errorf("failed to create skills directory: %v", err)
		}
		return nil // No skills to load yet
	}

	// Read all subdirectories in skills directory
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("failed to read skills directory: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(skillsDir, entry.Name())
		skill, err := sr.loadSkill(skillPath)
		if err != nil {
			// Log error but continue loading other skills
			fmt.Fprintf(os.Stderr, "Warning: failed to load skill %s: %v\n", entry.Name(), err)
			continue
		}

		if skill != nil {
			sr.skills[skill.Name] = skill
		}
	}

	return nil
}

// loadSkill loads a single skill from its directory
func (sr *SkillRegistry) loadSkill(skillPath string) (*Skill, error) {
	skillMdPath := filepath.Join(skillPath, "SKILL.md")
	if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("SKILL.md not found in %s", skillPath)
	}

	// Read the SKILL.md file
	content, err := os.ReadFile(skillMdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SKILL.md: %v", err)
	}

	// Parse YAML frontmatter
	skill, err := sr.parseSkillFile(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse SKILL.md: %v", err)
	}

	skill.Path = skillPath
	skill.Content = string(content)

	return skill, nil
}

// parseSkillFile parses the YAML frontmatter and content from a SKILL.md file
func (sr *SkillRegistry) parseSkillFile(content string) (*Skill, error) {
	// Split content into frontmatter and body
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid SKILL.md format: missing YAML frontmatter")
	}

	frontmatter := parts[1]

	// Parse YAML frontmatter (handles key: value format with basic list support)
	metadata := make(map[string]string)
	lines := strings.Split(frontmatter, "\n")
	var currentKey string
	var currentValue strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check if this is a new key-value pair
		if colonIdx := strings.Index(trimmed, ":"); colonIdx > 0 {
			// Save previous key if exists
			if currentKey != "" {
				metadata[currentKey] = strings.TrimSpace(currentValue.String())
			}

			// Extract new key
			currentKey = strings.TrimSpace(trimmed[:colonIdx])
			valueStr := strings.TrimSpace(trimmed[colonIdx+1:])

			// Handle inline lists [item1, item2]
			if strings.HasPrefix(valueStr, "[") && strings.HasSuffix(valueStr, "]") {
				// Keep as-is for now, will parse as comma-separated list
				metadata[currentKey] = valueStr[1 : len(valueStr)-1]
				currentKey = ""
				continue
			}

			// Remove quotes if present
			if len(valueStr) >= 2 && ((valueStr[0] == '"' && valueStr[len(valueStr)-1] == '"') ||
				(valueStr[0] == '\'' && valueStr[len(valueStr)-1] == '\'')) {
				valueStr = valueStr[1 : len(valueStr)-1]
			}
			currentValue.Reset()
			currentValue.WriteString(valueStr)
		}
	}

	// Save last key
	if currentKey != "" {
		metadata[currentKey] = strings.TrimSpace(currentValue.String())
	}

	skill := &Skill{
		Metadata: metadata,
	}

	// Validate and extract required fields
	var ok bool
	if skill.Name, ok = metadata["name"]; !ok || skill.Name == "" {
		return nil, fmt.Errorf("missing or empty 'name' field in frontmatter")
	}

	// Validate name: lowercase, alphanumeric + hyphens only
	if !isValidSkillName(skill.Name) {
		return nil, fmt.Errorf("invalid skill name '%s': must contain only lowercase letters, numbers, and hyphens", skill.Name)
	}

	if skill.Description, ok = metadata["description"]; !ok || skill.Description == "" {
		return nil, fmt.Errorf("missing or empty 'description' field in frontmatter")
	}

	if len(skill.Description) > 1024 {
		return nil, fmt.Errorf("description too long (max 1024 chars)")
	}

	return skill, nil
}

// isValidSkillName validates skill name format
func isValidSkillName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			return false
		}
	}
	return true
}

// GetSkill returns a skill by name
func (sr *SkillRegistry) GetSkill(name string) (*Skill, bool) {
	skill, exists := sr.skills[name]
	return skill, exists
}

// ListSkills returns all available skills sorted by name
func (sr *SkillRegistry) ListSkills() []*Skill {
	var skills []*Skill
	for _, skill := range sr.skills {
		skills = append(skills, skill)
	}

	// Sort by name
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return skills
}

// getDefaultSkillsDir returns the default skills directory path
func getDefaultSkillsDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "mai", "skills"), nil
}

// getSkillsDirForConfig returns the path to the skills directory for given config options
func getSkillsDirForConfig(configOptions ConfigOptions) (string, error) {
	// Check for custom skills directory configuration
	if skillsDir := configOptions.Get("repl.skillsdir"); skillsDir != "" {
		// Expand ~ to home directory if present
		if strings.HasPrefix(skillsDir, "~") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			skillsDir = filepath.Join(homeDir, skillsDir[1:])
		}
		return skillsDir, nil
	}

	// Use default
	return getDefaultSkillsDir()
}

// ExecuteSkill executes a skill by running its instructions
func (r *REPL) ExecuteSkill(skillName string, args []string) error {
	registry := r.skillRegistry
	if registry == nil {
		return fmt.Errorf("skill registry not initialized")
	}

	skill, exists := registry.GetSkill(skillName)
	if !exists {
		return fmt.Errorf("skill '%s' not found", skillName)
	}

	// For now, we'll display the skill content and let the user interact with it
	// In the future, this could be extended to automatically execute scripts or follow instructions

	fmt.Printf("Executing skill: %s\r\n", skill.Name)
	fmt.Printf("Description: %s\r\n\r\n", skill.Description)

	// Display the skill content (excluding frontmatter)
	contentParts := strings.SplitN(skill.Content, "---", 3)
	if len(contentParts) >= 3 {
		body := strings.TrimSpace(contentParts[2])
		fmt.Printf("%s\r\n\r\n", body)
	}

	// Check for executable scripts in the skill directory
	scripts, err := r.skillRegistry.findExecutableScripts(skill.Path)
	if err == nil && len(scripts) > 0 {
		fmt.Printf("Available scripts in this skill:\r\n")
		for _, script := range scripts {
			fmt.Printf("  %s\r\n", script)
		}
		fmt.Printf("\r\n")
	}

	return nil
}

// findExecutableScripts finds executable scripts in a skill directory
func (r *SkillRegistry) findExecutableScripts(skillPath string) ([]string, error) {
	var scripts []string

	err := filepath.Walk(skillPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip SKILL.md and directories
		if info.IsDir() || info.Name() == "SKILL.md" {
			return nil
		}

		// Check if it's executable or a script file
		if strings.HasSuffix(info.Name(), ".py") ||
			strings.HasSuffix(info.Name(), ".sh") ||
			strings.HasSuffix(info.Name(), ".js") ||
			strings.HasSuffix(info.Name(), ".pl") ||
			(info.Mode()&0111 != 0) { // executable bit set
			relPath, err := filepath.Rel(skillPath, path)
			if err != nil {
				relPath = path
			}
			scripts = append(scripts, relPath)
		}

		return nil
	})

	return scripts, err
}

// GetSkillAllowedTools returns the allowed tools for a skill (if specified)
func (sr *SkillRegistry) GetSkillAllowedTools(name string) []string {
	skill, exists := sr.GetSkill(name)
	if !exists {
		return nil
	}

	toolsStr, ok := skill.Metadata["allowed-tools"]
	if !ok || toolsStr == "" {
		return nil
	}

	// Parse comma-separated list
	var tools []string
	for _, tool := range strings.Split(toolsStr, ",") {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			tools = append(tools, tool)
		}
	}
	return tools
}

// buildSkillsPrompt builds the skills metadata for inclusion in the system prompt
// Uses progressive disclosure: only name+description loaded at startup
func (r *REPL) buildSkillsPrompt() string {
	if r.skillRegistry == nil {
		return ""
	}

	skills := r.skillRegistry.ListSkills()
	if len(skills) == 0 {
		return ""
	}

	var prompt strings.Builder
	prompt.WriteString("# AVAILABLE SKILLS\n\n")
	prompt.WriteString("You have access to the following specialized skills. Each skill is in a directory and contains instructions and resources.\n\n")
	prompt.WriteString("Skills are triggered by request content. When a user request matches a skill's purpose, you should:\n")
	prompt.WriteString("1. Mention which skill you'll use\n")
	prompt.WriteString("2. Read the full SKILL.md from the skill's directory\n")
	prompt.WriteString("3. Follow the instructions and use referenced files\n\n")

	for _, skill := range skills {
		prompt.WriteString(fmt.Sprintf("**%s**: %s\n", skill.Name, skill.Description))

		// Show allowed-tools if restricted
		if tools := r.skillRegistry.GetSkillAllowedTools(skill.Name); len(tools) > 0 {
			prompt.WriteString(fmt.Sprintf("  (Allowed tools: %s)\n", strings.Join(tools, ", ")))
		}

		// Show context type if forked
		if context, ok := skill.Metadata["context"]; ok && context == "fork" {
			prompt.WriteString("  (Runs in isolated context)\n")
		}

		prompt.WriteString("\n")
	}

	return prompt.String()
}

// SearchSkills searches skills by keyword in name and description
func (sr *SkillRegistry) SearchSkills(query string) []*Skill {
	query = strings.ToLower(query)
	var results []*Skill

	for _, skill := range sr.ListSkills() {
		if strings.Contains(strings.ToLower(skill.Name), query) ||
			strings.Contains(strings.ToLower(skill.Description), query) {
			results = append(results, skill)
		}
	}
	return results
}

// handleSkillsCommand handles the /skills command
func (r *REPL) handleSkillsCommand(args []string) (string, error) {
	if r.skillRegistry == nil {
		r.skillRegistry = NewSkillRegistry()
		skillsDir, err := getSkillsDirForConfig(r.configOptions)
		if err != nil {
			return fmt.Sprintf("Error determining skills directory: %v\n", err), nil
		}
		if err := r.skillRegistry.LoadSkills(skillsDir); err != nil {
			return fmt.Sprintf("Error loading skills: %v\n", err), nil
		}
	}

	if len(args) < 2 {
		// List all skills
		skills := r.skillRegistry.ListSkills()
		if len(skills) == 0 {
			skillsDir, _ := getSkillsDirForConfig(r.configOptions)
			return fmt.Sprintf("No skills found.\n\nSkills directory: %s\n\nCreate a directory with a SKILL.md file to add a skill.\n", skillsDir), nil
		}

		var output strings.Builder
		output.WriteString("Available Skills:\n\n")
		for i, skill := range skills {
			output.WriteString(fmt.Sprintf("%d. %s\n", i+1, skill.Name))
			output.WriteString(fmt.Sprintf("   Description: %s\n", skill.Description))

			// Show metadata if present
			if tools := r.skillRegistry.GetSkillAllowedTools(skill.Name); len(tools) > 0 {
				output.WriteString(fmt.Sprintf("   Allowed tools: %s\n", strings.Join(tools, ", ")))
			}
			if category, ok := skill.Metadata["category"]; ok {
				output.WriteString(fmt.Sprintf("   Category: %s\n", category))
			}
			output.WriteString("\n")
		}

		output.WriteString("Commands:\n")
		output.WriteString("  /skills list                - List all skills\n")
		output.WriteString("  /skills show <name>         - Show full skill details\n")
		output.WriteString("  /skills search <keyword>    - Search skills by keyword\n")
		output.WriteString("  /skills reload              - Reload skills from disk\n")
		output.WriteString("  /skills dir                 - Show skills directory path\n")

		return output.String(), nil
	}

	action := args[1]
	switch action {
	case "list":
		return r.handleSkillsCommand([]string{"/skills"}) // Reuse the main logic

	case "search":
		if len(args) < 3 {
			return "Usage: /skills search <keyword>\n", nil
		}
		query := strings.Join(args[2:], " ")
		results := r.skillRegistry.SearchSkills(query)
		if len(results) == 0 {
			return fmt.Sprintf("No skills found matching '%s'\n", query), nil
		}

		var output strings.Builder
		output.WriteString(fmt.Sprintf("Found %d skill(s) matching '%s':\n\n", len(results), query))
		for _, skill := range results {
			output.WriteString(fmt.Sprintf("• %s\n", skill.Name))
			output.WriteString(fmt.Sprintf("  %s\n\n", skill.Description))
		}
		return output.String(), nil

	case "show":
		if len(args) < 3 {
			return "Usage: /skills show <skill-name>\n", nil
		}
		skillName := args[2]
		skill, exists := r.skillRegistry.GetSkill(skillName)
		if !exists {
			return fmt.Sprintf("Skill '%s' not found. Use '/skills list' to see available skills.\n", skillName), nil
		}

		var output strings.Builder
		output.WriteString(fmt.Sprintf("Skill: %s\n", skill.Name))
		output.WriteString(fmt.Sprintf("Description: %s\n", skill.Description))
		output.WriteString(fmt.Sprintf("Path: %s\n", skill.Path))

		// Show metadata
		output.WriteString("\nMetadata:\n")
		for key, value := range skill.Metadata {
			if key != "name" && key != "description" {
				output.WriteString(fmt.Sprintf("  %s: %s\n", key, value))
			}
		}

		// Show content preview
		contentParts := strings.SplitN(skill.Content, "---", 3)
		if len(contentParts) >= 3 {
			body := strings.TrimSpace(contentParts[2])
			scanner := bufio.NewScanner(strings.NewReader(body))
			lineCount := 0
			output.WriteString("\nContent preview:\n")
			for scanner.Scan() && lineCount < 15 {
				output.WriteString(fmt.Sprintf("  %s\n", scanner.Text()))
				lineCount++
			}
			if lineCount >= 15 {
				output.WriteString("  ... (truncated)\n")
			}
		}

		return output.String(), nil

	case "dir":
		skillsDir, err := getSkillsDirForConfig(r.configOptions)
		if err != nil {
			return fmt.Sprintf("Error: %v\n", err), nil
		}
		return fmt.Sprintf("Skills directory: %s\n", skillsDir), nil

	case "reload":
		r.skillRegistry = NewSkillRegistry()
		skillsDir, err := getSkillsDirForConfig(r.configOptions)
		if err != nil {
			return fmt.Sprintf("Error determining skills directory: %v\n", err), nil
		}
		if err := r.skillRegistry.LoadSkills(skillsDir); err != nil {
			return fmt.Sprintf("Error reloading skills: %v\n", err), nil
		}
		skills := r.skillRegistry.ListSkills()
		return fmt.Sprintf("Skills reloaded successfully. Loaded %d skill(s).\n", len(skills)), nil

	default:
		return fmt.Sprintf("Unknown action: %s\n\nAvailable actions:\n  list, show, search, dir, reload\n", action), nil
	}
}
