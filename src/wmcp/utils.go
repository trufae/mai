package main

import (
	"log"
	"strings"
)

// normalizeToolName normalizes a tool name for drunk mode comparison
func normalizeToolName(name string) string {
	// Remove underscores and convert to lowercase
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

// findBestToolMatch tries to resolve a requested tool name to an actual tool
// name from the provided slice. Matching is strict by default; when drunk
// mode is enabled it will try normalized equality and fuzzy substring-based
// matches. It returns the matched tool name and true, or empty/false when
// nothing matched.
func findBestToolMatch(tools []Tool, requested string, drunk bool) (string, bool) {
	// Fast path: exact match
	for _, t := range tools {
		if t.Name == requested {
			return t.Name, true
		}
	}

	if !drunk {
		return "", false
	}

	reqNorm := normalizeToolName(requested)
	bestScore := 1 << 60
	bestName := ""

	for _, t := range tools {
		act := t.Name
		actNorm := normalizeToolName(act)

		// normalized exact
		if actNorm == reqNorm {
			return act, true
		}

		// prefer matches where one contains the other; shorter difference is better
		if strings.Contains(actNorm, reqNorm) {
			score := 100 + (len(actNorm) - len(reqNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}
		if strings.Contains(reqNorm, actNorm) {
			score := 200 + (len(reqNorm) - len(actNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}

		// fallback: prefix/suffix heuristics
		if strings.HasPrefix(actNorm, reqNorm) || strings.HasSuffix(actNorm, reqNorm) {
			score := 300 + (len(actNorm) - len(reqNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}
		if strings.HasPrefix(reqNorm, actNorm) || strings.HasSuffix(reqNorm, actNorm) {
			score := 400 + (len(reqNorm) - len(actNorm))
			if score < bestScore {
				bestScore = score
				bestName = act
			}
			continue
		}
	}

	if bestName != "" {
		return bestName, true
	}
	return "", false
}

// debugLog prints debug logs when debug mode is enabled
func debugLog(debug bool, format string, args ...interface{}) {
	if debug {
		log.Printf("DEBUG: "+format, args...)
	}
}

// compactSpaces removes extra spaces from a string
func compactSpaces(input string) string {
	if input == "" {
		return ""
	}
	return strings.Join(strings.Fields(input), " ")
}

// containsAny checks if the text contains any of the keywords
func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// extractParametersFromSchema extracts parameter information from JSON schema
func extractParametersFromSchema(schema map[string]interface{}) []ToolParameter {
	var parameters []ToolParameter

	// Extract properties from schema
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return parameters
	}

	// Extract required fields list
	requiredFields := make(map[string]bool)
	if required, ok := schema["required"].([]interface{}); ok {
		for _, field := range required {
			if fieldName, ok := field.(string); ok {
				requiredFields[fieldName] = true
			}
		}
	}

	// Process each property
	for name, propInterface := range properties {
		propInfo, ok := propInterface.(map[string]interface{})
		if !ok {
			continue
		}

		// Create parameter
		param := ToolParameter{
			Name:     name,
			Required: requiredFields[name],
		}

		// Extract description
		if desc, ok := propInfo["description"].(string); ok {
			param.Description = desc
		}

		// Extract type
		if typeStr, ok := propInfo["type"].(string); ok {
			param.Type = typeStr
		}

		parameters = append(parameters, param)
	}

	return parameters
}
