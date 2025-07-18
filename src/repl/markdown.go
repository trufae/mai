package main

import (
	"fmt"
	"regexp"
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
	BoldColor   = Bold
	ItalicColor = Italic

	// Links and quotes
	LinkColor  = BrightCyan + Underline
	QuoteColor = BrightBlack + Italic
)

// RenderMarkdown converts markdown text to colored terminal output
func RenderMarkdown(text string) string {
	// Replace Windows-style line endings with Unix-style
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// Process code blocks first to avoid formatting inside them
	text = processCodeBlocks(text)

	// Process headers
	text = processHeaders(text)

	// Process lists
	text = processLists(text)

	// Process inline formatting
	text = processInlineFormatting(text)

	// Process blockquotes
	text = processBlockquotes(text)

	// Process links
	text = processLinks(text)

	// Replace newlines with carriage return + newline for terminal display
	text = strings.ReplaceAll(text, "\n", "\r\n")

	return text
}

// processCodeBlocks handles both fenced code blocks and indented code blocks
func processCodeBlocks(text string) string {
	// Process fenced code blocks (```code```)
	// This regex handles code blocks with optional language specification
	fencedCodeBlockRegex := regexp.MustCompile("(?ms)```(?:\\w+)?\n(.*?)\n```")
	text = fencedCodeBlockRegex.ReplaceAllStringFunc(text, func(match string) string {
		// Extract the code content
		submatches := fencedCodeBlockRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match // Return original if no match
		}
		content := submatches[1]
		// Format with code block color
		return "\n" + CodeBlockColor + content + Reset + "\n"
	})

	// Process inline code (`code`)
	inlineCodeRegex := regexp.MustCompile("`([^`\n]+?)`")
	text = inlineCodeRegex.ReplaceAllStringFunc(text, func(match string) string {
		// Extract the code content
		submatches := inlineCodeRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match // Return original if no match
		}
		content := submatches[1]
		// Format with inline code color
		return InlineCodeColor + content + Reset
	})

	return text
}

// processHeaders handles markdown headers (# Header)
func processHeaders(text string) string {
	// H1 headers
	h1Regex := regexp.MustCompile("(?m)^# (.+)$")
	text = h1Regex.ReplaceAllString(text, H1Color+"$1"+Reset)

	// H2 headers
	h2Regex := regexp.MustCompile("(?m)^## (.+)$")
	text = h2Regex.ReplaceAllString(text, H2Color+"$1"+Reset)

	// H3 headers
	h3Regex := regexp.MustCompile("(?m)^### (.+)$")
	text = h3Regex.ReplaceAllString(text, H3Color+"$1"+Reset)

	// H4 headers
	h4Regex := regexp.MustCompile("(?m)^#### (.+)$")
	text = h4Regex.ReplaceAllString(text, H4Color+"$1"+Reset)

	// H5 headers
	h5Regex := regexp.MustCompile("(?m)^##### (.+)$")
	text = h5Regex.ReplaceAllString(text, H5Color+"$1"+Reset)

	// H6 headers
	h6Regex := regexp.MustCompile("(?m)^###### (.+)$")
	text = h6Regex.ReplaceAllString(text, H6Color+"$1"+Reset)

	return text
}

// processLists handles markdown lists (- item, * item, 1. item)
func processLists(text string) string {
	// Unordered lists with - or *
	ulRegex := regexp.MustCompile("(?m)^(\\s*)[-*] (.+)$")
	text = ulRegex.ReplaceAllString(text, "$1"+ListItemColor+"• $2"+Reset)

	// Ordered lists (1. item)
	olRegex := regexp.MustCompile("(?m)^(\\s*)\\d+\\. (.+)$")
	text = olRegex.ReplaceAllString(text, "$1"+ListItemColor+"• $2"+Reset)

	return text
}

// processInlineFormatting handles bold, italic, and other inline formatting
func processInlineFormatting(text string) string {
	// Bold with ** or __
	// Use a non-greedy match to handle multiple bold sections in the same line
	boldRegex := regexp.MustCompile("(\\*\\*|__)([^*_]+?)(\\*\\*|__)")
	text = boldRegex.ReplaceAllString(text, BoldColor+"$2"+Reset)

	// Italic with * or _
	// Use a non-greedy match to handle multiple italic sections in the same line
	italicRegex := regexp.MustCompile("(\\*|_)([^*_]+?)(\\*|_)")
	text = italicRegex.ReplaceAllString(text, ItalicColor+"$2"+Reset)

	return text
}

// processBlockquotes handles markdown blockquotes (> quote)
func processBlockquotes(text string) string {
	// Blockquotes
	quoteRegex := regexp.MustCompile("(?m)^> (.+)$")
	text = quoteRegex.ReplaceAllString(text, QuoteColor+"$1"+Reset)

	return text
}

