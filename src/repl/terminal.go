package main

import (
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// MakeRawPreserveNewline puts the terminal into a "raw" mode similar to term.MakeRaw
// but with two important differences:
//  1. Preserves output processing for newlines. This allows terminal applications to
//     use '\n' normally without having to manually send '\r\n' sequences.
//  2. Keeps signal handling enabled so Ctrl+C can be caught by signal handlers.
//
// It returns the previous terminal state so it can be restored with term.Restore.
func MakeRawPreserveNewline(fd int) (*term.State, error) {
	// First get the current terminal state
	oldState, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}

	// Get the current termios settings
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}

	// Start with standard raw mode settings
	// Disable canonical mode (line buffering)
	termios.Lflag &^= unix.ICANON
	// Disable echo
	termios.Lflag &^= unix.ECHO
	// Keep signal generation (Ctrl-C, etc) enabled so signal handlers can catch them
	// termios.Lflag &^= unix.ISIG
	// Disable extended processing
	termios.Lflag &^= unix.IEXTEN
	// Disable flow control
	termios.Iflag &^= unix.IXON | unix.IXOFF
	// Disable special character processing
	termios.Iflag &^= unix.INPCK | unix.ISTRIP | unix.PARMRK
	// Disable CR to NL mapping on input
	termios.Iflag &^= unix.ICRNL
	// Disable NL to CR mapping on input
	termios.Iflag &^= unix.INLCR
	// Disable ignore CR
	termios.Iflag &^= unix.IGNCR

	// Unlike term.MakeRaw, we KEEP output processing enabled
	// Do NOT disable output post-processing
	// termios.Oflag &^= unix.OPOST

	// Disable CR to NL translation on output
	// termios.Oflag &^= unix.ONLCR

	// Set the new terminal settings
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		return nil, err
	}

	return oldState, nil
}
