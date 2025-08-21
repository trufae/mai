package llm

import (
	"fmt"
	"strings"
)

// BuildConversationString constructs a single string representation of a
// conversation represented by messages. The behavior is controlled by the
// includeLLM and includeSystem flags and the format parameter.
//
// includeLLM: when true, include assistant/LLM messages; otherwise skip them.
// includeSystem: when true, include system messages; otherwise skip them.
// format: one of "tokens", "labeled", or "plain". If empty or unknown,
// "plain" behavior is used.
func BuildConversationString(messages []Message, includeLLM bool, includeSystem bool, format string, useLastUserOnly bool) string {
	if len(messages) == 0 {
		return ""
	}

	// If requested, only include the last user message (and system messages
	// if requested). This is useful when you want a short prompt built from the
	// latest user input plus any system context.
	if useLastUserOnly {
		var b strings.Builder
		// Include system messages first (if enabled)
		if includeSystem {
			for _, m := range messages {
				if strings.ToLower(m.Role) == "system" {
					var content string
					switch c := m.Content.(type) {
					case string:
						content = c
					default:
						content = fmt.Sprintf("%v", c)
					}
					if format == "tokens" {
						b.WriteString("<|start_of_turn>system\n")
						b.WriteString(content)
						b.WriteString("<|end_of_turn>\n")
					} else if format == "labeled" {
						b.WriteString("System: ")
						b.WriteString(content)
						b.WriteString("\n")
					} else {
						b.WriteString(content)
						if !strings.HasSuffix(content, "\n") {
							b.WriteString("\n")
						}
					}
				}
			}
		}

		// Find last user message
		var lastUser *Message
		for i := len(messages) - 1; i >= 0; i-- {
			if strings.ToLower(messages[i].Role) == "user" {
				lastUser = &messages[i]
				break
			}
		}
		if lastUser == nil {
			// Fallback to full conversation if no user message found
			return BuildConversationString(messages, includeLLM, includeSystem, format, false)
		}

		var content string
		switch c := lastUser.Content.(type) {
		case string:
			content = c
		default:
			content = fmt.Sprintf("%v", c)
		}

		// Append the last user message according to format
		if format == "tokens" {
			b.WriteString("<|start_of_turn>user\n")
			b.WriteString(content)
			b.WriteString("<|end_of_turn>\n")
		} else if format == "labeled" {
			b.WriteString("User: ")
			b.WriteString(content)
			b.WriteString("\n")
		} else {
			b.WriteString(content)
			if !strings.HasSuffix(content, "\n") {
				b.WriteString("\n")
			}
		}

		return b.String()
	}

	// Default: build full conversation
	var b strings.Builder
	for _, m := range messages {
		role := strings.ToLower(m.Role)
		// Filter roles per flags
		if role == "assistant" || role == "model" || role == "ai" {
			if !includeLLM {
				continue
			}
		}
		if role == "system" {
			if !includeSystem {
				continue
			}
		}

		// Extract content as string
		var content string
		switch c := m.Content.(type) {
		case string:
			content = c
		default:
			content = fmt.Sprintf("%v", c)
		}

		switch format {
		case "tokens":
			// Use explicit start/end of turn tokens which some models expect
			b.WriteString("<|start_of_turn>")
			b.WriteString(role)
			b.WriteString("\n")
			b.WriteString(content)
			b.WriteString("<|end_of_turn>\n")
		case "labeled":
			// Human-friendly labeled format
			b.WriteString(strings.Title(role))
			b.WriteString(": ")
			b.WriteString(content)
			b.WriteString("\n")
		default:
			// plain: just concatenate contents separated by newlines
			b.WriteString(content)
			if !strings.HasSuffix(content, "\n") {
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}
