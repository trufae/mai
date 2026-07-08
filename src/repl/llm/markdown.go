package llm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ANSI color codes for terminal output
const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Italic    = "\033[3m"
	Underline = "\033[4m"

	// Colors
	Black   = "\033[30m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"

	// Bright colors
	BrightBlack   = "\033[90m"
	BrightRed     = "\033[91m"
	BrightGreen   = "\033[92m"
	BrightYellow  = "\033[93m"
	BrightBlue    = "\033[94m"
	BrightMagenta = "\033[95m"
	BrightCyan    = "\033[96m"
	BrightWhite   = "\033[97m"
)

// Color scheme for different markdown elements
var (
	// Headers
	H1Color = BrightMagenta + Bold
	H2Color = BrightBlue + Bold
	H3Color = BrightCyan + Bold
	H4Color = Blue + Bold
	H5Color = Cyan + Bold
	H6Color = BrightBlack + Bold

	// Lists
	ListItemColor = BrightYellow

	// Code
	CodeBlockColor  = BrightGreen
	InlineCodeColor = Green

	// Emphasis
	BoldColor   = Cyan + Bold
	ItalicColor = Italic

	// Links and quotes
	LinkColor  = BrightCyan + Underline
	QuoteColor = BrightBlack + Italic
)

// ElementType represents different markdown element types
type ElementType int

const (
	TextElement ElementType = iota
	HeaderElement
	ListItemElement
	BlockquoteElement
	CodeBlockElement
	InlineCodeElement
	BoldElement
	ItalicElement
	LinkElement
)

// MarkdownElement represents a single markdown element with its type and content
type MarkdownElement struct {
	Type    ElementType
	Content string
	Level   int // For headers (1-6)
}

// MarkdownOptions controls terminal-specific markdown rendering.
type MarkdownOptions struct {
	TableWidth int
	UTF8       bool
	Colors     bool
}

var markdownOptions = MarkdownOptions{
	UTF8:   true,
	Colors: true,
}

// SetMarkdownOptions updates the package-level renderer options.
func SetMarkdownOptions(opts MarkdownOptions) {
	if opts.TableWidth < 0 {
		opts.TableWidth = 0
	}
	markdownOptions = opts
	ResetStreamRenderer()
}

// MarkdownRenderer is the state machine for processing markdown
type MarkdownRenderer struct {
	buffer            string
	currentElement    MarkdownElement
	elements          []MarkdownElement
	isStreaming       bool
	inCodeFence       bool
	codeLanguage      string
	isEscaping        bool
	isInLineStart     bool
	currentLineBuffer string
	linkTextBuffer    string
	linkURLBuffer     string
	inLinkText        bool
	inLinkURL         bool
	boldMarker        rune // '*' or '_' for bold
	italicMarker      rune // '*' or '_' for italic
	nestingLevel      int  // For handling nested formatting
	tableBuffer       []string

	// Streaming-specific pending state to handle cross-chunk emphasis markers
	pendingEmphasis bool
	pendingMarker   rune

	// State flags
	collectingHeader     bool
	headerLevel          int
	collectingListItem   bool
	collectingBlockquote bool
	collectingInlineCode bool
	collectingBold       bool
	collectingItalic     bool
	collectingLinkText   bool
	collectingLinkURL    bool

	// Markers for multi-character sequences
	boldStartPos       int
	italicStartPos     int
	inlineCodeStartPos int
	linkTextStartPos   int
	linkURLStartPos    int
}

// NewMarkdownRenderer creates a new markdown renderer
func NewMarkdownRenderer(isStreaming bool) *MarkdownRenderer {
	return &MarkdownRenderer{
		buffer:         "",
		isStreaming:    isStreaming,
		isInLineStart:  true,
		currentElement: MarkdownElement{Type: TextElement, Content: ""},
		elements:       make([]MarkdownElement, 0),
	}
}

