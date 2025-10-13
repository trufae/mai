package main

import (
	"fmt"
	"io/ioutil"
	"mcplib"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ShellService handles all shell-related operations
type ShellService struct{}

// NewShellService creates a new ShellService instance
func NewShellService() *ShellService {
	return &ShellService{}
}

// GetTools returns all available shell tools
func (s *ShellService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		// 1. CommandExecutor
		{
			Name:        "command_executor",
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
			Name:        "read_file",
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
			Name:        "write_file",
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
			Name:        "append_file",
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
			Name:        "delete_file",
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
			Name:        "move_file",
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
			Name:        "rename_file",
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
			Name:        "copy_file",
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
			Name:        "create_directory",
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
			Name:        "remove_directory",
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
			Name:        "get_os_info",
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
			Name:        "get_environment",
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
			Name:        "set_environment",
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

		// 3. SystemInformation - ListFiles
		{
			Name:        "list_files",
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
			Name:        "change_directory",
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
			Name:        "get_current_directory",
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
func (s *ShellService) handleCommandExecutor(args map[string]any) (any, error) {
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
func (s *ShellService) handleReadFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleWriteFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleMoveFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleCopyFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleCreateDirectory(args map[string]any) (any, error) {
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
func (s *ShellService) handleGetOSInfo(args map[string]any) (any, error) {
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
func (s *ShellService) handleListFiles(args map[string]any) (any, error) {
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
func (s *ShellService) handleChangeDirectory(args map[string]any) (any, error) {
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
func (s *ShellService) handleGetCurrentDirectory(args map[string]any) (any, error) {
	currentDir := s.getCurrentDirectory()
	return map[string]any{
		"current_directory": currentDir,
	}, nil
}

// getCurrentDirectory is a helper function to get the current working directory
func (s *ShellService) getCurrentDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// handleAppendFile handles the AppendFile request
func (s *ShellService) handleAppendFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleDeleteFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleRenameFile(args map[string]any) (any, error) {
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
func (s *ShellService) handleRemoveDirectory(args map[string]any) (any, error) {
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
func (s *ShellService) handleGetEnvironment(args map[string]any) (any, error) {
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
func (s *ShellService) handleSetEnvironment(args map[string]any) (any, error) {
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
