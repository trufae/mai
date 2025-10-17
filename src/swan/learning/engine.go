package learning

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mai/src/swan/config"
	"mai/src/swan/logging"
	"mai/src/swan/mcp"
)

// TaskRecord represents a task execution record
type TaskRecord struct {
	TaskID    string                 `json:"task_id"`
	AgentName string                 `json:"agent_name"`
	Query     string                 `json:"query"`
	Response  string                 `json:"response"`
	Duration  time.Duration          `json:"duration"`
	Success   bool                   `json:"success"`
	Error     string                 `json:"error,omitempty"`
	Quality   float64                `json:"quality"` // Quality score (0-1)
	Metadata  map[string]interface{} `json:"metadata"`
	Timestamp time.Time              `json:"timestamp"`
}

// PerformanceMetric represents agent performance data
type PerformanceMetric struct {
	AgentName   string        `json:"agent_name"`
	TotalTasks  int           `json:"total_tasks"`
	SuccessRate float64       `json:"success_rate"`
	AvgDuration time.Duration `json:"avg_duration"`
	LastUpdated time.Time     `json:"last_updated"`
}

// LearningEngine manages learning and decision-making for SWAN
type LearningEngine struct {
	config       *config.SwanConfig
	metrics      map[string]*PerformanceMetric
	tasks        []*TaskRecord          // Simple in-memory storage for now
	cache        map[string]*TaskRecord // Cache for fast responses
	logger       *logging.Logger
	vdbPath      string              // Path to VDB data directory
	networkFiles map[string]*os.File // Network knowledge files for each agent
	vdb          *SimpleVDB          // Vector database for semantic caching
}

// NewLearningEngine creates a new learning engine
func NewLearningEngine(cfg *config.SwanConfig) (*LearningEngine, error) {
	logger, err := logging.NewLogger(cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %v", err)
	}

	// Ensure VDB directory exists
	vdbPath := cfg.Orchestrator.VDBPath
	if err := os.MkdirAll(vdbPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create VDB directory: %v", err)
	}

	// Initialize network knowledge files directory
	networkDir := filepath.Join(cfg.WorkDir, "network")
	if err := os.MkdirAll(networkDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create network directory: %v", err)
	}

	engine := &LearningEngine{
		config:       cfg,
		metrics:      make(map[string]*PerformanceMetric),
		tasks:        make([]*TaskRecord, 0),
		cache:        make(map[string]*TaskRecord),
		logger:       logger,
		vdbPath:      vdbPath,
		networkFiles: make(map[string]*os.File),
		vdb:          NewSimpleVDB(64), // 64-dimensional vectors
	}

	// Load existing cache from disk
	engine.loadCacheFromDisk()

	return engine, nil
}

// RecordTask stores a task execution record with quality assessment and caching
func (le *LearningEngine) RecordTask(record *TaskRecord) error {
	// Assess quality based on response characteristics
	quality := le.AssessQuality(record)
	record.Quality = quality

	// Store in memory for now
	le.tasks = append(le.tasks, record)

	// Cache good responses from slow models for fast retrieval
	if record.Duration > 10*time.Second && quality > 0.8 {
		le.cache[record.Query] = record
		if err := le.storeInVDB(record); err != nil {
			le.logger.LogDecision("cache_error", fmt.Sprintf("Failed to store in VDB: %v", err), nil, nil, false, err)
		} else {
			le.logger.LogCacheOperation("cached", record.Query, true, record.Duration)
		}
	}

	// Save context information for VDB reloading
	le.saveContextForVDB(record)

	// Update performance metrics and rankings
	le.updateMetrics(record)
	le.updateRankings(record)

	// Detect and log mistakes
	le.detectAndLogMistakes(record, quality)

	// Log the task execution with comprehensive statistics
	metrics := map[string]interface{}{
		"duration_ms":     record.Duration.Milliseconds(),
		"quality":         quality,
		"success":         record.Success,
		"response_length": len(record.Response),
		"query_length":    len(record.Query),
		"cache_hit":       false, // This will be updated when cache is checked
	}
	le.logger.LogDecision("task_executed", fmt.Sprintf("Task %s executed by %s", record.TaskID, record.AgentName), metrics, nil, true, nil)

	return nil
}

