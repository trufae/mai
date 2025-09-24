package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mcplib"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GemCodeService handles all code-related operations
type GemCodeService struct {
	// Track file modification times to detect changes
	fileModTimes map[string]time.Time
	// Cache for language identification
	langCache map[string]string
	// Cache for build system identification
	buildSystemCache map[string]string
	// Cache for facts for save_memory
	memoryCache map[string]string
}

// NewGemCodeService creates a new GemCodeService instance
func NewGemCodeService() *GemCodeService {
	return &GemCodeService{
		fileModTimes:     make(map[string]time.Time),
		langCache:        make(map[string]string),
		buildSystemCache: make(map[string]string),
		memoryCache:      make(map[string]string),
	}
}

// GetTools returns all available code tools
func (s *GemCodeService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		// 1. list_directory
		{
			Name:        "list_directory",
			Description: "Lists names of files and subdirectories in a specified directory, with optional exclusion of entries matching glob patterns.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the directory to list",
					},
					"ignore": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "List of glob patterns to ignore",
					},
					"file_filtering_options": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"respect_git_ignore": map[string]any{
								"type":        "boolean",
								"description": "Optional: Whether to respect .gitignore patterns when listing files. Only available in git repositories. Defaults to true.",
							},
							"respect_gemini_ignore": map[string]any{
								"type":        "boolean",
								"description": "Optional: Whether to respect .geminiignore patterns when listing files. Defaults to true.",
							},
						},
					},
				},
				"required": []string{"path"},
			},
			Handler: s.handleListDirectory,
		},
		// 2. read_file
		{
			Name:        "read_file",
			Description: "Reads and returns the content of a specified file from the local filesystem. For large files, it can read specific line ranges.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"absolute_path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the file to read (e.g., '/home/user/project/file.txt'). Relative paths are not supported. You must provide an absolute path.",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Optional: For text files, maximum number of lines to read. Use with 'offset' to paginate through large files. If omitted, reads the entire file (if feasible, up to a default limit).",
					},
					"offset": map[string]any{
						"type":        "number",
						"description": "Optional: For text files, the 0-based line number to start reading from. Requires 'limit' to be set. Use for paginating through large files.",
					},
				},
				"required": []string{"absolute_path"},
			},
			Handler: s.handleReadFile,
		},
		// 3. search_file_content
		{
			Name:        "search_file_content",
			Description: "Searches for a regex pattern in files within a specified directory, filtered by a glob pattern. Returns matching lines with their file paths and line numbers.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The regular expression (regex) pattern to search for within file contents (e.g., \"function\\s+myFunction\", \"import\\s+\\{.*\\}\\s+from\\s+.*\").",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the directory to search within. If omitted, searches the current working directory.",
					},
					"include": map[string]any{
						"type":        "string",
						"description": "Optional: A glob pattern to filter which files are searched (e.g., '*.js', '*.{ts,tsx}', 'src/**'). If omitted, searches all files (respecting potential global ignores).",
					},
				},
				"required": []string{"pattern", "path"},
			},
			Handler: s.handleSearchFileContent,
		},
		// 4. glob
		{
			Name:        "glob",
			Description: "Finds file names matching specific glob patterns (e.g., `src/**/*.ts`, `**/*.md`), returning absolute paths",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "The glob pattern to match against (e.g., '**/*.py', 'docs/*.md').",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional: The absolute path to the directory to search within. If omitted, searches the root directory.",
					},
					"case_sensitive": map[string]any{
						"type":        "boolean",
						"description": "Optional: Whether the search should be case-sensitive. Defaults to false.",
					},
					"respect_git_ignore": map[string]any{
						"type":        "boolean",
						"description": "Optional: Whether to respect .gitignore patterns when finding files. Only available in git repositories. Defaults to true.",
					},
				},
				"required": []string{"pattern"},
			},
			Handler: s.handleGlob,
		},
		// 5. replace
		{
			Name:        "replace",
			Description: "Replaces text within a file. By default, replaces a single occurrence, but can replace multiple occurrences when `expected_replacements` is specified. This tool requires providing significant context around the change to ensure precise targeting. Always use the read_file tool to examine the file's current content before attempting a text replacement.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the file to modify. Must start with '/'.",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The exact literal text to replace, preferably unescaped. For single replacements (default), include at least 3 lines of context BEFORE and AFTER the target text, matching whitespace and indentation precisely. For multiple replacements, specify expected_replacements parameter. If this string is not the exact literal text (i.e. you escaped it) or does not match exactly, the tool will fail.",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The exact literal text to replace `old_string` with, preferably unescaped. Provide the EXACT text. Ensure the resulting code is correct and idiomatic.",
					},
					"expected_replacements": map[string]any{
						"type":        "number",
						"description": "Number of replacements expected. Defaults to 1 if not specified. Use when you want to replace multiple occurrences.",
					},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
			Handler: s.handleReplace,
		},
		// 6. write_file
		{
			Name:        "write_file",
			Description: "Writes content to a specified file in the local filesystem.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The absolute path to the file to write to (e.g., '/home/user/project/file.txt'). Relative paths are not supported.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file.",
					},
				},
				"required": []string{"file_path", "content"},
			},
			Handler: s.handleWriteFile,
		},
		// 7. web_fetch
		{
			Name:        "web_fetch",
			Description: "Processes content from URL(s), including local and private network addresses (e.g., localhost), embedded in a prompt. Include up to 20 URLs and instructions (e.g., summarize, extract specific data) directly in the 'prompt' parameter.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "A comprehensive prompt that includes the URL(s) (up to 20) to fetch and specific instructions on how to process their content (e.g., \"Summarize https://example.com/article and extract key points from https://another.com/data\"). Must contain as least one URL starting with http:// or https://.",
					},
				},
				"required": []string{"prompt"},
			},
			Handler: s.handleWebFetch,
		},
		// 8. read_many_files
		{
			Name:        "read_many_files",
			Description: "Reads content from multiple files specified by paths or glob patterns within a configured target directory. For text files, it concatenates their content into a single string. It is primarily designed for text-based files. However, it can also process image (e.g., .png, .jpg) and PDF (.pdf) files if their file names or extensions are explicitly included in the 'paths' argument. For these explicitly requested non-text files, their data is read and included in a format suitable for model consumption (e.g., base64 encoded).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"paths": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Required. An array of glob patterns or paths relative to the tool's target directory. Examples: ['src/**/*.ts'], ['README.md', 'docs/']",
					},
					"exclude": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Optional. Glob patterns for files/directories to exclude. Added to default excludes if useDefaultExcludes is true. Example: [\"**/*.log\", \"temp/\"]",
					},
					"include": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Optional. Additional glob patterns to include. These are merged with `paths`. Example: [\"*.test.ts\"] to specifically add test files if they were broadly excluded.",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Optional. Whether to search recursively (primarily controlled by `**` in glob patterns). Defaults to true.",
					},
					"useDefaultExcludes": map[string]any{
						"type":        "boolean",
						"description": "Optional. Whether to apply a list of default exclusion patterns (e.g., node_modules, .git, binary files). Defaults to true.",
					},
					"file_filtering_options": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"respect_git_ignore": map[string]any{
								"type":        "boolean",
								"description": "Optional: Whether to respect .gitignore patterns when listing files. Only available in git repositories. Defaults to true.",
							},
							"respect_gemini_ignore": map[string]any{
								"type":        "boolean",
								"description": "Optional: Whether to respect .geminiignore patterns when listing files. Defaults to true.",
							},
						},
						"description": "Whether to respect ignore patterns from .gitignore or .geminiignore",
					},
				},
				"required": []string{"paths"},
			},
			Handler: s.handleReadManyFiles,
		},
		// 9. run_shell_command
		{
			Name:        "run_shell_command",
			Description: "This tool executes a given shell command as `bash -c <command>`. Command can start background processes using `&`. Command is executed as a subprocess that leads its own process group. Command process group can be terminated as `kill -- -PGID` or signaled as `kill -s SIGNAL -- -PGID`.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Exact bash command to execute as `bash -c <command>`",
					},
					"directory": map[string]any{
						"type":        "string",
						"description": "(OPTIONAL) Directory to run the command in, if not the project root directory. Must be relative to the project root directory and must already exist.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Brief description of the command for the user. Be specific and concise. Ideally a single sentence. Can be up to 3 sentences for clarity. No line breaks.",
					},
				},
				"required": []string{"command"},
			},
			Handler: s.handleRunShellCommand,
		},
		// 10. save_memory
		{
			Name:        "save_memory",
			Description: "Saves a specific piece of information or fact to your long-term memory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fact": map[string]any{
						"type":        "string",
						"description": "The specific fact or piece of information to remember. Should be a clear, self-contained statement.",
					},
				},
				"required": []string{"fact"},
			},
			Handler: s.handleSaveMemory,
		},
	}
}

