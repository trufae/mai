# MCP REST Bridge

A REST API bridge for Model Context Protocol (MCP) servers written in Go. This service allows you to interact with multiple MCP servers through HTTP endpoints, converting JSONRPC calls to simple REST API calls.

## Features

- 🚀 **Multi-server support**: Run multiple MCP servers simultaneously
- 🔧 **Auto-discovery**: Automatically discovers and catalogs tools from each server
- 🌐 **REST API**: Simple HTTP endpoints for all MCP operations
- 📝 **Human-readable output**: Returns responses in markdown/plaintext format
- 🔄 **Flexible input**: Supports JSON, form data, and query parameters
- 🛡️ **Error handling**: Robust error handling and graceful shutdown

## Installation

```bash
# Clone or download the source code
git clone <repository-url>
cd mcp-rest-bridge

# Install dependencies
make deps

# Build the binary
make build
```

## Usage

### Starting the Service

```bash
# Start with multiple MCP servers
./mcpd "r2pm -r r2mcp" "timemcp" "weather-mcp"

# Or run directly with go
go run main.go "server-command-1" "server-command-2"
```

The service will:
1. Start each MCP server as a subprocess
2. Perform the MCP handshake with each server
3. Discover available tools from each server
4. Start the HTTP server on port 8080 (or $PORT environment variable)

### Environment Variables

- `PORT`: HTTP server port (default: 8080)

## API Endpoints

### List All Tools
```bash
GET /tools
```
Returns a markdown-formatted list of all available tools from all servers.

### Service Status
```bash
GET /status
```
Shows the status of all running MCP servers.

### Call a Tool
```bash
GET /tools/{server}/{tool}?param=value
POST /tools/{server}/{tool}
```
Calls a specific tool on a specific server.

## Examples

### 1. Discover Available Tools

```bash
# List all tools and their descriptions
curl http://localhost:8080/tools

# Check service status
curl http://localhost:8080/status
```

### 2. Radare2 MCP Examples (r2mcp)

Assuming you have r2mcp running as one of your servers:

```bash
# Open a binary file for analysis
curl -X POST "http://localhost:8080/tools/server1/openFile" \
  -H "Content-Type: application/json" \
  -d '{"filePath": "/path/to/binary"}'

# List all functions in the binary
curl "http://localhost:8080/tools/server1/listFunctions"

# Disassemble a function at a specific address
curl "http://localhost:8080/tools/server1/disassembleFunction?address=0x1000"

# Decompile a function
curl "http://localhost:8080/tools/server1/decompileFunction?address=0x1000"

# Search for strings in the binary
curl "http://localhost:8080/tools/server1/listStrings?regexpFilter=password"

# Get cross-references to an address
curl "http://localhost:8080/tools/server1/xrefsTo?address=0x1000"

# Add a comment to an address
curl -X POST "http://localhost:8080/tools/server1/setComment" \
  -H "Content-Type: application/json" \
  -d '{"address": "0x1000", "message": "This is the main function"}'
```

### 3. Time MCP Examples (timemcp)

```bash
# Get current time
curl "http://localhost:8080/tools/server2/getCurrentTime"

# Get time in specific timezone
curl "http://localhost:8080/tools/server2/getTimeInTimezone?timezone=America/New_York"

# Format timestamp
curl -X POST "http://localhost:8080/tools/server2/formatTime" \
  -H "Content-Type: application/json" \
  -d '{"timestamp": 1640995200, "format": "2006-01-02 15:04:05"}'
```

### 4. Weather MCP Examples

```bash
# Get current weather
curl "http://localhost:8080/tools/server3/getCurrentWeather?location=New York"

# Get weather forecast
curl "http://localhost:8080/tools/server3/getForecast?location=London&days=5"

# Get weather with specific units
curl -X POST "http://localhost:8080/tools/server3/getWeather" \
  -H "Content-Type: application/json" \
  -d '{"location": "Tokyo", "units": "metric"}'
```

### 5. Form Data Examples

You can also use form data instead of JSON:

```bash
# Using form data
curl -X POST "http://localhost:8080/tools/server1/disassemble" \
  -d "address=0x1000" \
  -d "numInstructions=20"

# Using query parameters
curl "http://localhost:8080/tools/server1/analyze?level=2&verbose=true"
```

### 6. Complex JSON Payloads

```bash
# Search with complex criteria
curl -X POST "http://localhost:8080/tools/server1/search" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "main",
    "case_sensitive": false,
    "max_results": 10,
    "file_types": ["c", "cpp", "h"]
  }'

# Batch operations
curl -X POST "http://localhost:8080/tools/server1/batchAnalyze" \
  -H "Content-Type: application/json" \
  -d '{
    "files": ["/path/to/file1", "/path/to/file2"],
    "options": {
      "deep_analysis": true,
      "extract_strings": true
    }
  }'
```

## Response Format

All responses are returned in plaintext/markdown format for easy reading:

```
# Analysis Results

## Function: main
Address: 0x1000
Size: 156 bytes

### Assembly Code
```
0x1000  push rbp
0x1001  mov rbp, rsp
0x1004  sub rsp, 0x20
...
```

### Cross References
- Called from: 0x2000 (entry point)
- References: 0x3000 (printf)
```

## Error Handling

The service provides clear error messages:

```bash
# Server not found
curl "http://localhost:8080/tools/nonexistent/tool"
# HTTP 404: Server 'nonexistent' not found

# Tool not found
curl "http://localhost:8080/tools/server1/nonexistent"
# HTTP 400: Tool call failed: method not found

# Invalid parameters
curl -X POST "http://localhost:8080/tools/server1/openFile" \
  -H "Content-Type: application/json" \
  -d '{"invalid": "parameter"}'
# HTTP 400: Tool call failed: missing required parameter 'filePath'
```

## Development

### Building

```bash
# Install dependencies
make deps

# Build binary
make build

# Run tests
make test

# Clean build artifacts
make clean
```

### Adding New MCP Servers

Simply add the server command to the command line arguments:

```bash
./mcpd "r2pm -r r2mcp" "timemcp" "your-new-mcp-server --args"
```

The service will automatically:
1. Start the server process
2. Perform the MCP handshake
3. Discover available tools
4. Expose them via REST API

## Troubleshooting

### Server Won't Start

```bash
# Check if the MCP server binary exists and is executable
which r2mcp
ls -la $(which r2mcp)

# Check server logs (stderr is captured)
curl http://localhost:8080/status
```

### Tool Call Fails

```bash
# Check tool requirements by listing tools first
curl http://localhost:8080/tools

# Verify JSON format
echo '{"param": "value"}' | jq .

# Check parameter types (strings vs numbers vs booleans)
curl -X POST "http://localhost:8080/tools/server1/tool" \
  -H "Content-Type: application/json" \
  -d '{"count": 10}' # number, not "10"
```

### Connection Issues

```bash
# Check if service is running
curl http://localhost:8080/

# Check specific server status
curl http://localhost:8080/status

# Test with verbose output
curl -v http://localhost:8080/tools
```

## License

[Add your license information here]

## Contributing

[Add contribution guidelines here]
