package main

import (
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/term"
)

// ReadLine represents a line editor with horizontal scrolling
type ReadLine struct {
	buffer          []rune
	cursorPos       int
	scrollPos       int
	width           int
	history         []string
	historyPos      int
	mu              sync.Mutex
	oldState        *term.State
	completions     []string
	completeIdx     int
	interruptFunc   func()
}

// NewReadLine creates a new ReadLine instance
func NewReadLine() (*ReadLine, error) {
	width, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		// Default width if we can't get terminal size
		width = 80
	}

	// Account for prompt length (>>> plus a space)
	promptLen := 4
	width = width - promptLen

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to set terminal to raw mode: %v", err)
	}

	return &ReadLine{
		buffer:        make([]rune, 0, 256),
		cursorPos:     0,
		scrollPos:     0,
		width:         width,
		history:       make([]string, 0),
		historyPos:    -1,
		oldState:      oldState,
		completions:   nil,
		completeIdx:   0,
		interruptFunc: nil,
	}, nil
}

// Restore restores the terminal to its original state
func (r *ReadLine) Restore() {
	if r.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), r.oldState)
		r.oldState = nil
	}
}

// AddToHistory adds the current input to history
func (r *ReadLine) AddToHistory(input string) {
	if input == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.history = append(r.history, input)
	r.historyPos = -1
}

// SetCompletions sets the available completions for tab completion
func (r *ReadLine) SetCompletions(completions []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.completions = completions
	r.completeIdx = 0
}

// Read reads a line of input with proper cursor movement and scrolling
func (r *ReadLine) Read() (string, error) {
	r.buffer = r.buffer[:0]
	r.cursorPos = 0
	r.scrollPos = 0

	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", err
		}

		if n == 0 {
			continue
		}

		b := buf[0]

		switch b {
		case '\r', '\n': // Enter
			fmt.Print("\r\n")
			return string(r.buffer), nil

		case 127, 8: // Backspace
			if r.cursorPos > 0 {
				r.buffer = append(r.buffer[:r.cursorPos-1], r.buffer[r.cursorPos:]...)
				r.cursorPos--
				if r.scrollPos > 0 && r.cursorPos < r.scrollPos {
					r.scrollPos--
				}
				r.refreshLine()
			}

		case 4: // Ctrl+D
			if len(r.buffer) == 0 {
				fmt.Print("\r\n")
				return "", io.EOF
			}

		case 3: // Ctrl+C
			fmt.Print("^C\r\n")
			r.buffer = r.buffer[:0]
			r.cursorPos = 0
			r.scrollPos = 0
			// Call the interrupt function if set
			if r.interruptFunc != nil {
				r.interruptFunc()
			}
			// Continue reading input after interruption instead of returning error
			fmt.Print("\x1b[33m>>> ")
			continue

		case 23: // Ctrl+W (delete word)
			r.deleteWord()

		case 1: // Ctrl+A (beginning of line)
			r.moveCursorToStart()

		case 5: // Ctrl+E (end of line)
			r.moveCursorToEnd()

		case 9: // Tab (completion) - Skip in this implementation as it will be handled by REPL
			// Do nothing - this is now handled by the REPL directly

		case 27: // Escape sequence
			r.handleEscapeSequence()

		default:
			if b >= 32 && b <= 126 { // Printable characters
				r.insertRune(rune(b))
				r.refreshLine()
			}
		}
	}
}

// refreshLine redraws the current line with scrolling if needed
func (r *ReadLine) refreshLine() {
	// First, calculate the visible portion of the buffer
	visibleEnd := r.scrollPos + r.width
	if visibleEnd > len(r.buffer) {
		visibleEnd = len(r.buffer)
	}

	// Adjust scroll position if cursor would be out of view
	if r.cursorPos < r.scrollPos {
		r.scrollPos = r.cursorPos
	} else if r.cursorPos >= r.scrollPos+r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}

	// Clear the current line
	fmt.Print("\r\033[K")

	// Print prompt
	fmt.Print("\x1b[33m>>> ")

	// Print visible portion of the buffer
	visibleText := string(r.buffer[r.scrollPos:visibleEnd])
	fmt.Print(visibleText)

	// Calculate cursor position on screen
	screenPos := r.cursorPos - r.scrollPos + 4 // +4 for ">>> "

	// Move cursor to the correct position
	fmt.Printf("\r\033[%dC", screenPos)
}

