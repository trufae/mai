package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"mcplib"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mai-mcp-applemail:", err)
		os.Exit(1)
	}
}

func run() error {
	db := flag.String("db", "", "Mail Envelope Index path (default: newest ~/Library/Mail/V*/MailData/Envelope Index)")
	listen := flag.String("l", "", "optional MCP TCP, HTTP or SSE listen address")
	timeout := flag.Duration("timeout", 10*time.Second, "maximum duration per tool call")
	readOnly := flag.Bool("read-only", false, "expose only mailboxes, search_emails and read_email")
	command := flag.String("T", "", "execute tool commands and exit, e.g. 'search_emails limit=6'")
	flag.Parse()
	if *timeout <= 0 || *timeout > time.Minute {
		return fmt.Errorf("timeout must be greater than zero and at most 1m")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s := newService(&store{path: *db}, *timeout)
	tools := s.tools(*readOnly)
	var active sync.WaitGroup
	var mu sync.Mutex
	closing := false
	defer func() {
		mu.Lock()
		closing = true
		mu.Unlock()
		cancel()
		active.Wait()
	}()
	for i := range tools {
		handler := tools[i].Handler
		tools[i].Handler = func(callCtx context.Context, args map[string]any) (any, error) {
			mu.Lock()
			if closing {
				mu.Unlock()
				return nil, context.Canceled
			}
			active.Add(1)
			mu.Unlock()
			defer active.Done()
			callCtx, stop := context.WithTimeout(callCtx, s.timeout)
			defer stop()
			go func() {
				select {
				case <-ctx.Done():
					stop()
				case <-callCtx.Done():
				}
			}()
			return handler(callCtx, args)
		}
	}
	if *command != "" {
		return mcplib.RunDSL(ctx, tools, *command)
	}
	definitions := make([]mcplib.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		definitions = append(definitions, mcplib.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	server := mcplib.NewMCPServer(definitions)
	for _, tool := range tools {
		handler := tool.Handler
		server.RegisterToolWithContext(tool.Name, func(callCtx context.Context, args map[string]any) (any, error) {
			result, err := handler(callCtx, args)
			if err != nil {
				return mcplib.ToolCallResult{IsError: true, Content: []any{map[string]any{"type": "text", "text": err.Error()}}}, nil
			}
			return result, nil
		})
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe(*listen, false) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return nil
	}
}
