package imports

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// maxCSVBytes is the largest CSV file an import takes.
const maxCSVBytes = 50 << 20

// maxCSVRows bounds the rows of one file.
const maxCSVRows = 500_000

// csvFile is a parsed CSV file: its header and rows, with the line each row started on.
type csvFile struct {
	Header []string
	Rows   [][]string
	Lines  []int
}

var errCSVEmpty = errors.New("The file has no rows under its header.")

// parseCSV reads a CSV file: UTF-8 (with or without a byte order mark) or UTF-16 with one,
// separated by commas, semicolons or tabs, with a header row.
func parseCSV(raw []byte) (*csvFile, error) {
	text, err := decodeText(raw)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(text) {
		return nil, errors.New("Save the file as UTF-8 and upload it again.")
	}
	r := csv.NewReader(bytes.NewReader(text))
	r.Comma = sniffComma(text)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.ReuseRecord = false
	f := &csvFile{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return nil, fmt.Errorf("Line %d of the file could not be read: %s.", pe.Line, pe.Err)
			}
			return nil, err
		}
		if f.Header == nil {
			for i, h := range rec {
				h = strings.TrimSpace(h)
				if h == "" {
					h = "Column " + strconv.Itoa(i+1)
				}
				f.Header = append(f.Header, h)
			}
			continue
		}
		if blank(rec) {
			continue
		}
		if len(f.Rows) >= maxCSVRows {
			return nil, fmt.Errorf("The file has more than %d rows. Split it into smaller files.", maxCSVRows)
		}
		line, _ := r.FieldPos(0)
		f.Rows = append(f.Rows, rec)
		f.Lines = append(f.Lines, line)
	}
	if len(f.Header) == 0 || len(f.Rows) == 0 {
		return nil, errCSVEmpty
	}
	return f, nil
}

func blank(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// decodeText strips a UTF-8 byte order mark and turns UTF-16 (with its mark) into UTF-8.
func decodeText(raw []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return raw[3:], nil
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		out, _, err := transform.Bytes(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder(), raw)
		if err != nil {
			return nil, errors.New("The file's text could not be read. Save it as UTF-8 and upload it again.")
		}
		return out, nil
	}
	return raw, nil
}

// sniffComma picks the separator the header line uses most, outside quotes.
func sniffComma(text []byte) rune {
	end := bytes.IndexByte(text, '\n')
	if end < 0 {
		end = len(text)
	}
	counts := map[rune]int{}
	quoted := false
	for _, c := range string(text[:end]) {
		switch {
		case c == '"':
			quoted = !quoted
		case !quoted && (c == ',' || c == ';' || c == '\t'):
			counts[c]++
		}
	}
	best := ','
	for _, c := range []rune{';', '\t'} {
		if counts[c] > counts[best] {
			best = c
		}
	}
	return best
}

// Column names people use for each field, normalized (normHeader).
var csvAliases = []struct {
	field   string
	aliases []string
}{
	{"ticket_id", []string{"ticket id", "ticket", "ticket number", "ticket no", "conversation id", "conversation", "thread id", "case id", "case number", "issue key", "issue id", "id", "number", "#"}},
	{"subject", []string{"subject", "title", "summary", "topic", "ticket subject"}},
	{"body", []string{"body", "message", "description", "content", "text", "comment", "html", "message body", "body html", "body text", "reply"}},
	{"from_email", []string{"from email", "email", "from", "sender email", "sender", "requester email", "customer email", "contact email", "author email", "user email", "e mail", "reporter email"}},
	{"from_name", []string{"from name", "name", "sender name", "requester name", "customer name", "contact name", "author name", "author", "requester", "customer", "user name", "full name", "reporter"}},
	{"author", []string{"author type", "sender type", "from type", "direction", "role", "type", "message type", "is agent", "visibility"}},
	{"status", []string{"status", "state", "ticket status"}},
	{"priority", []string{"priority", "urgency", "importance"}},
	{"tags", []string{"tags", "labels", "tag", "label", "categories"}},
	{"created_at", []string{"created at", "created", "date", "created date", "timestamp", "time", "sent at", "sent", "date created", "message date", "received at", "created time"}},
	{"assignee_email", []string{"assignee email", "assignee", "agent email", "agent", "assigned to", "owner", "owner email", "responder"}},
	{"team", []string{"team", "group", "department", "queue", "inbox", "mailbox"}},
	{"company", []string{"company", "organization", "organisation", "org", "account", "company name"}},
	{"message_id", []string{"message id", "rfc message id", "email message id", "internet message id", "rfc 822 message id", "msg id"}},
	{"in_reply_to", []string{"in reply to", "in reply to id", "in reply to message id", "parent message id"}},
	{"references", []string{"references", "email references", "references header"}},
}

