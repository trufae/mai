import Foundation

struct ProviderInfo: Codable, Identifiable {
    let id = UUID()
    let name: String
    let available: Bool
    let current: Bool

    enum CodingKeys: String, CodingKey {
        case name, available, current
    }

    init(name: String, available: Bool = true, current: Bool = false) {
        self.name = name
        self.available = available
        self.current = current
    }

    init(from decoder: Decoder) throws {
        // Support both string ("openai") and object formats
        let container = try decoder.singleValueContainer()
        if let name = try? container.decode(String.self) {
            self.name = name
            self.available = true
            self.current = false
            return
        }
        let keyed = try decoder.container(keyedBy: CodingKeys.self)
        self.name = try keyed.decode(String.self, forKey: .name)
        self.available = (try? keyed.decode(Bool.self, forKey: .available)) ?? true
        self.current = (try? keyed.decode(Bool.self, forKey: .current)) ?? false
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

    enum CodingKeys: String, CodingKey {
        case id, description, current
    }

    init(id: String, description: String = "", current: Bool = false) {
        self.id = id
        self.description = description
        self.current = current
    }

    init(from decoder: Decoder) throws {
        // Support both string ("gpt-4o") and object formats
        let container = try decoder.singleValueContainer()
        if let id = try? container.decode(String.self) {
            self.id = id
            self.description = ""
            self.current = false
            return
        }
        let keyed = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try keyed.decode(String.self, forKey: .id)
        self.description = (try? keyed.decode(String.self, forKey: .description)) ?? ""
        self.current = (try? keyed.decode(Bool.self, forKey: .current)) ?? false
    }
}
