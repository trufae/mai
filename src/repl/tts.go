package main

import (
	"os/exec"
	"strings"
)

// Speak converts the given message to speech using the 'say' command.
// It kills any existing 'say' processes to avoid overlapping voices.
func Speak(message, voice string) {
	// Kill any existing say processes to avoid overlapping voices
	exec.Command("pkill", "say").Run()

	// Prepare the command: echo message | say -v voice
	cmd := exec.Command("sh", "-c", "echo "+strings.ReplaceAll(message, "'", "'\"'\"'")+" | say -v '"+voice+"'")

	// Run in background
	cmd.Start()
}
