package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"mcplib"
)

// SearchProvider defines the interface for different web search implementations
type SearchProvider interface {
	Search(query string) (*SearchResult, error)
	Name() string
}

// SearchResult represents the result of a web search
type SearchResult struct {
	Query   string       `json:"query"`
	Results []SearchItem `json:"results"`
}

// SearchItem represents a single search result item
type SearchItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// DuckDuckGoResponse represents the response from DuckDuckGo API
type DuckDuckGoResponse struct {
	Abstract     string `json:"Abstract"`
	AbstractText string `json:"AbstractText"`
	AbstractURL  string `json:"AbstractURL"`
	Heading      string `json:"Heading"`
	Results      []struct {
		FirstURL string `json:"FirstURL"`
		Text     string `json:"Text"`
	} `json:"Results"`
}

// WikipediaResponse represents the response from Wikipedia API
type WikipediaResponse struct {
	Query struct {
		Search []struct {
			Title     string `json:"title"`
			PageID    int    `json:"pageid"`
			Snippet   string `json:"snippet"`
			Timestamp string `json:"timestamp"`
		} `json:"search"`
	} `json:"query"`
}

// OllamaSearchProvider implements SearchProvider for Ollama's web search API
type OllamaSearchProvider struct{}

// Name returns the provider name
func (p *OllamaSearchProvider) Name() string {
	return "ollama"
}

// Search performs a web search using Ollama's API
func (p *OllamaSearchProvider) Search(query string) (*SearchResult, error) {
	apiKey := os.Getenv("OLLAMA_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OLLAMA_API_KEY environment variable is required")
	}

	apiURL := "https://ollama.com/api/web_search"
	payload := map[string]string{"query": query}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var result SearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	result.Query = query
	return &result, nil
}

// DuckDuckGoSearchProvider implements SearchProvider for DuckDuckGo's instant answers API
type DuckDuckGoSearchProvider struct{}

// Name returns the provider name
func (p *DuckDuckGoSearchProvider) Name() string {
	return "duckduckgo"
}

