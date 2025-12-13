# Using Claude with local mai server

Date: 2025-12-13T08:52:59.459Z

Point the Anthropic/Claude client at the mai repl server running on localhost and set the model to qwen3.

export ANTHROPIC_BASE_URL=http://localhost:9000
export ANTHROPIC_AUTH_TOKEN=${YOUR_MOONSHOT_API_KEY}
export ANTHROPIC_MODEL=qwen3
export ANTHROPIC_DEFAULT_OPUS_MODEL=qwen3
export ANTHROPIC_DEFAULT_SONNET_MODEL=qwen3
export ANTHROPIC_DEFAULT_HAIKU_MODEL=qwen3
export CLAUDE_CODE_SUBAGENT_MODEL=qwen3

# Then run the claude client
claude

# Notes
- If mai is listening on a different port or path, update ANTHROPIC_BASE_URL accordingly.
- Ensure the mai repl is started with the /serve command before running the client.
- This configuration lets the Claude client use qwen3 while routing requests through the local mai server.