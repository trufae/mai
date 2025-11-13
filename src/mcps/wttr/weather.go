package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mcplib"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	userAgent     = "curl/8.1.2"
	currentFormat = "%l|%T|%Z|%c|%C|%t|%f|%h|%w"
	moonFormat    = "%l|%T|%Z|%m|%M"
	lunarCycle    = 30
	fullMoonDay   = 15
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

// CurrentWeather represents the current conditions for a location.
type CurrentWeather struct {
	Location      string `json:"location"`
	LocalTime     string `json:"local_time"`
	Timezone      string `json:"timezone"`
	ConditionIcon string `json:"condition_icon"`
	Condition     string `json:"condition"`
	Temperature   string `json:"temperature"`
	FeelsLike     string `json:"feels_like"`
	Humidity      string `json:"humidity"`
	Wind          string `json:"wind"`
	CurrentTime   string `json:"current_time"`
}

// ForecastSummary represents an aggregated forecast for up to a week.
type ForecastSummary struct {
	Location    string        `json:"location"`
	Days        []ForecastDay `json:"days"`
	CurrentTime string        `json:"current_time"`
}

// ForecastDay captures daily forecast details.
type ForecastDay struct {
	Date      string `json:"date"`
	MinTempC  int    `json:"min_temp_c"`
	MaxTempC  int    `json:"max_temp_c"`
	AvgTempC  int    `json:"avg_temp_c"`
	Condition string `json:"condition"`
	Sunrise   string `json:"sunrise,omitempty"`
	Sunset    string `json:"sunset,omitempty"`
}

// MoonInfo contains moon phase and next full moon information.
type MoonInfo struct {
	Location            string `json:"location"`
	LocalTime           string `json:"local_time"`
	Timezone            string `json:"timezone"`
	Phase               string `json:"phase"`
	Icon                string `json:"icon"`
	MoonDay             int    `json:"moon_day"`
	IlluminationPercent string `json:"illumination_percent"`
	DaysUntilFullMoon   int    `json:"days_until_full_moon"`
	NextFullMoonDate    string `json:"next_full_moon_date"`
	CurrentTime         string `json:"current_time"`
}

type wttrResponse struct {
	CurrentCondition []struct {
		FeelsLikeC  string `json:"FeelsLikeC"`
		TempC       string `json:"temp_C"`
		Humidity    string `json:"humidity"`
		WeatherDesc []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
		WindspeedKmph    string `json:"windspeedKmph"`
		Winddir16Point   string `json:"winddir16Point"`
		LocalObsDateTime string `json:"localObsDateTime"`
	} `json:"current_condition"`
	Weather []struct {
		Date      string `json:"date"`
		AvgtempC  string `json:"avgtempC"`
		MaxtempC  string `json:"maxtempC"`
		MintempC  string `json:"mintempC"`
		Astronomy []struct {
			Sunrise          string `json:"sunrise"`
			Sunset           string `json:"sunset"`
			MoonPhase        string `json:"moon_phase"`
			MoonIllumination string `json:"moon_illumination"`
		} `json:"astronomy"`
		Hourly []struct {
			Time        string `json:"time"`
			WeatherDesc []struct {
				Value string `json:"value"`
			} `json:"weatherDesc"`
		} `json:"hourly"`
	} `json:"weather"`
	NearestArea []struct {
		AreaName []struct {
			Value string `json:"value"`
		} `json:"areaName"`
		Country []struct {
			Value string `json:"value"`
		} `json:"country"`
		Region []struct {
			Value string `json:"value"`
		} `json:"region"`
	} `json:"nearest_area"`
}

// GetCurrentWeather fetches current weather details for a location.
func (s *WeatherService) GetCurrentWeather(location string) (*CurrentWeather, error) {
	parts, err := s.fetchFormatted(location, currentFormat)
	if err != nil {
		return nil, err
	}
	if len(parts) != 9 {
		return nil, fmt.Errorf("unexpected response format: %q", strings.Join(parts, "|"))
	}

	return &CurrentWeather{
		Location:      strings.TrimSpace(parts[0]),
		LocalTime:     strings.TrimSpace(parts[1]),
		Timezone:      strings.TrimSpace(parts[2]),
		ConditionIcon: strings.TrimSpace(parts[3]),
		Condition:     strings.TrimSpace(parts[4]),
		Temperature:   strings.TrimSpace(parts[5]),
		FeelsLike:     strings.TrimSpace(parts[6]),
		Humidity:      strings.TrimSpace(parts[7]),
		Wind:          strings.TrimSpace(parts[8]),
		CurrentTime:   time.Now().Format("2006-01-02 Monday January"),
	}, nil
}