// GetLogger returns the logger instance
func (le *LearningEngine) GetLogger() *logging.Logger {
	return le.logger
}

// saveContextForVDB saves context information that can be reloaded by the VDB
func (le *LearningEngine) saveContextForVDB(record *TaskRecord) {
	contextDir := filepath.Join(le.vdbPath, "context")
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		le.logger.LogDecision("context_save_error", fmt.Sprintf("Failed to create context directory: %v", err), nil, nil, false, err)
		return
	}

	// Create context file with query-response pair and metadata
	contextFile := filepath.Join(contextDir, fmt.Sprintf("task_%s.txt", record.TaskID))
	context := fmt.Sprintf("Query: %s\nResponse: %s\nAgent: %s\nQuality: %.2f\nDuration: %v\nTimestamp: %s\nSuccess: %t\n",
		record.Query, record.Response, record.AgentName, record.Quality, record.Duration, record.Timestamp.Format(time.RFC3339), record.Success)

	if err := os.WriteFile(contextFile, []byte(context), 0644); err != nil {
		le.logger.LogDecision("context_save_error", fmt.Sprintf("Failed to save context file: %v", err), nil, nil, false, err)
	}
}

// QuerySimilarTasks finds similar past tasks for a given query
func (le *LearningEngine) QuerySimilarTasks(query string, limit int) ([]*TaskRecord, error) {
	var records []*TaskRecord

	// Simple similarity based on query string matching
	for _, task := range le.tasks {
		if strings.Contains(strings.ToLower(task.Query), strings.ToLower(query)) {
			records = append(records, task)
			if len(records) >= limit {
				break
			}
		}
	}

	return records, nil
}

// GetBestAgent suggests the best agent for a task based on past performance and caching
func (le *LearningEngine) GetBestAgent(query string) (string, error) {
	// Check in-memory cache first for fast responses
	if cached, exists := le.cache[query]; exists {
		le.logger.LogCacheOperation("hit", query, true, cached.Duration)
		return cached.AgentName, nil
	}

	// Check VDB cache for similar queries
	if cachedResponse, err := le.queryVDBCache(query); err == nil && cachedResponse != "" {
		le.logger.LogCacheOperation("vdb_hit", query, true, 0)
		// Return a fast agent for cached responses - could be improved to return the original agent
		return "fast-agent", nil
	}

	similarTasks, err := le.QuerySimilarTasks(query, 10)
	if err != nil {
		return "", err
	}

	// Calculate agent scores based on time and quality
	agentScores := make(map[string]float64)
	agentCounts := make(map[string]int)

	for _, task := range similarTasks {
		if task.Success {
			// Score based on quality and inverse time (faster + higher quality = better)
			timeScore := 1.0 / (1.0 + task.Duration.Seconds()/60.0)
			score := task.Quality * timeScore
			agentScores[task.AgentName] += score
			agentCounts[task.AgentName]++
		}
	}

	// Find agent with highest average score
	var bestAgent string
	var bestScore float64

	for agent, score := range agentScores {
		if count := agentCounts[agent]; count > 0 {
			avgScore := score / float64(count)
			if avgScore > bestScore {
				bestScore = avgScore
				bestAgent = agent
			}
		}
	}

	if bestAgent == "" {
		// Fallback to random agent if no data
		return "", fmt.Errorf("no agent data available")
	}

	return bestAgent, nil
}

// AnalyzePerformance returns performance analysis for all agents
func (le *LearningEngine) AnalyzePerformance() map[string]*PerformanceMetric {
	return le.metrics
}

