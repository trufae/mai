package main

import (
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/art"
	"github.com/trufae/mai/src/repl/llm"
	"golang.org/x/term"
	"os"
	"strings"
)

func (r *REPL) toolsPromptPrefix() string {
	reason := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.reason")))
	switch reason {
	case "low":
		// Rewritten based on high
		return toolsPromptPrefixLow
	case "medium":
		// Rewritten based on high
		return toolsPromptPrefixMedium
	case "high":
		// OK
		return toolsPromptPrefixHigh
	default:
		return toolsPromptPrefixLow
	}
}

// 3. Update your plan only if **new, unforeseen information** is discovered.
// 1. Track progress, each step should move the plan forward.
const toolsPromptPrefixLow = `
# System Prompt

This is a simple planning and execution agent designed to solve user requests using the provided tools.

## Instructions

- Create a basic plan to solve the user **request**.
  1. Understand what the user wants.
  2. Break down into steps that may require tools.
  3. Choose simple, direct steps using available tools.
  4. Perform actions once, do not reopen files.
- Follow the plan **step-by-step**, run only one action at a time.
  1. Update the plan outline on every iteration with tool results.
  2. Advance the plan index after each successful tool execution.
  3. Continue until the complete goal is achieved.
- Use tools to gather information instead of guessing.
  1. Call tools from the catalog when you need external data.
  2. Use context information when available.
- Before reaching the "Solve" state
  1. Use tools instead of asking the user for information.
  2. Use tools for any file operations or external data access.
  3. Analyze tool results to determine what to do next.
  4. Do not leave gaps in the plan, use tools to fill them.
`

const toolsPromptPrefixMedium = `
# System Prompt

This is a planning and execution agent designed to solve user requests using the provided tools with moderate reasoning.

## Instructions

- Create a plan to solve the user **request**.
  1. Analyze the query to understand the goal.
  2. Break down into key steps if needed.
  3. Choose efficient paths, avoid redundancy.
  4. Use available context information.
  5. Perform actions once, do not reopen files.
- Follow the plan **step-by-step**, run only one action at a time.
  1. Update the plan outline on every iteration with tool results.
  2. Track progress accurately and advance the plan index after successful tool execution.
  3. Continue until the complete goal is achieved.
- Use tools appropriately.
  1. Prefer tools over manual instructions.
  2. Analyze results to determine next steps.
- Before reaching the "Solve" state
  1. Use tools instead of educating the user with manual actions.
  2. Analyze tool results to determine extra steps to perform.
  3. Do not leave information gaps in the plan, add the plan steps necessary.
`
const toolsPromptPrefixHigh = `
# System Prompt

This a multi-step planning and execution agent designed to **efficiently** solve user requests using the provided tools.

## Instructions

- Create a plan to solve the user **request**.
  1. Analyze the query to understand the real goal.
  2. Break down the problem into a sequence of steps.
  3. Choose the **most efficient path**.
  4. Avoid unnecessary or redundant actions.
  5. Perform actions once, do not reopen files.
- Follow the plan **step-by-step**, run only one action at a time.
  1. Update the plan outline on every iteration with tool results.
  2. Track progress accurately, what to avoid, tools executed and decisions taken.
  3. Continue until the complete goal is achieved.
- Before reaching the "Solve" state
  1. Use tools instead of educating the user with manual actions.
  2. Analyze tool results to determine extra steps to perform.
  3. Do not leave information gaps in the plan, add the plan steps necessary.

`

const toolsPromptSuffix = `
### Output Format

Based on these instructions, determine the "action" for the current step inside the plan.

- "Iterate" selected tool needs to be called and redefine the plan if necessary to progress towards the solution.
- "Error" something wrong happened and we cannot resolve the user request.
- "Done" do not call any tool, we have all the context information to resolve user-request.

Provide an array of plans, specify the current plan index, the reasoning behind the current step, the action associated, what must be followup in the next step and if needed, the tool name and its parameters. Do not decorate the resulting JSON, not even using markdown code blocks, use plain json.

{
  "plan": [
    "..."
  ],
  "current_plan_index": 0,
  "progress": "Summary of the progress towards the plan",
  "reasoning": "Explain why we need to use a tool",
  "next_step": "Expected follow up action after calling the tool",
  "action": "Done | Iterate | Error",
  "tool": "RequiredToolNameToCall",
  "tool_params": {
    "parameterName": "parameterValue"
  }
}
`

