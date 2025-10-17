package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mai/src/swan/config"
	"mai/src/swan/daemon"
	"mai/src/swan/orchestrator"
)

func main() {
	var debug = flag.Bool("d", false, "enable debug mode")
	var workdir = flag.String("w", "/tmp/swandb", "work directory for swan")
	var configPath = flag.String("c", "swan.yaml", "configuration file")
	flag.Parse()

	if flag.NArg() > 0 {
		fmt.Println("Usage: swan [options]")
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("Example: swan")
		fmt.Println("Example: swan -d -w /tmp/mywork -c myconfig.yaml")
		os.Exit(1)
	}

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Override work directory
	cfg.WorkDir = *workdir

	fmt.Printf("Starting SWAN with config: %s\n", *configPath)
	fmt.Printf("Work directory: %s\n", cfg.WorkDir)
	if *debug {
		fmt.Printf("Debug mode enabled\n")
		fmt.Printf("Orchestrator port: %d\n", cfg.Orchestrator.Port)
		fmt.Printf("Orchestrator listen addr: %s\n", cfg.Orchestrator.ListenAddr)
		fmt.Printf("VDB path: %s\n", cfg.Orchestrator.VDBPath)
	}
	fmt.Printf("DEBUG: Initializing daemon manager...\n")

	// Initialize daemon manager
	fmt.Printf("DEBUG: Creating daemon manager...\n")
	daemonMgr := daemon.NewDaemonManager(cfg)

	// Load existing agents
	fmt.Printf("DEBUG: Loading existing agents...\n")
	if err := daemonMgr.LoadAgents(); err != nil {
		log.Printf("Warning: failed to load agents: %v", err)
	}

	// Start all agents from config
	fmt.Printf("DEBUG: Starting agents from config...\n")
	if err := daemonMgr.StartAllAgents(); err != nil {
		log.Printf("Warning: failed to start some agents: %v", err)
	}

	// Initialize orchestrator server with learning engine
	fmt.Printf("DEBUG: Creating orchestrator server...\n")
	orchServer := orchestrator.NewOrchestratorServer(cfg, daemonMgr)

	// Start orchestrator server in background
	fmt.Printf("DEBUG: Starting orchestrator server in background...\n")
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

	// Wait a bit for server to start
	fmt.Printf("DEBUG: Waiting for server to start...\n")
	time.Sleep(2 * time.Second)

	// Health check
	fmt.Printf("DEBUG: Performing health check...\n")
	healthURL := fmt.Sprintf("http://%s:%d/health", cfg.Orchestrator.ListenAddr, cfg.Orchestrator.Port)
	resp, err := http.Get(healthURL)
	if err != nil {
		log.Printf("Warning: orchestrator health check failed: %v", err)
		log.Printf("Check if port %d is available and not blocked by firewall", cfg.Orchestrator.Port)
		fmt.Printf("DEBUG: Health check failed, but continuing...\n")
	} else {
		resp.Body.Close()
		fmt.Println("SWAN orchestrator started successfully")
		fmt.Printf("OpenAI endpoint: http://%s:%d/v1/chat/completions\n", cfg.Orchestrator.ListenAddr, cfg.Orchestrator.Port)
		fmt.Printf("Ollama endpoint: http://%s:%d/api/generate\n", cfg.Orchestrator.ListenAddr, cfg.Orchestrator.Port)
		fmt.Printf("Models list: http://%s:%d/api/tags\n", cfg.Orchestrator.ListenAddr, cfg.Orchestrator.Port)
		fmt.Printf("Health check: %s\n", healthURL)
		fmt.Printf("Root status: http://%s:%d/\n", cfg.Orchestrator.ListenAddr, cfg.Orchestrator.Port)
		fmt.Printf("DEBUG: Health check passed\n")
	}

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
