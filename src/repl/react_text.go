package main

import (
	"fmt"
	"github.com/trufae/mai/src/repl/llm"
	"github.com/trufae/mai/src/repl/art"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const toolsPrompt = `
Use Reasoning: low

Tool Agent Instructions:
- Understand the request, sketch a short plan, and avoid repeating steps.
- Pick the most efficient tool sequence. Skip tools if they are not required.
- Track progress after every action and stop when the goal is satisfied.

Respond using ONLY the tag blocks below. Do not add markdown formatting or prose outside these tags.

<tool_plan>
one concise step per line describing the plan you intend to follow
</tool_plan>
<tool_reasoning>
optional extra notes for context (leave empty if none)
</tool_reasoning>
<tool_call>
ToolRequired=true|false
Action=Think|Iterate|Done|Solve|Error
Tool=name.of.tool (omit or set to none if no tool call is needed)
NextStep=describe what happens next
Reasoning=short justification
argument=value for each tool parameter on separate lines
</tool_call>

Assignments may use either "key=value" or "key: value" notation. Keep the response compact so small models can follow it consistently.

Below you will find the user prompt and the list of tools

----
`

// getToolPrompt returns the content of the tool.md prompt file
func (repl *REPL) getToolPrompt(foo string) (string, error) {
	// Get prompt directory path
	toolPromptPath, err := repl.resolvePromptPath(foo)
	if err != nil {
		return "", fmt.Errorf("failed to find the tool prompt: %w", err)
	}
	toolPromptBytes, err := os.ReadFile(toolPromptPath)
	if err != nil {
		return "", fmt.Errorf("failed to read tool prompt file: %w", err)
	}
	return string(toolPromptBytes), nil
}

// adjustReasoningPrompt injects or adjusts a simple "Use Reasoning: <level>" directive
// inside the tool prompt to influence how much time the LLM should spend reasoning.
func adjustReasoningPrompt(prompt string, level string) string {
	if level == "" {
		return prompt
	}
	// Normalize level
	lvl := strings.ToLower(strings.TrimSpace(level))
	if lvl != "low" && lvl != "medium" && lvl != "high" {
		// Unknown value; keep original
		return prompt
	}
	// Replace existing directive if present
	re := regexp.MustCompile(`(?m)^\s*Use\s+Reasoning:\s*(low|medium|high)\s*$`)
	if re.MatchString(prompt) {
		return re.ReplaceAllString(prompt, "Use Reasoning: "+lvl)
	}
	// Otherwise, prepend a directive near the top
	return "Use Reasoning: " + lvl + "\n\n" + prompt
}

// buildMessageWithTools formats a message with tool information
func buildMessageWithTools(toolPrompt string, userInput string, ctx string, toolList string) string {
	return fmt.Sprintf("%s\n<prompt>\n%s\n</prompt>\n<context>%s</context>\n<tools>\n%s\n</tools>",
		toolPrompt, userInput, ctx, toolList)
}

func mapToArray(m map[string]interface{}) []string {
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

// extractTaggedSection returns the content inside the first matching tag pair.
// It supports multiple tag names (useful for backward compatibility) and is
// case-insensitive.
func extractTaggedSection(text string, tags ...string) string {
	for _, tag := range tags {
		pattern := fmt.Sprintf(`(?is)<\s*%s\s*>(.*?)</\s*%s\s*>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag))
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(text)
		if len(match) >= 2 {
			return match[1]
		}
	}
	return ""
}

// stripJSONComments removes single-line (//) and block (/* */) comments from JSON input.
func stripJSONComments(input string) string {
	// Remove single-line comments (// ...)
	reLine := regexp.MustCompile(`(?m)//.*$`)
	noLine := reLine.ReplaceAllString(input, "")
	// Remove block comments (/* ... */)
	reBlock := regexp.MustCompile(`(?s)/\*.*?\*/`)
	noBlock := reBlock.ReplaceAllString(noLine, "")
	return strings.TrimSpace(noBlock)
}

// parseMarkdownResponse parses the markdown-formatted response into PlanResponse
func parseMarkdownResponse(text string) (PlanResponse, string, error) {
	planContent := strings.TrimSpace(extractTaggedSection(text, "tool_plan", "|tool_plan|"))
	planLines := strings.Split(planContent, "\n")
	reNumPrefix := regexp.MustCompile(`^\d+[\).:\-]*\s*`)
	plan := make([]string, 0, len(planLines))
	for _, raw := range planLines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		line = strings.TrimSpace(reNumPrefix.ReplaceAllString(line, ""))
		if line != "" {
			plan = append(plan, line)
		}
	}

	explainText := strings.TrimSpace(extractTaggedSection(text, "tool_reasoning", "|tool_call_reasoning|"))
	callContent := strings.TrimSpace(extractTaggedSection(text, "tool_call", "|tool_call|"))
	if callContent == "" {
		return PlanResponse{}, "", fmt.Errorf("invalid response format: missing <tool_call> block")
	}

	lines := strings.Split(callContent, "\n")
	response := PlanResponse{
		Plan:      plan,
		PlanIndex: 0,
		ToolArgs:  map[string]interface{}{},
	}
	toolArgs := response.ToolArgs.(map[string]interface{})
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || line == "----" {
			continue
		}
		// Legacy "toolname + param=value" format
		if strings.Contains(line, "+") && !strings.Contains(line, "=") && !strings.Contains(line, ":") {
			parts := strings.SplitN(line, "+", 2)
			if len(parts) == 2 {
				response.SelectedTool = strings.TrimSpace(parts[0])
				paramsStr := strings.TrimSpace(parts[1])
				for _, p := range strings.Fields(paramsStr) {
					kv := strings.SplitN(p, "=", 2)
					if len(kv) == 2 {
						toolArgs[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
					}
				}
			}
			continue
		}

		key := ""
		value := ""
		if idx := strings.IndexAny(line, ":="); idx != -1 {
			key = strings.TrimSpace(line[:idx])
			value = strings.TrimSpace(line[idx+1:])
		} else {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				key = strings.TrimSpace(fields[0])
				value = strings.TrimSpace(strings.Join(fields[1:], " "))
			}
		}
		if key == "" {
			continue
		}
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "toolrequired", "tool_required":
			val := strings.ToLower(value)
			response.ToolRequired = val == "true" || val == "yes" || val == "1"
		case "action":
			response.Action = value
		case "reasoning":
			response.Reasoning = value
		case "nextstep", "next_step":
			response.NextStep = value
		case "tool", "toolname", "selectedtool":
			if strings.EqualFold(value, "none") || strings.EqualFold(value, "null") || strings.EqualFold(value, "") {
				response.SelectedTool = ""
			} else {
				response.SelectedTool = value
			}
		case "planindex", "plan_index":
			if idx, err := strconv.Atoi(value); err == nil {
				response.PlanIndex = idx
			}
		case "progress":
			response.Progress = value
		default:
			toolArgs[key] = value
		}
	}

	if response.Progress == "" {
		if response.Reasoning != "" {
			response.Progress = response.Reasoning
		} else {
			response.Progress = response.NextStep
		}
	}

	return response, explainText, nil
}

func (r *REPL) toolStep(toolPrompt string, input string, ctx string, toolList string) (PlanResponse, string, error) {
	query := buildMessageWithTools(toolPrompt, input, ctx, toolList)
	if r.configOptions.GetBool("repl.debug") {
		art.DebugBanner("Tools Query", query)
	}
	messages := []llm.Message{{Role: "user", Content: query}}
	responseText, err := r.currentClient.SendMessage(messages, false, nil)
	if err != nil {
		return PlanResponse{}, "", fmt.Errorf("failed to get response for tools: %v", err)
	}
	if r.configOptions.GetBool("repl.debug") {
		art.DebugBanner("Tools Response", responseText)
	}
	// strip out any internal reasoning between <think>...</think> before processing
	reThink := regexp.MustCompile(`(?s)\s*<think>.*?</think>\s*`)
	responseText = reThink.ReplaceAllString(responseText, "")
	if responseText == "" {
		return PlanResponse{}, "", fmt.Errorf("cancel empty response from the llm")
	}
	// debug(responseText)
	response, explainText, err := parseMarkdownResponse(responseText)
	if err != nil {
		return PlanResponse{}, "", err
	}
	return response, explainText, nil
}

func (r *REPL) ReactText(messages []llm.Message, input string) (string, error) {
	// TODO: Do something with the previous messages
	display := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.display")))
	if display == "" {
		display = "verbose"
	}
	fmt.Println("NONOTE")
	showPlan := (display == "verbose" || display == "plan")
	toolPrompt := toolsPrompt
	// Apply reasoning level directive
	toolPrompt = adjustReasoningPrompt(toolPrompt, r.configOptions.Get("mcp.reason"))

	toolList, err := GetAvailableTools(Quiet) // Markdown)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	if strings.TrimSpace(toolList) == "" {
		fmt.Println("No tools available, doing nothing")
		return input, nil
	}
	context := ""
	stepCount := 0
	reasoning := ""
	clearScreen := true
	for {
		stepCount++
		step, expl, err := r.toolStep(toolPrompt, input, context, toolList)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			if strings.Contains(err.Error(), "cancel") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue
		}
		if clearScreen && display != "quiet" {
			prompt := r.configOptions.Get("repl.prompt")
			if prompt == "" {
				prompt = ">>>"
			}
			fmt.Print("\033[2J\033[H\033[33m" + prompt + " " + input + "\r\n")
			cl := len(context)
			if cl > 0 {
				fmt.Printf("Context: %d bytes\r\n", cl)
			}
		}
		if display != "quiet" && (display == "verbose" || display == "progress") {
			fmt.Printf("\033[0m\n%s\r\n", step.Progress)
		}
		if display != "quiet" && (display == "verbose" || display == "reason") {
			fmt.Printf("\r\n%s\r\n\r\n", step.Reasoning)
		}
		if showPlan && display != "quiet" {
			planString := "## Plan\n\n\r"
			i := 0
			for _, s := range step.Plan {
				if i == step.PlanIndex {
					fmt.Print("\033[36m >> ")
				} else {
					fmt.Print("\033[32m -- ")
				}
				fmt.Printf("%s\r\n", s)
				planString += fmt.Sprintf("%d. %s\n", i, s)
				i++
			}
		} else {
			if display != "quiet" {
				fmt.Printf("\033[0m\r\n%s\r\n", step.NextStep)
			}
		}
		fmt.Printf("\033[0m")

		if step.Action == "" || step.Action == "Solve" || step.Action == "Done" {
			fmt.Println("(tools) Problem solved")
			break
		}
		fmt.Println("Action: " + step.Action)
		/*
			if !step.ToolRequired {
				if expl != "" {
					reasoning += "\n\n## Reasoning\n\n" + expl
				}
				break
			}
		*/
		if step.SelectedTool == "" {
			continue
		}
		toolName := strings.ReplaceAll(step.SelectedTool, ".", "/")
		tool := &Tool{
			Name: toolName,
			Args: mapToArray(step.ToolArgs.(map[string]interface{})),
		}
		if display != "quiet" {
			fmt.Printf("\r\n\033[0mUsing Tool: %s\r\n\033[0m", tool.ToString())
		}
		timeout, err := r.configOptions.GetNumber("mcp.timeout")
		if err != nil || timeout <= 0 {
			timeout = 60
		}
		result, err := callTool(tool, r.configOptions.GetBool("mcp.debug"), int(timeout))
		if err != nil {
			input += fmt.Sprintf("\nTool %s execution failed: %s\n\n", tool.ToString(), err.Error())
			continue
			// return "", err
		}
		/*
			fmt.Println ("<calltoolResult>")
			fmt.Println (result)
			fmt.Println ("</calltoolResult>")
		*/
		// results = append(results, result)
		// toolResponse := fmt.Sprintf("\n\n## Step %d Tool Response\n\n**Reasoning**: %s\n**Next Step**: %s\n**ToolName**: %s\n**Contents**: %s\n", stepCount, step.Reasoning, step.NextStep, tool.Name, result)
		toolResponse := fmt.Sprintf("\n\n## Step %d Tool Response\n\n**Reasoning**: %s\n**ToolName**: %s\n**Contents**:\n\n```\n%s\n```\n\n", stepCount, step.NextStep, tool.Name, result)
		toolResponse += fmt.Sprintf("reason: %s\n", step.Reasoning)
		// fmt.Println (toolResponse)
		if expl != "" {
			context += "\n\n## Context\n\n" + expl
			if display != "quiet" && (display == "verbose" || display == "reason") {
				reasoning += "\n\n## Reasoning\n\n" + expl
			}
		}
		reasoning += "- " + step.Progress + "\n"
		context += toolResponse
		// input += planString + toolResponse
	}
	if reasoning != "" && display != "quiet" {
		reasoning = "<reasoning>\n" + reasoning + "</reasoning>\n"
	}
	if display != "quiet" {
		fmt.Println(strings.ReplaceAll(reasoning, "\n", "\r\n"))
	}
	return input + context, nil
	// return input + context + reasoning, nil
}
