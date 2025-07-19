package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"mcplib"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// CodeService handles all code-related operations
type CodeService struct{
	// Track file modification times to detect changes
	fileModTimes map[string]time.Time
	// Cache for language identification
	langCache map[string]string
	// Cache for build system identification
	buildSystemCache map[string]string
}

// NewCodeService creates a new CodeService instance
func NewCodeService() *CodeService {
	return &CodeService{
		fileModTimes: make(map[string]time.Time),
		langCache: make(map[string]string),
		buildSystemCache: make(map[string]string),
	}
}

// GetTools returns all available code tools
func (s *CodeService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		// Base tools from original implementation
		// 1. CommandExecutor
		{
			Name:        "CommandExecutor",
			Description: "Executes shell commands on the MCP server and returns the standard output, standard error, and return code.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to be executed.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Maximum time in seconds to wait for the command to execute.",
					},
				},
				"required": []string{"command"},
			},
			UsageExamples: "Example: {\"command\": \"ls -la\"} - Lists files in the current directory with details",
			Handler:       s.handleCommandExecutor,
		},

		// 2. FileOperations - ReadFile
		{
			Name:        "ReadFile",
			Description: "Reads content from a file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filepath": map[string]any{
						"type":        "string",
						"description": "The path of the file to read.",
					},
				},
				"required": []string{"filepath"},
			},
			UsageExamples: "Example: {\"filepath\": \"/server/path/file.txt\"} - Reads content from the specified file",
			Handler:       s.handleReadFile,
		},

		// 2. FileOperations - WriteFile
		{
			Name:        "WriteFile",
			Description: "Writes content to a file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filepath": map[string]any{
						"type":        "string",
						"description": "The path of the file to write to.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file.",
					},
				},
				"required": []string{"filepath", "content"},
			},
			UsageExamples: "Example: {\"filepath\": \"/server/path/file.txt\", \"content\": \"Hello, World!\"} - Writes content to the specified file",
			Handler:       s.handleWriteFile,
		},

		// 2. FileOperations - AppendFile
		{
			Name:        "AppendFile",
			Description: "Appends content to an existing file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filepath": map[string]any{
						"type":        "string",
						"description": "The path of the file to append to.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to append to the file.",
					},
				},
				"required": []string{"filepath", "content"},
			},
			UsageExamples: "Example: {\"filepath\": \"/server/path/file.txt\", \"content\": \"Additional content\"} - Appends content to the specified file",
			Handler:       s.handleAppendFile,
		},

		// 2. FileOperations - DeleteFile
		{
			Name:        "DeleteFile",
			Description: "Deletes a file from the filesystem.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filepath": map[string]any{
						"type":        "string",
						"description": "The path of the file to delete.",
					},
				},
				"required": []string{"filepath"},
			},
			UsageExamples: "Example: {\"filepath\": \"/server/path/file.txt\"} - Deletes the specified file",
			Handler:       s.handleDeleteFile,
		},

		// 2. FileOperations - MoveFile
		{
			Name:        "MoveFile",
			Description: "Moves a file from source to destination.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "Source path of the file.",
					},
					"destination": map[string]any{
						"type":        "string",
						"description": "Destination path where the file should be moved.",
					},
				},
				"required": []string{"source", "destination"},
			},
			UsageExamples: "Example: {\"source\": \"/server/path/file.txt\", \"destination\": \"/new/path/file.txt\"} - Moves file to new location",
			Handler:       s.handleMoveFile,
		},

		// 2. FileOperations - RenameFile
		{
			Name:        "RenameFile",
			Description: "Renames a file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filepath": map[string]any{
						"type":        "string",
						"description": "Current path of the file.",
					},
					"new_name": map[string]any{
						"type":        "string",
						"description": "New name for the file.",
					},
				},
				"required": []string{"filepath", "new_name"},
			},
			UsageExamples: "Example: {\"filepath\": \"/server/path/file.txt\", \"new_name\": \"newfile.txt\"} - Renames the specified file",
			Handler:       s.handleRenameFile,
		},

		// 2. FileOperations - CopyFile
		{
			Name:        "CopyFile",
			Description: "Copies a file from source to destination.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "Source path of the file.",
					},
					"destination": map[string]any{
						"type":        "string",
						"description": "Destination path where the file should be copied.",
					},
				},
				"required": []string{"source", "destination"},
			},
			UsageExamples: "Example: {\"source\": \"/server/path/file.txt\", \"destination\": \"/backup/path/file.txt\"} - Copies file to new location",
			Handler:       s.handleCopyFile,
		},

		// 2. FileOperations - CreateDirectory
		{
			Name:        "CreateDirectory",
			Description: "Creates a new directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"directory_path": map[string]any{
						"type":        "string",
						"description": "The path of the new directory.",
					},
				},
				"required": []string{"directory_path"},
			},
			UsageExamples: "Example: {\"directory_path\": \"/new/directory/\"} - Creates a new directory at the specified path",
			Handler:       s.handleCreateDirectory,
		},

		// 2. FileOperations - RemoveDirectory
		{
			Name:        "RemoveDirectory",
			Description: "Removes a directory and all its contents.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"directory_path": map[string]any{
						"type":        "string",
						"description": "The path of the directory to remove.",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Whether to remove the directory recursively (including all contents).",
					},
				},
				"required": []string{"directory_path"},
			},
			UsageExamples: "Example: {\"directory_path\": \"/old/directory/\", \"recursive\": true} - Removes the directory and all its contents",
			Handler:       s.handleRemoveDirectory,
		},

		// 3. SystemInformation - GetOSInfo
		{
			Name:        "GetOSInfo",
			Description: "Gets information about the operating system.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			UsageExamples: "Example: {} - Returns information about the operating system",
			Handler:       s.handleGetOSInfo,
		},

		// 3. SystemInformation - GetEnvironment
		{
			Name:        "GetEnvironment",
			Description: "Gets environment variables.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"variable": map[string]any{
						"type":        "string",
						"description": "The name of the environment variable to get. If not provided, returns all environment variables.",
					},
				},
			},
			UsageExamples: "Example: {\"variable\": \"PATH\"} - Returns the value of the PATH environment variable",
			Handler:       s.handleGetEnvironment,
		},

		// 3. SystemInformation - SetEnvironment
		{
			Name:        "SetEnvironment",
			Description: "Sets an environment variable.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"variable": map[string]any{
						"type":        "string",
						"description": "The name of the environment variable to set.",
					},
					"value": map[string]any{
						"type":        "string",
						"description": "The value to set for the environment variable.",
					},
				},
				"required": []string{"variable", "value"},
			},
			UsageExamples: "Example: {\"variable\": \"DEBUG\", \"value\": \"true\"} - Sets the DEBUG environment variable to true",
			Handler:       s.handleSetEnvironment,
		},

		// Code-related tools
		// 1. IdentifyLanguage
		{
			Name:        "IdentifyLanguage",
			Description: "Identifies the programming language used in a file or directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or directory to identify the language for.",
					},
				},
				"required": []string{"path"},
			},
			UsageExamples: "Example: {\"path\": \"/path/to/project\"} - Identifies the primary programming languages used",
			Handler:       s.handleIdentifyLanguage,
		},

		// 2. IdentifyBuildSystem
		{
			Name:        "IdentifyBuildSystem",
			Description: "Identifies the build system used in a project directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"directory": map[string]any{
						"type":        "string",
						"description": "Path to the project directory.",
					},
				},
				"required": []string{"directory"},
			},
			UsageExamples: "Example: {\"directory\": \"/path/to/project\"} - Identifies the build system used",
			Handler:       s.handleIdentifyBuildSystem,
		},

		// 3. Compile
		{
			Name:        "Compile",
			Description: "Compiles a project or file using the appropriate build system.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or project directory to compile.",
					},
					"options": map[string]any{
						"type":        "string",
						"description": "Additional compilation options.",
					},
					"build_system": map[string]any{
						"type":        "string",
						"description": "Explicitly specify the build system to use. If not provided, it will be auto-detected.",
					},
				},
				"required": []string{"path"},
			},
			UsageExamples: "Example: {\"path\": \"/path/to/project\"} - Compiles the project",
			Handler:       s.handleCompile,
		},

		// 4. Run
		{
			Name:        "Run",
			Description: "Runs a project or file using the appropriate execution method.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or project directory to run.",
					},
					"args": map[string]any{
						"type":        "string",
						"description": "Command-line arguments to pass to the program.",
					},
					"compile_first": map[string]any{
						"type":        "boolean",
						"description": "Whether to compile the project before running it (for compiled languages).",
					},
				},
				"required": []string{"path"},
			},
			UsageExamples: "Example: {\"path\": \"/path/to/project\", \"compile_first\": true} - Compiles and runs the project",
			Handler:       s.handleRun,
		},

		// 5. ListFunctions
		{
			Name:        "ListFunctions",
			Description: "Lists functions in a file or directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or directory to list functions from.",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Whether to search recursively in subdirectories (if path is a directory).",
					},
				},
				"required": []string{"path"},
			},
			UsageExamples: "Example: {\"path\": \"/path/to/file.go\", \"recursive\": false} - Lists functions in the file",
			Handler:       s.handleListFunctions,
		},

		// 6. GetFunctionBody
		{
			Name:        "GetFunctionBody",
			Description: "Gets the body of a function given its name and file/directory path.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"function_name": map[string]any{
						"type":        "string",
						"description": "The name of the function to retrieve.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or directory to search for the function.",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Whether to search recursively in subdirectories (if path is a directory).",
					},
				},
				"required": []string{"function_name", "path"},
			},
			UsageExamples: "Example: {\"function_name\": \"main\", \"path\": \"/path/to/file.go\"} - Gets the body of the main function",
			Handler:       s.handleGetFunctionBody,
		},

		// 7. FileChangeCheck
		{
			Name:        "FileChangeCheck",
			Description: "Checks if a file has been modified since it was last checked.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to the file to check for changes.",
					},
				},
				"required": []string{"file_path"},
			},
			UsageExamples: "Example: {\"file_path\": \"/path/to/file.go\"} - Checks if the file has been modified",
			Handler:       s.handleFileChangeCheck,
		},

		// 8. ApplyPatch
		{
			Name:        "ApplyPatch",
			Description: "Applies a patch to a file by replacing specific lines.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to the file to patch.",
					},
					"start_line": map[string]any{
						"type":        "integer",
						"description": "The line number to start replacing from (1-based).",
					},
					"end_line": map[string]any{
						"type":        "integer",
						"description": "The line number to end replacing at (1-based, inclusive).",
					},
					"new_content": map[string]any{
						"type":        "string",
						"description": "The new content to replace the specified lines with.",
					},
				},
				"required": []string{"file_path", "start_line", "end_line", "new_content"},
			},
			UsageExamples: "Example: {\"file_path\": \"/path/to/file.go\", \"start_line\": 10, \"end_line\": 15, \"new_content\": \"// New code here\"} - Replaces lines 10-15 with new content",
			Handler:       s.handleApplyPatch,
		},

		// 9. QueryStructure
		{
			Name:        "QueryStructure",
			Description: "Queries structure information (classes, interfaces, structs, etc.) in a file or directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file or directory to query structures from.",
					},
					"recursive": map[string]any{
						"type":        "boolean",
						"description": "Whether to search recursively in subdirectories (if path is a directory).",
					},
					"structure_name": map[string]any{
						"type":        "string",
						"description": "Optional name of a specific structure to query.",
					},
				},
				"required": []string{"path"},
			},
			UsageExamples: "Example: {\"path\": \"/path/to/project\", \"recursive\": true} - Lists all structures in the project",
			Handler:       s.handleQueryStructure,
		},

		// 10. Make
		{
			Name:        "Make",
			Description: "Runs a make command in a project directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"directory": map[string]any{
						"type":        "string",
						"description": "Path to the project directory with the Makefile.",
					},
					"target": map[string]any{
						"type":        "string",
						"description": "The make target to run. If not provided, the default target will be used.",
					},
				},
				"required": []string{"directory"},
			},
			UsageExamples: "Example: {\"directory\": \"/path/to/project\", \"target\": \"test\"} - Runs 'make test' in the project directory",
			Handler:       s.handleMakeCmd,
		},

		// 3. SystemInformation - ListFiles
		{
			Name:        "ListFiles",
			Description: "Lists files in a directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"directory_path": map[string]any{
						"type":        "string",
						"description": "The path of the directory to list files from.",
					},
				},
				"required": []string{"directory_path"},
			},
			UsageExamples: "Example: {\"directory_path\": \"/some/directory/\"} - Lists files in the specified directory",
			Handler:       s.handleListFiles,
		},

		// 3. SystemInformation - ChangeDirectory
		{
			Name:        "ChangeDirectory",
			Description: "Changes the current working directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"directory_path": map[string]any{
						"type":        "string",
						"description": "The path to change the current working directory to.",
					},
				},
				"required": []string{"directory_path"},
			},
			UsageExamples: "Example: {\"directory_path\": \"/another/directory/\"} - Changes the current working directory",
			Handler:       s.handleChangeDirectory,
		},

		// 3. SystemInformation - GetCurrentDirectory
		{
			Name:        "GetCurrentDirectory",
			Description: "Gets the current working directory.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			UsageExamples: "Example: {} - Returns the current working directory",
			Handler:       s.handleGetCurrentDirectory,
		},
	}
}

