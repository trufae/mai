# Terminal Multiplexer Protocol

The terminal multiplexer (`mai-term`) communicates over a Unix domain socket. This protocol allows external programs to interact with a spawned shell by sending input and receiving output.

## Overview

- The multiplexer spawns a command (default `$SHELL` or `/bin/bash`, or specified as second argument) with a pseudo-TTY (PTY) for full interactive support.
- Input to the command comes from the multiplexer's stdin and from connected clients via the socket.
- Output from the command (stdout and stderr are merged in the PTY) is forwarded to the multiplexer's stdout and to connected clients via the socket.

## Usage

```
mai-term <socket-path> [command]
```

- `<socket-path>`: Path to the Unix socket file.
- `[command]`: Optional command to run instead of the default shell. The command is executed via `/bin/sh -c`.

## Socket Usage

- **Socket Type**: Unix domain socket (stream-oriented).

## Protocol Details

### Sending Input to the Shell

Clients can send data to the shell's stdin by writing directly to the socket. The data is forwarded as-is to the shell.

### Receiving Output from the Command

Output from the command is sent to clients as framed messages. Since the command runs with a PTY, stdout and stderr are merged. Each message consists of:

1. **Type Byte** (1 byte):
   - `0x01`: output data (merged stdout and stderr)

2. **Length** (4 bytes, big-endian unsigned 32-bit integer): The length of the data in bytes.

3. **Data** (N bytes): The actual output data.

Clients must read the socket and parse these frames.

### Connection Handling

- Multiple clients can connect simultaneously.
- When a client sends data, it is written to the shell's stdin.
- Shell output is broadcast to all connected clients.
- If a client disconnects, it is removed from the broadcast list.

### Example

To send "ls\n" to the command:
- Client writes `ls\n` to the socket.

To receive output:
- Client reads from socket, expecting frames like:
  - `0x01 00 00 00 0A` followed by 10 bytes of output data.

## Notes

- The multiplexer forwards its own stdin to the command, making it transparent to the user.
- The command runs with a PTY, enabling full interactive support (e.g., for vim, less).
- Data is handled in chunks (up to 1024 bytes per read), so clients should handle partial frames if necessary, but since it's framed, they can reassemble.
- The socket file is removed on startup if it exists.