# Using Claude with local mai server

Date: 2025-12-13T08:52:59.459Z

Point the Anthropic/Claude client at the mai repl server running on localhost and set the model to devstral-2512.

export ANTHROPIC_BASE_URL=http://localhost:9000
export ANTHROPIC_AUTH_TOKEN=1234567890
export ANTHROPIC_MODEL=devstral-2512
export ANTHROPIC_DEFAULT_OPUS_MODEL=devstral-2512
export ANTHROPIC_DEFAULT_SONNET_MODEL=devstral-2512
export ANTHROPIC_DEFAULT_HAIKU_MODEL=devstral-2512
export CLAUDE_CODE_SUBAGENT_MODEL=devstral-2512

# Then run the claude client
claude

# Notes
- If mai is listening on a different port or path, update ANTHROPIC_BASE_URL accordingly.
- Ensure the mai repl is started with the /serve command before running the client.
- This configuration lets the Claude client use devstral-2512 while routing requests through the local mai server.
