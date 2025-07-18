package main

import (
	"mcplib"
)

func main() {
	// Create the weather service
	weatherService := NewWeatherService()

	// Create the weather tool
	weatherTool := CreateWeatherTool()

	// Create the MCP server with the weather tool
	server := mcplib.NewMCPServer([]mcplib.ToolDefinition{weatherTool})

	// Register the weather tool handler
	server.RegisterTool("getWeather", WeatherToolHandler(weatherService))

	// Start the server
	server.Start()
}
