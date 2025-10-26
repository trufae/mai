package main

import (
	"fmt"
	"sort"
	"strings"
)

type quietToolEntry struct {
	Server    string
	Name      string
	Purpose   string
	WhenToUse string
	Category  string
	Args      []ToolParameter
}

func buildQuietToolEntry(serverName string, tool Tool) quietToolEntry {
	purpose, whenHint := sanitizeToolDescription(tool.Description)
	params := tool.Parameters
	if len(params) == 0 && tool.InputSchema != nil {
		params = extractParametersFromSchema(tool.InputSchema)
	}
	arguments := make([]ToolParameter, len(params))
	copy(arguments, params)
	sort.Slice(arguments, func(i, j int) bool { return arguments[i].Name < arguments[j].Name })

	entry := quietToolEntry{
		Server:    serverName,
		Name:      tool.Name,
		Purpose:   purpose,
		WhenToUse: formatWhenToUse(purpose, whenHint),
		Category:  categorizeTool(tool.Name, purpose),
		Args:      arguments,
	}
	return entry
}

func sanitizeToolDescription(desc string) (string, string) {
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

func formatWhenToUse(purpose, hint string) string {
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

func formatQuietArgument(arg ToolParameter) string {
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

func categorizeTool(name, description string) string {
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