// insertRune inserts a character at the current cursor position
func (r *ReadLine) insertRune(char rune) {
	if r.cursorPos == len(r.buffer) {
		r.buffer = append(r.buffer, char)
	} else {
		r.buffer = append(r.buffer[:r.cursorPos+1], r.buffer[r.cursorPos:]...)
		r.buffer[r.cursorPos] = char
	}
	r.cursorPos++

	// Adjust scroll position if needed
	if r.cursorPos >= r.scrollPos+r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}
}

// deleteWord deletes the word before the cursor
func (r *ReadLine) deleteWord() {
	if r.cursorPos == 0 {
		return
	}

	// Skip any spaces immediately before the cursor
	end := r.cursorPos
	for end > 0 && r.buffer[end-1] == ' ' {
		end--
	}

	// Find the beginning of the word
	start := end
	for start > 0 && r.buffer[start-1] != ' ' {
		start--
	}

	// Delete from start to the cursor position
	r.buffer = append(r.buffer[:start], r.buffer[r.cursorPos:]...)
	r.cursorPos = start

	// Adjust scroll position if needed
	if r.scrollPos > 0 && r.cursorPos < r.scrollPos {
		r.scrollPos = r.cursorPos
	}

	r.refreshLine()
}

// moveCursorToStart moves the cursor to the start of the line
func (r *ReadLine) moveCursorToStart() {
	r.cursorPos = 0
	r.scrollPos = 0
	r.refreshLine()
}

// moveCursorToEnd moves the cursor to the end of the line
func (r *ReadLine) moveCursorToEnd() {
	r.cursorPos = len(r.buffer)
	if r.cursorPos >= r.scrollPos+r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}
	r.refreshLine()
}

// GetContent returns the current content of the buffer as a string
func (r *ReadLine) GetContent() string {
	return string(r.buffer)
}

// SetContent updates the content of the buffer with the provided string
func (r *ReadLine) SetContent(content string) {
	r.buffer = []rune(content)
	r.cursorPos = len(r.buffer)
	r.scrollPos = 0
	if r.cursorPos >= r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}
	r.refreshLine()
}

// SetInterruptFunc sets the function to be called when Ctrl+C is pressed
func (r *ReadLine) SetInterruptFunc(fn func()) {
	r.interruptFunc = fn
}

// handleTabCompletion handles tab completion (kept for reference)
func (r *ReadLine) handleTabCompletion() {
	if len(r.completions) == 0 {
		return
	}

	// Cycle through completions
	if r.completeIdx >= len(r.completions) {
		r.completeIdx = 0
	}

	// Replace current input with the completion
	completion := r.completions[r.completeIdx]
	r.buffer = []rune(completion)
	r.cursorPos = len(r.buffer)
	r.scrollPos = 0
	if r.cursorPos >= r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}
	r.completeIdx++
	r.refreshLine()
}

// handleEscapeSequence handles escape sequences (arrow keys, etc.)
func (r *ReadLine) handleEscapeSequence() {
	buf := make([]byte, 2)
	n, err := os.Stdin.Read(buf)
	if err != nil || n != 2 {
		return
	}

	if buf[0] == '[' {
		switch buf[1] {
		case 'A': // Up arrow
			r.navigateHistory(-1)
		case 'B': // Down arrow
			r.navigateHistory(1)
		case 'C': // Right arrow
			if r.cursorPos < len(r.buffer) {
				r.cursorPos++
				if r.cursorPos >= r.scrollPos+r.width {
					r.scrollPos++
				}
				r.refreshLine()
			}
		case 'D': // Left arrow
			if r.cursorPos > 0 {
				r.cursorPos--
				if r.cursorPos < r.scrollPos {
					r.scrollPos--
				}
				r.refreshLine()
			}
		}
	}
}

// navigateHistory navigates through command history
func (r *ReadLine) navigateHistory(direction int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.history) == 0 {
		return
	}

	// Calculate new history position
	newPos := r.historyPos + direction
	if newPos >= len(r.history) {
		newPos = len(r.history) - 1
	} else if newPos < -1 {
		newPos = -1
	}

	r.historyPos = newPos

	if r.historyPos == -1 {
		// Clear the line
		r.buffer = r.buffer[:0]
		r.cursorPos = 0
		r.scrollPos = 0
	} else {
		// Set buffer to history item
		historyItem := r.history[r.historyPos]
		r.buffer = []rune(historyItem)
		r.cursorPos = len(r.buffer)
		r.scrollPos = 0
		if r.cursorPos >= r.width {
			r.scrollPos = r.cursorPos - r.width + 1
		}
	}

	r.refreshLine()
}
