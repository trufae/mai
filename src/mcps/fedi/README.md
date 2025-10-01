# Fedi MCP (mai-mcp-fedi)

MCP server for interacting with Mastodon and other federated social networks.

## Environment Variables

- `MASTODON_INSTANCE`: Mastodon instance URL (default: mastodon.social)
- `MASTODON_API_KEY`: API key for authenticated operations (optional, read from ~/.r2ai.mastodon-key if not set)

## Tools

### search_posts
Search for posts on Mastodon. May require API key for full results.

```json
{
  "query": "artificial intelligence",
  "limit": 10
}
```

### post_message
Post a message to Mastodon. Requires MASTODON_API_KEY.

```json
{
  "content": "Hello from MAI!",
  "visibility": "public"
}
```

### get_timeline
Get posts from a Mastodon timeline.

```json
{
  "type": "public",
  "limit": 10
}
```

## Usage

```bash
# Build
make

# Run
./mai-mcp-fedi

# Or via wmcp
mai-wmcp -c config.json
```

Where config.json includes:
```json
{
  "mcpServers": {
    "fedi": {
      "type": "stdio",
      "command": "./mai-mcp-fedi",
      "args": []
    }
  }
}
```