package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"
)

// ReadLine represents a line editor with horizontal scrolling
type ReadLine struct {
	buffer     []rune
	cursorPos  int
	scrollPos  int
	width      int
	history    []string
	historyPos int
	// Saved buffer content when entering history navigation so it can be
	// restored if the user navigates back to their original input.
	historySavedBuffer []rune
	mu                 sync.Mutex
	oldState           *term.State
	completions        []string
	completeIdx        int
	interruptFunc      func()
	// Prompt customization
	prompt         string // Main prompt string
	readlinePrompt string // Prompt for heredoc/continuation mode
	defaultPrompt  string // copy of prompt when using readlinePrompt
	// Heredoc support
	isHeredoc     bool     // Whether we are in heredoc mode
	heredocDelim  string   // The delimiter to look for
	heredocBuffer []string // Lines collected in heredoc mode
	// Line continuation support
	isContinuation     bool     // Whether we are in line continuation mode
	continuationBuffer []string // Lines collected in continuation mode
	// Reverse search support
	isSearchMode   bool   // Whether we are in reverse search mode
	searchQuery    string // Current search query
	searchIndex    int    // Current position in search results
	searchMatches  []int  // Indices of history items that match the search
	originalBuffer []rune // Buffer content before entering search mode
	bgColor        string // Background color for the input line
	fgColor        string // Foreground color for the input line text
	bold           bool   // Whether to use bold text for the input line
}

// NewReadLine creates a new ReadLine instance
func NewReadLine() (*ReadLine, error) {
	width, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		// Default width if we can't get terminal size
		width = 80
	}

	// Default prompts
	prompt := ">>>"
	readlinePrompt := "..." // multiline

	// Account for prompt length plus a space
	promptLen := len(prompt) + 1
	width = width - promptLen
	oldState, err := MakeRawPreserveNewline(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to set terminal to raw mode: %v", err)
	}

	r := &ReadLine{
		buffer:             make([]rune, 0, 256),
		cursorPos:          0,
		scrollPos:          0,
		width:              width,
		history:            make([]string, 0),
		historyPos:         -1,
		oldState:           oldState,
		completions:        nil,
		completeIdx:        0,
		interruptFunc:      nil,
		prompt:             prompt,
		defaultPrompt:      prompt,
		readlinePrompt:     readlinePrompt,
		isHeredoc:          false,
		heredocDelim:       "",
		heredocBuffer:      nil,
		isContinuation:     false,
		continuationBuffer: nil,
		isSearchMode:       false,
		searchQuery:        "",
		searchIndex:        0,
		searchMatches:      nil,
		originalBuffer:     nil,
		bgColor:            "",
		fgColor:            "",
		bold:               false,
	}
	r.Restore()
	return r, nil
}

// colorMap defines background and foreground ANSI color codes for supported colors
var colorMap = map[string]struct{ bg, fg string }{
	"black":          {"40", "37"},  // black bg, white fg
	"red":            {"41", "37"},  // red bg, white fg
	"green":          {"42", "37"},  // green bg, white fg
	"yellow":         {"43", "30"},  // yellow bg, black fg
	"blue":           {"44", "37"},  // blue bg, white fg
	"dark-blue":      {"44", "37"},  // blue bg, white fg (alias for blue)
	"magenta":        {"45", "37"},  // magenta bg, white fg
	"cyan":           {"46", "37"},  // cyan bg, white fg
	"white":          {"47", "30"},  // white bg, black fg
	"grey":           {"100", "37"}, // dark grey bg, white fg
	"bright-black":   {"100", "37"}, // dark grey bg, white fg
	"bright-red":     {"101", "30"}, // bright red bg, black fg
	"bright-green":   {"102", "30"}, // bright green bg, black fg
	"bright-yellow":  {"103", "30"}, // bright yellow bg, black fg
	"bright-blue":    {"104", "30"}, // bright blue bg, black fg
	"bright-magenta": {"105", "30"}, // bright magenta bg, black fg
	"bright-cyan":    {"106", "30"}, // bright cyan bg, black fg
	"bright-white":   {"107", "30"}, // bright white bg, black fg
}

// fgMap defines foreground ANSI color codes
var fgMap = map[string]string{
	"black":          "30",
	"red":            "31",
	"green":          "32",
	"yellow":         "33",
	"blue":           "34",
	"magenta":        "35",
	"cyan":           "36",
	"white":          "37",
	"grey":           "90", // bright black
	"bright-black":   "90",
	"bright-red":     "91",
	"bright-green":   "92",
	"bright-yellow":  "93",
	"bright-blue":    "94",
	"bright-magenta": "95",
	"bright-cyan":    "96",
	"bright-white":   "97",
}

