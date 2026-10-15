package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Mail owns this database. Always use normal read-only SQLite locking, including
// its WAL; immutable/nolock and copying a live index can return stale results.
type store struct {
	path string
	mu   sync.Mutex
}

type mailbox struct {
	ID      int64  `json:"id"`
	URL     string `json:"url"`
	Total   int64  `json:"total"`
	Unread  int64  `json:"unread"`
	Account string
	Name    string
}

type message struct {
	ID      int64  `json:"id"`
	Date    int64  `json:"date"`
	Subject string `json:"subject"`
	Sender  string `json:"sender"`
	Mailbox int64  `json:"mailbox"`
	Read    int    `json:"read"`
	Flagged int    `json:"flagged"`
	Preview string `json:"preview"`
}

func (s *store) database() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root := filepath.Join(home, "Library", "Mail")
		entries, err := os.ReadDir(root)
		if err != nil {
			return "", mailAccessError(err)
		}
		latest := -1
		for _, entry := range entries {
			version, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), "V"))
			if err != nil || !strings.HasPrefix(entry.Name(), "V") || version <= latest {
				continue
			}
			path := filepath.Join(root, entry.Name(), "MailData", "Envelope Index")
			if _, err := os.Stat(path); err == nil {
				s.path, latest = path, version
			}
		}
		if s.path == "" {
			return "", fmt.Errorf("Mail Envelope Index not found; open Mail and sync an account, or use -db PATH. Grant Full Disk Access to the host app if Mail data is inaccessible")
		}
	}
	path, err := filepath.Abs(s.path)
	return path, err
}

func mailAccessError(err error) error {
	return fmt.Errorf("cannot read Mail's local index: %w; grant Full Disk Access to the terminal/MCP host in System Settings > Privacy & Security, then restart it", err)
}

func (s *store) query(ctx context.Context, sql string, result any) error {
	path, err := s.database()
	if err != nil {
		return err
	}
	// No shell, user sqlite3 init file, writable connection, or SQLite extension.
	cmd := exec.CommandContext(ctx, "/usr/bin/sqlite3", "-init", "/dev/null", "-batch", "-bail", "-readonly", "-json", "-cmd", ".timeout 1000", path, "PRAGMA query_only=ON; "+sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return fmt.Errorf("Mail index query stopped: %w; narrow the mailbox, sender or date range", ctx.Err())
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if strings.Contains(detail, "no such") {
			return fmt.Errorf("unsupported Mail index schema: %s", detail)
		}
		if strings.Contains(detail, "locked") || strings.Contains(detail, "busy") {
			return fmt.Errorf("Mail's local index is busy; try again shortly")
		}
		if detail == "" {
			detail = err.Error()
		}
		return mailAccessError(fmt.Errorf("%s", detail))
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return nil // sqlite3 emits no JSON at all for an empty result.
	}
	if err := json.Unmarshal(output, result); err != nil {
		return fmt.Errorf("decode Mail index result: %w", err)
	}
	return nil
}

func (s *store) mailboxes(ctx context.Context) ([]mailbox, error) {
	var boxes []mailbox
	err := s.query(ctx, `SELECT ROWID AS id, url, total_count AS total, unread_count AS unread FROM mailboxes ORDER BY url`, &boxes)
	if err != nil {
		return nil, err
	}
	for i := range boxes {
		u, err := url.Parse(boxes[i].URL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL for mailbox %d: %w", boxes[i].ID, err)
		}
		boxes[i].Account, boxes[i].Name = u.Host, strings.TrimPrefix(u.Path, "/")
	}
	return boxes, nil
}

