package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"io/ioutil"
	"log"
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

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "", "Path to YAML configuration file")
	flag.Parse()
}

// ArgumentConfig represents a single argument's configuration in YAML,
// supporting both simple string descriptions and detailed mappings.
type ArgumentConfig struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Type        string `yaml:"type"`
}

// UnmarshalYAML allows ArgumentConfig to be parsed from either a simple string
// or a detailed mapping defining description, required, and type.
func (a *ArgumentConfig) UnmarshalYAML(value *yaml.Node) error {
	var desc string
	if err := value.Decode(&desc); err == nil {
		a.Description = desc
		a.Type = "string"
		a.Required = false
		return nil
	}
	type rawArg ArgumentConfig
	var ra rawArg
	if err := value.Decode(&ra); err != nil {
		return err
	}
	*a = ArgumentConfig(ra)
	return nil
}

// ToolConfig defines the configuration for a single tool in YAML
type ToolConfig struct {
	Description string                    `yaml:"description"`
	Arguments   map[string]ArgumentConfig `yaml:"arguments"`
	Command     struct {
		Program     string            `yaml:"program"`
		Environment map[string]string `yaml:"environment"`
		Args        []string          `yaml:"args"`
	} `yaml:"command"`
}

// Config holds multiple tool configurations
type Config struct {
	Tools map[string]*ToolConfig `yaml:"tools"`
}

// loadConfig reads and parses the YAML config file
func loadConfig(path string) (*Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// buildTools constructs mcplib.Tools based on the loaded config
func buildTools(cfg *Config) []mcplib.Tool {
	var tools []mcplib.Tool
	for name, tcfg := range cfg.Tools {
		properties := map[string]any{}
		// Build JSON schema properties for each argument, collecting required fields
		requiredFields := []string{}
		for argName, argCfg := range tcfg.Arguments {
			properties[argName] = map[string]any{
				"type":        argCfg.Type,
				"description": argCfg.Description,
			}
			if argCfg.Required {
				requiredFields = append(requiredFields, argName)
			}
		}
		inputSchema := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(requiredFields) > 0 {
			inputSchema["required"] = requiredFields
		}
		handler := func(args map[string]any) (any, error) {
			cmd := exec.Command(tcfg.Command.Program, tcfg.Command.Args...)
			env := os.Environ()
			for k, v := range tcfg.Command.Environment {
				env = append(env, fmt.Sprintf("%s=%s", k, v))
			}
			cmd.Env = env

			// Marshal arguments to JSON and pass to stdin
			stdinBytes, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			cmd.Stdin = bytes.NewReader(stdinBytes)

			output, err := cmd.CombinedOutput()
			return string(output), err
		}
		tools = append(tools, mcplib.Tool{
			Name:        name,
			Description: tcfg.Description,
			InputSchema: inputSchema,
			Handler:     handler,
		})
	}
	return tools
}

// PipeService handles all code-related operations
type PipeService struct {
	// Track file modification times to detect changes
	fileModTimes map[string]time.Time
	// Cache for language identification
	langCache map[string]string
	// Cache for build system identification
	buildSystemCache map[string]string
	// Cache for facts for save_memory
	memoryCache map[string]string
}

// NewPipeService creates a new PipeService instance
func NewPipeService() *PipeService {
	return &PipeService{
		fileModTimes:     make(map[string]time.Time),
		langCache:        make(map[string]string),
		buildSystemCache: make(map[string]string),
		memoryCache:      make(map[string]string),
	}
}

// GetTools returns all available code tools
func (s *PipeService) GetTools() []mcplib.Tool {
	// If a YAML config file is provided, build tools dynamically
	if configPath != "" || len(flag.Args()) > 0 {
		cfgPath := configPath
		if cfgPath == "" {
			cfgPath = flag.Args()[0]
		}
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			log.Fatalf("failed to load config: %v", err)
		}
		return buildTools(cfg)
	}
	return []mcplib.Tool{}
}

// Handler implementations

func (s *PipeService) handleListDirectory(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	files, err := ioutil.ReadDir(path)
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

func (s *PipeService) handleReadFile(args map[string]any) (any, error) {
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

	file, err := os.Open(absolute_path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	if limit == -1 && offset == 0 {
		content, err := ioutil.ReadFile(absolute_path)
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

func (s *PipeService) handleSearchFileContent(args map[string]any) (any, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	path := "."
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
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

func (s *PipeService) handleGlob(args map[string]any) (any, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	path := "."
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
	}

	matches, err := filepath.Glob(filepath.Join(path, pattern))
	if err != nil {
		return nil, fmt.Errorf("glob failed: %w", err)
	}

	return map[string]any{"files": matches}, nil
}

func (s *PipeService) handleReplace(args map[string]any) (any, error) {
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

	content, err := ioutil.ReadFile(filePath)
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

	err = ioutil.WriteFile(filePath, []byte(newContent), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %v", err)
	}

	return map[string]any{"success": true}, nil
}

func (s *PipeService) handleWriteFile(args map[string]any) (any, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content is required")
	}

	err := ioutil.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

func (s *PipeService) handleWebFetch(args map[string]any) (any, error) {
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

func (s *PipeService) handleReadManyFiles(args map[string]any) (any, error) {
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

func (s *PipeService) handleRunShellCommand(args map[string]any) (any, error) {
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
		cmd.Dir = dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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

	return map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": returnCode,
		"error":     errStr,
	}, nil
}

func (s *PipeService) handleSaveMemory(args map[string]any) (any, error) {
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
