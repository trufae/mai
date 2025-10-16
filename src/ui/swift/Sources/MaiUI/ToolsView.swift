import SwiftUI

struct ToolsView: View {
    @State private var baseURL: String = ""
    @State private var servers: [String: [String: String]] = [:]
    @State private var tools: [String: [Tool]] = [:]
    @State private var prompts: [String: [Prompt]] = [:]
    @State private var resources: [String: [Resource]] = [:]
    @State private var isLoading = false
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 16) {
            // Base URL input
            HStack {
                Text("Base URL:")
                TextField("http://localhost:8989", text: $baseURL)
                    .textFieldStyle(.roundedBorder)
                Button("Load") {
                    loadData()
                }
                .disabled(isLoading)
            }
            .padding(.horizontal)

            if isLoading {
                ProgressView("Loading...")
            } else if let error = errorMessage {
                Text("Error: \(error)")
                    .foregroundColor(.red)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        // Servers
                        if !servers.isEmpty {
                            SectionView(title: "Servers", items: servers.keys.sorted()) { server in
                                VStack(alignment: .leading) {
                                    Text(server).font(.headline)
                                    if let info = servers[server] {
                                        ForEach(info.sorted(by: { $0.key < $1.key }), id: \.key) { key, value in
                                            Text("\(key): \(value)").font(.caption)
                                        }
                                    }
                                }
                            }
                        }

                         // Tools
                         if !tools.isEmpty {
                             SectionView(title: "Tools", items: tools.keys.filter { !(tools[$0] ?? []).isEmpty }.sorted()) { server in
                                 VStack(alignment: .leading) {
                                     Text(server).font(.headline)
                                     ForEach(tools[server] ?? []) { tool in
                                         VStack(alignment: .leading) {
                                             Text(tool.name).font(.subheadline)
                                             if let desc = tool.description, !desc.isEmpty {
                                                 Text(desc).font(.caption)
                                             }
                                         }
                                         .padding(.leading)
                                     }
                                 }
                             }
                         }

                         // Prompts
                         if !prompts.isEmpty {
                             SectionView(title: "Prompts", items: prompts.keys.filter { !(prompts[$0] ?? []).isEmpty }.sorted()) { server in
                                 VStack(alignment: .leading) {
                                     Text(server).font(.headline)
                                     ForEach(prompts[server] ?? []) { prompt in
                                         VStack(alignment: .leading) {
                                             Text(prompt.name).font(.subheadline)
                                             if let desc = prompt.description, !desc.isEmpty {
                                                 Text(desc).font(.caption)
                                             }
                                         }
                                         .padding(.leading)
                                     }
                                 }
                             }
                         }

                        // Resources
                        if !resources.isEmpty {
                            SectionView(title: "Resources", items: resources.keys.sorted()) { server in
                                VStack(alignment: .leading) {
                                    Text(server).font(.headline)
                                    ForEach(resources[server] ?? []) { resource in
                                        VStack(alignment: .leading) {
                                            Text(resource.uri).font(.subheadline)
                                            if let name = resource.name, !name.isEmpty {
                                                Text(name).font(.caption)
                                            }
                                            if let desc = resource.description, !desc.isEmpty {
                                                Text(desc).font(.caption)
                                            }
                                        }
                                        .padding(.leading)
                                    }
                                }
                            }
                        }
                    }
                    .padding()
                }
            }
        }
        .onAppear {
            // Load default base URL
            if baseURL.isEmpty {
                baseURL = ProcessInfo.processInfo.environment["MAI_TOOL_BASEURL"] ?? "http://localhost:8989"
            }
            loadData()
        }
    }

    private func loadData() {
        isLoading = true
        errorMessage = nil

        Task {
            do {
                // Load servers
                let serversData = try await runMaiTool(command: "servers")
                servers = try JSONDecoder().decode([String: [String: String]].self, from: serversData)

                // Load tools
                let toolsData = try await runMaiTool(command: "list")
                tools = try JSONDecoder().decode([String: [Tool]].self, from: toolsData)

                // Load prompts
                let promptsData = try await runMaiTool(command: "prompts")
                prompts = try JSONDecoder().decode([String: [Prompt]].self, from: promptsData)

                // Load resources
                let resourcesData = try await runMaiTool(command: "resources")
                resources = try JSONDecoder().decode([String: [Resource]].self, from: resourcesData)

            } catch {
                errorMessage = error.localizedDescription
            }
            isLoading = false
        }
    }

    private func runMaiTool(command: String) async throws -> Data {
        let process = Process()
        // Try to find mai-tool in PATH or local build
        let possiblePaths = ["/usr/local/bin/mai-tool", "/usr/bin/mai-tool", "./mai-tool", "../tool/mai-tool"]
        var executablePath: String?
        for path in possiblePaths {
            if FileManager.default.fileExists(atPath: path) {
                executablePath = path
                break
            }
        }
        if executablePath == nil {
            // Try which command
            let whichProcess = Process()
            whichProcess.executableURL = URL(fileURLWithPath: "/usr/bin/which")
            whichProcess.arguments = ["mai-tool"]
            let whichPipe = Pipe()
            whichProcess.standardOutput = whichPipe
            try? whichProcess.run()
            whichProcess.waitUntilExit()
            if whichProcess.terminationStatus == 0 {
                let whichData = whichPipe.fileHandleForReading.readDataToEndOfFile()
                executablePath = String(data: whichData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
            }
        }
        guard let path = executablePath else {
            throw NSError(domain: "MaiTool", code: 1, userInfo: [NSLocalizedDescriptionKey: "mai-tool executable not found"])
        }
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = ["-b", baseURL, "-j", command]

        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = Pipe()

        try process.run()
        process.waitUntilExit()

        if process.terminationStatus != 0 {
            throw NSError(domain: "MaiTool", code: Int(process.terminationStatus), userInfo: [NSLocalizedDescriptionKey: "mai-tool command failed"])
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        return data
    }
}

struct SectionView<Content: View>: View {
    let title: String
    let items: [String]
    let content: (String) -> Content

    var body: some View {
        VStack(alignment: .leading) {
            Text(title).font(.title2).bold()
            ForEach(items, id: \.self) { item in
                content(item)
                    .padding(.vertical, 4)
            }
        }
    }
}
