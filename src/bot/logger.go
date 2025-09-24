package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

type logEntry struct {
	Timestamp  string `json:"timestamp"`
	UserID     int64  `json:"user_id"`
	Username   string `json:"username"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	ChatID     int64  `json:"chat_id"`
	Query      string `json:"query"`
	Response   string `json:"response"`
	ReturnCode int    `json:"return_code"`
}

func logInteraction(config Config, entry logEntry) {
	if config.Logfile == "" && !config.LogToStdout {
		return
	}

	entry.Timestamp = time.Now().Format(time.RFC3339)

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Error marshaling log entry: %v", err)
		return
	}

	if config.Logfile != "" {
		file, fileErr := os.OpenFile(config.Logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if fileErr == nil {
			if _, writeErr := file.Write(append(data, '\n')); writeErr != nil {
				log.Printf("Error writing logfile: %v", writeErr)
			}
			_ = file.Close()
		} else {
			log.Printf("Error opening logfile: %v", fileErr)
		}
	}

	if config.LogToStdout {
		fmt.Println(string(data))
	}
}
