// Package front reads a Front account (contracts/imports.md) through the Core API with an
// API token: conversations from every shared inbox the token can see, their messages and
// comments, and the files on them. Syncs read the events feed; a token without event
// access falls back to walking the inboxes by last activity.
package front

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

const (
	apiHost = "api2.frontapp.com"
	apiBase = "https://" + apiHost
	appURL  = "https://app.frontapp.com"

	// perPage is the most Front lists at once.
	perPage = 100
	// maxPages bounds a listing read whole (inboxes, teammates, a conversation's messages).
	maxPages = 200
	// walkMargin is how far past the sync point an inbox walk reads before it stops,
	// since Front orders inboxes by last activity, which updated_at only approximates.
	walkMargin = 24 * time.Hour
)

// minGap spaces requests to Front: 60 a minute, under the smallest plan's burst. Tests set
// it to zero.
var minGap = time.Second

// maxResetWait caps a wait for Front's rate limit window to reset.
var maxResetWait = 61 * time.Second

type front struct {
	opts source.Options
	api  *source.Client // api2.frontapp.com, or Options.BaseURL in tests
	auth func(*http.Request)

	mu    sync.Mutex
	hosts map[string]*source.Client // <company>.api.frontapp.com, from next links and files

	inboxes []inbox // sorted by id; read once
}

// New builds a Front source. It checks the token's shape only.
func New(c source.Credentials, o source.Options) (source.Source, error) {
	token, err := source.Required(c, "token", "API token")
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return nil, source.Invalid("token", "The API token has no spaces; paste it again.")
	}
	s := &front{hosts: map[string]*source.Client{}}
	s.auth = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	s.api = source.NewClient(apiBase, o)
	p := &pacer{}
	s.api.HTTP = paced(s.api.HTTP, p)
	s.api.Files = paced(s.api.Files, p)
	s.api.Auth = s.auth
	s.opts = source.Options{Dev: o.Dev, HTTP: s.api.HTTP, Files: s.api.Files}
	return s, nil
}

// frontHost says h is one of Front's API hosts: api2.frontapp.com or a company's
// <company>.api.frontapp.com, which next links and file links point at.
func frontHost(h string) bool {
	h = strings.ToLower(h)
	if h == apiHost {
		return true
	}
	label, ok := strings.CutSuffix(h, ".api.frontapp.com")
	if !ok || label == "" || len(label) > 63 {
		return false
	}
	for _, r := range label {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// clientFor is the client for a path or a full link: the API client for a path or a link
// on its host, a client of the company's own API host (same token, same pacing) for a
// Front link elsewhere, and an error for anything else, so the token never leaves Front.
func (s *front) clientFor(raw string) (*source.Client, error) {
	if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
		return s.api, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(s.api.Base)
	if base != nil && strings.EqualFold(u.Host, base.Host) {
		return s.api, nil
	}
	if u.Scheme != "https" || !frontHost(u.Host) {
		return nil, errors.New("front: refusing a link off Front's API hosts")
	}
	host := strings.ToLower(u.Host)
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.hosts[host]
	if c == nil {
		c = source.NewClient("https://"+host, s.opts)
		c.Auth = s.auth
		s.hosts[host] = c
	}
	return c, nil
}

func (s *front) get(ctx context.Context, path string, q url.Values, out any) error {
	c, err := s.clientFor(path)
	if err != nil {
		return err
	}
	return c.Get(ctx, path, q, out)
}

// list is one page of a Front listing.
type list[T any] struct {
	Pagination struct {
		Next *string `json:"next"`
	} `json:"_pagination"`
	Results []T  `json:"_results"`
	Total   *int `json:"_total"`
}

func (l list[T]) next() string {
	if l.Pagination.Next == nil {
		return ""
	}
	return strings.TrimSpace(*l.Pagination.Next)
}

// listAll reads every page of a listing.
func listAll[T any](ctx context.Context, s *front, path string, q url.Values) ([]T, error) {
	var all []T
	next := path
	for n := 0; n < maxPages && next != ""; n++ {
		var out list[T]
		if err := s.get(ctx, next, q, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Results...)
		// A next link carries its own query.
		next, q = out.next(), nil
	}
	return all, nil
}

type inbox struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type teammate struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Type      string `json:"type"`
}

func (t *teammate) name() string {
	if n := strings.TrimSpace(strings.TrimSpace(t.FirstName) + " " + strings.TrimSpace(t.LastName)); n != "" {
		return n
	}
	if n := strings.TrimSpace(t.Username); n != "" {
		return n
	}
	return strings.TrimSpace(t.Email)
}

// person says the teammate is a person rather than a rule, macro or other bot.
func (t *teammate) person() bool { return t != nil && t.ID != "" && (t.Type == "" || t.Type == "user") }

func (s *front) loadInboxes(ctx context.Context) ([]inbox, error) {
	if s.inboxes != nil {
		return s.inboxes, nil
	}
	boxes, err := listAll[inbox](ctx, s, "/inboxes", url.Values{"limit": {strconv.Itoa(perPage)}})
	if err != nil {
		return nil, err
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].ID < boxes[j].ID })
	s.inboxes = append([]inbox{}, boxes...)
	return s.inboxes, nil
}

