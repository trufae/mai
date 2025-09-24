package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func executeCommand(config Config, input string) (string, int) {
	if len(config.Program) == 0 {
		return "No program configured", -1
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