var headerSpace = regexp.MustCompile(`[\s_\-.]+`)

func normHeader(h string) string {
	return strings.TrimSpace(headerSpace.ReplaceAllString(strings.ToLower(h), " "))
}

// suggestMapping guesses the column of each field from the header names. A column is used
// for one field only; aliases earlier in a field's list win.
func suggestMapping(header []string) ImportCSVMapping {
	normed := make([]string, len(header))
	for i, h := range header {
		normed[i] = normHeader(h)
	}
	used := map[int]bool{}
	pick := map[string]string{}
	for _, f := range csvAliases {
		for _, a := range f.aliases {
			i := slices.Index(normed, a)
			if i >= 0 && !used[i] {
				used[i] = true
				pick[f.field] = header[i]
				break
			}
		}
	}
	return ImportCSVMapping{TicketID: pick["ticket_id"], Subject: pick["subject"], Body: pick["body"], FromEmail: pick["from_email"],
		FromName: pick["from_name"], Author: pick["author"], Status: pick["status"], Priority: pick["priority"], Tags: pick["tags"],
		CreatedAt: pick["created_at"], AssigneeEmail: pick["assignee_email"], Team: pick["team"], Company: pick["company"],
		MessageID: pick["message_id"], InReplyTo: pick["in_reply_to"], References: pick["references"]}
}

// checkMapping refuses a mapping that names a column the file does not have, or has
// nothing to import.
func checkMapping(m ImportCSVMapping, header []string) error {
	for _, c := range m.columns() {
		if c != "" && !slices.Contains(header, c) {
			return fmt.Errorf("The file has no column %q.", c)
		}
	}
	if m.Body == "" && m.Subject == "" {
		return errors.New("Choose the column with the message, or at least the subject.")
	}
	return nil
}

func (m ImportCSVMapping) columns() []string {
	return []string{m.TicketID, m.Subject, m.Body, m.FromEmail, m.FromName, m.Author, m.Status, m.Priority, m.Tags,
		m.CreatedAt, m.AssigneeEmail, m.Team, m.Company, m.MessageID, m.InReplyTo, m.References}
}

// csvSource reads tickets from a parsed file with a mapping. Rows with the same ticket id
// are one ticket, in the order the file first names them.
type csvSource struct {
	name    string
	key     string
	tickets []source.Ticket
	// agents are the people whose rows are agent rows or who are assignees, by email.
	agents []source.Agent
	groups []source.Group
	// problems are rows left out, with why.
	problems []ImportProblem
}

// csvPageSize is how many tickets one page of a CSV source holds.
const csvPageSize = 100

