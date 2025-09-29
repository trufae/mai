package art

import (
	"github.com/mattn/go-runewidth"
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

// DisplayWidth calculates the display width of a string
func DisplayWidth(s string) int {
	return runewidth.StringWidth(s)
}

// RuneWidth returns the display width of a rune
func RuneWidth(r rune) int {
	return runewidth.RuneWidth(r)
}
