import SwiftUI

struct SettingsView: View {
    @AppStorage("aiProvider") private var aiProvider = "openai"
    @AppStorage("aiModel") private var aiModel = "gpt-4"
    @AppStorage("theme") private var theme = "system"
    @AppStorage("aiBaseURL") private var aiBaseURL = ""

    @State private var models: [ModelInfo] = []

    let mcpClient: MCPClient?
    @Binding var providers: [ProviderInfo]

    var body: some View {
        Form {
            Section("AI Settings") {
                Picker("Provider", selection: $aiProvider) {
                    ForEach(providers) { provider in
                        Text(provider.name.capitalized)
                            .tag(provider.name)
                    }
                }
                .onChange(of: aiProvider) { newProvider in
                    print("SettingsView: Provider changed to \(newProvider)")
                    Task {
                        await setProviderAndLoadModels(newProvider)
                    }
                }

                Picker("Model", selection: $aiModel) {
                    ForEach(models) { model in
                        Text(model.displayName)
                            .tag(model.id)
                    }
                }
                .onChange(of: aiModel) { newModel in
                    print("SettingsView: Model changed to \\(newModel)")
                    Task {
                        await setModel(newModel)
                    }
                }

                HStack {
                    Text("Base URL")
                    TextField("(f.ex: https://127.0.0.1:11434/v1)", text: $aiBaseURL)
                        .textFieldStyle(.roundedBorder)
                        .onSubmit {
                            print("SettingsView: Base URL submitted: \\(aiBaseURL)")
                            Task { await setBaseURL(aiBaseURL) }
                        }
                }
                .onChange(of: aiBaseURL) { newURL in
                    print("SettingsView: Base URL changed to \\(newURL)")
                }
            }

            Section("Appearance") {
                Picker("Theme", selection: $theme) {
                    Text("System").tag("system")
                    Text("Light").tag("light")
                    Text("Dark").tag("dark")
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Settings")
        .onAppear {
            print("SettingsView.onAppear")
            // If providers are empty, try loading them here too
            if providers.isEmpty {
                Task { await loadProviders() }
            }
            Task { await syncCurrentConfigAndModels() }
        }
    }

    private func loadModels() {
        guard let client = mcpClient else {
            print("SettingsView.loadModels: no MCP client")
            return
        }

        Task {
            do {
                let response = try await client.callTool("list_models", arguments: [:])
                print("SettingsView.loadModels raw: \(response)")
                let data = Data(response.utf8)

                var loadedModels: [ModelInfo] = []
                if let decoded = try? JSONDecoder().decode([ModelInfo].self, from: data) {
                    loadedModels = decoded
                    print("SettingsView.loadModels decoded objects: \(loadedModels.count)")
                } else if let ids = try? JSONDecoder().decode([String].self, from: data) {
                    loadedModels = ids.map { ModelInfo(id: $0, description: "", current: false) }
                    print("SettingsView.loadModels decoded strings: \(loadedModels.count)")
                } else {
                    print("SettingsView.loadModels: failed to decode models JSON")
                }

                await MainActor.run {
                    self.models = loadedModels
                    // If no current flag provided, try to keep selection consistent
                    if !loadedModels.contains(where: { $0.id == aiModel }) {
                        if let first = loadedModels.first {
                            aiModel = first.id
                        }
                    }
                }
            } catch {
                print("SettingsView.loadModels error: \(error)")
                await MainActor.run { models = [] }
            }
        }
    }

    private func setProviderAndLoadModels(_ provider: String) async {
        guard let client = mcpClient else {
            print("SettingsView.setProviderAndLoadModels: no MCP client")
            return
        }

        do {
            _ = try await client.callTool("set_provider", arguments: ["provider": provider])
            await loadProviders()
            await MainActor.run { models = [] }
            loadModels()
        } catch {
            print("SettingsView.setProviderAndLoadModels error: \(error)")
        }
    }

    private func setModel(_ model: String) async {
        guard let client = mcpClient else {
            print("SettingsView.setModel: no MCP client")
            return
        }

        do {
            _ = try await client.callTool("set_model", arguments: ["model": model])
        } catch {
            print("SettingsView.setModel error: \(error)")
        }
    }

    private func setBaseURL(_ url: String) async {
        guard let client = mcpClient else {
            print("SettingsView.setBaseURL: no MCP client")
            return
        }
        do {
            _ = try await client.callTool("set_setting", arguments: ["name": "ai.baseurl", "value": url])
            print("SettingsView.setBaseURL applied: \(url)")
        } catch {
            print("SettingsView.setBaseURL error: \(error)")
        }
    }

    private func loadProviders() async {
        guard let client = mcpClient else {
            print("SettingsView.loadProviders: no MCP client")
            return
        }

        do {
            let response = try await client.callTool("list_providers", arguments: [:])
            print("SettingsView.loadProviders raw: \(response)")
            let data = Data(response.utf8)
            var loaded: [ProviderInfo] = []
            if let decoded = try? JSONDecoder().decode([ProviderInfo].self, from: data) {
                loaded = decoded
            } else if let names = try? JSONDecoder().decode([String].self, from: data) {
                loaded = names.map { ProviderInfo(name: $0) }
            }
            await MainActor.run { providers = loaded }
            print("SettingsView.loadProviders loaded: \(loaded.count)")
        } catch {
            print("SettingsView.loadProviders error: \(error)")
        }
    }

    private func syncCurrentConfigAndModels() async {
        guard let client = mcpClient else {
            print("SettingsView.syncCurrentConfigAndModels: no MCP client")
            return
        }
        do {
            let resp = try await client.callTool("get_settings", arguments: [:])
            print("SettingsView.get_settings raw: \(resp.prefix(200))")
            if let data = resp.data(using: .utf8),
               let dict = try JSONSerialization.jsonObject(with: data) as? [String: Any] {
                let provider = (dict["ai.provider"] as? String) ?? aiProvider
                let model = (dict["ai.model"] as? String) ?? aiModel
                let baseurl = (dict["ai.baseurl"] as? String) ?? aiBaseURL
                await MainActor.run {
                    aiProvider = provider
                    aiModel = model
                    aiBaseURL = baseurl
                }
            }
            // Ensure models list reflects current provider
            loadModels()
        } catch {
            print("SettingsView.syncCurrentConfigAndModels error: \(error)")
            // Still try to load models with current UI state
            loadModels()
        }
    }
}