func (s *front) Check(ctx context.Context) (source.Scan, error) {
	scan := source.Scan{Tickets: -1, Articles: 0}
	var me struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	// The token's company names the account. Front answering /me with an error status
	// (other than a refusal) falls back to the oldest inbox below.
	meErr := s.get(ctx, "/me", nil, &me)
	var re *source.RemoteError
	if meErr != nil && !errors.As(meErr, &re) {
		return scan, meErr
	}
	boxes, err := s.loadInboxes(ctx)
	if err != nil {
		return scan, err
	}
	switch {
	case meErr == nil && strings.TrimSpace(me.ID) != "":
		scan.Account.Key = strings.TrimSpace(me.ID)
	case len(boxes) > 0:
		// Inbox ids are unique across Front, and the oldest one stays with the account.
		scan.Account.Key = "inbox-" + boxes[0].ID
	default:
		return scan, errors.New("front did not say which account this is")
	}
	scan.Account.Name = "Front"
	if n := strings.TrimSpace(me.Name); n != "" {
		scan.Account.Name = n
	}
	scan.Account.URL = appURL
	for _, b := range boxes {
		scan.Groups = append(scan.Groups, source.Group{ID: b.ID, Name: strings.TrimSpace(b.Name)})
	}

	mates, err := listAll[teammate](ctx, s, "/teammates", url.Values{"limit": {strconv.Itoa(perPage)}})
	if err != nil {
		return scan, err
	}
	for _, m := range mates {
		if !m.person() {
			continue
		}
		scan.Agents = append(scan.Agents, source.Agent{ID: m.ID, Name: m.name(), Email: strings.TrimSpace(m.Email)})
	}
	scan.Tickets = s.count(ctx)
	return scan, nil
}

// count totals open and archived conversations with two searches; -1 when Front does not
// say. Snoozed conversations are archived ones, and trashed ones are left out.
func (s *front) count(ctx context.Context) int {
	total := 0
	for _, q := range []string{"is:open", "is:archived"} {
		var out list[json.RawMessage]
		if err := s.get(ctx, "/conversations/search/"+url.PathEscape(q), url.Values{"limit": {"1"}}, &out); err != nil || out.Total == nil {
			return -1
		}
		total += *out.Total
	}
	return total
}

type recipient struct {
	Handle string `json:"handle"`
	Role   string `json:"role"`
	Name   string `json:"name"`
	Links  struct {
		Related struct {
			Contact *string `json:"contact"`
		} `json:"related"`
	} `json:"_links"`
}

// person is the recipient as a source.Person: its Front contact id when it has one,
// otherwise its handle, which is an email address or a phone number.
func (r recipient) person() source.Person {
	h := strings.TrimSpace(r.Handle)
	p := source.Person{Name: strings.TrimSpace(r.Name)}
	if c := r.Links.Related.Contact; c != nil && strings.TrimSpace(*c) != "" {
		link := strings.TrimRight(strings.TrimSpace(*c), "/")
		p.ID = link[strings.LastIndex(link, "/")+1:]
	} else if h != "" {
		p.ID = "handle:" + h
	}
	if strings.Contains(h, "@") {
		p.Email = h
	} else {
		p.Phone = h
	}
	return p
}

