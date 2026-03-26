package llm

import (
	"context"
	"encoding/json"
	"strings"
)

func accountResponseText(ctx context.Context, text string) {
	if text == "" || ctx == nil {
		return
	}
	if cb, ok := ctx.Value(contextAccountTextCallbackKey).(func(string)); ok && cb != nil {
		cb(text)
	}
}

func appendResponseText(dst *strings.Builder, ctx context.Context, text string) {
	if text == "" {
		return
	}
	dst.WriteString(text)
	accountResponseText(ctx, text)
}

func extractOpenAIStreamDelta(data string) (string, []string) {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", nil
	}

	choices, ok := payload["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return "", nil
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return "", nil
	}

	content := strings.Join(collectDeltaTexts(delta["content"]), "")
	reasoning := append(collectDeltaTexts(delta["reasoning"]), collectDeltaTexts(delta["thinking"])...)
	reasoning = append(reasoning, collectDeltaTexts(delta["reasoning_content"])...)
	reasoning = append(reasoning, collectDeltaTexts(delta["thinking_content"])...)
	return content, reasoning
}

func extractOpenAIResponseMessage(body []byte) (string, []string) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil
	}

	choices, ok := payload["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return "", nil
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return "", nil
	}

	content := strings.Join(collectDeltaTexts(message["content"]), "")
	reasoning := append(collectDeltaTexts(message["reasoning"]), collectDeltaTexts(message["thinking"])...)
	reasoning = append(reasoning, collectDeltaTexts(message["reasoning_content"])...)
	reasoning = append(reasoning, collectDeltaTexts(message["thinking_content"])...)
	return content, reasoning
}

func collectDeltaTexts(v interface{}) []string {
	switch value := v.(type) {
	case string:
		if value != "" {
			return []string{value}
		}
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, collectDeltaTexts(item)...)
		}
		return out
	case map[string]interface{}:
		out := collectDeltaTexts(value["text"])
		if len(out) > 0 {
			return out
		}
		out = append(out, collectDeltaTexts(value["content"])...)
		out = append(out, collectDeltaTexts(value["reasoning"])...)
		out = append(out, collectDeltaTexts(value["thinking"])...)
		return out
	}
	return nil
}