func newCSVSource(f *csvFile, m ImportCSVMapping, filename, key string) *csvSource {
	col := map[string]int{}
	for i, h := range f.Header {
		if _, ok := col[h]; !ok {
			col[h] = i
		}
	}
	get := func(rec []string, name string) string {
		if name == "" {
			return ""
		}
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	s := &csvSource{name: filename, key: key}
	index := map[string]int{}
	agentSeen := map[string]bool{}
	groupSeen := map[string]bool{}
	addAgent := func(email, name string) {
		email = strings.ToLower(email)
		if email == "" {
			return
		}
		if agentSeen[email] {
			// Seen first as an assignee, without a name: an agent row names them.
			if i := slices.IndexFunc(s.agents, func(a source.Agent) bool { return a.ID == email }); i >= 0 && s.agents[i].Name == "" {
				s.agents[i].Name = name
			}
			return
		}
		agentSeen[email] = true
		s.agents = append(s.agents, source.Agent{ID: email, Email: email, Name: name})
	}
	now := time.Now().UTC()
	for n, rec := range f.Rows {
		line := f.Lines[n]
		body, subject := get(rec, m.Body), get(rec, m.Subject)
		if body == "" && subject == "" {
			s.problems = append(s.problems, ImportProblem{At: now, Item: "Row " + strconv.Itoa(line), Message: "No message or subject, so it was left out."})
			continue
		}
		id := get(rec, m.TicketID)
		if id == "" {
			id = "row-" + strconv.Itoa(line)
		}
		created := parseWhen(get(rec, m.CreatedAt))
		kind := authorKind(get(rec, m.Author))
		email := strings.ToLower(get(rec, m.FromEmail))
		if email != "" && !looksLikeEmail(email) {
			email = ""
		}
		name := get(rec, m.FromName)
		i, ok := index[id]
		if !ok {
			i = len(s.tickets)
			index[id] = i
			s.tickets = append(s.tickets, source.Ticket{ID: id, Ref: id, Status: "", Priority: "normal", Channel: "email", Loaded: true, CreatedAt: created})
		}
		t := &s.tickets[i]
		if t.Subject == "" && subject != "" {
			t.Subject = subject
		}
		if v := get(rec, m.Status); v != "" {
			t.Status = csvStatus(v)
		}
		if v := get(rec, m.Priority); v != "" {
			t.Priority = csvPriority(v)
		}
		for _, tag := range strings.FieldsFunc(get(rec, m.Tags), func(r rune) bool { return r == ',' || r == ';' || r == '|' }) {
			if tag = strings.TrimSpace(tag); tag != "" && !slices.Contains(t.Tags, tag) {
				t.Tags = append(t.Tags, tag)
			}
		}
		if v := strings.ToLower(get(rec, m.AssigneeEmail)); looksLikeEmail(v) {
			t.AssigneeID = v
			addAgent(v, "")
		}
		if v := get(rec, m.Team); v != "" {
			t.GroupID = v
			if !groupSeen[strings.ToLower(v)] {
				groupSeen[strings.ToLower(v)] = true
				s.groups = append(s.groups, source.Group{ID: v, Name: v})
			}
		}
		if kind == "customer" && t.Requester == nil && (email != "" || name != "") {
			t.Requester = &source.Person{ID: email, Email: email, Name: name, Company: get(rec, m.Company)}
		}
		if t.CreatedAt.IsZero() || (!created.IsZero() && created.Before(t.CreatedAt)) {
			t.CreatedAt = created
		}
		msg := source.Message{ID: id + ":" + strconv.Itoa(line), Agent: kind != "customer", Note: kind == "note",
			Author: source.Person{Email: email, Name: name}, CreatedAt: created, References: messageIDs(get(rec, m.References))}
		if ids := messageIDs(get(rec, m.MessageID)); len(ids) > 0 {
			msg.MessageID = ids[0]
		}
		if ids := messageIDs(get(rec, m.InReplyTo)); len(ids) > 0 {
			msg.InReplyTo = ids[0]
		}
		if body == "" {
			body = subject
		}
		if looksLikeHTML(body) {
			msg.HTML = body
		} else {
			msg.Text = body
		}
		if msg.Agent {
			msg.AuthorID = email
			addAgent(email, name)
		}
		t.Messages = append(t.Messages, msg)
	}
	for i := range s.tickets {
		t := &s.tickets[i]
		slices.SortStableFunc(t.Messages, func(a, b source.Message) int {
			if a.CreatedAt.IsZero() || b.CreatedAt.IsZero() {
				return 0
			}
			return a.CreatedAt.Compare(b.CreatedAt)
		})
		if t.Status == "" {
			t.Status = "closed" // history without a status is done with
		}
		if t.Subject == "" && len(t.Messages) > 0 {
			t.Subject = firstWords(firstNonEmpty(t.Messages[0].Text, hd.HTMLToText(t.Messages[0].HTML)), 80)
		}
		for _, msg := range t.Messages {
			if msg.CreatedAt.After(t.UpdatedAt) {
				t.UpdatedAt = msg.CreatedAt
			}
		}
		if t.Requester == nil {
			for _, msg := range t.Messages {
				if !msg.Agent && (msg.Author.Email != "" || msg.Author.Name != "") {
					p := msg.Author
					t.Requester = &p
					break
				}
			}
		}
	}
	return s
}

func (s *csvSource) Check(context.Context) (source.Scan, error) {
	return source.Scan{Account: source.Account{Key: s.key, Name: s.name}, Tickets: len(s.tickets), Articles: 0,
		Agents: s.agents, Groups: s.groups}, nil
}

func (s *csvSource) Tickets(_ context.Context, cursor string, since, _ *time.Time) (source.Page[source.Ticket], error) {
	start, _ := strconv.Atoi(cursor)
	if start < 0 || start > len(s.tickets) {
		start = len(s.tickets)
	}
	end := min(start+csvPageSize, len(s.tickets))
	page := source.Page[source.Ticket]{}
	for _, t := range s.tickets[start:end] {
		if since != nil && !t.CreatedAt.IsZero() && t.CreatedAt.Before(*since) {
			continue
		}
		page.Items = append(page.Items, t)
	}
	if end < len(s.tickets) {
		page.Next = strconv.Itoa(end)
	}
	return page, nil
}

func (s *csvSource) Load(context.Context, *source.Ticket) error { return nil }

func (s *csvSource) Articles(context.Context, string) (source.Page[source.Article], error) {
	return source.Page[source.Article]{}, source.ErrUnsupported
}

func (s *csvSource) Download(context.Context, source.Attachment) (io.ReadCloser, error) {
	return nil, source.ErrUnsupported
}

// authorKind reads who wrote a row: agent, customer or note (an agent's internal note).
func authorKind(v string) string {
	switch normHeader(v) {
	case "agent", "staff", "admin", "support", "operator", "teammate", "team", "outbound", "out", "outgoing", "reply", "public reply",
		"sent", "employee", "true", "yes", "1":
		return "agent"
	case "note", "internal", "private", "internal note", "private note", "comment internal":
		return "note"
	}
	return "customer"
}

func csvStatus(v string) string {
	switch normHeader(v) {
	case "open", "new", "active", "in progress", "assigned", "reopened", "unresolved", "unassigned", "todo", "to do":
		return "open"
	case "pending", "waiting on customer", "awaiting customer", "awaiting reply", "waiting for customer", "pending customer":
		return "pending"
	case "on hold", "hold", "onhold", "waiting", "snoozed", "waiting on third party", "paused", "blocked":
		return "on_hold"
	case "solved", "resolved", "done", "fixed", "completed", "complete", "answered":
		return "solved"
	}
	return "closed"
}

func csvPriority(v string) string {
	switch normHeader(v) {
	case "low", "p4", "4 low", "minor", "lowest":
		return "low"
	case "high", "p2", "important", "major":
		return "high"
	case "urgent", "p1", "critical", "highest", "blocker", "emergency":
		return "urgent"
	}
	// Freshdesk-style numbers: 1 low, 2 medium, 3 high, 4 urgent.
	switch strings.TrimSpace(v) {
	case "1":
		return "low"
	case "3":
		return "high"
	case "4":
		return "urgent"
	}
	return "normal"
}

var whenLayouts = []string{
	time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006/01/02 15:04:05", "2006/01/02 15:04", "2006/01/02",
	"01/02/2006 15:04:05", "01/02/2006 15:04", "01/02/2006 3:04 PM", "01/02/2006 3:04:05 PM", "01/02/2006", "1/2/2006 15:04", "1/2/2006 3:04 PM", "1/2/2006",
	"02.01.2006 15:04:05", "02.01.2006 15:04", "02.01.2006", "Jan 2, 2006 3:04 PM", "Jan 2, 2006 15:04", "Jan 2, 2006", "2 Jan 2006 15:04", "2 Jan 2006",
	"January 2, 2006 3:04 PM", "January 2, 2006", time.RFC1123Z, time.RFC1123, time.RFC822Z, "Mon, 2 Jan 2006 15:04:05 -0700",
}

// parseWhen reads a date and time in the formats spreadsheets and exports write, or unix
// seconds or milliseconds. Times without a zone are UTC; unreadable values are the zero time.
func parseWhen(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		switch {
		case n > 1e11 && n < 1e14:
			return time.UnixMilli(n).UTC()
		case n > 1e8 && n < 1e11:
			return time.Unix(n, 0).UTC()
		}
		return time.Time{}
	}
	for _, l := range whenLayouts {
		if t, err := time.Parse(l, v); err == nil {
			if t.Year() < 1990 || t.After(time.Now().Add(24*time.Hour)) {
				return time.Time{}
			}
			return t.UTC()
		}
	}
	return time.Time{}
}