// Handler implementations

func (s *GemCodeService) handleListDirectory(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	abs, err := AllowedPath(path)
	if err != nil {
		return nil, err
	}

	files, err := ioutil.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %v", err)
	}

	var ignorePatterns []string
	if ignore, ok := args["ignore"].([]any); ok {
		for _, pattern := range ignore {
			if p, ok := pattern.(string); ok {
				ignorePatterns = append(ignorePatterns, p)
			}
		}
	}

	fileNames := []string{}
	dirNames := []string{}
	for _, file := range files {
		fileName := file.Name()
		isIgnored := false
		for _, pattern := range ignorePatterns {
			matched, err := filepath.Match(pattern, fileName)
			if err == nil && matched {
				isIgnored = true
				break
			}
		}
		if !isIgnored {
			if file.IsDir() {
				dirNames = append(dirNames, fileName)
			} else {
				fileNames = append(fileNames, fileName)
			}
		}
	}

	return map[string]any{
		"files":       fileNames,
		"directories": dirNames,
	}, nil
}

func (s *GemCodeService) handleReadFile(args map[string]any) (any, error) {
	absolute_path, ok := args["absolute_path"].(string)
	if !ok || absolute_path == "" {
		return nil, fmt.Errorf("absolute_path is required")
	}

	limit := -1
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	offset := 0
	if o, ok := args["offset"].(float64); ok {
		offset = int(o)
	}

	abs, err := AllowedPath(absolute_path)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	if limit == -1 && offset == 0 {
		content, err := ioutil.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %v", err)
		}
		return map[string]any{
			"content": string(content),
		}, nil
	}

	scanner := bufio.NewScanner(file)
	var lines []string
	lineNum := 0
	for scanner.Scan() {
		if lineNum >= offset {
			lines = append(lines, scanner.Text())
		}
		lineNum++
		if limit != -1 && len(lines) >= limit {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning file: %v", err)
	}

	return map[string]any{
		"content": strings.Join(lines, "\n"),
	}, nil
}

