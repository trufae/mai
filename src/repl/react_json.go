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
		return toolsPromptPrefixLow
	case "medium":
		return toolsPromptPrefixMedium
	case "high":
		return toolsPromptPrefixHigh
	default:
		return toolsPromptPrefixLow
	}
}

// 3. Update your plan only if **new, unforeseen information** is discovered.
// 1. Track progress, each step should move the plan forward.
const toolsPromptPrefixLow = `
# Direct Tool Usage

Use tools directly and efficiently to solve the user's request. Minimize planning overhead.
`
const toolsPromptPrefixMedium = `
# System Prompt

This is a planning agent designed to solve user requests using tools efficiently.

## Instructions

- Analyze the query to understand the goal.
- Break down into key steps if complex.
- Choose efficient paths, avoid redundancy.
- Execute one action at a time.
- Track progress and adjust as needed.
- Use tools instead of manual instructions.
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

### Output Rules

Based on these instructions, determine the action for the current step inside the plan.

- Use "action": "Done" all the steps are done, we can quit the loop
- Use "action": "Iterate" to continue executing tools to progress toward the solution
- Use "action": "Error" when the tool required to solve the step fails

Provide an array of plans, specify the current plan index, the reasoning behind the current step, the action associated, what must be followup in the next step and if needed, the tool name and its parameters. Do not decorate the resulting JSON, not even using markdown code blocks, use plain json.

{
  "plan": [
    "..."
  ],
  "current_plan_index": 0,
  "progress": "Summary of what has been done so far or is in progress.",
  "reasoning": "Why are we performing this step, which other actions must be taken later.",
  "next_step": "Follow up of what should happen next.",
  "action": "Done | Iterate | Error",
  "tool_required": true,
  "tool": "ToolName",
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
      "description": "A list of context-aware steps in sequential, human-readable format."
    },
    "current_plan_index": {
      "type": "integer",
      "minimum": 0,
      "description": "The index of the current step in the plan."
    },
    "progress": {
      "type": "string",
      "description": "A summary of what has been done so far or is in progress."
    },
    "reasoning": {
      "type": "string",
      "description": "Explanation of why a specific tool was chosen for the current step."
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
    "tool_required": {
      "type": "boolean",
      "description": "Indicates if a tool is required for the current step."
    },
    "tool": {
      "type": "string",
      "description": "The name of the tool required."
    },
    "tool_params": {
      "type": "object",
      "additionalProperties": {
        "type": "string"
      },
      "description": "Parameters required by the tool."
    }
  },
  "required": [
    "plan",
    "current_plan_index",
    "progress",
    "reasoning",
    "next_step",
    "action",
    "tool_required",
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
      "description": "A list of context-aware steps in sequential, human-readable format."
    },
    "current_plan_index": {
      "type": "integer",
      "minimum": 0,
      "description": "The index of the current step in the plan."
    },
    "progress": {
      "type": "string",
      "description": "A summary of what has been done so far or is in progress."
    },
    "reasoning": {
      "type": "string",
      "description": "Explanation of why a specific tool was chosen for the current step."
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
    "tool_required": {
      "type": "boolean",
      "description": "Indicates if a tool is required for the current step."
    },
    "tool": {
      "type": "string",
      "description": "The name of the tool required."
    },
    "tool_params": {
      "type": "array",
      "description": "Parameters required by the tool as key/value pairs.",
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
    "tool_required",
    "tool",
    "tool_params"
  ]
}
`

func buildToolsMessage(toolPrompt string, userInput string, ctx string, toolList string, chatHistory string) string {
	return fmt.Sprintf("<user-request>\n%s\n</user-request>\n<rules>%s</rules><context>%s</context>\n<tools-catalog>\n%s\n</tools-catalog><history>%s</history>",
		userInput, toolPrompt, ctx, toolList, chatHistory)
}

func (r *REPL) newToolStep(toolPrompt string, input string, ctx string, toolList string, chatHistory string) (PlanResponse, error) {
	query := buildToolsMessage(toolPrompt, input, ctx, toolList, chatHistory)
	messages := []llm.Message{{Role: "user", Content: query}}

	// Debug output: show the reasoning prompt sent to LLM
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("MCP Reasoning Prompt", query)
	}

	responseJson, err := r.currentClient.SendMessage(messages, false, nil)
	if err != nil {
		return PlanResponse{}, fmt.Errorf("failed to get response for tools: %v", err)
	}

	// Debug output: show the raw response from LLM
	if r.configOptions.GetBool("mcp.debug") {
		art.DebugBanner("MCP Raw Response", responseJson)
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

// AITODO: rename to ReactJson()
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
	r.currentClient, _ = llm.NewLLMClient(r.buildLLMConfig())
	toolList, err := GetAvailableTools(XML) // Markdown)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	if strings.TrimSpace(toolList) == "" {
		fmt.Println("No tools available, doing nothing")
		return input, nil
	}
	var context = ""
	var progress = ""
	var stepCount = 0
	for {
		stepCount++
		// Build the dynamic tools prompt with optional plan template
		reasonLevel := strings.ToLower(strings.TrimSpace(r.configOptions.Get("mcp.reason")))
		if reasonLevel != "low" && reasonLevel != "medium" && reasonLevel != "high" {
			reasonLevel = "low"
		}
		customPrompt := strings.TrimSpace(r.configOptions.Get("mcp.prompt"))
		dynamicToolsPrompt := r.toolsPromptPrefix() + planTemplate
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
			if strings.Contains(err.Error(), "failed") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue

		}
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
		if (step.Action == "Done" || step.Action == "") || !step.ToolRequired {
			//	context += "Progres: " + step.Progress
			context += "Reasoning: " + step.Reasoning
			break
		}
		timeout, err := r.configOptions.GetNumber("mcp.timeout")
		if err != nil || timeout <= 0 {
			timeout = 60
		}
		result, err := callTool(tool, r.configOptions.GetBool("mcp.debug"), int(timeout))
		if err != nil {
			fmt.Println(err)
			// break
		} else {
			msg := fmt.Sprintf("\n\n## Step %d Tool '%s'\n\n%s\n<output>\n%s\n</output>\n", stepCount, tool.ToString(), step.Reasoning, result)
			/*
				fmt.Println("-----------")
				fmt.Println(msg)
				fmt.Println("-----------")
			*/
			context += msg
			// context += "## Action Done\n" + step.NextStep
		}
	}
	if display != "quiet" {
		fmt.Println("\x1b[33m" + FillLineWithTriangles() + "\x1b[0m")
	}

	return input + context + progress + "\n## Resolution Instructions\n\nBe concise in your response", nil
}
