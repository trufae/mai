# MAI Tool

A command-line interface for interacting with the MAI MCPD REST service.

## Features

- List available servers
- List available tools, resources, and prompts
- Call tools with parameters
- Read resources by URI
- Render prompts with parameters
- Output in Markdown (default), JSON, or XML format

## Building

```bash
make
```

## Usage

```
mai-tool [options] <command>

Options:
  -b <url>      Base URL where mcpd is running (default: http://localhost:8989)
                Can also be set with MAI_TOOL_BASEURL environment variable
  -j            Output in JSON format
  -x            Output in XML format
  -m            Wrap markdown output in code blocks
  -q            Suppress non-essential output
  -s            Use simple output format (for small models)
  -d            Enable debug mode to show HTTP requests and JSON payloads
  -h            Show help message

Commands:
  list                           List all available tools
  servers                        List all available servers
  call <server> <tool> [params]  Call a specific tool
  prompts [list]                 List all available prompts
  prompts get <server>/<name>    Render a prompt (accepts params)
  resources [list]               List all available resources
  resources read <server>/<uri>  Read a resource by URI

Note: When parameters are provided, the client sends them as a JSON POST request, enabling multiline values and special characters without URL-encoding.

Examples:
  mai-tool list
  mai-tool -j list
  mai-tool call server1 mytool param1=value1 param2=value2
  mai-tool call server1 mytool "text=value with spaces"
  mai-tool servers
  mai-tool prompts list
  mai-tool prompts get server1/prompt1 param1=value1
  mai-tool resources list
  mai-tool resources read server1/file.txt
```

## Example Commands

### List all servers

```bash
./mai-tool servers
```

### List all tools

```bash
./mai-tool list
```

### List tools in JSON format

```bash
./mai-tool -j list
```

### Call a tool with parameters

```bash
./mai-tool call server1 mytool param1=value1 param2=value2
```

### Call a tool and get JSON output

```bash
./mai-tool -j call server1 mytool param1=value1
```

### List prompts

```bash
./mai-tool prompts list
```

### Render a prompt with parameters

```bash
./mai-tool prompts get server1/my-prompt topic="artificial intelligence"
```

### List resources

```bash
./mai-tool resources list
```

### Read a resource

```bash
./mai-tool resources read server1/document.txt
```
