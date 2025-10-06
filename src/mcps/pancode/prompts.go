package main

import "mcplib"

// getSamplePrompts returns four sample prompts for the coding agent.
func getSamplePrompts() []mcplib.PromptDefinition {
	return []mcplib.PromptDefinition{
		{
			Name:        "refactor_file",
			Description: "Refactor a file to improve readability, structure, and idioms without changing behavior.",
			Arguments: []mcplib.PromptArgument{
				{Name: "file_path", Type: "string", Description: "Absolute path to the file to refactor.", Required: true},
				{Name: "goals", Type: "string", Description: "Specific refactoring goals or constraints.", Required: false},
			},
			Messages: []mcplib.Message{
				{
					Role:    "system",
					Content: []mcplib.Content{{Type: "text", Text: "You are a senior software engineer. Refactor code conservatively, preserving behavior and tests."}},
				},
				{
					Role:    "user",
					Content: []mcplib.Content{{Type: "text", Text: "Refactor the file at {{file_path}}. Goals: {{goals}}. Provide a unified diff and rationale."}},
				},
			},
		},
		{
			Name:        "write_tests",
			Description: "Write unit tests for a file/function using the specified framework.",
			Arguments: []mcplib.PromptArgument{
				{Name: "file_path", Type: "string", Description: "Target file to create tests for.", Required: true},
				{Name: "framework", Type: "string", Description: "Test framework to use (e.g., go test, jest, pytest).", Required: false},
				{Name: "focus", Type: "string", Description: "Functions or behaviors to prioritize.", Required: false},
			},
			Messages: []mcplib.Message{
				{
					Role:    "system",
					Content: []mcplib.Content{{Type: "text", Text: "You are meticulous at writing clear, isolated tests with meaningful names."}},
				},
				{
					Role:    "user",
					Content: []mcplib.Content{{Type: "text", Text: "Write tests for {{file_path}} using {{framework}}. Focus: {{focus}}. Return only new/modified files as a patch."}},
				},
			},
		},
		{
			Name:        "explain_code",
			Description: "Explain how a piece of code works and answer a question about it.",
			Arguments: []mcplib.PromptArgument{
				{Name: "file_path", Type: "string", Description: "File to explain.", Required: true},
				{Name: "question", Type: "string", Description: "Specific question to address.", Required: false},
			},
			Messages: []mcplib.Message{
				{
					Role:    "system",
					Content: []mcplib.Content{{Type: "text", Text: "Explain code accurately with concise examples."}},
				},
				{
					Role: "user",
					Content: []mcplib.Content{
						{
							Type: "text",
							Text: "Analyze the project structure at {{project_path}}. Follow this systematic approach with proper path handling:\n\n**CRITICAL PATH INSTRUCTIONS:**\n- Always check relative paths first: ./README.md, ./AGENTS.md, ./Makefile\n- Use Glob() to find files by pattern instead of assuming locations\n- Never use absolute paths like /README.md - use ./README.md\n- List current directory first to understand structure\n\n**Analysis Steps:**\n\n1. **Project Information & Layout:**\n   - Check if ./AGENTS.md exists first (use Glob(\"AGENTS.md\"))\n   - Read ./README.md for project overview\n   - List root directory to see project structure\n   - Use Glob patterns to find files by type (e.g., Glob(\"*.go\"))\n\n2. **Build System Detection:**\n   - Look for ./Makefile (Glob(\"Makefile\"))\n   - Search for build files: Glob(\"meson.build\"), Glob(\"package.json\")\n   - Check for Python: Glob(\"setup.py\"), Glob(\"pyproject.toml\")\n   - Examine discovered build files using Read()\n\n3. **Language & Framework Detection:**\n   - Use Glob to identify primary languages: *.go, *.py, *.js, *.ts, *.rs\n   - Search for dependency files: go.mod, Cargo.toml, package-lock.json\n   - Check for framework patterns with Grep tool\n\n4. **Testing & Workflow:**\n   - Find test files: Glob(\"*test*\"), Glob(\"**/test/**\")\n   - Check for CI config: Glob(\".github/workflows/*\")\n   - Look for Docker: Glob(\"Dockerfile*\"), Glob(\"docker-compose*\")\n\n5. **Documentation & Tools:**\n   - Recommend git grep for searching: git grep -n \"search term\"\n   - Suggest build commands based on found Makefile targets\n   - Point to specific test commands discovered\n\nReturn a comprehensive summary covering:\n- Project name from README\n- Primary language(s) with file evidence\n- Build system with working commands\n- Test framework and how to run tests\n- Development setup instructions",
						},
					},
				},
			},
		},
		{
			Name:        "fix_bug",
			Description: "Propose and implement a fix for a bug observed in logs or error output.",
			Arguments: []mcplib.PromptArgument{
				{Name: "error", Type: "string", Description: "Observed error message or log excerpt.", Required: true},
				{Name: "context", Type: "string", Description: "Additional context like steps to reproduce or file hints.", Required: false},
			},
			Messages: []mcplib.Message{
				{
					Role:    "system",
					Content: []mcplib.Content{{Type: "text", Text: "Diagnose the root cause before proposing changes. Prefer minimal diffs with strong rationale."}},
				},
				{
					Role:    "user",
					Content: []mcplib.Content{{Type: "text", Text: "Given the error: {{error}}. Context: {{context}}. Identify the root cause and provide a minimal patch."}},
				},
			},
		},
		{
			Name:        "analyze_project",
			Description: "Analyze project structure, build system, programming languages, and development workflow.",
			Arguments: []mcplib.PromptArgument{
				{Name: "project_path", Type: "string", Description: "Path to analyze (defaults to current working directory).", Required: false},
			},
			Messages: []mcplib.Message{
				{
					Role:    "system",
					Content: []mcplib.Content{{Type: "text", Text: "You are a project analysis expert. Systematically examine project structure, documentation, and build systems to provide comprehensive insights."}},
				},
				{
					Role: "user",
					Content: []mcplib.Content{
						{
							Type: "text",
							Text: "Analyze the project at {{project_path}}.\n\n- Read README.md and AGENTS.md for overview\n- Use Glob to find build files (Makefile, package.json, etc.)\n- Identify languages by file extensions (*.go, *.py, *.js, etc.)\n- Find test files, CI configs, and Docker files\n- Provide summary: project name, languages, build commands, tests, setup instructions",
						},
					},
				},
			},
		},
	}
}
