import SwiftUI

struct MessageBubble: View {
    let message: ChatMessage

    @Environment(\.colorScheme) var colorScheme

    private var processedText: String {
        // Replace * at start of line with • for better bullets
        let regex = try? NSRegularExpression(pattern: "^\\* ", options: [.anchorsMatchLines])
        let range = NSRange(location: 0, length: message.text.utf16.count)
        return regex?.stringByReplacingMatches(in: message.text, options: [], range: range, withTemplate: "• ") ?? message.text
    }

    var body: some View {
        HStack(alignment: .bottom, spacing: 8) {
            if message.isUser {
                Spacer(minLength: 60)
            }

            VStack(alignment: message.isUser ? .trailing : .leading, spacing: 4) {
                MarkdownText(text: processedText)
                    .textSelection(.enabled)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                    .background(Color.clear)
                    .clipShape(ChatBubbleShape(isUser: message.isUser))
                    .overlay(message.isUser ? AnyView(RoundedRectangle(cornerRadius: 18)
                        .stroke(Color.gray.opacity(0.2), lineWidth: 0.5)) : AnyView(Color.clear))
                    .shadow(color: message.isUser ? Color.black.opacity(0.15) : Color.clear, radius: message.isUser ? 3 : 0, x: 0, y: message.isUser ? 2 : 0)

                Text(message.timestamp, style: .time)
                    .font(.caption2)
                    .foregroundColor(.secondary)
                    .padding(.horizontal, 4)
            }

            if !message.isUser {
                Spacer(minLength: 60)
            }
        }
        .padding(.horizontal)
        .transition(.asymmetric(insertion: .scale(scale: 0.8).combined(with: .opacity),
                                removal: .scale(scale: 0.8).combined(with: .opacity)))
    }

    private var bubbleColor: Color {
        let color: Color
        if message.isUser {
            color = Color.gray.opacity(0.2)
        } else {
            color = Color.gray.opacity(0.2)
        }
        return color
    }

    private var textColor: Color {
        let color: Color
            color = .primary
        return color
    }
}