var bracketedID = regexp.MustCompile(`<([^<>\s]{1,900})>`)

// maxReferences is how many References a message keeps, the latest.
const maxReferences = 50

// messageIDs reads email Message-IDs from a cell, as "<id>": bracketed ones as a header
// writes them, else values separated by spaces, commas or semicolons. A value without an
// "@" is not a Message-ID (an export's own message number) and is left out.
func messageIDs(v string) []string {
	var raw []string
	if m := bracketedID.FindAllStringSubmatch(v, -1); m != nil {
		for _, g := range m {
			raw = append(raw, g[1])
		}
	} else {
		raw = strings.Fields(strings.NewReplacer(",", " ", ";", " ").Replace(v))
	}
	var out []string
	for _, id := range raw {
		if len(id) > 900 || !strings.Contains(id, "@") || strings.ContainsAny(id, "<>") {
			continue
		}
		if id = "<" + id + ">"; !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) > maxReferences {
		out = out[len(out)-maxReferences:]
	}
	return out
}

var emailPattern = regexp.MustCompile(`^[^\s@<>()",;]+@[^\s@<>()",;]+\.[^\s@<>()",;]+$`)

func looksLikeEmail(s string) bool { return len(s) <= 320 && emailPattern.MatchString(s) }

var htmlTag = regexp.MustCompile(`(?i)<(p|br|div|span|a|b|strong|em|i|ul|ol|li|table|img|h[1-6]|blockquote|pre)(\s|>|/>)`)

func looksLikeHTML(s string) bool { return htmlTag.MatchString(s) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// firstWords is the start of text on one line, at most n runes, cut at a word.
func firstWords(text string, n int) string {
	s := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	out := string(r)
	if i := strings.LastIndex(out, " "); i > n/2 {
		out = out[:i]
	}
	return strings.TrimRight(out, " ,.;:") + "…"
}
