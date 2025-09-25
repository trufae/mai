package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

func main() {
	configFile := flag.String("config", "", "path to config file")
	telegramToken := flag.String("telegram-token", "", "telegram bot token")
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		maxLen := 0
		flag.VisitAll(func(f *flag.Flag) {
			if len(f.Name) > maxLen {
				maxLen = len(f.Name)
			}
		})
		flag.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(os.Stderr, "  -%s%s %s\n", f.Name, strings.Repeat(" ", maxLen-len(f.Name)+2), f.Usage)
		})
	}

	flag.Parse()

	config := loadConfig(*configFile)

	// Apply overrides
	if *telegramToken != "" {
		config.Telegram.Token = *telegramToken
	}
	if *inputMethod != "" {
		config.InputMethod = *inputMethod
	}
	if *maxLength != 0 {
		config.Telegram.MaxLength = *maxLength
	}
	if *splitMessages {
		config.Telegram.SplitMessages = *splitMessages
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
		config.IRC.Server = *ircServer
	}
	if *ircPort != 0 {
		config.IRC.Port = *ircPort
	}
	if *ircTLS {
		config.IRC.TLS = *ircTLS
	}
	if *ircNick != "" {
		config.IRC.Nick = *ircNick
	}
	if *ircUser != "" {
		config.IRC.User = *ircUser
	}
	if *ircRealname != "" {
		config.IRC.Realname = *ircRealname
	}
	if *ircPassword != "" {
		config.IRC.Password = *ircPassword
	}
	if *ircChannel != "" {
		config.IRC.Channel = *ircChannel
	}
	if *ircMaxLength != 0 {
		config.IRC.MaxLength = *ircMaxLength
	}

	var wg sync.WaitGroup
	started := false

	if config.Telegram.Token != "" {
		wg.Add(1)
		started = true
		go runTelegram(config, &wg)
	}

	if config.IRC.Server != "" && config.IRC.Channel != "" {
		wg.Add(1)
		started = true
		go runIRC(config, &wg)
	}

	if !started {
		log.Fatal("No backend configured")
	}

	wg.Wait()
}
