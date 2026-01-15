//
//  ContentView.swift
//  MaiUI
//
//  Created by MaiUI on 2025-01-11.
//

import SwiftUI

#if os(macOS)
    import AppKit
#endif

struct ContentView: View {
    @AppStorage("aiProvider") private var aiProvider = "openai"
    @AppStorage("aiModel") private var aiModel = "gpt-4"
    @AppStorage("aiStreaming") private var aiStreaming = false
    @State private var messages: [ChatMessage] = []
    @State private var inputText = ""
    @State private var isWaitingForResponse = false
    @State private var mcpClient: MCPClient?
    @State private var showErrorAlert = false
    @State private var errorMessage = ""
    @State private var mcpConnected = false
    @State private var providers: [ProviderInfo] = []
    @State private var selectedTab = "Chat"
    @FocusState private var isInputFocused: Bool

    @Environment(\.colorScheme) var colorScheme

    private var buttonColor: Color {
        let trimmed = inputText.trimmingCharacters(in: .whitespacesAndNewlines)
        let color: Color
        if isWaitingForResponse {
            color = Color.gray.opacity(0.6)
        } else if trimmed.isEmpty {
            color = Color.gray.opacity(0.4)
        } else {
            color = Color.white
        }
        print(
            "ContentView buttonColor computed with isWaiting: \(isWaitingForResponse), trimmedEmpty: \(trimmed.isEmpty), color: \(color)"
        )
        return color
    }

    private var buttonIconColor: Color {
        if buttonColor == Color.white {
            return Color.black
        } else {
            return Color.white
        }
    }