// Handler implementations

// handleCommandExecutor handles the CommandExecutor request
func (s *CodeService) handleCommandExecutor(args map[string]any) (any, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Default timeout is 60 seconds if not specified
	timeout := 60
	if timeoutArg, ok := args["timeout"].(float64); ok {
		timeout = int(timeoutArg)
	}

	// Create a command with shell
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}

	// Set up pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %v", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %v", err)
	}

	// Create a channel to signal command completion
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Set up timeout
	var timeoutReached bool
	var cmdErr error

	select {
	case cmdErr = <-done:
		// Command completed before timeout
	case <-time.After(time.Duration(timeout) * time.Second):
		// Timeout reached
		if err := cmd.Process.Kill(); err != nil {
			return nil, fmt.Errorf("failed to kill process after timeout: %v", err)
		}
		timeoutReached = true
		cmdErr = fmt.Errorf("command timed out after %d seconds", timeout)
	}

	// Read stdout and stderr
	stdoutBytes, _ := ioutil.ReadAll(stdout)
	stderrBytes, _ := ioutil.ReadAll(stderr)

	// Get return code
	returnCode := 0
	if cmdErr != nil && !timeoutReached {
		if exitError, ok := cmdErr.(*exec.ExitError); ok {
			returnCode = exitError.ExitCode()
		}
	}

	return map[string]any{
		"stdout":      string(stdoutBytes),
		"stderr":      string(stderrBytes),
		"return_code": returnCode,
		"timed_out":   timeoutReached,
	}, nil
}

// handleReadFile handles the ReadFile request
func (s *CodeService) handleReadFile(args map[string]any) (any, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || filepath == "" {
		return nil, fmt.Errorf("filepath is required")
	}

	content, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	return map[string]any{
		"content": string(content),
	}, nil
}

