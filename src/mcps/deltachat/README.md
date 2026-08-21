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
| `deltachat_contacts` | `search`, `info` |
| `deltachat_chats` | `list`, `create`, `update`, `members`, `mute`, `invite`, `join`, `leave`, `accept`, `clear` |
| `deltachat_messages` | `receive`, `read`, `search`, `send`, `reply`, `edit`, `delete`, `forward`, `download`, `react`, `reactions`, `receipts`, `info` |
| `deltachat_accounts` | `list`, `create`, `switch`, `update`, `remove` |

Examples:

```json
{"action":"create","chat_type":"group","name":"Team","members":["alice@example.org",42]}
{"action":"create","chat_type":"channel","name":"Announcements","members":["alice@example.org"]}
{"action":"update","chat_id":123,"add_members":["bob@example.org"],"description":"Project coordination"}
{"action":"members","chat_id":123}
{"action":"mute","chat_id":123,"muted":true,"mute_seconds":3600}
{"action":"invite","chat_id":123}
{"action":"join","invite_link":"OPENPGP4FPR:..."}
{"action":"leave","chat_id":123,"confirm":true}
{"action":"send","recipient":"alice@example.org","text":"Hello"}
{"action":"reply","message_id":456,"text":"Got it"}
{"action":"search","query":"release plan","chat_id":123}
{"action":"edit","message_id":456,"text":"Updated text"}
{"action":"forward","message_ids":[456,457],"recipient":"Team"}
{"action":"delete","message_id":456,"delete_for_all":true,"confirm":true}
{"action":"download","message_id":458}
{"action":"react","message_id":456,"reactions":["👍"]}
{"action":"react","message_id":456,"reactions":[]}
{"action":"receipts","message_id":456}
{"action":"info","message_id":456}
{"action":"info","contact":"alice@example.org"}
```

`create` supports encrypted groups and outgoing broadcast channels. Group
members and channel recipients can be contact IDs, email addresses, or exact
contact names. Use `update` to rename a group/channel, change its description or
image, and add or remove members. Channels are controlled by their creator;
recipients receive a read-only conversation rather than explicitly joining it.
`members` returns full contact records for group members or channel recipients.
`mute` accepts `muted: false` to unmute, or `muted: true` with an optional
`mute_seconds`; omitting the duration mutes forever.

`reply` accepts text, a file, or both and derives the destination conversation
from `message_id`. `react` replaces the selected account's reaction; an empty
array clears it. `receive` fetches pending messages and, by default, accepts
contact requests and marks returned messages seen. `receipts` returns read
receipts with the associated contacts, while `info` includes quote, edit,
reaction, attachment-download, transport, and expiry details.

Message deletion accepts either `message_id` or `message_ids`. It always
requires `confirm: true`; `delete_for_all: true` additionally requests deletion
for every chat member. Attachment downloads run asynchronously. The `download`
result reports the current `downloadState` and local file path when available;
call `info` later to check completion. `clear` permanently removes a local
conversation and `leave` leaves a group, so both also require `confirm: true`.

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
reactions, search, edits, deletion, forwarding, attachment downloads, read
receipts, mute state, detailed contact/message information, pending-message
consumption, and secure joins.

The JSON-RPC transport was adapted from PicoClaw's MIT-licensed Delta Chat
channel implementation (Copyright 2026 PicoClaw contributors).