func (s *GemCodeService) handleSearchFileContent(args map[string]any) (any, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	path := "."
	if p, ok := args["path"].(string); ok && p != "" {
		// ensure provided path is allowed
		if abs, err := AllowedPath(p); err == nil {
			path = abs
		} else {
			return nil, err
		}
	}

	var cmdArgs []string
	var cmdName string

	if _, err := exec.LookPath("rg"); err == nil {
		cmdName = "rg"
		cmdArgs = append(cmdArgs, "--json", pattern, path)
		if include, ok := args["include"].(string); ok && include != "" {
			cmdArgs = append(cmdArgs, "-g", include)
		}
	} else if _, err := exec.LookPath("git"); err == nil {
		cmdName = "git"
		cmdArgs = append(cmdArgs, "grep", "-n", "-I", "--line-number", pattern)
		if include, ok := args["include"].(string); ok && include != "" {
			cmdArgs = append(cmdArgs, "--", path+"/"+include)
		}
	} else {
		cmdName = "grep"
		cmdArgs = append(cmdArgs, "-r", "-n", "-I", pattern, path)
	}

	cmd := exec.Command(cmdName, cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return map[string]any{"matches": []any{}}, nil
		}
		return nil, fmt.Errorf("failed to execute search command: %v, output: %s", err, string(output))
	}

	var matches []any
	if cmdName == "rg" {
		decoder := json.NewDecoder(strings.NewReader(string(output)))
		for {
			var rgMatch struct {
				Type string `json:"type"`
				Data struct {
					Path struct {
						Text string `json:"text"`
					} `json:"path"`
					Lines struct {
						Text string `json:"text"`
					} `json:"lines"`
					LineNumber uint64 `json:"line_number"`
					Submatches []struct {
						Match struct {
							Text string `json:"text"`
						} `json:"match"`
						Start uint64 `json:"start"`
						End   uint64 `json:"end"`
					} `json:"submatches"`
				} `json:"data"`
			}
			if err := decoder.Decode(&rgMatch); err == io.EOF {
				break
			} else if err != nil {
				break
			}
			if rgMatch.Type == "match" {
				matches = append(matches, map[string]any{
					"file_path":   rgMatch.Data.Path.Text,
					"line_number": rgMatch.Data.LineNumber,
					"line":        strings.TrimSpace(rgMatch.Data.Lines.Text),
				})
			}
		}
	} else {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 3)
			if len(parts) < 3 {
				continue
			}
			filePath := parts[0]
			lineNumber, _ := strconv.Atoi(parts[1])
			lineContent := parts[2]
			matches = append(matches, map[string]any{
				"file_path":   filePath,
				"line_number": lineNumber,
				"line":        lineContent,
			})
		}
	}

	return map[string]any{
		"matches": matches,
	}, nil
}

