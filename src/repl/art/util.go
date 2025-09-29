package art

import (
	"golang.org/x/term"
	"os"
)

// GetTerminalWidth returns a conservative default width.
func GetTerminalWidth(fd uintptr) int {
	if w, _, err := term.GetSize(int(fd)); err == nil && w > 0 {
		return w
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// DisplayWidth calculates the display width of a string, assuming ASCII is 1 column, others 2
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r < 128 {
			w++
		} else {
			w += 2
		}
	}
	return w
}

// RuneWidth returns the display width of a rune
func RuneWidth(r rune) int {
	if r < 128 {
		return 1
	}
	return 2
}