// parseRGB parses rgb:RGB format (3 hex chars) and returns ANSI code parameters
func parseRGB(color string) (string, bool) {
	if !strings.HasPrefix(color, "rgb:") || len(color) != 7 {
		return "", false
	}
	hexStr := color[4:]
	if len(hexStr) != 3 {
		return "", false
	}
	var r, g, b int
	for i, c := range hexStr {
		var val int
		switch {
		case c >= '0' && c <= '9':
			val = int(c - '0')
		case c >= 'a' && c <= 'f':
			val = 10 + int(c-'a')
		case c >= 'A' && c <= 'F':
			val = 10 + int(c-'A')
		default:
			return "", false
		}
		val *= 17
		switch i {
		case 0:
			r = val
		case 1:
			g = val
		case 2:
			b = val
		}
	}
	return fmt.Sprintf("%d;%d;%d", r, g, b), true
}

// getColorCodes returns the ANSI color codes for foreground and background based on fgColor and bgColor
func (r *ReadLine) getColorCodes() string {
	var codes []string
	if r.bold {
		codes = append(codes, "1")
	}

	var fgCode string
	if r.fgColor != "" {
		if strings.HasPrefix(r.fgColor, "rgb:") {
			if code, ok := parseRGB(r.fgColor); ok {
				fgCode = "38;2;" + code
			}
		} else if fg, ok := fgMap[r.fgColor]; ok {
			fgCode = fg
		}
	}
	if fgCode != "" {
		codes = append(codes, fgCode)
	}

	var bgCode string
	if r.bgColor != "" {
		if strings.HasPrefix(r.bgColor, "rgb:") {
			if code, ok := parseRGB(r.bgColor); ok {
				bgCode = "48;2;" + code
			}
		} else if info, ok := colorMap[r.bgColor]; ok {
			bgCode = info.bg
		}
	}
	if bgCode != "" {
		codes = append(codes, bgCode)
	}

	if len(codes) == 0 {
		return "\x1b[33m" // default yellow foreground
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// Restore restores the terminal to its original state
func (r *ReadLine) Restore() {
	if r.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), r.oldState)
		// r.oldState = nil
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
	// Clear any saved buffer when we add a new history entry
	r.historySavedBuffer = nil
}

// SetCompletions sets the available completions for tab completion
func (r *ReadLine) SetCompletions(completions []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.completions = completions
	r.completeIdx = 0
}

// isHeredocSyntax checks if the buffer ends with heredoc syntax (<<DELIM)
// and returns the delimiter if found
func (r *ReadLine) isHeredocSyntax() (bool, string) {
	content := string(r.buffer)
	// Need at least <<X (3 chars) for valid heredoc
	if len(content) < 3 {
		return false, ""
	}

	// Check if the content ends with <<
	if content[len(content)-2:] == "<<" {
		// Found << without delimiter, use EOF as default
		return true, "EOF"
	}

	// Find the position of << near the end
	pos := strings.LastIndex(content, "<<")
	// Must be at end or have space or delimiter between << and end
	if pos != -1 && pos >= len(content)-10 && pos < len(content)-1 {
		// Extract the delimiter (everything after <<)
		delim := content[pos+2:]
		// If valid delimiter found, return it
		if delim != "" {
			return true, delim
		}
	}

	return false, ""
}

func (r *ReadLine) Interrupted() {
	// this function aims to be called by the Interrupt handler
	r.isSearchMode = false
	r.searchQuery = ""
	r.searchIndex = 0
	r.searchMatches = nil
	r.originalBuffer = nil
}

