package main

import (
	"io"
)

// registerExitCommands registers exit commands
func registerExitCommands(r *REPL) {
	r.commands["/quit"] = Command{
		Name:        "/quit",
		Description: "Exit REPL",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", io.EOF
		},
	}

	r.commands["/exit"] = Command{
		Name:        "/exit",
		Description: "Exit REPL",
		Handler: func(r *REPL, args []string) (string, error) {
			return "", io.EOF
		},
	}
}
