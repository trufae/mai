package main

import (
	"fmt"
	"github.com/trufae/mai/src/repl/art"
	"github.com/trufae/mai/src/repl/llm"
	"os"
	"regexp"
	"strings"
)

const toolsPrompt = `
You are an assistant that must resolve user prompt using the provided tools.

RULES:
- Do not output anything else outside these fields.
- Use the tool descriptions to decide which one is appropriate.
- Design a plan to resolve the problem and adjust its steps if necessary
- Analyze the context information and choose the tool necessary to go one step further in your plan
- Once you have all the information to resolve the user request use ACTION DONE

OUTPUT RESPONSE:

Case 1: You need to call a tool

<|response_begin|>
THINK: <short reason why>
ACTION: TOOL <tool-name> <arg>=<value> ...
<|response_end|>

Case 2: You are finished

<|response_begin|>
THINK: <short reason why no more tools are needed>
ACTION: DONE
ANSWER: <final reply for the user>
<|response_end|>
AVAILABLE TOOLS:

<|tools_begin|>
{tools}
<|tools_end|>

`

/*

EXAMPLES:

<|example_begin|>
User: "Open /bin/ls"
THINK: I need to open the file before analyzing
ACTION: TOOL openFile filePath=/bin/ls
<|example_end|>

<|example_begin|>
User: "What functions does it have?"
THINK: I should list all discovered functions
ACTION: TOOL listFunctions
<|example_end|>
*/