const toolsSchema = `
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "plan": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "A list of context-aware steps in sequential, human-readable format"
    },
    "current_plan_index": {
      "type": "integer",
      "minimum": 0,
      "description": "The index of the current step in the plan"
    },
    "progress": {
      "type": "string",
      "description": "A summary of what has been done so far or is in progress"
    },
    "reasoning": {
      "type": "string",
      "description": "Explanation of why a specific tool was chosen for the current step"
    },
    "next_step": {
      "type": "string",
      "description": "Description of what should happen next"
    },
    "action": {
      "type": "string",
      "enum": ["Done", "Iterate", "Error"],
      "description": "The current action status"
    },
    "tool": {
      "type": "string",
      "description": "The name of the tool required"
    },
    "tool_params": {
      "type": "object",
      "additionalProperties": {
        "type": "string"
      },
      "description": "Parameters required by the tool"
    }
  },
  "required": [
    "plan",
    "current_plan_index",
    "progress",
    "reasoning",
    "next_step",
    "action",
    "tool",
    "tool_params"
  ],
  "additionalProperties": false
}
`

const geminiToolsSchema = `
{
  "type": "object",
  "properties": {
    "plan": {
      "type": "array",
      "items": { "type": "string" },
      "description": "A list of context-aware steps in sequential, human-readable format"
    },
    "current_plan_index": {
      "type": "integer",
      "minimum": 0,
      "description": "The index of the current step in the plan"
    },
    "progress": {
      "type": "string",
      "description": "A summary of what has been done so far or is in progress"
    },
    "reasoning": {
      "type": "string",
      "description": "Explanation of why a specific tool was chosen for the current step"
    },
    "next_step": {
      "type": "string",
      "description": "Description of what should happen next."
    },
    "action": {
      "type": "string",
      "enum": ["Done", "Iterate", "Error"],
      "description": "The current action status."
    },
    "tool": {
      "type": "string",
      "description": "The name of the tool required (optional)"
    },
    "tool_params": {
      "type": "array",
      "description": "Parameters required by the tool as key/value pairs",
      "items": {
        "type": "object",
        "properties": {
          "key": { "type": "string" },
          "value": { "type": "string" }
        },
        "required": ["key", "value"]
      }
    }
  },
  "required": [
    "plan",
    "current_plan_index",
    "progress",
    "reasoning",
    "next_step",
    "action",
    "tool",
    "tool_params"
  ]
}
`

func buildToolsMessage(toolPrompt string, userInput string, ctx string, toolList string, chatHistory string) string {
	tools := fmt.Sprintf("%s\n<tools-catalog>%s</tools-catalog>\n", toolPrompt, toolList)
	query := fmt.Sprintf("<user-request>%s</user-request>\n<context>%s</context>\n</context>", chatHistory, ctx)
	return query + tools // tools + query
	/*
		return fmt.Sprintf("<user-request>\n%s\n</user-request>\n<context>%s<conversation-log>%s</conversation></context>\n</input>\n<tools>%s<catalog>%s</catalog></tools>", userInput, ctx, chatHistory, toolPrompt, toolList)
		return fmt.Sprintf("<user-request>\n%s\n</user-request>\n<tool-selection-prompt>%s</tool-selection-prompt><context>%s</context>\n<tools-catalog>\n%s\n</tools-catalog><conversation-log>%s</conversation-log>",
			userInput, toolPrompt, ctx, toolList, chatHistory)
	*/
}

func (r *REPL) newToolStep(toolPrompt string, input string, ctx string, toolList string, chatHistory string) (PlanResponse, error) {
	query := buildToolsMessage(toolPrompt, input, ctx, toolList, chatHistory)
	messages := []llm.Message{{Role: "user", Content: query}}

	// Debug output: show the reasoning prompt sent to LLM
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("newToolStep Query", query)
	}
	responseJson, err := r.currentClient.SendMessage(messages, false, nil)
	if err != nil {
		return PlanResponse{}, fmt.Errorf("failed to get response for tools: %v", err)
	}
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("newToolStep Response", responseJson)
	}

	// Debug output: show the raw response from LLM
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("MCP Tool Response", responseJson)
	}

	if strings.HasPrefix(responseJson, "```") {
		res, _ := extractJSONBlock(responseJson)
		responseJson = res
	}
	if strings.HasPrefix(responseJson, "<|") {
		pos := strings.LastIndex(responseJson, "|>")
		if pos != -1 {
			responseJson = responseJson[pos+2:]
			fmt.Println("Responded JSON:\n" + responseJson)
		} else {
			fmt.Println("Invalid JSON:\n" + responseJson)
		}
	}
	var response PlanResponse
	if responseJson != "" {
		err2 := json.Unmarshal([]byte(responseJson), &response)
		if err2 != nil {
			return PlanResponse{}, err2
		}
	}
	return response, nil
}

