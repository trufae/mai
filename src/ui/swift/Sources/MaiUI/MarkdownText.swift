import SwiftUI

struct MarkdownText: View {
    let text: String
    @Environment(\.colorScheme) var colorScheme

    private var parsedSegments: [TextSegment] {
        parseMarkdown(text)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(parsedSegments.indices, id: \.self) { index in
                let segment = parsedSegments[index]
                renderSegment(segment)
            }
        }
    }

    @ViewBuilder
    private func renderInlineSegment(_ segment: TextSegment) -> some View {
        switch segment.type {
        case .text:
            Text(segment.content)
                .font(.system(.body, design: .monospaced))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .bold:
            Text(segment.content)
                .font(.system(.body, design: .monospaced, weight: .bold))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .italic:
            Text(segment.content)
                .font(.system(.body, design: .monospaced).italic())
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .boldItalic:
            Text(segment.content)
                .font(.system(.body, design: .monospaced, weight: .bold).italic())
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .inlineCode:
            Text(segment.content)
                .font(.system(.body, design: .monospaced))
                .foregroundColor(.primary)
                .padding(.horizontal, 4)
                .padding(.vertical, 2)
                .background(Color.gray.opacity(0.3))
                .cornerRadius(4)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .link:
            if let url = segment.url {
                Link(segment.content, destination: URL(string: url) ?? URL(string: "about:blank")!)
                    .foregroundColor(.blue)
                    .font(.system(.body, design: .monospaced))
            } else {
                Text(segment.content)
                    .foregroundColor(.blue)
                    .underline()
                    .font(.system(.body, design: .monospaced))
            }

        default:
            Text(segment.content)
                .font(.system(.body, design: .monospaced))
                .foregroundColor(.primary)
        }
    }

    @ViewBuilder
    private func renderSegment(_ segment: TextSegment) -> some View {
        switch segment.type {
        case .text:
            Text(segment.content)
                .font(.system(.body))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .bold:
            Text(segment.content)
                .font(.system(.body, weight: .bold))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .italic:
            Text(segment.content)
                .font(.system(.body).italic())
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .boldItalic:
            Text(segment.content)
                .font(.system(.body, weight: .bold).italic())
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .inlineCode:
            Text(segment.content)
                .font(.system(.body, design: .monospaced))
                .foregroundColor(.primary)
                .padding(.horizontal, 4)
                .padding(.vertical, 2)
                .background(Color.gray.opacity(0.2))
                .cornerRadius(4)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

        case .header1:
            Text(segment.content)
                .font(.system(.title, weight: .bold))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.bottom, 4)

        case .header2:
            Text(segment.content)
                .font(.system(.title2, weight: .bold))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.bottom, 4)

        case .header3:
            Text(segment.content)
                .font(.system(.title3, weight: .bold))
                .foregroundColor(.primary)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.bottom, 4)

        case .listItem:
            HStack(alignment: .top, spacing: 8) {
                Text("•")
                    .foregroundColor(.secondary)
                Text(segment.content)
                    .font(.system(.body))
                    .foregroundColor(.primary)
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
            }

        case .numberedListItem:
            HStack(alignment: .top, spacing: 8) {
                if let number = segment.number {
                    Text("\(number).")
                        .foregroundColor(.secondary)
                }
                Text(segment.content)
                    .font(.system(.body))
                    .foregroundColor(.primary)
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
            }

        case .codeBlock:
            VStack(alignment: .leading, spacing: 0) {
                // Language label if present
                if let language = segment.language, !language.isEmpty {
                    Text(language)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.gray.opacity(0.1))
                }

                // Code content - parse for inline formatting
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(alignment: .top, spacing: 0) {
                        ForEach(parseInlineFormattedText(segment.content).indices, id: \.self) { index in
                            let inlineSegment = parseInlineFormattedText(segment.content)[index]
                            renderInlineSegment(inlineSegment)
                        }
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                }
            }
            .background(Color.gray.opacity(colorScheme == .dark ? 0.3 : 0.1))
            .cornerRadius(8)
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(Color.gray.opacity(0.3), lineWidth: 1)
            )

        case .link:
            if let url = segment.url {
                Link(segment.content, destination: URL(string: url) ?? URL(string: "about:blank")!)
                    .foregroundColor(.blue)
            } else {
                Text(segment.content)
                    .foregroundColor(.blue)
                    .underline()
            }
        }
    }

    private func parseMarkdown(_ input: String) -> [TextSegment] {
        let lines = input.split(separator: "\n", omittingEmptySubsequences: false)
        var segments: [TextSegment] = []
        var i = 0
        var currentListNumber = 0

        while i < lines.count {
            let line = String(lines[i])
            let trimmedLine = line.trimmingCharacters(in: .whitespaces)

            if trimmedLine.isEmpty {
                // Empty line - add spacing
                segments.append(TextSegment(type: .text, content: ""))
                i += 1
                continue
            }

            // Check for code blocks
            if trimmedLine.hasPrefix("```") {
                if let (codeBlockSegment, linesConsumed) = parseCodeBlock(from: lines, startingAt: i) {
                    segments.append(codeBlockSegment)
                    i += linesConsumed
                    continue
                }
            }

            // Check for headers
            if let headerSegment = parseHeader(line) {
                segments.append(headerSegment)
                i += 1
                continue
            }

            // Check for list items
            if let listSegment = parseListItem(line, currentListNumber: &currentListNumber) {
                segments.append(listSegment)
                i += 1
                continue
            }

            // Regular line with inline formatting
            segments.append(contentsOf: parseInlineFormattedText(line))
            i += 1
        }

        return segments
    }

    private func parseHeader(_ line: String) -> TextSegment? {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.hasPrefix("### ") {
            return TextSegment(type: .header3, content: String(trimmed.dropFirst(4)))
        } else if trimmed.hasPrefix("## ") {
            return TextSegment(type: .header2, content: String(trimmed.dropFirst(3)))
        } else if trimmed.hasPrefix("# ") {
            return TextSegment(type: .header1, content: String(trimmed.dropFirst(2)))
        }
        return nil
    }

    private func parseListItem(_ line: String, currentListNumber: inout Int) -> TextSegment? {
        let trimmed = line.trimmingCharacters(in: .whitespaces)

        // Unordered list (- or *)
        if trimmed.hasPrefix("- ") {
            return TextSegment(type: .listItem, content: String(trimmed.dropFirst(2)))
        } else if trimmed.hasPrefix("* "), !trimmed.hasPrefix("**") {
            return TextSegment(type: .listItem, content: String(trimmed.dropFirst(2)))
        }

        // Numbered list
        if let range = trimmed.range(of: "^\\d+\\. ", options: .regularExpression) {
            let numberString = String(trimmed[range.lowerBound ..< range.upperBound].dropLast(2))
            if let number = Int(numberString) {
                currentListNumber = number
                let content = String(trimmed[range.upperBound...])
                return TextSegment(type: .numberedListItem, content: content, number: number)
            }
        }

        return nil
    }

    private func parseCodeBlock(from lines: [Substring], startingAt index: Int) -> (TextSegment, Int)? {
        guard index < lines.count else { return nil }
        let startLine = String(lines[index])

        // Extract language if present
        var language: String? = nil
        var languageLine = startLine.trimmingCharacters(in: .whitespaces)
        if languageLine.hasPrefix("```") {
            languageLine = String(languageLine.dropFirst(3))
            if !languageLine.isEmpty {
                language = languageLine
            }
        }

        // Find the closing ```
        var codeLines: [String] = []
        var endIndex = index + 1

        for i in (index + 1) ..< lines.count {
            let currentLine = String(lines[i])
            if currentLine.trimmingCharacters(in: .whitespaces) == "```" {
                endIndex = i + 1 // Include the closing line
                break
            }
            codeLines.append(currentLine)
        }

        if endIndex > index + 1 {
            let codeContent = codeLines.joined(separator: "\n")
            let linesConsumed = endIndex - index
            return (
                TextSegment(type: .codeBlock, content: codeContent, language: language), linesConsumed
            )
        }

        return nil
    }

    private func parseInlineFormattedText(_ text: String) -> [TextSegment] {
        var segments: [TextSegment] = []
        var remaining = text

        while !remaining.isEmpty {
            var foundMatch = false

            // Check for links first: [text](url)
            if let linkStart = remaining.range(of: "[") {
                let linkStartIndex = remaining.distance(
                    from: remaining.startIndex, to: linkStart.lowerBound
                )
                let afterBracket = String(remaining[linkStart.upperBound...])

                if let bracketEnd = afterBracket.range(of: "]("),
                   let parenEnd = afterBracket[bracketEnd.upperBound...].range(of: ")")
                {
                    // Found a complete link
                    if linkStartIndex > 0 {
                        let beforeText = String(remaining.prefix(linkStartIndex))
                        segments.append(TextSegment(type: .text, content: beforeText))
                    }

                    let linkText = String(afterBracket[..<bracketEnd.lowerBound])
                    let urlStart = afterBracket.index(bracketEnd.lowerBound, offsetBy: 1)
                    let url = String(afterBracket[urlStart ..< parenEnd.lowerBound])

                    segments.append(TextSegment(type: .link, content: linkText, url: url))
                    remaining = String(afterBracket[parenEnd.upperBound...])
                    foundMatch = true
                    continue
                }
            }

            // Check for bold italic: ***text*** or ___text___
            if let boldItalicStart = remaining.range(of: "***") ?? remaining.range(of: "___") {
                let marker = String(remaining[boldItalicStart])
                let startIndex = remaining.distance(
                    from: remaining.startIndex, to: boldItalicStart.lowerBound
                )
                let afterMarker = String(remaining[boldItalicStart.upperBound...])

                if let endRange = afterMarker.range(of: marker) {
                    if startIndex > 0 {
                        let beforeText = String(remaining.prefix(startIndex))
                        segments.append(TextSegment(type: .text, content: beforeText))
                    }

                    let content = String(afterMarker[..<endRange.lowerBound])
                    segments.append(TextSegment(type: .boldItalic, content: content))
                    remaining = String(afterMarker[endRange.upperBound...])
                    foundMatch = true
                    continue
                }
            }

            // Check for bold: **text** or __text__
            if let boldStart = remaining.range(of: "**") ?? remaining.range(of: "__") {
                let marker = String(remaining[boldStart])
                let startIndex = remaining.distance(from: remaining.startIndex, to: boldStart.lowerBound)
                let afterMarker = String(remaining[boldStart.upperBound...])

                if let endRange = afterMarker.range(of: marker) {
                    if startIndex > 0 {
                        let beforeText = String(remaining.prefix(startIndex))
                        segments.append(TextSegment(type: .text, content: beforeText))
                    }

                    let content = String(afterMarker[..<endRange.lowerBound])
                    segments.append(TextSegment(type: .bold, content: content))
                    remaining = String(afterMarker[endRange.upperBound...])
                    foundMatch = true
                    continue
                }
            }

            // Check for italic: *text* or _text_
            if let italicStart = remaining.range(of: "*") ?? remaining.range(of: "_") {
                let marker = String(remaining[italicStart])
                let startIndex = remaining.distance(from: remaining.startIndex, to: italicStart.lowerBound)
                let afterMarker = String(remaining[italicStart.upperBound...])

                if let endRange = afterMarker.range(of: marker) {
                    if startIndex > 0 {
                        let beforeText = String(remaining.prefix(startIndex))
                        segments.append(TextSegment(type: .text, content: beforeText))
                    }

                    let content = String(afterMarker[..<endRange.lowerBound])
                    segments.append(TextSegment(type: .italic, content: content))
                    remaining = String(afterMarker[endRange.upperBound...])
                    foundMatch = true
                    continue
                }
            }

            // Check for inline code: `code` (last, so it doesn't interfere with other formatting)
            if let codeStart = remaining.range(of: "`") {
                let codeStartIndex = remaining.distance(
                    from: remaining.startIndex, to: codeStart.lowerBound
                )
                let afterBacktick = String(remaining[codeStart.upperBound...])

                if let codeEnd = afterBacktick.range(of: "`") {
                    // Found inline code
                    if codeStartIndex > 0 {
                        let beforeText = String(remaining.prefix(codeStartIndex))
                        segments.append(TextSegment(type: .text, content: beforeText))
                    }

                    let codeContent = String(afterBacktick[..<codeEnd.lowerBound])
                    segments.append(TextSegment(type: .inlineCode, content: codeContent))
                    remaining = String(afterBacktick[codeEnd.upperBound...])
                    foundMatch = true
                    continue
                }
            }

            if !foundMatch {
                // No formatting found, add the rest as text
                segments.append(TextSegment(type: .text, content: remaining))
                break
            }
        }

        return segments
    }
}

struct TextSegment: Identifiable {
    let id = UUID()
    let type: SegmentType
    let content: String
    let language: String?
    let url: String?
    let number: Int?

    init(
        type: SegmentType, content: String, language: String? = nil, url: String? = nil,
        number: Int? = nil
    ) {
        self.type = type
        self.content = content
        self.language = language
        self.url = url
        self.number = number
    }
}

enum SegmentType {
    case text
    case bold
    case italic
    case boldItalic
    case inlineCode
    case header1
    case header2
    case header3
    case listItem
    case numberedListItem
    case codeBlock
    case link
}
