package main

import (
	"fmt"
	"mcplib"
	"time"
)

// Tool represents a complete tool definition with handler
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Handler     mcplib.ToolHandler
}

// TimeService handles all time-related operations
type TimeService struct{}

// NewTimeService creates a new TimeService instance
func NewTimeService() *TimeService {
	return &TimeService{}
}

// formatTime formats time according to the required format YYYY:MM:DD hh:mm:ss
func (s *TimeService) formatTime(t time.Time) string {
	return t.Format("2006:01:02 15:04:05")
}

// GetTools returns all available tools
func (s *TimeService) GetTools() []Tool {
	return []Tool{
		{
			Name:        "current_time",
			Description: "Get the current time in the specified timezone",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": "Timezone (e.g., 'America/New_York', 'Europe/London'). Defaults to UTC if not provided.",
					},
				},
			},
			Handler: s.handleCurrentTime,
		},
		{
			Name:        "sunrise_sunset",
			Description: "Get sunrise and sunset times for a location",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"latitude": map[string]interface{}{
						"type":        "number",
						"description": "Latitude of the location",
					},
					"longitude": map[string]interface{}{
						"type":        "number",
						"description": "Longitude of the location",
					},
					"date": map[string]interface{}{
						"type":        "string",
						"description": "Date in YYYY:MM:DD format. Defaults to today if not provided.",
					},
				},
				"required": []string{"latitude", "longitude"},
			},
			Handler: s.handleSunriseSunset,
		},
		{
			Name:        "moon_phase",
			Description: "Get the current moon phase",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{
						"type":        "string",
						"description": "Date in YYYY:MM:DD format. Defaults to today if not provided.",
					},
				},
			},
			Handler: s.handleMoonPhase,
		},
		{
			Name:        "timezone_info",
			Description: "Get timezone information for a location",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"latitude": map[string]interface{}{
						"type":        "number",
						"description": "Latitude of the location",
					},
					"longitude": map[string]interface{}{
						"type":        "number",
						"description": "Longitude of the location",
					},
				},
				"required": []string{"latitude", "longitude"},
			},
			Handler: s.handleTimezone,
		},
	}
}

// handleCurrentTime handles the current time request
func (s *TimeService) handleCurrentTime(args map[string]interface{}) (interface{}, error) {
	var location *time.Location
	var err error

	// Get timezone from args or use UTC as default
	timezone, ok := args["timezone"].(string)
	if !ok || timezone == "" {
		location = time.UTC
	} else {
		location, err = time.LoadLocation(timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone: %v", err)
		}
	}

	// Get current time in the specified timezone
	now := time.Now().In(location)

	return map[string]interface{}{
		"time":     s.formatTime(now),
		"timezone": location.String(),
	}, nil
}

// handleSunriseSunset handles the sunrise/sunset request
func (s *TimeService) handleSunriseSunset(args map[string]interface{}) (interface{}, error) {
	// Extract latitude and longitude
	lat, ok := args["latitude"].(float64)
	if !ok {
		return nil, fmt.Errorf("latitude must be a number")
	}

	lng, ok := args["longitude"].(float64)
	if !ok {
		return nil, fmt.Errorf("longitude must be a number")
	}

	// Parse date if provided or use today
	var date time.Time
	dateStr, ok := args["date"].(string)
	if !ok || dateStr == "" {
		date = time.Now().UTC()
	} else {
		var err error
		date, err = time.Parse("2006:01:02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid date format, use YYYY:MM:DD: %v", err)
		}
	}

	// Calculate sunrise and sunset times
	sunrise, sunset := calculateSunriseSunset(lat, lng, date)

	return map[string]interface{}{
		"sunrise": s.formatTime(sunrise),
		"sunset":  s.formatTime(sunset),
		"date":    date.Format("2006:01:02"),
	}, nil
}