// Read reads a line of input with proper cursor movement and scrolling
func (r *ReadLine) Read() (string, error) {
	r.prompt = r.defaultPrompt
	r.Restore()
	state, err := MakeRawPreserveNewline(int(os.Stdin.Fd()))
	if err != nil {
		return "", fmt.Errorf("failed to set terminal to raw mode: %v", err)
	}
	r.oldState = state
	// Don't reset the buffer or cursor position for history continuity
	if len(r.buffer) == 0 {
		r.cursorPos = 0
		r.scrollPos = 0
	}

	// Show the prompt immediately when starting to read
	// Choose appropriate prompt based on mode
	if r.isHeredoc || r.isContinuation {
		fmt.Printf("\r\x1b[33m%s\x1b[0m ", r.readlinePrompt)
	} else {
		fmt.Printf("\r\x1b[33m%s\x1b[0m ", r.prompt)
	}
	r.refreshLine()

	// Buffer large enough to handle multi-byte characters
	buf := make([]byte, 8)
	for {
		// Always read first byte and check if it's a control character or start of multi-byte sequence
		n, err := os.Stdin.Read(buf[:1])
		if err != nil {
			r.Restore()
			return "", err
		}

		if n == 0 {
			continue
		}

		b := buf[0]

		switch b {
		case '\r', '\n': // Enter
			fmt.Print("\n") // Changed from "\r\n" to "\n" to use terminal's natural translation
			result := string(r.buffer)

			// Check if we're in heredoc mode
			if r.isHeredoc {
				// Check if this line exactly matches the delimiter
				if result == r.heredocDelim {
					// End of heredoc, combine all lines with newlines
					fullResult := strings.Join(r.heredocBuffer, "\n")
					// Reset heredoc state
					r.isHeredoc = false
					r.heredocDelim = ""
					r.heredocBuffer = nil
					// Reset search mode if active
					r.isSearchMode = false
					r.searchQuery = ""
					r.searchIndex = 0
					r.searchMatches = nil
					r.originalBuffer = nil
					// Clear buffer for next input while preserving history
					r.buffer = r.buffer[:0]
					r.cursorPos = 0
					r.scrollPos = 0
					r.Restore()
					return fullResult, nil
				} else {
					// Add the line to heredoc buffer
					r.heredocBuffer = append(r.heredocBuffer, result)
					// Show the prompt again for next line
					fmt.Printf("\x1b[33m%s\x1b[0m ", r.readlinePrompt)
					// Clear buffer for next line
					r.buffer = r.buffer[:0]
					r.cursorPos = 0
					r.scrollPos = 0
					continue
				}
			}

			// Check if we're in continuation mode
			if r.isContinuation {
				// Remove the trailing backslash if present
				if len(result) > 0 && result[len(result)-1] == '\\' {
					// Add line without the trailing backslash to buffer
					r.continuationBuffer = append(r.continuationBuffer, result[:len(result)-1])
					// Show prompt for next line
					fmt.Printf("\x1b[33m%s\x1b[0m ", r.readlinePrompt)
					// Clear buffer for next line
					r.buffer = r.buffer[:0]
					r.cursorPos = 0
					r.scrollPos = 0
					continue
				} else {
					// No trailing backslash, end continuation
					// Add the final line
					r.continuationBuffer = append(r.continuationBuffer, result)
					// Combine all lines
					fullResult := strings.Join(r.continuationBuffer, "\n")
					// Reset continuation state
					r.isContinuation = false
					r.continuationBuffer = nil
					// Reset search mode if active
					r.isSearchMode = false
					r.searchQuery = ""
					r.searchIndex = 0
					r.searchMatches = nil
					r.originalBuffer = nil
					// Clear buffer for next input
					r.buffer = r.buffer[:0]
					r.cursorPos = 0
					r.scrollPos = 0
					r.Restore()
					return fullResult, nil
				}
			}

			// Check for heredoc syntax
			isHeredoc, delim := r.isHeredocSyntax()
			if isHeredoc {
				// Enter heredoc mode
				r.isHeredoc = true
				r.heredocDelim = delim
				r.heredocBuffer = []string{}
				r.defaultPrompt = r.prompt
				r.prompt = r.readlinePrompt

				// Add the first line without the heredoc marker
				firstLine := strings.TrimSuffix(result, "<<"+delim)
				if firstLine != result { // If we trimmed something
					r.heredocBuffer = append(r.heredocBuffer, firstLine)
				}

				// Show the prompt for next line
				fmt.Printf("\x1b[33m%s\x1b[0m ", r.readlinePrompt)
				// Clear buffer for next line
				r.buffer = r.buffer[:0]
				r.cursorPos = 0
				r.scrollPos = 0
				continue
			}

			// Check if this line ends with a backslash for continuation
			if len(result) > 0 && result[len(result)-1] == '\\' {
				// Enter continuation mode
				r.isContinuation = true
				r.continuationBuffer = []string{result[:len(result)-1]} // Store line without backslash

				// Show prompt for next line
				fmt.Printf("\x1b[33m%s\x1b[0m ", r.readlinePrompt)
				// Clear buffer for next line
				r.buffer = r.buffer[:0]
				r.cursorPos = 0
				r.scrollPos = 0
				r.prompt = r.readlinePrompt
				continue
			}

			// Regular line input (not heredoc or continuation)
			// Reset search mode if somehow still active
			r.isSearchMode = false
			r.searchQuery = ""
			r.searchIndex = 0
			r.searchMatches = nil
			r.originalBuffer = nil
			// Clear buffer for next input while preserving history
			r.buffer = r.buffer[:0]
			r.cursorPos = 0
			r.scrollPos = 0
			r.Restore()
			return result, nil

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
				fmt.Print("\n")
				r.Restore()
				return "", io.EOF
			}
		case '\f': // Ctrl+L
			fmt.Printf("\033[2J\033[H") // Clear screen ANSI
			r.refreshLine()             // Refresh the input line after clearing

		case 3: // Ctrl+C
			// This case may not get triggered if our custom terminal mode allows
			// the OS signal handler to intercept Ctrl+C first. But we keep it for robustness.
			fmt.Print("^C\n")
			r.buffer = r.buffer[:0]
			r.isSearchMode = false
			r.cursorPos = 0
			r.scrollPos = 0
			// Call the interrupt function if set
			if r.interruptFunc != nil {
				r.interruptFunc()
			}
			// Continue reading input after interruption instead of returning error
			fmt.Printf("\x1b[33m%s\x1b[0m ", r.prompt)
			continue

		case 23: // Ctrl+W (delete word)
			r.deleteWord()
			continue

		case 1: // Ctrl+A (beginning of line)
			r.moveCursorToStart()
			continue

		case 5: // Ctrl+E (end of line)
			r.moveCursorToEnd()
			continue

		case 16: // Ctrl+P (previous history)
			r.navigateHistory(-1)
			continue

		case 14: // Ctrl+N (next history)
			r.navigateHistory(1)
			continue

		case 18: // Ctrl+R (reverse search)
			r.startReverseSearch()
			continue

		case 6: // Ctrl+F (forward cursor)
			if r.cursorPos < len(r.buffer) {
				r.cursorPos++
				if r.cursorPos >= r.scrollPos+r.width {
					r.scrollPos++
				}
				r.refreshLine()
			}
			continue

		case 2: // Ctrl+B (backward cursor)
			if r.cursorPos > 0 {
				r.cursorPos--
				if r.cursorPos < r.scrollPos {
					r.scrollPos--
				}
				r.refreshLine()
			}
			continue

		case 9: // Tab (completion)
			// Exit search mode if active before returning tab
			if r.isSearchMode {
				r.exitSearchMode()
			}
			// Return a special value to indicate tab was pressed
			// This will be handled by the REPL's tab completion logic
			return "\t", nil

		case 27: // Escape sequence
			r.handleEscapeSequence()
			continue

		default:
			// Handle input based on current mode
			if r.isSearchMode {
				r.handleSearchInput(b, buf)
			} else {
				r.handleNormalInput(b, buf)
			}
		}
	}
}

