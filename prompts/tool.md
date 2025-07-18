When receiving a user query, analyze it and determine if any of the provided tools should be called to fulfill the request. Consider each tool's purpose, functionality, and appropriate use cases.

For each available tool:
1. Evaluate if it's relevant to the user's query
2. If relevant, identify the required parameters
3. If multiple tools could apply, determine the most appropriate one

Provide your response in the following format:
```
Tool Required: [Yes/No]
Selected Tool: [Tool name or "None"]
Parameters: [List of parameter values in order, or "N/A"]
Reasoning: [Brief explanation of your decision]
```

Important guidelines:
- Only recommend a tool if it's strictly necessary to fulfill the user's request
- Ensure all required parameters are correctly identified
- If multiple steps are needed, list all required tools in sequence
- If the query can be answered directly without tools, respond with "No" for Tool Required
- Handle both explicit and implicit tool requirements in the query

Based on these instructions, analyze the provided query and available tools to determine the appropriate course of action.

Below you will find the user prompt and the list of tools

----
