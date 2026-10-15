# Apple Mail MCP

A Go server with five tools and compact Markdown responses. Searches read Mail's
local SQLite Envelope Index instead of enumerating messages through AppleScript.
They work with Mail closed and never fetch message bodies or change read status.
Gmail labels are included, so INBOX works even when messages live in All Mail.

No Python, Go dependencies outside this repository, CGO, or installation step.
The runtime uses macOS's `/usr/bin/sqlite3` (JSON output support required) and
`/usr/bin/osascript` for targeted automation. Mail's database is opened read-only
with normal WAL/locking semantics; it is never modified, copied, or treated as
immutable. Mail's internal index/cache formats are not a public Apple API; an
unsupported schema produces an error rather than falling back to a mailbox scan.

## Build and configure

```sh
make -C src/mcps/applemail
src/mcps/applemail/mai-mcp-applemail -T 'search_emails limit=6'
```

Example MCP configuration (adjust the absolute path):

```json
{
  "mcpServers": {
    "applemail": {
      "command": "/Users/pancake/prg/mai/src/mcps/applemail/mai-mcp-applemail",
      "args": []
    }
  }
}
```

Grant the terminal/MCP host **Full Disk Access** in System Settings → Privacy &
Security and restart it if access to `~/Library/Mail` is denied. Targeted Mail
automation also needs permission under **Automation → Mail**. Index-only searches
and cached reads do not need Automation permission.

Flags:

- `-read-only`: expose only the three reading tools.
- `-timeout 10s`: total deadline per call, including lock waits (maximum 1m).
- `-db PATH`: override automatic discovery of the newest `V*/MailData/Envelope Index`.
- `-T 'TOOL key=value ...'`: direct command mode; multiple commands use `;`.
- `-l 127.0.0.1:9000`: optional TCP transport; HTTP/SSE listen URLs also work.

The normal repository build/install includes `mai-mcp-applemail`.

## Tools

| Tool | Purpose |
|---|---|
| `mailboxes` | Mailbox IDs, full paths, account UUIDs and Mail's cached counts. Optional `query`, `account`, `limit` (100, max 500). |
| `search_emails` | Recent mail or literal substring search. Default `mailbox="INBOX"`, `limit=10` (max 50). |
| `read_email` | One local message ID; plain-text MIME extraction, attachment names, Mail link. `max_chars=6000` (max 20000). |
| `update_email` | Up to 500 messages: `read`, `unread`, `flag`, `unflag`, `move` (requires `mailbox_id`) or `trash`. Supply `id`, `ids`, or both. |
| `compose_email` | Plain-text draft by default; `send=true` submits to Mail. New mail requires `to`, `subject`, `body`. A threaded reply uses `reply_id`, `body`, optional `reply_all`. |

Search filters combine with AND: `query`, `subject`, `sender`, `recipient`,
`date_from`, `date_to`, `unread`, `flagged`, `account`, and `mailbox`. Dates are
inclusive `YYYY-MM-DD` in the server's local timezone. `query` matches subject,
sender or cached preview; it **does not search full message bodies**. SQLite's
standard LIKE matching folds ASCII case; non-ASCII letters retain their case.
`preview=true` adds cached snippets of at most 240 characters without downloading
bodies. `%` and `_` are literal characters, not wildcards.

Use `mailboxes` to discover account UUIDs and exact mailbox paths or IDs.
`mailbox="All"` searches every folder, including Trash and Junk. Results show the
physical mailbox; a Gmail INBOX result may therefore show `[Gmail]/All Mail`.
Search returns at most `limit` rows plus a `Next cursor` when another page exists.
Copy it to `cursor` with the same filters. Ordering is by received time then local
ID, so equal timestamps do not repeat or skip rows. No full-result count is run.
Changes to mailbox membership during pagination can still change later pages.

```sh
mai-mcp-applemail -T 'mailboxes query=INBOX'
mai-mcp-applemail -T 'search_emails sender=example.org limit=6 preview=true'
mai-mcp-applemail -T 'search_emails mailbox=All subject="invoice" date_from=2026-01-01'
mai-mcp-applemail -T 'read_email id=12345 max_chars=4000'
mai-mcp-applemail -T 'update_email action=trash ids=[12345,12346,12347]'
mai-mcp-applemail -T 'compose_email to=person@example.org subject="Hello" body="Draft text"'
```

Batch updates use one batch message lookup and one Mail script, including across
accounts and mailboxes. Repeated IDs are processed once. Successful IDs are listed compactly;
missing messages and other failures are reported individually. A message failure
does not stop the remaining batch, except an Apple-event timeout stops further
attempts. Partial failures produce an MCP tool error containing the outcome report.
A hard process deadline can obscure completed actions: those outcomes are marked
unknown and must be checked in Mail before retrying. The whole batch shares the
normal call deadline; use `-timeout 60s` when processing larger batches.

Cached `.emlx` files are read directly using bounded MIME parsing (16 MiB,
12 nesting levels); multipart alternatives prefer plain text, HTML is stripped,
and attachments are listed without loading their contents into the response.
UTF-8, ASCII, Latin-1 and Windows-1252 are supported locally. An uncached message,
unfamiliar cache layout, oversized message or unsupported charset falls back to
one timed, direct-ID Mail read. It may require Mail to download the body. Encrypted
or signed content receives only the text available locally/to Mail.

All AppleScript is serialized within the process and across cooperating instances
using the same advisory lock as `../apple-mail-mcp`. Search never waits on that
lock. Scripts also carry an Apple-event timeout; subprocesses are killed and
reaped on deadlines or server shutdown. Mail's index may briefly lag mutations.
Draft/send errors can have an uncertain outcome: inspect Drafts/Sent before
retrying. Sending reports submission to Mail, not delivery. Trash uses Mail's
normal delete action; there is no permanent erase tool. Attachment download,
attachment sending, mailbox administration and analytics are outside this small
interface.

Run `go test ./...` in this directory. Tests use a synthetic 100,000-message index,
exercise label search/pagination, literal queries, deadlines and MIME, and compile
generated AppleScript on macOS. They never execute updates, drafts, or sends.
