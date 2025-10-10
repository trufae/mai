//go:build windows
// +build windows

package main

import "os"

func handleResize(ptmx *os.File) {
	// Window resize not supported on Windows
}
