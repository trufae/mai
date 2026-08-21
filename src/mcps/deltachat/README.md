# Delta Chat MCP Server

`mai-mcp-deltachat` exposes Delta Chat messaging and account management through
MCP. It uses `deltachat-rpc-server` over JSON-RPC on standard I/O, so the Go
binary has no CGO or third-party dependencies.

## Install and run

Install `deltachat-rpc-server` and ensure it is on `PATH`, then build and run:

```sh
make -C src/mcps/deltachat
src/mcps/deltachat/mai-mcp-deltachat
```

No arguments or environment variables are required. By default, accounts are
stored in the user configuration directory (`~/.config/mai/deltachat` on most
Unix systems) and all tools, including account management, are exposed.

The first account can be created by calling `deltachat_accounts` with
`{"action":"create"}`. This creates a chatmail account on
`nine.testrun.org`, selects it, and returns its address and secure invite link.

## Configuration

| CLI flag | Environment | Default |
|---|---|---|
| `-rpc-server PATH` | `DELTACHAT_RPC_SERVER` | Find `deltachat-rpc-server` on `PATH` |
| `-accounts-path PATH` | `DELTACHAT_ACCOUNTS_PATH` or `DC_ACCOUNTS_PATH` | User config directory under `mai/deltachat` |
| `-account-management=true/false` | `DELTACHAT_ACCOUNT_MANAGEMENT` | `true` |
| `-l ADDRESS` | `DELTACHAT_LISTEN` | MCP over stdin/stdout |

CLI flags override environment values. `~` is expanded in configured paths.
When an account path is explicitly configured, the directory is created with
private permissions and passed to the RPC process as `DC_ACCOUNTS_PATH`.

To expose messaging only:

```sh
mai-mcp-deltachat -account-management=false
```

or:

```sh
DELTACHAT_ACCOUNT_MANAGEMENT=false mai-mcp-deltachat
```

## Scoped tools

The MCP intentionally exposes a small surface: three messaging tools and, by
default, one account-management tool. Each tool uses an `action` parameter.

| Tool | Actions |
|---|---|
| `deltachat_contacts` | `search` lists or filters known contacts |
| `deltachat_chats` | `list`, `create`, `update`, `invite`, `join`, `leave`, `accept`, `clear` |
| `deltachat_messages` | `receive`, `read`, `send`, `reply`, `react`, `reactions` |
| `deltachat_accounts` | `list`, `create`, `switch`, `update`, `remove` |

Examples:

```json
{"action":"create","chat_type":"group","name":"Team","members":["alice@example.org",42]}
{"action":"create","chat_type":"channel","name":"Announcements","members":["alice@example.org"]}
{"action":"update","chat_id":123,"add_members":["bob@example.org"],"description":"Project coordination"}
{"action":"invite","chat_id":123}
{"action":"join","invite_link":"OPENPGP4FPR:..."}
{"action":"leave","chat_id":123,"confirm":true}
{"action":"send","recipient":"alice@example.org","text":"Hello"}
{"action":"reply","message_id":456,"text":"Got it"}
{"action":"react","message_id":456,"reactions":["👍"]}
{"action":"react","message_id":456,"reactions":[]}
```

`create` supports encrypted groups and outgoing broadcast channels. Group
members and channel recipients can be contact IDs, email addresses, or exact
contact names. Use `update` to rename a group/channel, change its description or
image, and add or remove members. Channels are controlled by their creator;
recipients receive a read-only conversation rather than explicitly joining it.

`reply` accepts text, a file, or both and derives the destination conversation
from `message_id`. `react` replaces the selected account's reaction; an empty
array clears it. `receive` fetches pending messages and, by default, accepts
contact requests and marks returned messages seen. `clear` permanently removes
a local conversation and `leave` leaves a group, so both require
`confirm: true`.

Every messaging tool accepts an optional `account_id`. If omitted, the selected
configured account is used; if none is selected, the first configured account is
selected automatically.

## Account management

The `deltachat_accounts` tool is enabled by default and omitted entirely in
messaging-only mode. Its actions are:

- `list`: list accounts, the active account, and account-store path.
- `create`: create a default chatmail account, choose another relay, or
  configure a conventional email account with `email` and `password`.
- `switch`: persist the account used by default for messaging.
- `update`: update the display name or profile image.
- `remove`: permanently remove an account; requires `confirm: true`.

## Library

The reusable package in `lib` can be imported as `deltachatmcp/lib`. It provides
the RPC process lifecycle, concurrent request correlation, raw `Call` access,
typed account/contact/chat/message APIs, account selection, recipient resolution,
file sending, group and channel management, membership changes, replies,
reactions, pending-message consumption, and secure joins.

The JSON-RPC transport was adapted from PicoClaw's MIT-licensed Delta Chat
channel implementation (Copyright 2026 PicoClaw contributors).
