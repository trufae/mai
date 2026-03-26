package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trufae/mai/src/repl/llm"
)

// Skill represents a local skill with its metadata and content.
type Skill struct {
	Name        string
	Description string
	Path        string // Full path to the skill directory or markdown file
	Metadata    map[string]string
	Content     string // Full content of SKILL.md
	IsFile      bool   // True when the skill was loaded from a standalone markdown file
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

// LoadSkills discovers and loads all skills from the specified skills directory.
// A skill entry can be either:
// - a directory containing SKILL.md/skill.md/skills.md
// - a standalone .md file, treated as a prompt-backed skill
func (sr *SkillRegistry) LoadSkills(skillsDir string) error {
	// Use default directory if none specified
	if skillsDir == "" {
		var err error
		skillsDir, err = getDefaultSkillsDir()
		if err != nil {
			return fmt.Errorf("failed to find default skills directory: %v", err)
		}
	}

	// Missing directories are not an error; some search roots are optional.
	fi, err := os.Stat(skillsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat skills directory: %v", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("skills path is not a directory: %s", skillsDir)
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("failed to read skills directory: %v", err)
	}

	for _, entry := range entries {
		skillPath := filepath.Join(skillsDir, entry.Name())
		skill, err := sr.loadSkillEntry(skillPath, entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load skill %s: %v\n", entry.Name(), err)
			continue
		}
		if skill != nil {
			sr.skills[skill.Name] = skill
		}
	}

	return nil
}

func (sr *SkillRegistry) loadSkillEntry(skillPath string, entry os.DirEntry) (*Skill, error) {
	if entry.IsDir() {
		return sr.loadSkillDir(skillPath)
	}
	if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
		return sr.loadSkillFile(skillPath)
	}
	return nil, nil
}

func (sr *SkillRegistry) loadSkillDir(skillPath string) (*Skill, error) {
	skillMdPath, err := findSkillMarkdown(skillPath)
	if err != nil {
		return nil, err
	}
	return sr.loadSkillMarkdown(skillMdPath, skillPath, false, true)
}

func (sr *SkillRegistry) loadSkillFile(skillPath string) (*Skill, error) {
	return sr.loadSkillMarkdown(skillPath, skillPath, true, false)
}

