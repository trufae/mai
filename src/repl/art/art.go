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
		if bodyCols < 10 {
			bodyCols = width - artCols
		}
	}

	// Wrap the text into lines fitting bodyCols (use display widths)
	words := strings.Fields(text)
	lines := []string{}
	cur := ""
	for _, w := range words {
		if DisplayWidth(cur)+1+DisplayWidth(w) <= bodyCols {
			if cur == "" {
				cur = w
			} else {
				cur = cur + " " + w
			}
		} else {
			if cur != "" {
				lines = append(lines, cur)
			}
			// if single word longer than bodyCols, break it by rune display width
			if DisplayWidth(w) > bodyCols {
				runes := []rune(w)
				part := ""
				for _, r := range runes {
					if DisplayWidth(part)+RuneWidth(r) > bodyCols {
						lines = append(lines, part)
						part = string(r)
					} else {
						part += string(r)
					}
				}
				if part != "" {
					cur = part
				} else {
					cur = ""
				}
			} else {
				cur = w
			}
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}

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

		// body block: darker background and white text, padded to bodyCols
		padded := ln
		padN := bodyCols - DisplayWidth(padded)
		if padN < 0 {
			padN = 0
		}
		padded += strings.Repeat(" ", padN)
		fmt.Fprintf(os.Stderr, "\x1b[48;5;%dm\x1b[38;5;15m%s\x1b[0m", bodyBgDark, padded)
		fmt.Fprint(os.Stderr, "\n")
	}
	// final reset
	fmt.Fprint(os.Stderr, reset)
}
