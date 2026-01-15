# MAI Skills Guide

## Overview

Skills are reusable, modular capabilities that extend MAI's knowledge. Each skill is a directory containing a `SKILL.md` file with instructions and supporting resources.

Skills are automatically loaded and included in the system prompt, allowing the AI to use them when relevant to your request.

## Quick Start

### 1. Create a Skill Directory

```bash
# Create a new skill directory
mkdir -p ~/.config/mai/skills/my-skill

# Create the SKILL.md file
cd ~/.config/mai/skills/my-skill
```

### 2. Write SKILL.md

Every skill needs a `SKILL.md` file with YAML frontmatter:

```yaml
---
name: my-skill
description: "What this skill does and when to use it"
category: productivity
---

# My Skill Title

Instructions and guidance for Claude to follow when using this skill...

## How to use

1. Step one
2. Step two
```

### 3. Test the Skill

In MAI REPL:

```
/skills list                    # See your new skill
/skills show my-skill           # View details
/skills search skill            # Search by keyword
```

## Metadata Fields

### Required Fields

| Field | Type | Notes |
|-------|------|-------|
| `name` | string | Lowercase letters, numbers, hyphens only. Max 64 chars. |
| `description` | string | What it does and when to use it. Max 1024 chars. |

### Optional Fields

| Field | Type | Notes |
|-------|------|-------|
| `category` | string | Group related skills (development, productivity, etc.) |
| `tags` | string | Comma-separated keywords for searching |
| `allowed-tools` | string | Comma-separated list of tools Claude can use |
| `context` | string | Use "fork" to run in isolated subagent context |
| `version` | string | Skill version for updates |

## Example SKILL.md Structure

```yaml
---
name: code-review
description: Review code changes for quality, security, and best practices
category: development
tags: code-quality, review, standards
allowed-tools: read, grep
---

# Code Review Skill

Instructions and workflow...

## When to use

List when this skill should be applied...

## Guidelines

Key guidelines for the AI to follow...

## Examples

Concrete examples of using this skill...
```

## File Organization

### Simple Skill (Single File)

```
~/.config/mai/skills/my-skill/
├── SKILL.md          # Main skill instructions
```

### Complex Skill (Multiple Files)

```
~/.config/mai/skills/my-skill/
├── SKILL.md          # Main instructions (references below files)
├── templates/        # Templates the AI can reference
│   ├── template1.txt
│   └── template2.txt
├── scripts/          # Executable scripts (Python, Bash, etc.)
│   └── helper.py     # Claude can execute these
└── docs/             # Additional documentation
    └── reference.md
```

### Progressive Disclosure

The `SKILL.md` is loaded into context when Claude decides to use the skill. Reference additional files from within:

```markdown
# Code Review Skill

[Full instructions here]

For detailed templates, see [templates/review-template.txt](templates/review-template.txt)
```

## Skill Examples in MAI

MAI comes with example skills in `doc/skills/`:

- **commitmsg**: Generate commit messages from git diffs
- **code-review**: Structured code review workflow
- **debugging**: Systematic debugging methodology
- **testing**: Comprehensive testing strategies
- **git-workflow**: Git branching and commit best practices
- **api-design**: RESTful API design principles

Read these to understand skill structure and style.

## Best Practices

### 1. Clear Descriptions

Good descriptions help Claude know when to use your skill:

```yaml
# Good - Specific about what and when
description: Review code changes for security, performance, and style issues

# Bad - Too vague
description: Helps with code
```

### 2. Structured Instructions

Use clear sections and formatting:

```markdown
## When to use this skill
- List of situations

## Guidelines
- Clear rules to follow

## Format
- Expected output format

## Examples
- Concrete examples
```

### 3. Keep Files Focused

Don't make one massive SKILL.md. Break into:
- Main instructions in SKILL.md
- Detailed docs in separate files
- Templates in separate files
- Executable code in scripts

### 4. Add Examples

Concrete examples are more helpful than abstract instructions:

```markdown
## Good Example
[Show actual input and output]

## Common Mistakes to Avoid
- Mistake 1
- Mistake 2
```

### 5. Reference Files Appropriately

If your skill references other files, use relative paths:

```markdown
See [templates/template.md](templates/template.md) for examples.
```

## Restricted Tool Access