// loadCacheFromDisk loads cached entries from disk on startup
func (le *LearningEngine) loadCacheFromDisk() {
	cacheFile := filepath.Join(le.vdbPath, "cache.json")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		// No cache file exists yet, which is fine
		return
	}

	var entries []CacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		le.logger.LogDecision("cache_load_error", fmt.Sprintf("Failed to load cache from disk: %v", err), nil, nil, false, err)
		return
	}

	// Rebuild the VDB with loaded entries
	for _, entry := range entries {
		le.vdb.Insert(entry)
		// Also populate in-memory cache for fast access
		if entry.Quality > 0.8 {
			le.cache[entry.Query] = &TaskRecord{
				Query:     entry.Query,
				Response:  entry.Response,
				AgentName: entry.AgentName,
				Quality:   entry.Quality,
				Duration:  entry.Duration,
				Timestamp: entry.Timestamp,
				Success:   true,
			}
		}
	}

	le.logger.LogDecision("cache_loaded", fmt.Sprintf("Loaded %d cached entries from disk", len(entries)), map[string]interface{}{
		"entries_loaded": len(entries),
		"vdb_size":       len(le.vdb.Entries),
	}, nil, true, nil)
}

// GetStatistics returns comprehensive statistics about the learning engine
func (le *LearningEngine) GetStatistics() map[string]interface{} {
	stats := map[string]interface{}{
		"total_tasks":      len(le.tasks),
		"cached_responses": len(le.cache),
		"vdb_entries":      len(le.vdb.Entries),
		"agents_tracked":   len(le.metrics),
		"network_files":    len(le.networkFiles),
	}

	// Calculate average quality and duration
	var totalQuality float64
	var totalDuration time.Duration
	var successfulTasks int

	for _, task := range le.tasks {
		totalQuality += task.Quality
		totalDuration += task.Duration
		if task.Success {
			successfulTasks++
		}
	}

	if len(le.tasks) > 0 {
		stats["avg_quality"] = totalQuality / float64(len(le.tasks))
		stats["avg_duration_ms"] = totalDuration.Milliseconds() / int64(len(le.tasks))
		stats["success_rate"] = float64(successfulTasks) / float64(len(le.tasks))
	}

	// Agent rankings
	var rankings []map[string]interface{}
	for agent, metric := range le.metrics {
		rankings = append(rankings, map[string]interface{}{
			"agent":        agent,
			"total_tasks":  metric.TotalTasks,
			"success_rate": metric.SuccessRate,
			"avg_duration": metric.AvgDuration.String(),
			"last_updated": metric.LastUpdated,
		})
	}
	stats["agent_rankings"] = rankings

	return stats
}

// SuggestImprovements suggests ways to improve the system
func (le *LearningEngine) SuggestImprovements() []string {
	var suggestions []string

	// Check for underperforming agents
	for agentName, metric := range le.metrics {
		if metric.TotalTasks > 5 && metric.SuccessRate < 0.7 {
			suggestions = append(suggestions, fmt.Sprintf("Consider restarting or reconfiguring agent %s (success rate: %.2f%%)", agentName, metric.SuccessRate*100))
		}
		if metric.TotalTasks > 5 && metric.AvgDuration > 30*time.Second {
			suggestions = append(suggestions, fmt.Sprintf("Agent %s is slow (avg duration: %v), consider optimization", agentName, metric.AvgDuration))
		}
	}

	// Suggest new agent types based on query patterns
	queryPatterns := le.analyzeQueryPatterns()
	if len(queryPatterns) > 0 {
		suggestions = append(suggestions, fmt.Sprintf("Detected query patterns: %v - consider adding specialized agents", queryPatterns))
	}

	return suggestions
}

