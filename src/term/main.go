package main

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		panic("usage: mai-term <socket-path> [command]")
	}
	socketPath := os.Args[1]

	var command string
	if len(os.Args) == 3 {
		command = os.Args[2]
	} else {
		command = os.Getenv("SHELL")
		if command == "" {
			command = "/bin/bash"
		}
	}

	// Remove socket if exists
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	// Spawn command
	cmd := exec.Command("/bin/sh", "-c", command)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		panic(err)
	}

	// Set PTY size to match terminal
	if term.IsTerminal(int(os.Stdout.Fd())) {
		width, height, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil {
			pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
		}
	}

	// Handle window resize
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		for range ch {
			if term.IsTerminal(int(os.Stdout.Fd())) {
				width, height, err := term.GetSize(int(os.Stdout.Fd()))
				if err == nil {
					pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
				}
			}
		}
	}()

	var mu sync.Mutex
	connections := make([]net.Conn, 0)
	outputBuffer := make([]byte, 0)
	const maxBuffer = 10240

	// Goroutine to accept connections
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			// Send recent output buffer to new connection
			if len(outputBuffer) > 0 {
				sendFramed(conn, 1, outputBuffer)
			}
			mu.Unlock()

			// Handle input from this connection
			go func(c net.Conn) {
				io.Copy(ptmx, c)
				// When connection closes, remove it
				mu.Lock()
				for i, conn := range connections {
					if conn == c {
						connections = append(connections[:i], connections[i+1:]...)
						break
					}
				}
				mu.Unlock()
				c.Close()
			}(conn)
		}
	}()

	// Forward own stdin to command
	go io.Copy(ptmx, os.Stdin)

	// Forward output to own stdout and to connections
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				return
			}
			os.Stdout.Write(buf[:n])
			// Send to connections as stdout (since PTY merges stdout/stderr)
			mu.Lock()
			for _, conn := range connections {
				sendFramed(conn, 1, buf[:n])
			}
			// Append to buffer for new connections
			outputBuffer = append(outputBuffer, buf[:n]...)
			if len(outputBuffer) > maxBuffer {
				outputBuffer = outputBuffer[len(outputBuffer)-maxBuffer:]
			}
			mu.Unlock()
		}
	}()

	cmd.Wait()
}

func sendFramed(conn net.Conn, typ byte, data []byte) {
	// Type byte
	conn.Write([]byte{typ})
	// Length 4 bytes big endian
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	conn.Write(lenBuf)
	// Data
	conn.Write(data)
}
