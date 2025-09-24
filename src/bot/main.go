package main

import (
	"log"
	"sync"
)

func main() {
	config := loadConfig()

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
