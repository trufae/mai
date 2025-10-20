#!/bin/bash

# Commit Message Generator
# Analyzes git diff and generates a concise commit message

set -e

# Check if we're in a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo "Error: Not in a git repository" >&2
    exit 1
fi

# Get the diff to analyze
# First try staged changes, then working directory changes
if git diff --cached --quiet; then
    # No staged changes, check working directory
    if git diff --quiet; then
        echo "Error: No changes to commit (no staged or working directory changes)" >&2
        exit 1
    fi
    # Use working directory changes
    DIFF=$(git diff --no-color --no-ext-diff)
else
    # Use staged changes
    DIFF=$(git diff --cached --no-color --no-ext-diff)
fi

# If diff is empty, exit
if [ -z "$DIFF" ]; then
    echo "Error: No changes detected" >&2
    exit 1
fi

# Analyze the diff to determine commit type and scope
analyze_diff() {
    local diff="$1"

    # Check for specific file types and patterns
    local has_tests=$(echo "$diff" | grep -E '\.(test|spec)\.' | wc -l)
    local has_docs=$(echo "$diff" | grep -E '\.(md|txt|rst|adoc)$' | grep -v 'SKILL.md' | wc -l)
    local has_config=$(echo "$diff" | grep -E '(config|settings|env|yml|yaml|json|toml|ini)$' | wc -l)
    local has_build=$(echo "$diff" | grep -E '(Makefile|makefile|\.mk|Dockerfile|docker-compose|\.dockerfile)$' | wc -l)
    local has_scripts=$(echo "$diff" | grep -E '\.(sh|py|js|pl|rb|go|rs|cpp|c|h)$' | wc -l)

    # Analyze content changes
    local added_lines=$(echo "$diff" | grep '^+' | grep -v '^+++' | wc -l)
    local deleted_lines=$(echo "$diff" | grep '^-' | grep -v '^---' | wc -l)
    local total_changes=$((added_lines + deleted_lines))

    # Determine commit type based on file types and change patterns
    if [ $has_tests -gt 0 ] && [ $has_scripts -eq 0 ] && [ $has_docs -eq 0 ] && [ $has_config -eq 0 ]; then
        echo "test"
    elif [ $has_docs -gt 0 ] && [ $has_scripts -eq 0 ] && [ $has_config -eq 0 ] && [ $has_build -eq 0 ]; then
        echo "docs"
    elif [ $has_config -gt 0 ] && [ $has_scripts -eq 0 ] && [ $has_docs -eq 0 ]; then
        echo "chore"
    elif [ $has_build -gt 0 ] && [ $has_scripts -eq 0 ] && [ $has_docs -eq 0 ]; then
        echo "chore"
    elif [ $added_lines -gt $((deleted_lines * 2)) ] && [ $total_changes -gt 10 ]; then
        echo "feat"
    elif [ $deleted_lines -gt $((added_lines * 2)) ] && [ $total_changes -gt 10 ]; then
        echo "refactor"
    elif [ $total_changes -gt 0 ]; then
        echo "fix"
    else
        echo "chore"
    fi
}

# Extract a brief description from the diff
extract_description() {
    local diff="$1"

    # For this skill, we focus on file names and types rather than parsing diff content
    # since the content might include documentation or script code

    # Check for specific patterns in file names
    local has_skill=$(echo "$diff" | grep -i 'skill' | wc -l)
    local has_commit=$(echo "$diff" | grep -i 'commit' | wc -l)
    local has_config=$(echo "$diff" | grep -E '(config|settings|env|yml|yaml|json|toml|ini)$' | wc -l)

    if [ $has_skill -gt 0 ] && [ $has_commit -gt 0 ]; then
        echo "add commit message skill"
        return
    fi

    # Look for file names being added/modified
    local main_file=$(echo "$diff" | grep '^+++ ' | head -1 | sed 's/^+++ b\///' | sed 's/\..*$//' | sed 's/[-_]/ /g' | xargs)
    if [ -n "$main_file" ] && [ "$main_file" != "SKILL" ] && [ "$main_file" != "commitmsg" ]; then
        echo "$main_file"
        return
    fi

    # Fallback based on file types
    local file_count=$(echo "$diff" | grep '^+++ ' | wc -l)
    local code_files=$(echo "$diff" | grep '^+++ ' | grep -E '\.(go|c|cpp|h|rs|py|js|java|php)$' | wc -l)
    local doc_files=$(echo "$diff" | grep '^+++ ' | grep -E '\.(md|txt|rst|adoc)$' | wc -l)
    local script_files=$(echo "$diff" | grep '^+++ ' | grep -E '\.(sh|py|js|pl|rb)$' | wc -l)

    if [ $code_files -gt 0 ]; then
        echo "update code functionality"
    elif [ $doc_files -gt 0 ]; then
        echo "update documentation"
    elif [ $script_files -gt 0 ]; then
        echo "update scripts"
    elif [ $has_config -gt 0 ]; then
        echo "update configuration"
    else
        echo "update project files"
    fi
}

# Generate the commit message
generate_commit_message() {
    local diff="$1"
    local commit_type=$(analyze_diff "$diff")
    local description=$(extract_description "$diff")

    # Capitalize first letter of description
    description=$(echo "$description" | sed 's/\b\w/\U&/g')

    # Ensure description is not too long
    if [ ${#description} -gt 50 ]; then
        description="${description:0:47}..."
    fi

    # Format the commit message
    echo "$commit_type: $description"
}

# Generate and output the commit message
generate_commit_message "$DIFF"