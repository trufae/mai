package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mai/src/swan/config"
	"mai/src/swan/daemon"
	"mai/src/swan/orchestrator"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: swan <config-file>")
		fmt.Println("Example: swan swan.yaml")
		os.Exit(1)
	}

	configPath := os.Args[1]

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Starting SWAN with config: %s\n", configPath)
	fmt.Printf("Work directory: %s\n", cfg.WorkDir)

	// Initialize daemon manager
	daemonMgr := daemon.NewDaemonManager(cfg)

	// Load existing agents
	if err := daemonMgr.LoadAgents(); err != nil {
		log.Printf("Warning: failed to load agents: %v", err)
	}

	// Start all agents from config
	if err := daemonMgr.StartAllAgents(); err != nil {
		log.Printf("Warning: failed to start some agents: %v", err)
	}

	// Initialize orchestrator server with learning engine
	orchServer := orchestrator.NewOrchestratorServer(cfg, daemonMgr)

	// Start orchestrator server in background
	go func() {
		if err := orchServer.Start(); err != nil {
			log.Fatalf("Failed to start orchestrator server: %v", err)
		}
	}()

	// Start autonomous evolution in background
	go func() {
		ticker := time.NewTicker(1 * time.Hour) // Evolve every hour
		defer ticker.Stop()

		for range ticker.C {
			if err := orchServer.TriggerEvolution(); err != nil {
				log.Printf("Warning: failed to evolve prompts: %v", err)
			}
		}
	}()

	fmt.Println("SWAN orchestrator started successfully")
	fmt.Printf("OpenAI endpoint available at: http://%s:%d/v1/chat/completions\n", cfg.Orchestrator.ListenAddr, cfg.Orchestrator.Port)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down SWAN...")

	// Stop orchestrator server
	if err := orchServer.Stop(); err != nil {
		log.Printf("Error stopping orchestrator server: %v", err)
	}

	// Stop all agents
	if err := daemonMgr.StopAllAgents(); err != nil {
		log.Printf("Error stopping agents: %v", err)
	}

	fmt.Println("SWAN shutdown complete")
}
