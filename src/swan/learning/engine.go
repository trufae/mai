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

// LearningDataset represents a structured dataset entry for ML training
type LearningDataset struct {
	QueryID            string                 `json:"query_id"`
	Query              string                 `json:"query"`
	QueryFeatures      map[string]interface{} `json:"query_features"` // tone, length, complexity, etc.
	AgentName          string                 `json:"agent_name"`
	Response           string                 `json:"response"`
	ResponseQuality    float64                `json:"response_quality"`
	Duration           time.Duration          `json:"duration"`
	Success            bool                   `json:"success"`
	Error              string                 `json:"error,omitempty"`
	Timestamp          time.Time              `json:"timestamp"`
	CompetitionResults []CompetitionEntry     `json:"competition_results,omitempty"`
}

// CompetitionEntry represents a single agent's performance in a competition
type CompetitionEntry struct {
	AgentName string        `json:"agent_name"`
	Response  string        `json:"response"`
	Quality   float64       `json:"quality"`
	Duration  time.Duration `json:"duration"`
	Winner    bool          `json:"winner"`
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
	dataset      []*LearningDataset  // Structured dataset for learning
	datasetFile  string              // Path to dataset JSON file
	idleLearning bool                // Whether idle learning is active
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

	// Initialize dataset directory
	datasetDir := filepath.Join(cfg.WorkDir, "dataset")
	if err := os.MkdirAll(datasetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create dataset directory: %v", err)
	}

	datasetFile := filepath.Join(datasetDir, "learning_dataset.json")

	engine := &LearningEngine{
		config:       cfg,
		metrics:      make(map[string]*PerformanceMetric),
		tasks:        make([]*TaskRecord, 0),
		cache:        make(map[string]*TaskRecord),
		logger:       logger,
		vdbPath:      vdbPath,
		networkFiles: make(map[string]*os.File),
		vdb:          NewSimpleVDB(64), // 64-dimensional vectors
		dataset:      make([]*LearningDataset, 0),
		datasetFile:  datasetFile,
		idleLearning: true,
	}

	// Load existing cache from disk
	engine.loadCacheFromDisk()

	// Load existing dataset from disk
	engine.loadDatasetFromDisk()

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

	// Add to structured learning dataset
	le.addToDataset(record)

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

// addToDataset adds a task record to the structured learning dataset
func (le *LearningEngine) addToDataset(record *TaskRecord) {
	// Extract query features for ML training
	queryFeatures := le.extractQueryFeatures(record.Query)

	datasetEntry := &LearningDataset{
		QueryID:         record.TaskID,
		Query:           record.Query,
		QueryFeatures:   queryFeatures,
		AgentName:       record.AgentName,
		Response:        record.Response,
		ResponseQuality: record.Quality,
		Duration:        record.Duration,
		Success:         record.Success,
		Error:           record.Error,
		Timestamp:       record.Timestamp,
	}

	le.dataset = append(le.dataset, datasetEntry)

	// Save dataset to disk periodically (every 10 entries)
	if len(le.dataset)%10 == 0 {
		le.SaveDatasetToDisk()
	}
}

// extractQueryFeatures extracts features from a query for ML training
func (le *LearningEngine) extractQueryFeatures(query string) map[string]interface{} {
	features := make(map[string]interface{})

	// Basic features
	features["length"] = len(query)
	features["word_count"] = len(strings.Fields(query))

	// Tone analysis (reuse existing logic)
	tone, confidence := le.analyzeToneForFeatures(query)
	features["tone"] = tone
	features["tone_confidence"] = confidence

	// Complexity indicators
	features["has_question"] = strings.Contains(query, "?")
	features["has_exclamation"] = strings.Contains(query, "!")
	features["uppercase_ratio"] = le.calculateUppercaseRatio(query)

	// Keyword presence
	keywords := []string{"code", "debug", "explain", "search", "write", "test", "calculate", "analyze"}
	for _, keyword := range keywords {
		features["has_"+keyword] = strings.Contains(strings.ToLower(query), keyword)
	}

	return features
}

// analyzeToneForFeatures analyzes tone for feature extraction (similar to orchestrator's analyzeTone)
func (le *LearningEngine) analyzeToneForFeatures(query string) (string, float64) {
	query = strings.ToLower(query)

	// Aggressive/frustrated indicators
	aggressiveWords := []string{"stupid", "idiot", "useless", "broken", "sucks", "hate", "worst", "terrible", "awful", "damn", "hell"}
	correctionWords := []string{"wrong", "incorrect", "mistake", "error", "fix", "should be", "not working"}
	urgentWords := []string{"urgent", "asap", "immediately", "right now", "quickly"}
	informationalWords := []string{"what", "how", "explain", "tell me", "show me", "can you"}

	aggressiveScore := 0
	correctionScore := 0
	urgentScore := 0
	informationalScore := 0

	words := strings.Fields(query)
	for _, word := range words {
		for _, aw := range aggressiveWords {
			if strings.Contains(word, aw) {
				aggressiveScore++
			}
		}
		for _, cw := range correctionWords {
			if strings.Contains(word, cw) {
				correctionScore++
			}
		}
		for _, uw := range urgentWords {
			if strings.Contains(word, uw) {
				urgentScore++
			}
		}
		for _, iw := range informationalWords {
			if strings.Contains(word, iw) {
				informationalScore++
			}
		}
	}

	// Check for exclamation marks and question marks
	exclamationCount := strings.Count(query, "!")
	questionCount := strings.Count(query, "?")

	// Determine dominant tone
	scores := []float64{float64(aggressiveScore), float64(correctionScore), float64(urgentScore), float64(informationalScore)}
	maxScore := scores[0]
	for _, score := range scores {
		if score > maxScore {
			maxScore = score
		}
	}

	if aggressiveScore > 0 && exclamationCount > 1 {
		return "aggressive", float64(aggressiveScore) / float64(len(words))
	} else if correctionScore > 0 {
		return "corrective", float64(correctionScore) / float64(len(words))
	} else if urgentScore > 0 || exclamationCount > 0 {
		return "urgent", float64(urgentScore+exclamationCount) / float64(len(words)+1)
	} else if informationalScore > 0 || questionCount > 0 {
		return "informational", float64(informationalScore+questionCount) / float64(len(words)+1)
	}

	return "neutral", 0.5
}

// calculateUppercaseRatio calculates the ratio of uppercase characters
func (le *LearningEngine) calculateUppercaseRatio(text string) float64 {
	if len(text) == 0 {
		return 0.0
	}

	uppercaseCount := 0
	for _, char := range text {
		if char >= 'A' && char <= 'Z' {
			uppercaseCount++
		}
	}

	return float64(uppercaseCount) / float64(len(text))
}

// SaveDatasetToDisk saves the learning dataset to disk
func (le *LearningEngine) SaveDatasetToDisk() error {
	data, err := json.MarshalIndent(le.dataset, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal dataset: %v", err)
	}

	if err := os.WriteFile(le.datasetFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write dataset file: %v", err)
	}

	le.logger.LogDecision("dataset_saved", fmt.Sprintf("Saved %d dataset entries to disk", len(le.dataset)), map[string]interface{}{
		"entries_saved": len(le.dataset),
	}, nil, true, nil)

	return nil
}

// GetLogger returns the logger instance
func (le *LearningEngine) GetLogger() *logging.Logger {
	return le.logger
}

// StartIdleLearning starts the idle-time learning process
func (le *LearningEngine) StartIdleLearning(daemonMgr interface{}) {
	le.idleLearning = true
	go le.idleLearningLoop(daemonMgr)
}

// StopIdleLearning stops the idle-time learning process
func (le *LearningEngine) StopIdleLearning() {
	le.idleLearning = false
}

// idleLearningLoop runs the idle learning process
func (le *LearningEngine) idleLearningLoop(daemonMgr interface{}) {
	ticker := time.NewTicker(5 * time.Minute) // Check every 5 minutes
	defer ticker.Stop()

	for le.idleLearning {
		select {
		case <-ticker.C:
			le.performIdleLearning(daemonMgr)
		}
	}
}

// performIdleLearning performs learning tasks when the system is idle
func (le *LearningEngine) performIdleLearning(daemonMgr interface{}) {
	// Only run if we have enough data and agents
	if len(le.dataset) < 10 {
		return // Not enough data yet
	}

	// Find queries that haven't been tested with all available agents
	le.findUntestedQueryAgentCombinations(daemonMgr)
}

// findUntestedQueryAgentCombinations finds queries that could benefit from testing with other agents
func (le *LearningEngine) findUntestedQueryAgentCombinations(daemonMgr interface{}) {
	// This implementation performs actual query replay between agents during idle periods

	// Get available agents (this is a simplified approach - in practice we'd get this from daemonMgr)
	// For now, we'll work with the agents we have data for
	agentSet := make(map[string]bool)
	for _, entry := range le.dataset {
		agentSet[entry.AgentName] = true
	}

	availableAgents := make([]string, 0, len(agentSet))
	for agent := range agentSet {
		availableAgents = append(availableAgents, agent)
	}

	if len(availableAgents) < 2 {
		le.logger.LogDecision("idle_learning_skip", "Not enough agents for idle learning", map[string]interface{}{
			"available_agents": len(availableAgents),
		}, nil, true, nil)
		return
	}

	// Build query-agent testing matrix
	queryAgentMap := make(map[string]map[string]bool)
	for _, entry := range le.dataset {
		if queryAgentMap[entry.Query] == nil {
			queryAgentMap[entry.Query] = make(map[string]bool)
		}
		queryAgentMap[entry.Query][entry.AgentName] = true
	}

	// Find queries that could benefit from testing with other agents
	tasksPerformed := 0
	maxTasksPerCycle := 3 // Limit to prevent overwhelming the system

	for query, testedAgents := range queryAgentMap {
		if tasksPerformed >= maxTasksPerCycle {
			break
		}

		// Find agents that haven't tested this query
		for _, agentName := range availableAgents {
			if tasksPerformed >= maxTasksPerCycle {
				break
			}

			if !testedAgents[agentName] {
				// This agent hasn't tested this query - perform idle learning task
				le.performIdleQueryReplay(query, agentName)
				tasksPerformed++
			}
		}
	}

	le.logger.LogDecision("idle_learning_cycle", "Completed idle learning cycle", map[string]interface{}{
		"dataset_size":     len(le.dataset),
		"cache_size":       len(le.cache),
		"tasks_performed":  tasksPerformed,
		"available_agents": len(availableAgents),
	}, nil, true, nil)
}

// performIdleQueryReplay performs a query replay task with a specific agent during idle learning
func (le *LearningEngine) performIdleQueryReplay(query, agentName string) {
	// Create a simulated task record for idle learning
	// In a real implementation, this would call back to the orchestrator to execute the task
	// For now, we'll simulate the learning by analyzing existing patterns

	// Find similar queries that this agent has handled well
	var similarResponses []*LearningDataset
	for _, entry := range le.dataset {
		if entry.AgentName == agentName && le.calculateQuerySimilarity(query, entry.Query) > 0.7 {
			similarResponses = append(similarResponses, entry)
		}
	}

	// Predict quality based on similar past performance
	predictedQuality := 0.5
	if len(similarResponses) > 0 {
		totalQuality := 0.0
		for _, resp := range similarResponses {
			totalQuality += resp.ResponseQuality
		}
		predictedQuality = totalQuality / float64(len(similarResponses))
	}

	// Simulate task execution time based on agent performance
	simulatedDuration := 5 * time.Second
	if agentMetrics, exists := le.metrics[agentName]; exists {
		simulatedDuration = agentMetrics.AvgDuration
	}

	// Create a learning record for this idle learning task
	record := &TaskRecord{
		TaskID:    fmt.Sprintf("idle-%d", time.Now().UnixNano()),
		AgentName: agentName,
		Query:     query,
		Response:  fmt.Sprintf("[IDLE LEARNING] Predicted response quality: %.2f", predictedQuality),
		Duration:  simulatedDuration,
		Success:   predictedQuality > 0.6, // Consider successful if predicted quality is good
		Error:     "",
		Timestamp: time.Now(),
	}

	// Assess actual quality and record
	quality := le.AssessQuality(record)
	record.Quality = quality

	le.RecordTask(record)

	le.logger.LogDecision("idle_query_replay", fmt.Sprintf("Performed idle query replay for agent %s", agentName), map[string]interface{}{
		"query":             query,
		"agent":             agentName,
		"predicted_quality": predictedQuality,
		"actual_quality":    quality,
		"similar_responses": len(similarResponses),
	}, nil, true, nil)
}

// calculateQuerySimilarity calculates similarity between two queries (simple implementation)
func (le *LearningEngine) calculateQuerySimilarity(query1, query2 string) float64 {
	if query1 == query2 {
		return 1.0
	}

	words1 := strings.Fields(strings.ToLower(query1))
	words2 := strings.Fields(strings.ToLower(query2))

	if len(words1) == 0 || len(words2) == 0 {
		return 0.0
	}

	// Simple Jaccard similarity
	wordSet1 := make(map[string]bool)
	wordSet2 := make(map[string]bool)

	for _, word := range words1 {
		if len(word) > 2 { // Ignore very short words
			wordSet1[word] = true
		}
	}
	for _, word := range words2 {
		if len(word) > 2 {
			wordSet2[word] = true
		}
	}

	intersection := 0
	for word := range wordSet1 {
		if wordSet2[word] {
			intersection++
		}
	}

	union := len(wordSet1) + len(wordSet2) - intersection
	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// GetDataset returns the current learning dataset
func (le *LearningEngine) GetDataset() []*LearningDataset {
	return le.dataset
}

// GetDatasetStats returns statistics about the learning dataset
func (le *LearningEngine) GetDatasetStats() map[string]interface{} {
	stats := map[string]interface{}{
		"total_entries":  len(le.dataset),
		"unique_queries": le.countUniqueQueries(),
		"agents_covered": le.countUniqueAgents(),
		"avg_quality":    le.calculateAverageQuality(),
		"date_range":     le.getDateRange(),
	}

	return stats
}

// countUniqueQueries counts unique queries in the dataset
func (le *LearningEngine) countUniqueQueries() int {
	querySet := make(map[string]bool)
	for _, entry := range le.dataset {
		querySet[entry.Query] = true
	}
	return len(querySet)
}

// countUniqueAgents counts unique agents in the dataset
func (le *LearningEngine) countUniqueAgents() int {
	agentSet := make(map[string]bool)
	for _, entry := range le.dataset {
		agentSet[entry.AgentName] = true
	}
	return len(agentSet)
}

// calculateAverageQuality calculates the average quality score in the dataset
func (le *LearningEngine) calculateAverageQuality() float64 {
	if len(le.dataset) == 0 {
		return 0.0
	}

	totalQuality := 0.0
	for _, entry := range le.dataset {
		totalQuality += entry.ResponseQuality
	}

	return totalQuality / float64(len(le.dataset))
}

// getDateRange returns the date range of the dataset
func (le *LearningEngine) getDateRange() map[string]string {
	if len(le.dataset) == 0 {
		return map[string]string{"start": "", "end": ""}
	}

	earliest := le.dataset[0].Timestamp
	latest := le.dataset[0].Timestamp

	for _, entry := range le.dataset {
		if entry.Timestamp.Before(earliest) {
			earliest = entry.Timestamp
		}
		if entry.Timestamp.After(latest) {
			latest = entry.Timestamp
		}
	}

	return map[string]string{
		"start": earliest.Format(time.RFC3339),
		"end":   latest.Format(time.RFC3339),
	}
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

// loadDatasetFromDisk loads the learning dataset from disk
func (le *LearningEngine) loadDatasetFromDisk() {
	data, err := os.ReadFile(le.datasetFile)
	if err != nil {
		if !os.IsNotExist(err) {
			le.logger.LogDecision("dataset_load_error", fmt.Sprintf("Failed to load dataset from disk: %v", err), nil, nil, false, err)
		}
		return
	}

	var dataset []*LearningDataset
	if err := json.Unmarshal(data, &dataset); err != nil {
		le.logger.LogDecision("dataset_load_error", fmt.Sprintf("Failed to parse dataset JSON: %v", err), nil, nil, false, err)
		return
	}

	le.dataset = dataset
	le.logger.LogDecision("dataset_loaded", fmt.Sprintf("Loaded %d dataset entries from disk", len(dataset)), map[string]interface{}{
		"entries_loaded": len(dataset),
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

// AssessQuality assesses the quality of a response using advanced trustable model evaluation (0-1 scale)
func (le *LearningEngine) AssessQuality(record *TaskRecord) float64 {
	quality := 0.5 // Base quality

	// Success factor - most important
	if record.Success {
		quality += 0.3
	} else {
		quality -= 0.4 // Failed tasks get heavily penalized
	}

	// Response characteristics analysis
	responseLen := len(record.Response)
	if responseLen > 100 {
		quality += 0.1 // Substantial responses
	} else if responseLen < 10 {
		quality -= 0.1 // Too brief might indicate incomplete response
	}

	// Error analysis
	if record.Error != "" {
		// Different error types have different penalties
		if strings.Contains(record.Error, "timeout") || strings.Contains(record.Error, "cancelled") {
			quality -= 0.2 // Timeout errors
		} else {
			quality -= 0.3 // Other errors
		}
	}

	// Time-based quality assessment with trustable model
	durationSec := record.Duration.Seconds()
	if durationSec < 2 {
		quality -= 0.15 // Suspiciously fast - might be low quality
	} else if durationSec < 5 {
		quality += 0.05 // Reasonable speed
	} else if durationSec > 60 {
		quality -= 0.1 // Too slow, but not as bad as errors
	} else if durationSec > 30 {
		quality += 0.05 // Thorough responses get slight bonus
	}

	// Trustable model: Compare against historical performance of this agent
	if agentMetrics, exists := le.metrics[record.AgentName]; exists && agentMetrics.TotalTasks > 5 {
		// Adjust quality based on agent's historical success rate
		historicalSuccessRate := agentMetrics.SuccessRate
		if record.Success && historicalSuccessRate > 0.8 {
			quality += 0.05 // Consistent performer bonus
		} else if !record.Success && historicalSuccessRate < 0.5 {
			quality += 0.05 // Expected failure, less penalty
		}

		// Adjust based on response time vs historical average
		if agentMetrics.AvgDuration > 0 {
			timeRatio := durationSec / agentMetrics.AvgDuration.Seconds()
			if timeRatio < 0.5 {
				quality += 0.05 // Faster than usual - good
			} else if timeRatio > 2.0 {
				quality -= 0.05 // Slower than usual - concerning
			}
		}
	}

	// Query complexity adjustment
	queryLen := len(record.Query)
	if queryLen > 200 {
		// Complex queries should have more detailed responses
		if responseLen < queryLen/2 {
			quality -= 0.1 // Response too short for complex query
		}
	} else if queryLen < 20 {
		// Simple queries can have shorter responses
		if responseLen > 500 {
			quality -= 0.05 // Overly verbose for simple query
		}
	}

	// Content quality indicators
	response := strings.ToLower(record.Response)
	query := strings.ToLower(record.Query)

	// Check if response addresses the query
	queryWords := strings.Fields(query)
	responseWords := strings.Fields(response)
	commonWords := 0
	for _, qWord := range queryWords {
		if len(qWord) > 3 { // Only check meaningful words
			for _, rWord := range responseWords {
				if strings.Contains(rWord, qWord) || strings.Contains(qWord, rWord) {
					commonWords++
					break
				}
			}
		}
	}

	if len(queryWords) > 0 {
		relevanceScore := float64(commonWords) / float64(len(queryWords))
		quality += (relevanceScore - 0.5) * 0.2 // Adjust quality based on relevance
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
