// Package deltachat provides a dependency-free Go client for
// deltachat-rpc-server.
package deltachat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultServer = "deltachat-rpc-server"

// Config controls the deltachat-rpc-server child process.
type Config struct {
	ServerPath   string
	AccountsPath string
	Stderr       io.Writer
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// RPCError is an error returned by deltachat-rpc-server.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("deltachat rpc error %d: %s", e.Code, e.Message)
}

// Client owns one deltachat-rpc-server process and its account store.
// It is safe for concurrent use.
type Client struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	accountsPath string

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan rpcResponse
	closed  bool
	once    sync.Once
}

// New starts deltachat-rpc-server and verifies that it answers requests.
func New(ctx context.Context, cfg Config) (*Client, error) {
	serverPath, err := resolveServerPath(cfg.ServerPath)
	if err != nil {
		return nil, err
	}

	accountsPath, err := prepareAccountsPath(cfg.AccountsPath)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(serverPath)
	if accountsPath != "" {
		cmd.Env = setEnvironment(cmd.Environ(), "DC_ACCOUNTS_PATH", accountsPath)
	}
	if cfg.Stderr != nil {
		cmd.Stderr = cfg.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("deltachat rpc stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("deltachat rpc stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start %s: %w", serverPath, err)
	}

	c := &Client{
		cmd:          cmd,
		stdin:        stdin,
		stdout:       stdout,
		accountsPath: accountsPath,
		pending:      make(map[uint64]chan rpcResponse),
	}
	go c.readLoop()

	var systemInfo map[string]string
	if err := c.Call(ctx, "get_system_info", &systemInfo); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("start deltachat rpc: %w", err)
	}
	return c, nil
}

// AccountsPath returns the configured account store. An empty string means the
// server's default "accounts" directory under its working directory.
func (c *Client) AccountsPath() string {
	return c.accountsPath
}

// Call invokes a raw positional JSON-RPC method. A nil result discards the
// response body.
func (c *Client) Call(ctx context.Context, method string, result any, params ...any) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("deltachat rpc: connection closed")
	}
	c.nextID++
	id := c.nextID
	response := make(chan rpcResponse, 1)
	c.pending[id] = response
	c.mu.Unlock()

	data, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		c.clearPending(id)
		return fmt.Errorf("encode deltachat rpc %s: %w", method, err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	if c.closed {
		delete(c.pending, id)
		c.mu.Unlock()
		return errors.New("deltachat rpc: connection closed")
	}
	_, err = c.stdin.Write(data)
	c.mu.Unlock()
	if err != nil {
		c.clearPending(id)
		return fmt.Errorf("write deltachat rpc %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.clearPending(id)
		return ctx.Err()
	case resp := <-response:
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil || string(resp.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("decode deltachat rpc %s: %w", method, err)
		}
		return nil
	}
}

func (c *Client) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var resp rpcResponse
			if jsonErr := json.Unmarshal(line, &resp); jsonErr == nil && resp.ID != 0 {
				c.mu.Lock()
				waiter := c.pending[resp.ID]
				delete(c.pending, resp.ID)
				c.mu.Unlock()
				if waiter != nil {
					waiter <- resp
				}
			}
		}
		if err != nil {
			c.failAll(err)
			return
		}
	}
}

func (c *Client) clearPending(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, waiter := range c.pending {
		waiter <- rpcResponse{Error: &RPCError{Code: -1, Message: "connection closed: " + err.Error()}}
		delete(c.pending, id)
	}
}

// Close stops account I/O and terminates the child process.
func (c *Client) Close() error {
	var closeErr error
	c.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = c.Call(ctx, "stop_io_for_all_accounts", nil)
		cancel()

		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				closeErr = err
			}
			if err := c.cmd.Wait(); err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) && closeErr == nil {
					closeErr = err
				}
			}
		}
	})
	return closeErr
}

func resolveServerPath(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = defaultServer
	}
	path, err := exec.LookPath(expandHome(configured))
	if err != nil {
		if configured == defaultServer {
			return "", fmt.Errorf("%s not found on PATH", defaultServer)
		}
		return "", fmt.Errorf("deltachat rpc server %q not found", configured)
	}
	return path, nil
}

func prepareAccountsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	path = expandHome(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Delta Chat accounts path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("create Delta Chat accounts path %s: %w", abs, err)
	}
	return abs, nil
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func regularFilePath(path string) (string, error) {
	abs, err := filepath.Abs(expandHome(strings.TrimSpace(path)))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", abs, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", abs)
	}
	return abs, nil
}

func setEnvironment(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	found := false
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			if !found {
				result = append(result, key+"="+value)
				found = true
			}
			continue
		}
		result = append(result, entry)
	}
	if !found {
		result = append(result, key+"="+value)
	}
	return result
}
