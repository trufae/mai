package main

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

var (
	replSession *ReplSession
	replMu      sync.Mutex
)

func executeCommand(config Config, input string) (string, int) {
	if len(config.Program) == 0 {
		return "No program configured", -1
	}

	if config.Interactive {
		replMu.Lock()
		defer replMu.Unlock()
		if replSession == nil {
			var err error
			replSession, err = NewReplSession(config.Program, config.CaptureStderr)
			if err != nil {
				return fmt.Sprintf("Error starting REPL session: %v", err), -1
			}
		}
		output, err := replSession.Execute(input, config.CaptureStderr)
		if err != nil {
			return fmt.Sprintf("Error executing in REPL: %v", err), -1
		}
		return output, 0
	}

	cmd := prepareCommand(config, input)

	var (
		output []byte
		err    error
	)

	if config.CaptureStderr {
		output, err = cmd.CombinedOutput()
	} else {
		output, err = cmd.Output()
	}

	if err != nil {
		exitCode := extractExitCode(err)
		if config.CaptureStderr {
			return fmt.Sprintf("%s\nError: %v", string(output), err), exitCode
		}
		return fmt.Sprintf("Error executing command: %v", err), exitCode
	}

	return string(output), 0
}

func prepareCommand(config Config, input string) *exec.Cmd {
	if config.InputMethod == "stdin" {
		cmd := exec.Command(config.Program[0], config.Program[1:]...)
		cmd.Stdin = strings.NewReader(input)
		return cmd
	}

	args := make([]string, len(config.Program)-1)
	copy(args, config.Program[1:])

	replaced := false
	for i, arg := range args {
		if arg == "{input}" {
			args[i] = input
			replaced = true
		}
	}

	if !replaced {
		args = append(args, input)
	}

	return exec.Command(config.Program[0], args...)
}

func extractExitCode(err error) int {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}
