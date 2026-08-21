package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"deltachatmcp/lib"
	"mcplib"
)

func main() {
	accountManagement, err := environmentBool("DELTACHAT_ACCOUNT_MANAGEMENT", true)
	if err != nil {
		log.Fatal(err)
	}

	listen := flag.String("l", os.Getenv("DELTACHAT_LISTEN"), "listen host:port, http://host:port/path, or sse://host:port/path")
	serverPath := flag.String("rpc-server", firstEnvironment("DELTACHAT_RPC_SERVER", "DELTACHAT_RPC_SERVER_PATH"), "path to deltachat-rpc-server (default: find it on PATH)")
	accountsPath := firstEnvironment("DELTACHAT_ACCOUNTS_PATH", "DC_ACCOUNTS_PATH")
	if accountsPath == "" {
		accountsPath = defaultAccountsPath()
	}
	accountsPathFlag := flag.String("accounts-path", accountsPath, "Delta Chat account store")
	manageAccounts := flag.Bool("account-management", accountManagement, "expose account-management tools in addition to messaging tools")
	flag.Parse()

	startupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	client, err := deltachat.New(startupCtx, deltachat.Config{
		ServerPath:   *serverPath,
		AccountsPath: *accountsPathFlag,
	})
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("close Delta Chat RPC server: %v", err)
		}
	}()

	service := newDeltaChatService(client, *manageAccounts)
	tools := service.tools()
	definitions := make([]mcplib.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		definitions = append(definitions, tool.definition)
	}
	server := mcplib.NewMCPServer(definitions)
	for _, tool := range tools {
		server.RegisterToolWithContext(tool.definition.Name, tool.handler)
	}
	server.SetResponseMode(mcplib.ResponseModeBoth)
	if err := server.ListenAndServe(*listen, false); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func firstEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func environmentBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", name, err)
	}
	return parsed, nil
}

func defaultAccountsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "accounts"
	}
	return filepath.Join(configDir, "mai", "deltachat")
}
