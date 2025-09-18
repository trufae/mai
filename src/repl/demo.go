package main

import (
	"fmt"
	"sync"
	"time"
)

var (
	mu          sync.Mutex
	running     bool
	message     string
	stopChannel chan struct{}
	spinner     = []rune{'—', '\\', '|', '/'}
)

// startLoop starts the waiting animation in a separate goroutine.
func startLoop(initialMessage string) {
	mu.Lock()
	if running {
		mu.Unlock()
		return
	}
	running = true
	message = initialMessage
	stopChannel = make(chan struct{})
	mu.Unlock()

	go func() {
		i := 0
		for {
			select {
			case <-stopChannel:
				// Clear the line before exiting
				fmt.Print("" + string(make([]rune, len(message)+3)) + "")
				return
			default:
				mu.Lock()
				fmt.Printf("%c %s", spinner[i%len(spinner)], message)
				mu.Unlock()
				i++
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()
}

// addMoreText appends text to the current waiting message.
func addMoreText(text string) {
	mu.Lock()
	message += text
	mu.Unlock()
}

// stopLoop stops the waiting animation goroutine.
func stopLoop() {
	mu.Lock()
	if !running {
		mu.Unlock()
		return
	}
	running = false
	close(stopChannel)
	mu.Unlock()
	fmt.Println()
}
