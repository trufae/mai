package main

import (
	"encoding/json"
	"fmt"
	"mcplib"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Note represents a note with tags
type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MemoryService handles all memory/note operations
type MemoryService struct {
	notes       map[string]Note
	dbPath      string
	lastModTime time.Time
}

// NewMemoryService creates a new MemoryService instance
func NewMemoryService() *MemoryService {
	dbPath := os.Getenv("MEMORY_DB_PATH")
	if dbPath == "" {
		tmpDir := os.TempDir()
		dbPath = filepath.Join(tmpDir, "mai_memory.json")
	}

	service := &MemoryService{
		notes:  make(map[string]Note),
		dbPath: dbPath,
	}

	// Load existing data if file exists
	service.loadFromFile()

	return service
}

// loadFromFile loads notes from the JSON file if it exists and is newer
func (s *MemoryService) loadFromFile() error {
	file, err := os.Open(s.dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist, that's fine
		}
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	modTime := stat.ModTime()
	if modTime.After(s.lastModTime) || s.lastModTime.IsZero() {
		var notes map[string]Note
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&notes); err != nil {
			return err
		}
		s.notes = notes
		s.lastModTime = modTime
	}

	return nil
}

// saveToFile saves notes to the JSON file
func (s *MemoryService) saveToFile() error {
	// Ensure directory exists
	dir := filepath.Dir(s.dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	file, err := os.Create(s.dbPath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.notes); err != nil {
		return err
	}

	// Update last mod time
	if stat, err := file.Stat(); err == nil {
		s.lastModTime = stat.ModTime()
	}

	return nil
}

// generateID generates a simple ID for notes
func (s *MemoryService) generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// GetTools returns all available tools
func (s *MemoryService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		{
			Name:        "add_note",
			Description: "Add a new note with title, content, and optional tags",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Title of the note",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Content/body of the note",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Optional list of tags for the note",
					},
				},
				"required": []string{"title", "content"},
			},
			UsageExamples: "Example: {\"title\": \"Meeting Notes\", \"content\": \"Discussed project timeline\", \"tags\": [\"work\", \"meeting\"]}",
			Handler:       s.handleAddNote,
		},
		{
			Name:        "update_note",
			Description: "Update an existing note by ID with new title, content, and/or tags",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "ID of the note to update",
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "New title (optional)",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "New content (optional)",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "New tags (optional)",
					},
				},
				"required": []string{"id"},
			},
			UsageExamples: "Example: {\"id\": \"123456789\", \"title\": \"Updated Meeting Notes\", \"content\": \"Updated discussion\"}",
			Handler:       s.handleUpdateNote,
		},
		{
			Name:        "delete_note",
			Description: "Delete a note by ID",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "ID of the note to delete",
					},
				},
				"required": []string{"id"},
			},
			UsageExamples: "Example: {\"id\": \"123456789\"}",
			Handler:       s.handleDeleteNote,
		},
		{
			Name:        "search_notes",
			Description: "Search notes by keywords in title, content, or tags. Returns matching notes sorted by relevance",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"keywords": map[string]interface{}{
						"type":        "string",
						"description": "Keywords to search for (space-separated)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results to return (optional, default 10)",
					},
				},
				"required": []string{"keywords"},
			},
			UsageExamples: "Example: {\"keywords\": \"project meeting\", \"limit\": 5}",
			Handler:       s.handleSearchNotes,
		},
	}
}