// handleWriteFile handles the WriteFile request
func (s *CodeService) handleWriteFile(args map[string]any) (any, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || filepath == "" {
		return nil, fmt.Errorf("filepath is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content is required")
	}

	err := ioutil.WriteFile(filepath, []byte(content), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleMoveFile handles the MoveFile request
func (s *CodeService) handleMoveFile(args map[string]any) (any, error) {
	source, ok := args["source"].(string)
	if !ok || source == "" {
		return nil, fmt.Errorf("source is required")
	}

	destination, ok := args["destination"].(string)
	if !ok || destination == "" {
		return nil, fmt.Errorf("destination is required")
	}

	err := os.Rename(source, destination)
	if err != nil {
		return nil, fmt.Errorf("failed to move file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleCopyFile handles the CopyFile request
func (s *CodeService) handleCopyFile(args map[string]any) (any, error) {
	source, ok := args["source"].(string)
	if !ok || source == "" {
		return nil, fmt.Errorf("source is required")
	}

	destination, ok := args["destination"].(string)
	if !ok || destination == "" {
		return nil, fmt.Errorf("destination is required")
	}

	// Read the source file
	content, err := ioutil.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file: %v", err)
	}

	// Write to the destination file
	err = ioutil.WriteFile(destination, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write destination file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleCreateDirectory handles the CreateDirectory request
func (s *CodeService) handleCreateDirectory(args map[string]any) (any, error) {
	directoryPath, ok := args["directory_path"].(string)
	if !ok || directoryPath == "" {
		return nil, fmt.Errorf("directory_path is required")
	}

	err := os.MkdirAll(directoryPath, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create directory: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleGetOSInfo handles the GetOSInfo request
func (s *CodeService) handleGetOSInfo(args map[string]any) (any, error) {
	hostname, _ := os.Hostname()

	return map[string]any{
		"os":             runtime.GOOS,
		"architecture":   runtime.GOARCH,
		"hostname":       hostname,
		"number_of_cpus": runtime.NumCPU(),
		"go_version":     runtime.Version(),
		"working_dir":    s.getCurrentDirectory(),
		"temp_directory": os.TempDir(),
		"path_separator": string(os.PathSeparator),
		"list_separator": string(os.PathListSeparator),
	}, nil
}

// handleListFiles handles the ListFiles request
func (s *CodeService) handleListFiles(args map[string]any) (any, error) {
	directoryPath, ok := args["directory_path"].(string)
	if !ok || directoryPath == "" {
		return nil, fmt.Errorf("directory_path is required")
	}

	files, err := ioutil.ReadDir(directoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %v", err)
	}

	fileList := []map[string]any{}
	for _, file := range files {
		fileInfo := map[string]any{
			"name":         file.Name(),
			"size":         file.Size(),
			"is_directory": file.IsDir(),
			"mode":         file.Mode().String(),
			"modified":     file.ModTime().Format(time.RFC3339),
		}
		fileList = append(fileList, fileInfo)
	}

	return map[string]any{
		"files": fileList,
	}, nil
}

// handleChangeDirectory handles the ChangeDirectory request
func (s *CodeService) handleChangeDirectory(args map[string]any) (any, error) {
	directoryPath, ok := args["directory_path"].(string)
	if !ok || directoryPath == "" {
		return nil, fmt.Errorf("directory_path is required")
	}

	err := os.Chdir(directoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to change directory: %v", err)
	}

	currentDir := s.getCurrentDirectory()
	return map[string]any{
		"success":           true,
		"current_directory": currentDir,
	}, nil
}

// handleGetCurrentDirectory handles the GetCurrentDirectory request
func (s *CodeService) handleGetCurrentDirectory(args map[string]any) (any, error) {
	currentDir := s.getCurrentDirectory()
	return map[string]any{
		"current_directory": currentDir,
	}, nil
}

// getCurrentDirectory is a helper function to get the current working directory
func (s *CodeService) getCurrentDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// handleAppendFile handles the AppendFile request
func (s *CodeService) handleAppendFile(args map[string]any) (any, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || filepath == "" {
		return nil, fmt.Errorf("filepath is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content is required")
	}

	// Open file in append mode
	file, err := os.OpenFile(filepath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for appending: %v", err)
	}
	defer file.Close()

	// Write the content to the file
	if _, err := file.WriteString(content); err != nil {
		return nil, fmt.Errorf("failed to append to file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleDeleteFile handles the DeleteFile request
func (s *CodeService) handleDeleteFile(args map[string]any) (any, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || filepath == "" {
		return nil, fmt.Errorf("filepath is required")
	}

	err := os.Remove(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to delete file: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleRenameFile handles the RenameFile request
func (s *CodeService) handleRenameFile(args map[string]any) (any, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || filepath == "" {
		return nil, fmt.Errorf("filepath is required")
	}

	newName, ok := args["new_name"].(string)
	if !ok || newName == "" {
		return nil, fmt.Errorf("new_name is required")
	}

	// Get the directory path
	dir := filepath[:len(filepath)-len(filepath[strings.LastIndex(filepath, "/")+1:])]
	newPath := dir + newName

	err := os.Rename(filepath, newPath)
	if err != nil {
		return nil, fmt.Errorf("failed to rename file: %v", err)
	}

	return map[string]any{
		"success":  true,
		"old_path": filepath,
		"new_path": newPath,
	}, nil
}

// handleRemoveDirectory handles the RemoveDirectory request
func (s *CodeService) handleRemoveDirectory(args map[string]any) (any, error) {
	directoryPath, ok := args["directory_path"].(string)
	if !ok || directoryPath == "" {
		return nil, fmt.Errorf("directory_path is required")
	}

	recursive, _ := args["recursive"].(bool)

	var err error
	if recursive {
		// Remove directory and all contents
		err = os.RemoveAll(directoryPath)
	} else {
		// Remove only if directory is empty
		err = os.Remove(directoryPath)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to remove directory: %v", err)
	}

	return map[string]any{
		"success": true,
	}, nil
}

// handleGetEnvironment handles the GetEnvironment request
func (s *CodeService) handleGetEnvironment(args map[string]any) (any, error) {
	variable, ok := args["variable"].(string)

	if !ok || variable == "" {
		// Return all environment variables
		envVars := make(map[string]string)
		for _, env := range os.Environ() {
			pair := strings.SplitN(env, "=", 2)
			if len(pair) == 2 {
				envVars[pair[0]] = pair[1]
			}
		}

		return map[string]any{
			"environment": envVars,
		}, nil
	}

	// Return specific environment variable
	value := os.Getenv(variable)
	return map[string]any{
		"variable": variable,
		"value":    value,
	}, nil
}

// handleSetEnvironment handles the SetEnvironment request
func (s *CodeService) handleSetEnvironment(args map[string]any) (any, error) {
	variable, ok := args["variable"].(string)
	if !ok || variable == "" {
		return nil, fmt.Errorf("variable is required")
	}

	value, ok := args["value"].(string)
	if !ok {
		return nil, fmt.Errorf("value is required")
	}

	err := os.Setenv(variable, value)
	if err != nil {
		return nil, fmt.Errorf("failed to set environment variable: %v", err)
	}

	return map[string]any{
		"success":  true,
		"variable": variable,
		"value":    value,
	}, nil
}

// handleIdentifyLanguage identifies the programming language used in a file or directory
func (s *CodeService) handleIdentifyLanguage(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Check if we have a cached result for this path
	if lang, exists := s.langCache[path]; exists {
		return map[string]any{
			"language":   lang,
			"from_cache": true,
		}, nil
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %v", err)
	}

	languageDetails := map[string]any{}

	if fileInfo.IsDir() {
		// For directories, identify based on files and project structure
		languages, err := s.identifyLanguagesInDirectory(path)
		if err != nil {
			return nil, err
		}

		// Cache the primary language
		if len(languages) > 0 {
			s.langCache[path] = languages[0]["name"].(string)
		}

		languageDetails["languages"] = languages
		languageDetails["is_directory"] = true
	} else {
		// For single files, identify based on file extension and content
		lang, confidence, err := s.identifyLanguageFromFile(path)
		if err != nil {
			return nil, err
		}

		// Cache the result
		s.langCache[path] = lang

		languageDetails["language"] = lang
		languageDetails["confidence"] = confidence
		languageDetails["is_directory"] = false
	}

	return languageDetails, nil
}

// identifyLanguagesInDirectory analyzes a directory and returns a list of languages used
// The languages are returned in order of prevalence (most used first)
func (s *CodeService) identifyLanguagesInDirectory(dirPath string) ([]map[string]any, error) {
	// Map to track file counts by language
	langCounts := make(map[string]int)
	// Map to track file extensions by language
	langExtensions := make(map[string][]string)

	// Walk through directory
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and hidden files
		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Get file extension
		ext := strings.ToLower(filepath.Ext(path))
		if ext == "" {
			return nil
		}

		// Identify language from file extension
		lang := s.languageFromExtension(ext)
		if lang != "Unknown" {
			langCounts[lang]++
			if !containsString(langExtensions[lang], ext) {
				langExtensions[lang] = append(langExtensions[lang], ext)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory: %v", err)
	}

	// Convert map to slice for sorting
	langList := []map[string]any{}
	for lang, count := range langCounts {
		langList = append(langList, map[string]any{
			"name":       lang,
			"file_count": count,
			"extensions": langExtensions[lang],
		})
	}

	// Sort by file count (descending)
	sort.Slice(langList, func(i, j int) bool {
		return langList[i]["file_count"].(int) > langList[j]["file_count"].(int)
	})

	return langList, nil
}

// identifyLanguageFromFile identifies the programming language of a single file
func (s *CodeService) identifyLanguageFromFile(filePath string) (string, float64, error) {
	// First check by file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	lang := s.languageFromExtension(ext)

	// If we have a confident match from extension, return it
	if lang != "Unknown" {
		return lang, 0.9, nil
	}

	// Read a sample of the file for content-based identification
	content, err := s.readFileSample(filePath, 4096) // Read first 4KB
	if err != nil {
		return "Unknown", 0.0, err
	}

	// Try to identify by content patterns
	lang, confidence := s.languageFromContent(content)
	return lang, confidence, nil
}

// languageFromExtension maps file extensions to programming languages
func (s *CodeService) languageFromExtension(ext string) string {
	// Remove the dot if present
	ext = strings.TrimPrefix(ext, ".")

	// Map of file extensions to languages
	extMap := map[string]string{
		// C/C++
		"c":    "C",
		"h":    "C",
		"cpp":  "C++",
		"cc":   "C++",
		"cxx":  "C++",
		"hpp":  "C++",
		"hxx":  "C++",

		// Go
		"go":   "Go",

		// Python
		"py":   "Python",
		"pyw":  "Python",
		"ipynb":  "Python (Jupyter)",

		// JavaScript/TypeScript
		"js":   "JavaScript",
		"jsx":  "JavaScript (React)",
		"ts":   "TypeScript",
		"tsx":  "TypeScript (React)",

		// Ruby
		"rb":   "Ruby",

		// Java
		"java": "Java",

		// C#
		"cs":   "C#",

		// PHP
		"php":  "PHP",

		// Swift
		"swift": "Swift",

		// Rust
		"rs":   "Rust",

		// Scala
		"scala": "Scala",

		// Kotlin
		"kt":   "Kotlin",
		"kts":  "Kotlin",

		// HTML/CSS
		"html": "HTML",
		"htm":  "HTML",
		"css":  "CSS",
		"scss": "SCSS",
		"sass": "SASS",
		"less": "LESS",

		// Shell scripts
		"sh":   "Bash",
		"bash": "Bash",
		"zsh":  "Zsh",
		"fish": "Fish",

		// PowerShell
		"ps1":  "PowerShell",

		// Other
		"r":    "R",
		"dart": "Dart",
		"lua":  "Lua",
		"pl":   "Perl",
		"pm":   "Perl",
		"hs":   "Haskell",
		"erl":  "Erlang",
		"ex":   "Elixir",
		"exs":  "Elixir",
		"ml":   "OCaml",
		"mli":  "OCaml",
		"f":    "Fortran",
		"f90":  "Fortran",
		"lisp": "Lisp",
		"cl":   "Common Lisp",
		"clj":  "Clojure",
		"sql":  "SQL",
		"asm":  "Assembly",
		"s":    "Assembly",
	}

	if lang, found := extMap[ext]; found {
		return lang
	}
	return "Unknown"
}

// languageFromContent attempts to identify a language from file content patterns
func (s *CodeService) languageFromContent(content string) (string, float64) {
	// Define patterns for different languages
	patterns := map[string][]string{
		"Python": {
			`^#!/usr/bin/env python`,
			`^#!/usr/bin/python`,
			`import\s+[a-zA-Z0-9_]+`,
			`from\s+[a-zA-Z0-9_\.]+\s+import`,
			`def\s+[a-zA-Z0-9_]+\s*\(.*\)\s*:`,
			`class\s+[A-Z][a-zA-Z0-9_]*\s*\(?.*\)?\s*:`,
		},
		"JavaScript": {
			`const\s+[a-zA-Z0-9_]+\s*=`,
			`let\s+[a-zA-Z0-9_]+\s*=`,
			`var\s+[a-zA-Z0-9_]+\s*=`,
			`function\s+[a-zA-Z0-9_]+\s*\(.*\)\s*{`,
			`export\s+default\s+`,
			`import\s+.*\s+from\s+['\"](.*)['\"]`,
			`document\.getElementById`,
		},
		"Go": {
			`package\s+[a-zA-Z0-9_]+`,
			`import\s+\(.*\)`,
			`func\s+[A-Z][a-zA-Z0-9_]*\s*\(.*\)\s*\{`,
			`type\s+[A-Z][a-zA-Z0-9_]*\s+struct\s*\{`,
		},
		"Java": {
			`public\s+class\s+[A-Z][a-zA-Z0-9_]*`,
			`private\s+class\s+[A-Z][a-zA-Z0-9_]*`,
			`import\s+java\.`,
			`public\s+static\s+void\s+main\s*\(\s*String\s*\[\]\s*args\s*\)`,
		},
		"C++": {
			`#include\s+<[a-zA-Z0-9_\.]+>`,
			`class\s+[A-Z][a-zA-Z0-9_]*\s*\{`,
			`std::`,
			`template\s*<\s*typename`,
		},
		"C": {
			`#include\s+<[a-zA-Z0-9_\.]+\.h>`,
			`int\s+main\s*\(\s*int\s+argc\s*,\s*char\s*\*\s*argv\s*\[\]\s*\)`,
			`void\s+[a-zA-Z0-9_]+\s*\(.*\)\s*\{`,
		},
		"Rust": {
			`fn\s+[a-zA-Z0-9_]+\s*\(.*\)`,
			`let\s+mut\s+[a-zA-Z0-9_]+`,
			`use\s+std::`,
			`impl\s+[A-Z][a-zA-Z0-9_]*`,
			`pub\s+struct\s+[A-Z][a-zA-Z0-9_]*`,
		},
		"Ruby": {
			`require\s+['\"](.*)['\"]`,
			`def\s+[a-z_]+.*$`,
			`class\s+[A-Z][a-zA-Z0-9_]*\s+<`,
			`module\s+[A-Z][a-zA-Z0-9_]*`,
		},
		"PHP": {
			`<\?php`,
			`function\s+[a-zA-Z0-9_]+\s*\(.*\)\s*\{`,
			`class\s+[A-Z][a-zA-Z0-9_]*`,
			`\$[a-zA-Z0-9_]+\s*=`,
		},
		"HTML": {
			`<!DOCTYPE\s+html>`,
			`<html[\s>]`,
			`<head>`,
			`<body>`,
		},
	}

	scores := make(map[string]int)
	for lang, regexList := range patterns {
		for _, pattern := range regexList {
			reg := regexp.MustCompile(pattern)
			if reg.MatchString(content) {
				scores[lang]++
			}
		}
	}

	// Find the language with the highest score
	maxScore := 0
	bestLang := "Unknown"
	for lang, score := range scores {
		if score > maxScore {
			maxScore = score
			bestLang = lang
		}
	}

	// Calculate confidence level (0.0 to 1.0)
	confidence := 0.0
	if bestLang != "Unknown" && len(patterns[bestLang]) > 0 {
		confidence = float64(maxScore) / float64(len(patterns[bestLang]))
	}

	return bestLang, confidence
}

// readFileSample reads a sample of a file for content analysis
func (s *CodeService) readFileSample(filePath string, maxBytes int) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	return string(buf[:n]), nil
}

// Helper function to check if a string slice contains a string
func containsString(slice []string, str string) bool {
	for _, item := range slice {
		if item == str {
			return true
		}
	}
	return false
}

// handleIdentifyBuildSystem identifies the build system used in a project directory
func (s *CodeService) handleIdentifyBuildSystem(args map[string]any) (any, error) {
	directory, ok := args["directory"].(string)
	if !ok || directory == "" {
		return nil, fmt.Errorf("directory is required")
	}

	// Check if we have a cached result for this directory
	if buildSystem, exists := s.buildSystemCache[directory]; exists {
		return map[string]any{
			"build_system": buildSystem,
			"from_cache":  true,
		}, nil
	}

	// Check if the directory exists
	fileInfo, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to access directory: %v", err)
	}

	if !fileInfo.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", directory)
	}

	// Detect the build system
	buildSystem, buildFiles, err := s.detectBuildSystem(directory)
	if err != nil {
		return nil, err
	}

	// Cache the result
	s.buildSystemCache[directory] = buildSystem

	return map[string]any{
		"build_system":   buildSystem,
		"build_files":    buildFiles,
		"directory":      directory,
		"build_commands": s.getBuildCommands(buildSystem),
	}, nil
}

// detectBuildSystem identifies the build system from a directory
func (s *CodeService) detectBuildSystem(dirPath string) (string, []string, error) {
	// Define build system identifiers (files that indicate a build system)
	buildSystemFiles := map[string][]string{
		"Make":         {"Makefile", "makefile", "GNUmakefile"},
		"CMake":        {"CMakeLists.txt"},
		"Meson":        {"meson.build"},
		"Bazel":        {"WORKSPACE", "BUILD", "BUILD.bazel"},
		"Gradle":       {"build.gradle", "build.gradle.kts"},
		"Maven":        {"pom.xml"},
		"npm":          {"package.json"},
		"Yarn":         {"yarn.lock"},
		"Cargo":        {"Cargo.toml"},
		"Go Modules":   {"go.mod"},
		"pip":          {"requirements.txt", "setup.py"},
		"Poetry":       {"pyproject.toml"},
		"SBT":          {"build.sbt"},
		"Ant":          {"build.xml"},
		"Buck":         {"BUCK"},
		"Visual Studio": {"*.sln", "*.vcxproj"},
		"Xcode":        {"*.xcodeproj", "*.xcworkspace"},
		"qmake":        {"*.pro"},
		"Autotools":    {"configure.ac", "configure.in", "autogen.sh"},
		"Rake":         {"Rakefile"},
		"SCons":        {"SConstruct", "Sconstruct", "sconstruct", "SConscript"},
		"waf":          {"wscript"},
	}

	// Store found build files
	foundBuildFiles := []string{}
	buildSystemFound := "Unknown"

	// Check for each build system file
	for system, files := range buildSystemFiles {
		for _, file := range files {
			// Handle glob patterns (files with *)
			if strings.Contains(file, "*") {
				matches, err := filepath.Glob(filepath.Join(dirPath, file))
				if err == nil && len(matches) > 0 {
					buildSystemFound = system
					for _, match := range matches {
						foundBuildFiles = append(foundBuildFiles, match)
					}
					break
				}
			} else {
				// Check for exact file match
				filePath := filepath.Join(dirPath, file)
				if _, err := os.Stat(filePath); err == nil {
					buildSystemFound = system
					foundBuildFiles = append(foundBuildFiles, filePath)
					break
				}
			}
		}

		// If we found a build system, no need to check others
		if buildSystemFound != "Unknown" {
			break
		}
	}

	// If no build system is detected, check for language-specific conventions
	if buildSystemFound == "Unknown" {
		// Check if it might be a simple project for a specific language
		languages, err := s.identifyLanguagesInDirectory(dirPath)
		if err == nil && len(languages) > 0 {
			primaryLang := languages[0]["name"].(string)
			switch primaryLang {
			case "Go":
				// Check for Go files with main package
				goFiles, _ := filepath.Glob(filepath.Join(dirPath, "*.go"))
				if len(goFiles) > 0 {
					buildSystemFound = "Go"
					foundBuildFiles = append(foundBuildFiles, goFiles[0])
				}
			case "Python":
				// Check for Python files
				pyFiles, _ := filepath.Glob(filepath.Join(dirPath, "*.py"))
				if len(pyFiles) > 0 {
					buildSystemFound = "Python"
					foundBuildFiles = append(foundBuildFiles, pyFiles[0])
				}
			case "JavaScript", "TypeScript":
				// Check for package.json one more time (in case it was missed)
				if _, err := os.Stat(filepath.Join(dirPath, "package.json")); err == nil {
					buildSystemFound = "npm"
					foundBuildFiles = append(foundBuildFiles, filepath.Join(dirPath, "package.json"))
				} else {
					// Just a plain JS/TS project
					jsFiles, _ := filepath.Glob(filepath.Join(dirPath, "*.js"))
					tsFiles, _ := filepath.Glob(filepath.Join(dirPath, "*.ts"))
					if len(jsFiles) > 0 {
						buildSystemFound = "JavaScript"
						foundBuildFiles = append(foundBuildFiles, jsFiles[0])
					} else if len(tsFiles) > 0 {
						buildSystemFound = "TypeScript"
						foundBuildFiles = append(foundBuildFiles, tsFiles[0])
					}
				}
			}
		}
	}

	return buildSystemFound, foundBuildFiles, nil
}

// getBuildCommands returns common commands for a build system
func (s *CodeService) getBuildCommands(buildSystem string) map[string]string {
	commands := map[string]map[string]string{
		"Make": {
			"build":   "make",
			"clean":   "make clean",
			"install": "make install",
			"test":    "make test",
		},
		"CMake": {
			"configure": "cmake .",
			"build":     "cmake --build .",
			"clean":     "cmake --build . --target clean",
			"install":   "cmake --install .",
			"test":      "ctest",
		},
		"Meson": {
			"configure": "meson setup builddir",
			"build":     "ninja -C builddir",
			"clean":     "ninja -C builddir clean",
			"install":   "ninja -C builddir install",
			"test":      "ninja -C builddir test",
		},
		"Bazel": {
			"build":   "bazel build //...",
			"clean":   "bazel clean",
			"test":    "bazel test //...",
			"run":     "bazel run <target>",
		},
		"Gradle": {
			"build":   "./gradlew build",
			"clean":   "./gradlew clean",
			"test":    "./gradlew test",
			"run":     "./gradlew run",
		},
		"Maven": {
			"build":   "mvn compile",
			"clean":   "mvn clean",
			"test":    "mvn test",
			"package": "mvn package",
			"install": "mvn install",
		},
		"npm": {
			"build":     "npm run build",
			"test":      "npm test",
			"start":     "npm start",
			"install":   "npm install",
			"dev":       "npm run dev",
		},
		"Yarn": {
			"build":     "yarn build",
			"test":      "yarn test",
			"start":     "yarn start",
			"install":   "yarn install",
			"dev":       "yarn dev",
		},
		"Cargo": {
			"build":   "cargo build",
			"clean":   "cargo clean",
			"test":    "cargo test",
			"run":     "cargo run",
			"release": "cargo build --release",
		},
		"Go Modules": {
			"build":   "go build",
			"test":    "go test",
			"run":     "go run .",
			"install": "go install",
		},
		"Go": {
			"build":   "go build",
			"test":    "go test",
			"run":     "go run .",
			"install": "go install",
		},
		"pip": {
			"install": "pip install -e .",
			"test":    "pytest",
		},
		"Python": {
			"run":  "python <file>.py",
			"test": "pytest",
		},
		"Poetry": {
			"install": "poetry install",
			"build":   "poetry build",
			"run":     "poetry run python <file>.py",
			"test":    "poetry run pytest",
		},
		"JavaScript": {
			"run": "node <file>.js",
		},
		"TypeScript": {
			"build": "tsc",
			"run":   "ts-node <file>.ts",
		},
		"Autotools": {
			"configure": "./configure",
			"build":     "make",
			"clean":     "make clean",
			"install":   "make install",
			"setup":     "autoreconf -i",
		},
	}

	if cmds, found := commands[buildSystem]; found {
		return cmds
	}

	// Return empty map for unknown build systems
	return map[string]string{}
}

// handleListFunctions lists functions in a file or directory
func (s *CodeService) handleListFunctions(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	recursive := false
	if recursiveArg, ok := args["recursive"].(bool); ok {
		recursive = recursiveArg
	}

	// Get file info
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %v", err)
	}

	var functions []map[string]any

	if fileInfo.IsDir() {
		// Process directory
		functions, err = s.listFunctionsInDirectory(path, recursive)
	} else {
		// Process single file
		fileFunctions, err := s.listFunctionsInFile(path)
		if err != nil {
			return nil, err
		}
		functions = fileFunctions
	}

	return map[string]any{
		"functions": functions,
		"count":     len(functions),
	}, nil
}

// listFunctionsInDirectory finds all functions in a directory
func (s *CodeService) listFunctionsInDirectory(dirPath string, recursive bool) ([]map[string]any, error) {
	allFunctions := []map[string]any{}

	// Walk through directory if recursive, otherwise just list files in the directory
	var filesToProcess []string

	if recursive {
		err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip directories and hidden files
			if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
				return nil
			}

			// Check if this is likely a source code file
			if s.isSourceCodeFile(path) {
				filesToProcess = append(filesToProcess, path)
			}

			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("error walking directory: %v", err)
		}
	} else {
		// Just process files in the current directory
		files, err := ioutil.ReadDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("error reading directory: %v", err)
		}

		for _, file := range files {
			if !file.IsDir() && s.isSourceCodeFile(filepath.Join(dirPath, file.Name())) {
				filesToProcess = append(filesToProcess, filepath.Join(dirPath, file.Name()))
			}
		}
	}

	// Process each file
	for _, file := range filesToProcess {
		functions, err := s.listFunctionsInFile(file)
		if err != nil {
			// Skip files that can't be processed
			continue
		}
		
		for _, function := range functions {
			// Add relative file path
			relPath, _ := filepath.Rel(dirPath, file)
			function["file_path"] = relPath
			allFunctions = append(allFunctions, function)
		}
	}

	return allFunctions, nil
}

// listFunctionsInFile extracts functions from a single file
func (s *CodeService) listFunctionsInFile(filePath string) ([]map[string]any, error) {
	// Identify the language first
	lang, _, err := s.identifyLanguageFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to identify language: %v", err)
	}

	// Read file content
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	// Extract functions based on language
	functions, err := s.extractFunctions(string(content), lang, filePath)
	if err != nil {
		return nil, err
	}

	return functions, nil
}

// isSourceCodeFile determines if a file is likely source code based on extension
func (s *CodeService) isSourceCodeFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	codeFileExtensions := map[string]bool{
		".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true,
		".hpp": true, ".hxx": true, ".go": true, ".py": true, ".js": true,
		".jsx": true, ".ts": true, ".tsx": true, ".java": true, ".kt": true,
		".rb": true, ".php": true, ".swift": true, ".m": true, ".rs": true,
		".scala": true, ".cs": true, ".fs": true, ".hs": true, ".clj": true,
		".ex": true, ".exs": true, ".erl": true, ".ml": true, ".mli": true,
		".pl": true, ".pm": true, ".r": true, ".sh": true, ".bash": true,
		".lua": true, ".tcl": true, ".groovy": true, ".dart": true,
	}

	return codeFileExtensions[ext]
}

// extractFunctions extracts function definitions from code using regex patterns for various languages
func (s *CodeService) extractFunctions(content, language, filePath string) ([]map[string]any, error) {
	var functions []map[string]any

	// Function extraction patterns for different languages
	patterns := map[string]string{
		"Go": `func\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*(\([^)]*\))?\s*\{`,
		"Python": `def\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*:`,
		"JavaScript": `(?:function\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)|const\s+([A-Za-z0-9_]+)\s*=\s*\(([^)]*)\)\s*=>|([A-Za-z0-9_]+)\s*:\s*function\s*\(([^)]*)\))`,
		"TypeScript": `(?:function\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)|const\s+([A-Za-z0-9_]+)\s*=\s*\(([^)]*)\)\s*=>|([A-Za-z0-9_]+)\s*:\s*function\s*\(([^)]*)\))`,
		"Java": `(?:[public|private|protected]\s+)?(?:static\s+)?(?:[a-zA-Z0-9_<>\[\]]+)\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*(?:throws\s+[A-Za-z0-9_, ]+)?\s*\{`,
		"C": `(?:[a-zA-Z0-9_]+)\s+([A-Za-z0-9_]+)\s*\(([^;]*)\)\s*\{`,
		"C++": `(?:[a-zA-Z0-9_:]+)\s+([A-Za-z0-9_]+)\s*\(([^;]*)\)(?:\s*const)?\s*\{`,
		"Ruby": `def\s+([A-Za-z0-9_?.!]+)(?:\(([^)]*)\))?\s*`,
		"Rust": `fn\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)(?:\s*->\s*[^{]+)?\s*\{`,
		"PHP": `function\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*\{`,
	}

	// Get the pattern for the language
	pattern, found := patterns[language]
	if !found {
		// If language is not directly supported, try to use a similar language's pattern
		switch language {
			case "C#":
				pattern = patterns["Java"] // C# has similar syntax to Java
			case "Kotlin":
				pattern = patterns["Java"] // Kotlin has somewhat similar syntax to Java
			case "Swift":
				pattern = `func\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*(?:->\s*[^{]+)?\s*\{`
			default:
				// Use a very generic pattern as fallback
				pattern = `(?:function|def|func)\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)`
		}
	}

	// Compile regex
	reg, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex pattern for %s: %v", language, err)
	}

	// Find all matches
	matches := reg.FindAllStringSubmatch(content, -1)
	// Count number of lines in the content
	_ = strings.Split(content, "\n")

	// Process matches into function definitions
	for _, match := range matches {
		// Skip if no function name captured (should not happen with our patterns)
		if len(match) < 2 || match[1] == "" {
			continue
		}

		// Find the line number of the function
		lineNumber := s.findLineNumber(content, match[0])
		
		// Extract parameters
		var params string
		if len(match) >= 3 {
			params = match[2]
		}

		// Create function info
		functionInfo := map[string]any{
			"name":       match[1],
			"parameters": params,
			"line":       lineNumber,
			"language":   language,
		}

		// Add to result
		functions = append(functions, functionInfo)
	}

	return functions, nil
}

// findLineNumber returns the line number where the given text starts
func (s *CodeService) findLineNumber(content, text string) int {
	lines := strings.Split(content, "\n")
	lineNumber := 0
	
	for i, line := range lines {
		if strings.Contains(line, text[:min(len(text), 40)]) { // Use a substring to avoid very long matches
			lineNumber = i + 1 // 1-based line numbering
			break
		}
	}

	return lineNumber
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// handleGetFunctionBody retrieves the full function body
func (s *CodeService) handleGetFunctionBody(args map[string]any) (any, error) {
	functionName, ok := args["function_name"].(string)
	if !ok || functionName == "" {
		return nil, fmt.Errorf("function_name is required")
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	recursive := false
	if recursiveArg, ok := args["recursive"].(bool); ok {
		recursive = recursiveArg
	}

	// Get file info
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %v", err)
	}

	var functionDetails map[string]any

	if fileInfo.IsDir() {
		// Find function in directory
		functionDetails, err = s.findFunctionInDirectory(path, functionName, recursive)
		if err != nil {
			return nil, err
		}
	} else {
		// Find function in a single file
		functionDetails, err = s.findFunctionInFile(path, functionName)
		if err != nil {
			return nil, err
		}
	}

	return functionDetails, nil
}

// findFunctionInDirectory locates a function in a directory
func (s *CodeService) findFunctionInDirectory(dirPath, functionName string, recursive bool) (map[string]any, error) {
	// List all functions in directory
	functions, err := s.listFunctionsInDirectory(dirPath, recursive)
	if err != nil {
		return nil, err
	}

	// Find the specific function
	var targetFunction map[string]any
	var filePath string

	for _, function := range functions {
		name, _ := function["name"].(string)
		if name == functionName {
			targetFunction = function
			filePath = filepath.Join(dirPath, function["file_path"].(string))
			break
		}
	}

	if targetFunction == nil {
		return nil, fmt.Errorf("function '%s' not found", functionName)
	}

	// Extract the full function body
	functionBody, err := s.extractFunctionBody(filePath, targetFunction)
	if err != nil {
		return nil, err
	}

	targetFunction["body"] = functionBody
	// Convert relative path to absolute
	targetFunction["file_path"] = filePath

	return targetFunction, nil
}

// findFunctionInFile locates a function in a single file
func (s *CodeService) findFunctionInFile(filePath, functionName string) (map[string]any, error) {
	// List all functions in the file
	functions, err := s.listFunctionsInFile(filePath)
	if err != nil {
		return nil, err
	}

	// Find the specific function
	var targetFunction map[string]any

	for _, function := range functions {
		name, _ := function["name"].(string)
		if name == functionName {
			targetFunction = function
			break
		}
	}

	if targetFunction == nil {
		return nil, fmt.Errorf("function '%s' not found in %s", functionName, filePath)
	}

	// Extract the full function body
	functionBody, err := s.extractFunctionBody(filePath, targetFunction)
	if err != nil {
		return nil, err
	}

	targetFunction["body"] = functionBody
	targetFunction["file_path"] = filePath

	return targetFunction, nil
}

// extractFunctionBody retrieves the full body of a function
func (s *CodeService) extractFunctionBody(filePath string, functionInfo map[string]any) (string, error) {
	// Read the file
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	lines := strings.Split(string(content), "\n")

	// Get the line where the function starts
	lineNum, ok := functionInfo["line"].(int)
	if !ok || lineNum <= 0 || lineNum > len(lines) {
		return "", fmt.Errorf("invalid line number")
	}

	// Determine the language to know how to extract the function body
	language, _ := functionInfo["language"].(string)

	// Extract the function body based on language
	return s.extractBodyByLanguage(lines, lineNum-1, language) // lineNum is 1-based, convert to 0-based
}

// extractBodyByLanguage extracts a function body based on language-specific rules
func (s *CodeService) extractBodyByLanguage(lines []string, startLineIndex int, language string) (string, error) {
	// Different languages have different ways to determine function body boundaries
	switch language {
	// For C-like languages (C, C++, Java, JavaScript, Go, etc.)
	case "C", "C++", "Java", "JavaScript", "TypeScript", "Go", "C#", "PHP", "Rust":
		return s.extractBodyBraces(lines, startLineIndex)
	
	// For Python and similar indentation-based languages
	case "Python":
		return s.extractBodyIndentation(lines, startLineIndex)
	
	// For Ruby which uses various end markers
	case "Ruby":
		return s.extractBodyRubyStyle(lines, startLineIndex)
	
	// Default to braces for unknown languages
	default:
		return s.extractBodyBraces(lines, startLineIndex)
	}
}

// extractBodyBraces extracts function body for languages using braces {} for blocks
func (s *CodeService) extractBodyBraces(lines []string, startLineIndex int) (string, error) {
	if startLineIndex >= len(lines) {
		return "", fmt.Errorf("start line index out of bounds")
	}

	// Find the opening brace if it's not on the first line
	openingLine := startLineIndex
	for openingLine < len(lines) && !strings.Contains(lines[openingLine], "{") {
		openingLine++
	}

	if openingLine >= len(lines) {
		return strings.Join(lines[startLineIndex:], "\n"), nil // Return what we have if no opening brace found
	}

	// Count braces to find matching closing brace
	braceCount := 0
	endLine := openingLine

	for i := openingLine; i < len(lines); i++ {
		line := lines[i]
		
		// Count opening braces
		for _, char := range line {
			if char == '{' {
				braceCount++
			} else if char == '}' {
				braceCount--
				// If braces balance out, we've found the end of the function
				if braceCount == 0 {
					endLine = i
					break
				}
			}
		}
		
		if braceCount == 0 && i > openingLine {
			endLine = i
			break
		}
	}

	// Extract the function body including the braces
	return strings.Join(lines[startLineIndex:endLine+1], "\n"), nil
}

// extractBodyIndentation extracts function body for indentation-based languages like Python
func (s *CodeService) extractBodyIndentation(lines []string, startLineIndex int) (string, error) {
	if startLineIndex >= len(lines) {
		return "", fmt.Errorf("start line index out of bounds")
	}

	// Get base indentation of the function definition line
	functionIndent := s.getIndentLevel(lines[startLineIndex])
	
	// The body starts on the next line
	startBodyLine := startLineIndex + 1
	if startBodyLine >= len(lines) {
		return lines[startLineIndex], nil // Only the definition line is available
	}

	// Find the end of the function (where indentation returns to base level or less)
	endLine := startBodyLine
	for i := startBodyLine; i < len(lines); i++ {
		// Skip empty lines
		if strings.TrimSpace(lines[i]) == "" {
			endLine = i
			continue
		}
		
		// If indentation is less than or equal to the base, we've exited the function
		currentIndent := s.getIndentLevel(lines[i])
		if currentIndent <= functionIndent {
			endLine = i - 1 // The previous line was the last line of the function
			break
		}
		
		endLine = i
	}

	// Extract the function body including the definition line
	return strings.Join(lines[startLineIndex:endLine+1], "\n"), nil
}

// extractBodyRubyStyle extracts function body for Ruby which uses 'end' to terminate blocks
func (s *CodeService) extractBodyRubyStyle(lines []string, startLineIndex int) (string, error) {
	if startLineIndex >= len(lines) {
		return "", fmt.Errorf("start line index out of bounds")
	}

	// Start from the function definition line
	startLine := startLineIndex
	
	// Look for the 'end' keyword at the appropriate indentation level
	_ = s.getIndentLevel(lines[startLineIndex])
	endLine := startLine

	// Count block starters and enders
	blockDepth := 1 // Start with 1 for the function itself

	for i := startLine + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		
		// Count block starters
		if strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "class ") || 
		   strings.HasPrefix(line, "module ") || strings.HasPrefix(line, "if ") || 
		   strings.HasPrefix(line, "unless ") || strings.HasPrefix(line, "case ") || 
		   strings.HasPrefix(line, "while ") || strings.HasPrefix(line, "for ") || 
		   strings.HasPrefix(line, "begin") || strings.HasPrefix(line, "do ") {
			blockDepth++
		}
		
		// Check for end
		if strings.HasPrefix(line, "end") {
			blockDepth--
			if blockDepth == 0 {
				endLine = i
				break
			}
		}
		
		endLine = i
	}

	// Extract the function body including the definition and end
	return strings.Join(lines[startLineIndex:endLine+1], "\n"), nil
}

// getIndentLevel calculates the indentation level of a line
func (s *CodeService) getIndentLevel(line string) int {
	indent := 0
	for _, char := range line {
		if char == ' ' {
			indent++
		} else if char == '\t' {
			indent += 4 // Count a tab as 4 spaces
		} else {
			break
		}
	}
	return indent
}

// handleQueryStructure queries structure information in a file or directory
func (s *CodeService) handleQueryStructure(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	recursive := false
	if recursiveArg, ok := args["recursive"].(bool); ok {
		recursive = recursiveArg
	}

	structureName := ""
	if nameArg, ok := args["structure_name"].(string); ok {
		structureName = nameArg
	}

	// Get file info
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %v", err)
	}

	var structures []map[string]any

	if fileInfo.IsDir() {
		// Process directory
		structures, err = s.queryStructuresInDirectory(path, structureName, recursive)
		if err != nil {
			return nil, err
		}
	} else {
		// Process single file
		fileStructures, err := s.queryStructuresInFile(path, structureName)
		if err != nil {
			return nil, err
		}
		structures = fileStructures
	}

	return map[string]any{
		"structures": structures,
		"count":      len(structures),
	}, nil
}

// queryStructuresInDirectory finds all structures in a directory
func (s *CodeService) queryStructuresInDirectory(dirPath, structureName string, recursive bool) ([]map[string]any, error) {
	allStructures := []map[string]any{}

	// Walk through directory if recursive, otherwise just list files in the directory
	var filesToProcess []string

	if recursive {
		err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip directories and hidden files
			if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
				return nil
			}

			// Check if this is likely a source code file
			if s.isSourceCodeFile(path) {
				filesToProcess = append(filesToProcess, path)
			}

			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("error walking directory: %v", err)
		}
	} else {
		// Just process files in the current directory
		files, err := ioutil.ReadDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("error reading directory: %v", err)
		}

		for _, file := range files {
			if !file.IsDir() && s.isSourceCodeFile(filepath.Join(dirPath, file.Name())) {
				filesToProcess = append(filesToProcess, filepath.Join(dirPath, file.Name()))
			}
		}
	}

	// Process each file
	for _, file := range filesToProcess {
		structures, err := s.queryStructuresInFile(file, structureName)
		if err != nil {
			// Skip files that can't be processed
			continue
		}
		
		for _, structure := range structures {
			// Add relative file path
			relPath, _ := filepath.Rel(dirPath, file)
			structure["file_path"] = relPath
			allStructures = append(allStructures, structure)
		}
	}

	return allStructures, nil
}

