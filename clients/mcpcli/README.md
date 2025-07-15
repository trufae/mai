# MCPD Command Line Client

A command-line interface for interacting with the MCPD REST service.

## Features

- List available servers
- List available tools
- Call tools with parameters
- Output in Markdown (default) or JSON format

## Building

```bash
cd clients/cli
go build -o mcpcli
```

## Usage

```
mcpcli [options] <command>

Options:
  -h <host>  Host where mcpd is running (default: localhost)
  -p <port>  Port where mcpd is running (default: 8080)
  -j         Output in JSON format
  -m         Wrap markdown output in code blocks

Commands:
  list                           List all available tools
  servers                        List all available servers
  call <server> <tool> [params]  Call a specific tool

Examples:
  mcpcli list
  mcpcli -j list
  mcpcli call server1 mytool param1=value1 param2=value2
  mcpcli call server1 mytool "text=value with spaces"
```

## Example Commands

### List all servers

```bash
./mcpcli servers
```

### List all tools

```bash
./mcpcli list
```

### List tools in JSON format

```bash
./mcpcli -j list
```

### Call a tool with parameters

```bash
./mcpcli call server1 mytool param1=value1 param2=value2
```

### Call a tool and get JSON output

```bash
./mcpcli -j call server1 mytool param1=value1
```