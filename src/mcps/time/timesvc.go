package main

import (
	"fmt"
	"math"
	"mcplib"
	"time"
)

// Using mcplib.Tool instead of local Tool definition

// TimeService handles all time-related operations
type TimeService struct{}

// NewTimeService creates a new TimeService instance
func NewTimeService() *TimeService {
	return &TimeService{}
}

// formatTime formats time according to the required format YYYY-MM-DD hh:mm:ss
func (s *TimeService) formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// GetTools returns all available tools
func (s *TimeService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		{
			Name:        "current_time",
			Description: "Get the current time and weekday in the specified timezone",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": "(optional) UTC, GMT ,..",
					},
				},
			},
			UsageExamples: "Example: {\"timezone\": \"America/New_York\"} - Returns current time in New York",
			Handler:       s.handleCurrentTime,
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
						"description": "Date in YYYY-MM-DD format. Defaults to today if not provided.",
					},
				},
				"required": []string{"latitude", "longitude"},
			},
			UsageExamples: "Example: {\"latitude\": 40.7128, \"longitude\": -74.0060} - Returns sunrise/sunset times for New York City",
			Handler:       s.handleSunriseSunset,
		},
		{
			Name:        "moon_phase",
			Description: "Get the current moon phase",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{
						"type":        "string",
						"description": "(optional) Date in YYYY-MM-DD format.",
					},
				},
			},
			UsageExamples: "Example: {\"date\": \"2023-12-25\"} - Returns moon phase for Christmas 2023",
			Handler:       s.handleMoonPhase,
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
			UsageExamples: "Example: {\"latitude\": 51.5074, \"longitude\": -0.1278} - Returns timezone information for London",
			Handler:       s.handleTimezone,
		},
	}
}

// handleCurrentTime handles the current time request
func (s *TimeService) handleCurrentTime(args map[string]interface{}) (interface{}, error) {
	var location *time.Location
	var err error

	// Get timezone from args or use local timezone as default
	timezone, ok := args["timezone"].(string)
	if !ok || timezone == "" {
		location = time.Local
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
		"weekday":  now.Weekday().String(),
		"month":    now.Month().String(),
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
		date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid date format, use YYYY-MM-DD: %v", err)
		}
	}

	// Calculate sunrise and sunset times
	sunrise, sunset := calculateSunriseSunset(lat, lng, date)

	return map[string]interface{}{
		"sunrise": s.formatTime(sunrise),
		"sunset":  s.formatTime(sunset),
		"date":    date.Format("2006-01-02"),
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
		date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid date format, use YYYY-MM-DD: %v", err)
		}
	}

	// Calculate moon phase
	phase, phaseName := calculateMoonPhase(date)

	return map[string]interface{}{
		"date":       date.Format("2006-01-02"),
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

func calculateMoonPhase(date time.Time) (float64, string) {
	// Reference date: January 6, 2000, at 18:14 UTC, which was a new moon
	const referenceNewMoon = 2451550.1 // Julian date for the reference new moon

	// Calculate the number of days between the given date and the reference new moon
	julianDate := dateToJulian(date)
	daysSinceNewMoon := julianDate - referenceNewMoon

	// The length of a lunar cycle is approximately 29.53 days
	synodicMonth := 29.53058867

	// Calculate the phase of the moon as a percentage of the lunar month
	// Use modulo to handle negative values and get a value between 0 and 1
	phase := math.Mod(daysSinceNewMoon/synodicMonth, 1.0)

	// Ensure phase is positive (between 0 and 1)
	if phase < 0 {
		phase += 1.0
	}

	// Determine the phase name
	// Using standard astronomical definitions for moon phases
	var phaseName string
	switch {
	case phase < 0.0345 || phase >= 0.9655:
		phaseName = "New Moon"
	case phase < 0.2155:
		phaseName = "Waxing Crescent"
	case phase < 0.2845:
		phaseName = "First Quarter"
	case phase < 0.4655:
		phaseName = "Waxing Gibbous"
	case phase < 0.5345:
		phaseName = "Full Moon"
	case phase < 0.7155:
		phaseName = "Waning Gibbous"
	case phase < 0.7845:
		phaseName = "Last Quarter"
	case phase < 0.9655:
		phaseName = "Waning Crescent"
	}

	return phase, phaseName
}

// Helper function to convert a time.Time object to Julian date
func dateToJulian(date time.Time) float64 {
	// Convert to UTC to ensure consistent calculations
	utc := date.UTC()

	// Extract date components
	Y := float64(utc.Year())
	M := float64(utc.Month())
	D := float64(utc.Day())

	// Time components for fractional day
	h := float64(utc.Hour())
	m := float64(utc.Minute())
	s := float64(utc.Second())
	ns := float64(utc.Nanosecond())

	// Calculate fractional day (0.0 - 1.0)
	dayFrac := (h + m/60.0 + s/3600.0 + ns/3600000000000.0) / 24.0

	// Convert calendar date to Julian date
	// Using algorithm from Jean Meeus' "Astronomical Algorithms"
	if M <= 2 {
		Y -= 1
		M += 12
	}

	// Check if date is in Gregorian calendar (after Oct 15, 1582)
	var A, B float64
	if Y > 1582 || (Y == 1582 && M > 10) || (Y == 1582 && M == 10 && D >= 15) {
		A = math.Floor(Y / 100)
		B = 2 - A + math.Floor(A/4)
	} else {
		// Julian calendar
		A = 0
		B = 0
	}

	// Main Julian Day calculation
	JD := math.Floor(365.25*(Y+4716)) + math.Floor(30.6001*(M+1)) + D + B - 1524.5

	// Add fractional day
	return JD + dayFrac
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
