package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
)

type ircClient struct {
	config   Config
	conn     net.Conn
	reader   *bufio.Reader
	writer   *bufio.Writer
	nick     string
	user     string
	realname string
	channel  string
}

func runIRC(config Config, wg *sync.WaitGroup) {
	defer wg.Done()

	client, err := newIRCClient(config)
	if err != nil {
		log.Printf("IRC connect error: %v", err)
		return
	}
	defer client.close()

	if err := client.register(); err != nil {
		log.Printf("IRC register error: %v", err)
		return
	}

	client.loop()
}

func newIRCClient(config Config) (*ircClient, error) {
	server := config.IrcServer
	if server == "" {
		return nil, fmt.Errorf("missing IRC server")
	}

	port := config.IrcPort
	if port == 0 {
		port = 6667
	}

	addr := fmt.Sprintf("%s:%d", server, port)

	var (
		conn net.Conn
		err  error
	)

	if config.IrcTLS {
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: server})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return nil, err
	}

	nick := config.IrcNick
	if nick == "" {
		nick = "mai-bot"
	}

	user := config.IrcUser
	if user == "" {
		user = nick
	}

	realname := config.IrcRealname
	if realname == "" {
		realname = "mai"
	}

	client := &ircClient{
		config:   config,
		conn:     conn,
		reader:   bufio.NewReader(conn),
		writer:   bufio.NewWriter(conn),
		nick:     nick,
		user:     user,
		realname: realname,
		channel:  config.IrcChannel,
	}

	return client, nil
}

func (c *ircClient) register() error {
	if c.config.IrcPassword != "" {
		if err := c.write(fmt.Sprintf("PASS %s", c.config.IrcPassword)); err != nil {
			return err
		}
	}

	if err := c.write(fmt.Sprintf("NICK %s", c.nick)); err != nil {
		return err
	}

	if err := c.write(fmt.Sprintf("USER %s 0 * :%s", c.user, c.realname)); err != nil {
		return err
	}

	if c.channel != "" {
		if err := c.write(fmt.Sprintf("JOIN %s", c.channel)); err != nil {
			return err
		}
	}

	return nil
}

func (c *ircClient) loop() {
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			log.Printf("IRC read error: %v", err)
			return
		}

		line = strings.TrimRight(line, "\r\n")

		switch {
		case strings.HasPrefix(line, "PING "):
			payload := strings.TrimPrefix(line, "PING ")
			_ = c.write("PONG " + payload)
			continue
		case strings.Contains(line, " 001 ") && c.channel != "":
			_ = c.write(fmt.Sprintf("JOIN %s", c.channel))
		}

		if !strings.Contains(line, " PRIVMSG ") {
			continue
		}

		c.handlePrivMsg(line)
	}
}

func (c *ircClient) handlePrivMsg(line string) {
	sender := extractIRCSender(line)
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return
	}

	target := parts[2]
	message := strings.TrimPrefix(parts[3], ":")

	command := c.extractCommand(target, message)
	if command == "" {
		return
	}

	response, exitCode := executeCommand(c.config, command)

	logInteraction(c.config, logEntry{
		Username:   sender,
		Query:      command,
		Response:   response,
		ReturnCode: exitCode,
	})

	replyTarget := target
	if strings.EqualFold(target, c.nick) {
		replyTarget = sender
	}

	c.sendMessage(replyTarget, response)
}

func (c *ircClient) extractCommand(target, message string) string {
	if strings.EqualFold(target, c.nick) {
		return message
	}

	prefixes := []string{c.nick + ":", c.nick + ",", c.nick + " "}
	for _, prefix := range prefixes {
		if strings.HasPrefix(message, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(message, prefix))
		}
	}
	return ""
}

func (c *ircClient) sendMessage(target, text string) {
	maxLen := c.config.IrcMaxLength
	if maxLen <= 0 {
		maxLen = 400
	}

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		runes := []rune(line)
		for len(runes) > 0 {
			length := maxLen
			if len(runes) < length {
				length = len(runes)
			}
			chunk := string(runes[:length])
			runes = runes[length:]
			_ = c.write(fmt.Sprintf("PRIVMSG %s :%s", target, chunk))
		}
	}
}

func (c *ircClient) write(line string) error {
	if _, err := c.writer.WriteString(line + "\r\n"); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *ircClient) close() {
	_ = c.conn.Close()
}

func extractIRCSender(line string) string {
	if !strings.HasPrefix(line, ":") {
		return ""
	}

	end := strings.Index(line, " ")
	if end <= 1 {
		return ""
	}

	prefix := line[1:end]
	if sep := strings.Index(prefix, "!"); sep > 0 {
		return prefix[:sep]
	}

	return prefix
}