type conversation struct {
	ID                 string            `json:"id"`
	Subject            string            `json:"subject"`
	Type               string            `json:"type"`
	Status             string            `json:"status"`
	StatusCategory     string            `json:"status_category"`
	TicketIDs          []string          `json:"ticket_ids"`
	CreatedAt          float64           `json:"created_at"`
	UpdatedAt          float64           `json:"updated_at"`
	Assignee           *teammate         `json:"assignee"`
	Recipient          *recipient        `json:"recipient"`
	ScheduledReminders []json.RawMessage `json:"scheduled_reminders"`
	Tags               []struct {
		Name string `json:"name"`
	} `json:"tags"`
}

// at turns Front's seconds since the epoch (with a fraction) into a UTC time.
func at(secs float64) time.Time {
	if secs <= 0 {
		return time.Time{}
	}
	whole := int64(secs)
	return time.Unix(whole, int64((secs-float64(whole))*1e9)).UTC().Truncate(time.Millisecond)
}

// ticket maps a conversation; group is the inbox it was listed in ("" = unknown).
func ticket(c conversation, group string) source.Ticket {
	t := source.Ticket{
		ID:        c.ID,
		Ref:       c.ID,
		URL:       appURL + "/open/" + url.PathEscape(c.ID),
		Subject:   strings.TrimSpace(c.Subject),
		Priority:  "normal",
		Channel:   "email",
		GroupID:   group,
		CreatedAt: at(c.CreatedAt),
	}
	if ids := source.Strings(c.TicketIDs); len(ids) > 0 {
		t.Ref = ids[0]
	}
	t.Status = status(c)
	if c.Status == "deleted" || c.Status == "trashed" || c.Status == "spam" {
		t.Skip = true
	}
	// Discussions and tasks are teammates talking among themselves, not customer tickets.
	if c.Type != "" && c.Type != "conversation" {
		t.Skip = true
	}
	if c.Assignee.person() {
		t.AssigneeID = c.Assignee.ID
	}
	if r := c.Recipient; r != nil && strings.TrimSpace(r.Handle) != "" {
		p := r.person()
		t.Requester = &p
	}
	for _, tag := range c.Tags {
		t.Tags = append(t.Tags, tag.Name)
	}
	t.Tags = source.Strings(t.Tags)
	t.UpdatedAt = at(c.UpdatedAt)
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	if t.Status == "solved" || t.Status == "closed" {
		solved := t.UpdatedAt
		t.SolvedAt = &solved
	}
	return t
}

// status maps Front's status and, with ticketing, its status category. Archived with a
// reminder set is snoozed: it comes back by itself.
func status(c conversation) string {
	switch c.Status {
	case "deleted", "trashed", "spam":
		return "closed"
	case "archived":
		if len(c.ScheduledReminders) > 0 {
			return "on_hold"
		}
		return "closed"
	case "snoozed":
		return "on_hold"
	}
	switch c.StatusCategory {
	case "waiting":
		return "pending"
	case "resolved":
		return "solved"
	}
	return "open"
}

// cursor is where Tickets resumes. M is the mode: "list" (an import, inbox by inbox),
// "events" (a sync, the events feed) or "walk" (a sync without event access). I is the
// inbox, N the next page's link ("" = the inbox's first page).
type cursor struct {
	M string `json:"m"`
	I int    `json:"i,omitempty"`
	N string `json:"n,omitempty"`
}

func (c cursor) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}

func parseCursor(raw string, updatedSince *time.Time) (cursor, error) {
	if raw == "" {
		if updatedSince != nil {
			return cursor{M: "events"}, nil
		}
		return cursor{M: "list"}, nil
	}
	var c cursor
	if err := json.Unmarshal([]byte(raw), &c); err != nil || c.I < 0 {
		return c, errors.New("front: malformed cursor")
	}
	switch c.M {
	case "list", "walk", "events":
		return c, nil
	}
	return c, errors.New("front: malformed cursor")
}