func selectMailboxes(boxes []mailbox, name, account string) ([]mailbox, error) {
	var selected []mailbox
	for _, box := range boxes {
		if account != "" && !strings.EqualFold(account, box.Account) {
			continue
		}
		if name == "" || strings.EqualFold(name, "all") || name == strconv.FormatInt(box.ID, 10) || strings.EqualFold(name, box.Name) {
			selected = append(selected, box)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("mailbox/account not found; use mailboxes to find an exact mailbox path, numeric ID and account UUID")
	}
	return selected, nil
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func containsSQL(column, value string) string {
	value = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
	return column + " LIKE " + sqlString("%"+value+"%") + ` ESCAPE '\'`
}

const messageColumns = `m.ROWID AS id, m.date_received AS date,
 substr(COALESCE(m.subject_prefix,'') || COALESCE(s.subject,''),1,400) AS subject,
 substr(trim(COALESCE(a.comment,'') || ' <' || COALESCE(a.address,'') || '>'),1,320) AS sender,
 m.mailbox, m.read, m.flagged`

const messageJoins = ` FROM messages m LEFT JOIN subjects s ON s.ROWID=m.subject LEFT JOIN addresses a ON a.ROWID=m.sender`

func (s *store) get(ctx context.Context, id int64) (message, mailbox, error) {
	rows, boxes, err := s.getMany(ctx, []int64{id})
	if err != nil {
		return message{}, mailbox{}, err
	}
	if len(rows) == 0 {
		return message{}, mailbox{}, fmt.Errorf("message %d no longer exists; search again", id)
	}
	for _, box := range boxes {
		if box.ID == rows[0].Mailbox {
			return rows[0], box, nil
		}
	}
	return message{}, mailbox{}, fmt.Errorf("mailbox for message %d no longer exists; search again", id)
}

func (s *store) getMany(ctx context.Context, ids []int64) ([]message, []mailbox, error) {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.FormatInt(id, 10)
	}
	var rows []message
	if err := s.query(ctx, "SELECT "+messageColumns+messageJoins+" WHERE m.ROWID IN ("+strings.Join(values, ",")+") AND m.deleted=0", &rows); err != nil {
		return nil, nil, err
	}
	boxes, err := s.mailboxes(ctx)
	return rows, boxes, err
}

func (s *store) search(ctx context.Context, args searchArgs) ([]message, string, []mailbox, error) {
	boxes, err := s.mailboxes(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	selected, err := selectMailboxes(boxes, args.Mailbox, args.Account)
	if err != nil {
		return nil, "", nil, err
	}
	where := []string{"m.deleted=0"}
	if len(selected) != len(boxes) {
		ids := make([]string, 0, len(selected))
		for _, box := range selected {
			ids = append(ids, strconv.FormatInt(box.ID, 10))
		}
		idList := strings.Join(ids, ",")
		// Gmail stores one physical message in All Mail and INBOX/Sent/etc as
		// labels. UNION avoids duplicate results when several labels match.
		where = append(where, "m.ROWID IN (SELECT ROWID FROM messages WHERE mailbox IN ("+idList+") UNION SELECT message_id FROM labels WHERE mailbox_id IN ("+idList+"))")
	}
	if args.Query != "" {
		where = append(where, "("+containsSQL("COALESCE(m.subject_prefix,'') || s.subject", args.Query)+" OR "+containsSQL("a.address", args.Query)+" OR "+containsSQL("a.comment", args.Query)+" OR m.summary IN (SELECT ROWID FROM summaries WHERE "+containsSQL("summary", args.Query)+"))")
	}
	if args.Subject != "" {
		where = append(where, containsSQL("COALESCE(m.subject_prefix,'') || s.subject", args.Subject))
	}
	if args.Sender != "" {
		where = append(where, "("+containsSQL("a.address", args.Sender)+" OR "+containsSQL("a.comment", args.Sender)+")")
	}
	if args.Recipient != "" {
		where = append(where, "m.ROWID IN (SELECT r.message FROM recipients r JOIN addresses ra ON ra.ROWID=r.address WHERE "+containsSQL("ra.address", args.Recipient)+" OR "+containsSQL("ra.comment", args.Recipient)+")")
	}
	if args.Unread != nil {
		where = append(where, fmt.Sprintf("m.read=%d", boolInt(!*args.Unread)))
	}
	if args.Flagged != nil {
		where = append(where, fmt.Sprintf("m.flagged=%d", boolInt(*args.Flagged)))
	}
	if args.From != "" {
		where = append(where, fmt.Sprintf("m.date_received>=%d", args.DateFrom))
	}
	if args.To != "" {
		where = append(where, fmt.Sprintf("m.date_received<%d", args.DateTo))
	}
	if args.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(args.Cursor)
		var position struct{ Date, ID int64 }
		if err != nil || json.Unmarshal(data, &position) != nil || position.ID <= 0 {
			return nil, "", nil, fmt.Errorf("invalid cursor; copy the next cursor from search_emails and keep the same filters")
		}
		where = append(where, fmt.Sprintf("(m.date_received<%d OR (m.date_received=%d AND m.ROWID<%d))", position.Date, position.Date, position.ID))
	}
	preview := ""
	if args.Preview {
		preview = ", (SELECT substr(summary,1,240) FROM summaries WHERE ROWID=m.summary) AS preview"
	}
	var rows []message
	err = s.query(ctx, "SELECT "+messageColumns+preview+messageJoins+" WHERE "+strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY m.date_received DESC, m.ROWID DESC LIMIT %d", args.Limit+1), &rows)
	if err != nil {
		return nil, "", nil, err
	}
	next := ""
	if len(rows) > args.Limit {
		rows = rows[:args.Limit]
		last := rows[len(rows)-1]
		data, _ := json.Marshal(struct{ Date, ID int64 }{last.Date, last.ID})
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	return rows, next, boxes, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
