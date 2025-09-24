# wttr-mcp

MCPd service that exposes


```bash
# Get current weather
curl "http://localhost:8989/tools/server3/getCurrentWeather?location=New York"

# Get weather forecast
curl "http://localhost:8989/tools/server3/getForecast?location=London&days=5"

# Get weather with specific units
curl -X POST "http://localhost:8989/tools/server3/getWeather" \
  -H "Content-Type: application/json" \
  -d '{"location": "Tokyo", "units": "metric"}'
```