// refreshLine redraws the current line with scrolling if needed
func (r *ReadLine) refreshLine() {
	// Get terminal width
	width, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err == nil {
		// Account for prompt length plus a space
		promptLen := len(r.prompt) + 1
		r.width = width - promptLen
	}
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
	fmt.Print("\r\033[2K")

	// Print prompt with default color
	fmt.Printf("\x1b[33m%s\x1b[0m ", r.prompt)

	// Print visible text and padding with set colors
	color := r.getColorCodes()
	visibleText := string(r.buffer[r.scrollPos:visibleEnd])
	textLen := len(visibleText)
	fmt.Printf("%s%s", color, visibleText)
	// Pad with spaces to fill the terminal width minus one to avoid colorizing the next line
	padding := r.width - textLen - 1
	if padding < 0 {
		padding = 0
	}
	for i := 0; i < padding; i++ {
		fmt.Print(" ")
	}
	fmt.Print("\x1b[0m") // reset color

	// Calculate cursor position on screen
	promptLen := len(r.prompt) + 1 // +1 for space
	screenPos := r.cursorPos - r.scrollPos + promptLen

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

// GetCursorPos returns the current cursor position
func (r *ReadLine) GetCursorPos() int {
	return r.cursorPos
}

// GetHistory returns the command history
func (r *ReadLine) GetHistory() []string {
	return r.history
}

// SetCursorPos sets the cursor position to a specific location
func (r *ReadLine) SetCursorPos(pos int) {
	// Ensure the position is valid
	if pos < 0 {
		pos = 0
	}
	if pos > len(r.buffer) {
		pos = len(r.buffer)
	}

	// Set the cursor position
	r.cursorPos = pos

	// Adjust scroll position if needed
	if r.cursorPos < r.scrollPos {
		r.scrollPos = r.cursorPos
	} else if r.cursorPos >= r.scrollPos+r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}

	// Update the display
	r.refreshLine()
}

