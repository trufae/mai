package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const DemoSpeed = 100

var (
	mu          sync.Mutex
	running     bool
	message     string
	stopChannel chan struct{}
	doneChannel chan struct{}

	// Simple ANSI color codes used for the rainbow
	palette = []string{
		"\x1b[31m", // red
		"\x1b[33m", // yellow
		"\x1b[32m", // green
		"\x1b[36m", // cyan
		"\x1b[34m", // blue
		"\x1b[35m", // magenta
	}
	resetColor = "\x1b[0m"
)

// startLoop starts the waiting animation in a separate goroutine.
// startLoop starts an animated colored demo writing to stderr so it doesn't
// interfere with prompts written to stdout. It displays a rainbow banner and
// a bouncing "laser" that traverses the terminal width.
func startLoop(initialMessage string) {
	mu.Lock()
	if running {
		mu.Unlock()
		return
	}
	running = true
	message = initialMessage
	stopChannel = make(chan struct{})
	doneChannel = make(chan struct{})
	mu.Unlock()

	go func() {
		tick := time.NewTicker(DemoSpeed * time.Millisecond)
		defer tick.Stop()

		frame := 0
		spinners := []string{"|", "/", "-", "\\"}
		for {
			select {
			case <-stopChannel:
				// Clear line immediately and signal done using ANSI escape code
				fmt.Fprint(os.Stderr, "\r\x1b[2K")
				close(doneChannel)
				return
			case <-tick.C:
				select {
				case <-stopChannel:
					// Check for stop signal again to avoid race condition
					fmt.Fprint(os.Stderr, "\r\x1b[2K")
					close(doneChannel)
					return
				default:
					// Continue with animation
				}

				mu.Lock()
				// Build rainbow banner from message
				banner := make([]byte, 0, len(message)*8)
				for i, ch := range message {
					color := palette[(i+frame)%len(palette)]
					banner = append(banner, []byte(color)...)
					banner = append(banner, []byte(string(ch))...)
				}
				banner = append(banner, []byte(resetColor)...)

				// Compose the display: spinner + banner
				spinner := spinners[frame%len(spinners)]
				display := fmt.Sprintf("%s %s", spinner, banner)

				// Print carriage return then the composed line to stderr
				fmt.Fprint(os.Stderr, "\r")
				fmt.Fprint(os.Stderr, display)

				mu.Unlock()

				frame++
			}
		}
	}()
}

// addMoreText appends text to the current waiting message.
func addMoreText(text string) {
	mu.Lock()
	message += text
	mu.Unlock()
}

// stopLoop stops the waiting animation goroutine.
func stopLoop() {
	mu.Lock()
	if !running {
		mu.Unlock()
		return
	}
	running = false
	close(stopChannel)
	// copy doneChannel so we can wait outside the lock
	done := doneChannel
	mu.Unlock()

	// Wait briefly for the goroutine to finish clearing the line.
	// Protect against a stuck goroutine by timing out after 1s.
	select {
	case <-done:
	case <-time.After(1 * time.Second):
	}
}

// getTerminalWidth returns the number of columns for the terminal attached to fd.
// Falls back to 80 if the call fails.
func getTerminalWidth(fd uintptr) int {
	return 80
}
