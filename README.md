<img width="300px" height="300px" align="left" style="float: left; margin: 0 10px 0 0;" alt="mailogo" src="https://raw.githubusercontent.com/trufae/mai/master/mai-logo.png?nocache">

### M(🐱)AI: My Artificial Intelligence

[![CI](https://github.com/trufae/mai/actions/workflows/ci.yml/badge.svg)](https://github.com/trufae/mai/actions/workflows/ci.yml)

MAI is a comprehensive AI toolkit providing unified access to multiple AI providers through a powerful REPL shell, MCP (Model Context Protocol) ecosystem, and specialized tools for coding, shell operations, and more.

## Components

* **mai**: Main REPL shell with multi-provider AI support
* **mai-wmcp**: MCP proxy server exposing tools via REST API
* **mai-tool**: Client for interacting with MCP proxy servers
* **mai-bot**: Telegram/IRC bot integration
* **mai-vdb**: Vector database for semantic document search
* **mai-term**: Terminal multiplexer for shared PTY sessions
* **MCP Servers**: Specialized servers for shell, weather, coding, time, markdown, fediverse, and code analysis

## Key Features

* **Multi-Provider Support**: Single interface for Ollama, OpenAI, Claude, Gemini, DeepSeek, Mistral, Bedrock
* **MCP Ecosystem**: Rich tool ecosystem with standardized protocol for AI-tool integration
* **Shell Integration**: Command substitution, environment variables, inline expressions
* **Multi-Modal**: Image attachments and processing
* **HTTP API**: OpenAI-compatible endpoints plus simplified chat/generate APIs
* **Prompt Templating**: Custom prompts with variable substitution
* **Batch Processing**: Non-interactive modes for automation
* **Terminal Multiplexing**: Multiple clients sharing PTY sessions
* **Bot Integration**: Full REPL functionality in chat platforms
* **Vector Search**: Semantic search across documents
* **Multi-Platform UIs**: Native interfaces for GNOME, macOS, iOS

## Distinctive Capabilities

* **Unified MCP Implementation**: Complete MCP ecosystem with servers for coding, shell operations, weather, and more
* **Terminal-Aware MCP Tools**: MCP servers that interact with terminal sessions
* **Shell-Style AI Interactions**: Deep integration with command-line environments
* **Multi-UI Backend**: Single backend powering different native UIs
* **Bot with Full REPL**: Chat bots exposing complete REPL functionality

## Usage

### REPL Shell
```bash
mai                    # Start interactive REPL
mai "hello world"      # Send message directly
mai -t "analyze code"  # Use MCP tools
mai -i image.png "describe this image"
echo "prompt" | mai    # Pipe input
```

### MCP Proxy
```bash
# Start proxy with multiple MCP servers
mai-wmcp "src/mcps/shell/mai-mcp-shell" "src/mcps/wttr/mai-mcp-wttr"

# List tools
mai-tool list

# Call tools
mai-tool call shell/run_command command="ls -la"
mai-tool call wttr/get_weather location="New York"
```

### Vector Database
```bash
# Search documents semantically
mai-vdb -s docs/ -n 5 "machine learning algorithms"
```

### HTTP API
```bash
# Start HTTP server
mai
/serve start

# Use OpenAI-compatible API
curl -X POST http://localhost:9000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "gemma3:1b", "messages": [{"role": "user", "content": "Hello!"}]}'
```

## Download

### Source Code
```bash
git clone https://github.com/trufae/mai.git
cd mai
```

### Releases
Download pre-built binaries from [GitHub Releases](https://github.com/trufae/mai/releases)

## Build from Source

### Prerequisites
* Go 1.21+
* Make

### Build All Components
```bash
make
```

### Build Specific Components
```bash
make -C src/repl     # Main REPL
make -C src/wmcp     # MCP proxy
make -C src/tool     # MCP client
make -C src/mcps     # All MCP servers
make -C src/vdb      # Vector database
make -C src/bot      # Bot integration
```

## Install

### System-wide Installation
```bash
make install
```

This creates symlinks in `/usr/local/bin`, no need to reinstall after recompiling.

### Uninstall
```bash
make uninstall
```

## Configuration

### Environment Variables
```bash
# Provider selection
MAI_PROVIDER=ollama|openai|claude|gemini|deepseek|mistral|bedrock

# API keys (provider-specific)
OPENAI_API_KEY=sk-...
CLAUDE_API_KEY=sk-ant-...
GEMINI_API_KEY=...
DEEPSEEK_API_KEY=...

# Local models
OLLAMA_MODEL=gemma3:1b

# Custom endpoints
MAI_BASEURL=https://api.example.com
MAI_USERAGENT=mai-repl/1.0
```

### REPL Configuration
```bash
mai
/set provider ollama
/set model gemma3:1b
/set listen 0.0.0.0:9000
```

## Author

pancake // Sergi Alvarez Capilla

## License

MIT