// Process handles a chunk of text (or the entire text in non-streaming mode)
func (r *MarkdownRenderer) Process(chunk string) string {
	// Replace Windows-style line endings with Unix-style
	chunk = strings.ReplaceAll(chunk, "\r\n", "\n")

	var result strings.Builder

	// In streaming mode, buffer by line and only render on newline
	if r.isStreaming {
		r.currentLineBuffer += chunk
		for {
			idx := strings.IndexRune(r.currentLineBuffer, '\n')
			if idx == -1 {
				break
			}
			line := r.currentLineBuffer[:idx]
			r.currentLineBuffer = r.currentLineBuffer[idx+1:]
			result.WriteString(r.renderStreamLine(line, true))
			r.isInLineStart = true
		}
		return result.String()
	}

	chunk = renderMarkdownTables(chunk)

	// Process character by character, supporting multibyte UTF-8 runes
	runes := []rune(chunk)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		// If we have a pending single emphasis marker from previous chunk,
		// try to resolve it now with the current character.
		if r.pendingEmphasis {
			if c == r.pendingMarker {
				// We have a pair across chunks (e.g., ** or __)
				if r.collectingBold && c == r.boldMarker {
					// End of bold, add the element
					r.collectingBold = false
					formatted := markdownColor(BoldColor, r.currentElement.Content)
					if r.collectingListItem {
						r.currentElement.Type = ListItemElement
						r.currentElement.Content = formatted
					} else {
						result.WriteString(formatted)
						r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
					}
				} else if !r.collectingBold && !r.collectingItalic {
					// Start of bold
					if r.currentElement.Content != "" {
						// Flush any existing content
						result.WriteString(r.currentElement.Content)
					}
					r.collectingBold = true
					r.boldMarker = c
					r.currentElement = MarkdownElement{Type: BoldElement, Content: ""}
				} else {
					// Inside another formatting context, treat as literal
					r.currentElement.Content += string(r.pendingMarker) + string(c)
				}
				r.pendingEmphasis = false
				continue
			} else {
				// No pair; flush the pending marker as literal and keep processing c
				r.currentElement.Content += string(r.pendingMarker)
				r.pendingEmphasis = false
				// fallthrough to process c normally
			}
		}

		// Handle escape character
		if c == '\\' && !r.isEscaping {
			r.isEscaping = true
			continue
		}

		if r.isEscaping {
			r.buffer += string(c)
			r.isEscaping = false
			continue
		}

		// Check if we're at the start of a new line
		if r.isInLineStart {
			// Handle headers (# Header)
			if c == '#' {
				headerCount := 1
				j := i + 1
				for j < len(runes) && runes[j] == '#' && headerCount < 6 {
					headerCount++
					j++
				}

				// Validate that it's a proper header (needs space after #)
				if j < len(runes) && runes[j] == ' ' {
					r.collectingHeader = true
					r.headerLevel = headerCount
					r.currentElement = MarkdownElement{Type: HeaderElement, Level: headerCount, Content: ""}
					i = j // Skip past the # and space
					r.isInLineStart = false
					continue
				}
			}

			// Handle list items (- item or * item)
			if (c == '-' || c == '*') && i+1 < len(runes) && runes[i+1] == ' ' {
				r.collectingListItem = true
				r.currentElement = MarkdownElement{Type: ListItemElement, Content: ""}
				i += 1 // Skip the marker and space
				r.isInLineStart = false
				continue
			}

			// Handle ordered list items (1. item)
			if isDigit(c) {
				j := i
				for j < len(runes) && isDigit(runes[j]) {
					j++
				}
				if j < len(runes) && runes[j] == '.' && j+1 < len(runes) && runes[j+1] == ' ' {
					r.collectingListItem = true
					r.currentElement = MarkdownElement{Type: ListItemElement, Content: ""}
					i = j + 1 // Skip past the number, dot, and space
					r.isInLineStart = false
					continue
				}
			}

			// Handle blockquotes (> quote)
			if c == '>' && i+1 < len(runes) && runes[i+1] == ' ' {
				r.collectingBlockquote = true
				r.currentElement = MarkdownElement{Type: BlockquoteElement, Content: ""}
				i += 1 // Skip the > and space
				r.isInLineStart = false
				continue
			}

			// Handle code fences (```)
			if c == '`' && i+2 < len(runes) && runes[i+1] == '`' && runes[i+2] == '`' {
				r.inCodeFence = !r.inCodeFence
				if r.inCodeFence {
					// Look for optional language identifier
					langStart := i + 3
					for langStart < len(runes) && runes[langStart] == ' ' {
						langStart++
					}
					langEnd := langStart
					for langEnd < len(runes) && runes[langEnd] != '\n' {
						langEnd++
					}
					if langEnd > langStart {
						r.codeLanguage = string(runes[langStart:langEnd])
					}
					r.currentElement = MarkdownElement{Type: CodeBlockElement, Content: ""}
				} else {
					// End of code block, add the element
					result.WriteString(formatElement(r.currentElement))
					r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
					r.codeLanguage = ""
				}
				i += 2 // Skip past the ```
				if i+1 < len(runes) && runes[i+1] == '\n' {
					i++ // Skip the newline after code fence
				}
				r.isInLineStart = true
				continue
			}

			r.isInLineStart = false
		}

		// Inside code fence - collect everything until the closing fence
		if r.inCodeFence && r.currentElement.Type == CodeBlockElement {
			// Check for closing fence
			if c == '`' && i+2 < len(runes) && runes[i+1] == '`' && runes[i+2] == '`' {
				r.inCodeFence = false
				result.WriteString(formatElement(r.currentElement))
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
				i += 2 // Skip the remaining `` characters
				if i+1 < len(runes) && runes[i+1] == '\n' {
					i++ // Skip the newline after code fence
				}
				r.isInLineStart = true
				continue
			}

			// Add character to code block content
			if c == '\n' {
				r.currentElement.Content += "\r\n"
				r.isInLineStart = true
			} else {
				r.currentElement.Content += string(c)
			}
			continue
		}

		// Handle inline code (`code`)
		if c == '`' && !r.inCodeFence {
			if r.collectingInlineCode {
				// End of inline code, add the element
				r.collectingInlineCode = false
				formatted := markdownColor(InlineCodeColor, r.currentElement.Content)
				if r.collectingListItem {
					r.currentElement.Type = ListItemElement
					r.currentElement.Content = formatted
				} else {
					result.WriteString(formatted)
					r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
				}
			} else {
				// Start of inline code
				if r.currentElement.Content != "" {
					// Flush any existing content
					result.WriteString(r.currentElement.Content)
				}
				r.collectingInlineCode = true
				r.currentElement = MarkdownElement{Type: InlineCodeElement, Content: ""}
			}
			continue
		}

		// Handle emphasis markers (*) with support for cross-chunk bold pairs
		if c == '*' {
			// Bold pair within the same chunk
			if i+1 < len(runes) && runes[i+1] == c {
				if r.collectingBold && c == r.boldMarker {
					// End of bold, add the element
					r.collectingBold = false
					formatted := markdownColor(BoldColor, r.currentElement.Content)
					if r.collectingListItem {
						r.currentElement.Type = ListItemElement
						r.currentElement.Content = formatted
					} else {
						result.WriteString(formatted)
						r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
					}
					i++ // Skip the second * or _
				} else if !r.collectingBold && !r.collectingItalic {
					// Start of bold
					if r.currentElement.Content != "" {
						// Flush any existing content
						result.WriteString(r.currentElement.Content)
					}
					r.collectingBold = true
					r.boldMarker = c
					r.currentElement = MarkdownElement{Type: BoldElement, Content: ""}
					i++ // Skip the second * or _
				} else {
					// Just add the character if we're in another formatting context
					r.currentElement.Content += string(c)
				}
				continue
			}
			// No immediate pair. In streaming mode, defer decision to next chunk
			if r.isStreaming {
				r.pendingEmphasis = true
				r.pendingMarker = c
				continue
			}
			// Non-streaming: handle italics as before
			if !r.collectingBold {
				if r.collectingItalic && c == r.italicMarker {
					// End of italic, add the element
					r.collectingItalic = false
					formatted := markdownColor(ItalicColor, r.currentElement.Content)
					if r.collectingListItem {
						r.currentElement.Type = ListItemElement
						r.currentElement.Content = formatted
					} else {
						result.WriteString(formatted)
						r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
					}
					continue
				} else if !r.collectingItalic {
					// Start of italic
					if r.currentElement.Content != "" {
						// Flush any existing content
						result.WriteString(r.currentElement.Content)
					}
					r.collectingItalic = true
					r.italicMarker = c
					r.currentElement = MarkdownElement{Type: ItalicElement, Content: ""}
					continue
				}
			}
			// Fallback: treat as literal
			r.currentElement.Content += string(c)
			continue
		}

		// Handle link [text](url)
		if c == '[' && !r.collectingLinkText && !r.collectingLinkURL && !r.collectingInlineCode {
			if r.currentElement.Content != "" {
				// Flush any existing content
				result.WriteString(r.currentElement.Content)
			}
			r.collectingLinkText = true
			r.linkTextBuffer = ""
			continue
		}

		if c == ']' && r.collectingLinkText {
			if i+1 < len(runes) && runes[i+1] == '(' {
				r.collectingLinkText = false
				r.collectingLinkURL = true
				i++ // Skip the opening (
				continue
			} else {
				// Not a valid link, treat as regular text
				r.linkTextBuffer += "]"
				r.collectingLinkText = false
				// Add the collected text as regular content
				if r.currentElement.Content != "" {
					result.WriteString(r.currentElement.Content)
				}
				r.currentElement.Content = "[" + r.linkTextBuffer
				r.linkTextBuffer = ""
				continue
			}
		}

		if c == ')' && r.collectingLinkURL {
			r.collectingLinkURL = false
			// Format and add the link
			result.WriteString(formatLink(r.linkTextBuffer, r.currentElement.Content))
			r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			continue
		}

		// Handle newlines
		if c == '\n' {
			// Finish any current element
			if r.collectingHeader {
				r.collectingHeader = false
				result.WriteString(formatHeader(r.headerLevel, r.currentElement.Content))
				r.headerLevel = 0
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingListItem {
				r.collectingListItem = false
				result.WriteString(formatListItem(r.currentElement.Content))
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingBlockquote {
				r.collectingBlockquote = false
				result.WriteString(markdownColor(QuoteColor, r.currentElement.Content))
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingLinkText {
				// Incomplete link, treat as regular text
				result.WriteString("[" + r.linkTextBuffer)
				r.collectingLinkText = false
				r.linkTextBuffer = ""
			} else if r.collectingLinkURL {
				// Incomplete link, treat as regular text
				result.WriteString("[" + r.linkTextBuffer + "](" + r.currentElement.Content)
				r.collectingLinkURL = false
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
				r.linkTextBuffer = ""
			} else if r.collectingInlineCode {
				// Incomplete inline code, treat as regular text
				result.WriteString("`" + r.currentElement.Content)
				r.collectingInlineCode = false
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingBold {
				// Incomplete bold, treat as regular text
				result.WriteString(string(r.boldMarker) + string(r.boldMarker) + r.currentElement.Content)
				r.collectingBold = false
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingItalic {
				// Incomplete italic, treat as regular text
				result.WriteString(string(r.italicMarker) + r.currentElement.Content)
				r.collectingItalic = false
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.currentElement.Type == TextElement && r.currentElement.Content != "" {
				result.WriteString(r.currentElement.Content)
				r.currentElement.Content = ""
			}

			// Add the newline character for terminal display
			result.WriteString("\r\n")
			r.isInLineStart = true
			continue
		}

		// Collect character for the current element
		if r.collectingLinkText {
			r.linkTextBuffer += string(c)
		} else if r.collectingLinkURL || r.collectingHeader || r.collectingListItem ||
			r.collectingBlockquote || r.collectingInlineCode || r.collectingBold ||
			r.collectingItalic {
			r.currentElement.Content += string(c)
		} else {
			r.currentElement.Content += string(c)
		}
	}

	// Handle any remaining content
	if r.currentElement.Content != "" && r.currentElement.Type == TextElement {
		result.WriteString(r.currentElement.Content)
		r.currentElement.Content = ""
	}

	// Handle incomplete states
	if r.collectingLinkText {
		result.WriteString("[" + r.linkTextBuffer)
		r.collectingLinkText = false
		r.linkTextBuffer = ""
	} else if r.collectingLinkURL {
		result.WriteString("[" + r.linkTextBuffer + "](" + r.currentElement.Content)
		r.collectingLinkURL = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
		r.linkTextBuffer = ""
	} else if r.collectingInlineCode {
		result.WriteString("`" + r.currentElement.Content)
		r.collectingInlineCode = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.collectingBold {
		result.WriteString(string(r.boldMarker) + string(r.boldMarker) + r.currentElement.Content)
		r.collectingBold = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.collectingItalic {
		result.WriteString(string(r.italicMarker) + r.currentElement.Content)
		r.collectingItalic = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	}
	// Note: collectingHeader, collectingListItem, and collectingBlockquote
	// are left in their collecting state for streaming mode. They will be
	// handled in Flush() if the stream ends.

	return result.String()
}

// renderStreamLine formats a single line for streaming mode, keeping multi-line
// state like code fences and markdown tables.
func (r *MarkdownRenderer) renderStreamLine(line string, complete bool) string {
	trimmed := strings.TrimSpace(line)
	newline := ""
	if complete {
		newline = "\r\n"
	}

	// Handle code fences in streaming mode
	if strings.HasPrefix(trimmed, "```") {
		prefix := r.flushTableBuffer(true)
		fenceRest := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		if !r.inCodeFence {
			// Opening fence: capture language (optional) and enter code block
			r.inCodeFence = true
			r.codeLanguage = fenceRest
		} else {
			// Closing fence
			r.inCodeFence = false
			r.codeLanguage = ""
		}
		// Do not render the fence line itself
		return prefix + newline
	}
	if r.inCodeFence {
		// Inside code block: render raw content with code color
		if line == "" {
			return newline
		}
		return markdownColor(CodeBlockColor, line) + newline
	}

	if isPotentialTableLine(line) {
		r.tableBuffer = append(r.tableBuffer, line)
		return ""
	}

	prefix := r.flushTableBuffer(true)
	return prefix + renderStreamPlainLine(line) + newline
}

func renderStreamPlainLine(line string) string {
	// For regular markdown lines, reuse the non-streaming renderer to format the full line.
	// Append a newline so header/list parsing finalizes; then strip the CRLF we get back.
	tmp := NewMarkdownRenderer(false)
	out := tmp.Process(line + "\n")
	out = strings.TrimSuffix(out, "\r\n")
	return out
}

func (r *MarkdownRenderer) flushTableBuffer(terminated bool) string {
	if len(r.tableBuffer) == 0 {
		return ""
	}
	lines := r.tableBuffer
	r.tableBuffer = nil

	var out string
	if table, ok := parseMarkdownTable(lines); ok {
		out = strings.ReplaceAll(renderMarkdownTable(table), "\n", "\r\n")
	} else {
		rendered := make([]string, 0, len(lines))
		for _, line := range lines {
			rendered = append(rendered, renderStreamPlainLine(line))
		}
		out = strings.Join(rendered, "\r\n")
	}
	if terminated {
		return out + "\r\n"
	}
	return out
}

type tableAlign int

const (
	tableAlignLeft tableAlign = iota
	tableAlignRight
	tableAlignCenter
)

type markdownTable struct {
	headers []string
	aligns  []tableAlign
	rows    [][]string
}

func renderMarkdownTables(text string) string {
	lines := strings.Split(text, "\n")
	trailingNewline := strings.HasSuffix(text, "\n")
	var out strings.Builder
	inCodeFence := false

	for i := 0; i < len(lines); {
		if i == len(lines)-1 && lines[i] == "" && trailingNewline {
			break
		}
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
		}
		if !inCodeFence && i+1 < len(lines) {
			tableLines := collectMarkdownTable(lines[i:])
			if len(tableLines) > 0 {
				if table, ok := parseMarkdownTable(tableLines); ok {
					out.WriteString(renderMarkdownTable(table))
					i += len(tableLines)
					if i < len(lines)-1 || trailingNewline {
						out.WriteByte('\n')
					}
					continue
				}
			}
		}
		out.WriteString(lines[i])
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
		i++
	}
	return out.String()
}

func collectMarkdownTable(lines []string) []string {
	if len(lines) < 2 || !isPotentialTableLine(lines[0]) || !isMarkdownTableSeparator(lines[1]) {
		return nil
	}
	tableLines := []string{lines[0], lines[1]}
	for i := 2; i < len(lines); i++ {
		if !isPotentialTableLine(lines[i]) {
			break
		}
		tableLines = append(tableLines, lines[i])
	}
	return tableLines
}

func isPotentialTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(trimmed, "|") {
		return false
	}
	return strings.HasPrefix(trimmed, "|") || strings.HasSuffix(trimmed, "|") || strings.Count(trimmed, "|") > 1
}

func isMarkdownTableSeparator(line string) bool {
	cells := splitMarkdownTableRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if _, ok := parseTableSeparatorCell(cell); !ok {
			return false
		}
	}
	return true
}

func parseMarkdownTable(lines []string) (markdownTable, bool) {
	if len(lines) < 2 {
		return markdownTable{}, false
	}
	headers := splitMarkdownTableRow(lines[0])
	separatorCells := splitMarkdownTableRow(lines[1])
	if len(headers) == 0 || len(headers) != len(separatorCells) {
		return markdownTable{}, false
	}

	aligns := make([]tableAlign, len(separatorCells))
	for i, cell := range separatorCells {
		align, ok := parseTableSeparatorCell(cell)
		if !ok {
			return markdownTable{}, false
		}
		aligns[i] = align
	}

	table := markdownTable{
		headers: normalizeTableRow(headers, len(headers)),
		aligns:  aligns,
	}
	for _, line := range lines[2:] {
		table.rows = append(table.rows, normalizeTableRow(splitMarkdownTableRow(line), len(headers)))
	}
	return table, true
}

func splitMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "|") {
		line = line[1:]
	}
	if strings.HasSuffix(line, "|") {
		line = line[:len(line)-1]
	}

	var cells []string
	var cell strings.Builder
	escaped := false
	for _, r := range line {
		if escaped {
			if r != '|' {
				cell.WriteRune('\\')
			}
			cell.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteRune(r)
		}
	}
	if escaped {
		cell.WriteRune('\\')
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	return cells
}

func parseTableSeparatorCell(cell string) (tableAlign, bool) {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return tableAlignLeft, false
	}
	left := strings.HasPrefix(cell, ":")
	right := strings.HasSuffix(cell, ":")
	cell = strings.TrimPrefix(cell, ":")
	cell = strings.TrimSuffix(cell, ":")
	if len(cell) < 3 {
		return tableAlignLeft, false
	}
	for _, r := range cell {
		if r != '-' {
			return tableAlignLeft, false
		}
	}
	switch {
	case left && right:
		return tableAlignCenter, true
	case right:
		return tableAlignRight, true
	default:
		return tableAlignLeft, true
	}
}

func normalizeTableRow(cells []string, width int) []string {
	row := make([]string, width)
	for i := range row {
		if i < len(cells) {
			row[i] = cells[i]
		}
	}
	return row
}

func renderMarkdownTable(table markdownTable) string {
	widths := tableColumnWidths(table)
	glyphs := tableGlyphSet()
	var out strings.Builder

	out.WriteString(tableBorder(widths, glyphs.topLeft, glyphs.topMiddle, glyphs.topRight))
	out.WriteByte('\n')
	out.WriteString(renderTableRow(table.headers, widths, table.aligns))
	out.WriteByte('\n')
	out.WriteString(tableBorder(widths, glyphs.middleLeft, glyphs.middle, glyphs.middleRight))
	for _, row := range table.rows {
		out.WriteByte('\n')
		out.WriteString(renderTableRow(row, widths, table.aligns))
		out.WriteByte('\n')
		out.WriteString(tableBorder(widths, glyphs.middleLeft, glyphs.middle, glyphs.middleRight))
	}
	rendered := out.String()
	separator := tableBorder(widths, glyphs.middleLeft, glyphs.middle, glyphs.middleRight)
	if strings.HasSuffix(rendered, separator) {
		return strings.TrimSuffix(rendered, separator) + tableBorder(widths, glyphs.bottomLeft, glyphs.bottomMiddle, glyphs.bottomRight)
	}
	return rendered + "\n" + tableBorder(widths, glyphs.bottomLeft, glyphs.bottomMiddle, glyphs.bottomRight)
}

func tableColumnWidths(table markdownTable) []int {
	widths := make([]int, len(table.headers))
	rows := append([][]string{table.headers}, table.rows...)
	for _, row := range rows {
		for i, cell := range row {
			width := tableTextWidth(renderTableCellMarkdown(cell))
			if width > widths[i] {
				widths[i] = width
			}
		}
	}
	for i := range widths {
		if widths[i] < 3 {
			widths[i] = 3
		}
	}

	available := terminalTableWidth()
	if minTotal := tableRenderedWidth(len(widths), 1); available < minTotal {
		available = minTotal
	}
	for tableRenderedWidthSum(widths) > available {
		widest := 0
		for i := 1; i < len(widths); i++ {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 1 {
			break
		}
		widths[widest]--
	}
	return widths
}

func terminalTableWidth() int {
	if markdownOptions.TableWidth > 0 {
		return minimumTableWidth(markdownOptions.TableWidth)
	}
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width > 0 {
		return minimumTableWidth(width)
	}
	width, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || width <= 0 {
		return 80
	}
	return minimumTableWidth(width)
}

func minimumTableWidth(width int) int {
	if width < 20 {
		return 20
	}
	return width
}

func tableRenderedWidth(cols, cellWidth int) int {
	return cols*cellWidth + 3*cols + 1
}

func tableRenderedWidthSum(widths []int) int {
	total := 1
	for _, width := range widths {
		total += width + 3
	}
	return total
}

type tableGlyphs struct {
	horizontal   string
	vertical     string
	topLeft      string
	topMiddle    string
	topRight     string
	middleLeft   string
	middle       string
	middleRight  string
	bottomLeft   string
	bottomMiddle string
	bottomRight  string
}

func tableGlyphSet() tableGlyphs {
	if !markdownOptions.UTF8 {
		return tableGlyphs{
			horizontal:   "-",
			vertical:     "|",
			topLeft:      "+",
			topMiddle:    "+",
			topRight:     "+",
			middleLeft:   "+",
			middle:       "+",
			middleRight:  "+",
			bottomLeft:   "+",
			bottomMiddle: "+",
			bottomRight:  "+",
		}
	}
	return tableGlyphs{
		horizontal:   "─",
		vertical:     "│",
		topLeft:      "┌",
		topMiddle:    "┬",
		topRight:     "┐",
		middleLeft:   "├",
		middle:       "┼",
		middleRight:  "┤",
		bottomLeft:   "└",
		bottomMiddle: "┴",
		bottomRight:  "┘",
	}
}

func tableBorder(widths []int, left, middle, right string) string {
	glyphs := tableGlyphSet()
	var out strings.Builder
	out.WriteString(left)
	for _, width := range widths {
		out.WriteString(strings.Repeat(glyphs.horizontal, width+2))
		out.WriteString(middle)
	}
	return strings.TrimSuffix(out.String(), middle) + right
}

func renderTableRow(row []string, widths []int, aligns []tableAlign) string {
	glyphs := tableGlyphSet()
	wrapped := make([][]string, len(widths))
	height := 1
	for i, width := range widths {
		if i < len(row) {
			wrapped[i] = wrapTableCell(renderTableCellMarkdown(row[i]), width)
		} else {
			wrapped[i] = []string{""}
		}
		if len(wrapped[i]) > height {
			height = len(wrapped[i])
		}
	}

	var out strings.Builder
	for line := 0; line < height; line++ {
		if line > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(glyphs.vertical)
		for col, width := range widths {
			cell := ""
			if line < len(wrapped[col]) {
				cell = wrapped[col][line]
			}
			out.WriteByte(' ')
			out.WriteString(alignTableCell(cell, width, aligns[col]))
			out.WriteByte(' ')
			out.WriteString(glyphs.vertical)
		}
	}
	return out.String()
}

func renderTableCellMarkdown(cell string) string {
	renderer := NewMarkdownRenderer(false)
	renderer.isInLineStart = false
	out := renderer.Process(cell)
	out = strings.ReplaceAll(out, "\r\n", " ")
	out = strings.ReplaceAll(out, "\n", " ")
	return strings.TrimSpace(out)
}

func alignTableCell(cell string, width int, align tableAlign) string {
	pad := width - tableTextWidth(cell)
	if pad < 0 {
		pad = 0
	}
	switch align {
	case tableAlignRight:
		return strings.Repeat(" ", pad) + cell
	case tableAlignCenter:
		left := pad / 2
		right := pad - left
		return strings.Repeat(" ", left) + cell + strings.Repeat(" ", right)
	default:
		return cell + strings.Repeat(" ", pad)
	}
}

func wrapTableCell(cell string, width int) []string {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return []string{""}
	}

	var lines []string
	var current string
	for _, word := range strings.Fields(cell) {
		for tableTextWidth(word) > width {
			prefix, rest := splitTableWord(word, width)
			if current != "" {
				lines = append(lines, closeTableLine(current))
				current = ""
			}
			lines = append(lines, prefix)
			word = rest
		}
		if current == "" {
			current = word
			continue
		}
		if tableTextWidth(current)+1+tableTextWidth(word) <= width {
			current += " " + word
		} else {
			active := tableActiveANSI(current)
			lines = append(lines, closeTableLine(current))
			if active != "" {
				word = active + word
			}
			current = word
		}
	}
	if current != "" {
		lines = append(lines, closeTableLine(current))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func splitTableWord(word string, width int) (string, string) {
	if width < 1 {
		return "", word
	}
	if tableTextWidth(word) <= width {
		return word, ""
	}
	var prefix strings.Builder
	var rest strings.Builder
	var active strings.Builder
	visible := 0
	inRest := false
	for i := 0; i < len(word); {
		if seq, next := readANSISequence(word, i); seq != "" {
			if inRest {
				rest.WriteString(seq)
			} else {
				prefix.WriteString(seq)
			}
			if seq == Reset {
				active.Reset()
			} else if strings.HasSuffix(seq, "m") {
				active.WriteString(seq)
			}
			i = next
			continue
		}

		r, size := utf8.DecodeRuneInString(word[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if visible >= width {
			if !inRest {
				if active.Len() > 0 {
					prefix.WriteString(Reset)
					rest.WriteString(active.String())
				}
				inRest = true
			}
			rest.WriteRune(r)
		} else {
			prefix.WriteRune(r)
			visible++
		}
		i += size
	}
	return prefix.String(), rest.String()
}

func closeTableLine(line string) string {
	if tableActiveANSI(line) == "" {
		return line
	}
	return line + Reset
}

func tableActiveANSI(s string) string {
	var active strings.Builder
	for i := 0; i < len(s); {
		seq, next := readANSISequence(s, i)
		if seq == "" {
			_, size := utf8.DecodeRuneInString(s[i:])
			if size == 0 {
				break
			}
			i += size
			continue
		}
		if seq == Reset {
			active.Reset()
		} else if strings.HasSuffix(seq, "m") {
			active.WriteString(seq)
		}
		i = next
	}
	return active.String()
}

func tableTextWidth(s string) int {
	return len([]rune(stripANSI(s)))
}

func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if _, next := readANSISequence(s, i); next > i {
			i = next
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		out.WriteRune(r)
		i += size
	}
	return out.String()
}

func readANSISequence(s string, start int) (string, int) {
	if start+1 >= len(s) || s[start] != 0x1b || s[start+1] != '[' {
		return "", start
	}
	for i := start + 2; i < len(s); i++ {
		if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
			return s[start : i+1], i + 1
		}
	}
	return "", start
}

// Flush handles any remaining content in the buffer
// This is used in streaming mode to finish processing
func (r *MarkdownRenderer) Flush() string {
	var result strings.Builder

	// In streaming mode, flush any remaining partial line
	if r.isStreaming && r.currentLineBuffer != "" {
		result.WriteString(r.renderStreamLine(r.currentLineBuffer, false))
		r.currentLineBuffer = ""
	}
	result.WriteString(r.flushTableBuffer(false))

	// If we had a pending emphasis marker, flush it as literal
	if r.pendingEmphasis {
		result.WriteString(string(r.pendingMarker))
		r.pendingEmphasis = false
	}

	// Handle incomplete states first
	if r.collectingLinkText {
		result.WriteString("[" + r.linkTextBuffer)
		r.collectingLinkText = false
		r.linkTextBuffer = ""
	} else if r.collectingLinkURL {
		result.WriteString("[" + r.linkTextBuffer + "](" + r.currentElement.Content)
		r.collectingLinkURL = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
		r.linkTextBuffer = ""
	} else if r.collectingInlineCode {
		result.WriteString("`" + r.currentElement.Content)
		r.collectingInlineCode = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.collectingBold {
		result.WriteString(string(r.boldMarker) + string(r.boldMarker) + r.currentElement.Content)
		r.collectingBold = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.collectingItalic {
		result.WriteString(string(r.italicMarker) + r.currentElement.Content)
		r.collectingItalic = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.collectingHeader {
		// Incomplete header at end of stream - format as header
		result.WriteString(formatHeader(r.headerLevel, r.currentElement.Content))
		r.collectingHeader = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
		r.headerLevel = 0
	} else if r.collectingListItem {
		// Incomplete list item at end of stream - format as list item
		result.WriteString(formatListItem(r.currentElement.Content))
		r.collectingListItem = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.collectingBlockquote {
		// Incomplete blockquote at end of stream - format as blockquote
		result.WriteString(markdownColor(QuoteColor, r.currentElement.Content))
		r.collectingBlockquote = false
		r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
	} else if r.currentElement.Content != "" {
		// Format based on current element type
		switch r.currentElement.Type {
		case HeaderElement:
			result.WriteString(formatHeader(r.headerLevel, r.currentElement.Content))
		case ListItemElement:
			result.WriteString(formatListItem(r.currentElement.Content))
		case BlockquoteElement:
			result.WriteString(markdownColor(QuoteColor, r.currentElement.Content))
		case CodeBlockElement:
			result.WriteString(markdownColor(CodeBlockColor, r.currentElement.Content))
		case InlineCodeElement:
			result.WriteString(markdownColor(InlineCodeColor, r.currentElement.Content))
		case BoldElement:
			result.WriteString(markdownColor(BoldColor, r.currentElement.Content))
		case ItalicElement:
			result.WriteString(markdownColor(ItalicColor, r.currentElement.Content))
		case LinkElement:
			result.WriteString(formatLink(r.linkTextBuffer, r.currentElement.Content))
		case TextElement:
			result.WriteString(r.currentElement.Content)
		}
	}

	// Reset the current element
	r.currentElement = MarkdownElement{Type: TextElement, Content: ""}

	return result.String()
}

func markdownColor(style, content string) string {
	if !markdownOptions.Colors || style == "" || content == "" {
		return content
	}
	return style + content + Reset
}

func formatHeader(level int, content string) string {
	switch level {
	case 1:
		return markdownColor(H1Color, content)
	case 2:
		return markdownColor(H2Color, content)
	case 3:
		return markdownColor(H3Color, content)
	case 4:
		return markdownColor(H4Color, content)
	case 5:
		return markdownColor(H5Color, content)
	case 6:
		return markdownColor(H6Color, content)
	}
	return content
}

func formatListItem(content string) string {
	prefix := "- "
	if markdownOptions.UTF8 {
		prefix = "• "
	}
	return markdownColor(ListItemColor, prefix+content)
}

func formatLink(text, url string) string {
	return markdownColor(LinkColor, text) + " (" + markdownColor(LinkColor, url) + ")"
}

// formatElement formats a single markdown element
func formatElement(element MarkdownElement) string {
	switch element.Type {
	case HeaderElement:
		return formatHeader(element.Level, element.Content)
	case ListItemElement:
		return formatListItem(element.Content)
	case BlockquoteElement:
		return markdownColor(QuoteColor, element.Content)
	case CodeBlockElement:
		return markdownColor(CodeBlockColor, element.Content)
	case InlineCodeElement:
		return markdownColor(InlineCodeColor, element.Content)
	case BoldElement:
		return markdownColor(BoldColor, element.Content)
	case ItalicElement:
		return markdownColor(ItalicColor, element.Content)
	case LinkElement:
		return markdownColor(LinkColor, element.Content)
	}
	return element.Content
}

// isDigit checks if a character is a digit
func isDigit(c rune) bool {
	return c >= '0' && c <= '9'
}

// RenderMarkdown converts markdown text to colored terminal output
func RenderMarkdown(text string) string {
	renderer := NewMarkdownRenderer(false)
	return renderer.Process(text)
}

// Global renderer for streaming mode
var streamingRenderer *MarkdownRenderer

// GetStreamRenderer returns the global streaming renderer, creating it if needed
func GetStreamRenderer() *MarkdownRenderer {
	if streamingRenderer == nil {
		streamingRenderer = NewMarkdownRenderer(true)
	}
	return streamingRenderer
}

// ResetStreamRenderer resets the global streaming renderer
func ResetStreamRenderer() {
	streamingRenderer = NewMarkdownRenderer(true)
}

// FormatResponse applies markdown formatting to the LLM response if enabled
func FormatResponse(text string, markdownEnabled bool) string {
	if markdownEnabled {
		return RenderMarkdown(text)
	}
	return text
}

// PrintFormattedResponse prints the response with proper formatting
func PrintFormattedResponse(text string, markdownEnabled bool) {
	if markdownEnabled {
		fmt.Print(RenderMarkdown(text))
	} else {
		fmt.Print(text)
	}
}

// FormatStreamingChunk formats a chunk of text for streaming output
func FormatStreamingChunk(chunk string, markdownEnabled bool) string {
	if !markdownEnabled {
		// Just handle newlines for terminal display
		return strings.ReplaceAll(chunk, "\n", "\r\n")
	}

	// Use the streaming renderer
	renderer := GetStreamRenderer()
	return renderer.Process(chunk)
}
