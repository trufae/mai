# Memory MCP Server

A Model Context Protocol (MCP) server that provides tools for managing notes with tags and searching capabilities.

## Features

- **Add Notes**: Create new notes with title, content, and optional tags
- **Update Notes**: Modify existing notes by ID
- **Delete Notes**: Remove notes by ID
- **Search Notes**: Find notes by keywords in title, content, or tags with relevance scoring

## Database

- Default storage: `$TMPDIR/mai_memory.json` (or system temp directory)
- Configurable via `MEMORY_DB_PATH` environment variable
- Automatic sync based on file modification timestamps
- JSON format for persistence

## Tools

### add_note
Add a new note.

**Parameters:**
- `title` (string, required): Title of the note
- `content` (string, required): Content/body of the note
- `tags` (array of strings, optional): List of tags

**Example:**
```json
{
  "title": "Meeting Notes",
  "content": "Discussed project timeline and deliverables",
  "tags": ["work", "meeting"]
}
```

### update_note
Update an existing note.

**Parameters:**
- `id` (string, required): ID of the note to update
- `title` (string, optional): New title
- `content` (string, optional): New content
- `tags` (array of strings, optional): New tags

**Example:**
```json
{
  "id": "123456789",
  "title": "Updated Meeting Notes",
  "content": "Updated discussion with new timeline"
}
```

### delete_note
Delete a note by ID.

**Parameters:**
- `id` (string, required): ID of the note to delete

**Example:**
```json
{
  "id": "123456789"
}
```

### search_notes
Search notes by keywords.

**Parameters:**
- `keywords` (string, required): Space-separated keywords to search for
- `limit` (integer, optional): Maximum number of results (default: 10)

**Example:**
```json
{
  "keywords": "project meeting",
  "limit": 5
}
```

## Building

```bash
make -C src/mcps/memory
```

## Running

```bash
# Default (uses temp directory)
./mai-mcp-memory

# Custom database path
MEMORY_DB_PATH=/path/to/notes.json ./mai-mcp-memory

# TCP server mode
./mai-mcp-memory -l 127.0.0.1:8080
```