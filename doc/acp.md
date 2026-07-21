# ACP Sub-Agents

The Agent Client Protocol (https://agentclientprotocol.com) is a JSON-RPC
over stdio protocol that connects editors with coding agents. mai speaks
it in both directions:

- **mai as agent**: the `mai-acp` binary exposes mai to any ACP client
  (Zed, JetBrains, ...).
- **mai as client**: the `/acp` command in the REPL launches external
  agents (gemini, claude, codex, qwen, opencode, ...) as sub-agents,
  without needing to implement each vendor's protocol.

## Usage

```
/acp list                 # list known agents and their availability
/acp info <agent>         # details and install instructions
/acp run <agent> <prompt> # run a prompt and wait for the reply
/acp bg <agent> <prompt>  # run in background (parallel job)
/acp jobs                 # list jobs and their status
/acp output <id>          # show the (partial) output of a job
/acp wait [id ...]        # wait for jobs and collect their results
/acp kill <id|all>        # cancel running job(s)
/acp edit                 # edit user-defined agents
```

Example, fan out work to several agents and collect the results:

```
>>> /acp bg gemini summarize the README
>>> /acp bg codex review the last commit
>>> /acp wait
```

`/acp <agent> <prompt>` is a shortcut for `/acp run`.

## Agents

The builtin catalog follows the official ACP registry
(https://github.com/agentclientprotocol/registry). Native ACP agents
include gemini, qwen, opencode, reasonix (deepseek), goose, kimi,
cursor, copilot, cline, kilo, vibe (mistral), auggie, stakpak, vtcode
and crow. Claude Code and
Codex do not speak ACP natively; they are driven through adapters:

```
npm install -g @agentclientprotocol/claude-agent-acp   # claude
npm install -g @agentclientprotocol/codex-acp          # codex
```

`/acp list` marks each agent as installed (✅), runnable through npx
(📦) or not installed (❌). Agents must be authenticated with their own
CLI once before they can be used as sub-agents.

Custom agents can be defined in `~/.config/mai/acp.json` (`/acp edit`):

```json
{
  "agents": {
    "myagent": {
      "description": "My custom ACP agent",
      "command": "myagent",
      "args": ["--acp"],
      "env": {"KEY": "value"}
    }
  }
}
```

Entries with the same name override the builtin catalog.

## Configuration

- `acp.permission`: how to answer agent permission requests: `allow`,
  `reject` or `auto` (default: allow read-only tools, reject the rest).
- `acp.timeout`: seconds to wait for a prompt (default 600, 0 = none).
- `acp.debug`: log the JSON-RPC traffic to stderr.