func showPlan(step *PlanResponse) {
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
}

func map2array(m interface{}) []string {
	result := []string{}

	switch val := m.(type) {
	case map[string]interface{}:
		// Case 1: plain map
		result = make([]string, 0, len(val))
		for k, v := range val {
			result = append(result, fmt.Sprintf("%s=%v", k, v))
		}

	case []interface{}:
		// Case 2: array of {key, value} maps
		for _, item := range val {
			if kv, ok := item.(map[string]interface{}); ok {
				k, _ := kv["key"].(string)
				v := kv["value"]
				result = append(result, fmt.Sprintf("%s=%v", k, v))
			}
		}
	}
	return result
}

func buildChatHistory(input string, messages []llm.Message) string {
	var b strings.Builder
	for _, m := range messages {
		role := strings.ToLower(m.Role)
		if role == "assistant" || role == "model" || role == "ai" {
			var content string
			switch c := m.Content.(type) {
			case string:
				content = c
			default:
				content = fmt.Sprintf("%v", c)
			}
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			b.WriteString(content)
		}
	}
	bs := b.String()
	if bs == "" {
		return "<user>" + input + "</user>"
	}
	return "<user>" + input + "</user>\n<assistant>" + b.String() + "</assistant>"
}
func FillLineWithTriangles() string {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		width = 80 // fallback if we can't get size
	}
	width *= 2

	// Each "◤◢" takes 2 runes (width = 2)
	pattern := "◤◢"
	result := ""

	for len(result) < width {
		result += pattern
	}
	return result
}

func (r *REPL) getReasoningLevel() string {
	reasonLevel := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.reason")))
	if reasonLevel != "low" && reasonLevel != "medium" && reasonLevel != "high" {
		return "low"
	}
	return reasonLevel
}

