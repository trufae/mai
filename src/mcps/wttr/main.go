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

	weatherTool := CreateWeatherTool()
	forecastTool := CreateForecastTool()
	moonTool := CreateMoonTool()

	server := mcplib.NewMCPServer([]mcplib.ToolDefinition{weatherTool, forecastTool, moonTool})

	server.RegisterTool("get_weather", WeatherToolHandler(weatherService))
	server.RegisterTool("get_weather_forecast", ForecastToolHandler(weatherService))
	server.RegisterTool("get_moon_phase", MoonToolHandler(weatherService))

	// Start the server
	if *listen != "" {
		if err := server.ServeTCP(*listen); err != nil {
			log.Fatalln("ServeTCP:", err)
		}
	} else {
		server.Start()
	}
}
