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

	fmt.Printf("DEBUG: Looking for skills in: %s\n", skillsDir)

	// Check if directory exists
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "DEBUG: Skills directory does not exist: %s\n", skillsDir)
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
			fmt.Fprintf(os.Stderr, "DEBUG: Loaded skill: %s\n", skill.Name)
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

	// Simple key-value parsing for frontmatter (supports basic YAML)
	metadata := make(map[string]string)
	lines := strings.Split(frontmatter, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle basic key: value format
		if colonIdx := strings.Index(line, ":"); colonIdx > 0 {
			key := strings.TrimSpace(line[:colonIdx])
			value := strings.TrimSpace(line[colonIdx+1:])
			// Remove quotes if present
			if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'')) {
				value = value[1 : len(value)-1]
			}
			metadata[key] = value
		}
	}

	skill := &Skill{
		Metadata: metadata,
	}

	// Extract required fields
	var ok bool
	if skill.Name, ok = metadata["name"]; !ok || skill.Name == "" {
		return nil, fmt.Errorf("missing 'name' field in frontmatter")
	}

	if skill.Description, ok = metadata["description"]; !ok || skill.Description == "" {
		return nil, fmt.Errorf("missing 'description' field in frontmatter")
	}

	return skill, nil
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

// buildSkillsPrompt builds the skills metadata for inclusion in the system prompt
func (r *REPL) buildSkillsPrompt() string {
	if r.skillRegistry == nil {
		return ""
	}

	skills := r.skillRegistry.ListSkills()
	if len(skills) == 0 {
		return ""
	}

	var prompt strings.Builder
	prompt.WriteString("AVAILABLE CLAUDE SKILLS:\n")
	prompt.WriteString("You have access to the following skills. Each skill contains instructions and resources in its directory.\n")
	prompt.WriteString("When a user request matches a skill's description, you should use that skill by reading its SKILL.md file and following its instructions.\n\n")

	for _, skill := range skills {
		prompt.WriteString(fmt.Sprintf("- %s: %s\n", skill.Name, skill.Description))
		prompt.WriteString(fmt.Sprintf("  Location: %s\n", skill.Path))
	}

	prompt.WriteString("\nTo use a skill:\n")
	prompt.WriteString("1. Read the SKILL.md file from the skill's directory\n")
	prompt.WriteString("2. Follow the instructions in the SKILL.md file\n")
	prompt.WriteString("3. Use any additional files or scripts referenced in the skill\n")
	prompt.WriteString("4. Execute any scripts or commands as needed using available tools\n")

	return prompt.String()
}

// handleSkillsCommand handles the /skills command
func (r *REPL) handleSkillsCommand(args []string) (string, error) {
	if r.skillRegistry == nil {
		r.skillRegistry = NewSkillRegistry()
		skillsDir, err := getSkillsDirForConfig(r.configOptions)
		if err != nil {
			return fmt.Sprintf("Error determining skills directory: %v\r\n", err), nil
		}
		if err := r.skillRegistry.LoadSkills(skillsDir); err != nil {
			return fmt.Sprintf("Error loading skills: %v\r\n", err), nil
		}
	}

	if len(args) < 2 {
		// List all skills
		skills := r.skillRegistry.ListSkills()
		if len(skills) == 0 {
			return "No skills found. Skills should be placed in ~/.config/mai/skills/\r\n", nil
		}

		var output strings.Builder
		output.WriteString("Available Skills:\r\n\r\n")
		for _, skill := range skills {
			output.WriteString(fmt.Sprintf("📋 %s\r\n", skill.Name))
			output.WriteString(fmt.Sprintf("   %s\r\n", skill.Description))
			output.WriteString(fmt.Sprintf("   Path: %s\r\n\r\n", skill.Path))
		}

		output.WriteString("Usage:\r\n")
		output.WriteString("  /skills list                    - List all available skills\r\n")
		output.WriteString("  /skills show <name>             - Show details of a specific skill\r\n")
		output.WriteString("  /skills execute <name> [args]   - Execute a skill\r\n")
		output.WriteString("  /skills reload                  - Reload skills from disk\r\n")

		return output.String(), nil
	}

	action := args[1]
	switch action {
	case "list":
		return r.handleSkillsCommand([]string{"/skills"}) // Reuse the main logic

	case "show":
		if len(args) < 3 {
			return "Usage: /skills show <skill-name>\r\n", nil
		}
		skillName := args[2]
		skill, exists := r.skillRegistry.GetSkill(skillName)
		if !exists {
			return fmt.Sprintf("Skill '%s' not found\r\n", skillName), nil
		}

		var output strings.Builder
		output.WriteString(fmt.Sprintf("Skill: %s\r\n", skill.Name))
		output.WriteString(fmt.Sprintf("Description: %s\r\n", skill.Description))
		output.WriteString(fmt.Sprintf("Path: %s\r\n\r\n", skill.Path))

		// Show content preview
		contentParts := strings.SplitN(skill.Content, "---", 3)
		if len(contentParts) >= 3 {
			body := strings.TrimSpace(contentParts[2])
			// Show first few lines
			scanner := bufio.NewScanner(strings.NewReader(body))
			lineCount := 0
			output.WriteString("Content preview:\r\n")
			for scanner.Scan() && lineCount < 10 {
				output.WriteString(fmt.Sprintf("  %s\r\n", scanner.Text()))
				lineCount++
			}
			if lineCount >= 10 {
				output.WriteString("  ... (use /skills execute to see full content)\r\n")
			}
		}

		return output.String(), nil

	case "execute":
		if len(args) < 3 {
			return "Usage: /skills execute <skill-name> [args...]\r\n", nil
		}
		skillName := args[2]
		skillArgs := args[3:]
		return "", r.ExecuteSkill(skillName, skillArgs)

	case "reload":
		r.skillRegistry = NewSkillRegistry()
		skillsDir, err := getSkillsDirForConfig(r.configOptions)
		if err != nil {
			return fmt.Sprintf("Error determining skills directory: %v\r\n", err), nil
		}
		if err := r.skillRegistry.LoadSkills(skillsDir); err != nil {
			return fmt.Sprintf("Error reloading skills: %v\r\n", err), nil
		}
		return "Skills reloaded successfully\r\n", nil

	default:
		return fmt.Sprintf("Unknown action: %s\r\nAvailable actions: list, show, execute, reload\r\n", action), nil
	}
}
