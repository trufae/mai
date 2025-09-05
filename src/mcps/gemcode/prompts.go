package main

import "mcplib"

// getSamplePrompts returns four sample prompts for the coding agent.
func getSamplePrompts() []mcplib.PromptDefinition {
    return []mcplib.PromptDefinition{
        {
            Name:        "refactor_file",
            Description: "Refactor a file to improve readability, structure, and idioms without changing behavior.",
            Arguments: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "file_path": map[string]any{"type": "string", "description": "Absolute path to the file to refactor."},
                    "goals":     map[string]any{"type": "string", "description": "Specific refactoring goals or constraints."},
                },
                "required": []string{"file_path"},
            },
            Messages: []mcplib.Message{
                {
                    Role: "system",
                    Content: []mcplib.Content{{Type: "text", Text: "You are a senior software engineer. Refactor code conservatively, preserving behavior and tests."}},
                },
                {
                    Role: "user",
                    Content: []mcplib.Content{{Type: "text", Text: "Refactor the file at {{file_path}}. Goals: {{goals}}. Provide a unified diff and rationale."}},
                },
            },
        },
        {
            Name:        "write_tests",
            Description: "Write unit tests for a file/function using the specified framework.",
            Arguments: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "file_path": map[string]any{"type": "string", "description": "Target file to create tests for."},
                    "framework": map[string]any{"type": "string", "description": "Test framework to use (e.g., go test, jest, pytest)."},
                    "focus":     map[string]any{"type": "string", "description": "Functions or behaviors to prioritize."},
                },
                "required": []string{"file_path"},
            },
            Messages: []mcplib.Message{
                {
                    Role: "system",
                    Content: []mcplib.Content{{Type: "text", Text: "You are meticulous at writing clear, isolated tests with meaningful names."}},
                },
                {
                    Role: "user",
                    Content: []mcplib.Content{{Type: "text", Text: "Write tests for {{file_path}} using {{framework}}. Focus: {{focus}}. Return only new/modified files as a patch."}},
                },
            },
        },
        {
            Name:        "explain_code",
            Description: "Explain how a piece of code works and answer a question about it.",
            Arguments: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "file_path": map[string]any{"type": "string", "description": "File to explain."},
                    "question":  map[string]any{"type": "string", "description": "Specific question to address."},
                },
                "required": []string{"file_path"},
            },
            Messages: []mcplib.Message{
                {
                    Role: "system",
                    Content: []mcplib.Content{{Type: "text", Text: "Explain code accurately with concise examples."}},
                },
                {
                    Role: "user",
                    Content: []mcplib.Content{{Type: "text", Text: "Explain the code in {{file_path}}. Question: {{question}}."}},
                },
            },
        },
        {
            Name:        "fix_bug",
            Description: "Propose and implement a fix for a bug observed in logs or error output.",
            Arguments: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "error":   map[string]any{"type": "string", "description": "Observed error message or log excerpt."},
                    "context": map[string]any{"type": "string", "description": "Additional context like steps to reproduce or file hints."},
                },
                "required": []string{"error"},
            },
            Messages: []mcplib.Message{
                {
                    Role: "system",
                    Content: []mcplib.Content{{Type: "text", Text: "Diagnose the root cause before proposing changes. Prefer minimal diffs with strong rationale."}},
                },
                {
                    Role: "user",
                    Content: []mcplib.Content{{Type: "text", Text: "Given the error: {{error}}. Context: {{context}}. Identify the root cause and provide a minimal patch."}},
                },
            },
        },
    }
}

