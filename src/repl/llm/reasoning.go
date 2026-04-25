package llm

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// NormalizeReasoningEffort converts user-facing aliases into the internal
// reasoning effort names used by providers. An empty return value means auto.
func NormalizeReasoningEffort(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "default":
		return "", true
	case "0", "false", "no", "off", "none", "disable", "disabled":
		return "none", true
	case "1", "true", "yes", "on":
		return "medium", true
	case "min", "minimal":
		return "minimal", true
	case "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(value)), true
	case "x-high", "extra-high", "extra_high", "very-high", "very_high":
		return "xhigh", true
	}
	return "", false
}

// ReasoningEffortDisplay returns the user-facing value for an effort string.
func ReasoningEffortDisplay(effort string) string {
	if effort == "" {
		return "auto"
	}
	return effort
}

// ReasoningEffortValues documents the accepted /set think.reason values.
func ReasoningEffortValues() string {
	return "auto, off/none, minimal, low, medium, high, xhigh"
}

func reasoningEnabled(effort string) bool {
	return effort != "" && effort != "none"
}

func effortRatio(effort string) float64 {
	switch effort {
	case "minimal":
		return 0.10
	case "low":
		return 0.20
	case "medium":
		return 0.50
	case "high":
		return 0.80
	case "xhigh":
		return 0.90
	default:
		return 0
	}
}

func reasoningBudgetTokens(effort string, maxTokens int) int {
	if !reasoningEnabled(effort) || maxTokens <= 1024 {
		return 0
	}
	budget := int(math.Round(float64(maxTokens) * effortRatio(effort)))
	if budget < 1024 {
		budget = 1024
	}
	// Anthropic requires budget_tokens to be lower than max_tokens.
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	return budget
}

func openAIReasoningEffort(model, effort string) (string, error) {
	if effort == "" {
		return "", nil
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if !isOpenAIReasoningModel(model) {
		if effort == "none" {
			return "", nil
		}
		return "", fmt.Errorf("think.reason=%s is only supported by OpenAI reasoning models for provider openai", effort)
	}
	if strings.HasPrefix(model, "gpt-5-pro") {
		return "high", nil
	}
	if effort == "none" && !openAIModelSupportsNoneReasoning(model) {
		return "", fmt.Errorf("think.reason=none is not supported by OpenAI model %s; use auto or a reasoning effort", model)
	}
	if strings.HasPrefix(model, "gpt-5.1") && !strings.Contains(model, "codex") {
		switch effort {
		case "minimal":
			return "low", nil
		case "xhigh":
			return "high", nil
		}
	}
	return effort, nil
}

func isOpenAIReasoningModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "gpt-5")
}

func openAIModelSupportsNoneReasoning(model string) bool {
	if !strings.HasPrefix(model, "gpt-5.") {
		return false
	}
	rest := strings.TrimPrefix(model, "gpt-5.")
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if len(parts) == 0 {
		return false
	}
	minor, err := strconv.Atoi(parts[0])
	return err == nil && minor >= 1
}

func claudeThinkingSupported(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "claude-3-7") ||
		strings.Contains(model, "claude-sonnet-4") ||
		strings.Contains(model, "claude-opus-4")
}

func geminiThinkingConfig(model, effort string) map[string]interface{} {
	if effort == "" {
		return nil
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(model, "gemini-3") {
		level := effort
		switch level {
		case "none", "minimal":
			level = "minimal"
		case "xhigh":
			level = "high"
		}
		return map[string]interface{}{"thinkingLevel": level}
	}

	if effort == "none" {
		return map[string]interface{}{"thinkingBudget": 0}
	}

	budget := 0
	switch effort {
	case "minimal":
		budget = 512
	case "low":
		budget = 1024
	case "medium":
		budget = 4096
	case "high":
		budget = 8192
	case "xhigh":
		budget = 16384
	}
	if budget == 0 {
		return nil
	}
	return map[string]interface{}{"thinkingBudget": budget}
}

func ollamaThinkValue(model, effort string) (interface{}, bool) {
	if effort == "" {
		return nil, false
	}
	if effort == "none" {
		return false, true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(model, "gpt-oss") {
		switch effort {
		case "minimal", "low":
			return "low", true
		case "medium":
			return "medium", true
		case "high", "xhigh":
			return "high", true
		}
	}
	return true, true
}

func openRouterReasoning(effort string, exclude bool) map[string]interface{} {
	if effort == "" {
		return nil
	}
	reasoning := map[string]interface{}{
		"exclude": exclude,
	}
	if effort == "none" {
		reasoning["effort"] = "none"
		return reasoning
	}
	reasoning["effort"] = effort
	return reasoning
}
