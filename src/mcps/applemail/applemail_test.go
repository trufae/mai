package main

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) *service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "V10", "MailData", "Envelope Index")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	sql := `
CREATE TABLE messages (ROWID INTEGER PRIMARY KEY, subject_prefix TEXT, subject INTEGER, sender INTEGER, summary INTEGER, date_received INTEGER, mailbox INTEGER, read INTEGER DEFAULT 0, flagged INTEGER DEFAULT 0, deleted INTEGER DEFAULT 0);
CREATE INDEX by_date ON messages(date_received);
CREATE INDEX by_mailbox ON messages(mailbox,date_received);
CREATE TABLE subjects (ROWID INTEGER PRIMARY KEY, subject TEXT);
CREATE TABLE addresses (ROWID INTEGER PRIMARY KEY, address TEXT, comment TEXT);
CREATE TABLE summaries (ROWID INTEGER PRIMARY KEY, summary TEXT);
CREATE TABLE recipients (message INTEGER, address INTEGER);
CREATE TABLE mailboxes (ROWID INTEGER PRIMARY KEY, url TEXT, total_count INTEGER, unread_count INTEGER);
CREATE TABLE labels (message_id INTEGER, mailbox_id INTEGER, PRIMARY KEY(message_id,mailbox_id));
CREATE INDEX by_label ON labels(mailbox_id);
INSERT INTO mailboxes VALUES (1,'imap://test-account/INBOX',7,7),(2,'imap://test-account/%5BGmail%5D/All%20Mail',100007,7),(3,'imap://test-account/Archive',1,1);
INSERT INTO subjects VALUES (1,'Needle | Ω'),(2,'Unrelated'),(3,'budget_100%'),(4,'date test');
INSERT INTO addresses VALUES (1,'alice@example.org','Alice'),(2,'bob@example.org','Bob');
INSERT INTO summaries VALUES (1,'Cached preview only'),(2,'Other snippet');
WITH RECURSIVE n(x) AS (VALUES(1001) UNION ALL SELECT x+1 FROM n WHERE x<1006)
INSERT INTO messages(ROWID,subject_prefix,subject,sender,summary,date_received,mailbox,read,flagged) SELECT x,'Re: ',1,1,1,1700000000,2,x%2,x%2 FROM n;
INSERT INTO labels SELECT ROWID,1 FROM messages;
INSERT INTO labels VALUES(1001,3);
INSERT INTO recipients VALUES(1001,2);
INSERT INTO messages(ROWID,subject, sender,summary,date_received,mailbox) VALUES(1007,3,2,2,1600000000,3);
WITH RECURSIVE n(x) AS (VALUES(2000) UNION ALL SELECT x+1 FROM n WHERE x<101999)
INSERT INTO messages(ROWID,subject,sender,summary,date_received,mailbox) SELECT x,2,2,2,1500000000+x,2 FROM n;
`
	cmd := exec.Command("/usr/bin/sqlite3", "-init", "/dev/null", path)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	return newService(&store{path: path}, time.Second)
}

