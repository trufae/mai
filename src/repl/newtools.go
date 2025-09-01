package main

import (
	"encoding/json"
	"fmt"
	"github.com/trufae/mai/src/repl/llm"
	"strings"
)

const toolsPrompt = `
# System Prompt

You are a multi-step planning and execution agent designed to **efficiently** solve user requests using the provided tools catalog.

Your goal is to solve the problem proposed by writing down a plan which may change on every execution step if needed in order to reach the user goal.

## Rules

1. First, **analyze user's query** to understand the goals proposed.
2. **Plan everything first**. Don’t start until you’ve carefully thought the first steps.
3. **Remember what you’ve done**. Avoid repeating the same steps. **Do not** overthink.
4. **Use the right tools**, automate and call the required tools instead of telling the user which actions take.
5. **Track progress clearly**. Each step should move the plan forward.
6. /nothink /no_think Reasoning: low

## Planning

Start by carefully crafting a plan by collecting all the necessary information

1. Analyze the user's query and understand the goal completely.
2. Break the problem into **a finite set of sequential steps** needed to reach the goal.
3. Choose the **most efficient path**, avoiding unnecessary or redundant actions.
4. Make sure each step is clear, distinct, and **only performed once**.

> 🔁 **Avoid loops:** If you find yourself proposing the same step again, stop and re-evaluate.  
> ❌ **No validation:** Once a tool is executed, do not re-check it unless new input justifies it

**IMPORTANT** Create a new plan if we find out new information that is relevant to solve the user request.

## Execution

1. Follow your plan **step-by-step**, running only one action at a time.
2. Maintain context: remember results from previous steps, including:
   - tool outputs
   - decisions made
   - paths avoided
3. Track progress accurately.
4. Update your plan only if **new, unforeseen information** is discovered.
5. Continue until the complete goal is achieved.

### Tool Selection

1. Only call tools if necessary to fulfill the user's request
2. Fill "tool_params" with the right parameters and its values
3. Ensure all required parameters are correctly identified
4. Avoid using optional parameters unless necessary
5. When multiple tools are needed, redesign the plan to call then one after the other.

### Action Types

Based on these instructions, analyze the provided query and available tools to determine the appropriate course of action.

- Use "Action: Done" all the steps are done, we can quit the loop
- Use "Action: Solve" only when the goal is completely solved
- Use "Action: Iterate" to continue executing tools to progress toward the solution
- Use "Action: Think" when reasoning is needed to plan new tool calls in another iteration
- Use "Action: Error" when the tool required to solve the step fails

### Output Example

Provide an array of plans, specify the current plan index, the reasoning behind the current step, the action associated, what must be followup in the next step and if needed, the tool name and its parameters. Do not decorate the resulting JSON, not even using markdown code blocks, use plain json.

{
  "plan": [
    "Open the binary file '/tmp/crackme0x05' using radare2.",
    "Analyze the binary using radare2's analysis capabilities.",
    "List strings from data sections to find potential password candidates.",
    "If no clear password candidates are found in strings, examine functions for password checks using decompilation and cross-references.",
    "Test the identified password candidates."
  ],
  "current_plan_index": 0,
  "progress": "Summary of what has been done so far or is in progress.",
  "reasoning": "Why this specific tool was chosen for the current step.",
  "next_step": "What should happen next.",
  "action": "Done | Solve | Think | Iterate | Error",
  "tool_required": true,
  "tool": "openFile",
  "tool_params": {
    "filePath": "/tmp/crackme0x05",
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
      "enum": ["Done", "Solve", "Think", "Iterate", "Error"],
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
      "enum": ["Done", "Solve", "Think", "Iterate", "Error"],
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
          "key":   { "type": "string" },
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

func buildToolsMessage(toolPrompt string, userInput string, ctx string, toolList string) string {
	return fmt.Sprintf("<user-request>\n%s\n</user-request>\n<rules>%s</rules><context-history>%s</context-history>\n<tools-catalog>\n%s\n</tools-catalog>",
		userInput, toolPrompt, ctx, toolList)
}

func (r *REPL) newToolStep(toolPrompt string, input string, ctx string, toolList string) (PlanResponse, error) {
	query := buildToolsMessage(toolPrompt, input, ctx, toolList)
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

/*
func map2array(m map[string]interface{}) []string{
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
*/

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
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaString), &schema); err != nil {
		return "", fmt.Errorf("i cannot unmarshal the schema")
	}
	r.config.Schema = schema
	toolList, err := GetAvailableTools(Markdown)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	var reasoning = ""
	var context = ""
	var stepCount = 0
	for {
		stepCount++
		step, err := r.newToolStep(toolsPrompt, input, context, toolList)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			if strings.Contains(err.Error(), "failed") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue

		}
		fmt.Println("\x1b[0m" + step.Reasoning)
		if step.Action == "Done" || step.Action == "Solve" {
			break
		}
		if step.ToolRequired {
			tool := &Tool{
				Name: step.SelectedTool,
				Args: map2array(step.ToolArgs),
			}
			debug(tool)
			result, err := callTool(tool)
			if err == nil {
				context += fmt.Sprintf("\n\n## Step %d Tool Output\n\n**Reasoning**: %s\n**ToolName**: %s\n**Output**:\n\n```\n%s\n```\n\n", stepCount, step.Reasoning, tool.Name, result)
			} else {
				fmt.Println(err)
				break
			}
		}
		fmt.Println("Action: " + step.Action)
		showPlan(&step)
		if reasoning != "" {
			context += "\n\n## Context\n\n" + reasoning
		}
	}

	// fmt.Println(strings.ReplaceAll(reasoning, "\n", "\r\n"))
	return input + context, nil
}