// queryStructuresInFile extracts structures from a single file
func (s *CodeService) queryStructuresInFile(filePath, structureName string) ([]map[string]any, error) {
	// Identify the language first
	lang, _, err := s.identifyLanguageFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to identify language: %v", err)
	}

	// Read file content
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	// Extract structures based on language
	structures, err := s.extractStructures(string(content), lang, filePath)
	if err != nil {
		return nil, err
	}

	// Filter by structure name if specified
	if structureName != "" {
		filtered := []map[string]any{}
		for _, structure := range structures {
			if name, ok := structure["name"].(string); ok && name == structureName {
				filtered = append(filtered, structure)
			}
		}
		structures = filtered
	}

	return structures, nil
}

// extractStructures extracts structure definitions from code using regex patterns for various languages
func (s *CodeService) extractStructures(content, language, filePath string) ([]map[string]any, error) {
	var structures []map[string]any

	// Structure extraction patterns for different languages
	patterns := map[string]string{
		"Go": `type\s+([A-Za-z0-9_]+)\s+struct\s*\{`,
		"Python": `class\s+([A-Za-z0-9_]+)(?:\(([^)]*)\))?\s*:`,
		"JavaScript": `class\s+([A-Za-z0-9_]+)(?:\s+extends\s+([A-Za-z0-9_]+))?\s*\{`,
		"TypeScript": `(?:interface|class|enum)\s+([A-Za-z0-9_]+)(?:\s+extends\s+([A-Za-z0-9_]+))?\s*\{`,
		"Java": `(?:class|interface|enum)\s+([A-Za-z0-9_]+)(?:\s+extends\s+([A-Za-z0-9_]+))?(?:\s+implements\s+([A-Za-z0-9_, ]+))?\s*\{`,
		"C": `(?:struct|union|enum)\s+([A-Za-z0-9_]+)\s*\{`,
		"C++": `(?:class|struct|union|enum)\s+([A-Za-z0-9_]+)(?:\s*:\s*(?:public|private|protected)\s+([A-Za-z0-9_]+))?\s*\{`,
		"Ruby": `(?:class|module)\s+([A-Za-z0-9_:]+)(?:\s+<\s+([A-Za-z0-9_:]+))?`,
		"Rust": `(?:struct|enum|trait|impl)\s+([A-Za-z0-9_]+)(?:\s+for\s+([A-Za-z0-9_]+))?\s*\{`,
		"PHP": `(?:class|interface|trait)\s+([A-Za-z0-9_]+)(?:\s+extends\s+([A-Za-z0-9_]+))?(?:\s+implements\s+([A-Za-z0-9_, ]+))?\s*\{`,
	}

	// Get the pattern for the language
	pattern, found := patterns[language]
	if !found {
		// If language is not directly supported, try to use a similar language's pattern
		switch language {
			case "C#":
				pattern = patterns["Java"] // C# has similar syntax to Java
			case "Kotlin":
				pattern = `(?:class|interface|object|data class)\s+([A-Za-z0-9_]+)(?:\s*:\s*([A-Za-z0-9_]+))?\s*(?:\(|\{)`
			case "Swift":
				pattern = `(?:class|struct|enum|protocol|extension)\s+([A-Za-z0-9_]+)(?:\s*:\s*([A-Za-z0-9_, ]+))?\s*\{`
			default:
				// Use a very generic pattern as fallback
				pattern = `(?:class|struct|interface|enum)\s+([A-Za-z0-9_]+)`
		}
	}

	// Compile regex
	reg, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex pattern for %s: %v", language, err)
	}

	// Find all matches
	matches := reg.FindAllStringSubmatch(content, -1)

	// Process matches into structure definitions
	for _, match := range matches {
		// Skip if no structure name captured (should not happen with our patterns)
		if len(match) < 2 || match[1] == "" {
			continue
		}

		// Find the line number of the structure
		lineNumber := s.findLineNumber(content, match[0])
		
		// Create structure info
		structureInfo := map[string]any{
			"name":     match[1],
			"type":     s.getStructureType(language, match[0]),
			"line":     lineNumber,
			"language": language,
		}

		// Add inheritance/implementation info if available
		if len(match) >= 3 && match[2] != "" {
			structureInfo["extends"] = match[2]
		}

		// Add to result
		structures = append(structures, structureInfo)
	}

	return structures, nil
}

