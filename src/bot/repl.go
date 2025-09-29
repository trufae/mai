package main

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type ReplSession struct {
	cmd    *exec.Cmd
	stdin  io.Writer
	stdout io.Reader
	stderr io.Reader
	prompt string
	mu     sync.Mutex
}

func NewReplSession(program []string, captureStderr bool) (*ReplSession, error) {
	cmd := exec.Command(program[0], program[1:]...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	var stderr io.Reader
	if captureStderr {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	session := &ReplSession{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}

	session.detectPrompt(captureStderr)

	return session, nil
}

func (r *ReplSession) detectPrompt(captureStderr bool) {
	// Read initial output to detect prompt
	time.Sleep(100 * time.Millisecond) // Wait a bit for output

	var readers []io.Reader
	readers = append(readers, r.stdout)
	if captureStderr && r.stderr != nil {
		readers = append(readers, r.stderr)
	}

	multi := io.MultiReader(readers...)
	scanner := bufio.NewScanner(multi)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Fuzzy detection: look for lines that end with common prompt characters
	promptCandidates := []string{">>> ", ">> ", "> ", "$ ", "# ", "% ", "? "}
	r.prompt = "> " // default
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, candidate := range promptCandidates {
			if strings.HasSuffix(trimmed, strings.TrimSpace(candidate)) {
				r.prompt = trimmed
				return
			}
		}
	}
	// If no candidate found, take the last line
	if len(lines) > 0 {
		r.prompt = lines[len(lines)-1]
	}
}

func (r *ReplSession) Execute(input string, captureStderr bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Send input
	_, err := io.WriteString(r.stdin, input+"\n")
	if err != nil {
		return "", err
	}

	// Read output until prompt
	var output strings.Builder
	var readers []io.Reader
	readers = append(readers, r.stdout)
	if captureStderr && r.stderr != nil {
		readers = append(readers, r.stderr)
	}

	multi := io.MultiReader(readers...)
	scanner := bufio.NewScanner(multi)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == strings.TrimSpace(r.prompt) {
			break
		}
		output.WriteString(line + "\n")
	}

	return strings.TrimSpace(output.String()), nil
}

func (r *ReplSession) Close() error {
	return r.cmd.Process.Kill()
}
