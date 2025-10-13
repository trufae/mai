import SwiftUI

struct TypingIndicator: View {
    @State private var isAnimating = false

    @Environment(\.colorScheme) var colorScheme

    var body: some View {
        HStack(alignment: .bottom, spacing: 8) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 4) {
                    ForEach(0..<3) { index in
                        Circle()
                            .fill(Color.gray.opacity(0.6))
                            .frame(width: 6, height: 6)
                            .scaleEffect(isAnimating ? 1.0 : 0.3)
                            .animation(
                                Animation.easeInOut(duration: 0.8)
                                    .repeatForever()
                                    .delay(0.2 * Double(index)),
                                value: isAnimating
                            )
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
                .background(bubbleColor)
                .clipShape(ChatBubbleShape(isUser: false))
                .shadow(color: Color.black.opacity(0.1), radius: 2, x: 0, y: 1)

                Text("typing...")
                    .font(.caption2)
                    .foregroundColor(.secondary)
                    .padding(.horizontal, 4)
            }

            Spacer(minLength: 60)
        }
        .padding(.horizontal)
        .onAppear {
            isAnimating = true
        }
        .onDisappear {
            isAnimating = false
        }
    }

    private var bubbleColor: Color {
        return colorScheme == .dark ? Color.gray.opacity(0.2) : Color.gray.opacity(0.1)
    }
}