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

The first account can be created by calling `create_account` with `{}`. This
creates a chatmail account on `nine.testrun.org`, selects it, and returns its
address and secure invite link.

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

## Messaging tools

- `search_contacts`: search contacts, or list them with no arguments.
- `list_conversations`: list or search conversations.
- `read_conversation`: read recent messages and attachment paths.
- `receive_pending_messages`: fetch pending messages; by default it accepts
  contact requests and marks returned messages seen.
- `send_message`: send text to a chat ID, email, contact, or conversation name.
- `send_attachment`: send a local file with an optional caption.
- `accept_chat`: accept a contact request.
- `clear_chat`: permanently clear a conversation; requires `confirm: true`.
- `get_invite_link`: get an account or group secure-join link.
- `join_invite`: join a contact or group from a secure-join link.

Every messaging tool accepts an optional `account_id`. If omitted, the selected
configured account is used; if none is selected, the first configured account is
selected automatically.

## Account-management tools

These tools are enabled by default and omitted entirely in messaging-only mode:

- `list_accounts`: list accounts, the active account, and account-store path.
- `create_account`: create a default chatmail account, choose another relay, or
  configure a conventional email account with `email` and `password`.
- `switch_account`: persist the account used by default for messaging.
- `set_account_profile`: update the display name or profile image.
- `remove_account`: permanently remove an account; requires `confirm: true`.

## Library

The reusable package in `lib` can be imported as `deltachatmcp/lib`. It provides
the RPC process lifecycle, concurrent request correlation, raw `Call` access,
typed account/contact/chat/message APIs, account selection, recipient resolution,
file sending, pending-message consumption, and secure joins.

The JSON-RPC transport was adapted from PicoClaw's MIT-licensed Delta Chat
channel implementation (Copyright 2026 PicoClaw contributors).
