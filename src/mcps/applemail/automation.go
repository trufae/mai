package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
)

func appleString(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", `\r`, "\n", `\n`, "\t", `\t`, "\u2028", `\n`, "\u2029", `\n`).Replace(value) + `"`
}

func mailboxRef(box mailbox) (string, error) {
	names := strings.Split(box.Name, "/")
	ref := ""
	for i := len(names) - 1; i >= 0; i-- {
		if names[i] == "" || names[i] == "." || names[i] == ".." {
			return "", fmt.Errorf("invalid mailbox path")
		}
		ref += "mailbox " + appleString(names[i]) + " of "
	}
	if box.Account == "" || strings.HasPrefix(box.URL, "local:") {
		return strings.TrimSuffix(ref, " of "), nil
	}
	return ref + "account id " + appleString(box.Account), nil
}

func messageRef(msg message, box mailbox) (string, error) {
	ref, err := mailboxRef(box)
	// Mail's multiword "message id" property shadows the message class when
	// followed by an ID selector. Its raw class code keeps a direct ID lookup.
	return fmt.Sprintf("«class mssg» id %d of (%s)", msg.ID, ref), err
}

func (s *service) appleScript(ctx context.Context, script string) (string, error) {
	select {
	case s.automation <- struct{}{}:
		defer func() { <-s.automation }()
	case <-ctx.Done():
		return "", fmt.Errorf("Mail automation is busy: %w", ctx.Err())
	}
	unlock, err := lockAutomation(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	seconds := int(math.Ceil(s.timeout.Seconds())) - 1
	if seconds < 1 {
		seconds = 1
	}
	script = wrapScript(script, seconds)
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("Mail automation stopped: %w", ctx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if strings.Contains(detail, "-1743") {
			detail += "; allow the MCP host to control Mail in System Settings > Privacy & Security > Automation"
		}
		return "", fmt.Errorf("Mail automation: %s", detail)
	}
	return strings.TrimSpace(string(output)), nil
}

func wrapScript(script string, seconds int) string {
	return fmt.Sprintf("tell application id \"com.apple.mail\"\nwith timeout of %d seconds\n%s\nend timeout\nend tell", seconds, script)
}