// SuggestNewAgent suggests a new agent configuration based on learning data and MCP inspection
func (le *LearningEngine) SuggestNewAgent() (*config.ResolvedAgentConfig, error) {
	// Analyze query patterns and performance metrics
	patterns := le.analyzeQueryPatterns()
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no patterns detected")
	}

	// Check if we need a new agent based on performance
	needsNewAgent := false
	for _, metric := range le.metrics {
		if metric.TotalTasks > 10 && metric.SuccessRate < 0.7 {
			needsNewAgent = true
			break
		}
	}

	if !needsNewAgent {
		return nil, fmt.Errorf("no need for new agent based on current performance")
	}

	// Inspect available MCPs to understand their capabilities
	var availableMCPs []string
	for _, task := range le.tasks {
		if mcps, ok := task.Metadata["mcps"].([]string); ok {
			availableMCPs = append(availableMCPs, mcps...)
		}
	}

	// Remove duplicates
	uniqueMCPs := make(map[string]bool)
	for _, mcp := range availableMCPs {
		uniqueMCPs[mcp] = true
	}

	// For each unique MCP, inspect its tools
	mcpCapabilities := make(map[string][]string)
	for mcpName := range uniqueMCPs {
		info, err := mcp.InspectMCPServer(mcpName)
		if err != nil {
			continue // Skip if inspection fails
		}

		// Get pseudo-MCPs based on capabilities
		pseudoMCPs := info.SuggestPseudoMCPs()
		for category, tools := range pseudoMCPs {
			mcpCapabilities[category] = append(mcpCapabilities[category], tools...)
		}
	}

	// Suggest agent based on dominant patterns and capabilities
	var suggestedProvider string

	// Enhanced logic: consider time/quality metrics
	if strings.Contains(strings.Join(patterns, " "), "code") {
		suggestedProvider = "openai" // Fast for coding
	} else if strings.Contains(strings.Join(patterns, " "), "search") {
		suggestedProvider = "claude" // Good for research
	} else {
		suggestedProvider = "gemini" // Balanced
	}

	suggestion := &config.ResolvedAgentConfig{
		Name:     fmt.Sprintf("dynamic-agent-%d", time.Now().Unix()),
		Provider: suggestedProvider,
		Model:    "gpt-3.5-turbo",         // Default model
		MCPs:     []config.MCPConfig{},    // TODO: Resolve from config
		Prompts:  []config.PromptConfig{}, // TODO: Resolve from config
		Dynamic:  true,
	}

	// Log the suggestion
	le.logger.LogDecision("agent_suggested", fmt.Sprintf("Suggested new agent %s for patterns: %v", suggestion.Name, patterns), nil, nil, true, nil)

	return suggestion, nil
}

// AssessQuality assesses the quality of a response (0-1 scale)
func (le *LearningEngine) AssessQuality(record *TaskRecord) float64 {
	quality := 0.5 // Base quality

	// Success factor
	if record.Success {
		quality += 0.3
	}

	// Length factor (longer responses might be more detailed)
	if len(record.Response) > 100 {
		quality += 0.1
	}

	// Error factor
	if record.Error != "" {
		quality -= 0.3
	}

	// Time factor (faster is better, but not too fast for quality)
	if record.Duration < 5*time.Second {
		quality -= 0.1 // Might be too rushed
	} else if record.Duration > 30*time.Second {
		quality += 0.1 // Thorough response
	}

	// Clamp to 0-1
	if quality > 1.0 {
		quality = 1.0
	}
	if quality < 0.0 {
		quality = 0.0
	}

	return quality
}

// updateRankings updates agent rankings based on performance
func (le *LearningEngine) updateRankings(record *TaskRecord) {
	// This could be expanded to maintain global rankings
	// For now, just log ranking changes
	if record.Success && record.Quality > 0.8 {
		le.logger.LogDecision("ranking_update", fmt.Sprintf("Agent %s improved ranking with quality %.2f", record.AgentName, record.Quality), map[string]interface{}{
			"agent":       record.AgentName,
			"quality":     record.Quality,
			"duration_ms": record.Duration.Milliseconds(),
		}, nil, true, nil)
	}
}

// updateMetrics updates performance metrics for an agent
func (le *LearningEngine) updateMetrics(record *TaskRecord) {
	metric, exists := le.metrics[record.AgentName]
	if !exists {
		metric = &PerformanceMetric{
			AgentName: record.AgentName,
		}
		le.metrics[record.AgentName] = metric
	}

	metric.TotalTasks++
	if record.Success {
		// Calculate success rate
		successCount := int(metric.SuccessRate * float64(metric.TotalTasks-1))
		successCount++
		metric.SuccessRate = float64(successCount) / float64(metric.TotalTasks)
	} else {
		// Recalculate success rate
		successCount := int(metric.SuccessRate * float64(metric.TotalTasks-1))
		metric.SuccessRate = float64(successCount) / float64(metric.TotalTasks)
	}

	// Update average duration
	if metric.TotalTasks == 1 {
		metric.AvgDuration = record.Duration
	} else {
		metric.AvgDuration = time.Duration((float64(metric.AvgDuration)*float64(metric.TotalTasks-1) + float64(record.Duration)) / float64(metric.TotalTasks))
	}

	metric.LastUpdated = time.Now()
}

