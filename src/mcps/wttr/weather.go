package main

import (
	"fmt"
	"io"
	"mcplib"
	"net/http"
	"net/url"
)

// WeatherService provides methods to fetch weather information
type WeatherService struct {
	client *http.Client
}

// NewWeatherService creates a new WeatherService
func NewWeatherService() *WeatherService {
	return &WeatherService{
		client: &http.Client{},
	}
}

// GetWeather fetches weather information for a location
func (s *WeatherService) GetWeather(location string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("location cannot be empty")
	}

	esc := url.QueryEscape(location)

	// Create a new request with the curl User-Agent
	req, err := http.NewRequest("GET", fmt.Sprintf("https://wttr.in/%s?format=%%l:+%%m+%%c+%%C+%%t+%%f'", esc), nil)
	if err != nil {
		return "", err
	}

	// Set User-Agent to match curl's
	req.Header.Set("User-Agent", "curl/8.1.2")
	req.Header.Set("Accept", "*/*")

	// Use the client to send the request
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CreateWeatherTool creates a tool definition for the weather service
func CreateWeatherTool() mcplib.ToolDefinition {
	return mcplib.ToolDefinition{
		Name:        "getWeather",
		Description: "Get weather for a location",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "The location to get weather for",
				},
			},
			"required": []string{"location"},
		},
	}
}

// WeatherToolHandler creates a handler function for the weather tool
func WeatherToolHandler(service *WeatherService) mcplib.ToolHandler {
	return func(args map[string]interface{}) (interface{}, error) {
		locRaw, ok := args["location"].(string)
		if !ok || locRaw == "" {
			return nil, fmt.Errorf("missing or invalid 'location' argument")
		}

		return service.GetWeather(locRaw)
	}
}
