package main

import (
	"flag"
	"log"

	"mcplib"
)

func main() {
	listen := flag.String("l", "", "listen host:port or http://host:port/path (optional) serve MCP over TCP or HTTP")
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
		config, err := mcplib.ParseListenString(*listen)
		if err != nil {
			log.Fatalln("ParseListenString:", err)
		}
		if config.Protocol == "http" {
			if err := server.ServeHTTP(config.Port, config.BasePath, false, ""); err != nil {
				log.Fatalln("ServeHTTP:", err)
			}
		} else {
			// TCP mode (default)
			if err := server.ServeTCP(config.Address); err != nil {
				log.Fatalln("ServeTCP:", err)
			}
		}
	} else {
		server.Start()
	}
}