// handleMoonPhase handles the moon phase request
func (s *TimeService) handleMoonPhase(args map[string]interface{}) (interface{}, error) {
	// Parse date if provided or use today
	var date time.Time
	dateStr, ok := args["date"].(string)
	if !ok || dateStr == "" {
		date = time.Now().UTC()
	} else {
		var err error
		date, err = time.Parse("2006:01:02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid date format, use YYYY:MM:DD: %v", err)
		}
	}

	// Calculate moon phase
	phase, phaseName := calculateMoonPhase(date)

	return map[string]interface{}{
		"date":       date.Format("2006:01:02"),
		"phase":      phase,
		"phase_name": phaseName,
	}, nil
}

// handleTimezone handles the timezone request
func (s *TimeService) handleTimezone(args map[string]interface{}) (interface{}, error) {
	// Extract latitude and longitude
	lat, ok := args["latitude"].(float64)
	if !ok {
		return nil, fmt.Errorf("latitude must be a number")
	}

	lng, ok := args["longitude"].(float64)
	if !ok {
		return nil, fmt.Errorf("longitude must be a number")
	}

	// Get timezone for the location
	timezone, err := getTimezoneForLocation(lat, lng)
	if err != nil {
		return nil, err
	}

	// Get current time in the timezone
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("failed to load timezone: %v", err)
	}

	now := time.Now().In(location)

	return map[string]interface{}{
		"timezone":     timezone,
		"current_time": s.formatTime(now),
		"offset":       now.Format("-07:00"),
	}, nil
}

// Helper functions for astronomical calculations

// calculateSunriseSunset calculates sunrise and sunset times for a location
func calculateSunriseSunset(lat, lng float64, date time.Time) (time.Time, time.Time) {
	// This is a simplified calculation
	// In a real implementation, you would use a proper astronomical library

	// For now, return placeholder values
	// Sunrise at 6 AM UTC
	sunrise := time.Date(date.Year(), date.Month(), date.Day(), 6, 0, 0, 0, time.UTC)
	// Sunset at 6 PM UTC
	sunset := time.Date(date.Year(), date.Month(), date.Day(), 18, 0, 0, 0, time.UTC)

	return sunrise, sunset
}

// calculateMoonPhase calculates the moon phase for a given date
func calculateMoonPhase(date time.Time) (float64, string) {
	// This is a simplified calculation
	// In a real implementation, you would use a proper astronomical library

	// For now, return placeholder values
	phase := 0.5 // 0 = new moon, 0.5 = full moon, 1 = new moon
	phaseName := "Full Moon"

	return phase, phaseName
}

// getTimezoneForLocation gets the timezone for a location based on coordinates
func getTimezoneForLocation(lat, lng float64) (string, error) {
	// This is a simplified implementation
	// In a real implementation, you would use a timezone database or API

	// For now, return a placeholder timezone based on longitude
	// This is very approximate and just for demonstration
	if lng >= -15 && lng <= 15 {
		return "Europe/London", nil
	} else if lng > 15 && lng <= 45 {
		return "Europe/Berlin", nil
	} else if lng > 45 && lng <= 75 {
		return "Asia/Dubai", nil
	} else if lng > 75 && lng <= 105 {
		return "Asia/Kolkata", nil
	} else if lng > 105 && lng <= 135 {
		return "Asia/Shanghai", nil
	} else if lng > 135 && lng <= 165 {
		return "Asia/Tokyo", nil
	} else if lng > 165 || lng <= -165 {
		return "Pacific/Auckland", nil
	} else if lng > -165 && lng <= -135 {
		return "America/Anchorage", nil
	} else if lng > -135 && lng <= -105 {
		return "America/Los_Angeles", nil
	} else if lng > -105 && lng <= -75 {
		return "America/Chicago", nil
	} else if lng > -75 && lng <= -45 {
		return "America/New_York", nil
	} else if lng > -45 && lng <= -15 {
		return "America/Sao_Paulo", nil
	}

	return "UTC", nil
}
