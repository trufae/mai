# Time MCP Examples (timemcp)

```bash
# Get current time
curl "http://localhost:8080/tools/server2/getCurrentTime"

# Get time in specific timezone
curl "http://localhost:8080/tools/server2/getTimeInTimezone?timezone=America/New_York"

# Format timestamp
curl -X POST "http://localhost:8080/tools/server2/formatTime" \
  -H "Content-Type: application/json" \
  -d '{"timestamp": 1640995200, "format": "2006-01-02 15:04:05"}'
```