// SetInterruptFunc sets the function to be called when Ctrl+C is pressed
func (r *ReadLine) SetInterruptFunc(fn func()) {
	r.interruptFunc = fn
}

// SetPrompt sets the main prompt string
func (r *ReadLine) SetPrompt(prompt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompt = prompt
	r.defaultPrompt = prompt

	// Update width based on new prompt length
	width, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err == nil {
		r.width = width - len(prompt) - 1 // Account for prompt plus space
	}
}

// SetReadlinePrompt sets the prompt used for heredoc and continuation lines
func (r *ReadLine) SetReadlinePrompt(prompt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readlinePrompt = prompt
}

// SetBgColor sets the background color for the input line
func (r *ReadLine) SetBgColor(color string) {
	r.bgColor = color
}

// SetFgColor sets the foreground color for the input line text
func (r *ReadLine) SetFgColor(color string) {
	r.fgColor = color
}

// SetBold sets whether to use bold text for the input line
func (r *ReadLine) SetBold(b bool) {
	r.bold = b
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
	if r.historyPos == -1 {
		// Save current buffer so we can restore it when the user navigates
		// back down to the empty prompt.
		if len(r.buffer) > 0 {
			r.historySavedBuffer = make([]rune, len(r.buffer))
			copy(r.historySavedBuffer, r.buffer)
		} else {
			r.historySavedBuffer = nil
		}
		r.historyPos = len(r.history)
	}

	// Calculate new history position. Allow a virtual position equal to
	// len(history) which maps to the empty prompt (historyPos == -1).
	newPos := r.historyPos + direction

	// Clamp newPos into [0, len(history)] (len(history) means empty prompt)
	if newPos < 0 {
		newPos = 0
	}
	if newPos > len(r.history) {
		newPos = len(r.history)
	}

	if newPos == len(r.history) {
		// Virtual slot after the last history entry -> restore original input
		r.historyPos = -1
		if r.historySavedBuffer != nil {
			r.buffer = make([]rune, len(r.historySavedBuffer))
			copy(r.buffer, r.historySavedBuffer)
			// place cursor at end of restored content
			r.cursorPos = len(r.buffer)
			r.historySavedBuffer = nil
		} else {
			// No saved content -> clear the line
			r.buffer = r.buffer[:0]
			r.cursorPos = 0
		}
		r.scrollPos = 0
	} else {
		r.historyPos = newPos
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

// startReverseSearch initiates reverse search mode
func (r *ReadLine) startReverseSearch() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.history) == 0 {
		return
	}

	// Save the current buffer content
	r.originalBuffer = make([]rune, len(r.buffer))
	copy(r.originalBuffer, r.buffer)

	r.isSearchMode = true
	r.searchQuery = ""
	r.searchIndex = 0
	r.searchMatches = nil

	// Find all matches for empty query (all history items)
	for i := len(r.history) - 1; i >= 0; i-- {
		r.searchMatches = append(r.searchMatches, i)
	}

	if len(r.searchMatches) > 0 {
		r.showSearchResult()
	} else {
		r.showSearchPrompt()
	}
}

// updateSearchQuery updates the search query and finds new matches
func (r *ReadLine) updateSearchQuery(query string) {
	r.searchQuery = query
	r.searchMatches = nil
	r.searchIndex = 0

	// Find matches in reverse order
	for i := len(r.history) - 1; i >= 0; i-- {
		if strings.Contains(r.history[i], query) {
			r.searchMatches = append(r.searchMatches, i)
		}
	}

	if len(r.searchMatches) > 0 {
		r.showSearchResult()
	} else {
		r.showSearchPrompt()
	}
}

// showSearchResult displays the current search match
func (r *ReadLine) showSearchResult() {
	if len(r.searchMatches) == 0 || r.searchIndex >= len(r.searchMatches) {
		return
	}

	matchIdx := r.searchMatches[r.searchIndex]
	historyItem := r.history[matchIdx]

	// Set buffer to the matched history item
	r.buffer = []rune(historyItem)
	r.cursorPos = len(r.buffer)
	r.scrollPos = 0
	if r.cursorPos >= r.width {
		r.scrollPos = r.cursorPos - r.width + 1
	}

	r.showSearchPrompt()
}

