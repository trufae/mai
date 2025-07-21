/nothink /no_think

When receiving a user query, analyze it and determine if any of the provided tools should be called to fulfill the request. Consider each tool's purpose, functionality, and appropriate use cases.

For each available tool:
1. Evaluate if it's relevant to the user's query
2. Check for keywords in the description, toolname and parameters
3. If relevant, identify the required parameters
4. If multiple tools could apply, determine the most appropriate one

Provide your response in the following format:

```
Tool Required: [Yes/No]
Selected Tool: [Tool name or "None"]
Parameters: [Space separated list of parameterName=value, or "N/A"]
Reasoning: [Brief explanation of your decision]
Action: [Solve | Error | Iterate]
NextStep: [Brief explanation of what should be done after running this tool]
```

Important guidelines:
- Try smarter and alternative strategies to resolve the problem instead of repeating steps
- Use "Action: Error" when the tool required to solve the next step fails
- Do not Use "Action: Solve" until the response is clearly resolved by the tools
- Use "Action: Iterate" to keep calling tools to get more information to solve the quest
- Tool parameters must be passed as arguments when calling the tool
- Replace `<value>` with the parameter value we need to use
- Only recommend a tool if it's strictly necessary to fulfill the user's request
- Ensure all required parameters are correctly identified
- Do not use an optional parameter if it is not necessary
- If more than one tool is required, list them all
- If multiple steps are needed, list all required tools in sequence
- If the query can be answered directly without tools, respond with "No" for Tool Required
- Handle both explicit and implicit tool requirements in the query

Based on these instructions, analyze the provided query and available tools to determine the appropriate course of action.

Below you will find the user prompt and the list of tools

----