// processLinks handles markdown links [text](url)
func processLinks(text string) string {
	// Links
	linkRegex := regexp.MustCompile("\\[([^\\]]+)\\]\\(([^)]+)\\)")
	text = linkRegex.ReplaceAllString(text, LinkColor+"$1"+Reset+" ("+LinkColor+"$2"+Reset+")")

	return text
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

// StreamingMarkdownProcessor is a stateful processor for streaming markdown
type StreamingMarkdownProcessor struct {
	buffer           string
	inCodeBlock      bool
	inBold           bool
	inItalic         bool
	inHeader         bool
	headerLevel      int
	inListItem       bool
	inBlockquote     bool
	currentLineStart int
}

// NewStreamingMarkdownProcessor creates a new streaming markdown processor
func NewStreamingMarkdownProcessor() *StreamingMarkdownProcessor {
	return &StreamingMarkdownProcessor{
		buffer:           "",
		inCodeBlock:      false,
		inBold:           false,
		inItalic:         false,
		inHeader:         false,
		headerLevel:      0,
		inListItem:       false,
		inBlockquote:     false,
		currentLineStart: 0,
	}
}

// ProcessChunk processes a chunk of text in a streaming-friendly way
func (p *StreamingMarkdownProcessor) ProcessChunk(chunk string) string {
	if chunk == "" {
		return ""
	}

	// Add the chunk to our buffer
	p.buffer += chunk

	// Process the buffer character by character
	var result strings.Builder

	for i := 0; i < len(p.buffer); i++ {
		char := p.buffer[i]

		// Check for line start
		if i == p.currentLineStart {
			// Check for headers at the start of a line
			if char == '#' && i+1 < len(p.buffer) && p.buffer[i+1] == ' ' {
				// Count header level
				headerLevel := 1
				j := i + 1
				for j < len(p.buffer) && p.buffer[j] == '#' {
					headerLevel++
					j++
				}

				// If we have a valid header (# followed by space)
				if j < len(p.buffer) && p.buffer[j] == ' ' {
					p.inHeader = true
					p.headerLevel = headerLevel

					// Add header color based on level
					switch headerLevel {
					case 1:
						result.WriteString(H1Color)
					case 2:
						result.WriteString(H2Color)
					case 3:
						result.WriteString(H3Color)
					case 4:
						result.WriteString(H4Color)
					case 5:
						result.WriteString(H5Color)
					default:
						result.WriteString(H6Color)
					}

					// Skip the # characters and the space
					i = j
					continue
				}
			}

			// Check for list items
			if (char == '-' || char == '*') && i+1 < len(p.buffer) && p.buffer[i+1] == ' ' {
				p.inListItem = true
				result.WriteString(ListItemColor + "• " + Reset)
				i += 1 // Skip the - or * and the space
				continue
			}

			// Check for numbered list items
			if i+2 < len(p.buffer) && isDigit(char) && p.buffer[i+1] == '.' && p.buffer[i+2] == ' ' {
				p.inListItem = true
				result.WriteString(ListItemColor + "• " + Reset)

				// Skip the number, dot, and space
				i += 2
				for i+1 < len(p.buffer) && isDigit(p.buffer[i+1]) {
					i++
				}
				continue
			}

			// Check for blockquotes
			if char == '>' && i+1 < len(p.buffer) && p.buffer[i+1] == ' ' {
				p.inBlockquote = true
				result.WriteString(QuoteColor)
				i += 1 // Skip the > and the space
				continue
			}
		}

		// Check for inline code
		if char == '`' {
			// Check if we're already in a code block
			if p.inCodeBlock {
				p.inCodeBlock = false
				result.WriteString(Reset)
			} else {
				p.inCodeBlock = true
				result.WriteString(InlineCodeColor)
			}
			continue
		}

		// Check for bold (** or __)
		if (char == '*' || char == '_') && i+1 < len(p.buffer) && p.buffer[i+1] == char {
			if p.inBold {
				p.inBold = false
				result.WriteString(Reset)
				i++ // Skip the second * or _
			} else {
				p.inBold = true
				result.WriteString(BoldColor)
				i++ // Skip the second * or _
			}
			continue
		}

		// Check for italic (* or _)
		if (char == '*' || char == '_') && !p.inBold {
			if p.inItalic {
				p.inItalic = false
				result.WriteString(Reset)
			} else {
				p.inItalic = true
				result.WriteString(ItalicColor)
			}
			continue
		}

		// Check for newlines
		if char == '\n' {
			// Reset line-specific states
			if p.inHeader {
				p.inHeader = false
				result.WriteString(Reset)
			}
			if p.inListItem {
				p.inListItem = false
				result.WriteString(Reset)
			}
			if p.inBlockquote {
				p.inBlockquote = false
				result.WriteString(Reset)
			}

			// Update line start position for the next line
			p.currentLineStart = i + 1

			// Add carriage return for terminal display
			result.WriteString("\r\n")
			continue
		}

		// Add the character to the result
		result.WriteByte(char)
	}

	// Clear the buffer
	p.buffer = ""

	return result.String()
}

// isDigit checks if a character is a digit
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// Static processor instance to maintain state between chunks
var staticProcessor *StreamingMarkdownProcessor

// getStaticProcessor returns the static processor instance, creating it if needed
func getStaticProcessor() *StreamingMarkdownProcessor {
	if staticProcessor == nil {
		staticProcessor = NewStreamingMarkdownProcessor()
	}
	return staticProcessor
}

// ResetStaticProcessor resets the static processor state
func ResetStaticProcessor() {
	staticProcessor = NewStreamingMarkdownProcessor()
}

// FormatStreamingChunk formats a chunk of text for streaming output
func FormatStreamingChunk(chunk string, markdownEnabled bool) string {
	if !markdownEnabled {
		// Just handle newlines for terminal display
		return strings.ReplaceAll(chunk, "\n", "\r\n")
	}

	// Use a static processor to maintain state between chunks
	static := getStaticProcessor()
	return static.ProcessChunk(chunk)
}
