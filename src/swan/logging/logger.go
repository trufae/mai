package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DecisionLog represents a logged decision or action taken by SWAN
type DecisionLog struct {
	Timestamp time.Time              `json:"timestamp"`
	Action    string                 `json:"action"`  // e.g., "agent_created", "config_updated", "cache_hit"
	Reason    string                 `json:"reason"`  // Why this decision was made
	Metrics   map[string]interface{} `json:"metrics"` // Relevant metrics (time, quality, etc.)
	Details   map[string]interface{} `json:"details"` // Additional context
	Success   bool                   `json:"success"` // Whether the action succeeded
	Error     string                 `json:"error,omitempty"`
}

// Logger manages logging of SWAN decisions and actions
type Logger struct {
	logFile    *os.File
	configPath string
}

// NewLogger creates a new logger instance
func NewLogger(workDir string) (*Logger, error) {
	logPath := filepath.Join(workDir, "tmp", "swan_decisions.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}

	return &Logger{
		logFile:    logFile,
		configPath: filepath.Join(workDir, "evolved_config.yaml"),
	}, nil
}

// LogDecision logs a decision or action taken by SWAN
func (l *Logger) LogDecision(action, reason string, metrics, details map[string]interface{}, success bool, err error) error {
	log := DecisionLog{
		Timestamp: time.Now(),
		Action:    action,
		Reason:    reason,
		Metrics:   metrics,
		Details:   details,
		Success:   success,
	}

	if err != nil {
		log.Error = err.Error()
	}

	data, jsonErr := json.Marshal(log)
	if jsonErr != nil {
		return fmt.Errorf("failed to marshal log: %v", jsonErr)
	}

	_, writeErr := l.logFile.WriteString(string(data) + "\n")
	if writeErr != nil {
		return fmt.Errorf("failed to write log: %v", writeErr)
	}

	return nil
}

// LogAgentCreation logs the creation of a new agent
func (l *Logger) LogAgentCreation(agentName, provider string, reason string) error {
	details := map[string]interface{}{
		"agent_name": agentName,
		"provider":   provider,
	}
	return l.LogDecision("agent_created", reason, nil, details, true, nil)
}

// LogConfigUpdate logs an update to the configuration
func (l *Logger) LogConfigUpdate(changes string, reason string) error {
	details := map[string]interface{}{
		"changes": changes,
	}
	return l.LogDecision("config_updated", reason, nil, details, true, nil)
}

// LogCacheOperation logs a caching operation
func (l *Logger) LogCacheOperation(operation, query string, hit bool, timeSaved time.Duration) error {
	metrics := map[string]interface{}{
		"cache_hit":  hit,
		"time_saved": timeSaved.String(),
	}
	details := map[string]interface{}{
		"operation": operation,
		"query":     query,
	}
	return l.LogDecision("cache_operation", fmt.Sprintf("Cache %s for query", operation), metrics, details, true, nil)
}

// LogMistake logs when SWAN detects and corrects a mistake
func (l *Logger) LogMistake(mistake, correction string, metrics map[string]interface{}) error {
	details := map[string]interface{}{
		"mistake":    mistake,
		"correction": correction,
	}
	return l.LogDecision("mistake_corrected", "Detected and corrected a mistake", metrics, details, true, nil)
}

// Close closes the logger
func (l *Logger) Close() error {
	return l.logFile.Close()
}