const oldToolsPrompt = `
You are a terminal assistant that can run tools to solve the user's request.

Available tools:
{tools}

Always respond using these uppercase fields:

THINK: <short reasoning for the next action>
ACTION: TOOL <tool-name> <arg>=<value> ...

If no tool is required, finish with:

THINK: <why nothing else is needed>
ACTION: DONE
ANSWER: <final reply for the user>

Output only these fields, no markdown, no extra prose.
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
	prompt := strings.Replace(toolPrompt, "{tools}", toolList, -1)
	var builder strings.Builder
	builder.WriteString(prompt)
	builder.WriteString("\n\nUSER QUERY TASK TO RESOLVE:\n")
	builder.WriteString(userInput)
	trimmedCtx := strings.TrimSpace(ctx)
	builder.WriteString("\n\nCONTEXT:\n")
	if trimmedCtx == "" {
		builder.WriteString("none")
	} else {
		builder.WriteString(trimmedCtx)
	}
	return builder.String()
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

func normalizeInline(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

func appendContextSection(current string, snippet string) string {
	trimmed := strings.TrimSpace(snippet)
	if trimmed == "" {
		return current
	}
	if current == "" {
		return trimmed
	}
	return current + "\n" + trimmed
}

func formatToolContext(step int, reason string, toolDesc string, output string) string {
	reasonLine := normalizeInline(reason)
	toolLine := strings.TrimSpace(toolDesc)
	result := strings.TrimSpace(output)
	lines := []string{fmt.Sprintf("STEP %d", step)}
	if reasonLine != "" {
		lines = append(lines, "THINK: "+reasonLine)
	}
	if toolLine != "" {
		lines = append(lines, "TOOL: "+toolLine)
	}
	lines = append(lines, "RESULT:")
	if result != "" {
		lines = append(lines, result)
	}
	lines = append(lines, "--")
	return strings.Join(lines, "\n")
}

func formatContextTag(tag string, content string) string {
	inline := normalizeInline(content)
	if inline == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s", strings.ToUpper(strings.TrimSpace(tag)), inline)
}

func formatToolLine(tool *Tool) string {
	if tool == nil {
		return ""
	}
	if len(tool.Args) == 0 {
		return tool.Name
	}
	return tool.Name + " " + strings.Join(tool.Args, " ")
}

// parseMarkdownResponse parses the response into PlanResponse
func parseMarkdownResponse(text string) (PlanResponse, string, error) {
	response := PlanResponse{
		Plan:      []string{},
		PlanIndex: 0,
		ToolArgs:  map[string]interface{}{},
	}
	toolArgs := response.ToolArgs.(map[string]interface{})

	lines := strings.Split(text, "\n")
	var answerBuilder strings.Builder
	answerActive := false

	extractContent := func(line string) string {
		if idx := strings.Index(line, ":"); idx >= 0 {
			return strings.TrimSpace(line[idx+1:])
		}
		parts := strings.Fields(line)
		if len(parts) > 1 {
			return strings.TrimSpace(strings.Join(parts[1:], " "))
		}
		return ""
	}

	appendAnswer := func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return
		}
		if answerBuilder.Len() > 0 {
			answerBuilder.WriteString("\n")
		}
		answerBuilder.WriteString(trimmed)
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if answerActive && answerBuilder.Len() > 0 {
				answerBuilder.WriteString("\n")
			}
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "THINK"):
			answerActive = false
			thought := extractContent(line)
			if thought == "" {
				thought = line
			}
			response.Progress = thought
			response.Reasoning = thought
			response.NextStep = thought
		case strings.HasPrefix(upper, "ACTION"):
			answerActive = false
			actionLine := extractContent(line)
			if actionLine == "" {
				actionLine = line
			}
			upperAction := strings.ToUpper(actionLine)
			if strings.HasPrefix(upperAction, "DONE") {
				response.Action = "Done"
				continue
			}
			fields := strings.Fields(actionLine)
			if len(fields) > 0 {
				if strings.EqualFold(fields[0], "TOOL") {
					if len(fields) > 1 {
						response.SelectedTool = fields[1]
					}
					if len(fields) > 2 {
						for _, arg := range fields[2:] {
							if strings.Contains(arg, "=") {
								kv := strings.SplitN(arg, "=", 2)
								if len(kv) == 2 {
									toolArgs[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
								}
							}
						}
					}
					response.Action = "Iterate"
					response.ToolRequired = true
					continue
				}
				// Allow syntax like ACTION: mai-mcp/tool arg=value
				response.SelectedTool = fields[0]
				for _, arg := range fields[1:] {
					if strings.Contains(arg, "=") {
						kv := strings.SplitN(arg, "=", 2)
						if len(kv) == 2 {
							toolArgs[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
						}
					}
				}
				response.Action = "Iterate"
				response.ToolRequired = true
				continue
			}
		case strings.HasPrefix(upper, "TOOL"):
			// Backward compatibility with TOOL: ... format
			answerActive = false
			toolLine := extractContent(line)
			if toolLine == "" {
				toolLine = strings.TrimSpace(strings.TrimPrefix(line, "TOOL"))
			}
			fields := strings.Fields(toolLine)
			if len(fields) > 0 {
				response.SelectedTool = fields[0]
				for _, arg := range fields[1:] {
					if strings.Contains(arg, "=") {
						kv := strings.SplitN(arg, "=", 2)
						if len(kv) == 2 {
							toolArgs[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
						}
					}
				}
				response.Action = "Iterate"
				response.ToolRequired = true
			}
		case strings.EqualFold(line, "DONE") || strings.HasPrefix(upper, "DONE"):
			answerActive = false
			response.Action = "Done"
		case strings.HasPrefix(upper, "ANSWER"):
			answerActive = true
			answer := extractContent(line)
			if answer != "" {
				appendAnswer(answer)
			}
		default:
			if answerActive {
				appendAnswer(line)
			} else if response.Reasoning == "" {
				response.Reasoning = line
			} else {
				response.Reasoning += "\n" + line
			}
		}
	}

	// Mark tool requirement if a tool was selected
	if response.SelectedTool != "" {
		response.ToolRequired = true
	}

	return response, strings.TrimSpace(answerBuilder.String()), nil
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
	if r.configOptions.GetBool("repl.debug") {
		art.DebugBanner("Stripped Response", responseText)
	}
	if responseText == "" {
		return PlanResponse{}, "", fmt.Errorf("cancel empty response from the llm")
	}
	// debug(responseText)
	response, explainText, err := parseMarkdownResponse(responseText)
	if err != nil {
		if r.configOptions.GetBool("repl.debug") {
			art.DebugBanner("Parse Error", err.Error())
		}
		return PlanResponse{}, "", err
	}
	if r.configOptions.GetBool("repl.debug") {
		debugInfo := fmt.Sprintf("Action: %s\nSelectedTool: %s\nToolArgs: %v\nPlan: %v\nPlanIndex: %d\nProgress: %s\nReasoning: %s\nNextStep: %s\nToolRequired: %t",
			response.Action, response.SelectedTool, response.ToolArgs, response.Plan, response.PlanIndex, response.Progress, response.Reasoning, response.NextStep, response.ToolRequired)
		art.DebugBanner("Parsed Response", debugInfo)
	}
	return response, explainText, nil
}

func (r *REPL) ReactText(messages []llm.Message, input string) (string, error) {
	if r.configOptions.GetBool("repl.debug") {
		art.DebugBanner("ReactText Start", fmt.Sprintf("Input: %s", input))
	}
	// TODO: Do something with the previous messages
	display := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.display")))
	if display == "" {
		display = "verbose"
	}
	showPlan := (display == "verbose" || display == "plan")
	toolPrompt := toolsPrompt
	// Apply reasoning level directive
	toolPrompt = adjustReasoningPrompt(toolPrompt, r.configOptions.Get("mcp.reason"))
	// Add custom prompt text if configured
	customPrompt := strings.TrimSpace(r.configOptions.Get("mcp.prompt"))
	if customPrompt != "" {
		toolPrompt += "\n\n" + customPrompt
	}

	toolList, err := GetAvailableTools(Quiet)
	if err != nil || strings.TrimSpace(toolList) == "" {
		if r.configOptions.GetBool("repl.debug") {
			art.DebugBanner("Tool List Warning", fmt.Sprintf("quiet mode failed: %v", err))
		}
		toolList, err = GetAvailableTools(Markdown)
		if err != nil {
			fmt.Println("Cannot retrieve tools, doing nothing")
			return input, nil
		}
	}
	lines := strings.Split(toolList, "\n")
	cleanLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		trim = strings.TrimPrefix(trim, "- ")
		trim = strings.TrimPrefix(trim, "* ")
		cleanLines = append(cleanLines, trim)
	}
	if len(cleanLines) == 0 {
		fmt.Println("No tools available, doing nothing")
		return input, nil
	}
	toolList = strings.Join(cleanLines, "\n")
	context := ""
	stepCount := 0
	reasoning := ""
	clearScreen := true
	for {
		stepCount++
		if r.configOptions.GetBool("repl.debug") {
			art.DebugBanner("React Loop Step", fmt.Sprintf("Step %d", stepCount))
		}
		step, expl, err := r.toolStep(toolPrompt, input, context, toolList)
		if err != nil {
			if r.configOptions.GetBool("repl.debug") {
				art.DebugBanner("ToolStep Error", err.Error())
			}
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
		action := strings.TrimSpace(step.Action)
		if action == "" && step.SelectedTool == "" && expl != "" {
			action = "Done"
		}
		finished := strings.EqualFold(action, "done") || strings.EqualFold(action, "solve")
		if finished {
			if r.configOptions.GetBool("repl.debug") {
				art.DebugBanner("Loop Exit", fmt.Sprintf("Action: %s", action))
			}
			if expl != "" {
				context = appendContextSection(context, formatContextTag("answer", expl))
				if display != "quiet" {
					fmt.Printf("\n%s\n", expl)
				}
			}
			fmt.Println("(tools) Problem solved")
			break
		}
		if action != "" && display != "quiet" {
			fmt.Println("Action: " + action)
		}
		/*
			if !step.ToolRequired {
				if expl != "" {
					reasoning += "\n\n## Reasoning\n\n" + expl
				}
				break
			}
		*/
		if step.SelectedTool == "" {
			if r.configOptions.GetBool("repl.debug") {
				art.DebugBanner("No Tool Selected", "Model must reply with ACTION: TOOL or ACTION: DONE")
			}
			input += "\nThe response must include `ACTION: TOOL <tool-name>` with arguments or `ACTION: DONE` when answering.\n"
			continue
		}
		toolName := strings.ReplaceAll(step.SelectedTool, ".", "/")
		tool := &Tool{
			Name: toolName,
			Args: mapToArray(step.ToolArgs.(map[string]interface{})),
		}
		if r.configOptions.GetBool("repl.debug") {
			art.DebugBanner("Tool Prepared", tool.ToString())
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
			if r.configOptions.GetBool("repl.debug") {
				art.DebugBanner("Tool Call Error", err.Error())
			}
			input += fmt.Sprintf("\nTool %s execution failed: %s\n\n", tool.ToString(), err.Error())
			continue
			// return "", err
		}
		if r.configOptions.GetBool("repl.debug") {
			art.DebugBanner("Tool Result", result)
		}
		/*
			fmt.Println ("<calltoolResult>")
			fmt.Println (result)
			fmt.Println ("</calltoolResult>")
		*/
		// results = append(results, result)
		// toolResponse := fmt.Sprintf("\n\n## Step %d Tool Response\n\n**Reasoning**: %s\n**Next Step**: %s\n**ToolName**: %s\n**Contents**: %s\n", stepCount, step.Reasoning, step.NextStep, tool.Name, result)
		reasonField := step.Reasoning
		if reasonField == "" {
			reasonField = step.Progress
		}
		toolResponse := formatToolContext(stepCount, reasonField, formatToolLine(tool), result)
		if expl != "" {
			context = appendContextSection(context, formatContextTag("observation", expl))
			if display != "quiet" && (display == "verbose" || display == "reason") {
				reasoning += "\n\n## Reasoning\n\n" + expl
			}
		}
		if step.Progress != "" {
			reasoning += "- " + step.Progress + "\n"
		}
		context = appendContextSection(context, toolResponse)
		// input += planString + toolResponse
		clearScreen = false
	}
	if reasoning != "" && display != "quiet" {
		reasoning = "<reasoning>\n" + reasoning + "</reasoning>\n"
	}
	if display != "quiet" {
		fmt.Println(strings.ReplaceAll(reasoning, "\n", "\r\n"))
	}
	if r.configOptions.GetBool("repl.debug") {
		art.DebugBanner("ReactText Return", fmt.Sprintf("Input: %s\nContext: %s", input, context))
	}
	return input + context, nil
	// return input + context + reasoning, nil
}