// getStructureType determines the type of structure (class, interface, enum, etc.)
func (s *CodeService) getStructureType(language, declaration string) string {
	// Check for specific structure types based on language
	switch language {
		case "Go":
			if strings.Contains(declaration, "interface") {
				return "interface"
			}
			return "struct"
		
		case "Python":
			return "class"
		
		case "JavaScript", "TypeScript":
			if strings.Contains(declaration, "interface") {
				return "interface"
			} else if strings.Contains(declaration, "enum") {
				return "enum"
			}
			return "class"
		
		case "Java", "C#":
			if strings.Contains(declaration, "interface") {
				return "interface"
			} else if strings.Contains(declaration, "enum") {
				return "enum"
			}
			return "class"
		
		case "C", "C++":
			if strings.Contains(declaration, "class") {
				return "class"
			} else if strings.Contains(declaration, "union") {
				return "union"
			} else if strings.Contains(declaration, "enum") {
				return "enum"
			}
			return "struct"
		
		case "Ruby":
			if strings.Contains(declaration, "module") {
				return "module"
			}
			return "class"
		
		case "Rust":
			if strings.Contains(declaration, "enum") {
				return "enum"
			} else if strings.Contains(declaration, "trait") {
				return "trait"
			} else if strings.Contains(declaration, "impl") {
				return "impl"
			}
			return "struct"
		
		case "PHP":
			if strings.Contains(declaration, "interface") {
				return "interface"
			} else if strings.Contains(declaration, "trait") {
				return "trait"
			}
			return "class"
		
		default:
			return "structure"
	}
}

