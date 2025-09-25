package main

import (
	"flag"
	"log"
	"sync"
)

func main() {
	configFile := flag.String("config", "config.json", "path to config file")
	token := flag.String("token", "", "telegram bot token")
	inputMethod := flag.String("input-method", "", "input method")
	maxLength := flag.Int("max-length", 0, "max message length")
	splitMessages := flag.Bool("split-messages", false, "split long messages")
	captureStderr := flag.Bool("capture-stderr", false, "capture stderr")
	logfile := flag.String("logfile", "", "log file path")
	logToStdout := flag.Bool("log-to-stdout", false, "log to stdout")
	ircServer := flag.String("irc-server", "", "IRC server")
	ircPort := flag.Int("irc-port", 0, "IRC port")
	ircTLS := flag.Bool("irc-tls", false, "use IRC TLS")
	ircNick := flag.String("irc-nick", "", "IRC nickname")
	ircUser := flag.String("irc-user", "", "IRC username")
	ircRealname := flag.String("irc-realname", "", "IRC realname")
	ircPassword := flag.String("irc-password", "", "IRC password")
	ircChannel := flag.String("irc-channel", "", "IRC channel")
	ircMaxLength := flag.Int("irc-max-length", 0, "IRC max message length")

	flag.Parse()

	config := loadConfig(*configFile)

	// Apply overrides
	if *token != "" {
		config.Token = *token
	}
	if *inputMethod != "" {
		config.InputMethod = *inputMethod
	}
	if *maxLength != 0 {
		config.MaxLength = *maxLength
	}
	if *splitMessages {
		config.SplitMessages = *splitMessages
	}
	if *captureStderr {
		config.CaptureStderr = *captureStderr
	}
	if *logfile != "" {
		config.Logfile = *logfile
	}
	if *logToStdout {
		config.LogToStdout = *logToStdout
	}
	if *ircServer != "" {
		config.IrcServer = *ircServer
	}
	if *ircPort != 0 {
		config.IrcPort = *ircPort
	}
	if *ircTLS {
		config.IrcTLS = *ircTLS
	}
	if *ircNick != "" {
		config.IrcNick = *ircNick
	}
	if *ircUser != "" {
		config.IrcUser = *ircUser
	}
	if *ircRealname != "" {
		config.IrcRealname = *ircRealname
	}
	if *ircPassword != "" {
		config.IrcPassword = *ircPassword
	}
	if *ircChannel != "" {
		config.IrcChannel = *ircChannel
	}
	if *ircMaxLength != 0 {
		config.IrcMaxLength = *ircMaxLength
	}

	var wg sync.WaitGroup
	started := false

	if config.Token != "" {
		wg.Add(1)
		started = true
		go runTelegram(config, &wg)
	}

	if config.IrcServer != "" && config.IrcChannel != "" {
		wg.Add(1)
		started = true
		go runIRC(config, &wg)
	}

	if !started {
		log.Fatal("No backend configured")
	}

	wg.Wait()
}
