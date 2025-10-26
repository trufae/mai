# MCP Proxy

## Environment Variables

- `PORT`: HTTP server port (default: 8989)
- `MAI_MCP_AUTH_<DOMAIN>`: Bearer token for HTTP MCP servers (domain sanitized, e.g., MAI_MCP_AUTH_API_EXAMPLE_COM)

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

### List All Prompts
```bash
GET /prompts
```
Returns a markdown-formatted list of all available prompts from all servers.

### List All Prompts (JSON)
```bash
GET /prompts/json
```
Returns a JSON-formatted list of all available prompts from all servers.

### Service Status
```bash
GET /status
```
Shows the status of all running MCP servers.

### Call a Tool
```bash
GET /call/{server}/{tool}?param=value
GET /call/{tool}?param=value
POST /call/{server}/{tool}
POST /call/{tool}
```
Calls a specific tool on a specific server, or uses auto-discovery when only the tool is specified.

### Get a Prompt
```bash
GET /prompts/{server}/{prompt}?arg=value
GET /prompts/{prompt}?arg=value
POST /prompts/{server}/{prompt}
POST /prompts/{prompt}
```
Retrieves a prompt’s rendered messages from a specific server, or uses auto-discovery when only the prompt name is specified. Arguments can be passed via query string or JSON body.

## HTTP MCP Servers

The proxy supports connecting to remote MCP servers via HTTP/HTTPS. To use an HTTP server, specify the URL as the server argument:

```bash
mai-wmcp "https://api.example.com/mcp"
```

Authentication is handled via bearer tokens from environment variables. The variable name is constructed as `MAI_MCP_AUTH_<DOMAIN>`, where the domain is sanitized by replacing dots and hyphens with underscores and converting to uppercase.

For example, for `https://api.example.com/mcp`, set:

```bash
export MAI_MCP_AUTH_API_EXAMPLE_COM="your-bearer-token"
```

## Configuration File

You can also configure servers in a JSON config file:

```json
{
  "mcpServers": {
    "local-r2": {
      "type": "stdio",
      "command": "r2pm -r r2mcp"
    },
    "remote-api": {
      "type": "http",
      "url": "https://api.example.com/mcp"
    }
  }
}
```

## Examples

### 1. Discover Available Tools

```bash
# List all tools and their descriptions
curl http://localhost:8989/tools

# Check service status
curl http://localhost:8989/status

# List all prompts
curl http://localhost:8989/prompts

# Get a specific prompt
curl "http://localhost:8989/prompts/server1/welcome?topic=onboarding"
```

### 2. Radare2 MCP Examples (r2mcp)

Assuming you have r2mcp running as one of your servers:

```bash
# Open a binary file for analysis
curl -X POST "http://localhost:8989/tools/server1/openFile" \
  -H "Content-Type: application/json" \
  -d '{"filePath": "/path/to/binary"}'

# List all functions in the binary
curl "http://localhost:8989/tools/server1/listFunctions"

# Disassemble a function at a specific address
curl "http://localhost:8989/tools/server1/disassembleFunction?address=0x1000"

# Decompile a function
curl "http://localhost:8989/tools/server1/decompileFunction?address=0x1000"

# Search for strings in the binary
curl "http://localhost:8989/tools/server1/listStrings?regexpFilter=password"

# Get cross-references to an address
curl "http://localhost:8989/tools/server1/xrefsTo?address=0x1000"

# Add a comment to an address
curl -X POST "http://localhost:8989/tools/server1/setComment" \
  -H "Content-Type: application/json" \
  -d '{"address": "0x1000", "message": "This is the main function"}'
```

### Form Data Examples

You can also use form data instead of JSON:

```bash
# Using form data
curl -X POST "http://localhost:8989/tools/server1/disassemble" \
  -d "address=0x1000" \
  -d "numInstructions=20"

# Using query parameters
curl "http://localhost:8989/tools/server1/analyze?level=2&verbose=true"
```

### 6. Complex JSON Payloads

```bash
# Search with complex criteria
curl -X POST "http://localhost:8989/tools/server1/search" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "main",
    "case_sensitive": false,
    "max_results": 10,
    "file_types": ["c", "cpp", "h"]
  }'

# Batch operations
curl -X POST "http://localhost:8989/tools/server1/batchAnalyze" \
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
curl "http://localhost:8989/tools/nonexistent/tool"
# HTTP 404: Server 'nonexistent' not found

# Tool not found
curl "http://localhost:8989/tools/server1/nonexistent"
# HTTP 400: Tool call failed: method not found

# Invalid parameters
curl -X POST "http://localhost:8989/tools/server1/openFile" \
  -H "Content-Type: application/json" \
  -d '{"invalid": "parameter"}'
# HTTP 400: Tool call failed: missing required parameter 'filePath'
```
