package main

import (
	"flag"
	"log"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port (optional) serve MCP over TCP")
	flag.Parse()

	// Create the weather service
	weatherService := NewWeatherService()

	// Create the weather tool
	weatherTool := CreateWeatherTool()

	// Create the MCP server with the weather tool
	server := mcplib.NewMCPServer([]mcplib.ToolDefinition{weatherTool})

	// Register the weather tool handler
	server.RegisterTool("getWeather", WeatherToolHandler(weatherService))

	// Start the server
	if *listen != "" {
		if err := server.ServeTCP(*listen); err != nil {
			log.Fatalln("ServeTCP:", err)
		}
	} else {
		server.Start()
	}
}
