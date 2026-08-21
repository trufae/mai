package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"deltachatmcp/lib"
	"mcplib"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mai-mcp-deltachat:", err)
		os.Exit(1)
	}
}

func run() error {
	accountManagement, err := environmentBool("DELTACHAT_ACCOUNT_MANAGEMENT", true)
	if err != nil {
		return err
	}
	commandTimeout, err := environmentDuration("DELTACHAT_COMMAND_TIMEOUT", 5*time.Minute)
	if err != nil {
		return err
	}

	listen := flag.String("l", os.Getenv("DELTACHAT_LISTEN"), "listen host:port, http://host:port/path, or sse://host:port/path")
	serverPath := flag.String("rpc-server", firstEnvironment("DELTACHAT_RPC_SERVER", "DELTACHAT_RPC_SERVER_PATH"), "path to deltachat-rpc-server (default: find it on PATH)")
	accountsPath := firstEnvironment("DELTACHAT_ACCOUNTS_PATH", "DC_ACCOUNTS_PATH")
	if accountsPath == "" {
		accountsPath = defaultAccountsPath()
	}
	accountsPathFlag := flag.String("accounts-path", accountsPath, "Delta Chat account store")
	manageAccounts := flag.Bool("account-management", accountManagement, "expose account-management tools in addition to messaging tools")
	command := ""
	flag.StringVar(&command, "T", "", "execute one or more DSL commands and exit")
	flag.StringVar(&command, "command", "", "execute one or more DSL commands and exit")
	listCommands := flag.Bool("t", false, "list direct CLI commands and exit")
	timeout := flag.Duration("timeout", commandTimeout, "direct CLI command timeout; 0 disables it")
	flag.Usage = printUsage
	flag.Parse()

	service := newDeltaChatService(nil, *manageAccounts)
	tools := service.tools()
	if *listCommands {
		printCLICommands(tools)
		return nil
	}
	if command == "" && flag.NArg() > 0 {
		command = dslFromArgs(flag.Args())
	}

	startupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	client, err := deltachat.New(startupCtx, deltachat.Config{
		ServerPath:   *serverPath,
		AccountsPath: *accountsPathFlag,
	})
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close Delta Chat RPC server:", err)
		}
	}()

	service = newDeltaChatService(client, *manageAccounts)
	tools = service.tools()
	if command != "" {
		ctx := context.Background()
		if *timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, *timeout)
			defer cancel()
		}
		return mcplib.RunDSL(ctx, cliTools(tools), command)
	}

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
		return fmt.Errorf("ListenAndServe: %w", err)
	}
	return nil
}

func cliTools(tools []mcpTool) []mcplib.ToolWithContext {
	result := make([]mcplib.ToolWithContext, 0, len(tools)*2)
	for _, tool := range tools {
		definition := tool.definition
		cliTool := mcplib.ToolWithContext{
			Name:        strings.TrimPrefix(definition.Name, "deltachat_"),
			Description: definition.Description,
			InputSchema: definition.InputSchema,
			Handler:     tool.handler,
		}
		result = append(result, cliTool)
		cliTool.Name = definition.Name
		result = append(result, cliTool)
	}
	return result
}

func printCLICommands(tools []mcpTool) {
	for _, tool := range tools {
		name := strings.TrimPrefix(tool.definition.Name, "deltachat_")
		properties, _ := tool.definition.InputSchema["properties"].(map[string]any)
		action, _ := properties["action"].(map[string]any)
		actions, _ := action["enum"].([]string)
		fmt.Printf("%s: %s\n", name, strings.Join(actions, ", "))
	}
}

func dslFromArgs(args []string) string {
	args = append([]string(nil), args...)
	if len(args) > 1 && !strings.Contains(args[1], "=") {
		args[1] = "action=" + args[1]
	}
	for i := 1; i < len(args); i++ {
		key, value, ok := strings.Cut(args[i], "=")
		if !ok || !strings.ContainsAny(value, " \t;") {
			continue
		}
		if (strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{")) && json.Valid([]byte(value)) {
			var parsed any
			if json.Unmarshal([]byte(value), &parsed) == nil {
				if compact, err := json.Marshal(parsed); err == nil {
					args[i] = key + "=" + string(compact)
					continue
				}
			}
		}
		args[i] = key + "=" + strconv.Quote(value)
	}
	return strings.Join(args, " ")
}

func printUsage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "Usage:\n  %s [server flags]\n  %s [flags] <scope> <action> [key=value ...]\n  %s [flags] -T '<scope> action=<action> ...'\n\n", os.Args[0], os.Args[0], os.Args[0])
	flag.PrintDefaults()
	fmt.Fprintln(out, "\nDirect CLI scopes: accounts, contacts, chats, messages. Use -t to list their actions.")
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

func environmentDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration", name)
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
