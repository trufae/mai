import SwiftUI

struct ChatBubbleShape: Shape {
    let isUser: Bool

    func path(in rect: CGRect) -> Path {
        let cornerRadius: CGFloat = 18
        let tailSize: CGFloat = 0

        var path = Path()

        if isUser {
            // Right-aligned bubble with tail on the right
            path.addRoundedRect(in: CGRect(x: rect.minX, y: rect.minY,
                                          width: rect.width - tailSize, height: rect.height),
                               cornerSize: CGSize(width: cornerRadius, height: cornerRadius))
        } else {
            // Left-aligned bubble with tail on the left
            path.addRoundedRect(in: CGRect(x: rect.minX + tailSize, y: rect.minY,
                                          width: rect.width - tailSize, height: rect.height),
                               cornerSize: CGSize(width: cornerRadius, height: cornerRadius))
        }

        return path
    }
}