// GetWeeklyForecast retrieves up to seven days of forecast data for a location.
func (s *WeatherService) GetWeeklyForecast(location string) (*ForecastSummary, error) {
	data, err := s.fetchWeatherJSON(location)
	if err != nil {
		return nil, err
	}

	summary := &ForecastSummary{Location: locationLabel(data), CurrentTime: time.Now().Format("2006-01-02 Monday January")}
	for i, day := range data.Weather {
		if i >= 7 {
			break
		}
		minTemp, err := strconv.Atoi(strings.TrimSpace(day.MintempC))
		if err != nil {
			minTemp = 0
		}
		maxTemp, err := strconv.Atoi(strings.TrimSpace(day.MaxtempC))
		if err != nil {
			maxTemp = 0
		}
		avgTemp, err := strconv.Atoi(strings.TrimSpace(day.AvgtempC))
		if err != nil {
			avgTemp = 0
		}

		condition := pickCondition(day.Hourly)
		sunrise, sunset := "", ""
		if len(day.Astronomy) > 0 {
			sunrise = strings.TrimSpace(day.Astronomy[0].Sunrise)
			sunset = strings.TrimSpace(day.Astronomy[0].Sunset)
		}

		summary.Days = append(summary.Days, ForecastDay{
			Date:      strings.TrimSpace(day.Date),
			MinTempC:  minTemp,
			MaxTempC:  maxTemp,
			AvgTempC:  avgTemp,
			Condition: condition,
			Sunrise:   sunrise,
			Sunset:    sunset,
		})
	}

	if len(summary.Days) == 0 {
		return nil, fmt.Errorf("no forecast data returned")
	}

	return summary, nil
}

// GetMoonInfo retrieves moon data and computes the next full moon date.
func (s *WeatherService) GetMoonInfo(location string) (*MoonInfo, error) {
	data, err := s.fetchWeatherJSON(location)
	if err != nil {
		return nil, err
	}

	parts, err := s.fetchFormatted(location, moonFormat)
	if err != nil {
		return nil, err
	}
	if len(parts) != 5 {
		return nil, fmt.Errorf("unexpected moon response format: %q", strings.Join(parts, "|"))
	}

	moonDay, err := strconv.Atoi(strings.TrimSpace(parts[4]))
	if err != nil {
		return nil, fmt.Errorf("parse moon day: %w", err)
	}

	daysUntilFull := (fullMoonDay - moonDay + lunarCycle) % lunarCycle
	timezone := strings.TrimSpace(parts[2])
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		loc = time.UTC
	}

	baseDate := time.Now().In(loc)
	if len(data.Weather) > 0 {
		if parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(data.Weather[0].Date), loc); err == nil {
			baseDate = parsed
		}
	}
	nextFull := baseDate.AddDate(0, 0, daysUntilFull)

	phase := ""
	illumination := ""
	if len(data.Weather) > 0 && len(data.Weather[0].Astronomy) > 0 {
		astro := data.Weather[0].Astronomy[0]
		phase = strings.TrimSpace(astro.MoonPhase)
		illumination = strings.TrimSpace(astro.MoonIllumination)
	}

	locationName := strings.TrimSpace(parts[0])
	if locationName == "" {
		locationName = locationLabel(data)
	}

	return &MoonInfo{
		Location:            locationName,
		LocalTime:           strings.TrimSpace(parts[1]),
		Timezone:            timezone,
		Phase:               phase,
		Icon:                strings.TrimSpace(parts[3]),
		MoonDay:             moonDay,
		IlluminationPercent: illumination,
		DaysUntilFullMoon:   daysUntilFull,
		NextFullMoonDate:    nextFull.Format("2006-01-02"),
		CurrentTime:         time.Now().Format("2006-01-02 Monday January"),
	}, nil
}