// Search performs a web search using DuckDuckGo's instant answers API
func (p *DuckDuckGoSearchProvider) Search(query string) (*SearchResult, error) {
	// DuckDuckGo instant answers API
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1", url.QueryEscape(query))

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var ddgResp DuckDuckGoResponse
	if err := json.Unmarshal(body, &ddgResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	result := &SearchResult{
		Query:   query,
		Results: []SearchItem{},
	}

	// Add abstract result if available
	if ddgResp.AbstractText != "" {
		result.Results = append(result.Results, SearchItem{
			Title:   ddgResp.Heading,
			URL:     ddgResp.AbstractURL,
			Content: ddgResp.AbstractText,
		})
	}

	// Add other results
	for _, item := range ddgResp.Results {
		// Extract title from HTML if needed
		title := item.Text
		if strings.Contains(title, "<b>") {
			// Simple HTML stripping for title
			title = strings.ReplaceAll(title, "<b>", "")
			title = strings.ReplaceAll(title, "</b>", "")
		}

		result.Results = append(result.Results, SearchItem{
			Title:   title,
			URL:     item.FirstURL,
			Content: item.Text,
		})
	}

	return result, nil
}

// WikipediaSearchProvider implements SearchProvider for Wikipedia's search API
type WikipediaSearchProvider struct{}

// Name returns the provider name
func (p *WikipediaSearchProvider) Name() string {
	return "wikipedia"
}

// Search performs a web search using Wikipedia's search API
func (p *WikipediaSearchProvider) Search(query string) (*SearchResult, error) {
	// Wikipedia search API
	apiURL := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=10", url.QueryEscape(query))

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var wikiResp WikipediaResponse
	if err := json.Unmarshal(body, &wikiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	result := &SearchResult{
		Query:   query,
		Results: []SearchItem{},
	}

	// Add search results
	for _, item := range wikiResp.Query.Search {
		// Clean up the snippet by removing HTML tags
		content := strings.ReplaceAll(item.Snippet, "<span class=\"searchmatch\">", "")
		content = strings.ReplaceAll(content, "</span>", "")

		// Construct Wikipedia URL
		wikiURL := fmt.Sprintf("https://en.wikipedia.org/wiki/%s", url.QueryEscape(item.Title))

		result.Results = append(result.Results, SearchItem{
			Title:   item.Title,
			URL:     wikiURL,
			Content: content,
		})
	}

	return result, nil
}

// SearxngSearchProvider implements SearchProvider for Searxng's search API
type SearxngSearchProvider struct{}

// Name returns the provider name
func (p *SearxngSearchProvider) Name() string {
	return "searxng"
}

// Search performs a web search using Searxng's API
func (p *SearxngSearchProvider) Search(query string) (*SearchResult, error) {
	// Default to localhost:8888 as per user example, but allow env override
	apiURL := "http://localhost:8888/search"
	if envURL := os.Getenv("SEARXNG_API_URL"); envURL != "" {
		apiURL = envURL
	}

	// Prepare query parameters
	params := url.Values{}
	params.Add("q", query)
	params.Add("format", "csv")

	// Construct request URL
	reqURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Add Accept header as per user example
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse CSV response
	reader := csv.NewReader(resp.Body)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSV response: %v", err)
	}

	result := &SearchResult{
		Query:   query,
		Results: []SearchItem{},
	}

	if len(records) < 2 {
		// No results or just header
		return result, nil
	}

	// Map headers to indices
	headers := records[0]
	headerMap := make(map[string]int)
	for i, h := range headers {
		headerMap[strings.ToLower(h)] = i
	}

	// Check for required headers
	titleIdx, okTitle := headerMap["title"]
	urlIdx, okUrl := headerMap["url"]
	contentIdx, okContent := headerMap["content"]

	if !okTitle || !okUrl {
		return nil, fmt.Errorf("CSV response missing required headers (title, url)")
	}

	for _, record := range records[1:] {
		// Ensure record has enough fields
		if len(record) <= titleIdx || len(record) <= urlIdx {
			continue
		}

		item := SearchItem{
			Title: record[titleIdx],
			URL:   record[urlIdx],
		}

		if okContent && len(record) > contentIdx {
			item.Content = record[contentIdx]
		}

		result.Results = append(result.Results, item)
	}

	return result, nil
}

// WebSearchService handles web search operations
type WebSearchService struct {
	providers          map[string]SearchProvider
	enabledProviders   map[string]bool
	searchAllProviders bool
}

// NewWebSearchService creates a new WebSearchService instance
func NewWebSearchService(searchAllProviders bool) *WebSearchService {
	service := &WebSearchService{
		providers:          make(map[string]SearchProvider),
		enabledProviders:   make(map[string]bool),
		searchAllProviders: searchAllProviders,
	}

	// Register available providers
	service.providers["ollama"] = &OllamaSearchProvider{}
	service.providers["duckduckgo"] = &DuckDuckGoSearchProvider{}
	service.providers["wikipedia"] = &WikipediaSearchProvider{}
	service.providers["searxng"] = &SearxngSearchProvider{}

	return service
}

// EnableProvider enables a specific search provider
func (s *WebSearchService) EnableProvider(name string) error {
	if _, exists := s.providers[name]; !exists {
		return fmt.Errorf("unknown provider: %s", name)
	}
	s.enabledProviders[name] = true
	return nil
}

// GetEnabledProviders returns a list of enabled and working providers
func (s *WebSearchService) GetEnabledProviders() []string {
	var enabled []string
	for name, provider := range s.providers {
		if s.enabledProviders[name] {
			// Test if provider can work (has required API keys, etc.)
			if s.isProviderWorking(provider) {
				enabled = append(enabled, name)
			}
		}
	}
	return enabled
}

// isProviderWorking checks if a provider is working (has required credentials, etc.)
func (s *WebSearchService) isProviderWorking(provider SearchProvider) bool {
	switch provider.Name() {
	case "ollama":
		return os.Getenv("OLLAMA_API_KEY") != ""
	case "duckduckgo":
		// DuckDuckGo doesn't require API keys
		return true
	case "wikipedia":
		// Wikipedia doesn't require API keys
		return true
	case "searxng":
		// Searxng doesn't require API keys (uses localhost or configured URL)
		return true
	default:
		return false
	}
}

// GetTools returns all available web search tools
func (s *WebSearchService) GetTools() []mcplib.Tool {
	enabledProviders := s.GetEnabledProviders()
	if len(enabledProviders) == 0 {
		// Return empty tools if no providers are working
		return []mcplib.Tool{}
	}

	providerEnum := make([]string, len(enabledProviders))
	copy(providerEnum, enabledProviders)

	return []mcplib.Tool{
		{
			Name:        "web_search",
			Description: "Performs a web search using the configured search provider and returns relevant results.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query to perform.",
					},
					"provider": map[string]any{
						"type":        "string",
						"description": "The search provider to use (optional, defaults to first enabled provider).",
						"enum":        providerEnum,
					},
				},
				"required": []string{"query"},
			},
			UsageExamples: fmt.Sprintf("Example: {\"query\": \"what is ollama?\"} - Searches the web for information about Ollama\nExample: {\"query\": \"golang tutorials\", \"provider\": \"%s\"} - Searches using the specified provider", enabledProviders[0]),
			Handler:       s.handleWebSearch,
		},
	}
}