// analyzeQueryPatterns analyzes common query patterns
func (le *LearningEngine) analyzeQueryPatterns() []string {
	// Simple keyword analysis
	keywordCounts := make(map[string]int)
	words := []string{"code", "debug", "explain", "search", "write", "test"}

	for _, metric := range le.metrics {
		// This is a simplified analysis; in practice, you'd query VDB for task texts
		for _, word := range words {
			keywordCounts[word] += metric.TotalTasks / 10 // Rough estimate
		}
	}

	var patterns []string
	for word, count := range keywordCounts {
		if count > 5 {
			patterns = append(patterns, word)
		}
	}

	return patterns
}

// Helper functions
func getString(metadata map[string]interface{}, key string) string {
	if v, ok := metadata[key].(string); ok {
		return v
	}
	return ""
}

func getBool(metadata map[string]interface{}, key string) bool {
	if v, ok := metadata[key].(bool); ok {
		return v
	}
	return false
}

func parseDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func extractQueryFromText(text string) string {
	parts := strings.Split(text, " | ")
	if len(parts) > 0 {
		queryPart := strings.TrimPrefix(parts[0], "Query: ")
		return queryPart
	}
	return text
}

// storeInVDB stores a cached response in the VDB for future retrieval
func (le *LearningEngine) storeInVDB(record *TaskRecord) error {
	// Store in the vector database for semantic search
	entry := CacheEntry{
		Query:     record.Query,
		Response:  record.Response,
		AgentName: record.AgentName,
		Quality:   record.Quality,
		Duration:  record.Duration,
		Timestamp: record.Timestamp,
	}

	le.vdb.Insert(entry)

	// Also save to disk for persistence (optional - could be loaded on startup)
	cacheFile := filepath.Join(le.vdbPath, "cache.json")
	entries := le.vdb.Entries

	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %v", err)
	}

	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %v", err)
	}

	return nil
}

// detectAndLogMistakes detects mistakes and logs corrections
func (le *LearningEngine) detectAndLogMistakes(record *TaskRecord, quality float64) {
	mistakeDetected := false
	var mistake string
	var correction string

	// Check for low quality responses
	if quality < 0.5 && record.Success {
		mistake = fmt.Sprintf("Agent %s produced low quality response (%.2f) for query: %s", record.AgentName, quality, record.Query)
		correction = "Will avoid using this agent for similar queries in the future"
		mistakeDetected = true
	}

	// Check for repeated failures by the same agent
	if !record.Success {
		metric, exists := le.metrics[record.AgentName]
		if exists && metric.TotalTasks > 3 {
			failureRate := float64(metric.TotalTasks-int(metric.SuccessRate*float64(metric.TotalTasks-1))) / float64(metric.TotalTasks)
			if failureRate > 0.5 {
				mistake = fmt.Sprintf("Agent %s has high failure rate (%.2f%%) - %d/%d tasks failed", record.AgentName, failureRate*100, int(failureRate*float64(metric.TotalTasks)), metric.TotalTasks)
				correction = "Will reduce preference for this agent and consider creating a new agent"
				mistakeDetected = true
			}
		}
	}

	// Check for inconsistent responses to similar queries
	le.checkResponseConsistency(record)

	if mistakeDetected {
		metrics := map[string]interface{}{
			"quality":  quality,
			"success":  record.Success,
			"agent":    record.AgentName,
			"query":    record.Query,
			"task_id":  record.TaskID,
			"duration": record.Duration.String(),
		}
		le.logger.LogMistake(mistake, correction, metrics)
	}
}