// CreateWeatherTool creates a tool definition for the weather service.
func CreateWeatherTool() mcplib.ToolDefinition {
	return mcplib.ToolDefinition{
		Name:        "get_weather",
		Description: "Get current weather, including local time and timezone. Location is optional.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "Optional city or country to get weather for. Leave empty for caller location.",
				},
			},
		},
	}
}

// CreateForecastTool defines the week-forecast tool.
func CreateForecastTool() mcplib.ToolDefinition {
	return mcplib.ToolDefinition{
		Name:        "get_weather_forecast",
		Description: "Get the week-long weather forecast (up to 7 days) for a location.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "Optional city or country to forecast. Leave empty for caller location.",
				},
			},
		},
	}
}

// CreateMoonTool defines the moon phase tool.
func CreateMoonTool() mcplib.ToolDefinition {
	return mcplib.ToolDefinition{
		Name:        "get_moon_phase",
		Description: "Get current moon phase, local moon info, and the next full moon date.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "Optional city or country for moon data. Leave empty for caller location.",
				},
			},
		},
	}
}

// WeatherToolHandler creates a handler function for the weather tool.
func WeatherToolHandler(service *WeatherService) mcplib.ToolHandler {
	return func(args map[string]interface{}) (interface{}, error) {
		location := extractLocation(args)
		return service.GetCurrentWeather(location)
	}
}

// ForecastToolHandler handles forecast queries.
func ForecastToolHandler(service *WeatherService) mcplib.ToolHandler {
	return func(args map[string]interface{}) (interface{}, error) {
		location := extractLocation(args)
		return service.GetWeeklyForecast(location)
	}
}

// MoonToolHandler handles moon phase queries.
func MoonToolHandler(service *WeatherService) mcplib.ToolHandler {
	return func(args map[string]interface{}) (interface{}, error) {
		location := extractLocation(args)
		return service.GetMoonInfo(location)
	}
}

func (s *WeatherService) fetchFormatted(location, format string) ([]string, error) {
	req, err := http.NewRequest("GET", composeBaseURL(location), nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = "format=" + format
	decorateRequest(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("empty response from wttr.in")
	}
	return strings.Split(trimmed, "|"), nil
}

func (s *WeatherService) fetchWeatherJSON(location string) (*wttrResponse, error) {
	req, err := http.NewRequest("GET", composeBaseURL(location), nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("format", "j1")
	q.Set("num_of_days", "7")
	req.URL.RawQuery = q.Encode()
	decorateRequest(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "Unknown location") {
		return nil, fmt.Errorf("unknown location: %s", trimmed)
	}

	var result wttrResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func composeBaseURL(location string) string {
	base := "https://wttr.in"
	if strings.TrimSpace(location) == "" {
		return base
	}
	return base + "/" + url.PathEscape(location)
}

func decorateRequest(req *http.Request) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
}

func extractLocation(args map[string]interface{}) string {
	raw, ok := args["location"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func locationLabel(data *wttrResponse) string {
	if data == nil || len(data.NearestArea) == 0 {
		return ""
	}
	area := data.NearestArea[0]
	var parts []string
	if len(area.AreaName) > 0 {
		parts = append(parts, strings.TrimSpace(area.AreaName[0].Value))
	}
	if len(area.Region) > 0 {
		region := strings.TrimSpace(area.Region[0].Value)
		if region != "" {
			parts = append(parts, region)
		}
	}
	if len(area.Country) > 0 {
		parts = append(parts, strings.TrimSpace(area.Country[0].Value))
	}
	return strings.Join(parts, ", ")
}

func pickCondition(hourly []struct {
	Time        string `json:"time"`
	WeatherDesc []struct {
		Value string `json:"value"`
	} `json:"weatherDesc"`
}) string {
	if len(hourly) == 0 {
		return ""
	}
	for _, hour := range hourly {
		if strings.TrimSpace(hour.Time) == "1200" && len(hour.WeatherDesc) > 0 {
			return strings.TrimSpace(hour.WeatherDesc[0].Value)
		}
	}
	if len(hourly[0].WeatherDesc) > 0 {
		return strings.TrimSpace(hourly[0].WeatherDesc[0].Value)
	}
	return ""
}
