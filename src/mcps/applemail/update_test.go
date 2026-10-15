package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBatchUpdate(t *testing.T) {
	s := fixture(t)
	var scripts []string
	idsInScript := regexp.MustCompile(`\{([0-9]+), \(a reference to`)
	s.script = func(_ context.Context, script string) (string, error) {
		scripts = append(scripts, script)
		var lines []string
		for _, match := range idsInScript.FindAllStringSubmatch(script, -1) {
			lines = append(lines, match[1]+":0")
		}
		return strings.Join(lines, "\n"), nil
	}
	ctx := context.Background()
	_, err := s.update(ctx, map[string]any{"action": "trash", "id": 1001, "ids": []int64{1002, 1001, 1007, 9999999}})
	if err == nil || !strings.Contains(err.Error(), "3 succeeded, 1 failed") || !strings.Contains(err.Error(), "9999999 | failed") {
		t.Fatalf("missing ID outcome not reported: %v", err)
	}
	if len(scripts) != 1 || strings.Count(scripts[0], "«class mssg» id 1001 ") != 1 || !strings.Contains(scripts[0], `mailbox "Archive"`) || !strings.Contains(scripts[0], "delete targetMessage") || strings.Contains(scripts[0], "id 9999999 ") {
		t.Fatalf("expected one cross-mailbox, deduplicated script: %v", scripts)
	}
	_, err = s.update(ctx, map[string]any{"action": "move", "ids": []int64{1001, 1007}, "mailbox_id": 1})
	if err != nil || len(scripts) != 2 || !strings.Contains(scripts[1], `move targetMessage to mailbox "INBOX"`) {
		t.Fatalf("batch move: %v %v", scripts, err)
	}
	// Validate the batch limit on a real SQLite lookup, without executing Mail.
	ids := make([]int64, 500)
	for i := range ids {
		ids[i] = int64(2000 + i)
	}
	result, err := s.update(ctx, map[string]any{"action": "trash", "ids": ids})
	if err != nil || !strings.HasPrefix(fmt.Sprint(result), "trash: 500 succeeded") || len(scripts) != 3 || len(idsInScript.FindAllString(scripts[2], -1)) != 500 {
		t.Fatalf("500-message batch: %v %v", result, err)
	}
	before := len(scripts)
	for _, args := range []map[string]any{
		{"action": "trash"},
		{"action": "trash", "ids": []int64{}},
		{"action": "trash", "id": 0},
		{"action": "trash", "ids": []int64{1001, -1}},
		{"action": "trash", "ids": []float64{1001, 1.5}},
		{"action": "trash", "ids": "1001,1002"},
		{"action": "trash", "id": int64(1 << 53)},
		{"action": "trash", "ids": append([]int64{1001}, ids...)},
		{"action": "trash", "ids": ids, "id": 1001},
		{"action": "trash", "ids": ids, "mailbox_id": 1},
		{"action": "move", "ids": ids},
		{"action": "move", "ids": ids, "mailbox_id": 9999999},
		{"action": "invalid", "ids": ids},
		{"action": "trash", "ids": []int64{9999999}},
	} {
		if _, err := s.update(ctx, args); err == nil {
			t.Errorf("accepted invalid/unresolvable batch: %v", args)
		}
	}
	if len(scripts) != before {
		t.Fatal("invalid batch invoked Mail")
	}
	s.script = func(context.Context, string) (string, error) {
		return "1001:0", errors.New("simulated deadline")
	}
	_, err = s.update(ctx, map[string]any{"action": "trash", "ids": []int64{1001, 1002}})
	if err == nil || !strings.Contains(err.Error(), "1 succeeded, 0 failed, 1 unknown") || !strings.Contains(err.Error(), "simulated deadline") {
		t.Fatalf("lost partial outcomes/diagnostic: %v", err)
	}
	s.script = func(context.Context, string) (string, error) {
		return "1001:0\n1002:0", errors.New("simulated failure after completion")
	}
	_, err = s.update(ctx, map[string]any{"action": "trash", "ids": []int64{1001, 1002}})
	if err == nil || !strings.Contains(err.Error(), "2 succeeded, 0 failed, 0 unknown") || !strings.Contains(err.Error(), "simulated failure after completion") {
		t.Fatalf("lost confirmed outcomes after completion: %v", err)
	}
	compileScripts(t, scripts)
}

func TestBatchUpdateOutcomes(t *testing.T) {
	for _, test := range []struct {
		name, output, states string
		err                  error
	}{
		{"success", "1:0\n2:0\n3:0", "ok,ok,ok,failed", nil},
		{"stale message", "1:0\n2:-1728\n3:0", "ok,failed,ok,failed", nil},
		{"Apple event timeout", "1:0\n2:-1712", "ok,unknown,not attempted,failed", nil},
		{"process deadline", "1:0", "ok,unknown,unknown,failed", context.DeadlineExceeded},
		{"empty response", "", "not attempted,not attempted,not attempted,failed", nil},
		{"malformed response", "1:0\nbad", "unknown,unknown,unknown,failed", nil},
		{"duplicate response", "1:0\n1:-1728", "unknown,unknown,unknown,failed", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			results := []updateResult{{id: 1, state: "not attempted"}, {id: 2, state: "not attempted"}, {id: 3, state: "not attempted"}, {id: 4, state: "failed", detail: "missing"}}
			applyUpdateResults(results, test.output, test.err)
			var states []string
			for _, result := range results {
				states = append(states, result.state)
			}
			if strings.Join(states, ",") != test.states {
				t.Fatalf("outcomes: %+v", results)
			}
		})
	}
}

func TestBatchScriptExecution(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("AppleScript requires macOS")
	}
	// Exercise the actual loop with local list references, including a stale
	// reference in the middle. No Mail objects or mutation commands are used.
	targets := []string{
		"{1, (a reference to item 1 of fakeMessages)}",
		"{2, (a reference to item 99 of fakeMessages)}",
		"{3, (a reference to item 2 of fakeMessages)}",
	}
	script := "set fakeMessages to {\"first\", \"last\"}\n" + updateScript(targets, "set observedText to targetMessage as text")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-")
	cmd.Stdin = strings.NewReader(wrapScript(script, 3))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Join(strings.Fields(string(out)), ",") != "1:0,2:-1728,3:0" {
		t.Fatalf("batch loop outcomes: %s (%v)", out, err)
	}
}