func (s *GemCodeService) handleGlob(args map[string]any) (any, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		pattern = "*"
	}

	base := "."
	if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
		base = p
	}

	// If the user supplied an absolute pattern, don't join it with base.
	full := pattern
	if !filepath.IsAbs(pattern) {
		full = filepath.Join(base, pattern)
	}

	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, fmt.Errorf("glob failed: %w", err)
	}

	// Ensure JSON renders [] instead of null when there are no matches.
	if matches == nil {
		matches = []string{}
	}

	// filter matches to allowed locations
	allowed := FilterAllowed(matches)

	return map[string]any{"files": allowed}, nil
}

func (s *GemCodeService) handleReplace(args map[string]any) (any, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}
	oldString, ok := args["old_string"].(string)
	if !ok {
		return nil, fmt.Errorf("old_string is required")
	}
	newString, ok := args["new_string"].(string)
	if !ok {
		return nil, fmt.Errorf("new_string is required")
	}

	// ensure file path is within allowed directories
	abs, err := AllowedPath(filePath)
	if err != nil {
		return nil, err
	}
	content, err := ioutil.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	fileContent := string(content)

	replacements := -1
	if er, ok := args["expected_replacements"].(float64); ok {
		replacements = int(er)
	} else {
		count := strings.Count(fileContent, oldString)
		if count != 1 {
			return nil, fmt.Errorf("old_string found %d times, but expected 1 for a default replacement", count)
		}
		replacements = 1
	}

	newContent := strings.Replace(fileContent, oldString, newString, replacements)

	// ensure write path is allowed
	absW, err := AllowedPath(filePath)
	if err != nil {
		return nil, err
	}
	err = ioutil.WriteFile(absW, []byte(newContent), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %v", err)
	}

	return map[string]any{"success": true}, nil
}