// checkResponseConsistency checks if responses to similar queries are consistent
func (le *LearningEngine) checkResponseConsistency(currentRecord *TaskRecord) {
	// Find similar queries
	similarTasks, err := le.QuerySimilarTasks(currentRecord.Query, 5)
	if err != nil || len(similarTasks) < 2 {
		return
	}

	// Check if different agents gave different responses to similar queries
	agentResponses := make(map[string]string)
	for _, task := range similarTasks {
		if task.Success && task.Response != "" {
			agentResponses[task.AgentName] = task.Response
		}
	}

	// If we have responses from different agents, check for significant differences
	if len(agentResponses) > 1 {
		// Simple check: if responses are very different in length
		var lengths []int
		for _, response := range agentResponses {
			lengths = append(lengths, len(response))
		}

		// Calculate variance in response lengths
		if len(lengths) > 1 {
			avgLength := 0.0
			for _, l := range lengths {
				avgLength += float64(l)
			}
			avgLength /= float64(len(lengths))

			variance := 0.0
			for _, l := range lengths {
				variance += (float64(l) - avgLength) * (float64(l) - avgLength)
			}
			variance /= float64(len(lengths))

			// If variance is high relative to average, responses are inconsistent
			if variance > avgLength*avgLength*0.5 { // High variance threshold
				mistake := fmt.Sprintf("Inconsistent responses detected for similar query: %s", currentRecord.Query)
				correction := "Will prioritize agents with consistent high-quality responses for similar queries"
				metrics := map[string]interface{}{
					"query":          currentRecord.Query,
					"response_count": len(agentResponses),
					"avg_length":     avgLength,
					"variance":       variance,
				}
				le.logger.LogMistake(mistake, correction, metrics)
			}
		}
	}
}

// queryVDBCache queries the VDB cache for similar queries
func (le *LearningEngine) queryVDBCache(query string) (string, error) {
	// Query the vector database for similar cached responses
	entries := le.vdb.Query(query, 1)
	if len(entries) == 0 {
		return "", fmt.Errorf("no cached results found")
	}

	// Return the best match (highest quality)
	bestEntry := entries[0]
	for _, entry := range entries {
		if entry.Quality > bestEntry.Quality {
			bestEntry = entry
		}
	}

	return bestEntry.Response, nil
}

// RunEvaluationCompetition runs a competition between agents for the same task
func (le *LearningEngine) RunEvaluationCompetition(query string, agentNames []string) (*CompetitionResult, error) {
	competition := &CompetitionResult{
		Query:     query,
		Timestamp: time.Now(),
		Results:   make(map[string]*TaskRecord),
	}

	// Run task on each agent
	for _, agentName := range agentNames {
		// This would need to be called from the orchestrator
		// For now, we'll simulate with existing data
		if record, exists := le.cache[query]; exists && record.AgentName == agentName {
			competition.Results[agentName] = record
		}
	}

	if len(competition.Results) == 0 {
		return nil, fmt.Errorf("no competition data available")
	}

	// Determine winner based on quality score
	var winner string
	var bestQuality float64

	for agentName, record := range competition.Results {
		if record.Quality > bestQuality {
			bestQuality = record.Quality
			winner = agentName
		}
	}

	competition.Winner = winner
	competition.Reasoning = fmt.Sprintf("Agent %s won with quality score %.2f", winner, bestQuality)

	// Log the competition result
	le.logger.LogDecision("competition_completed", fmt.Sprintf("Competition for query: %s", query), map[string]interface{}{
		"winner":       winner,
		"quality":      bestQuality,
		"participants": len(competition.Results),
	}, nil, true, nil)

	return competition, nil
}

// CompetitionResult represents the result of an agent competition
type CompetitionResult struct {
	Query     string                 `json:"query"`
	Timestamp time.Time              `json:"timestamp"`
	Results   map[string]*TaskRecord `json:"results"`
	Winner    string                 `json:"winner"`
	Reasoning string                 `json:"reasoning"`
}

