# MaiUI - SwiftUI Chat Interface

A modern, native macOS chat interface for MAI (Model Context Protocol) built with SwiftUI.

## Features

- **Native macOS Design**: Follows macOS design guidelines with native look and feel
- **Chat Bubbles**: Modern chat bubble design with message tails
- **Dark/Light Themes**: Automatic theme switching with system preference support
- **Animated Interactions**: Smooth animations for typing indicators and message sending
- **MCP Protocol**: Full Model Context Protocol support for communicating with MAI backend
- **Settings Panel**: Configurable AI provider and model settings

## Architecture

The UI is completely decoupled from the business logic, which is handled entirely by the MAI MCP server. The SwiftUI frontend focuses on providing a fluent user experience while the MCP client handles all communication.

### Components

- **ContentView**: Main chat interface with message display and input
- **MessageBubble**: Individual message display with chat bubble styling
- **TypingIndicator**: Animated indicator shown while waiting for responses
- **SettingsView**: Configuration panel for AI settings
- **MCPClient**: Handles JSON-RPC communication with MAI over stdio

## Building

### Prerequisites

- macOS 13.0+
- Swift 5.9+
- Xcode 14.0+

### Build Commands

```bash
# Build in release mode
make build

# Run the application
make run

# Run in debug mode
make debug

# Clean build artifacts
make clean

# Format code
make format
```

### Swift Package Manager

The project uses Swift Package Manager for dependency management. No external dependencies are required.

## Usage

1. Ensure MAI is installed and available in your PATH
2. Build and run the SwiftUI application
3. The app will automatically spawn `mai -M` and establish MCP connection
4. Start chatting with your AI assistant

## MCP Communication

The app communicates with MAI using the Model Context Protocol over stdio:

- Spawns `mai -M` process
- Sends JSON-RPC requests for initialization and tool calls
- Receives responses and displays them in the chat interface
- Handles streaming responses and error conditions

## Design Principles

Taking inspiration from the existing Go UI but advancing the design:

- **Native Integration**: Uses SwiftUI and AppKit components for true macOS integration
- **Modern UI**: Chat bubbles, smooth animations, and material design elements
- **Accessibility**: Proper contrast ratios and keyboard navigation
- **Performance**: Efficient rendering with SwiftUI's declarative approach
- **Decoupled Architecture**: Clean separation between UI and business logic

## Settings

The settings panel allows configuration of:

- AI Provider (OpenAI, Anthropic, Local)
- AI Model selection
- Theme preference (System, Light, Dark)

Settings are persisted using AppStorage and can be modified while the app is running.