// handleFileChangeCheck checks if a file has been modified since it was last checked
func (s *CodeService) handleFileChangeCheck(args map[string]any) (any, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	// Check if the file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to access file: %v", err)
	}

	// Get current modification time
	currentModTime := fileInfo.ModTime()

	// Check if we have a previous mod time for this file
	lastModTime, hasRecord := s.fileModTimes[filePath]

	// Update our record of the mod time
	s.fileModTimes[filePath] = currentModTime

	response := map[string]any{
		"file_path":          filePath,
		"current_mod_time":   currentModTime.Format(time.RFC3339),
		"has_previous_check": hasRecord,
	}

	if hasRecord {
		// Check if the file has been modified
		hasChanged := !currentModTime.Equal(lastModTime)
		response["has_changed"] = hasChanged
		response["last_mod_time"] = lastModTime.Format(time.RFC3339)
	} else {
		// First time checking this file
		response["has_changed"] = false
		response["first_check"] = true
	}

	return response, nil
}

// handleApplyPatch applies a patch to a file by replacing specific lines
func (s *CodeService) handleApplyPatch(args map[string]any) (any, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	startLine, ok := args["start_line"].(float64)
	if !ok {
		return nil, fmt.Errorf("start_line is required and must be a number")
	}

	endLine, ok := args["end_line"].(float64)
	if !ok {
		return nil, fmt.Errorf("end_line is required and must be a number")
	}

	newContent, ok := args["new_content"].(string)
	if !ok {
		return nil, fmt.Errorf("new_content is required")
	}

	// Convert to int
	startLineInt := int(startLine)
	endLineInt := int(endLine)

	// Validate line numbers
	if startLineInt <= 0 || endLineInt < startLineInt {
		return nil, fmt.Errorf("invalid line numbers: start_line must be positive and end_line must be >= start_line")
	}

	// Check if the file exists
	_, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to access file: %v", err)
	}

	// Read the file
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	// Split into lines
	lines := strings.Split(string(content), "\n")

	// Check if line numbers are valid
	if startLineInt > len(lines) {
		return nil, fmt.Errorf("start_line %d exceeds file length of %d lines", startLineInt, len(lines))
	}

	// Cap end_line to file length if it exceeds
	if endLineInt > len(lines) {
		endLineInt = len(lines)
	}

	// Create a backup of the file before modifying
	backupFilePath := filePath + ".bak"
	err = ioutil.WriteFile(backupFilePath, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create backup file: %v", err)
	}

	// Apply the patch - replace the specified lines with the new content
	newLines := strings.Split(newContent, "\n")
	
	// Convert from 1-based to 0-based indexing
	startIdx := startLineInt - 1
	endIdx := endLineInt - 1

	// Construct the new file content
	resultLines := append(lines[:startIdx], newLines...)
	if endIdx+1 < len(lines) {
		resultLines = append(resultLines, lines[endIdx+1:]...)
	}
	
	// Join lines back into a single string
	resultContent := strings.Join(resultLines, "\n")

	// Write the result back to the file
	err = ioutil.WriteFile(filePath, []byte(resultContent), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write patched file: %v", err)
	}

	// Update the mod time in our cache
	if fileInfo, err := os.Stat(filePath); err == nil {
		s.fileModTimes[filePath] = fileInfo.ModTime()
	}

	return map[string]any{
		"success":          true,
		"file_path":        filePath,
		"lines_replaced":   endLineInt - startLineInt + 1,
		"new_lines_count":  len(newLines),
		"backup_file":      backupFilePath,
	}, nil
}

