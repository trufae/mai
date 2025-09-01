package main

import (
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/llm"
	"strings"
)

const toolsPrompt = `
# System Prompt

This a multi-step planning and execution agent designed to **efficiently** solve user requests using the provided tools.

## Instructions

- Create a plan to solve the user **request**.
  1. Analyze the user's query to understand the real goal.
  2. Split the problem into a sequence of steps.
  3. Choose the **most efficient path**, avoid unnecessary or redundant actions.
- Improve the plan steps on every iteration if necessary.
  1. Follow the plan **step-by-step**, run only one action at a time.
  2. Track progress accurately, including paths to avoid, tools executed and decisions taken
  3. Update your plan only if **new, unforeseen information** is discovered.
  4. Continue until the complete goal is achieved.
- Before reaching the Solve state, use tools instead of instructing the user with manual actions.
  1. Track progress, each step should move the plan forward.
  2. Analyze the result of each tool to determine extra steps to perform.
  3. Collect information needed to provide the most precise response possible.

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

func debug(m any) {
	return
	fmt.Println("==========================")
	fmt.Println(m)
	fmt.Println("==========================")
}

func buildToolsMessage(toolPrompt string, userInput string, ctx string, toolList string, chatHistory string) string {
	return fmt.Sprintf("<user-request>\n%s\n</user-request>\n<rules>%s</rules><context>%s</context>\n<tools-catalog>\n%s\n</tools-catalog><history>%s</history>",
		userInput, toolPrompt, ctx, toolList, chatHistory)
}

func (r *REPL) newToolStep(toolPrompt string, input string, ctx string, toolList string, chatHistory string) (PlanResponse, error) {
	query := buildToolsMessage(toolPrompt, input, ctx, toolList, chatHistory)
	messages := []llm.Message{{Role: "user", Content: query}}
	responseJson, err := r.currentClient.SendMessage(messages, false, nil)
	if err != nil {
		return PlanResponse{}, fmt.Errorf("failed to get response for tools: %v", err)
	}
	if strings.HasPrefix(responseJson, "```") {
		res, _ := extractJSONBlock(responseJson)
		responseJson = res
	}
	var response PlanResponse
	fmt.Println(responseJson)
	debug(responseJson)
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

func (r *REPL) QueryWithNewTools(messages []llm.Message, input string) (string, error) {
	origSchema := r.config.Schema
	defer func() {
		r.config.Schema = origSchema
	}()
	schemaString := func(s string) string {
		if s == "gemini" {
			return geminiToolsSchema
		}
		return toolsSchema
	}(r.configOptions.Get("provider"))

	chatHistory := buildChatHistory(input, messages)

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaString), &schema); err != nil {
		return "", fmt.Errorf("I cannot unmarshal the schema")
	}
	r.config.Schema = schema
	toolList, err := GetAvailableTools(Markdown)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	var context = ""
	var progress = ""
	var stepCount = 0
	for {
		stepCount++
		step, err := r.newToolStep(toolsPrompt, input, context, toolList, chatHistory)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			if strings.Contains(err.Error(), "failed") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue

		}
		showPlan(&step)
		progress = step.Progress
		fmt.Println("Action: " + step.Action)
		fmt.Println("\x1b[0m[PROGRESS]" + step.Progress)
		fmt.Println("\x1b[0m[REASON]" + step.Reasoning)
		if (step.Action == "Done" || step.Action == "") || !step.ToolRequired {
			//	context += "Progres: " + step.Progress
			context += "Reasoning: " + step.Reasoning
			break
		}
		tool := &Tool{
			Name: step.SelectedTool,
			Args: map2array(step.ToolArgs),
		}
		debug(tool)
		result, err := callTool(tool)
		if err == nil {
			context += fmt.Sprintf("\n\n## Step %d Tool Output\n\n**Progress**: %s\n**Reasoning**: %s\n**ToolName**: %s\n**Output**:\n\n```\n%s\n```\n\n", stepCount, step.Progress, step.Reasoning, tool.Name, result)
			// context += "## Action Done\n" + step.NextStep
		} else {
			fmt.Println(err)
			break
		}
	}

	return input + context + progress, nil
	// return input + context + "## Resolution Rule\n\nBe concise in your response, be clear and do not ellaborate", nil
}