// handleWebSearch handles the WebSearch tool call
func (s *WebSearchService) handleWebSearch(args map[string]interface{}) (interface{}, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required")
	}

	enabledProviders := s.GetEnabledProviders()
	if len(enabledProviders) == 0 {
		return nil, fmt.Errorf("no search providers are enabled or working")
	}

	// If a specific provider is requested, use only that one
	if providerArg, ok := args["provider"].(string); ok && providerArg != "" {
		// Check if the requested provider is enabled and working
		found := false
		for _, p := range enabledProviders {
			if p == providerArg {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("provider '%s' is not enabled or not working", providerArg)
		}

		provider, exists := s.providers[providerArg]
		if !exists {
			return nil, fmt.Errorf("unknown search provider: %s", providerArg)
		}

		result, err := provider.Search(query)
		if err != nil {
			return nil, fmt.Errorf("search failed: %v", err)
		}

		return result, nil
	}

	// No specific provider requested - use configured behavior
	if s.searchAllProviders {
		return s.searchWithAllProviders(query, enabledProviders)
	} else {
		return s.searchWithFirstProvider(query, enabledProviders)
	}
}

// searchWithFirstProvider searches using the first enabled provider that works
func (s *WebSearchService) searchWithFirstProvider(query string, enabledProviders []string) (interface{}, error) {
	for _, providerName := range enabledProviders {
		provider := s.providers[providerName]
		result, err := provider.Search(query)
		if err == nil {
			return result, nil
		}
		// Log the error but continue to next provider
		fmt.Fprintf(os.Stderr, "Provider %s failed: %v\n", providerName, err)
	}

	return nil, fmt.Errorf("all enabled providers failed")
}

// searchWithAllProviders searches using all enabled providers and aggregates results
func (s *WebSearchService) searchWithAllProviders(query string, enabledProviders []string) (interface{}, error) {
	aggregatedResults := &SearchResult{
		Query:   query,
		Results: []SearchItem{},
	}

	hasResults := false

	for _, providerName := range enabledProviders {
		provider := s.providers[providerName]
		result, err := provider.Search(query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Provider %s failed: %v\n", providerName, err)
			continue
		}

		// Add results from this provider
		for _, item := range result.Results {
			// Add provider info to distinguish results
			itemWithProvider := SearchItem{
				Title:   fmt.Sprintf("[%s] %s", providerName, item.Title),
				URL:     item.URL,
				Content: item.Content,
			}
			aggregatedResults.Results = append(aggregatedResults.Results, itemWithProvider)
		}

		if len(result.Results) > 0 {
			hasResults = true
		}
	}

	if !hasResults {
		return nil, fmt.Errorf("all enabled providers failed to return results")
	}

	return aggregatedResults, nil
}
