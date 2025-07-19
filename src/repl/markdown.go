package main

import (
	"fmt"
	"strings"
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
	boldMarker        byte // '*' or '_' for bold
	italicMarker      byte // '*' or '_' for italic
	nestingLevel      int  // For handling nested formatting

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

	// Process character by character
	for i := 0; i < len(chunk); i++ {
		c := chunk[i]

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
				for j < len(chunk) && chunk[j] == '#' && headerCount < 6 {
					headerCount++
					j++
				}

				// Validate that it's a proper header (needs space after #)
				if j < len(chunk) && chunk[j] == ' ' {
					r.collectingHeader = true
					r.headerLevel = headerCount
					r.currentElement = MarkdownElement{Type: HeaderElement, Level: headerCount, Content: ""}
					i = j // Skip past the # and space
					r.isInLineStart = false
					continue
				}
			}

			// Handle list items (- item or * item)
			if (c == '-' || c == '*') && i+1 < len(chunk) && chunk[i+1] == ' ' {
				r.collectingListItem = true
				r.currentElement = MarkdownElement{Type: ListItemElement, Content: ""}
				i += 1 // Skip the marker and space
				r.isInLineStart = false
				continue
			}

			// Handle ordered list items (1. item)
			if isDigit(c) {
				j := i
				for j < len(chunk) && isDigit(chunk[j]) {
					j++
				}
				if j < len(chunk) && chunk[j] == '.' && j+1 < len(chunk) && chunk[j+1] == ' ' {
					r.collectingListItem = true
					r.currentElement = MarkdownElement{Type: ListItemElement, Content: ""}
					i = j + 1 // Skip past the number, dot, and space
					r.isInLineStart = false
					continue
				}
			}

			// Handle blockquotes (> quote)
			if c == '>' && i+1 < len(chunk) && chunk[i+1] == ' ' {
				r.collectingBlockquote = true
				r.currentElement = MarkdownElement{Type: BlockquoteElement, Content: ""}
				i += 1 // Skip the > and space
				r.isInLineStart = false
				continue
			}

			// Handle code fences (```)
			if c == '`' && i+2 < len(chunk) && chunk[i+1] == '`' && chunk[i+2] == '`' {
				r.inCodeFence = !r.inCodeFence
				if r.inCodeFence {
					// Look for optional language identifier
					langStart := i + 3
					for langStart < len(chunk) && chunk[langStart] == ' ' {
						langStart++
					}
					langEnd := langStart
					for langEnd < len(chunk) && chunk[langEnd] != '\n' {
						langEnd++
					}
					if langEnd > langStart {
						r.codeLanguage = chunk[langStart:langEnd]
					}
					r.currentElement = MarkdownElement{Type: CodeBlockElement, Content: ""}
				} else {
					// End of code block, add the element
					result.WriteString(formatElement(r.currentElement))
					r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
					r.codeLanguage = ""
				}
				i += 2 // Skip past the ```
				if i+1 < len(chunk) && chunk[i+1] == '\n' {
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
			if c == '`' && i+2 < len(chunk) && chunk[i+1] == '`' && chunk[i+2] == '`' {
				r.inCodeFence = false
				result.WriteString(formatElement(r.currentElement))
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
				i += 2 // Skip the remaining `` characters
				if i+1 < len(chunk) && chunk[i+1] == '\n' {
					i++ // Skip the newline after code fence
				}
				r.isInLineStart = true
				continue
			}

			// Add character to code block content
			r.currentElement.Content += string(c)
			if c == '\n' {
				r.isInLineStart = true
			}
			continue
		}

		// Handle inline code (`code`)
		if c == '`' && !r.inCodeFence {
			if r.collectingInlineCode {
				// End of inline code, add the element
				r.collectingInlineCode = false
				result.WriteString(InlineCodeColor + r.currentElement.Content + Reset)
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
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

		// Handle bold formatting (** or __)
		if (c == '*' || c == '_') && i+1 < len(chunk) && chunk[i+1] == c {
			if r.collectingBold && c == r.boldMarker {
				// End of bold, add the element
				r.collectingBold = false
				result.WriteString(BoldColor + r.currentElement.Content + Reset)
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
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

		// Handle italic formatting (* or _)
		if (c == '*' || c == '_') && !r.collectingBold {
			if r.collectingItalic && c == r.italicMarker {
				// End of italic, add the element
				r.collectingItalic = false
				result.WriteString(ItalicColor + r.currentElement.Content + Reset)
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if !r.collectingItalic {
				// Start of italic
				if r.currentElement.Content != "" {
					// Flush any existing content
					result.WriteString(r.currentElement.Content)
				}
				r.collectingItalic = true
				r.italicMarker = c
				r.currentElement = MarkdownElement{Type: ItalicElement, Content: ""}
			} else {
				// Just add the character if we're in another formatting context
				r.currentElement.Content += string(c)
			}
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

		if c == ']' && r.collectingLinkText && i+1 < len(chunk) && chunk[i+1] == '(' {
			r.collectingLinkText = false
			r.collectingLinkURL = true
			i++ // Skip the opening (
			continue
		}

		if c == ')' && r.collectingLinkURL {
			r.collectingLinkURL = false
			// Format and add the link
			result.WriteString(LinkColor + r.linkTextBuffer + Reset + " (" + LinkColor + r.currentElement.Content + Reset + ")")
			r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			continue
		}

		// Handle newlines
		if c == '\n' {
			// Finish any current element
			if r.collectingHeader {
				r.collectingHeader = false
				switch r.headerLevel {
				case 1:
					result.WriteString(H1Color + r.currentElement.Content + Reset)
				case 2:
					result.WriteString(H2Color + r.currentElement.Content + Reset)
				case 3:
					result.WriteString(H3Color + r.currentElement.Content + Reset)
				case 4:
					result.WriteString(H4Color + r.currentElement.Content + Reset)
				case 5:
					result.WriteString(H5Color + r.currentElement.Content + Reset)
				case 6:
					result.WriteString(H6Color + r.currentElement.Content + Reset)
				}
				r.headerLevel = 0
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingListItem {
				r.collectingListItem = false
				result.WriteString(ListItemColor + "• " + r.currentElement.Content + Reset)
				r.currentElement = MarkdownElement{Type: TextElement, Content: ""}
			} else if r.collectingBlockquote {
				r.collectingBlockquote = false
				result.WriteString(QuoteColor + r.currentElement.Content + Reset)
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

	res := result.String()
	return strings.ReplaceAll(res, "\n", "\n\r")
}

// Flush handles any remaining content in the buffer
// This is used in streaming mode to finish processing
func (r *MarkdownRenderer) Flush() string {
	if r.currentElement.Content == "" {
		return ""
	}

	var result strings.Builder

	// Format based on current element type
	switch r.currentElement.Type {
	case HeaderElement:
		switch r.headerLevel {
		case 1:
			result.WriteString(H1Color + r.currentElement.Content + Reset)
		case 2:
			result.WriteString(H2Color + r.currentElement.Content + Reset)
		case 3:
			result.WriteString(H3Color + r.currentElement.Content + Reset)
		case 4:
			result.WriteString(H4Color + r.currentElement.Content + Reset)
		case 5:
			result.WriteString(H5Color + r.currentElement.Content + Reset)
		case 6:
			result.WriteString(H6Color + r.currentElement.Content + Reset)
		}
	case ListItemElement:
		result.WriteString(ListItemColor + "• " + r.currentElement.Content + Reset)
	case BlockquoteElement:
		result.WriteString(QuoteColor + r.currentElement.Content + Reset)
	case CodeBlockElement:
		result.WriteString(CodeBlockColor + r.currentElement.Content + Reset)
	case InlineCodeElement:
		result.WriteString(InlineCodeColor + r.currentElement.Content + Reset)
	case BoldElement:
		result.WriteString(BoldColor + r.currentElement.Content + Reset)
	case ItalicElement:
		result.WriteString(ItalicColor + r.currentElement.Content + Reset)
	case LinkElement:
		result.WriteString(LinkColor + r.linkTextBuffer + Reset + " (" + LinkColor + r.currentElement.Content + Reset + ")")
	case TextElement:
		result.WriteString(r.currentElement.Content)
	}

	// Reset the current element
	r.currentElement = MarkdownElement{Type: TextElement, Content: ""}

	return result.String()
}

// formatElement formats a single markdown element
func formatElement(element MarkdownElement) string {
	switch element.Type {
	case HeaderElement:
		switch element.Level {
		case 1:
			return H1Color + element.Content + Reset
		case 2:
			return H2Color + element.Content + Reset
		case 3:
			return H3Color + element.Content + Reset
		case 4:
			return H4Color + element.Content + Reset
		case 5:
			return H5Color + element.Content + Reset
		case 6:
			return H6Color + element.Content + Reset
		}
	case ListItemElement:
		return ListItemColor + "• " + element.Content + Reset
	case BlockquoteElement:
		return QuoteColor + element.Content + Reset
	case CodeBlockElement:
		return CodeBlockColor + element.Content + Reset
	case InlineCodeElement:
		return InlineCodeColor + element.Content + Reset
	case BoldElement:
		return BoldColor + element.Content + Reset
	case ItalicElement:
		return ItalicColor + element.Content + Reset
	case LinkElement:
		return LinkColor + element.Content + Reset
	}
	return element.Content
}

// isDigit checks if a character is a digit
func isDigit(c byte) bool {
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
