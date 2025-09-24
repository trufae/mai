# MAI Bot

Go-based automation bot that connects to Telegram and IRC, executes configured shell commands, and relays the results back to chat users.

## Features

- Telegram and IRC front-ends with shared command execution pipeline
- Configurable program invocation via command-line arguments or stdin
- Optional stderr capture, output splitting, and message length limits per platform
- Structured JSON logging to file or stdout

## Prerequisites

- Go 1.21 or later
- A Telegram bot token from [@BotFather](https://t.me/botfather) when using the Telegram backend
- IRC server credentials when using the IRC backend

## Installation

1. Ensure dependencies are available:
   ```bash
   go mod tidy
   ```
2. Build the bot binary:
   ```bash
   make
   ```

## Configuration

Edit `config.json` to match your environment:

```json
{
  "token": "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
  "program": ["/usr/bin/printf", "User input: %s", "{input}"],
  "input_method": "argument",
  "split_messages": true,
  "log_to_stdout": true,
  "irc_server": "irc.example.net",
  "irc_channel": "#mai"
}
```

### Core Fields

- `program`: Executable followed by static arguments; use `{input}` as an inline placeholder.
- `input_method`: `"argument"` (default) appends or replaces `{input}`; `"stdin"` pipes chat text to the process.
- `capture_stderr`: When true, stderr is merged into the response.
- `max_length` / `split_messages`: Control trimming/splitting behaviour for Telegram messages; IRC uses `irc_max_length`.
- `logfile` / `log_to_stdout`: Enable structured JSON logging of every query/response pair.

### Telegram

- `token`: Bot token from BotFather. Leave empty to disable Telegram.
- `max_length`, `split_messages`: Telegram-specific output controls.

### IRC

Provide these to enable IRC:
- `irc_server`, `irc_channel`: Server address and target channel.
- `irc_port` (optional, default 6667), `irc_tls` (true to enable TLS).
- `irc_nick`, `irc_user`, `irc_realname`, `irc_password`: Identity configuration; defaults fall back to the nickname or `mai`.
- `irc_max_length`: Per-line split length (default 400 characters).

## Running

Execute the built binary or run the package directly:

```bash
./mai-bot
# or
GOFLAGS=-buildvcs=false go run .
```

Use `-c debug=true` when invoking command backends that support verbose output (see project instructions).

## Interaction

### Telegram

- Private chats: the full message body is passed to the configured program.
- Groups/channels: prefix the message with the bot handle, e.g. `@MaiBot uptime`.

### IRC

- Direct messages to the bot nickname run commands immediately.
- In-channel commands require mentioning the nickname (`mai-bot: uptime`).
- Replies are chunked automatically using `irc_max_length`.

## Security Notes

- The bot executes arbitrary commands as the running user—restrict access accordingly.
- Validate input or sandbox commands before exposing the bot publicly.
- Use rate limiting or auth layers in production deployments.

## Logging

When logging is enabled, each interaction is stored as a single-line JSON object containing user metadata, command text, response body, and exit code. This works identically across Telegram and IRC backends.
