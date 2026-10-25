package front

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// All fixtures are made up, shaped like the examples in Front's API reference.

// fake serves the Front Core API.
type fake struct {
	mu           sync.Mutex
	url          string
	queries      map[string]map[string][]string // path -> last query
	limited      bool
	meMissing    bool
	eventsDenied bool
	offHostNext  bool
	// hits counts requests by path.
	hits map[string]int
	// merged makes cnv_5 answer as a conversation merged into another (301).
	merged bool
	// rawMissing makes msg_1's raw email answer 404; rawLimited makes it answer 429.
	rawMissing bool
	rawLimited bool
}

const (
	mar1        = 1709287200 // 2024-03-01T10:00:00Z
	day         = 86400
	fixtureCred = "fixture-cred"
	badCred     = "wrong-cred-value"
)

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.limited {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+fixtureCred {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"_error":{"status":401,"title":"Unauthenticated"}}`)
		return
	}
	if f.merged && strings.HasPrefix(r.URL.Path, "/conversations/cnv_5") {
		w.Header().Set("Location", f.url+"/conversations/cnv_1")
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	if f.queries == nil {
		f.queries = map[string]map[string][]string{}
	}
	f.queries[r.URL.Path] = r.URL.Query()
	if f.hits == nil {
		f.hits = map[string]int{}
	}
	f.hits[r.URL.Path]++
	q := r.URL.Query()
	next := func(path string) string { return fmt.Sprintf("%q", f.url+path) }
	page := func(results string, nextLink string) string {
		if nextLink == "" {
			nextLink = "null"
		}
		return fmt.Sprintf(`{"_pagination":{"next":%s},"_links":{"self":"%s"},"_results":[%s]}`, nextLink, f.url+r.URL.Path, results)
	}
	switch r.URL.Path {
	case "/me":
		if f.meMissing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"_links":{"self":"https://acme.api.frontapp.com/me"},"id":"cmp_k30","name":"Acme Co"}`)
	case "/inboxes":
		if q.Get("page_token") == "p2" {
			_, _ = io.WriteString(w, page(`{"id":"inb_a","name":" Support ","is_private":false}`, ""))
			return
		}
		_, _ = io.WriteString(w, page(`{"id":"inb_b","name":"Billing","is_private":false}`, next("/inboxes?page_token=p2")))
	case "/teammates":
		_, _ = io.WriteString(w, page(`{"id":"tea_1","email":"leela@acme.example","username":"leela","first_name":"Leela","last_name":"Turanga","type":"user"},
			{"id":"tea_2","email":"fry@acme.example","username":"fry","first_name":"","last_name":"","is_blocked":true},
			{"id":"tea_bot","username":"rule","type":"rule"}`, ""))
	case "/conversations/search/is:open":
		_, _ = io.WriteString(w, `{"_pagination":{"next":null},"_total":3,"_results":[]}`)
	case "/conversations/search/is:archived":
		_, _ = io.WriteString(w, `{"_pagination":{"next":null},"_total":4,"_results":[]}`)
	case "/inboxes/inb_a/conversations":
		if q.Get("page_token") == "c2" {
			_, _ = io.WriteString(w, page(conv("cnv_3", "deleted", "", mar1, mar1, "")+","+conv("cnv_4", "assigned", "open", mar1, mar1, `"type":"discussion",`), ""))
			return
		}
		nl := next("/inboxes/inb_a/conversations?page_token=c2")
		if f.offHostNext {
			nl = `"https://evil.example.com/inboxes/inb_a/conversations?page_token=c2"`
		}
		_, _ = io.WriteString(w, page(
			conv("cnv_1", "assigned", "open", mar1, mar1+3600, `"ticket_ids":[],`)+","+
				conv("cnv_2", "archived", "resolved", mar1-30*day, mar1-29*day, `"scheduled_reminders":[{"scheduled_at":1709900000}],`), nl))
	case "/inboxes/inb_b/conversations":
		_, _ = io.WriteString(w, page(conv("cnv_5", "unassigned", "waiting", mar1+day, mar1+2*day, `"ticket_ids":["TICKET-17"],`)+","+
			conv("cnv_6", "archived", "resolved", mar1-60*day, mar1-60*day, ""), ""))
	case "/conversations/cnv_5":
		_, _ = io.WriteString(w, conv("cnv_5", "unassigned", "waiting", mar1+day, mar1+2*day, `"ticket_ids":["TICKET-17"],`))
	case "/conversations/cnv_1/messages":
		if q.Get("page_token") == "m2" {
			_, _ = io.WriteString(w, page(fmt.Sprintf(`{"id":"msg_2","type":"email","is_inbound":false,"is_draft":false,"created_at":%d.5,
			  "author":{"id":"tea_1","email":"leela@acme.example","first_name":"Leela","last_name":"Turanga","type":"user"},
			  "recipients":[{"handle":"help@acme.example","role":"from","name":"Acme"},{"handle":"bob@example.com","role":"to","name":"Bob"},{"handle":"boss@example.com","role":"cc"},{"handle":"hidden@example.com","role":"bcc"}],
			  "body":"<p>Fixed</p><img src=\"/us-west-1/api/1/companies/acme/attachments/abc?resource_link_id=99\" front-cid=\"img-1@front\" alt=\"shot\">",
			  "text":"Fixed","attachments":[{"id":"fil_2r","filename":"shot.png","content_type":"image/png","size":321,"url":"%s/messages/msg_2/download/fil_2r","metadata":{"is_inline":true,"cid":"img-1@front"}}]},
			 {"id":"msg_3","type":"email","is_inbound":false,"is_draft":true,"created_at":%d,"author":{"id":"tea_1","type":"user"},"recipients":[],"body":"<p>unsent</p>","text":"unsent","attachments":[]}`,
				mar1+600, f.url, mar1+700), ""))
			return
		}
		_, _ = io.WriteString(w, page(fmt.Sprintf(`{"id":"msg_1","type":"email","is_inbound":true,"is_draft":false,"created_at":%d,"author":null,
		  "recipients":[{"handle":"bob@example.com","role":"from","name":"Bob Example","_links":{"related":{"contact":"https://acme.api.frontapp.com/contacts/crd_9"}}},{"handle":"help@acme.example","role":"to"}],
		  "body":"<p>It broke</p>","text":"It broke","attachments":[]}`, mar1), next("/conversations/cnv_1/messages?page_token=m2")))
	case "/conversations/cnv_1/comments":
		_, _ = io.WriteString(w, page(fmt.Sprintf(`{"id":"com_1","author":{"id":"tea_1","email":"leela@acme.example","first_name":"Leela","last_name":"Turanga","type":"user"},"body":"Checked the logs","posted_at":%d,"is_pinned":false,"attachments":[]},
			{"id":"com_2","author":{"id":"tea_bot","username":"rule","type":"rule"},"body":"Auto note","posted_at":%d,"attachments":[]}`, mar1+300, mar1+300), ""))
	case "/conversations/cnv_1/inboxes", "/conversations/cnv_5/inboxes":
		_, _ = io.WriteString(w, page(`{"id":"inb_x","name":"Elsewhere"},{"id":"inb_b","name":"Billing"}`, ""))
	case "/conversations/cnv_5/messages":
		_, _ = io.WriteString(w, page(fmt.Sprintf(`{"id":"msg_9","type":"front_chat","is_inbound":true,"created_at":%d,"recipients":[{"handle":"+15550100","role":"from"}],"body":"hi","text":"hi","attachments":[]}`, mar1+day), ""))
	case "/conversations/cnv_5/comments":
		_, _ = io.WriteString(w, page("", ""))
	case "/events":
		if f.eventsDenied {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if q.Get("page_token") == "e2" {
			_, _ = io.WriteString(w, page("", ""))
			return
		}
		_, _ = io.WriteString(w, page(fmt.Sprintf(`{"id":"evt_1","type":"inbound","emitted_at":%d.25,"conversation":%s},
			{"id":"evt_2","type":"tag","emitted_at":%d,"conversation":{"_links":{"self":"%s/conversations/cnv_5"},"id":"cnv_5"}},
			{"id":"evt_3","type":"comment","emitted_at":%d,"conversation":%s},
			{"id":"evt_4","type":"sending_error","emitted_at":%d}`,
			mar1+2*day, conv("cnv_1", "assigned", "open", mar1, mar1+3600, ""), mar1+2*day, f.url, mar1+3*day,
			conv("cnv_1", "assigned", "open", mar1, mar1+3600, ""), mar1+3*day), next("/events?page_token=e2")))
	case "/messages/msg_1", "/messages/msg_2":
		if r.Header.Get("Accept") != "message/rfc822" {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		if r.URL.Path == "/messages/msg_1" && f.rawLimited {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if r.URL.Path == "/messages/msg_1" && f.rawMissing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "message/rfc822")
		if r.URL.Path == "/messages/msg_1" {
			_, _ = io.WriteString(w, "From: Bob Example <bob@example.com>\r\nTo: help@acme.example\r\n"+
				"Message-ID: <a1.bob@mail.example>\r\nSubject: It broke\r\n\r\nIt broke\r\n")
			return
		}
		_, _ = io.WriteString(w, "From: Acme <help@acme.example>\r\nMessage-Id: <r2.leela@acme.example>\r\n"+
			"In-Reply-To: <a1.bob@mail.example>\r\nReferences: <a0.bob@mail.example>\r\n <a1.bob@mail.example>\r\n"+
			"Content-Type: text/plain\r\n\r\nFixed\r\n")
	case "/messages/msg_2/download/fil_2r":
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "png bytes")
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func conv(id, status, category string, created, updated int, extra string) string {
	return fmt.Sprintf(`{"_links":{"self":"https://acme.api.frontapp.com/conversations/%s"},"id":%q,"subject":"Subject %s",%s
	 "status":%q,"status_category":%q,"created_at":%d,"updated_at":%d,"is_private":false,
	 "assignee":{"id":"tea_1","email":"leela@acme.example","first_name":"Leela","last_name":"Turanga","type":"user"},
	 "recipient":{"handle":"bob@example.com","role":"from","name":"Bob Example","_links":{"related":{"contact":"https://acme.api.frontapp.com/contacts/crd_9"}}},
	 "tags":[{"id":"tag_1","name":"vip"},{"id":"tag_2","name":" "}]}`,
		id, id, id, extra, status, category, created, updated)
}

func setup(t *testing.T) (*fake, *front) {
	t.Helper()
	old := minGap
	minGap = 0
	t.Cleanup(func() { minGap = old })
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	s, err := New(source.Credentials{"token": fixtureCred}, source.Options{Dev: true, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return f, s.(*front)
}

func (f *fake) query(path string) map[string][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queries[path]
}

func TestNewValidates(t *testing.T) {
	for _, v := range []string{"", "  ", "abc def", strings.Repeat("k", 5000)} {
		_, err := New(source.Credentials{"token": v}, source.Options{})
		var inv *source.InvalidError
		if !errors.As(err, &inv) || inv.Field != "token" {
			t.Errorf("New(%q) = %v, want invalid token", v, err)
		}
	}
}

func TestBadToken(t *testing.T) {
	f, _ := setup(t)
	s, err := New(source.Credentials{"token": badCred}, source.Options{Dev: true, BaseURL: f.url})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Check(context.Background())
	if !errors.Is(err, source.ErrAuth) {
		t.Fatalf("Check = %v, want ErrAuth", err)
	}
	if strings.Contains(err.Error(), badCred) {
		t.Fatalf("the error carries the token: %v", err)
	}
}

func TestCheck(t *testing.T) {
	_, s := setup(t)
	scan, err := s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scan.Account != (source.Account{Key: "cmp_k30", Name: "Acme Co", URL: "https://app.frontapp.com"}) {
		t.Errorf("account = %+v", scan.Account)
	}
	if scan.Tickets != 7 || scan.Articles != 0 || scan.HasArticles {
		t.Errorf("tickets %d, articles %d %v", scan.Tickets, scan.Articles, scan.HasArticles)
	}
	if fmt.Sprint(scan.Groups) != fmt.Sprint([]source.Group{{ID: "inb_a", Name: "Support"}, {ID: "inb_b", Name: "Billing"}}) {
		t.Errorf("groups = %+v", scan.Groups)
	}
	want := []source.Agent{{ID: "tea_1", Name: "Leela Turanga", Email: "leela@acme.example"}, {ID: "tea_2", Name: "fry", Email: "fry@acme.example"}}
	if fmt.Sprint(scan.Agents) != fmt.Sprint(want) {
		t.Errorf("agents = %+v", scan.Agents)
	}
	if _, err := s.Articles(context.Background(), ""); !errors.Is(err, source.ErrUnsupported) {
		t.Errorf("articles = %v", err)
	}
}

func TestCheckWithoutMe(t *testing.T) {
	f, s := setup(t)
	f.meMissing = true
	scan, err := s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scan.Account.Key != "inbox-inb_a" || scan.Account.Name != "Front" {
		t.Errorf("account = %+v", scan.Account)
	}
}

// readAll follows cursors to the end.
func readAll(t *testing.T, s *front, since, updatedSince *time.Time) ([]source.Ticket, []string) {
	t.Helper()
	var all []source.Ticket
	var cursors []string
	cursor := ""
	for range 20 {
		p, err := s.Tickets(context.Background(), cursor, since, updatedSince)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p.Items...)
		cursors = append(cursors, p.Next)
		if p.Next == "" {
			return all, cursors
		}
		cursor = p.Next
	}
	t.Fatal("cursors did not end")
	return nil, nil
}

func ids(ts []source.Ticket) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return fmt.Sprint(out)
}

func TestTickets(t *testing.T) {
	f, s := setup(t)
	p, err := s.Tickets(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q := f.query("/inboxes/inb_a/conversations"); fmt.Sprint(q["q[statuses][]"], q["limit"]) != "[assigned unassigned archived] [100]" {
		t.Errorf("inbox query = %v", q)
	}
	if !strings.Contains(p.Next, `"m":"list"`) || !strings.Contains(p.Next, "page_token=c2") {
		t.Errorf("next = %s", p.Next)
	}

	since := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	all, cursors := readAll(t, s, &since, nil)
	if len(cursors) != 3 || !strings.Contains(cursors[1], `"i":1`) {
		t.Errorf("cursors = %v", cursors)
	}
	// cnv_2 and cnv_6 were created before since.
	if ids(all) != "[cnv_1 cnv_3 cnv_4 cnv_5]" {
		t.Fatalf("ids = %v", ids(all))
	}
	tk := all[0]
	if tk.Ref != "cnv_1" || tk.URL != "https://app.frontapp.com/open/cnv_1" || tk.Subject != "Subject cnv_1" || tk.Status != "open" ||
		tk.Skip || tk.Priority != "normal" || tk.Channel != "email" || tk.GroupID != "inb_a" || tk.AssigneeID != "tea_1" || tk.Loaded ||
		tk.SolvedAt != nil || fmt.Sprint(tk.Tags) != "[vip]" {
		t.Errorf("ticket = %+v", tk)
	}
	if !tk.CreatedAt.Equal(time.Unix(mar1, 0)) || !tk.UpdatedAt.Equal(time.Unix(mar1+3600, 0)) {
		t.Errorf("times = %v %v", tk.CreatedAt, tk.UpdatedAt)
	}
	if tk.Requester == nil || *tk.Requester != (source.Person{ID: "crd_9", Email: "bob@example.com", Name: "Bob Example"}) {
		t.Errorf("requester = %+v", tk.Requester)
	}
	if !all[1].Skip || !all[2].Skip {
		t.Errorf("deleted and discussion not skipped: %+v %+v", all[1], all[2])
	}
	if w := all[3]; w.Status != "pending" || w.Ref != "TICKET-17" || w.GroupID != "inb_b" {
		t.Errorf("waiting = %+v", w)
	}

	all, _ = readAll(t, s, nil, nil)
	if ids(all) != "[cnv_1 cnv_2 cnv_3 cnv_4 cnv_5 cnv_6]" {
		t.Fatalf("without since = %v", ids(all))
	}
	if snoozed := all[1]; snoozed.Status != "on_hold" || snoozed.SolvedAt != nil {
		t.Errorf("snoozed = %+v", snoozed)
	}
	if closed := all[5]; closed.Status != "closed" || closed.SolvedAt == nil || !closed.SolvedAt.Equal(closed.UpdatedAt) {
		t.Errorf("archived = %+v", closed)
	}

	for _, bad := range []string{"x", `{"m":"nope"}`, `{"m":"list","i":-1}`, `{"m":"events"}`} {
		if _, err := s.Tickets(context.Background(), bad, nil, nil); err == nil {
			t.Errorf("cursor %q accepted", bad)
		}
	}
	// A cursor past the last inbox (inboxes removed since) ends the listing.
	if p, err := s.Tickets(context.Background(), `{"m":"list","i":9}`, nil, nil); err != nil || p.Next != "" || len(p.Items) != 0 {
		t.Errorf("past the end = %+v %v", p, err)
	}
}

func TestOffHostNextLink(t *testing.T) {
	f, s := setup(t)
	f.offHostNext = true
	p, err := s.Tickets(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tickets(context.Background(), p.Next, nil, nil); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("followed an off-host link: %v", err)
	}
}

func TestClientFor(t *testing.T) {
	_, s := setup(t)
	for raw, ok := range map[string]bool{
		"/inboxes":                                   true,
		"https://api2.frontapp.com/inboxes":          true,
		"https://acme.api.frontapp.com/inboxes":      true,
		"http://acme.api.frontapp.com/inboxes":       false,
		"https://acme.api.frontapp.com.evil.com/x":   false,
		"https://a.b.api.frontapp.com/x":             false,
		"https://api.frontapp.com.example.org/x":     false,
		"https://ext.frontusercontent.com/v1/x":      false,
		"https://acme.api.frontapp.com:8443/inboxes": false,
	} {
		c, err := s.clientFor(raw)
		if (err == nil) != ok {
			t.Errorf("clientFor(%q) = %v, want ok %v", raw, err, ok)
		}
		if ok && strings.Contains(raw, "acme.api") && c.Base != "https://acme.api.frontapp.com" {
			t.Errorf("clientFor(%q) base = %s", raw, c.Base)
		}
	}
	a, _ := s.clientFor("https://acme.api.frontapp.com/a")
	b, _ := s.clientFor("https://ACME.api.frontapp.com/b")
	if a != b || a.Auth == nil {
		t.Error("one company host got two clients, or none with the token")
	}
}

func TestLoad(t *testing.T) {
	_, s := setup(t)
	ctx := context.Background()
	p, err := s.Tickets(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tk := p.Items[0]
	if err := s.Load(ctx, &tk); err != nil {
		t.Fatal(err)
	}
	if !tk.Loaded || tk.GroupID != "inb_a" {
		t.Errorf("loaded = %v group %s", tk.Loaded, tk.GroupID)
	}
	var got []string
	for _, m := range tk.Messages {
		got = append(got, m.ID)
	}
	// Oldest first, the comments in between, the draft left out.
	if fmt.Sprint(got) != "[msg_1 com_1 com_2 msg_2]" {
		t.Fatalf("messages = %v", got)
	}
	in, note, bot, reply := tk.Messages[0], tk.Messages[1], tk.Messages[2], tk.Messages[3]
	if in.Agent || in.Note || in.AuthorID != "" || in.Author != (source.Person{ID: "crd_9", Email: "bob@example.com", Name: "Bob Example"}) ||
		in.HTML != "<p>It broke</p>" || in.Text != "It broke" {
		t.Errorf("inbound = %+v", in)
	}
	if !note.Agent || !note.Note || note.AuthorID != "tea_1" || note.Text != "Checked the logs" || note.HTML != "" {
		t.Errorf("note = %+v", note)
	}
	if !bot.Note || bot.AuthorID != "" {
		t.Errorf("rule note = %+v", bot)
	}
	if !reply.Agent || reply.Note || reply.AuthorID != "tea_1" || reply.Author.Name != "Leela Turanga" ||
		fmt.Sprint(reply.To, reply.Cc) != "[bob@example.com] [boss@example.com]" ||
		!reply.CreatedAt.Equal(time.Unix(mar1+600, 500_000_000)) {
		t.Errorf("reply = %+v", reply)
	}
	if len(reply.Attachments) != 1 {
		t.Fatalf("attachments = %+v", reply.Attachments)
	}
	att := reply.Attachments[0]
	if att.ID != "fil_2r" || att.Name != "shot.png" || att.ContentType != "image/png" || att.Size != 321 || !att.Inline ||
		!strings.HasSuffix(att.URL, "/messages/msg_2/download/fil_2r") {
		t.Errorf("attachment = %+v", att)
	}
	if want := `<p>Fixed</p><img src="` + att.URL + `" alt="shot">`; reply.HTML != want {
		t.Errorf("body = %s\nwant  %s", reply.HTML, want)
	}

	// A chat conversation a sync found has no inbox yet.
	chat := source.Ticket{ID: "cnv_5", Channel: "email"}
	if err := s.Load(ctx, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Channel != "chat" || fmt.Sprint(chat.Tags) != "[front-chat]" || chat.GroupID != "inb_b" ||
		chat.Messages[0].Author.Phone != "+15550100" || chat.Messages[0].Author.ID != "handle:+15550100" {
		t.Errorf("chat = %+v", chat)
	}
	if err := s.Load(ctx, &source.Ticket{ID: "cnv_1/../x"}); err == nil {
		t.Error("a malformed id was loaded")
	}
}

func TestSyncEvents(t *testing.T) {
	f, s := setup(t)
	upd := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	p, err := s.Tickets(context.Background(), "", nil, &upd)
	if err != nil {
		t.Fatal(err)
	}
	if q := f.query("/events"); fmt.Sprint(q["q[after]"], q["sort_by"], q["sort_order"], q["limit"]) != "[1709337600.000] [created_at] [asc] [100]" {
		t.Errorf("events query = %v", q)
	}
	if !strings.Contains(p.Next, `"m":"events"`) {
		t.Errorf("next = %s", p.Next)
	}
	all := p.Items
	if ids(all) != "[cnv_1 cnv_5]" {
		t.Fatalf("tickets = %v", ids(all))
	}
	// The last event on cnv_1 wins, and its time is the update.
	if !all[0].UpdatedAt.Equal(time.Unix(mar1+3*day, 0)) || all[0].GroupID != "" {
		t.Errorf("cnv_1 = %+v", all[0])
	}
	if all[1].Status != "pending" || all[1].Subject != "Subject cnv_5" {
		t.Errorf("preview conversation not fetched: %+v", all[1])
	}
	rest, _ := readAll(t, s, nil, &upd)
	if len(rest) != 2 {
		t.Errorf("all pages = %v", ids(rest))
	}
}

func TestSyncWalk(t *testing.T) {
	f, s := setup(t)
	f.eventsDenied = true
	upd := time.Date(2024, 3, 1, 10, 30, 0, 0, time.UTC)
	all, cursors := readAll(t, s, nil, &upd)
	// cnv_1 changed after upd; cnv_2 is a month older, so inbox a stops before its next page.
	if ids(all) != "[cnv_1 cnv_5]" {
		t.Fatalf("ids = %v", ids(all))
	}
	if !strings.Contains(cursors[0], `"m":"walk","i":1`) {
		t.Errorf("cursors = %v", cursors)
	}
	if _, err := s.Tickets(context.Background(), `{"m":"walk"}`, nil, nil); err == nil {
		t.Error("a walk without a sync point was accepted")
	}
}

func TestRateLimited(t *testing.T) {
	f, s := setup(t)
	f.limited = true
	_, err := s.Check(context.Background())
	var rl *source.RateLimited
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Fatalf("Check = %v, want rate limited for 7s", err)
	}
}

func TestPacer(t *testing.T) {
	old := minGap
	minGap = 40 * time.Millisecond
	defer func() { minGap = old }()
	p := &pacer{}
	ctx := context.Background()
	start := time.Now()
	for range 3 {
		if err := p.wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Errorf("three requests took %v, want at least 80ms", d)
	}

	minGap = 0
	p = &pacer{}
	reset := float64(time.Now().Add(300*time.Millisecond).UnixMilli()) / 1000
	p.observe(http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {fmt.Sprintf("%.3f", reset)}})
	start = time.Now()
	if err := p.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("waited %v for the reset, want about 300ms", d)
	}
	p.observe(http.Header{"X-Ratelimit-Remaining": {"12"}, "X-Ratelimit-Reset": {"9999999999"}})
	start = time.Now()
	_ = p.wait(ctx)
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("waited %v with requests left", d)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	p.next = time.Now().Add(time.Hour)
	if err := p.wait(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("wait after cancel = %v", err)
	}
}

func TestDownload(t *testing.T) {
	f, s := setup(t)
	ctx := context.Background()
	rc, err := s.Download(ctx, source.Attachment{URL: f.url + "/messages/msg_2/download/fil_2r"})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "png bytes" {
		t.Errorf("data = %q", b)
	}
	if _, err := s.Download(ctx, source.Attachment{URL: "https://files.example.com/fil_2r"}); err == nil {
		t.Error("downloaded off Front's hosts with the token")
	}
	if _, err := s.Download(ctx, source.Attachment{}); err == nil {
		t.Error("downloaded without a link")
	}
}

func TestStatuses(t *testing.T) {
	for _, c := range []struct {
		cv   conversation
		want string
		skip bool
	}{
		{conversation{Status: "assigned", StatusCategory: "open"}, "open", false},
		{conversation{Status: "unassigned"}, "open", false},
		{conversation{Status: "assigned", StatusCategory: "waiting"}, "pending", false},
		{conversation{Status: "assigned", StatusCategory: "resolved"}, "solved", false},
		{conversation{Status: "archived", StatusCategory: "resolved"}, "closed", false},
		{conversation{Status: "archived", StatusCategory: "waiting"}, "closed", false},
		{conversation{Status: "archived", ScheduledReminders: []json.RawMessage{[]byte(`{}`)}}, "on_hold", false},
		{conversation{Status: "snoozed"}, "on_hold", false},
		{conversation{Status: "deleted"}, "closed", true},
		{conversation{Status: "spam"}, "closed", true},
		{conversation{Status: "assigned", Type: "task"}, "open", true},
		{conversation{Status: "assigned", Type: "conversation"}, "open", false},
	} {
		got := ticket(c.cv, "")
		if got.Status != c.want || got.Skip != c.skip {
			t.Errorf("%+v = %s skip %v, want %s skip %v", c.cv, got.Status, got.Skip, c.want, c.skip)
		}
	}
}

func TestSyncSkipsMergedConversation(t *testing.T) {
	f, s := setup(t)
	f.merged = true
	upd := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	all, _ := readAll(t, s, nil, &upd)
	if ids(all) != "[cnv_1]" {
		t.Errorf("tickets = %v, want the merged cnv_5 left out", ids(all))
	}
}

func TestLoadSkipsMergedConversation(t *testing.T) {
	f, s := setup(t)
	f.merged = true
	tk := source.Ticket{ID: "cnv_5", Ref: "cnv_5"}
	if err := s.Load(context.Background(), &tk); err != nil {
		t.Fatalf("load merged: %v", err)
	}
	if !tk.Skip || len(tk.Messages) != 0 {
		t.Errorf("merged ticket = skip %v, %d messages", tk.Skip, len(tk.Messages))
	}
}
