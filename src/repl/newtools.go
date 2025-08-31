package main

import (
	"fmt"
	"strings"
	"encoding/json"
	"github.com/trufae/mai/src/repl/llm"
)
/*

## Response Format

Respond in JSON following the schema defined below:

```json
{
  "plan": [
    "Sequential, human-readable list of context-aware steps."
  ],
  "current_plan_index": 0,
  "progress": "Summary of what has been done so far or is in progress.",
  "reasoning": "Why this specific tool was chosen for the current step.",
  "next_step": "What should happen next.",
  "action": "Done | Solve | Think | Iterate | Error",
  "tool_required": true,
  "tool": "tool_name",
  "tool_params": {
    "param1": "value1",
    "param2": "value2"
  }
}
```
*/

const toolsPrompt = `
# Multi-Step Planning and Tool Execution

This is a multi-step planning and execution prompt designed to **efficiently** solve user queries using available tools. Your goal is to **create a simple plan before start executing any tools** and then go one by one executing every step in the process to reach the user goal.

## Reasoning

Do /nothink /no_think
Use Reasoning: low

## Overview

1. First, **analyze user's query** to understand the goals proposed.
2. **Plan everything first**. Don’t start until you’ve carefully thought through all the steps.
3. **Remember what you’ve done**. Avoid repeating the same steps. **Do not** overthink.
4. **Use the right tools**, automate and call the required tools instead of telling the user which actions take.
5. **Track progress clearly**. Each step should move the plan forward.

## Planning

Before executing any tools:

1. Analyze the user's query and understand the goal completely.
2. Break the problem into **a finite set of sequential steps** needed to reach the goal.
3. Choose the **most efficient path**, avoiding unnecessary or redundant actions.
4. Make sure each step is clear, distinct, and **only performed once**.

> 🔁 **Avoid loops:** If you find yourself proposing the same step again, stop and re-evaluate.  
> ❌ **No validation:** Once a tool is executed, do not re-check it unless new input justifies it

**IMPORTANT** Create a new plan if we find out new information that is relevant to solve the user request.

## Execution

When executing the plan:

1. **Follow your plan step-by-step**, executing one action at a time.
2. Maintain context: remember results from previous steps, including:
   - tool outputs
   - decisions made
   - paths avoided
3. Track progress accurately.
4. Update your plan only if **new, unforeseen information** is discovered.
5. Continue until the complete goal is achieved.

### Tool Usage

- Replace "tool_params" with the actual parameter names and values needed
- Only request to call a tool if it's necessary to fulfill the user's request
- Ensure all required parameters are correctly identified
- Do not use optional parameters unless necessary
- If multiple tools are needed, specify which one to use right now and which will come next

### Action Types

- Use "Action: Error" when the tool required to solve the step fails
- Use "Action: Done" all the steps are done, we can quit the loop
- Use "Action: Solve" only when the goal is completely solved
- Use "Action: Think" when reasoning is needed to plan new tool calls in another iteration
- Use "Action: Iterate" to continue executing tools to progress toward the solution

Based on these instructions, analyze the provided query and available tools to determine the appropriate course of action.

Below you will find the user prompt and the list of tools

----
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
func debug(m any) {
		fmt.Println("==========================")
		fmt.Println(m)
		fmt.Println("==========================")
}

func (r *REPL) newToolStep(toolPrompt string, input string, ctx string, toolList string) (PlanResponse, error) {
	query := buildMessageWithTools(toolPrompt, input, ctx, toolList)
	messages := []llm.Message{{Role: "user", Content: query}}
	responseJson, err := r.currentClient.SendMessage(messages, false, nil)
	if err != nil {
		return PlanResponse{}, fmt.Errorf("failed to get response for tools: %v", err)
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
func (r *REPL) QueryWithNewTools(messages []llm.Message, input string) (string, error) {
	origSchema := r.config.Schema
	defer func() {
		r.config.Schema = origSchema
	}()
	var schema map[string]interface{}
	fmt.Println("New tools")
	if err := json.Unmarshal([]byte(toolsSchema), &schema); err == nil {
		r.config.Schema = schema
	} else {
		return "", fmt.Errorf("cannot unmarshal the schema")
	}
	toolList, err := GetAvailableTools(Markdown)
	if err != nil {
		fmt.Println("Cannot retrieve tools, doing nothing")
		return input, nil
	}
	fmt.Println(toolList)
	fmt.Println("Result")
	var reasoning = ""
	var context = ""
	var stepCount = 0
	for {
		stepCount++
		step, err := r.newToolStep(toolsPrompt, input, context, toolList)
		if err != nil {
			fmt.Printf("## ERROR: toolStep: %s\r\n", err)
			if strings.Contains(err.Error(), "cancel") {
				break
			}
			input += fmt.Sprintf("\n[query error] %s. Try again with a new plan\n", err.Error())
			continue

		}
		fmt.Println(step)
		if step.Action == "Done" || step.Action == "Solve" {
			break
		}
		// ,"current_plan_index":0,"tool_required":true,"tool":"r2pm/openFile","tool_params":{"filePath":"/tmp/crackme0x05"},
		if step.ToolRequired {
			tool := &Tool{
				Name: step.SelectedTool,
				Args: mapToArray(step.ToolArgs),
			}
			debug(tool)
			result, err := callTool(tool)
			if err == nil {
				toolResponse := fmt.Sprintf("\n\n## Step %d Tool Response\n\n**Reasoning**: %s\n**ToolName**: %s\n**Contents**:\n\n```\n%s\n```\n\n", stepCount, step.NextStep, tool.Name, result)
				toolResponse += fmt.Sprintf("reason: %s\n", step.Reasoning)
			context += "\n\n## Tools Executed\n\n" + toolResponse
			} else {
				fmt.Println(err)
				break
			}
		}
		fmt.Println(step.Action)
		reasoning := step.Reasoning
		if reasoning != "" {
			context += "\n\n## Context\n\n" + reasoning
			reasoning += "\n\n## Reasoning\n\n" + reasoning
		}
	}
	if reasoning != "" {
		reasoning = "<reasoning>\n" + reasoning + "</reasoning>\n"
	}
	
	fmt.Println(strings.ReplaceAll(reasoning, "\n", "\r\n"))
	return input + context, nil
}
