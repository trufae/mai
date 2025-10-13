import SwiftUI

struct SettingsView: View {
    @AppStorage("aiProvider") private var aiProvider = "openai"
    @AppStorage("aiModel") private var aiModel = "gpt-4"
    @AppStorage("theme") private var theme = "system"

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
                    Task {
                        await setModel(newModel)
                    }
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
            // Set current provider from the data
            if let currentProvider = providers.first(where: { $0.current }) {
                aiProvider = currentProvider.name
            }
            loadModels()
        }
    }

    private func loadModels() {
        guard let client = mcpClient else {
            return
        }

        Task {
            do {
                let response = try await client.callTool("list_models", arguments: [:])

                let data = response.data(using: .utf8)!
                let loadedModels = try JSONDecoder().decode([ModelInfo].self, from: data)

                await MainActor.run {
                    models = loadedModels
                    // Set current model from the data
                    if let currentModel = models.first(where: { $0.current }) {
                        aiModel = currentModel.id
                    }
                }
            } catch {
                await MainActor.run {
                    models = []
                }
            }
        }
    }

    private func setProviderAndLoadModels(_ provider: String) async {
        guard let client = mcpClient else {
            return
        }

        do {
            _ = try await client.callTool("set_provider", arguments: ["provider": provider])
            // Reload providers to update current flag
            await loadProviders()
            // Clear current models while loading new ones
            await MainActor.run {
                models = []
            }
            loadModels()
        } catch {
            // Handle error if needed
        }
    }

    private func setModel(_ model: String) async {
        guard let client = mcpClient else {
            return
        }

        do {
            _ = try await client.callTool("set_model", arguments: ["model": model])
        } catch {
            // Handle error if needed
        }
    }

    private func loadProviders() async {
        guard let client = mcpClient else {
            return
        }

        do {
            let response = try await client.callTool("list_providers", arguments: [:])

            let data = response.data(using: .utf8)!
            let loadedProviders = try JSONDecoder().decode([ProviderInfo].self, from: data)

            await MainActor.run {
                providers = loadedProviders
            }
        } catch {
            // Handle error if needed
        }
    }
}