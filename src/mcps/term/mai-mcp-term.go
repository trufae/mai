package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	mcplib "mcplib"
)

const version = "0.1.0"

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[mGJK]`)

func filterOutput(data string) (stdout string, stderr string) {
	// Strip ANSI escape codes
	data = ansiRegex.ReplaceAllString(data, "")

	// Handle \r (carriage return) - take the last part after \r on each line
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\r") {
			parts := strings.Split(line, "\r")
			lines[i] = parts[len(parts)-1]
		}
	}
	data = strings.Join(lines, "\n")

	// Handle clearscreen (\x1b[2J) - if found, clear the buffer
	if strings.Contains(data, "\x1b[2J") {
		return "", ""
	}

	// For clearline (\x1b[2K), remove lines containing it, but since it's accumulated, approximate by removing the line
	lines = strings.Split(data, "\n")
	filteredLines := make([]string, 0)
	for _, line := range lines {
		if !strings.Contains(line, "\x1b[2K") {
			filteredLines = append(filteredLines, line)
		}
	}
	data = strings.Join(filteredLines, "\n")

	// Remove other noise - perhaps control characters except \n \t
	data = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`).ReplaceAllString(data, "")

	return data, ""
}

func readByte(r io.Reader) (byte, error) {
	buf := make([]byte, 1)
	_, err := r.Read(buf)
	return buf[0], err
}

func doRead(socketPath string, raw bool) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Println("Error connecting to socket:", err)
		return
	}
	defer conn.Close()

	buffer := make([]byte, 0)
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)) // Read with timeout

	for {
		typ, err := readByte(conn)
		if err != nil {
			break
		}
		lenBuf := make([]byte, 4)
		_, err = io.ReadFull(conn, lenBuf)
		if err != nil {
			break
		}
		length := binary.BigEndian.Uint32(lenBuf)
		data := make([]byte, length)
		_, err = io.ReadFull(conn, data)
		if err != nil {
			break
		}
		if typ == 1 {
			buffer = append(buffer, data...)
		}
	}

	data := string(buffer)
	if !raw {
		stdout, _ := filterOutput(data)
		fmt.Print(stdout)
	} else {
		fmt.Print(data)
	}
}

func doWrite(socketPath, text string) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Println("Error connecting to socket:", err)
		return
	}
	defer conn.Close()

	_, err = conn.Write([]byte(text))
	if err != nil {
		fmt.Println("Error writing to socket:", err)
	}
}

func main() {
	socketPath := flag.String("socket", "", "Unix socket path to connect to the terminal multiplexer")
	raw := flag.Bool("r", false, "Raw mode - no filtering of output")
	read := flag.Bool("read", false, "Read terminal output and print to stdout")
	write := flag.String("write", "", "Write the given text to the terminal")
	versionFlag := flag.Bool("v", false, "Show version")

	flag.Usage = func() {
		fmt.Println("mai-mcp-term - Terminal MCP server and CLI tool")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  As MCP server:")
		fmt.Println("    mai-mcp-term -socket <path> [-r]")
		fmt.Println()
		fmt.Println("  As CLI tool:")
		fmt.Println("    mai-mcp-term -socket <path> -read [-r]")
		fmt.Println("    mai-mcp-term -socket <path> -write <text>")
		fmt.Println("    mai-mcp-term -v")
		fmt.Println("    mai-mcp-term -h")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *versionFlag {
		fmt.Println("mai-mcp-term version", version)
		return
	}

	if *socketPath == "" {
		flag.Usage()
		return
	}

	if *read {
		doRead(*socketPath, *raw)
		return
	}

	if *write != "" {
		doWrite(*socketPath, *write)
		return
	}

	// Start MCP server
	conn, err := net.Dial("unix", *socketPath)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	var mu sync.Mutex
	buffer := make([]byte, 0)

	// Goroutine to read from socket and accumulate output
	go func() {
		for {
			typ, err := readByte(conn)
			if err != nil {
				return
			}
			lenBuf := make([]byte, 4)
			_, err = io.ReadFull(conn, lenBuf)
			if err != nil {
				return
			}
			length := binary.BigEndian.Uint32(lenBuf)
			data := make([]byte, length)
			_, err = io.ReadFull(conn, data)
			if err != nil {
				return
			}
			if typ == 1 { // stdout (merged output)
				mu.Lock()
				buffer = append(buffer, data...)
				mu.Unlock()
			}
			// Type 2 (stderr) is ignored since output is merged
		}
	}()

	tools := []mcplib.ToolDefinition{
		{
			Name:        "read_terminal",
			Description: "Read the terminal output since the last read call",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "write_terminal",
			Description: "Write input string to the terminal",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input": map[string]interface{}{
						"type":        "string",
						"description": "The input string to send to the terminal",
					},
				},
				"required": []string{"input"},
			},
		},
	}

	server := mcplib.NewMCPServer(tools)

	server.RegisterTool("read_terminal", func(args map[string]interface{}) (interface{}, error) {
		mu.Lock()
		data := string(buffer)
		buffer = buffer[:0]
		mu.Unlock()

		if *raw {
			return map[string]interface{}{
				"stdout": data,
				"stderr": "",
			}, nil
		} else {
			stdout, stderr := filterOutput(data)
			return map[string]interface{}{
				"stdout": stdout,
				"stderr": stderr,
			}, nil
		}
	})

	server.RegisterTool("write_terminal", func(args map[string]interface{}) (interface{}, error) {
		input, ok := args["input"].(string)
		if !ok {
			return nil, fmt.Errorf("input must be a string")
		}
		_, err := conn.Write([]byte(input))
		if err != nil {
			return nil, err
		}
		return "Input written to terminal", nil
	})

	server.Start()
}
