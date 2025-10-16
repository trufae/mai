//
//  ConsoleView.swift
//  MaiUI
//
//  Created by MaiUI on 2025-01-11.
//

import SwiftUI

struct ConsoleMessage: Identifiable, Codable {
    var id = UUID()
    let text: String
    let isUser: Bool
    let timestamp: Date
}

struct ConsoleView: View {
    @State private var messages: [ConsoleMessage] = []
    @State private var inputText = ""
    @State private var isWaitingForResponse = false
    @State private var mcpClient: MCPClient?
    @State private var showErrorAlert = false
    @State private var errorMessage = ""
    @State private var mcpConnected = false
    @FocusState private var isInputFocused: Bool

    @Environment(\.colorScheme) var colorScheme

    private let messagesKey = "consoleMessages"

    private func saveMessages() {
        do {
            let data = try JSONEncoder().encode(messages)
            UserDefaults.standard.set(data, forKey: messagesKey)
        } catch {
            print("Failed to save console messages: \(error)")
        }
    }

    private func loadMessages() {
        guard let data = UserDefaults.standard.data(forKey: messagesKey) else { return }
        do {
            messages = try JSONDecoder().decode([ConsoleMessage].self, from: data)
        } catch {
            print("Failed to load console messages: \(error)")
        }
    }

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
        VStack(spacing: 0) {
            // Header
            HStack {
                Spacer()
            }

            // Messages
            ScrollViewReader { scrollView in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 8) {
                        ForEach(messages) { message in
                            HStack(alignment: .top, spacing: 8) {
                                if message.isUser {
                                    Text(message.text)
                                        .font(.system(.body, design: .monospaced))
                                        .foregroundColor(.white)
                                        .padding(.horizontal, 12)
                                        .padding(.vertical, 8)
                                        .background(Color.blue.opacity(0.8))
                                        .clipShape(RoundedRectangle(cornerRadius: 8))
                                } else {
                                    Text(message.text)
                                        .font(.system(.body, design: .monospaced))
                                        .foregroundColor(.white)
                                        .padding(.horizontal, 12)
                                        .padding(.vertical, 8)
                                        .background(Color.black.opacity(0.8))
                                        .clipShape(RoundedRectangle(cornerRadius: 8))
                                }
                            }
                            .id(message.id)
                        }

                        if isWaitingForResponse {
                            HStack(alignment: .top, spacing: 8) {
                                TypingIndicator()
                                    .transition(.opacity)
                            }
                        }
                    }
                    .padding()
                }
                .background(Color.black)
                .onChange(of: messages.count) { _ in
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
                            Text("help")
                                .foregroundColor(.secondary)
                                .font(.system(.body, design: .monospaced))
                                .padding(.horizontal, 5)
                        }

                        TextField("", text: $inputText)
                            .textFieldStyle(.plain)
                            .font(.system(.body, design: .monospaced))
                            .padding(.horizontal, 4)
                            .padding(.vertical, 4)
                            .background(Color.clear)
                            .focused($isInputFocused)
                            .disabled(isWaitingForResponse)
                            .onSubmit {
                                sendCommand()
                                isInputFocused = true
                            }
                    }

                    Button(action: {
                        if isWaitingForResponse {
                            cancelRequest()
                        } else {
                            sendCommand()
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
                .clipShape(RoundedRectangle(cornerRadius: 15))
                .shadow(color: Color.black.opacity(0.1), radius: 5, x: 0, y: 4)
                .padding(.horizontal, 16)
                .padding(.vertical, 16)
            }
        }
        .onAppear {
            isInputFocused = true
            loadMessages()
            setupMCP()
        }
        .alert("MCP Connection Error", isPresented: $showErrorAlert) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(errorMessage)
        }
    }

    private func setupMCP() {
        mcpClient = MCPClient()
        Task.detached {
            do {
                try await mcpClient?.initialize()
                await MainActor.run {
                    mcpConnected = true
                }
            } catch {
                await MainActor.run {
                    mcpConnected = false
                    errorMessage =
                        "Failed to connect to MAI: \(error.localizedDescription)\n\nPlease make sure 'mai' is installed and available in your PATH."
                    showErrorAlert = true
                }
            }
        }
    }

    private func sendCommand() {
        let text = inputText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else {
            return
        }

        addMessage(text, isUser: true)
        inputText = ""
        isWaitingForResponse = true

        Task.detached {
            let client = await MainActor.run { () -> MCPClient? in
                return mcpClient
            }
            do {
                guard let mcpClient = client else {
                    await MainActor.run {
                        addMessage("Error: Not connected to MAI", isUser: false)
                        isWaitingForResponse = false
                    }
                    return
                }
                let response = try await mcpClient.callTool(
                    "execute_command", arguments: ["command": text]
                )
                await MainActor.run {
                    addMessage(response, isUser: false)
                    isWaitingForResponse = false
                    isInputFocused = true
                }
            } catch {
                await MainActor.run {
                    addMessage("Error: \(error.localizedDescription)", isUser: false)
                    isWaitingForResponse = false
                    isInputFocused = true
                }
            }
        }
    }

    private func cancelRequest() {
        isWaitingForResponse = false
        isInputFocused = true
        addMessage("Request cancelled", isUser: false)

        Task.detached {
            let client = await MainActor.run { () -> MCPClient? in
                return mcpClient
            }
            do {
                guard let mcpClient = client else {
                    await MainActor.run {
                        addMessage("Error: Not connected to MAI", isUser: false)
                    }
                    return
                }
                _ = try await mcpClient.callTool("cancel_request", arguments: [:])
                await MainActor.run {}
            } catch {
                await MainActor.run {}
            }
        }
    }

    private func addMessage(_ text: String, isUser: Bool) {
        let message = ConsoleMessage(text: text, isUser: isUser, timestamp: Date())
        messages.append(message)
        saveMessages()
    }
}