// EvolvePrompts modifies SWAN's prompts based on learning experience
func (le *LearningEngine) EvolvePrompts() error {
	// Analyze recent performance to determine prompt improvements
	recentTasks := le.getRecentTasks(100)
	if len(recentTasks) < 10 {
		return nil // Not enough data for evolution
	}

	// Calculate system-wide metrics
	totalQuality := 0.0
	totalTasks := len(recentTasks)
	successCount := 0

	for _, task := range recentTasks {
		totalQuality += task.Quality
		if task.Success {
			successCount++
		}
	}

	avgQuality := totalQuality / float64(totalTasks)
	successRate := float64(successCount) / float64(totalTasks)

	// Evolve quality evaluation prompt if average quality is low
	if avgQuality < 0.7 {
		newQualityEval := le.config.SwanPrompts.QualityEval + "\n- Pay special attention to accuracy and correctness (increased weight to 0.5)"
		le.config.SwanPrompts.QualityEval = newQualityEval
		le.logger.LogDecision("prompt_evolved", "Evolved quality evaluation prompt due to low average quality", map[string]interface{}{
			"avg_quality":       avgQuality,
			"old_prompt_length": len(le.config.SwanPrompts.QualityEval) - len("\n- Pay special attention to accuracy and correctness (increased weight to 0.5)"),
			"new_prompt_length": len(newQualityEval),
		}, nil, true, nil)
	}

	// Evolve reasoning prompt if success rate is low
	if successRate < 0.8 {
		newReasoning := le.config.SwanPrompts.Reasoning + "\n- Prioritize agents with proven track records for similar tasks"
		le.config.SwanPrompts.Reasoning = newReasoning
		le.logger.LogDecision("prompt_evolved", "Evolved reasoning prompt due to low success rate", map[string]interface{}{
			"success_rate":      successRate,
			"old_prompt_length": len(le.config.SwanPrompts.Reasoning) - len("\n- Prioritize agents with proven track records for similar tasks"),
			"new_prompt_length": len(newReasoning),
		}, nil, true, nil)
	}

	// Save evolved configuration
	return config.SaveConfig(le.config, "")
}

// getRecentTasks returns the most recent N tasks
func (le *LearningEngine) getRecentTasks(limit int) []*TaskRecord {
	if len(le.tasks) <= limit {
		return le.tasks
	}
	return le.tasks[len(le.tasks)-limit:]
}

// RecordInterAgentCommunication logs communication between agents
func (le *LearningEngine) RecordInterAgentCommunication(fromAgent, toAgent, message string) error {
	networkDir := filepath.Join(le.config.WorkDir, "network")

	// Ensure network file exists for the fromAgent
	networkFile := filepath.Join(networkDir, fmt.Sprintf("%s_network.txt", fromAgent))
	file, err := os.OpenFile(networkFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open network file for %s: %v", fromAgent, err)
	}
	defer file.Close()

	// Record the communication
	entry := fmt.Sprintf("[%s] To %s: %s\n", time.Now().Format(time.RFC3339), toAgent, message)
	if _, err := file.WriteString(entry); err != nil {
		return fmt.Errorf("failed to write to network file: %v", err)
	}

	le.logger.LogDecision("inter_agent_communication", fmt.Sprintf("Agent %s communicated with %s", fromAgent, toAgent), map[string]interface{}{
		"from_agent":     fromAgent,
		"to_agent":       toAgent,
		"message_length": len(message),
	}, nil, true, nil)

	return nil
}

// GetNetworkKnowledge retrieves learned knowledge for an agent
func (le *LearningEngine) GetNetworkKnowledge(agentName string) ([]string, error) {
	networkFile := filepath.Join(le.config.WorkDir, "network", fmt.Sprintf("%s_network.txt", agentName))

	data, err := os.ReadFile(networkFile)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	var knowledge []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			knowledge = append(knowledge, line)
		}
	}

	return knowledge, nil
}

// VDB integration for caching
type CacheEntry struct {
	Query     string
	Response  string
	AgentName string
	Quality   float64
	Duration  time.Duration
	Timestamp time.Time
}

// Simple VDB implementation for caching
type SimpleVDB struct {
	Dimension int
	Entries   []CacheEntry
	Root      *KDNode
}

type KDNode struct {
	Point []float32
	Text  string
	Left  *KDNode
	Right *KDNode
	Entry *CacheEntry
}

func NewSimpleVDB(dimension int) *SimpleVDB {
	return &SimpleVDB{
		Dimension: dimension,
		Entries:   make([]CacheEntry, 0),
	}
}

