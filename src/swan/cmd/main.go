package main

import (
	"flag"
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
	var cfg *config.SwanConfig
	var err error
	cfg, err = config.LoadConfig(*configPath)
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
	// Initialize daemon manager
	daemonMgr := daemon.NewDaemonManager(cfg)

	// Load existing agents
	if err = daemonMgr.LoadAgents(); err != nil {
		log.Printf("Warning: failed to load agents: %v", err)
	}

	// Start all agents from config
	if err = daemonMgr.StartAllAgents(); err != nil {
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

	// Wait a bit for server to start
	time.Sleep(2 * time.Second)

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