func (s *GemCodeService) handleWriteFile(args map[string]any) (any, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content is required")
	}

	abs, err := AllowedPath(filePath)
	if err != nil {
		return nil, err
	}
	err = ioutil.WriteFile(abs, []byte(content), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

func (s *GemCodeService) handleWebFetch(args map[string]any) (any, error) {
	prompt, ok := args["prompt"].(string)
	if !ok || prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	re := regexp.MustCompile(`https?://[^
	 /$.?#].*`)
	urls := re.FindAllString(prompt, -1)

	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs found in prompt")
	}

	var contents []string
	for _, url := range urls {
		resp, err := http.Get(url)
		if err != nil {
			contents = append(contents, fmt.Sprintf("Failed to fetch %s: %v", url, err))
			continue
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			contents = append(contents, fmt.Sprintf("Failed to read body from %s: %v", url, err))
			continue
		}
		contents = append(contents, fmt.Sprintf("Content from %s:\n%s", url, string(body)))
	}

	return map[string]any{
		"content": strings.Join(contents, "\n\n---\n\n"),
	}, nil
}

func (s *GemCodeService) handleReadManyFiles(args map[string]any) (any, error) {
	pathsArg, ok := args["paths"].([]any)
	if !ok {
		return nil, fmt.Errorf("paths is required and must be an array of strings")
	}

	var paths []string
	for _, p := range pathsArg {
		if path, ok := p.(string); ok {
			paths = append(paths, path)
		}
	}

	var contentBuilder strings.Builder
	for _, pattern := range paths {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			fmt.Fprintf(&contentBuilder, "--- Error processing pattern %s: %v ---\\n", pattern, err)
			continue
		}

		for _, match := range matches {
			// skip matches outside allowed directories
			if _, err := AllowedPath(match); err != nil {
				fmt.Fprintf(&contentBuilder, "--- Skipping disallowed file %s ---\\n", match)
				continue
			}
			content, err := ioutil.ReadFile(match)
			if err != nil {
				fmt.Fprintf(&contentBuilder, "--- Error reading file %s: %v ---\\n", match, err)
				continue
			}
			fmt.Fprintf(&contentBuilder, "--- %s ---\\n", match)
			contentBuilder.Write(content)
			contentBuilder.WriteString("\\n")
		}
	}

	return map[string]any{
		"content": contentBuilder.String(),
	}, nil
}

func (s *GemCodeService) handleRunShellCommand(args map[string]any) (any, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return nil, fmt.Errorf("command is required")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("bash", "-c", command)
	}

	if dir, ok := args["directory"].(string); ok && dir != "" {
		if abs, err := AllowedPath(dir); err == nil {
			cmd.Dir = abs
		} else {
			return nil, err
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	fmt.Fprintln(os.Stderr, "This is the command: "+command)

	err := cmd.Run()

	returnCode := 0
	var errStr string
	if err != nil {
		errStr = err.Error()
		if exitError, ok := err.(*exec.ExitError); ok {
			returnCode = exitError.ExitCode()
		} else {
			returnCode = -1
		}
	}
	stdoutString := stdout.String()
	stderrString := stderr.String()
	result := ""
	if stdoutString == "" {
		if returnCode == 0 {
			result = "Success"
		} else {
			result = "Failure"
		}
	}
	res := map[string]any{
		command: result,
		/*
			"command":   command,
			"exit_code": returnCode,
			"result":    result,
		*/
	}
	if stdoutString != "" {
		res["stdout"] = stdoutString
	}
	if stderrString != "" {
		res["stderr"] = stderrString
	}
	if errStr != "" {
		res["error"] = errStr
	}

	return res, nil
}

func (s *GemCodeService) handleSaveMemory(args map[string]any) (any, error) {
	fact, ok := args["fact"].(string)
	if !ok || fact == "" {
		return nil, fmt.Errorf("fact is required")
	}

	key := time.Now().Format(time.RFC3339Nano)
	s.memoryCache[key] = fact

	return map[string]any{
		"success": true,
	}, nil
}