func TestSearchLabelsPaginationAndFilters(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := searchArgs{Mailbox: "INBOX", Query: "Needle", Limit: 2}
	var ids []int64
	for {
		rows, cursor, _, err := s.store.search(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if cursor == "" {
			break
		}
		a.Cursor = cursor
	}
	if fmt.Sprint(ids) != "[1006 1005 1004 1003 1002 1001]" {
		t.Fatalf("missing, repeated or misordered label results: %v", ids)
	}
	a = searchArgs{Mailbox: "All", Query: "Needle", Limit: 10}
	rows, _, _, err := s.store.search(ctx, a)
	if err != nil || len(rows) != 6 {
		t.Fatalf("duplicate labels: rows=%d err=%v", len(rows), err)
	}
	for _, test := range []struct {
		field string
		value any
		count string
	}{
		{"sender", "Alice", "6 messages"},
		{"recipient", "bob@", "1 messages"},
		{"query", "preview only", "6 messages"},
		{"subject", "Re: Needle", "6 messages"},
		{"unread", true, "3 messages"},
		{"unread", false, "3 messages"},
		{"flagged", true, "3 messages"},
		{"mailbox", "Archive", "1 messages"},
		{"mailbox", "missing", "error"},
		{"cursor", "invalid", "error"},
		{"limit", 1.5, "error"},
		{"limit", 51, "error"},
		{"date_from", "2023-02-30", "error"},
		{"unknown", true, "error"},
		{"query", "nul\x00", "error"},
	} {
		t.Run(fmt.Sprint(test.field, test.value), func(t *testing.T) {
			args := map[string]any{"query": "Needle", test.field: test.value}
			result, err := s.search(ctx, args)
			if test.count == "error" {
				if err == nil {
					t.Fatalf("expected validation error, got %v", result)
				}
			} else if err != nil || !strings.HasPrefix(fmt.Sprint(result), test.count) {
				t.Fatalf("result=%v err=%v", result, err)
			}
		})
	}
	for _, query := range []string{"' OR 1=1 --", "%", "_", "\n.shell touch /tmp/mai-mail-injection"} {
		rows, _, _, err := s.store.search(ctx, searchArgs{Mailbox: "All", Query: query, Limit: 10})
		want := 0
		if query == "%" || query == "_" {
			want = 1
		}
		if err != nil || len(rows) != want {
			t.Errorf("literal query %q: rows=%d err=%v", query, len(rows), err)
		}
	}
	date := time.Unix(1700000000, 0).Local().Format("2006-01-02")
	result, err := s.search(ctx, map[string]any{"date_from": date, "date_to": date})
	if err != nil || !strings.HasPrefix(fmt.Sprint(result), "6 messages") {
		t.Fatalf("inclusive dates: %v %v", result, err)
	}
	_, err = s.search(ctx, map[string]any{"date_from": "2024-01-02", "date_to": "2024-01-01"})
	if err == nil {
		t.Fatal("accepted reversed date range")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := s.store.search(ctx, a); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestCachedReadMIMEAndFallback(t *testing.T) {
	s := fixture(t)
	raw := "From: alice@example.org\r\nTo: =?UTF-8?B?QsO2Yg==?= <bob@example.org>\r\nMessage-ID: <test@example.org>\r\nContent-Type: multipart/mixed; boundary=mix\r\n\r\n" +
		"--mix\r\nContent-Type: multipart/alternative; boundary=alt\r\n\r\n" +
		"--alt\r\nContent-Type: text/html\r\n\r\n<p>HTML fallback</p>\r\n" +
		"--alt\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\naMOpbGxvIPCfmIA=\r\n--alt--\r\n" +
		"--mix\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"report.txt\"\r\n\r\nsecret binary data\r\n--mix--\r\n"
	dir := filepath.Join(filepath.Dir(filepath.Dir(s.store.path)), "test-account", "[Gmail].mbox", "All Mail.mbox", "cache-uuid", "Data", "1", "Messages")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "1002.emlx")
	emlx := fmt.Sprintf("%d\n%s<trailing-plist>must not appear</trailing-plist>", len(raw), raw)
	if err := os.WriteFile(path, []byte(emlx), 0600); err != nil {
		t.Fatal(err)
	}
	s.script = func(context.Context, string) (string, error) {
		t.Fatal("cached read invoked Mail automation")
		return "", nil
	}
	result, err := s.read(context.Background(), map[string]any{"id": 1002, "max_chars": 6})
	text := fmt.Sprint(result)
	if err != nil || !strings.Contains(text, "héllo ") || !strings.Contains(text, "report.txt") || !strings.Contains(text, "Böb") || !strings.Contains(text, "[Body truncated]") || strings.Contains(text, "secret binary") || strings.Contains(text, "trailing-plist") || strings.Contains(text, "HTML fallback") {
		t.Fatalf("bad MIME read: %v %v", result, err)
	}
	msg, _, err := s.store.get(context.Background(), 1002)
	if err != nil || msg.Read != 0 {
		t.Fatalf("read changed flags: %+v %v", msg, err)
	}
	s.script = func(_ context.Context, script string) (string, error) {
		if !strings.Contains(script, "«class mssg» id 1001") || strings.Contains(script, "every message") {
			t.Fatalf("untargeted fallback: %s", script)
		}
		return "uncached body", nil
	}
	result, err = s.read(context.Background(), map[string]any{"id": 1001})
	if err != nil || !strings.Contains(fmt.Sprint(result), "uncached body") {
		t.Fatalf("fallback: %v %v", result, err)
	}
	if _, err := readEMLX(strings.NewReader("999999999999\nx")); err == nil {
		t.Fatal("accepted excessive MIME length")
	}
	if _, err := readEMLX(strings.NewReader("12\nshort")); err == nil {
		t.Fatal("accepted truncated emlx")
	}
	if _, err := readEMLX(strings.NewReader(strings.Repeat("9", 10000))); err == nil {
		t.Fatal("accepted an unbounded emlx length header")
	}
	if text := htmlText("<style>hidden</style><script>bad()</script><p>Hello &amp; hi<br>world</p>"); text != "Hello & hi\nworld" {
		t.Fatal(text)
	}
	parsed, _ := mail.ReadMessage(strings.NewReader("Content-Type: text/plain; charset=windows-1252\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=E9 =80"))
	content, err := decodeMIME(textproto.MIMEHeader(parsed.Header), parsed.Body, 0)
	if err != nil || content.Text != "café €" {
		t.Fatalf("charset: %+v %v", content, err)
	}
}

func TestMutationsAndAppleScriptSyntax(t *testing.T) {
	s := fixture(t)
	var scripts []string
	s.script = func(_ context.Context, script string) (string, error) {
		scripts = append(scripts, script)
		if strings.HasPrefix(script, "set targets to ") {
			return "1001:0", nil
		}
		return "OK", nil
	}
	for _, action := range []string{"read", "unread", "flag", "unflag", "trash", "move"} {
		args := map[string]any{"id": 1001, "action": action}
		if action == "move" {
			args["mailbox_id"] = 3
		}
		if _, err := s.update(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range []map[string]any{
		{"to": "Bob <bob@example.org>, other@example.org", "subject": "A \"quote\" \\ Ω", "body": "Hello\nline two\tend"},
		{"to": "bob@example.org", "cc": "cc@example.org", "bcc": "bcc@example.org", "subject": "Test", "body": "Hello", "sender": "alice@example.org", "send": true},
		{"reply_id": 1001, "body": "Reply", "reply_all": true},
	} {
		if _, err := s.compose(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	before := len(scripts)
	for _, args := range []map[string]any{
		{"to": "invalid", "subject": "Test", "body": "Hi"},
		{"to": "bob@example.org", "subject": "Test\nBcc: victim", "body": "Hi"},
		{"reply_id": 1001, "to": "bob@example.org", "body": "Hi"},
		{"to": "bob@example.org", "subject": "Test", "body": ""},
	} {
		if _, err := s.compose(context.Background(), args); err == nil {
			t.Fatalf("accepted invalid compose arguments: %v", args)
		}
	}
	if len(scripts) != before {
		t.Fatal("invalid compose triggered automation")
	}
	if len(s.tools(false)) != 5 || len(s.tools(true)) != 3 {
		t.Fatal("unexpected tool count")
	}
	compileScripts(t, scripts)
}

func compileScripts(t *testing.T, scripts []string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return
	}
	for i, script := range scripts {
		// Compile only: never execute drafts, sends, or message updates.
		cmd := exec.Command("/usr/bin/osacompile", "-o", filepath.Join(t.TempDir(), "check.scpt"))
		cmd.Stdin = strings.NewReader(wrapScript(script, 5))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("script %d did not compile: %v: %s\n%s", i, err, out, script)
		}
	}
}

func TestReadOnlySQLiteAndDeadline(t *testing.T) {
	s := fixture(t)
	var result []map[string]any
	if err := s.store.query(context.Background(), "DELETE FROM messages", &result); err == nil {
		t.Fatal("index opened writable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := s.store.query(ctx, "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n", &result)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("query did not obey deadline: %v", err)
	}
	if _, err := os.Stat(s.store.path + "-journal"); !os.IsNotExist(err) {
		t.Fatalf("created a rollback journal: %v", err)
	}
	// Output escaping must keep a single Markdown table row and valid UTF-8.
	if got := inline("a|b\n*c*"); got != `a\|b \*c\*` {
		t.Fatal(got)
	}
	if !bytes.Equal([]byte(clip("😀ab", 1)), []byte("😀\n\n[Body truncated]")) {
		t.Fatal("character limit split Unicode")
	}
}