    var body: some View {
        print(
            "ContentView body recomputed. messages: \(messages.count), waiting: \(isWaitingForResponse), selectedTab: \(selectedTab)"
        )
        return NavigationSplitView {
            // Sidebar
            List(selection: $selectedTab) {
                Label("Chat", systemImage: "message")
                    .tag("Chat")
                Label("Console", systemImage: "terminal")
                    .tag("Console")
                Label("Settings", systemImage: "gear")
                    .tag("Settings")
                Label("Tools", systemImage: "wrench")
                    .tag("Tools")
                Label("Prompts", systemImage: "doc.plaintext")
                    .tag("Prompts")
                Label("Sessions", systemImage: "person.crop.circle")
                    .tag("Sessions")
                Label("Context", systemImage: "info.circle")
                    .tag("Context")
            }
            .listStyle(.sidebar)
            .navigationTitle("Mai")
        } detail: {
            if selectedTab == "Chat" {
                VStack(spacing: 0) {
                    Button(action: clearChat) {
                        Text("")
                    }
                    .keyboardShortcut("l", modifiers: .command)
                    .opacity(0)
                    .frame(width: 0, height: 0)

                    // Header
                    HStack {
                        Spacer()
                    }

                    // Messages
                    ScrollViewReader { scrollView in
                        ScrollView {
                            LazyVStack(spacing: 12) {
                                ForEach(messages) { message in
                                    MessageBubble(message: message)
                                        .id(message.id)
                                }

                                if isWaitingForResponse {
                                    TypingIndicator()
                                        .transition(.opacity)
                                }
                            }
                            .padding()
                        }
                        .onChange(of: messages.count) { _ in
                            print("ContentView onChange messages.count triggered. New count: \(messages.count)")
                            withAnimation {
                                scrollView.scrollTo(messages.last?.id, anchor: .bottom)
                            }
                        }
                    }

                    // Input area
                    VStack(spacing: 0) {
                        HStack(spacing: 12) {
                            ZStack(alignment: .leading) {
                                if inputText.isEmpty {
                                    Text("Type your message...")
                                        .foregroundColor(.secondary)
                                        .padding(.horizontal, 5)
                                }

                                TextField("", text: $inputText)
                                    .textFieldStyle(.plain)
                                    .padding(.horizontal, 4)
                                    .padding(.vertical, 4)
                                    .background(Color.clear)
                                    .focused($isInputFocused)
                                    .disabled(isWaitingForResponse)
                                    .onSubmit {
                                        print("ContentView TextField onSubmit triggered")
                                        sendMessage()
                                        isInputFocused = true
                                    }
                            }

                            Button(action: {
                                print("ContentView send button tapped")
                                if isWaitingForResponse {
                                    cancelRequest()
                                } else {
                                    sendMessage()
                                }
                            }) {
                                ZStack {
                                    Circle()
                                        .fill(buttonColor)
                                        .frame(width: 36, height: 36)

                                    Image(systemName: isWaitingForResponse ? "stop.fill" : "arrow.up")
                                        .font(.system(size: 12, weight: .semibold))
                                        .foregroundColor(buttonIconColor)
                                }
                            }
                            .buttonStyle(.plain)
                            .disabled(
                                inputText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                    && !isWaitingForResponse)
                        }
                        .padding(.horizontal, 10)
                        .padding(.vertical, 10)
                        .background(Color(.windowBackgroundColor).opacity(0.9))
                        // .background(.ultraThinMaterial)
                        .clipShape(RoundedRectangle(cornerRadius: 15))
                        .shadow(color: Color.black.opacity(0.1), radius: 5, x: 0, y: 4)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 16)
                    }
                }
                .onAppear {
                    print("ContentView onAppear triggered")
                    isInputFocused = true
                    setupMCP()
                    #if os(macOS)
                        DispatchQueue.main.async {
                            NSApplication.shared.activate(ignoringOtherApps: true)
                            if let window = NSApplication.shared.keyWindow {
                                window.makeKeyAndOrderFront(nil)
                            }
                        }
                    #endif
                }
                .alert("MCP Connection Error", isPresented: $showErrorAlert) {
                    Button("OK", role: .cancel) {}
                } message: {
                    Text(errorMessage)
                }
            } else if selectedTab == "Console" {
                ConsoleView()
            } else if selectedTab == "Settings" {
                SettingsView(mcpClient: mcpClient, providers: $providers)
             } else if selectedTab == "Tools" {
                 ToolsView()
             } else if selectedTab == "Prompts" {
                Text("Prompts")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if selectedTab == "Sessions" {
                Text("Sessions")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if selectedTab == "Context" {
                Text("Context")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Text("")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }

    private func setupMCP() {
        print("ContentView.setupMCP invoked")
        mcpClient = MCPClient()
        Task.detached {
            print("ContentView.setupMCP Task started")
            do {
                try await mcpClient?.initialize()
                await MainActor.run {
                    print("ContentView.setupMCP Task success - MCP initialized")
                    mcpConnected = true
                }
                // Load providers after initialization
                await loadProviders()
                // Sync current provider and model
                await syncModelSettings()
            } catch {
                await MainActor.run {
                    print("ContentView.setupMCP Task failed with error: \(error)")
                    mcpConnected = false
                    errorMessage =
                        "Failed to connect to MAI: \(error.localizedDescription)\n\nPlease make sure 'mai' is installed and available in your PATH."
                    showErrorAlert = true
                }
            }
        }
    }

    private func loadProviders() async {
        print("ContentView.loadProviders called")
        guard let client = mcpClient else {
            print("ContentView.loadProviders: no MCP client")
            return
        }

        do {
            let response = try await client.callTool("list_providers", arguments: [:])
            print("ContentView.loadProviders received raw text: \(response)")

            let data = Data(response.utf8)
            var loaded: [ProviderInfo] = []
            if let decoded = try? JSONDecoder().decode([ProviderInfo].self, from: data) {
                loaded = decoded
                print("ContentView.loadProviders decoded ProviderInfo objects: \(loaded.count)")
            } else if let decodedNames = try? JSONDecoder().decode([String].self, from: data) {
                loaded = decodedNames.map { ProviderInfo(name: $0) }
                print("ContentView.loadProviders decoded string providers: \(loaded.count)")
            } else {
                print("ContentView.loadProviders failed to decode providers JSON")
            }
            await MainActor.run { self.providers = loaded }
            print("ContentView.loadProviders set providers: \(loaded.count)")
        } catch {
            print("ContentView.loadProviders error: \(error)")
        }
    }

    private func syncModelSettings() async {
        print("ContentView.syncModelSettings called")
        guard let client = mcpClient else {
            print("ContentView.syncModelSettings: no MCP client")
            return
        }

        do {
            // Get current settings from AppStorage
            let currentProvider = await MainActor.run { aiProvider }
            let currentModel = await MainActor.run { aiModel }

            print("ContentView.syncModelSettings syncing provider: \(currentProvider), model: \(currentModel)")

            // Set provider
            _ = try await client.callTool("set_provider", arguments: ["provider": currentProvider])
            print("ContentView.syncModelSettings set provider successfully")

            // Set model
            _ = try await client.callTool("set_model", arguments: ["model": currentModel])
            print("ContentView.syncModelSettings set model successfully")
        } catch {
            print("ContentView.syncModelSettings error: \(error)")
        }
    }

    private func sendMessage() {
        print("ContentView.sendMessage invoked with input: \(inputText)")
        let text = inputText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else {
            print("ContentView.sendMessage aborted due to empty text")
            return
        }

        addMessage(text, isUser: true)
        inputText = ""
        isWaitingForResponse = true
        print("ContentView.sendMessage queued MCP request with text: \(text)")

        Task.detached {
            print("ContentView.sendMessage Task started")
            let client = await MainActor.run { () -> MCPClient? in
                print("ContentView.sendMessage retrieving mcpClient on MainActor")
                return mcpClient
            }
            do {
                guard let mcpClient = client else {
                    await MainActor.run {
                        print("ContentView.sendMessage no MCP client available")
                        addMessage("Error: Not connected to MAI", isUser: false)
                        isWaitingForResponse = false
                    }
                    return
                }
                print("ContentView.sendMessage calling tool on MCP")
                let response = try await mcpClient.callTool(
                    "send_message", arguments: ["message": text, "stream": aiStreaming]
                )
                await MainActor.run {
                    print("ContentView.sendMessage received response: \(response)")
                    addMessage(response, isUser: false)
                    isWaitingForResponse = false
                    isInputFocused = true
                }
            } catch {
                await MainActor.run {
                    print("ContentView.sendMessage encountered error: \(error)")
                    addMessage("Error: \(error.localizedDescription)", isUser: false)
                    isWaitingForResponse = false
                    isInputFocused = true
                }
            }
        }
    }

    private func cancelRequest() {
        print("ContentView.cancelRequest invoked")
        isWaitingForResponse = false
        isInputFocused = true
        addMessage("Request cancelled", isUser: false)

        Task.detached {
            print("ContentView.cancelRequest Task started")
            let client = await MainActor.run { () -> MCPClient? in
                print("ContentView.cancelRequest retrieving mcpClient on MainActor")
                return mcpClient
            }
            do {
                guard let mcpClient = client else {
                    await MainActor.run {
                        print("ContentView.cancelRequest no MCP client available")
                        addMessage("Error: Not connected to MAI", isUser: false)
                    }
                    return
                }
                print("ContentView.cancelRequest calling cancel on MCP")
                _ = try await mcpClient.callTool("cancel_request", arguments: [:])
                await MainActor.run {
                    print("ContentView.cancelRequest cancelled successfully")
                }
            } catch {
                await MainActor.run {
                    print("ContentView.cancelRequest encountered error: \(error)")
                    // Error is ok, as the UI already shows cancelled
                }
            }
        }
    }

    private func addMessage(_ text: String, isUser: Bool) {
        print("ContentView.addMessage invoked with text: \(text.prefix(80)), isUser: \(isUser)")
        let message = ChatMessage(text: text, isUser: isUser, timestamp: Date())
        messages.append(message)
        print("ContentView.addMessage appended message. Total messages: \(messages.count)")
    }

    private func clearChat() {
        messages = []
    }
}
