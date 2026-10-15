package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type updateResult struct {
	id     int64
	state  string
	detail string
}

func (s *service) update(ctx context.Context, args map[string]any) (any, error) {
	var a struct {
		ID        *int64  `json:"id"`
		IDs       []int64 `json:"ids"`
		Action    string  `json:"action"`
		MailboxID int64   `json:"mailbox_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if (a.Action == "move" && a.MailboxID <= 0) || (a.Action != "move" && a.MailboxID != 0) {
		return nil, fmt.Errorf("mailbox_id is required only for move")
	}
	commands := map[string]string{"read": "set read status of targetMessage to true", "unread": "set read status of targetMessage to false", "flag": "set flagged status of targetMessage to true", "unflag": "set flagged status of targetMessage to false", "trash": "delete targetMessage", "move": ""}
	command, ok := commands[a.Action]
	if !ok {
		return nil, fmt.Errorf("action must be read, unread, flag, unflag, move or trash")
	}
	if len(a.IDs) > 500 || (a.ID == nil && len(a.IDs) == 0) {
		return nil, fmt.Errorf("provide id or ids with 1..500 distinct message IDs")
	}
	if a.ID != nil {
		a.IDs = append([]int64{*a.ID}, a.IDs...)
	}
	ids := make([]int64, 0, len(a.IDs))
	seen := make(map[int64]bool)
	for _, id := range a.IDs {
		if id <= 0 || id > 1<<53-1 {
			return nil, fmt.Errorf("message IDs must be integers in 1..9007199254740991")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("at most 500 distinct message IDs are allowed")
	}
	rows, boxes, err := s.store.getMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	if a.Action == "move" {
		selected, err := selectMailboxes(boxes, strconv.FormatInt(a.MailboxID, 10), "")
		if err != nil {
			return nil, err
		}
		destination, err := mailboxRef(selected[0])
		if err != nil {
			return nil, err
		}
		command = "move targetMessage to " + destination
	}
	mailboxes := make(map[int64]mailbox, len(boxes))
	for _, box := range boxes {
		mailboxes[box.ID] = box
	}
	messages := make(map[int64]message, len(rows))
	for _, row := range rows {
		messages[row.ID] = row
	}
	results := make([]updateResult, len(ids))
	var targets []string
	for i, id := range ids {
		results[i] = updateResult{id: id, state: "failed", detail: "message no longer exists; search again"}
		msg, ok := messages[id]
		if !ok {
			continue
		}
		box, ok := mailboxes[msg.Mailbox]
		if !ok {
			results[i].detail = "mailbox no longer exists; search again"
			continue
		}
		ref, err := messageRef(msg, box)
		if err != nil {
			results[i].detail = err.Error()
			continue
		}
		results[i].state, results[i].detail = "not attempted", ""
		targets = append(targets, fmt.Sprintf("{%d, (a reference to %s)}", id, ref))
	}
	if len(targets) > 0 {
		output, runErr := s.script(ctx, updateScript(targets, command))
		applyUpdateResults(results, output, runErr)
		if runErr != nil {
			report, reportErr := formatUpdateResults(a.Action, results)
			if reportErr != nil {
				report = reportErr.Error()
			}
			return nil, fmt.Errorf("%s\n\nMail automation stopped: %w", report, runErr)
		}
	}
	return formatUpdateResults(a.Action, results)
}

func updateScript(targets []string, command string) string {
	// References are resolved inside the try block so one stale ID cannot abort
	// the batch. Only numeric outcomes cross the AppleScript boundary.
	return "set targets to {" + strings.Join(targets, ", ") + `}
set resultLines to {}
repeat with targetInfo in targets
    set targetID to item 1 of targetInfo
    try
        set targetMessage to contents of item 2 of targetInfo
        ` + command + `
        set end of resultLines to (targetID as text) & ":0"
    on error errorText number errorNumber
        set end of resultLines to (targetID as text) & ":" & (errorNumber as text)
        if errorNumber is -1712 then exit repeat
    end try
end repeat
set AppleScript's text item delimiters to linefeed
return resultLines as text`
}

func applyUpdateResults(results []updateResult, output string, runErr error) {
	codes := make(map[int64]int64)
	valid := true
	for _, line := range strings.Fields(output) {
		idText, codeText, ok := strings.Cut(line, ":")
		id, idErr := strconv.ParseInt(idText, 10, 64)
		code, codeErr := strconv.ParseInt(codeText, 10, 64)
		if _, duplicate := codes[id]; !ok || idErr != nil || codeErr != nil || duplicate {
			valid = false
			break
		}
		codes[id] = code
	}
	for i := range results {
		result := &results[i]
		if result.state == "failed" {
			continue // Index preflight failures were never sent to Mail.
		}
		code, reported := codes[result.id]
		switch {
		case !valid || (!reported && runErr != nil):
			result.state, result.detail = "unknown", "check Mail before retrying"
		case !reported:
			result.state, result.detail = "not attempted", "batch stopped before this message"
		case code == 0:
			result.state = "ok"
		case code == -1712:
			result.state, result.detail = "unknown", "Mail timed out; check Mail before retrying"
		default:
			result.state, result.detail = "failed", fmt.Sprintf("Mail error %d", code)
		}
	}
}

func formatUpdateResults(action string, results []updateResult) (any, error) {
	counts := make(map[string]int)
	var succeeded []string
	for _, result := range results {
		counts[result.state]++
		if result.state == "ok" {
			succeeded = append(succeeded, strconv.FormatInt(result.id, 10))
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s: %d succeeded, %d failed, %d unknown, %d not attempted.\n", action, counts["ok"], counts["failed"], counts["unknown"], counts["not attempted"])
	if len(succeeded) > 0 {
		fmt.Fprintf(&out, "\nSucceeded IDs: %s\n", strings.Join(succeeded, ", "))
	}
	if counts["ok"] != len(results) {
		out.WriteString("\n| ID | Result | Detail |\n|---:|---|---|\n")
		for _, result := range results {
			if result.state != "ok" {
				fmt.Fprintf(&out, "| %d | %s | %s |\n", result.id, result.state, inline(result.detail))
			}
		}
		return nil, fmt.Errorf("%s", out.String())
	}
	out.WriteString("\nMail's index may take a moment to reflect the change.")
	return out.String(), nil
}
