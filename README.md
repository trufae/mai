<img width="300px" height="300px" align="left" style="float: left; margin: 0 10px 0 0;" alt="mailogo" src="https://raw.githubusercontent.com/trufae/mai/master/mai-logo.png?nocache">

### MAI

My Artificial Intelligence (🐱AI)

Set of commandline tools to use in batch or interactive mode against local and remote AI providers bringing powerful shell style oneliners in conjuntion of MCP agents.

[![CI](https://github.com/trufae/mai/actions/workflows/ci.yml/badge.svg)](https://github.com/trufae/mai/actions/workflows/ci.yml)

--pancake

## Tools

* **mai** the main commandline tool and repl shell
* **mai-wmcp** MCP web proxy exposing tools in json and markdown
* **mai-tool** execute and inspect MCP servers loaded in wmcp

## Features

- 🚀 **Multi-server support**: Run multiple MCP servers simultaneously
- 🔧 **Auto-discovery**: Automatically discovers and catalogs tools
- 🌐 **REST API**: Simple HTTP endpoints for all MCP operations
- 📝 **Human-readable output**: Returns responses in json/markdown format
- 🔄 **Flexible input**: Supports JSON, form data, and query parameters
- 🛡️ **Error handling**: Robust error handling and graceful shutdown

## Usage

Type `mai` to access the REPL. Then, enter `/help` and press `<tab>` to view all available commands.

* 🔄 Substitute LLM expressions by using inline backticks.
* 💻 Use `$()` for command shell substitution.
* 🌐 Insert environment variables using `${}`.
* 📁 Load custom prompts with the `#` symbol.
* 📝 Apply querying templates with `$`.
* 🚀 Enable batch mode through vim-like `%!mai`.
* 🖼️ Upload images using `-i` or `/image`.
* 🔧 Access tools through `mai-wmcp` with `-t` flag.
* ⚙️ Fully configure options using `/set`.
* 📊 Choose any model from any provider.
* 🎉 And so much more to explore!

### MCP Proxy Server

Start multiple MCP servers in a single line of shell.

1. Start each MCP server as a subprocess
2. Perform the MCP handshake with each server via stdio
3. Discover available tools from each server
4. Start the HTTP server on port 8080 (or $PORT environment variable)

```bash
./mai-wmcp "r2pm -r r2mcp" "src/mcps/wttr/mai-mcp-wttr"
```

Claude/VScode config files are supported, and use any MCP with **Mai**.

* Curl `localhost:8080` or use the `mai-tool` client for quiet, json or markdown output.

```bash
# List all available tools
mai-tool list

# Call a specific tool
mai-tool call server1/mytool param1=value1

# Get JSON output
mai-tool -j call server1/mytool param1=value1
```

## Building

Written in **Go** and orchestrated with **Makefiles**:

```bash
make
make install
```

Right now that will create symlinks, so there's no need to install everytime you recompile.

## Author

pancake // Sergi Alvarez Capilla

## License

MIT
