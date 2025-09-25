package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type TelegramConfig struct {
	Token         string `json:"token"`
	MaxLength     int    `json:"max_length,omitempty"`
	SplitMessages bool   `json:"split_messages,omitempty"`
}

type IRCConfig struct {
	Server    string `json:"server,omitempty"`
	Port      int    `json:"port,omitempty"`
	TLS       bool   `json:"tls,omitempty"`
	Nick      string `json:"nick,omitempty"`
	User      string `json:"user,omitempty"`
	Realname  string `json:"realname,omitempty"`
	Password  string `json:"password,omitempty"`
	Channel   string `json:"channel,omitempty"`
	MaxLength int    `json:"max_length,omitempty"`
}

type Config struct {
	Program       []string `json:"program"`
	InputMethod   string   `json:"input_method"`
	CaptureStderr bool     `json:"capture_stderr,omitempty"`
	Logfile       string   `json:"logfile,omitempty"`
	LogToStdout   bool     `json:"log_to_stdout,omitempty"`

	Telegram TelegramConfig `json:"telegram"`
	IRC      IRCConfig      `json:"irc"`
}

func loadConfig(filename string) Config {
	if filename == "" {
		// Default to config.json in the executable's directory or cwd
		execPath, err := os.Executable()
		if err == nil {
			// Follow symlink to resolve the actual executable path
			if resolvedPath, err := filepath.EvalSymlinks(execPath); err == nil {
				execPath = resolvedPath
			}
			filename = filepath.Join(filepath.Dir(execPath), "config.json")
		} else {
			filename = "config.json"
		}
	}
	configFile, err := os.Open(filename)
	if err != nil {
		log.Fatal("Error opening config file:", err)
	}
	defer configFile.Close()

	var config Config
	decoder := json.NewDecoder(configFile)
	if err := decoder.Decode(&config); err != nil {
		log.Fatal("Error decoding config file:", err)
	}
	return config
}
