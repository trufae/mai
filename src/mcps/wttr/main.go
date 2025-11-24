package main

import (
	"flag"
	"log"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port, http://host:port/path, or sse://host:port/path (optional) serve MCP over TCP, HTTP, or SSE")
	flag.Parse()

	// Create the weather service
	weatherService := NewWeatherService()

	weatherTool := CreateWeatherTool()
	forecastTool := CreateForecastTool()
	moonTool := CreateMoonTool()

	server := mcplib.NewMCPServer([]mcplib.ToolDefinition{weatherTool, forecastTool, moonTool})

	server.RegisterTool("get_weather", WeatherToolHandler(weatherService))
	server.RegisterTool("get_weather_forecast", ForecastToolHandler(weatherService))
	server.RegisterTool("get_moon_phase", MoonToolHandler(weatherService))

	// Start the server
	if err := server.ListenAndServe(*listen, false, ""); err != nil {
		log.Fatalln("ListenAndServe:", err)
	}
}
