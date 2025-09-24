package main

import (
	"encoding/json"
	"log"
	"os"
)

type Config struct {
	Token         string   `json:"token"`
	Program       []string `json:"program"`
	InputMethod   string   `json:"input_method"`
	MaxLength     int      `json:"max_length,omitempty"`
	SplitMessages bool     `json:"split_messages,omitempty"`
	CaptureStderr bool     `json:"capture_stderr,omitempty"`
	Logfile       string   `json:"logfile,omitempty"`
	LogToStdout   bool     `json:"log_to_stdout,omitempty"`

	IrcServer    string `json:"irc_server,omitempty"`
	IrcPort      int    `json:"irc_port,omitempty"`
	IrcTLS       bool   `json:"irc_tls,omitempty"`
	IrcNick      string `json:"irc_nick,omitempty"`
	IrcUser      string `json:"irc_user,omitempty"`
	IrcRealname  string `json:"irc_realname,omitempty"`
	IrcPassword  string `json:"irc_password,omitempty"`
	IrcChannel   string `json:"irc_channel,omitempty"`
	IrcMaxLength int    `json:"irc_max_length,omitempty"`
}

func loadConfig() Config {
	configFile, err := os.Open("config.json")
	if err != nil {
		log.Fatal("Error opening config.json:", err)
	}
	defer configFile.Close()

	var config Config
	decoder := json.NewDecoder(configFile)
	if err := decoder.Decode(&config); err != nil {
		log.Fatal("Error decoding config.json:", err)
	}
	return config
}
