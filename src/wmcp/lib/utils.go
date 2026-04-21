package wmcplib

import (
	"log"
	"strings"
)

// normalizeToolName normalizes a tool name for drunk mode comparison
func normalizeToolName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

// FindBestToolMatch is the exported wrapper around findBestToolMatch for
// callers that need to do per-server fuzzy matching themselves (e.g. the
// HTTP REST endpoints).
func FindBestToolMatch(tools []Tool, requested string, drunk bool) (string, bool) {
	return findBestToolMatch(tools, requested, drunk)
}

// findBestToolMatch tries to resolve a requested tool name to an actual tool.
// Matching is strict by default; drunk mode enables normalized equality and
// fuzzy substring matches.
func findBestToolMatch(tools []Tool, requested string, drunk bool) (string, bool) {
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

		if actNorm == reqNorm {
			return act, true
		}

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

func debugLog(debug bool, format string, args ...interface{}) {
	if debug {
		log.Printf("DEBUG: "+format, args...)
	}
}

func compactSpaces(input string) string {
	if input == "" {
		return ""
	}
	return strings.Join(strings.Fields(input), " ")
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// ExtractParametersFromSchema extracts parameter information from a JSON
// schema "properties" object. Exported so callers can normalize tool schemas
// outside of the service.
func ExtractParametersFromSchema(schema map[string]interface{}) []ToolParameter {
	var parameters []ToolParameter

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return parameters
	}

	requiredFields := make(map[string]bool)
	if required, ok := schema["required"].([]interface{}); ok {
		for _, field := range required {
			if fieldName, ok := field.(string); ok {
				requiredFields[fieldName] = true
			}
		}
	}

	for name, propInterface := range properties {
		propInfo, ok := propInterface.(map[string]interface{})
		if !ok {
			continue
		}

		param := ToolParameter{
			Name:     name,
			Required: requiredFields[name],
		}

		if desc, ok := propInfo["description"].(string); ok {
			param.Description = desc
		}

		if typeStr, ok := propInfo["type"].(string); ok {
			param.Type = typeStr
		}

		parameters = append(parameters, param)
	}

	return parameters
}
