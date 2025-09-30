//go:build windows
// +build windows

package main

import "golang.org/x/term"

// MakeRawPreserveNewline puts the terminal into raw mode on Windows.
// On Windows, we use the standard term.MakeRaw since custom ioctl-based
// preservation of newlines is not directly supported.
func MakeRawPreserveNewline(fd int) (*term.State, error) {
	return term.MakeRaw(fd)
}
