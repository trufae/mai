package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const maxMessageBytes = 16 << 20

// Resolve Mail's sharded cache with directory reads, never a recursive scan.
// For example, message 26170 is in Data/6/2/Messages/26170.emlx.
func (s *store) cachedMessage(ctx context.Context, msg message, box mailbox) ([]byte, error) {
	db, err := s.database()
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(filepath.Dir(db))
	u, err := url.Parse(box.URL)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, box.Account)
	if u.Scheme == "local" {
		dir = filepath.Join(root, "Mailboxes")
	}
	for _, name := range strings.Split(box.Name, "/") {
		if name == "" || name == "." || name == ".." {
			return nil, fmt.Errorf("invalid mailbox path")
		}
		dir = filepath.Join(dir, name+".mbox")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	parts := []string{"Data"}
	shard := strconv.FormatInt(msg.ID/1000, 10)
	if msg.ID >= 1000 {
		for i := len(shard) - 1; i >= 0; i-- {
			parts = append(parts, string(shard[i]))
		}
	}
	parts = append(parts, "Messages", strconv.FormatInt(msg.ID, 10)+".emlx")
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(append([]string{dir, entry.Name()}, parts...)...)
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		data, err := readEMLX(f)
		f.Close()
		return data, err
	}
	return nil, fmt.Errorf("message is not fully cached locally")
}

func readEMLX(r io.Reader) ([]byte, error) {
	reader := bufio.NewReader(r)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > 32 {
		return nil, fmt.Errorf("invalid emlx length header")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(line)), 10, 64)
	if err != nil || n < 0 || n > maxMessageBytes {
		return nil, fmt.Errorf("message exceeds the 16 MiB local MIME limit or has an invalid length")
	}
	data := make([]byte, n)
	_, err = io.ReadFull(reader, data)
	return data, err // The trailing plist is not part of the RFC message.
}

type mailContent struct {
	Text        string
	Attachments []string
}

