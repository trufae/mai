package art

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const DemoSpeed = 60 // ms per frame
// Dynamic timing bounds (ms). The loop speeds up when backlog grows.
const (
	minFrameMS           = 15  // fastest when lots of queued characters
	maxFrameMS           = 140 // slowest when no backlog
	fastBacklogThreshold = 200 // characters to reach minFrameMS
	rainbowStepMS        = 120 // time per rainbow phase step (constant speed)
)

var (
	mu          sync.Mutex
	demoRunning bool
	demoAction  string // Action label, e.g. "Thinking..."
	demoBuffer  strings.Builder
	stopChannel chan struct{}
	doneChannel chan struct{}

	// Gradient slice will be populated at init with a smoother greyscale
	gradient []string
	// rainbowColors used to color the action/prefix (e.g. "Thinking..")
	rainbowColors []string
	resetColor    = "\x1b[0m"
)

// StartLoop kept for backward compatibility; it simply sets the action
// and ensures the demo loop is running.
func StartLoop(initialMessage string) {
	SetAction(initialMessage)
}

// SetAction sets/updates the action label shown before the scrolling text.
// It ensures the demo loop is running.
func SetAction(action string) {
	mu.Lock()
	if !demoRunning {
		stopChannel = make(chan struct{})
		doneChannel = make(chan struct{})
		demoRunning = true
		go demoLoop()
	}
	demoAction = action
	mu.Unlock()
}

// FeedText appends text into the scrolling feeder. Newlines are normalized
// to spaces; existing spaces are preserved.
func FeedText(text string) {
	// Normalize common newline sequences to spaces, but keep other spaces
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	if text == "" {
		return
	}
	mu.Lock()
	demoBuffer.WriteString(text)
	mu.Unlock()
}

// StopLoop stops the demo loop
func StopLoop() {
	mu.Lock()
	if !demoRunning {
		mu.Unlock()
		return
	}
	demoBuffer.Reset()
	demoRunning = false
	close(stopChannel)
	done := doneChannel
	mu.Unlock()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
	}
}

