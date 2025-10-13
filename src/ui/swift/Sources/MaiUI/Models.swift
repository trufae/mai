import Foundation

struct ProviderInfo: Codable, Identifiable {
    let id = UUID()
    let name: String
    let available: Bool
    let current: Bool

    enum CodingKeys: String, CodingKey {
        case name, available, current
    }
}

struct ModelInfo: Codable, Identifiable {
    let id: String
    let description: String
    let current: Bool

    var displayName: String {
        if description.isEmpty {
            return id
        }
        return "\(id) - \(description)"
    }
}