var (
	htmlScript = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>`)
	htmlBreak  = regexp.MustCompile(`(?i)<br\b[^>]*>|</(?:p|div|tr|li|h[1-6])\s*>`)
	htmlTag    = regexp.MustCompile(`<[^>]*>`)
)

func htmlText(s string) string {
	s = htmlScript.ReplaceAllString(s, "")
	s = htmlBreak.ReplaceAllString(s, "\n")
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, ""))
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func charsetReader(charset string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return r, nil
	case "iso-8859-1", "latin1", "windows-1252":
		data, err := io.ReadAll(io.LimitReader(r, maxMessageBytes))
		if err != nil {
			return nil, err
		}
		const cp1252 = "€\u0081‚ƒ„…†‡ˆ‰Š‹Œ\u008dŽ\u008f\u0090‘’“”•–—˜™š›œ\u009džŸ"
		replacements := []rune(cp1252)
		var text strings.Builder
		for _, b := range data {
			c := rune(b)
			if strings.EqualFold(charset, "windows-1252") && b >= 0x80 && b < 0xa0 {
				c = replacements[b-0x80]
			}
			text.WriteRune(c)
		}
		return strings.NewReader(text.String()), nil
	default:
		return nil, fmt.Errorf("unsupported MIME charset %q", charset)
	}
}

func decodeMIME(header textproto.MIMEHeader, r io.Reader, depth int) (mailContent, error) {
	if depth > 12 {
		return mailContent{}, fmt.Errorf("MIME nesting is too deep")
	}
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if header.Get("Content-Type") == "" {
		mediaType, err = "text/plain", nil
	}
	if err != nil {
		return mailContent{}, err
	}
	disposition, dp, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dp["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if disposition == "attachment" || filename != "" {
		if filename == "" {
			filename = "attachment"
		}
		decoded, err := (&mime.WordDecoder{CharsetReader: charsetReader}).DecodeHeader(filename)
		if err == nil {
			filename = decoded
		}
		return mailContent{Attachments: []string{filename}}, nil
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(r, params["boundary"])
		var result mailContent
		plain := false
		for i := 0; ; i++ {
			part, err := reader.NextPart()
			if err == io.EOF {
				return result, nil
			}
			if err != nil {
				return mailContent{}, err
			}
			if i >= 200 {
				return mailContent{}, fmt.Errorf("too many MIME parts")
			}
			content, err := decodeMIME(part.Header, part, depth+1)
			part.Close()
			if err != nil {
				return mailContent{}, err
			}
			result.Attachments = append(result.Attachments, content.Attachments...)
			if content.Text == "" {
				continue
			}
			isPlain := strings.HasPrefix(strings.ToLower(part.Header.Get("Content-Type")), "text/plain")
			if mediaType == "multipart/alternative" {
				if result.Text == "" || (isPlain && !plain) {
					result.Text, plain = content.Text, isPlain
				}
			} else {
				result.Text += content.Text + "\n"
			}
		}
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		return mailContent{}, nil
	}
	switch strings.ToLower(header.Get("Content-Transfer-Encoding")) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	if params["charset"] != "" {
		r, err = charsetReader(params["charset"], r)
		if err != nil {
			return mailContent{}, err
		}
	}
	data, err := io.ReadAll(io.LimitReader(r, maxMessageBytes))
	text := string(data)
	if mediaType == "text/html" {
		text = htmlText(text)
	}
	return mailContent{Text: strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))}, err
}

func (s *service) read(ctx context.Context, args map[string]any) (any, error) {
	var a struct {
		ID       int64 `json:"id"`
		MaxChars int   `json:"max_chars"`
	}
	a.MaxChars = 6000
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 || a.MaxChars < 1 || a.MaxChars > 20000 {
		return nil, fmt.Errorf("id must be positive and max_chars must be 1..20000")
	}
	msg, box, err := s.store.get(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	data, cacheErr := s.store.cachedMessage(ctx, msg, box)
	var content mailContent
	var headers mail.Header
	if cacheErr == nil {
		var parsed *mail.Message
		parsed, cacheErr = mail.ReadMessage(bytes.NewReader(data))
		if cacheErr == nil {
			headers = parsed.Header
			content, cacheErr = decodeMIME(textproto.MIMEHeader(headers), parsed.Body, 0)
		}
	}
	source := "local cache"
	if cacheErr != nil {
		ref, err := messageRef(msg, box)
		if err != nil {
			return nil, err
		}
		body, err := s.script(ctx, "set targetMessage to "+ref+fmt.Sprintf("\nset bodyText to content of targetMessage as text\nif (count characters of bodyText) > %d then set bodyText to text 1 thru %d of bodyText\nreturn bodyText", a.MaxChars, a.MaxChars+1))
		if err != nil {
			return nil, fmt.Errorf("cached read failed (%v); targeted Mail read failed: %w", cacheErr, err)
		}
		content.Text, source = body, "Mail (uncached message)"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "## %s\n\nID: %d · %s · %s\nFrom: %s\n", inline(msg.Subject), msg.ID, formatDate(msg.Date), inline(box.Name), inline(msg.Sender))
	decoder := mime.WordDecoder{CharsetReader: charsetReader}
	if headers.Get("To") != "" {
		to, _ := decoder.DecodeHeader(headers.Get("To"))
		fmt.Fprintf(&out, "To: %s\n", inline(clip(to, 1000)))
	}
	if mid := strings.Trim(headers.Get("Message-ID"), " <>"); mid != "" && len(mid) <= 998 {
		fmt.Fprintf(&out, "[Open in Mail](message://%%3C%s%%3E)\n", url.PathEscape(mid))
	}
	if len(content.Attachments) != 0 {
		names := content.Attachments
		if len(names) > 20 {
			names = names[:20]
		}
		for i := range names {
			names[i] = clip(names[i], 160)
		}
		fmt.Fprintf(&out, "Attachments (%d): %s\n", len(content.Attachments), inline(strings.Join(names, ", ")))
	}
	if content.Text == "" {
		content.Text = "[No readable inline text in this message]"
	}
	fmt.Fprintf(&out, "Source: %s\n\n%s", source, clip(content.Text, a.MaxChars))
	return out.String(), nil
}
