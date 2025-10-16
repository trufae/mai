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
            available = true
            current = false
            return
        }
        let keyed = try decoder.container(keyedBy: CodingKeys.self)
        name = try keyed.decode(String.self, forKey: .name)
        available = (try? keyed.decode(Bool.self, forKey: .available)) ?? true
        current = (try? keyed.decode(Bool.self, forKey: .current)) ?? false
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
            description = ""
            current = false
            return
        }
        let keyed = try decoder.container(keyedBy: CodingKeys.self)
        id = try keyed.decode(String.self, forKey: .id)
        description = (try? keyed.decode(String.self, forKey: .description)) ?? ""
        current = (try? keyed.decode(Bool.self, forKey: .current)) ?? false
    }
}

struct Tool: Codable, Identifiable {
    let id = UUID()
    let name: String
    let description: String?
    let arguments: [String: AnyCodable]?
    let inputSchema: [String: AnyCodable]?

    enum CodingKeys: String, CodingKey {
        case name, description, arguments, inputSchema
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        description = try container.decodeIfPresent(String.self, forKey: .description)
        arguments = try container.decodeIfPresent([String: AnyCodable].self, forKey: .arguments)
        inputSchema = try container.decodeIfPresent([String: AnyCodable].self, forKey: .inputSchema)
    }
}

struct ServerInfo: Codable {
    let status: String?
    let version: String?
}

struct Prompt: Codable, Identifiable {
    let id = UUID()
    let name: String
    let description: String?
    let arguments: [String: AnyCodable]?

    enum CodingKeys: String, CodingKey {
        case name, description, arguments
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        description = try container.decodeIfPresent(String.self, forKey: .description)
        arguments = try container.decodeIfPresent([String: AnyCodable].self, forKey: .arguments)
    }
}

struct Resource: Codable, Identifiable {
    let id = UUID()
    let uri: String
    let name: String?
    let description: String?
    let mimeType: String?

    enum CodingKeys: String, CodingKey {
        case uri, name, description, mimeType
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        uri = try container.decode(String.self, forKey: .uri)
        name = try container.decodeIfPresent(String.self, forKey: .name)
        description = try container.decodeIfPresent(String.self, forKey: .description)
        mimeType = try container.decodeIfPresent(String.self, forKey: .mimeType)
    }
}

struct AnyCodable: Codable {
    let value: Any

    init(_ value: Any) {
        self.value = value
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let intVal = try? container.decode(Int.self) {
            value = intVal
        } else if let doubleVal = try? container.decode(Double.self) {
            value = doubleVal
        } else if let boolVal = try? container.decode(Bool.self) {
            value = boolVal
        } else if let stringVal = try? container.decode(String.self) {
            value = stringVal
        } else if let arrayVal = try? container.decode([AnyCodable].self) {
            value = arrayVal
        } else if let dictVal = try? container.decode([String: AnyCodable].self) {
            value = dictVal
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported type")
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case let intVal as Int:
            try container.encode(intVal)
        case let doubleVal as Double:
            try container.encode(doubleVal)
        case let boolVal as Bool:
            try container.encode(boolVal)
        case let stringVal as String:
            try container.encode(stringVal)
        case let arrayVal as [AnyCodable]:
            try container.encode(arrayVal)
        case let dictVal as [String: AnyCodable]:
            try container.encode(dictVal)
        default:
            throw EncodingError.invalidValue(value, EncodingError.Context(codingPath: encoder.codingPath, debugDescription: "Unsupported type"))
        }
    }
}