// demoLoop runs the scrolling display. It prints the action label followed
// by a horizontally-scrolling window of the demoBuffer. The window scrolls
// character-by-character and pulls new data from FeedText when available.
func demoLoop() {
	// Hide cursor while the slider is active
	fmt.Fprint(os.Stderr, "\x1b[?25l")
	defer func() {
		// Clear line on exit
		fmt.Fprint(os.Stderr, "\r\x1b[2K")
		// Show cursor back
		fmt.Fprint(os.Stderr, "\x1b[?25h")
		close(doneChannel)
	}()

	offset := 0
	rainbowPhase := 0
	lastRainbow := time.Now()
	for {
		// Non-blocking stop check before doing work
		select {
		case <-stopChannel:
			return
		default:
		}
		mu.Lock()
		action := demoAction
		buf := demoBuffer.String()
		mu.Unlock()

		// Determine available width after action and a separating space
		width := GetTerminalWidth(os.Stderr.Fd())
		prefix := action
		if prefix != "" {
			prefix = prefix + " "
		}
		avail := width - DisplayWidth(stripANSI(prefix))
		if avail <= 0 {
			avail = 10
		}

		// Build window slice
		runes := []rune(buf)
		if offset > len(runes) {
			offset = len(runes)
		}
		cumulative := 0
		end := offset
		for end < len(runes) {
			w := RuneWidth(runes[end])
			if cumulative+w > avail {
				break
			}
			cumulative += w
			end++
		}
		var window []rune
		if offset < end {
			window = runes[offset:end]
		} else {
			window = []rune{}
		}
		// pad with spaces to reach avail columns
		padLen := avail - cumulative
		if padLen > 0 {
			pad := make([]rune, padLen)
			for i := range pad {
				pad[i] = ' '
			}
			window = append(window, pad...)
		}

		// Update rainbow phase based on wall-clock time to keep a constant speed
		if len(rainbowColors) > 0 {
			elapsed := time.Since(lastRainbow)
			step := time.Duration(rainbowStepMS) * time.Millisecond
			if elapsed >= step {
				steps := int(elapsed / step)
				rainbowPhase = (rainbowPhase + steps) % len(rainbowColors)
				lastRainbow = lastRainbow.Add(time.Duration(steps) * step)
			}
		}

		// Build colored string: rainbow for the action/prefix, then grey gradient
		var b strings.Builder
		// color the prefix (action) with the rainbow but keep spaces plain
		if prefix != "" {
			var pb strings.Builder
			prunes := []rune(prefix)
			for j, rc := range prunes {
				if rc == ' ' {
					pb.WriteRune(' ')
					continue
				}
				// offset rainbow by current phase so colors rotate per-frame
				color := rainbowColors[(j+rainbowPhase)%len(rainbowColors)]
				pb.WriteString(color)
				pb.WriteRune(rc)
				pb.WriteString(resetColor)
			}
			b.WriteString(pb.String())
		}
		// displayPos tracks visible columns written (spaces count, control chars ignored)
		displayPos := 0
		for _, r := range window {
			// ignore control characters that would break layout when used in
			// "thinking" chunks: newlines, carriage returns, tabs
			if r == '\n' || r == '\r' || r == '\t' {
				// skip entirely (do not advance display position)
				continue
			}
			// don't color plain spaces — keep them as plain spaces for
			// consistent readability and to avoid invisible/dim gaps
			if r == ' ' {
				b.WriteRune(' ')
				displayPos++
				continue
			}
			// pick gradient index based on visible column so the fade maps to
			// screen position rather than raw buffer index
			gi := (displayPos * len(gradient)) / (avail + 1)
			if gi < 0 {
				gi = 0
			}
			if gi >= len(gradient) {
				gi = len(gradient) - 1
			}
			b.WriteString(gradient[gi])
			b.WriteRune(r)
			displayPos += RuneWidth(r)
		}
		b.WriteString(resetColor)

		// Print line (clear and redraw) to avoid artifacts
		fmt.Fprint(os.Stderr, "\r\x1b[2K")
		fmt.Fprint(os.Stderr, b.String())

		// Advance offset for scrolling; if we've displayed to the end and
		// there's no more data, don't advance past buffer length (stay until
		// new data arrives)
		mu.Lock()
		bufLen := len([]rune(demoBuffer.String()))
		mu.Unlock()
		if offset+1+avail <= bufLen {
			offset++
		} else if offset < bufLen {
			offset++
		}

		// rainbow phase advancement happens based on time above

		// Determine next delay based on backlog of queued characters
		// Backlog is how many runes are beyond the end of the visible window
		mu.Lock()
		currentLen := len([]rune(demoBuffer.String()))
		mu.Unlock()
		backlog := currentLen - (offset + avail)
		if backlog < 0 {
			backlog = 0
		}
		nextMS := maxFrameMS
		if backlog > 0 {
			if backlog > fastBacklogThreshold {
				backlog = fastBacklogThreshold
			}
			span := float64(maxFrameMS - minFrameMS)
			factor := float64(fastBacklogThreshold-backlog) / float64(fastBacklogThreshold)
			nextMS = minFrameMS + int(span*factor+0.5)
		}

		// Wait for next frame or stop
		select {
		case <-stopChannel:
			return
		case <-time.After(time.Duration(nextMS) * time.Millisecond):
		}
	}
}

// stripANSI removes simple ANSI color codes used in prefixes when measuring length
func stripANSI(s string) string {
	// Very small helper: strip ESC sequences of the form \x1b[...m
	res := ""
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' || s[i] == 27 {
			// skip until 'm' or end
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) && s[i] == 'm' {
				i++
			}
			continue
		}
		res += string(s[i])
		i++
	}
	return res
}

// init populates a smoother greyscale gradient used for scrolling text.
func init() {
	// choose a moderate number of steps for a smooth fade
	gradient = makeGreyGradient(14)
	rainbowColors = makeRainbowColors()
}

// makeGreyGradient returns `count` ANSI 256-color greyscale color codes
// spanning the grey ramp (232..255). These map to progressively lighter
// greys and give a smooth left->right fade for the scrolling text.
func makeGreyGradient(count int) []string {
	if count <= 0 {
		count = 1
	}
	// avoid extremely dark greys which are hard to read on many terminals;
	// start from a lighter greyscale index
	start, end := 240, 255
	if count == 1 {
		return []string{fmt.Sprintf("\x1b[38;5;%dm", end)}
	}
	out := make([]string, count)
	for i := 0; i < count; i++ {
		val := start + (i*(end-start))/(count-1)
		out[i] = fmt.Sprintf("\x1b[38;5;%dm", val)
	}
	return out
}

// makeRainbowColors returns a short sequence of ANSI 256-color codes that
// approximate a rainbow. These are used to color the action label (prefix).
func makeRainbowColors() []string {
	// Softer pastel-like 256-color palette (red→orange→yellow→green→cyan→blue→magenta)
	// Chosen from the xterm 256-color cube to be less saturated/bright
	cols := []int{181, 186, 191, 151, 116, 110, 183}
	out := make([]string, len(cols))
	for i, v := range cols {
		out[i] = fmt.Sprintf("\x1b[38;5;%dm", v)
	}
	return out
}
