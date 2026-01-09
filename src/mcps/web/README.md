# WebSearch MCP

A Model Context Protocol (MCP) server that provides web search capabilities using various search providers.

## Features

- **Extensible Design**: Easy to add new search providers through the `SearchProvider` interface
- **Multiple Providers**: Supports Ollama, DuckDuckGo, and Wikipedia search APIs
- **Flexible Search Modes**: Search with first working provider (fast) or all enabled providers (comprehensive)
- **MCP Compatible**: Full MCP server implementation using the shared mcplib

## Supported Providers

### Ollama Web Search
- Uses Ollama's official web search API
- Requires `OLLAMA_API_KEY` environment variable
- Endpoint: `https://ollama.com/api/web_search`

### DuckDuckGo Search
- Uses DuckDuckGo's instant answers API
- No API key required
- Returns abstract text and search results when available

### Wikipedia Search
- Uses Wikipedia's search API
- No API key required
- Returns search results from Wikipedia articles

## Usage

### Building

```bash
make
```

### Running

```bash
# Set your API key (only needed for Ollama)
export OLLAMA_API_KEY=your_api_key_here

# Run the MCP server with enabled providers
./websearch -ollama -duckduckgo -wikipedia

# Or just enable specific providers
./websearch -duckduckgo -wikipedia

# Search with all enabled providers (comprehensive results)
./websearch -all-providers -duckduckgo -wikipedia

# Search with first working provider only (faster)
./websearch -duckduckgo -wikipedia
```

### MCP Tool

The server provides a `WebSearch` tool that can be called with:

```json
{
  "query": "what is ollama?",
  "provider": "duckduckgo"  // optional, defaults to first enabled provider
}
```

Available providers: `ollama`, `duckduckgo`, `wikipedia`

**Search Behavior:**
- When `-all-providers` flag is used: Returns aggregated results from all enabled providers
- When `-all-providers` flag is not used: Returns results from the first working provider (faster)
- When a specific `provider` is requested: Uses only that provider regardless of the flag

## Adding New Providers

To add a new search provider:

1. Implement the `SearchProvider` interface:

```go
type SearchProvider interface {
    Search(query string) (*SearchResult, error)
    Name() string
}
```

2. Register it in `NewWebSearchService()`:

```go
service.providers["newprovider"] = &NewProvider{}
```

## Response Format

The search results are returned in the following format:

```json
{
  "query": "what is ollama?",
  "results": [
    {
      "title": "Result Title",
      "url": "https://example.com",
      "content": "Result content snippet..."
    }
  ]
}
```

## Environment Variables

- `OLLAMA_API_KEY`: Required for Ollama web search provider