// showSearchPrompt displays the search prompt with current query and match info
func (r *ReadLine) showSearchPrompt() {
	// Clear current line
	fmt.Print("\r\033[K")

	// Show search prompt
	if len(r.searchMatches) > 0 && r.searchIndex < len(r.searchMatches) {
		matchIdx := r.searchMatches[r.searchIndex]
		// Replace newlines with spaces to avoid display issues with multi-line entries
		displayText := strings.ReplaceAll(r.history[matchIdx], "\n", " ")
		fmt.Printf("(reverse-i-search)`%s': %s", r.searchQuery, displayText)
	} else {
		fmt.Printf("(failed reverse-i-search)`%s': ", r.searchQuery)
	}
}

// nextSearchResult moves to the next search result
func (r *ReadLine) nextSearchResult() {
	if len(r.searchMatches) == 0 {
		return
	}

	r.searchIndex = (r.searchIndex + 1) % len(r.searchMatches)
	r.showSearchResult()
}

// exitSearchMode exits reverse search mode and restores normal input
func (r *ReadLine) exitSearchMode() {
	r.isSearchMode = false
	r.searchQuery = ""
	r.searchIndex = 0
	r.searchMatches = nil

	// Restore the original buffer content
	if r.originalBuffer != nil {
		r.buffer = make([]rune, len(r.originalBuffer))
		copy(r.buffer, r.originalBuffer)
		r.cursorPos = len(r.buffer)
		r.scrollPos = 0
		if r.cursorPos >= r.width {
			r.scrollPos = r.cursorPos - r.width + 1
		}
		r.originalBuffer = nil
	}

	// Clear the search prompt and show normal prompt
	fmt.Print("\r\033[2K")
	fmt.Printf("\x1b[33m%s\x1b[0m ", r.prompt)
	r.refreshLine()
}

// acceptSearchResult accepts the current search result and exits search mode
func (r *ReadLine) acceptSearchResult() {
	r.isSearchMode = false
	r.searchQuery = ""
	r.searchIndex = 0
	r.searchMatches = nil
	r.originalBuffer = nil
	// Keep the current buffer content
}

// handleSearchInput handles input when in reverse search mode
func (r *ReadLine) handleSearchInput(b byte, buf []byte) {
	switch b {
	case '\r', '\n': // Enter - accept current result
		r.acceptSearchResult()
		r.refreshLine()
	case 27: // Escape - exit search mode
		r.exitSearchMode()
	case 18: // Ctrl+R - cycle to next result
		r.nextSearchResult()
	case 3: // Ctrl+C - exit search mode
		r.exitSearchMode()
	default:
		// Handle ASCII printable characters for search query
		if b >= 32 && b <= 126 {
			r.searchQuery += string(b)
			r.updateSearchQuery(r.searchQuery)
		} else if b == 127 || b == 8 { // Backspace
			if len(r.searchQuery) > 0 {
				r.searchQuery = r.searchQuery[:len(r.searchQuery)-1]
				r.updateSearchQuery(r.searchQuery)
			}
		}
	}
}

// handleNormalInput handles normal input when not in search mode
func (r *ReadLine) handleNormalInput(b byte, buf []byte) {
	// Handle ASCII printable characters directly
	if b >= 32 && b <= 126 {
		r.insertRune(rune(b))
		r.refreshLine()
	} else if b >= 128 {
		// This is the start of a UTF-8 multi-byte sequence
		// Determine how many bytes are in this character
		totalBytes := 0
		if b&0xE0 == 0xC0 { // 2 bytes
			totalBytes = 2
		} else if b&0xF0 == 0xE0 { // 3 bytes
			totalBytes = 3
		} else if b&0xF8 == 0xF0 { // 4 bytes
			totalBytes = 4
		}

		if totalBytes > 1 {
			// Already read first byte, read remaining bytes
			for i := 1; i < totalBytes; i++ {
				n, err := os.Stdin.Read(buf[i : i+1])
				if err != nil || n == 0 {
					// If error reading additional bytes, skip this character
					break
				}
			}
			// Convert complete sequence to rune and insert
			char, _ := utf8.DecodeRune(buf[:totalBytes])
			if char != utf8.RuneError {
				r.insertRune(char)
				r.refreshLine()
			}
		}
	}
}