func findSkillMarkdown(skillPath string) (string, error) {
	candidates := []string{"SKILL.md", "skill.md", "skills.md"}
	for _, name := range candidates {
		path := filepath.Join(skillPath, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("skill markdown not found in %s", skillPath)
}

func (sr *SkillRegistry) loadSkillMarkdown(contentPath, sourcePath string, isFile bool, strict bool) (*Skill, error) {
	content, err := os.ReadFile(contentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill markdown: %v", err)
	}

	skill, err := sr.parseSkillFile(string(content))
	if err != nil {
		if strict {
			return nil, fmt.Errorf("failed to parse skill markdown: %v", err)
		}
		skill = sr.parseLooseSkillFile(contentPath, string(content))
	}

	skill.Path = sourcePath
	skill.Content = string(content)
	skill.IsFile = isFile

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

func (sr *SkillRegistry) parseLooseSkillFile(path, content string) *Skill {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &Skill{
		Name:        base,
		Description: inferSkillDescription(content),
		Metadata:    map[string]string{"source": "markdown"},
	}
}

func inferSkillDescription(content string) string {
	body := skillBody(content)
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if trimmed != "" {
				return trimmed
			}
			continue
		}
		if len(trimmed) > 120 {
			return trimmed[:120] + "..."
		}
		return trimmed
	}
	return "Reusable markdown prompt"
}

func skillBody(content string) string {
	parts := strings.SplitN(content, "---", 3)
	if len(parts) >= 3 {
		return strings.TrimSpace(parts[2])
	}
	return strings.TrimSpace(content)
}

func (s *Skill) Body() string {
	return skillBody(s.Content)
}

// isValidSkillName validates skill name format
func isValidSkillName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, ch := range name {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
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

func expandSkillDir(skillsDir string) (string, error) {
	if strings.HasPrefix(skillsDir, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		skillsDir = filepath.Join(homeDir, skillsDir[1:])
	}
	return skillsDir, nil
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

// getSkillsDirsForConfig returns all skill search directories in ascending precedence.
// Later directories override earlier ones when skill names collide.
func getSkillsDirsForConfig(configOptions ConfigOptions) ([]string, error) {
	var dirs []string

	defaultDir, err := getDefaultSkillsDir()
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, defaultDir)

	if claudeDir, err := findDirUpwards(".claude/skills"); err == nil && claudeDir != "" {
		dirs = append(dirs, claudeDir)
	}
	if maiDir, err := findDirUpwards(".mai/skills"); err == nil && maiDir != "" {
		dirs = append(dirs, maiDir)
	}
	if skillsDir := configOptions.Get("repl.skillsdir"); skillsDir != "" {
		expanded, err := expandSkillDir(skillsDir)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, expanded)
	}
	return dedupePaths(dirs), nil
}

func (r *REPL) reloadSkillRegistry() ([]string, error) {
	dirs, err := getSkillsDirsForConfig(r.configOptions)
	if err != nil {
		return nil, err
	}
	registry := NewSkillRegistry()
	for _, dir := range dirs {
		if err := registry.LoadSkills(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load skills from %s: %v\n", dir, err)
		}
	}
	r.skillRegistry = registry
	return dirs, nil
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

	body := skill.Body()
	if body != "" {
		fmt.Printf("%s\r\n\r\n", body)
	}

	if !skill.IsFile {
		scripts, err := r.skillRegistry.findExecutableScripts(skill.Path)
		if err == nil && len(scripts) > 0 {
			fmt.Printf("Available scripts in this skill:\r\n")
			for _, script := range scripts {
				fmt.Printf("  %s\r\n", script)
			}
			fmt.Printf("\r\n")
		}
	}

	return nil
}

// findExecutableScripts finds executable scripts in a skill directory
func (r *SkillRegistry) findExecutableScripts(skillPath string) ([]string, error) {
	var scripts []string
	info, err := os.Stat(skillPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return scripts, nil
	}

	err = filepath.Walk(skillPath, func(path string, info os.FileInfo, err error) error {
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
	prompt.WriteString("The host can inject the full instructions for these local skills when they are relevant to the task.\n\n")
	prompt.WriteString("Use the descriptions below to identify matching skills.\n\n")

	for _, skill := range skills {
		fmt.Fprintf(&prompt, "**%s**: %s\n", skill.Name, skill.Description)

		// Show allowed-tools if restricted
		if tools := r.skillRegistry.GetSkillAllowedTools(skill.Name); len(tools) > 0 {
			fmt.Fprintf(&prompt, "  (Allowed tools: %s)\n", strings.Join(tools, ", "))
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
		if _, err := r.reloadSkillRegistry(); err != nil {
			return fmt.Sprintf("Error loading skills: %v\n", err), nil
		}
	}

	if len(args) < 2 {
		// List all skills
		skills := r.skillRegistry.ListSkills()
		if len(skills) == 0 {
			skillDirs, _ := getSkillsDirsForConfig(r.configOptions)
			return fmt.Sprintf("No skills found.\n\nSearched directories:\n%s\n\nAdd markdown files or skill directories to one of these locations.\n", formatSkillDirs(skillDirs)), nil
		}

		var output strings.Builder
		output.WriteString("Available Skills:\n\n")
		for i, skill := range skills {
			fmt.Fprintf(&output, "%d. %s\n", i+1, skill.Name)
			fmt.Fprintf(&output, "   Description: %s\n", skill.Description)

			// Show metadata if present
			if tools := r.skillRegistry.GetSkillAllowedTools(skill.Name); len(tools) > 0 {
				fmt.Fprintf(&output, "   Allowed tools: %s\n", strings.Join(tools, ", "))
			}
			if category, ok := skill.Metadata["category"]; ok {
				fmt.Fprintf(&output, "   Category: %s\n", category)
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
		fmt.Fprintf(&output, "Found %d skill(s) matching '%s':\n\n", len(results), query)
		for _, skill := range results {
			fmt.Fprintf(&output, "• %s\n", skill.Name)
			fmt.Fprintf(&output, "  %s\n\n", skill.Description)
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
		fmt.Fprintf(&output, "Skill: %s\n", skill.Name)
		fmt.Fprintf(&output, "Description: %s\n", skill.Description)
		fmt.Fprintf(&output, "Path: %s\n", skill.Path)

		// Show metadata
		output.WriteString("\nMetadata:\n")
		for key, value := range skill.Metadata {
			if key != "name" && key != "description" {
				fmt.Fprintf(&output, "  %s: %s\n", key, value)
			}
		}

		// Show content preview
		body := skill.Body()
		if body != "" {
			scanner := bufio.NewScanner(strings.NewReader(body))
			lineCount := 0
			output.WriteString("\nContent preview:\n")
			for scanner.Scan() && lineCount < 15 {
				fmt.Fprintf(&output, "  %s\n", scanner.Text())
				lineCount++
			}
			if lineCount >= 15 {
				output.WriteString("  ... (truncated)\n")
			}
		}

		return output.String(), nil

	case "dir":
		skillDirs, err := getSkillsDirsForConfig(r.configOptions)
		if err != nil {
			return fmt.Sprintf("Error: %v\n", err), nil
		}
		return fmt.Sprintf("Skill search directories:\n%s\n", formatSkillDirs(skillDirs)), nil

	case "reload":
		skillDirs, err := r.reloadSkillRegistry()
		if err != nil {
			return fmt.Sprintf("Error reloading skills: %v\n", err), nil
		}
		skills := r.skillRegistry.ListSkills()
		return fmt.Sprintf("Skills reloaded successfully. Loaded %d skill(s).\n\nDirectories:\n%s\n", len(skills), formatSkillDirs(skillDirs)), nil

	default:
		return fmt.Sprintf("Unknown action: %s\n\nAvailable actions:\n  list, show, search, dir, reload\n", action), nil
	}
}

func formatSkillDirs(skillDirs []string) string {
	if len(skillDirs) == 0 {
		return "  (none)"
	}
	var out strings.Builder
	for _, dir := range skillDirs {
		out.WriteString("  ")
		out.WriteString(dir)
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

type skillChoice struct {
	Skills    []string `json:"skills"`
	Reasoning string   `json:"reasoning"`
}

func (r *REPL) prepareSkillContext(userInput string) (string, error) {
	if r.skillRegistry == nil {
		return "", nil
	}
	skills := r.skillRegistry.ListSkills()
	if len(skills) == 0 {
		return "", nil
	}

	client := r.currentClient
	if client == nil {
		var err error
		client, err = llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
		if err != nil {
			return "", fmt.Errorf("failed to create LLM client for skill selection: %v", err)
		}
	}

	var catalog strings.Builder
	for _, skill := range skills {
		catalog.WriteString("- ")
		catalog.WriteString(skill.Name)
		catalog.WriteString(": ")
		catalog.WriteString(skill.Description)
		catalog.WriteString("\n")
	}

	selectionInstruction := `You are selecting which local skills should be loaded for the user's request.
Return a JSON object only, with this shape:
{"skills":["skill-name"],"reasoning":"why these skills fit"}
Rules:
- Choose at most 3 skills.
- Only choose skills from the provided catalog.
- Return an empty array when no skill is needed.`

	var query strings.Builder
	query.WriteString(selectionInstruction)
	query.WriteString("\n<skills>\n")
	query.WriteString(catalog.String())
	query.WriteString("</skills>\n<query>\n")
	query.WriteString(userInput)
	query.WriteString("\n</query>")

	resp, err := client.SendMessage([]llm.Message{{Role: "user", Content: query.String()}}, false, nil, nil)
	if err != nil {
		return "", fmt.Errorf("skill selection query failed: %w", err)
	}

	resp = llm.TrimLeadingThink(resp)
	jsonText, _ := extractJSONBlock(resp)
	if strings.TrimSpace(jsonText) == "" {
		return "", nil
	}

	var choice skillChoice
	if err := json.Unmarshal([]byte(jsonText), &choice); err != nil {
		return "", err
	}

	seen := make(map[string]struct{})
	var selected []*Skill
	for _, name := range choice.Skills {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		skill, ok := r.skillRegistry.GetSkill(name)
		if !ok {
			continue
		}
		selected = append(selected, skill)
		if len(selected) >= 3 {
			break
		}
	}
	if len(selected) == 0 {
		return "", nil
	}

	var context strings.Builder
	context.WriteString("# LOADED SKILLS\n\n")
	for _, skill := range selected {
		context.WriteString("## ")
		context.WriteString(skill.Name)
		context.WriteString("\n")
		if skill.Description != "" {
			context.WriteString(skill.Description)
			context.WriteString("\n\n")
		}
		context.WriteString(skill.Body())
		context.WriteString("\n\n")
	}
	return strings.TrimSpace(context.String()), nil
}
