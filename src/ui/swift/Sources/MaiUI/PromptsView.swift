import SwiftUI

struct CustomPrompt: Identifiable, Equatable {
    let id = UUID()
    let name: String
    let content: String
    let isFileBased: Bool
    let server: String? // for MCP prompts
}

struct PromptsView: View {
    @State private var prompts: [CustomPrompt] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var showingCreateSheet = false
    @State private var showingEditSheet = false
    @State private var showingRenameSheet = false
    @State private var editingPrompt: CustomPrompt?
    @State private var renamingPrompt: CustomPrompt?
    @State private var newPromptName = ""
    @State private var newPromptContent = ""
    @State private var newName = ""

    let mcpClient: MCPClient?

    var body: some View {
        VStack(spacing: 16) {
            HStack {
                Text("Custom Prompts")
                    .font(.title2)
                    .bold()
                Spacer()
                Button(action: { showingCreateSheet = true }) {
                    Image(systemName: "plus")
                }
                .buttonStyle(.borderedProminent)
                Button(action: loadPrompts) {
                    Image(systemName: "arrow.clockwise")
                }
                .disabled(isLoading)
            }
            .padding(.horizontal)

            if isLoading {
                ProgressView("Loading prompts...")
            } else if let error = errorMessage {
                Text("Error: \(error)")
                    .foregroundColor(.red)
            } else if prompts.isEmpty {
                Text("No prompts found")
                    .foregroundColor(.secondary)
            } else {
                List {
                    ForEach(prompts) { prompt in
                        HStack {
                            VStack(alignment: .leading) {
                                Text(prompt.name)
                                    .font(.headline)
                                if let server = prompt.server {
                                    Text("MCP: \(server)")
                                        .font(.caption)
                                        .foregroundColor(.secondary)
                                } else {
                                    Text("File-based")
                                        .font(.caption)
                                        .foregroundColor(.secondary)
                                }
                            }
                            Spacer()
                            Button(action: { editPrompt(prompt) }) {
                                Image(systemName: "pencil")
                            }
                            .buttonStyle(.borderless)
                            if prompt.isFileBased {
                                Button(action: { renamePrompt(prompt) }) {
                                    Image(systemName: "rectangle.and.pencil.and.ellipsis")
                                }
                                .buttonStyle(.borderless)
                            }
                            Button(action: { deletePrompt(prompt) }) {
                                Image(systemName: "trash")
                                    .foregroundColor(.red)
                            }
                            .buttonStyle(.borderless)
                        }
                    }
                }
            }
        }
        .padding()
        .onAppear {
            loadPrompts()
        }
        .sheet(isPresented: $showingCreateSheet) {
            PromptEditorView(
                title: "Create New Prompt",
                promptName: $newPromptName,
                promptContent: $newPromptContent,
                onSave: createPrompt,
                onCancel: { showingCreateSheet = false }
            )
        }
        .sheet(isPresented: $showingEditSheet) {
            if let prompt = editingPrompt {
                PromptEditorView(
                    title: "Edit Prompt",
                    promptName: .constant(prompt.name),
                    promptContent: .init(get: { prompt.content }, set: { newPromptContent = $0 }),
                    onSave: { updatePrompt(prompt, newContent: newPromptContent) },
                    onCancel: { showingEditSheet = false },
                    isEditing: true
                )
            }
        }
        .sheet(isPresented: $showingRenameSheet) {
            if let prompt = renamingPrompt {
                RenamePromptView(
                    prompt: prompt,
                    newName: $newName,
                    onSave: { renamePromptAction(prompt, newName: newName) },
                    onCancel: { showingRenameSheet = false }
                )
            }
        }
    }

    private func loadPrompts() {
        isLoading = true
        errorMessage = nil

        Task {
            do {
                var loadedPrompts: [CustomPrompt] = []

                // Load MCP prompts
                if let client = mcpClient {
                    let mcpResponse = try await client.callTool("shell/command_executor", arguments: ["command": "mai-tool -q prompts list"])
                    // Parse MCP prompts - this might need adjustment based on actual response
                    if let mcpPrompts = parseMCPPrompts(from: mcpResponse) {
                        loadedPrompts.append(contentsOf: mcpPrompts)
                    }
                }

                // Load file-based prompts
                let filePrompts = try await loadFileBasedPrompts()
                loadedPrompts.append(contentsOf: filePrompts)

                await MainActor.run {
                    prompts = loadedPrompts.sorted { $0.name < $1.name }
                    isLoading = false
                }
            } catch {
                await MainActor.run {
                    errorMessage = error.localizedDescription
                    isLoading = false
                }
            }
        }
    }

    private func parseMCPPrompts(from response: String) -> [CustomPrompt]? {
        // Parse JSON response from command_executor
        guard let data = response.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let stdout = json["stdout"] as? String else {
            return nil
        }

        // Parse the stdout from mai-tool prompts list
        let lines = stdout.split(separator: "\n")
        var prompts: [CustomPrompt] = []
        for line in lines {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.contains("/") && !trimmed.hasPrefix("#") {
                let parts = trimmed.split(separator: "/", maxSplits: 1)
                if parts.count == 2 {
                    let server = String(parts[0])
                    let name = String(parts[1])
                    // For MCP prompts, we don't have content yet, load on demand
                    prompts.append(CustomPrompt(name: name, content: "", isFileBased: false, server: server))
                }
            }
        }
        return prompts
    }