func (db *SimpleVDB) computeEmbedding(text string) []float32 {
	vec := make([]float32, db.Dimension)

	re := regexp.MustCompile(`[^a-z0-9\s]+`)
	words := strings.Fields(re.ReplaceAllString(strings.ToLower(text), " "))

	localTokens := make(map[string]int)
	for _, word := range words {
		localTokens[word]++
	}

	totalDocs := len(db.Entries) + 1

	df := make(map[string]int)

	// Count tokens across all entries
	for _, entry := range db.Entries {
		entryTokens := make(map[string]int)
		entryWords := strings.Fields(re.ReplaceAllString(strings.ToLower(entry.Query+" "+entry.Response), " "))
		for _, word := range entryWords {
			entryTokens[word]++
		}
		for token := range entryTokens {
			df[token]++
		}
	}

	// Add current document
	for token := range localTokens {
		df[token]++
	}

	for token, count := range localTokens {
		tf := 1 + math.Log(float64(count))
		idf := math.Log(float64(totalDocs+1)/float64(df[token]+1)) + 1
		weight := tf * idf
		index := db.simpleHash(token) % db.Dimension
		vec[index] += float32(weight)
	}

	return normalizeVector(vec)
}

func (db *SimpleVDB) simpleHash(s string) int {
	hash := 0
	for _, c := range s {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return hash
}

func normalizeVector(vec []float32) []float32 {
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	if sum == 0 {
		return vec
	}
	norm := float32(math.Sqrt(float64(sum)))
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func insertRecursive(node *KDNode, point []float32, text string, depth int, dimension int, entry *CacheEntry) *KDNode {
	if node == nil {
		return &KDNode{
			Point: point,
			Text:  text,
			Entry: entry,
		}
	}

	cd := depth % dimension
	if point[cd] < node.Point[cd] {
		node.Left = insertRecursive(node.Left, point, text, depth+1, dimension, entry)
	} else {
		node.Right = insertRecursive(node.Right, point, text, depth+1, dimension, entry)
	}

	return node
}

func (db *SimpleVDB) Insert(entry CacheEntry) {
	if entry.Quality < 0.7 { // Only cache high-quality responses
		return
	}

	text := entry.Query + " " + entry.Response
	embedding := db.computeEmbedding(text)

	db.Entries = append(db.Entries, entry)
	db.Root = insertRecursive(db.Root, embedding, text, 0, db.Dimension, &db.Entries[len(db.Entries)-1])
}

func knnSearch(node *KDNode, query []float32, k int, dimension int) []Result {
	var results []Result
	knnSearchRecursive(node, query, k, 0, dimension, &results)

	// Sort by distance
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Distance > results[j].Distance {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > k {
		results = results[:k]
	}
	return results
}

type Result struct {
	Node     *KDNode
	Distance float32
}

func knnSearchRecursive(node *KDNode, query []float32, k int, depth int, dimension int, results *[]Result) {
	if node == nil {
		return
	}

	dist := euclideanDistance(query, node.Point)
	*results = append(*results, Result{Node: node, Distance: dist})

	cd := depth % dimension
	var first, second *KDNode
	if query[cd] < node.Point[cd] {
		first, second = node.Left, node.Right
	} else {
		first, second = node.Right, node.Left
	}

	knnSearchRecursive(first, query, k, depth+1, dimension, results)

	// Check if we need to search the other subtree
	if len(*results) < k || euclideanDistance(query, node.Point) < getKthDistance(*results, k) {
		knnSearchRecursive(second, query, k, depth+1, dimension, results)
	}

	// Keep only k closest
	if len(*results) > k {
		*results = (*results)[:k]
		for i := 0; i < len(*results)-1; i++ {
			for j := i + 1; j < len(*results); j++ {
				if (*results)[i].Distance > (*results)[j].Distance {
					(*results)[i], (*results)[j] = (*results)[j], (*results)[i]
				}
			}
		}
	}
}

func euclideanDistance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return float32(math.Sqrt(float64(sum)))
}

func getKthDistance(results []Result, k int) float32 {
	if len(results) < k {
		return math.MaxFloat32
	}
	return results[k-1].Distance
}

func (db *SimpleVDB) Query(query string, k int) []*CacheEntry {
	if db.Root == nil {
		return nil
	}

	queryVec := db.computeEmbedding(query)
	results := knnSearch(db.Root, queryVec, k, db.Dimension)

	var entries []*CacheEntry
	for _, result := range results {
		if result.Node.Entry != nil {
			entries = append(entries, result.Node.Entry)
		}
	}
	return entries
}
