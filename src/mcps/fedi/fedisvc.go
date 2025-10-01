package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mcplib"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// FediService handles all federated social network operations
type FediService struct {
	instance string
	apiKey   string
	client   *http.Client
}

// MastodonStatus represents a Mastodon post
type MastodonStatus struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	Account   struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	} `json:"account"`
	URL string `json:"url"`
}

// NewFediService creates a new FediService instance
func NewFediService() *FediService {
	instance := os.Getenv("MASTODON_INSTANCE")
	if instance == "" {
		instance = "mastodon.social" // default instance
	}

	apiKey := os.Getenv("MASTODON_API_KEY")
	if apiKey == "" {
		// Try to read from file like r2ai does
		if home := os.Getenv("HOME"); home != "" {
			if data, err := os.ReadFile(home + "/.r2ai.mastodon-key"); err == nil {
				apiKey = strings.TrimSpace(string(data))
			}
		}
	}

	return &FediService{
		instance: instance,
		apiKey:   apiKey,
		client:   &http.Client{},
	}
}

// GetTools returns all available tools
func (s *FediService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		{
			Name:        "search_posts",
			Description: "Search for posts on Mastodon (federated social network). May require MASTODON_API_KEY for full results.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "(required) Search query for posts",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "(optional) Maximum number of results (default: 10, max: 40)",
					},
				},
				"required": []string{"query"},
			},
			UsageExamples: "Example: {\"query\": \"artificial intelligence\", \"limit\": 5} - Search for posts about AI",
			Handler:       s.handleSearchPosts,
		},
		{
			Name:        "post_message",
			Description: "Post a message to Mastodon",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "(required) The text content of the post",
					},
					"visibility": map[string]interface{}{
						"type":        "string",
						"description": "(optional) Post visibility: public, unlisted, private, direct (default: public)",
						"enum":        []string{"public", "unlisted", "private", "direct"},
					},
				},
				"required": []string{"content"},
			},
			UsageExamples: "Example: {\"content\": \"Hello from MAI!\", \"visibility\": \"public\"} - Post a public message",
			Handler:       s.handlePostMessage,
		},
		{
			Name:        "get_timeline",
			Description: "Get posts from a Mastodon timeline",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type": map[string]interface{}{
						"type":        "string",
						"description": "(optional) Timeline type: home, public, local (default: public)",
						"enum":        []string{"home", "public", "local"},
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "(optional) Maximum number of posts (default: 10, max: 40)",
					},
				},
			},
			UsageExamples: "Example: {\"type\": \"public\", \"limit\": 5} - Get latest public posts",
			Handler:       s.handleGetTimeline,
		},
	}
}

// handleSearchPosts handles the search posts request
func (s *FediService) handleSearchPosts(args map[string]interface{}) (interface{}, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required and must be a string")
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 40 {
			limit = 40
		}
	}

	posts, err := s.searchPosts(query, limit)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"query": query,
		"posts": posts,
		"count": len(posts),
	}, nil
}

// handlePostMessage handles the post message request
func (s *FediService) handlePostMessage(args map[string]interface{}) (interface{}, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("Mastodon API key is required for posting. Set MASTODON_API_KEY environment variable or create ~/.r2ai.mastodon-key file")
	}

	content, ok := args["content"].(string)
	if !ok || content == "" {
		return nil, fmt.Errorf("content is required and must be a string")
	}

	visibility := "public"
	if v, ok := args["visibility"].(string); ok {
		switch v {
		case "public", "unlisted", "private", "direct":
			visibility = v
		default:
			return nil, fmt.Errorf("invalid visibility: must be public, unlisted, private, or direct")
		}
	}

	post, err := s.postMessage(content, visibility)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"success": true,
		"post":    post,
	}, nil
}

// handleGetTimeline handles the get timeline request
func (s *FediService) handleGetTimeline(args map[string]interface{}) (interface{}, error) {
	timelineType := "public"
	if t, ok := args["type"].(string); ok {
		switch t {
		case "home", "public", "local":
			timelineType = t
		default:
			return nil, fmt.Errorf("invalid timeline type: must be home, public, or local")
		}
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 40 {
			limit = 40
		}
	}

	posts, err := s.getTimeline(timelineType, limit)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"timeline": timelineType,
		"posts":    posts,
		"count":    len(posts),
	}, nil
}

// searchPosts searches for posts using Mastodon API
func (s *FediService) searchPosts(query string, limit int) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("https://%s/api/v2/search?resolve=true&limit=%d&type=statuses&q=%s",
		s.instance, limit, query)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Try with auth first, then without if it fails
	authTried := false
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		authTried = true
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// If auth failed and we haven't tried without auth, retry without
	if resp.StatusCode == http.StatusUnauthorized && authTried && s.apiKey != "" {
		req.Header.Del("Authorization")
		resp, err = s.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var result struct {
		Statuses []MastodonStatus `json:"statuses"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	posts := make([]map[string]interface{}, 0, len(result.Statuses))
	for _, status := range result.Statuses {
		// Clean HTML from content
		content := s.stripHTML(status.Content)

		posts = append(posts, map[string]interface{}{
			"id":           status.ID,
			"content":      content,
			"created_at":   status.CreatedAt,
			"username":     status.Account.Username,
			"display_name": status.Account.DisplayName,
			"url":          status.URL,
		})
	}

	return posts, nil
}

// postMessage posts a message to Mastodon
func (s *FediService) postMessage(content, visibility string) (map[string]interface{}, error) {
	url := fmt.Sprintf("https://%s/api/v1/statuses", s.instance)

	payload := map[string]interface{}{
		"status":     content,
		"visibility": visibility,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status: %d, body: %s", resp.StatusCode, string(body))
	}

	var status MastodonStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":           status.ID,
		"content":      s.stripHTML(status.Content),
		"created_at":   status.CreatedAt,
		"username":     status.Account.Username,
		"display_name": status.Account.DisplayName,
		"url":          status.URL,
	}, nil
}

// getTimeline gets posts from a timeline
func (s *FediService) getTimeline(timelineType string, limit int) ([]map[string]interface{}, error) {
	var endpoint string
	switch timelineType {
	case "home":
		endpoint = "/api/v1/timelines/home"
	case "local":
		endpoint = "/api/v1/timelines/public?local=true"
	case "public":
		fallthrough
	default:
		endpoint = "/api/v1/timelines/public"
	}

	url := fmt.Sprintf("https://%s%s?limit=%d", s.instance, endpoint, limit)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if s.apiKey != "" && timelineType == "home" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var statuses []MastodonStatus
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return nil, err
	}

	posts := make([]map[string]interface{}, 0, len(statuses))
	for _, status := range statuses {
		content := s.stripHTML(status.Content)

		posts = append(posts, map[string]interface{}{
			"id":           status.ID,
			"content":      content,
			"created_at":   status.CreatedAt,
			"username":     status.Account.Username,
			"display_name": status.Account.DisplayName,
			"url":          status.URL,
		})
	}

	return posts, nil
}

// stripHTML removes HTML tags from content
func (s *FediService) stripHTML(content string) string {
	// Simple HTML tag removal
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(content, "")
}