func (r *REPL) ReactJson(messages []llm.Message, input string) (string, error) {
	var planTemplate = ""
	display := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.display")))
	if display == "" {
		display = "verbose"
	}
	// If enabled, query MCP prompts to choose a plan template before running the tool loop
	if r.configOptions.GetBool("mcp.prompts") {
		planTemplate, err := r.prepareMCPromptTemplate(input, messages)
		if err != nil {
			// Non-fatal: proceed without a template if selection fails
			fmt.Fprintf(os.Stderr, "mcpprompts: %v\n", err)
		} else if planTemplate != "" {
			fmt.Println("\x1b[0m 📝| Using a prompt template for the given task")
			fmt.Println(planTemplate)
		}
	}
	// Temporarily override schema via options; providers read it from buildLLMConfig
	origSchema := r.configOptions.Get("llm.schema")
	defer func() {
		_ = r.configOptions.Set("llm.schema", origSchema)
	}()
	schemaString := func(s string) string {
		if s == "gemini" {
			return geminiToolsSchema
		}
		return toolsSchema
	}(r.configOptions.Get("ai.provider"))

	chatHistory := buildChatHistory(input, messages)

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaString), &schema); err != nil {
		return "", fmt.Errorf("I cannot unmarshal the schema")
	}
	// Store inline schema JSON in options for providers to consume
	_ = r.configOptions.Set("llm.schema", schemaString)
	// Recreate client with the new schema
	r.currentClient, _ = llm.NewLLMClient(r.buildLLMConfig(), r.ctx)
	// toolList, err := GetAvailableToolsWithConfig(r.configOptions, Simple)
	toolList, err := GetAvailableToolsWithConfig(r.configOptions, Quiet)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	if strings.TrimSpace(toolList) == "" {
		fmt.Println("No tools available, doing nothing")
		return input, nil
	}
	// Build the dynamic tools prompt with optional plan template
	// AITODO: move this logic into a separate local function
	reasonLevel := r.getReasoningLevel()
	var context = ""
	var progress = ""
	var stepCount = 0
	var currentPlan []string
	for {
		stepCount++
		customPrompt := strings.TrimSpace(r.configOptions.Get("mcp.prompt"))
		dynamicToolsPrompt := r.toolsPromptPrefix() + planTemplate
		if len(currentPlan) > 0 {
			dynamicToolsPrompt += "\n\nCurrent Plan:\n" + strings.Join(currentPlan, "\n") + "\n"
		}
		if customPrompt != "" {
			dynamicToolsPrompt += "\n\n" + customPrompt
		}
		dynamicToolsPrompt += "\n\nUse Reasoning: " + reasonLevel + "\n" + toolsPromptSuffix
		if display != "quiet" {
			fmt.Println("\x1b[0m 🐾| ...")
		}
		step, err := r.newToolStep(dynamicToolsPrompt, input, context, toolList, chatHistory)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			if strings.Contains(err.Error(), "fallback to non-grammar mode") {
				return "", fmt.Errorf("fallback to non-grammar mode")
			}
			if strings.Contains(err.Error(), "failed") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue

		}
		currentPlan = step.Plan
		if display != "quiet" && (display == "verbose" || display == "plan") {
			showPlan(&step)
		}
		progress = step.Progress
		tool := &Tool{
			Name: step.SelectedTool,
			Args: map2array(step.ToolArgs),
		}
		if display != "quiet" {
			fmt.Println("\x1b[0m 🚀| " + step.Action + " | 🛠️ " + tool.ToString())
			if display == "verbose" || display == "progress" {
				fmt.Println("\x1b[0m ✅| " + step.Progress)
			}
			if display == "verbose" || display == "reason" {
				fmt.Println("\x1b[0m 🤔| " + step.Reasoning)
			}
		}
		if tool.Name == "" {
			if (step.Action == "Done" || step.Action == "") || !step.ToolRequired {
				//	context += "Progres: " + step.Progress
				context += "Reasoning: " + step.Reasoning
				break
			}
		}
		timeout, err := r.configOptions.GetNumber("mcp.timeout")
		if err != nil || timeout <= 0 {
			timeout = 60
		}
		toolDebug := r.configOptions.GetBool("mcp.debug")
		toolFormat := r.configOptions.Get("mcp.toolformat")
		result, err := callTool(tool, toolDebug, toolFormat, int(timeout))
		if err != nil {
			fmt.Println(err)
			// Update chat history with failed tool call
			chatHistory += fmt.Sprintf("\n<tool_call>%s</tool_call>\n<tool_error>%s</tool_error>", tool.ToString(), err.Error())
			// Add error to context so the model can learn from it
			context += fmt.Sprintf("\n\n## Tool Error\n\nTool %s execution failed: %s\n\nPlease try a different approach or tool.\n", tool.ToString(), err.Error())
			continue
		}
		// msg := fmt.Sprintf("\n\n## Step %d Tool '%s'\n\n%s\n<output>\n%s\n</output>\n", stepCount, tool.ToString(), step.Reasoning, result)
		msg := fmt.Sprintf("\n\n<tool-call>Step %d<tool-name>%s</tool-name>\n%s\n<output>\n%s\n</output></tool-call>\n", stepCount, tool.ToString(), step.Reasoning, result)
		context += msg
		// If the tool response contains pagination hints, add an explicit tag so the model can request more
		if idx := strings.Index(result, "Pages left:"); idx != -1 {
			// Extract the rest of the line
			rest := result[idx:]
			parts := strings.Fields(rest)
			pagesLeft := ""
			if len(parts) >= 3 {
				pagesLeft = parts[2]
			}
			// Look for next_page_token inside parentheses
			nextTok := ""
			if tokIdx := strings.Index(rest, "next_page_token:"); tokIdx != -1 {
				// token follows
				tokStart := tokIdx + len("next_page_token:")
				tokStr := strings.TrimSpace(rest[tokStart:])
				// trim trailing ')' or '\n'
				tokStr = strings.Trim(tokStr, " )\n\r")
				nextTok = tokStr
			}
			context += fmt.Sprintf("\n<pagination pages_left=%s next_page_token=\"%s\" />\n", pagesLeft, nextTok)
		}
		// Update chat history with tool call and result to maintain context across iterations
		chatHistory += fmt.Sprintf("\n<tool_call>%s</tool_call>\n<tool_result>%s</tool_result>", tool.ToString(), result)
		// context += "## Action Done\n" + step.NextStep
		if display == "verbose" {
			art.DebugBanner("TOOL RESPONSE", result)
		}
	}
	if display != "quiet" {
		fmt.Println("\x1b[33m" + FillLineWithTriangles() + "\x1b[0m")
	}

	if context != "" {
		return context + progress, nil //  + "\n## Resolution Instructions\n\nBe concise in your response", nil
	}
	return input, nil
}