    private func loadFileBasedPrompts() async throws -> [CustomPrompt] {
        guard let client = mcpClient else { return [] }

        // List files in prompts directory
        let listResponse = try await client.callTool("shell/list_files", arguments: ["directory_path": "share/mai/prompts"])
        let files = parseFileList(from: listResponse)

        var prompts: [CustomPrompt] = []
        for file in files where file.hasSuffix(".md") {
            let name = String(file.dropLast(3)) // remove .md
            let content = try await loadFileContent(file)
            prompts.append(CustomPrompt(name: name, content: content, isFileBased: true, server: nil))
        }
        return prompts
    }

    private func parseFileList(from response: String) -> [String] {
        // Parse JSON response from list_files
        guard let data = response.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let files = json["files"] as? [[String: Any]] else {
            return []
        }

        return files.compactMap { $0["name"] as? String }.filter { $0.hasSuffix(".md") }
    }

    private func loadFileContent(_ filename: String) async throws -> String {
        guard let client = mcpClient else { return "" }

        let response = try await client.callTool("shell/read_file", arguments: ["filepath": "share/mai/prompts/\(filename)"])
        if let data = response.data(using: .utf8),
           let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let content = json["content"] as? String {
            return content
        }
        return ""
    }

    private func createPrompt() {
        guard !newPromptName.isEmpty else { return }

        Task {
            do {
                guard let client = mcpClient else { return }

                _ = try await client.callTool("shell/write_file", arguments: [
                    "filepath": "share/mai/prompts/\(newPromptName).md",
                    "content": newPromptContent
                ])

                await MainActor.run {
                    showingCreateSheet = false
                    newPromptName = ""
                    newPromptContent = ""
                    loadPrompts()
                }
            } catch {
                await MainActor.run {
                    errorMessage = "Failed to create prompt: \(error.localizedDescription)"
                }
            }
        }
    }

    private func editPrompt(_ prompt: CustomPrompt) {
        editingPrompt = prompt
        newPromptContent = prompt.content
        showingEditSheet = true
    }

    private func renamePrompt(_ prompt: CustomPrompt) {
        renamingPrompt = prompt
        newName = prompt.name
        showingRenameSheet = true
    }

    private func updatePrompt(_ prompt: CustomPrompt, newContent: String) {
        Task {
            do {
                guard let client = mcpClient else { return }

                _ = try await client.callTool("shell/write_file", arguments: [
                    "filepath": "share/mai/prompts/\(prompt.name).md",
                    "content": newContent
                ])

                await MainActor.run {
                    showingEditSheet = false
                    editingPrompt = nil
                    newPromptContent = ""
                    loadPrompts()
                }
            } catch {
                await MainActor.run {
                    errorMessage = "Failed to update prompt: \(error.localizedDescription)"
                }
            }
        }
    }

    private func deletePrompt(_ prompt: CustomPrompt) {
        Task {
            do {
                guard let client = mcpClient else { return }

                _ = try await client.callTool("shell/delete_file", arguments: [
                    "filepath": "share/mai/prompts/\(prompt.name).md"
                ])

                await MainActor.run {
                    loadPrompts()
                }
            } catch {
                await MainActor.run {
                    errorMessage = "Failed to delete prompt: \(error.localizedDescription)"
                }
            }
        }
    }

    private func renamePromptAction(_ prompt: CustomPrompt, newName: String) {
        Task {
            do {
                guard let client = mcpClient else { return }

                _ = try await client.callTool("shell/move_file", arguments: [
                    "source": "share/mai/prompts/\(prompt.name).md",
                    "destination": "share/mai/prompts/\(newName).md"
                ])

                await MainActor.run {
                    showingRenameSheet = false
                    renamingPrompt = nil
                    self.newName = ""
                    loadPrompts()
                }
            } catch {
                await MainActor.run {
                    errorMessage = "Failed to rename prompt: \(error.localizedDescription)"
                }
            }
        }
    }
}

struct PromptEditorView: View {
    let title: String
    @Binding var promptName: String
    @Binding var promptContent: String
    let onSave: () -> Void
    let onCancel: () -> Void
    var isEditing = false

    @FocusState private var isNameFocused: Bool
    @FocusState private var isContentFocused: Bool

    var body: some View {
        NavigationView {
            Form {
                if !isEditing {
                    Section("Name") {
                        TextField("Prompt name", text: $promptName)
                            .focused($isNameFocused)
                    }
                }
                Section("Content") {
                    TextEditor(text: $promptContent)
                        .frame(minHeight: 200)
                        .focused($isContentFocused)
                }
            }
            .navigationTitle(title)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onCancel)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save", action: onSave).disabled(promptName.isEmpty && !isEditing)
                }
            }
        }
        .onAppear {
            if !isEditing {
                isNameFocused = true
            } else {
                isContentFocused = true
            }
        }
    }
}

struct RenamePromptView: View {
    let prompt: CustomPrompt
    @Binding var newName: String
    let onSave: () -> Void
    let onCancel: () -> Void

    @FocusState private var isNameFocused: Bool

    var body: some View {
        NavigationView {
            Form {
                Section("Current Name") {
                    Text(prompt.name)
                        .foregroundColor(.secondary)
                }
                Section("New Name") {
                    TextField("New prompt name", text: $newName)
                        .focused($isNameFocused)
                }
            }
            .navigationTitle("Rename Prompt")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onCancel)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Rename", action: onSave).disabled(newName.isEmpty || newName == prompt.name)
                }
            }
        }
        .onAppear {
            isNameFocused = true
        }
    }
}