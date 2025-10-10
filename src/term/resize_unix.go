//go:build !windows
// +build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func handleResize(ptmx *os.File) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	for range ch {
		if term.IsTerminal(int(os.Stdout.Fd())) {
			width, height, err := term.GetSize(int(os.Stdout.Fd()))
			if err == nil {
				pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
			}
		}
	}
}