// handleAddNote handles adding a new note
func (s *MemoryService) handleAddNote(args map[string]interface{}) (interface{}, error) {
	// Reload from file if necessary
	s.loadFromFile()

	title, ok := args["title"].(string)
	if !ok {
		return nil, fmt.Errorf("title must be a string")
	}

	content, ok := args["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	var tags []string
	if tagsRaw, ok := args["tags"]; ok {
		if tagsSlice, ok := tagsRaw.([]interface{}); ok {
			for _, tag := range tagsSlice {
				if tagStr, ok := tag.(string); ok {
					tags = append(tags, tagStr)
				}
			}
		}
	}

	now := time.Now()
	note := Note{
		ID:        s.generateID(),
		Title:     title,
		Content:   content,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.notes[note.ID] = note

	// Save to file
	if err := s.saveToFile(); err != nil {
		return nil, fmt.Errorf("failed to save note: %v", err)
	}

	return map[string]interface{}{
		"id":         note.ID,
		"message":    "Note added successfully",
		"created_at": note.CreatedAt.Format(time.RFC3339),
	}, nil
}

// handleUpdateNote handles updating an existing note
func (s *MemoryService) handleUpdateNote(args map[string]interface{}) (interface{}, error) {
	// Reload from file if necessary
	s.loadFromFile()

	id, ok := args["id"].(string)
	if !ok {
		return nil, fmt.Errorf("id must be a string")
	}

	note, exists := s.notes[id]
	if !exists {
		return nil, fmt.Errorf("note with id %s not found", id)
	}

	updated := false

	if title, ok := args["title"].(string); ok && title != "" {
		note.Title = title
		updated = true
	}

	if content, ok := args["content"].(string); ok && content != "" {
		note.Content = content
		updated = true
	}

	if tagsRaw, ok := args["tags"]; ok {
		if tagsSlice, ok := tagsRaw.([]interface{}); ok {
			var tags []string
			for _, tag := range tagsSlice {
				if tagStr, ok := tag.(string); ok {
					tags = append(tags, tagStr)
				}
			}
			note.Tags = tags
			updated = true
		}
	}

	if updated {
		note.UpdatedAt = time.Now()
		s.notes[id] = note

		// Save to file
		if err := s.saveToFile(); err != nil {
			return nil, fmt.Errorf("failed to save note: %v", err)
		}
	}

	return map[string]interface{}{
		"id":         note.ID,
		"message":    "Note updated successfully",
		"updated_at": note.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// handleDeleteNote handles deleting a note
func (s *MemoryService) handleDeleteNote(args map[string]interface{}) (interface{}, error) {
	// Reload from file if necessary
	s.loadFromFile()

	id, ok := args["id"].(string)
	if !ok {
		return nil, fmt.Errorf("id must be a string")
	}

	if _, exists := s.notes[id]; !exists {
		return nil, fmt.Errorf("note with id %s not found", id)
	}

	delete(s.notes, id)

	// Save to file
	if err := s.saveToFile(); err != nil {
		return nil, fmt.Errorf("failed to save changes: %v", err)
	}

	return map[string]interface{}{
		"id":      id,
		"message": "Note deleted successfully",
	}, nil
}

// handleSearchNotes handles searching notes by keywords
func (s *MemoryService) handleSearchNotes(args map[string]interface{}) (interface{}, error) {
	// Reload from file if necessary
	s.loadFromFile()

	keywordsStr, ok := args["keywords"].(string)
	if !ok {
		return nil, fmt.Errorf("keywords must be a string")
	}

	limit := 10 // default
	if limitRaw, ok := args["limit"]; ok {
		if limitInt, ok := limitRaw.(float64); ok {
			limit = int(limitInt)
		}
	}

	keywords := strings.Fields(strings.ToLower(keywordsStr))
	if len(keywords) == 0 {
		return nil, fmt.Errorf("no valid keywords provided")
	}

	type searchResult struct {
		note    Note
		score   int
		matches []string
	}

	var results []searchResult

	for _, note := range s.notes {
		score := 0
		var matches []string

		titleLower := strings.ToLower(note.Title)
		contentLower := strings.ToLower(note.Content)

		for _, keyword := range keywords {
			// Title matches (higher weight)
			if strings.Contains(titleLower, keyword) {
				score += 10
				matches = append(matches, "title:"+keyword)
			}

			// Content matches
			if strings.Contains(contentLower, keyword) {
				score += 5
				matches = append(matches, "content:"+keyword)
			}

			// Tag matches (highest weight)
			for _, tag := range note.Tags {
				if strings.Contains(strings.ToLower(tag), keyword) {
					score += 15
					matches = append(matches, "tag:"+tag)
				}
			}
		}

		if score > 0 {
			results = append(results, searchResult{note: note, score: score, matches: matches})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	// Format response
	var response []map[string]interface{}
	for _, result := range results {
		response = append(response, map[string]interface{}{
			"id":         result.note.ID,
			"title":      result.note.Title,
			"content":    result.note.Content,
			"tags":       result.note.Tags,
			"created_at": result.note.CreatedAt.Format(time.RFC3339),
			"updated_at": result.note.UpdatedAt.Format(time.RFC3339),
			"score":      result.score,
			"matches":    result.matches,
		})
	}

	return map[string]interface{}{
		"results":  response,
		"total":    len(response),
		"keywords": keywords,
	}, nil
}
