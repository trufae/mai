import SwiftUI

struct SettingsView: View {
    @AppStorage("aiProvider") private var aiProvider = "openai"
    @AppStorage("aiModel") private var aiModel = "gpt-4"
    @AppStorage("theme") private var theme = "system"
    @AppStorage("aiBaseURL") private var aiBaseURL = ""
    @State private var aiDeterministic = false

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
                    Text("BaseURL")
                    TextField("", text: $aiBaseURL)
                        .textFieldStyle(.roundedBorder)
                        .onSubmit {
                            print("SettingsView: Base URL submitted: \(aiBaseURL)")
                            Task { await setBaseURL(aiBaseURL) }
                        }
                }
                .onChange(of: aiBaseURL) { newURL in
                    print("SettingsView: Base URL changed to \(newURL)")
                }

                Toggle("Deterministic", isOn: $aiDeterministic)
                    .onChange(of: aiDeterministic) { newValue in
                        print("SettingsView: Deterministic changed to \(newValue)")
                        Task { await setDeterministic(newValue) }
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
        print("SettingsView.loadModels: starting")
        guard let client = mcpClient else {
            print("SettingsView.loadModels: no MCP client")
            return
        }

        Task {
            do {
                var response: String
                print("SettingsView.loadModels: attempting get_models")
                do {
                    response = try await client.callTool("get_models", arguments: [:])
                    print("SettingsView.loadModels: get_models succeeded, raw response: \(response)")
                } catch {
                    print("SettingsView.loadModels: get_models failed: \(error). Falling back to list_models")
                    response = try await client.callTool("list_models", arguments: [:])
                    print("SettingsView.loadModels: list_models succeeded, raw response: \(response)")
                }
                let data = Data(response.utf8)
                print("SettingsView.loadModels: converting response to data")

                var loadedModels: [ModelInfo] = []
                print("SettingsView.loadModels: attempting to decode as ModelInfo array")
                if let decoded = try? JSONDecoder().decode([ModelInfo].self, from: data) {
                    loadedModels = decoded
                    print("SettingsView.loadModels: decoded objects: \(loadedModels.count)")
                } else if let ids = try? JSONDecoder().decode([String].self, from: data) {
                    loadedModels = ids.map { ModelInfo(id: $0, description: "", current: false) }
                    print("SettingsView.loadModels: decoded strings: \(loadedModels.count)")
                } else {
                    print("SettingsView.loadModels: failed to decode models JSON")
                }

                print("SettingsView.loadModels: updating UI on main actor")
                await MainActor.run {
                    print("SettingsView.loadModels: setting models to \(loadedModels.count) items")
                    self.models = loadedModels
                    // Prefer server-reported current model if available
                    if let current = loadedModels.first(where: { $0.current }) {
                        print("SettingsView.loadModels: setting aiModel to current: \(current.id)")
                        aiModel = current.id
                    } else if !loadedModels.contains(where: { $0.id == aiModel }) {
                        // Otherwise keep selection if still present; else pick first
                        if let first = loadedModels.first {
                            print("SettingsView.loadModels: aiModel not in list, setting to first: \(first.id)")
                            aiModel = first.id
                        } else {
                            print("SettingsView.loadModels: no models available")
                        }
                    } else {
                        print("SettingsView.loadModels: keeping existing aiModel: \(aiModel)")
                    }
                }
                print("SettingsView.loadModels: completed successfully")
            } catch {
                print("SettingsView.loadModels error: \(error)")
                await MainActor.run { models = [] }
            }
        }
    }

    private func setProviderAndLoadModels(_ provider: String) async {
        print("SettingsView.setProviderAndLoadModels: starting for provider \(provider)")
        guard let client = mcpClient else {
            print("SettingsView.setProviderAndLoadModels: no MCP client")
            return
        }

        do {
            print("SettingsView.setProviderAndLoadModels: calling set_provider")
            _ = try await client.callTool("set_provider", arguments: ["provider": provider])
            print("SettingsView.setProviderAndLoadModels: set_provider succeeded")
            print("SettingsView.setProviderAndLoadModels: calling loadProviders")
            await loadProviders()
            print("SettingsView.setProviderAndLoadModels: loadProviders completed")
            print("SettingsView.setProviderAndLoadModels: calling list_config")
            let resp = try await client.callTool("list_config", arguments: [:])
            print("SettingsView.setProviderAndLoadModels: list_config succeeded")
            if let data = resp.data(using: .utf8),
               let dict = try JSONSerialization.jsonObject(with: data) as? [String: Any],
               let model = dict["ai.model"] as? String
            {
                await MainActor.run {
                    print("SettingsView.setProviderAndLoadModels: setting aiModel to \(model)")
                    aiModel = model
                }
            } else {
                print("SettingsView.setProviderAndLoadModels: failed to parse ai.model from list_config")
            }
            await MainActor.run {
                print("SettingsView.setProviderAndLoadModels: clearing models array")
                models = []
            }
            print("SettingsView.setProviderAndLoadModels: calling loadModels")
            loadModels()
            print("SettingsView.setProviderAndLoadModels: loadModels completed")
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
            _ = try await client.callTool("set_config", arguments: ["key": "ai.baseurl", "value": url])
            print("SettingsView.setBaseURL applied: \(url)")
        } catch {
            print("SettingsView.setBaseURL error: \(error)")
        }
    }

    private func setDeterministic(_ value: Bool) async {
        guard let client = mcpClient else {
            print("SettingsView.setDeterministic: no MCP client")
            return
        }
        do {
            _ = try await client.callTool(
                "set_config", arguments: ["key": "ai.deterministic", "value": value]
            )
            print("SettingsView.setDeterministic applied: \(value)")
        } catch {
            print("SettingsView.setDeterministic error: \(error)")
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
            let resp = try await client.callTool("list_config", arguments: [:])
            print("SettingsView.list_config raw: \(resp.prefix(200))")
            if let data = resp.data(using: .utf8),
               let dict = try JSONSerialization.jsonObject(with: data) as? [String: Any]
            {
                let provider = (dict["ai.provider"] as? String) ?? aiProvider
                let model = (dict["ai.model"] as? String) ?? aiModel
                let baseurl = (dict["ai.baseurl"] as? String) ?? aiBaseURL
                let deterministic = (dict["ai.deterministic"] as? Bool) ?? aiDeterministic
                await MainActor.run {
                    aiProvider = provider
                    aiModel = model
                    aiBaseURL = baseurl
                    aiDeterministic = deterministic
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