// Tickets reads conversations a page at a time. Front lists an inbox by last activity,
// newest first, with no other order, so an import is not oldest first: it reads inbox by
// inbox and the engine's records keep a conversation seen twice (in two inboxes, or moved
// up by new activity) from being written twice.
func (s *front) Tickets(ctx context.Context, raw string, since, updatedSince *time.Time) (source.Page[source.Ticket], error) {
	var res source.Page[source.Ticket]
	c, err := parseCursor(raw, updatedSince)
	if err != nil {
		return res, err
	}
	if c.M == "events" {
		if updatedSince == nil {
			return res, errors.New("front: an events cursor without a sync point")
		}
		res, err = s.events(ctx, c, *updatedSince)
		if !errors.Is(err, source.ErrAuth) {
			return res, err
		}
		// The token may not read events (a scope Front grants separately): walk instead.
		c = cursor{M: "walk"}
	}
	if c.M == "walk" && updatedSince == nil {
		return res, errors.New("front: a walk cursor without a sync point")
	}
	return s.inboxPage(ctx, c, since, updatedSince)
}

// inboxPage reads one page of one inbox's conversations, for an import (list) or a sync
// walk (walk, which stops an inbox once it is well past updatedSince).
func (s *front) inboxPage(ctx context.Context, c cursor, since, updatedSince *time.Time) (source.Page[source.Ticket], error) {
	var res source.Page[source.Ticket]
	boxes, err := s.loadInboxes(ctx)
	if err != nil {
		return res, err
	}
	if c.I >= len(boxes) {
		return res, nil
	}
	box := boxes[c.I]
	path, q := c.N, url.Values(nil)
	if path == "" {
		path = "/inboxes/" + url.PathEscape(box.ID) + "/conversations"
		// Every status but trashed; snoozed conversations are archived ones.
		q = url.Values{"limit": {strconv.Itoa(perPage)}, "q[statuses][]": {"assigned", "unassigned", "archived"}}
	}
	var out list[conversation]
	if err := s.get(ctx, path, q, &out); err != nil {
		return res, err
	}
	stop := false
	for _, cv := range out.Results {
		t := ticket(cv, box.ID)
		if since != nil && t.CreatedAt.Before(*since) {
			continue
		}
		if c.M == "walk" {
			if t.UpdatedAt.Before(updatedSince.Add(-walkMargin)) {
				stop = true
			}
			if t.UpdatedAt.Before(*updatedSince) {
				continue
			}
		}
		res.Items = append(res.Items, t)
	}
	if n := out.next(); n != "" && !stop {
		res.Next = cursor{M: c.M, I: c.I, N: n}.String()
	} else if c.I+1 < len(boxes) {
		res.Next = cursor{M: c.M, I: c.I + 1}.String()
	}
	return res, nil
}

type event struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	EmittedAt    float64       `json:"emitted_at"`
	Conversation *conversation `json:"conversation"`
}

// events reads one page of the events feed since updatedSince, oldest first, and returns
// each conversation an event touched once.
func (s *front) events(ctx context.Context, c cursor, updatedSince time.Time) (source.Page[source.Ticket], error) {
	var res source.Page[source.Ticket]
	path, q := c.N, url.Values(nil)
	if path == "" {
		path = "/events"
		q = url.Values{
			"q[after]":   {strconv.FormatFloat(float64(updatedSince.UnixMilli())/1000, 'f', 3, 64)},
			"sort_by":    {"created_at"},
			"sort_order": {"asc"},
			"limit":      {strconv.Itoa(perPage)},
		}
	}
	var out list[event]
	if err := s.get(ctx, path, q, &out); err != nil {
		return res, err
	}
	seen := map[string]int{}
	for _, e := range out.Results {
		cv := e.Conversation
		if cv == nil || cv.ID == "" {
			continue
		}
		if cv.CreatedAt == 0 {
			// A preview carries only the id.
			var full conversation
			err := s.get(ctx, "/conversations/"+url.PathEscape(cv.ID), nil, &full)
			var re *source.RemoteError
			// Gone (404), or merged into another conversation (3xx): the survivor
			// has its own events.
			if errors.As(err, &re) && (re.Status == http.StatusNotFound || moved(re.Status)) {
				continue
			}
			if err != nil {
				return res, err
			}
			cv = &full
		}
		t := ticket(*cv, "")
		// A comment or a reply may not move updated_at; the event says the conversation
		// changed, so the engine must not take it as unchanged.
		if ev := at(e.EmittedAt); ev.After(t.UpdatedAt) {
			t.UpdatedAt = ev
		}
		if i, ok := seen[t.ID]; ok {
			res.Items[i] = t
			continue
		}
		seen[t.ID] = len(res.Items)
		res.Items = append(res.Items, t)
	}
	if n := out.next(); n != "" {
		res.Next = cursor{M: "events", N: n}.String()
	}
	return res, nil
}

type attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
	Metadata    struct {
		IsInline bool   `json:"is_inline"`
		CID      string `json:"cid"`
	} `json:"metadata"`
}

type message struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`
	IsInbound   bool         `json:"is_inbound"`
	IsDraft     bool         `json:"is_draft"`
	CreatedAt   float64      `json:"created_at"`
	Author      *teammate    `json:"author"`
	Recipients  []recipient  `json:"recipients"`
	Body        string       `json:"body"`
	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments"`
}

type comment struct {
	ID          string       `json:"id"`
	Author      *teammate    `json:"author"`
	Body        string       `json:"body"`
	PostedAt    float64      `json:"posted_at"`
	Attachments []attachment `json:"attachments"`
}

// Load reads a conversation's messages and comments. A conversation merged into another
// one is marked to skip: the one it was merged into carries its messages.
func (s *front) Load(ctx context.Context, t *source.Ticket) error {
	if t.Loaded {
		return nil
	}
	if !strings.HasPrefix(t.ID, "cnv_") || strings.ContainsAny(t.ID, "/?#") {
		return errors.New("front: malformed conversation id")
	}
	base := "/conversations/" + t.ID
	limit := url.Values{"limit": {strconv.Itoa(perPage)}}
	msgs, err := listAll[message](ctx, s, base+"/messages", limit)
	var re *source.RemoteError
	if errors.As(err, &re) && moved(re.Status) {
		// Merged into another conversation, which carries these messages.
		t.Skip = true
		return nil
	}
	if err != nil {
		return err
	}
	comments, err := listAll[comment](ctx, s, base+"/comments", limit)
	if err != nil {
		return err
	}
	if t.GroupID == "" {
		t.GroupID = s.group(ctx, base)
	}
	var out []source.Message
	first := ""
	for _, m := range msgs {
		if m.IsDraft {
			continue
		}
		if first == "" {
			first = m.Type
		}
		out = append(out, toMessage(m))
	}
	for _, c := range comments {
		out = append(out, toNote(c))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if first != "" && first != "email" {
		if first == "front_chat" {
			t.Channel = "chat"
		} else {
			t.Channel = "api"
		}
		t.Tags = append(t.Tags, "front-"+strings.ReplaceAll(strings.TrimPrefix(first, "front_"), "_", "-"))
	}
	t.Messages = out
	t.Loaded = true
	return nil
}

// group is the first inbox the import reads that holds the conversation; "" when none
// does or Front does not say. Only a sync needs it: an import lists by inbox.
func (s *front) group(ctx context.Context, base string) string {
	boxes, err := s.loadInboxes(ctx)
	if err != nil {
		return ""
	}
	in, err := listAll[inbox](ctx, s, base+"/inboxes", nil)
	if err != nil {
		return ""
	}
	known := map[string]bool{}
	for _, b := range in {
		known[b.ID] = true
	}
	for _, b := range boxes {
		if known[b.ID] {
			return b.ID
		}
	}
	return ""
}

func toMessage(m message) source.Message {
	out := source.Message{
		ID:        m.ID,
		Agent:     !m.IsInbound,
		HTML:      cleanBody(m.Body, m.Attachments),
		Text:      cleanText(m.Text),
		CreatedAt: at(m.CreatedAt),
	}
	var from *recipient
	for i, r := range m.Recipients {
		h := strings.TrimSpace(r.Handle)
		switch r.Role {
		case "from":
			if from == nil {
				from = &m.Recipients[i]
			}
		case "to":
			if strings.Contains(h, "@") {
				out.To = append(out.To, h)
			}
		case "cc":
			if strings.Contains(h, "@") {
				out.Cc = append(out.Cc, h)
			}
		}
	}
	if out.Agent && m.Author != nil && m.Author.ID != "" {
		out.Author = source.Person{ID: m.Author.ID, Email: strings.TrimSpace(m.Author.Email), Name: m.Author.name()}
		if m.Author.person() {
			out.AuthorID = m.Author.ID
		}
	} else if from != nil {
		out.Author = from.person()
	}
	out.Attachments = attachments(m.Attachments)
	return out
}

func toNote(c comment) source.Message {
	out := source.Message{ID: c.ID, Agent: true, Note: true, Text: c.Body, CreatedAt: at(c.PostedAt)}
	if c.Author != nil && c.Author.ID != "" {
		out.Author = source.Person{ID: c.Author.ID, Email: strings.TrimSpace(c.Author.Email), Name: c.Author.name()}
		if c.Author.person() {
			out.AuthorID = c.Author.ID
		}
	}
	out.Attachments = attachments(c.Attachments)
	return out
}

func attachments(in []attachment) []source.Attachment {
	var out []source.Attachment
	for _, a := range in {
		if strings.TrimSpace(a.URL) == "" {
			continue
		}
		out = append(out, source.Attachment{
			ID:          a.ID,
			Name:        strings.TrimSpace(a.Filename),
			ContentType: strings.TrimSpace(a.ContentType),
			Size:        max(a.Size, 0),
			URL:         strings.TrimSpace(a.URL),
			Inline:      a.Metadata.IsInline,
		})
	}
	return out
}

// Articles: Front's knowledge bases are not imported.
func (s *front) Articles(ctx context.Context, cursor string) (source.Page[source.Article], error) {
	return source.Page[source.Article]{}, source.ErrUnsupported
}

// Download opens a file. Front links point at the company's API host and need the token;
// a redirect elsewhere (file storage) gets none.
func (s *front) Download(ctx context.Context, a source.Attachment) (io.ReadCloser, error) {
	if a.URL == "" {
		return nil, errors.New("the attachment has no link")
	}
	c, err := s.clientFor(a.URL)
	if err != nil {
		return nil, err
	}
	return c.Open(ctx, a.URL)
}

// moved says Front answered with a redirect, which it does for a conversation merged
// into another one.
func moved(status int) bool { return status >= 300 && status < 400 }

// pacer spaces requests to Front's hosts minGap apart, and holds them back until the
// window resets when Front says none are left. One pacer serves every client of a source.
type pacer struct {
	mu   sync.Mutex
	next time.Time
}

type pacedTransport struct {
	p    *pacer
	next http.RoundTripper
}

func paced(hc *http.Client, p *pacer) *http.Client {
	cp := *hc
	next := cp.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	cp.Transport = pacedTransport{p: p, next: next}
	return &cp
}

func (t pacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.p.wait(r.Context()); err != nil {
		return nil, err
	}
	resp, err := t.next.RoundTrip(r)
	if err == nil {
		t.p.observe(resp.Header)
	}
	return resp, err
}

// wait takes the next slot.
func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	now := time.Now()
	slot := p.next
	if slot.Before(now) {
		slot = now
	}
	p.next = slot.Add(minGap)
	p.mu.Unlock()
	d := time.Until(slot)
	if d <= 0 {
		return nil
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

// observe reads x-ratelimit-remaining and x-ratelimit-reset (a Unix time): with nothing
// left, the next request waits for the reset.
func (p *pacer) observe(h http.Header) {
	left, err := strconv.Atoi(strings.TrimSpace(h.Get("X-Ratelimit-Remaining")))
	if err != nil || left > 0 {
		return
	}
	reset, err := strconv.ParseFloat(strings.TrimSpace(h.Get("X-Ratelimit-Reset")), 64)
	if err != nil || reset <= 0 {
		return
	}
	until := at(reset)
	if limit := time.Now().Add(maxResetWait); until.After(limit) {
		until = limit
	}
	p.mu.Lock()
	if until.After(p.next) {
		p.next = until
	}
	p.mu.Unlock()
}
