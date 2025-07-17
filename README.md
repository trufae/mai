# ACLI

Artificial Command Line Intelligence

Set of commandline tools to use local or remote AI models with advanced customizable features to research, use MCP agents and much more.

--pancake

## Features

A REST API bridge for Model Context Protocol (MCP) servers written in Go. This service allows you to interact with multiple MCP servers through HTTP endpoints, converting JSONRPC calls to simple REST API calls.

## Features

- 🚀 **Multi-server support**: Run multiple MCP servers simultaneously
- 🔧 **Auto-discovery**: Automatically discovers and catalogs tools from each server
- 🌐 **REST API**: Simple HTTP endpoints for all MCP operations
- 📝 **Human-readable output**: Returns responses in markdown/plaintext format
- 🔄 **Flexible input**: Supports JSON, form data, and query parameters
- 🛡️ **Error handling**: Robust error handling and graceful shutdown

## Usage

### Starting the Service

```bash
# Start with multiple MCP servers
./mcpd "r2pm -r r2mcp" "servers/wttr/wttr"
```

Now you can curl localhost:8080 or use the commandline client

### Command Line Client

A CLI client is available in `clients/cli` to interact with the MCPD service:

```bash
# Build the CLI client
cd clients/mcpd-cli
go build -o mcpcli

# List all available tools
./mcpcli list

# Call a specific tool
./mcpcli call server1 mytool param1=value1

# Get JSON output
./mcpcli -j call server1 mytool param1=value1
```

See [clients/cli/README.md](clients/cli/README.md) for more information.

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

### List All Tools (JSON)
```bash
GET /tools/json
```
Returns a JSON-formatted list of all available tools from all servers.

### Service Status
```bash
GET /status
```
Shows the status of all running MCP servers.

### Call a Tool
```bash
GET /call/{server}/{tool}?param=value
POST /call/{server}/{tool}
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

### Form Data Examples

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

## License

MIT