// handleCompile compiles a project or file using the appropriate build system
func (s *CodeService) handleCompile(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Get optional parameters
	options := ""
	if optionsArg, ok := args["options"].(string); ok {
		options = optionsArg
	}

	explicitBuildSystem := ""
	if bsArg, ok := args["build_system"].(string); ok {
		explicitBuildSystem = bsArg
	}

	// Check if path exists
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %v", err)
	}

	// Determine if it's a file or directory
	isDir := fileInfo.IsDir()

	// If a file is provided, use its parent directory for build system detection
	buildSystemPath := path
	if !isDir {
		buildSystemPath = filepath.Dir(path)
	}

	// Detect the build system if not explicitly provided
	buildSystem := explicitBuildSystem

	if buildSystem == "" {
		var err error
		buildSystem, _, err = s.detectBuildSystem(buildSystemPath)
		if err != nil {
			return nil, fmt.Errorf("failed to detect build system: %v", err)
		}
	}

	// Handle special cases for single file compilation
	if !isDir {
		// If it's a single file and no build system was detected or an incompatible build system was detected,
		// try to compile the file directly based on its language
		if buildSystem == "Unknown" || s.isSingleFileCompilation(buildSystem) {
			return s.compileSingleFile(path, options)
		}
	}

	// Get build commands for the detected build system
	buildCommands := s.getBuildCommands(buildSystem)
	buildCommand, hasBuildCommand := buildCommands["build"]

	if !hasBuildCommand {
		return nil, fmt.Errorf("no build command available for build system: %s", buildSystem)
	}

	// Handle special case for Meson
	if buildSystem == "Meson" {
		return s.handleMesonBuild(buildSystemPath, options)
	}

	// Construct the final build command
	fullCommand := buildCommand
	if options != "" {
		fullCommand = fullCommand + " " + options
	}

	// Execute the build command in the correct directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Change to the build system directory
	err = os.Chdir(buildSystemPath)
	if err != nil {
		return nil, fmt.Errorf("failed to change to build directory: %v", err)
	}

	// Create and execute the command
	cmd := exec.Command("sh", "-c", fullCommand)
	cmdOutput, err := cmd.CombinedOutput()

	// Change back to the original directory
	changeBackErr := os.Chdir(currentDir)
	if changeBackErr != nil {
		// Log this error but continue with the response
		fmt.Printf("warning: failed to change back to original directory: %v\n", changeBackErr)
	}

	// Check if the build command succeeded
	success := err == nil
	result := map[string]any{
		"build_system":  buildSystem,
		"build_command": fullCommand,
		"success":       success,
		"output":        string(cmdOutput),
	}

	if !success {
		result["error"] = err.Error()
	}

	return result, nil
}

// isSingleFileCompilation determines if a build system supports compiling single files directly
func (s *CodeService) isSingleFileCompilation(buildSystem string) bool {
	// List of build systems that work on single files
	singleFileSystems := map[string]bool{
		"Go":         true,
		"Python":     true,
		"JavaScript": true,
		"TypeScript": true,
		"C":          true,
		"C++":        true,
		"Rust":       true,
	}

	return singleFileSystems[buildSystem]
}

// compileSingleFile handles compilation of a single file
func (s *CodeService) compileSingleFile(filePath, options string) (map[string]any, error) {
	// Identify the language of the file
	lang, _, err := s.identifyLanguageFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to identify language: %v", err)
	}

	// Determine compilation command based on language
	var compileCmd string
	switch lang {
		case "C":
			// Determine output file name
			baseName := filepath.Base(filePath)
			outputName := strings.TrimSuffix(baseName, filepath.Ext(baseName))
			compileCmd = fmt.Sprintf("gcc -o %s %s %s", outputName, filePath, options)
		
		case "C++":
			// Determine output file name
			baseName := filepath.Base(filePath)
			outputName := strings.TrimSuffix(baseName, filepath.Ext(baseName))
			compileCmd = fmt.Sprintf("g++ -o %s %s %s", outputName, filePath, options)
		
		case "Go":
			compileCmd = fmt.Sprintf("go build %s %s", options, filePath)
		
		case "Rust":
			compileCmd = fmt.Sprintf("rustc %s %s", filePath, options)
		
		case "Java":
			compileCmd = fmt.Sprintf("javac %s %s", filePath, options)
		
		case "TypeScript":
			compileCmd = fmt.Sprintf("tsc %s %s", filePath, options)
		
		case "Python", "JavaScript", "Ruby":
			// These are interpreted languages, no compilation needed
			return map[string]any{
				"language":    lang,
				"file_path":   filePath,
				"success":     true,
				"output":      "No compilation needed for interpreted language",
				"interpreted": true,
			}, nil
		
		default:
			return nil, fmt.Errorf("unsupported language for direct compilation: %s", lang)
	}

	// Execute the compilation command
	cmd := exec.Command("sh", "-c", compileCmd)
	cmdOutput, err := cmd.CombinedOutput()

	// Check if compilation succeeded
	success := err == nil
	result := map[string]any{
		"language":       lang,
		"file_path":      filePath,
		"compile_command": compileCmd,
		"success":        success,
		"output":         string(cmdOutput),
	}

	if !success {
		result["error"] = err.Error()
	}

	return result, nil
}

// handleMakeCmd handles the Make command
func (s *CodeService) handleMakeCmd(args map[string]any) (any, error) {
	directory, ok := args["directory"].(string)
	if !ok || directory == "" {
		return nil, fmt.Errorf("directory is required")
	}

	target := ""
	if targetArg, ok := args["target"].(string); ok {
		target = targetArg
	}

	// Check if the directory exists
	fileInfo, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to access directory: %v", err)
	}

	if !fileInfo.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", directory)
	}

	// Check if Makefile exists
	makefileFound := false
	makefileNames := []string{"Makefile", "makefile", "GNUmakefile"}
	
	for _, name := range makefileNames {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			makefileFound = true
			break
		}
	}

	if !makefileFound {
		return nil, fmt.Errorf("no Makefile found in %s", directory)
	}

	// Save current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Change to the project directory
	err = os.Chdir(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to change to project directory: %v", err)
	}

	// Construct the make command
	makeCmd := "make"
	if target != "" {
		makeCmd = makeCmd + " " + target
	}

	// Execute the make command
	cmd := exec.Command("sh", "-c", makeCmd)
	cmdOutput, err := cmd.CombinedOutput()

	// Change back to original directory
	changeBackErr := os.Chdir(currentDir)
	if changeBackErr != nil {
		// Log this error but continue with the response
		fmt.Printf("warning: failed to change back to original directory: %v\n", changeBackErr)
	}

	// Check if the make command succeeded
	success := err == nil
	result := map[string]any{
		"directory":   directory,
		"target":      target,
		"make_command": makeCmd,
		"success":     success,
		"output":      string(cmdOutput),
	}

	if !success && err != nil {
		result["error"] = err.Error()
	}

	return result, nil
}

// handleMesonBuild handles building a Meson project
func (s *CodeService) handleMesonBuild(projectDir, options string) (map[string]any, error) {
	// Save current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Change to the project directory
	err = os.Chdir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to change to project directory: %v", err)
	}

	// First check if build directory exists
	buildDirExists := false
	buildDir := "builddir" // Default Meson build directory

	// Check if builddir exists
	if _, err := os.Stat(buildDir); err == nil {
		buildDirExists = true
	}

	// If build directory doesn't exist, set it up
	output := ""
	if !buildDirExists {
		// Setup Meson build directory
		setupCmd := exec.Command("meson", "setup", buildDir)
		setupOutput, err := setupCmd.CombinedOutput()
		output += string(setupOutput) + "\n"

		if err != nil {
			// Change back to original directory
			os.Chdir(currentDir)
			return map[string]any{
				"build_system":  "Meson",
				"build_command": "meson setup " + buildDir,
				"success":       false,
				"output":        string(setupOutput),
				"error":         err.Error(),
			}, nil
		}
	}

	// Now run ninja in the build directory
	ninjaCmdStr := "ninja -C " + buildDir
	if options != "" {
		ninjaCmdStr += " " + options
	}

	ninjaCmd := exec.Command("sh", "-c", ninjaCmdStr)
	ninjaOutput, err := ninjaCmd.CombinedOutput()
	
	// Add ninja output
	output += string(ninjaOutput)

	// Change back to original directory
	os.Chdir(currentDir)

	// Check if the build command succeeded
	success := err == nil
	result := map[string]any{
		"build_system":  "Meson",
		"build_command": ninjaCmdStr,
		"build_dir":     filepath.Join(projectDir, buildDir),
		"success":       success,
		"output":        output,
	}

	if !success {
		result["error"] = err.Error()
	}

	return result, nil
}

