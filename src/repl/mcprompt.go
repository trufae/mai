package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/art"
	"github.com/trufae/mai/src/repl/llm"
	"os/exec"
	"strings"
)

// GetAvailableMCPrompts runs 'mai-tool prompts list' and returns the output as a string
func GetAvailableMCPrompts(f Format) (string, error) {
	var cmd *exec.Cmd
	switch f {
	case Quiet:
		cmd = exec.Command("mai-tool", "-q", "prompts", "list")
	case JSON:
		cmd = exec.Command("mai-tool", "-j", "prompts", "list")
	case Markdown:
		cmd = exec.Command("mai-tool", "prompts", "list")
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mai-tool prompts list failed: %v: %s", err, stderr.String())
	}
	return out.String(), nil
}

// GetMCPromptContent fetches a specific prompt's rendered content
func GetMCPromptContent(fullName string) (string, error) {
	// fullName format: server/prompt or just prompt
	cmd := exec.Command("mai-tool", "prompts", "get", fullName)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mai-tool prompts get failed: %v: %s", err, stderr.String())
	}
	return out.String(), nil
}

// mcpPromptChoice is the expected JSON returned by the LLM when choosing a template
type mcpPromptChoice struct {
	Prompt    string `json:"prompt"`    // server/name or name
	Reasoning string `json:"reasoning"` // optional explanation
}

// prepareMCPromptTemplate queries the model with available MCP prompts and the user's query
// to select which plan template to import into the newtools flow. It sets the package-level
// variable planTemplate used by newtools.go.
func (r *REPL) prepareMCPromptTemplate(userInput string, messages []llm.Message) (string, error) {
	// List all available MCP prompts
	promptList, err := GetAvailableMCPrompts(Markdown)
	if err != nil {
		// If we can't retrieve prompts, silently skip
		return "", err
	}

	// Check if there are any actual prompts available
	lines := strings.Split(promptList, "\n")
	hasPrompts := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed != "# Prompts Catalog" {
			hasPrompts = true
			break
		}
	}
	if !hasPrompts {
		// No prompts available, skip
		return "", nil
	}

	// Build a selection prompt
	selectionInstruction := `You are selecting the best plan template (from MCP prompts) to solve the user's task using tools.
Return a concise JSON object only, with the fields:
{"prompt": "server/name", "reasoning": "why this template fits"}`

	query := strings.Builder{}
	query.WriteString(selectionInstruction)
	query.WriteString("\n<prompts>\n")
	query.WriteString(promptList)
	query.WriteString("\n</prompts>\n")
	query.WriteString("<query>\n")
	query.WriteString(userInput)
	query.WriteString("\n</query>")

	// Build messages: keep current system and optional history already prepared by caller
	// We send just a single user message to keep the selection focused
	req := []llm.Message{{Role: "user", Content: query.String()}}

	// Debug output: show the MCP prompt selection query
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("MCP Prompt Selection", query.String())
	}

	resp, err := r.currentClient.SendMessage(req, false, nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to query LLM for mcpprompts selection: %w", err)
	}

	// Trim any leading <think> block (models sometimes prefix replies
	// with internal reasoning). Then, if the client requests hiding of
	// think regions, remove any remaining <think>...</think> sections.
	resp = llm.TrimLeadingThink(resp)
	if r.currentClient != nil && r.currentClient.Config != nil && r.currentClient.Config.ThinkHide {
		resp = strings.ReplaceAll(resp, "<think>", "")
		resp = strings.ReplaceAll(resp, "</think>", "")
	}
	jsonText, _ := extractJSONBlock(resp)

	// Debug output for MCP prompt selection
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("MCP Prompt Selection JSON", jsonText)
	}
	//jsonText = stripJSONComments(jsonText)
	if strings.TrimSpace(jsonText) == "" {
		// If the model did not reply JSON, attempt minimal extraction: look for server/name in text
		// and skip silently if nothing found.
		return "", nil
	}

	var choice mcpPromptChoice
	if err := json.Unmarshal([]byte(jsonText), &choice); err != nil {
		return "", err // don't fail the run if parsing fails
	}
	name := strings.TrimSpace(choice.Prompt)
	if name == "" {
		return "", nil
	}

	// Validate that the chosen prompt is in the available list
	if !strings.Contains(promptList, name) {
		// Invalid choice, skip
		return "", nil
	}

	// Fetch the selected template content and set it for newtools
	content, err := GetMCPromptContent(name)
	if err != nil {
		return "", nil // keep going without a template on failure
	}
	// Inject the selected template into the tools prompt used by newtools
	return "\n# Selected Plan Template\n\n" + strings.TrimSpace(content) + "\n", nil
}