Use `allowed-tools` to restrict what Claude can do with a skill:

```yaml
---
name: read-only-analyst
description: Analyze files without making changes
allowed-tools: read, grep
---
```

When this skill is active, Claude can only use the specified tools.

## Skill Discovery

Users can find skills using:

```
/skills list                    # List all skills
/skills search keyword          # Search by keyword
/skills show skill-name         # View specific skill
/skills dir                     # See skills directory path
```

## Creating Your Own Skills

### Template Skill

```yaml
---
name: my-workflow
description: "Structured workflow for [specific task]"
category: productivity
---

# My Workflow Skill

This skill provides a structured approach to [task].

## When to use this skill

- Situation 1
- Situation 2

## Workflow Steps

### Step 1: [Preparation]
[Instructions for step 1]

### Step 2: [Execution]
[Instructions for step 2]

### Step 3: [Verification]
[Instructions for step 3]

## Best Practices

- Practice 1
- Practice 2

## Common Pitfalls

- Pitfall 1
- Pitfall 2

## Examples

[Concrete examples of the workflow]
```

## Organizing Skills by Category

Skills are organized by `category` for easy discovery:

- `development`: Code-related tasks
- `productivity`: Workflow and process
- `documentation`: Writing and explaining
- `analysis`: Data and research
- `planning`: Strategy and design

## Skill Versioning

Track skill evolution:

```yaml
---
name: my-skill
version: "1.2.0"
---
```

When you update a skill:
1. Update the version number
2. Document changes at the top of the file
3. Keep the skill directory name the same

## Security Considerations

### Safe Skills

- Pure instructions and guidance
- Templates and examples
- Non-executable documentation

### Use Caution With

- Scripts that modify files
- Executable code
- Skills with elevated access

### Guidelines

1. Only use skills from trusted sources
2. Review script contents before using
3. Understand what tools a skill requests
4. Check for external network access
5. Audit before sharing with others

## Skill Lifecycle

### Creating
```bash
mkdir -p ~/.config/mai/skills/skill-name
# Create SKILL.md
```

### Testing
```
/skills list
/skills show skill-name
# Ask Claude something that should trigger the skill
```

### Updating
```bash
# Edit SKILL.md
# Update version number
/skills reload  # Reload in MAI
```

### Sharing
```bash
# Zip the skill directory
zip -r my-skill.zip my-skill/

# Share the zip file
# Others can extract to ~/.config/mai/skills/
```

### Removing
```bash
rm -rf ~/.config/mai/skills/skill-name
/skills reload
```

## Integration with Claude

Skills work best when they:

1. **Have clear triggers**: Good descriptions help Claude know when to use them
2. **Are self-contained**: Everything needed is in the directory
3. **Include examples**: Concrete examples beat abstract rules
4. **Have progressive disclosure**: Large skills reference separate files
5. **Are well-organized**: Clear sections and headings

## Performance Tips

- Keep SKILL.md file reasonable (< 10KB typically)
- Put large reference material in separate files
- Use executable scripts instead of reading large text
- Reference specific sections of files, not entire directories

## Troubleshooting

### Skill Not Triggering

The `description` field is how Claude decides to use your skill. Make sure it:
- Describes what the skill does (actions)
- Includes keywords users might use
- Is specific, not generic

Example:
```
# Good
description: Review pull requests for code quality, security, and style violations

# Bad
description: Helps with code
```

### Skill Not Loading

Check:
1. SKILL.md has required `name` and `description` fields
2. Skill name is lowercase alphanumeric + hyphens
3. Run `/skills reload` to refresh
4. Check `/skills dir` to see the skills directory

### Wrong Tool Access

If Claude can't use a tool you need, check:
1. Is the tool in the `allowed-tools` list?
2. Is the `allowed-tools` field correct?
3. Try removing `allowed-tools` to allow all tools

## Resources

- Skills directory: `/skills dir` in MAI
- Example skills: `doc/skills/` in MAI repository
- Claude skills docs: https://platform.claude.com/docs/agents-and-tools/agent-skills/overview

## Next Steps

1. Explore existing skills with `/skills list`
2. Read `doc/skills/` examples for inspiration
3. Create your first skill for a task you do regularly
4. Test with `/skills search` and `/skills show`
5. Share skills with your team