// handleRun runs a project or file using the appropriate execution method
func (s *CodeService) handleRun(args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// Get optional parameters
	runArgs := ""
	if argsArg, ok := args["args"].(string); ok {
		runArgs = argsArg
	}

	compileFirst := false
	if compileArg, ok := args["compile_first"].(bool); ok {
		compileFirst = compileArg
	}

	// Check if path exists
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %v", err)
	}

	// Determine if it's a file or directory
	isDir := fileInfo.IsDir()

	var result map[string]any

	if isDir {
		// Handle running a project directory
		result, err = s.runProject(path, runArgs, compileFirst)
	} else {
		// Handle running a single file
		result, err = s.runFile(path, runArgs, compileFirst)
	}

	if err != nil {
		return nil, err
	}

	return result, nil
}

// runProject executes a project based on its build system
func (s *CodeService) runProject(projectPath, args string, compileFirst bool) (map[string]any, error) {
	// Detect the build system
	buildSystem, _, err := s.detectBuildSystem(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to detect build system: %v", err)
	}

	if buildSystem == "Unknown" {
		return nil, fmt.Errorf("could not determine how to run the project: no build system detected")
	}

	// If compile first is requested, compile the project
	if compileFirst {
		compileResult, err := s.handleCompile(map[string]any{
			"path": projectPath,
		})
		
		if err != nil {
			return nil, fmt.Errorf("failed to compile project: %v", err)
		}
		
		// If compilation failed, return the error
		if compileResultMap, ok := compileResult.(map[string]any); ok {
			if success, ok := compileResultMap["success"].(bool); ok && !success {
				return compileResultMap, nil
			}
		}
	}

	// Get run command for the build system
	buildCommands := s.getBuildCommands(buildSystem)
	runCommand, hasRunCommand := buildCommands["run"]

	// Handle special cases for specific build systems
	switch buildSystem {
		case "Meson":
			return s.runMesonProject(projectPath, args)
		
		case "npm", "Yarn":
			// For JavaScript/Node projects, prefer "start" or "dev" command
			if startCmd, hasStart := buildCommands["start"]; hasStart {
				runCommand = startCmd
			} else if devCmd, hasDev := buildCommands["dev"]; hasDev {
				runCommand = devCmd
			}
	}

	if !hasRunCommand {
		// Fallback: Try to find a main executable file if no run command exists
		executablePath, err := s.findProjectExecutable(projectPath, buildSystem)
		if err != nil {
			return nil, fmt.Errorf("no run command available for build system %s: %v", buildSystem, err)
		}
		
		// Construct run command for the executable
		runCommand = executablePath
	}

	// Append arguments if provided
	fullCommand := runCommand
	if args != "" {
		fullCommand = fullCommand + " " + args
	}

	// Execute the run command in the project directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Change to the project directory
	err = os.Chdir(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to change to project directory: %v", err)
	}

	// Create and execute the command
	cmd := exec.Command("sh", "-c", fullCommand)
	cmdOutput, err := cmd.CombinedOutput()

	// Change back to the original directory
	changeBackErr := os.Chdir(currentDir)
	if changeBackErr != nil {
		// Log this error but continue with the response
		fmt.Printf("warning: failed to change back to original directory: %v\n", changeBackErr)
	}

	// Check if the run command succeeded
	success := err == nil
	result := map[string]any{
		"build_system": buildSystem,
		"run_command":  fullCommand,
		"success":      success,
		"output":       string(cmdOutput),
	}

	if !success {
		result["error"] = err.Error()
	}

	return result, nil
}

// runFile executes a single file based on its type
func (s *CodeService) runFile(filePath, args string, compileFirst bool) (map[string]any, error) {
	// Identify the language of the file
	lang, _, err := s.identifyLanguageFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to identify language: %v", err)
	}

	// For compiled languages, compile first if requested
	if compileFirst && s.isCompiledLanguage(lang) {
		compileResult, err := s.handleCompile(map[string]any{
			"path": filePath,
		})
		
		if err != nil {
			return nil, fmt.Errorf("failed to compile file: %v", err)
		}
		
		// If compilation failed, return the error
		if compileResultMap, ok := compileResult.(map[string]any); ok {
			if success, ok := compileResultMap["success"].(bool); ok && !success {
				return compileResultMap, nil
			}
		}
	}

	// Determine run command based on language
	var runCmd string
	switch lang {
		case "Python":
			runCmd = fmt.Sprintf("python %s %s", filePath, args)
		
		case "JavaScript":
			runCmd = fmt.Sprintf("node %s %s", filePath, args)
		
		case "TypeScript":
			runCmd = fmt.Sprintf("ts-node %s %s", filePath, args)
		
		case "Go":
			runCmd = fmt.Sprintf("go run %s %s", filePath, args)
		
		case "Java":
			// Extract class name for Java (assume filename matches class name)
			className := strings.TrimSuffix(filepath.Base(filePath), ".java")
			runCmd = fmt.Sprintf("java -cp %s %s %s", filepath.Dir(filePath), className, args)
		
		case "Ruby":
			runCmd = fmt.Sprintf("ruby %s %s", filePath, args)
		
		case "PHP":
			runCmd = fmt.Sprintf("php %s %s", filePath, args)
		
		case "C", "C++", "Rust":
			// For compiled languages, try to find the executable
			execPath := strings.TrimSuffix(filePath, filepath.Ext(filePath))
			// Check if executable exists
			if _, err := os.Stat(execPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("executable not found for %s, compile the file first", filePath)
			}
			runCmd = fmt.Sprintf("%s %s", execPath, args)
		
		default:
			return nil, fmt.Errorf("unsupported language for direct execution: %s", lang)
	}

	// Execute the run command
	cmd := exec.Command("sh", "-c", runCmd)
	cmdOutput, err := cmd.CombinedOutput()

	// Check if execution succeeded
	success := err == nil
	result := map[string]any{
		"language":    lang,
		"file_path":   filePath,
		"run_command": runCmd,
		"success":     success,
		"output":      string(cmdOutput),
	}

	if !success {
		result["error"] = err.Error()
	}

	return result, nil
}

// runMesonProject executes a Meson project
func (s *CodeService) runMesonProject(projectDir, args string) (map[string]any, error) {
	// Save current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Change to the project directory
	err = os.Chdir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to change to project directory: %v", err)
	}

	// Default Meson build directory
	buildDir := "builddir"

	// Check if builddir exists
	if _, err := os.Stat(buildDir); os.IsNotExist(err) {
		// Return to original directory
		os.Chdir(currentDir)
		return nil, fmt.Errorf("Meson build directory not found. Run compile first")
	}

	// Find the main executable in the build directory
	executables, err := s.findExecutablesInDir(buildDir)
	if err != nil {
		// Return to original directory
		os.Chdir(currentDir)
		return nil, fmt.Errorf("failed to find executable in build directory: %v", err)
	}

	if len(executables) == 0 {
		// Return to original directory
		os.Chdir(currentDir)
		return nil, fmt.Errorf("no executables found in Meson build directory")
	}

	// Use the first executable found (usually the main one)
	execPath := filepath.Join(buildDir, executables[0])

	// Construct run command
	runCmd := execPath
	if args != "" {
		runCmd = runCmd + " " + args
	}

	// Execute the run command
	cmd := exec.Command("sh", "-c", runCmd)
	cmdOutput, err := cmd.CombinedOutput()

	// Return to original directory
	os.Chdir(currentDir)

	// Check if execution succeeded
	success := err == nil
	result := map[string]any{
		"build_system":  "Meson",
		"executable":    executables[0],
		"run_command":   runCmd,
		"build_dir":     filepath.Join(projectDir, buildDir),
		"success":       success,
		"output":        string(cmdOutput),
	}

	if !success {
		result["error"] = err.Error()
	}

	return result, nil
}

// isCompiledLanguage checks if a language needs to be compiled
func (s *CodeService) isCompiledLanguage(lang string) bool {
	compiledLanguages := map[string]bool{
		"C":      true,
		"C++":    true,
		"Java":   true,
		"Rust":   true,
		"Go":     true, // Go can be compiled or run directly
		"Swift":  true,
		"Kotlin": true,
		"C#":     true,
	}

	return compiledLanguages[lang]
}

// findExecutablesInDir finds executable files in a directory
func (s *CodeService) findExecutablesInDir(dirPath string) ([]string, error) {
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var executables []string

	for _, file := range files {
		// Skip directories and hidden files
		if file.IsDir() || strings.HasPrefix(file.Name(), ".") {
			continue
		}

		// Check if the file is executable
		if file.Mode()&0111 != 0 { // Check executable bit
			executables = append(executables, file.Name())
		}
	}

	return executables, nil
}

// findProjectExecutable attempts to find the main executable in a project
func (s *CodeService) findProjectExecutable(projectPath, buildSystem string) (string, error) {
	// Check common locations based on build system
	switch buildSystem {
		case "Go Modules", "Go":
			// Look for Go main package files
			mainFile, err := s.findGoMainPackage(projectPath)
			if err == nil {
				return "go run " + mainFile, nil
			}

		case "Cargo":
			// Rust projects use cargo run
			return "cargo run", nil

		case "CMake", "Make":
			// Look for executables in common output directories
			executables, _ := s.findExecutablesInDir(projectPath)
			if len(executables) > 0 {
				return "." + string(os.PathSeparator) + executables[0], nil
			}
			
			// Check build directories
			buildDirs := []string{"build", "bin", "out"}
			for _, dir := range buildDirs {
				execs, err := s.findExecutablesInDir(filepath.Join(projectPath, dir))
				if err == nil && len(execs) > 0 {
					return filepath.Join(dir, execs[0]), nil
				}
			}
		
		default:
			// Look for executables in the project directory
			executables, _ := s.findExecutablesInDir(projectPath)
			if len(executables) > 0 {
				return "." + string(os.PathSeparator) + executables[0], nil
			}
	}

	return "", fmt.Errorf("could not find executable for project")
}

// findGoMainPackage looks for a Go file with package main
func (s *CodeService) findGoMainPackage(dirPath string) (string, error) {
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return "", err
	}

	// First check for main.go which is the conventional name
	if _, err := os.Stat(filepath.Join(dirPath, "main.go")); err == nil {
		return "main.go", nil
	}

	// Otherwise scan all Go files for package main
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".go" {
			filePath := filepath.Join(dirPath, file.Name())
			content, err := ioutil.ReadFile(filePath)
			if err != nil {
				continue
			}

			// Check if this file has package main
			if strings.Contains(string(content), "package main") {
				return file.Name(), nil
			}
		}
	}

	return "", fmt.Errorf("no main package found")
}
