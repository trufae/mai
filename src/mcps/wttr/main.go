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

	server.RegisterTool("getWeather", WeatherToolHandler(weatherService))
	server.RegisterTool("getWeatherForecast", ForecastToolHandler(weatherService))
	server.RegisterTool("getMoonPhase", MoonToolHandler(weatherService))

	// Start the server
	if *listen != "" {
		if err := server.ServeTCP(*listen); err != nil {
			log.Fatalln("ServeTCP:", err)
		}
	} else {
		server.Start()
	}
}
