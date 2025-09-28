package main

import (
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/llm"
	"os"
	"regexp"
	"strings"
)

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

func (r *REPL) toolStep(toolPrompt string, input string, ctx string, toolList string) (PlanResponse, string, error) {
	query := buildMessageWithTools(toolPrompt, input, ctx, toolList)
	// debug(query)
	messages := []llm.Message{{Role: "user", Content: query}}
	// debug(query)
	responseText, err := r.currentClient.SendMessage(messages, false, nil)
	if err != nil {
		return PlanResponse{}, "", fmt.Errorf("failed to get response for tools: %v", err)
	}
	// debug(responseText)
	// strip out any internal reasoning between <think>...</think> before processing
	reThink := regexp.MustCompile(`(?s)\s*<think>.*?</think>\s*`)
	responseText = reThink.ReplaceAllString(responseText, "")
	if responseText == "" {
		return PlanResponse{}, "", fmt.Errorf("cancel empty response from the llm")
	}
	// debug(responseText)
	responseJson, explainText := extractJSONBlock(responseText)
	responseJson = stripJSONComments(responseJson)
	// debug(explainText)
	var response PlanResponse
	if responseJson != "" {
		err2 := json.Unmarshal([]byte(responseJson), &response)
		if err2 != nil {
			// debug(responseJson)
			if strings.Contains(responseJson, "\"action\": \"Done\"") {
				response = PlanResponse{
					NextStep: "Cannot recover from invalid json parsing",
					Action:   "Done",
				}
				explainText = responseJson
			}
		}
		// response.NextStep += "<think>" + explainText + "</think>"
		//	fmt.Println(response)
		//	fmt.Println(response.NextStep)
		return response, explainText, err2
	}
	return response, explainText, nil
}

func (r *REPL) ReactText(messages []llm.Message, input string) (string, error) {
	// TODO: Do something with the previous messages
	display := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.display")))
	if display == "" {
		display = "verbose"
	}
	showPlan := (display == "verbose" || display == "plan")
	toolPrompt, err := r.getToolPrompt("tool.md")
	if err != nil {
		// If can't get the tool prompt, return input unchanged
		return input, err
	}
	// Apply reasoning level directive
	toolPrompt = adjustReasoningPrompt(toolPrompt, r.configOptions.Get("mcp.reason"))

	toolList, err := GetAvailableTools(Markdown)
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
