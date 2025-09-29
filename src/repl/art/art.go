package art

import (
	"fmt"
	"os"
	"strings"
)

// DebugBanner prints a colored debug banner using only ANSI 256-color escapes.
// title: short title string used to deterministically pick colors
// text: body text that will be wrapped to terminal width
func DebugBanner(title, text string) {
	width := GetTerminalWidth(os.Stderr.Fd())
	if width <= 0 {
		width = 80
	}

	// Derive a simple deterministic hash from the entire title text
	h := 0
	for _, r := range title {
		h = h*31 + int(r)
	}

	// Pleasant-ish palette chosen from xterm 256 colors (dark colors)
	palette := []int{17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46}
	p := func(offset int) int {
		idx := (h + offset) % len(palette)
		if idx < 0 {
			idx += len(palette)
		}
		return palette[idx]
	}

	titleBg := p(0)
	bodyBg := p(5)
	ribbonFg := 15
	ribbonBg := p(2)
	// darker variant for body column (clamped)
	bodyBgDark := bodyBg - 8
	if bodyBgDark < 0 {
		bodyBgDark = bodyBg
	}

	reset := "\x1b[0m"

	// Print title bar: background fill across width, title in white
	// measure title using display width (ASCII=1, others=2)
	titleRunes := []rune(title)
	tlen := DisplayWidth(title)
	if tlen > width-2 {
		// truncate title if too long (accounting for rune display width)
		truncated := []rune{}
		cur := 0
		for _, r := range titleRunes {
			w := RuneWidth(r)
			if cur+w > width-4 {
				break
			}
			truncated = append(truncated, r)
			cur += w
		}
		titleRunes = truncated
		tlen = cur
	}

	// build title line: 1 space, title, rest spaces
	leftPad := 1
	rightPad := width - leftPad - tlen
	if rightPad < 0 {
		rightPad = 0
	}
	fmt.Fprintf(os.Stderr, "\x1b[48;5;%dm\x1b[38;5;15m", titleBg)
	fmt.Fprintf(os.Stderr, "%s", strings.Repeat(" ", leftPad))
	fmt.Fprintf(os.Stderr, "%s", string(titleRunes))
	fmt.Fprintf(os.Stderr, "%s", strings.Repeat(" ", rightPad))
	fmt.Fprint(os.Stderr, reset+"\n")

	// Prepare body wrapping. Ribbon art uses these runes (each is generally width=2)
	artPattern := "◤◢◤◢"
	artCols := DisplayWidth(artPattern)
	bodyCols := width - artCols
	if bodyCols < 10 {
		// fall back to small ribbon if terminal narrow
		artPattern = "◤◢"
		artCols = DisplayWidth(artPattern)
		bodyCols = width - artCols
	}
	if bodyCols < 1 {
		bodyCols = width
	}
	bodyContentCols := bodyCols - 1
	if bodyContentCols < 1 {
		bodyContentCols = bodyCols
	}
	lines := wrapText(text, bodyContentCols)

	// rotate pattern by one rune: use slicing to keep types as string
	var artPattern2 = artPattern
	runes := []rune(artPattern)

	if len(runes) > 0 {
		artPattern2 = string(runes[1:]) + string(runes[:1])
	}
	ribbon := []string{
		artPattern,
		artPattern2,
	}
	// Print each wrapped line with art ribbon and body block
	for i, ln := range lines {
		// art block with ribbonFg on ribbonBg
		fmt.Fprintf(os.Stderr, "\x1b[38;5;%dm\x1b[48;5;%dm%s\x1b[0m", ribbonFg, ribbonBg, ribbon[i%2])

		contentWidth := DisplayWidth(ln)
		pad := bodyContentCols - contentWidth
		if pad < 0 {
			pad = 0
		}
		body := " " + ln + strings.Repeat(" ", pad)
		if extra := bodyCols - DisplayWidth(body); extra > 0 {
			body += strings.Repeat(" ", extra)
		}
		fmt.Fprintf(os.Stderr, "\x1b[48;5;%dm\x1b[38;5;15m%s\x1b[0m", bodyBgDark, body)
		fmt.Fprint(os.Stderr, "\n")
	}
	// final reset
	fmt.Fprint(os.Stderr, reset)
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var (
		lines []string
		line  strings.Builder
		lw    int
	)
	flush := func() {
		if line.Len() == 0 {
			return
		}
		lines = append(lines, line.String())
		line.Reset()
		lw = 0
	}
	appendFragment := func(fragment string) {
		fw := DisplayWidth(fragment)
		if fw == 0 {
			return
		}
		if lw == 0 {
			line.WriteString(fragment)
			lw = fw
			if lw >= width {
				flush()
			}
			return
		}
		if lw+1+fw <= width {
			line.WriteByte(' ')
			line.WriteString(fragment)
			lw += 1 + fw
			if lw >= width {
				flush()
			}
			return
		}
		flush()
		line.WriteString(fragment)
		lw = fw
		if lw >= width {
			flush()
		}
	}
	for _, word := range words {
		if DisplayWidth(word) <= width {
			appendFragment(word)
			continue
		}
		for _, part := range breakWord(word, width) {
			appendFragment(part)
		}
	}
	flush()
	return lines
}

func breakWord(word string, width int) []string {
	if width <= 0 {
		return []string{word}
	}
	var (
		parts    []string
		segment  strings.Builder
		segWidth int
	)
	flush := func() {
		if segment.Len() == 0 {
			return
		}
		parts = append(parts, segment.String())
		segment.Reset()
		segWidth = 0
	}
	for _, r := range word {
		rw := RuneWidth(r)
		if rw > width {
			flush()
			parts = append(parts, string(r))
			continue
		}
		if segWidth > 0 && segWidth+rw > width {
			flush()
		}
		segment.WriteRune(r)
		segWidth += rw
		if segWidth == width {
			flush()
		}
	}
	flush()
	if len(parts) == 0 {
		parts = append(parts, word)
	}
	return parts
}
