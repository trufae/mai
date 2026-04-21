package wmcplib

import (
	"fmt"
	"sort"
	"strings"
)

// QuietToolEntry is a pre-rendered, sort-ready description of one tool used
// by the compact catalog formats. It is exported so alternative formatters
// (e.g. mai-repl's embed backend) can reuse it.
type QuietToolEntry struct {
	Server    string
	Name      string
	Purpose   string
	WhenToUse string
	Category  string
	Args      []ToolParameter
}

// BuildQuietToolEntry renders a single tool into a QuietToolEntry.
func BuildQuietToolEntry(serverName string, tool Tool) QuietToolEntry {
	purpose, whenHint := SanitizeToolDescription(tool.Description)
	params := tool.Parameters
	if len(params) == 0 && tool.InputSchema != nil {
		params = ExtractParametersFromSchema(tool.InputSchema)
	}
	arguments := make([]ToolParameter, len(params))
	copy(arguments, params)
	sort.Slice(arguments, func(i, j int) bool { return arguments[i].Name < arguments[j].Name })

	return QuietToolEntry{
		Server:    serverName,
		Name:      tool.Name,
		Purpose:   purpose,
		WhenToUse: FormatWhenToUse(purpose, whenHint),
		Category:  CategorizeTool(tool.Name, purpose),
		Args:      arguments,
	}
}

// SanitizeToolDescription strips <think></think> blocks from a tool
// description and returns (purpose, when-to-use-hint).
func SanitizeToolDescription(desc string) (string, string) {
	if desc == "" {
		return "", ""
	}
	clean := desc
	var hints []string
	for {
		start := strings.Index(clean, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(clean[start:], "</think>")
		if end == -1 {
			clean = clean[:start]
			break
		}
		end += start
		thinkText := clean[start+len("<think>") : end]
		if trimmed := strings.TrimSpace(compactSpaces(thinkText)); trimmed != "" {
			hints = append(hints, trimmed)
		}
		clean = clean[:start] + clean[end+len("</think>"):]
	}

	purpose := strings.TrimSpace(compactSpaces(clean))
	whenHint := strings.TrimSpace(compactSpaces(strings.Join(hints, " ")))
	return purpose, whenHint
}

// FormatWhenToUse synthesises a short "Use to ..." hint from a tool purpose.
func FormatWhenToUse(purpose, hint string) string {
	if hint != "" {
		return compactSpaces(hint)
	}
	return deriveWhenFromPurpose(purpose)
}

func deriveWhenFromPurpose(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return "Use when this capability fits the request"
	}
	words := strings.Fields(purpose)
	if len(words) == 0 {
		return "Use when this capability fits the request"
	}
	first := strings.ToLower(words[0])
	switch {
	case strings.HasSuffix(first, "ies") && len(first) > 3:
		first = first[:len(first)-3] + "y"
	case strings.HasSuffix(first, "ses") || strings.HasSuffix(first, "xes") || strings.HasSuffix(first, "zes") || strings.HasSuffix(first, "ches") || strings.HasSuffix(first, "shes"):
		first = first[:len(first)-2]
	case strings.HasSuffix(first, "es") && len(first) > 2:
		first = first[:len(first)-1]
	case strings.HasSuffix(first, "s") && len(first) > 1:
		first = first[:len(first)-1]
	}
	rest := strings.TrimSpace(strings.TrimPrefix(purpose, words[0]))
	if rest == "" {
		return compactSpaces(fmt.Sprintf("Use to %s", first))
	}
	return compactSpaces(fmt.Sprintf("Use to %s %s", first, strings.TrimSpace(rest)))
}

// FormatQuietArgument renders a single tool parameter in quiet format.
func FormatQuietArgument(arg ToolParameter) string {
	name := strings.TrimSpace(arg.Name)
	if name == "" {
		name = "argument"
	}
	typeLabel := strings.TrimSpace(compactSpaces(arg.Type))
	if typeLabel == "" {
		typeLabel = "value"
	}
	requiredLabel := "optional"
	if arg.Required {
		requiredLabel = "required"
	}
	desc := strings.TrimSpace(compactSpaces(arg.Description))
	if desc != "" {
		return fmt.Sprintf("- %s=<%s> (%s) : %s", name, typeLabel, requiredLabel, desc)
	}
	return fmt.Sprintf("- %s=<%s> (%s)", name, typeLabel, requiredLabel)
}

// CategorizeTool classifies a tool into a coarse human-friendly category.
func CategorizeTool(name, description string) string {
	text := strings.ToLower(name + " " + description)
	if containsAny(text, []string{"write", "rename", "set", "update", "replace", "apply", "append", "delete", "remove", "create", "format", "patch", "edit", "modify", "use ", "use_", "toggle", "enable", "disable"}) {
		return "Editing"
	}
	if containsAny(text, []string{"file", "path", "directory", "folder", "filesystem"}) {
		return "File"
	}
	if containsAny(text, []string{"metadata", "status", "config", "capability", "version", "schema", "info"}) {
		return "Metadata"
	}
	if containsAny(text, []string{"list", "analy", "scan", "find", "search", "discover", "enumerate", "xref", "graph", "map"}) {
		return "Analysis"
	}
	if containsAny(text, []string{"show", "display", "get", "dump", "peek", "inspect", "view", "read", "print", "describe", "explain", "decompil", "disassembl"}) {
		return "Inspection"
	}
	return "Analysis"
}
