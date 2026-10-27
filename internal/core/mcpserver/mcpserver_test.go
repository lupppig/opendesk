package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/idempotency"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

const appOrigin = "http://app.test"

type env struct {
	*hdtest.Env
	WS hdtest.WS
}

// newEnv serves the API and /mcp behind the production middleware, on the test
// database (TEST_DATABASE_URL).
func newEnv(t *testing.T) *env {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("set TEST_DATABASE_URL to run database tests")
	}
	base := hdtest.Deps(t)
	d := *base
	cfg := *base.Cfg
	cfg.CORSOrigins = []string{appOrigin}
	d.Cfg = &cfg
	// Rate limits per test, not per process.
	d.Limiter = httpx.NewLimiter()

	httpx.Install(d.Log)
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Logger(d.Log), httpx.SecurityHeaders(false), httpx.CORS(cfg.CORSOrigins))
	resolver := &auth.Resolver{Pool: d.DB.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}
	r.Use(resolver.Middleware, idempotency.New(d.DB.Pool, d.Box, cfg.AuthSecret, d.Log).Middleware)
	api := humachi.New(r, huma.DefaultConfig("opendesk test", "test"))
	core.Register(&d, api, r)
	helpdesk.Register(&d, api, r)
	Register(&d, api, r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	e := &env{Env: &hdtest.Env{T: t, D: &d, Srv: srv, Ctx: context.Background()}}
	e.WS = e.NewWorkspace()
	return e
}

func (e *env) ticket(subject, text string) domain.Ticket {
	e.T.Helper()
	var tk domain.Ticket
	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		cid, _, err := domain.UpsertContactByEmail(e.Ctx, tx, e.WS.ID, "jane-"+ids.New().String()[:8]+"@example.com", "Jane Doe")
		if err != nil {
			return err
		}
		tk, err = domain.CreateTicket(e.Ctx, tx, domain.NewTicket{WorkspaceID: e.WS.ID, Subject: subject, Channel: domain.ChannelEmail, ContactID: &cid})
		if err != nil {
			return err
		}
		_, err = domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: e.WS.ID, TicketID: tk.ID, Kind: "reply",
			Direction: "inbound", AuthorType: "contact", AuthorContactID: &cid, BodyHTML: "<p>" + text + "</p>", BodyText: text,
			Trigger: jobs.TriggerTicketCreated})
		return err
	})
	if err != nil {
		e.T.Fatal(err)
	}
	return tk
}

// headers adds fixed headers to every request of an MCP client.
type headers struct {
	h    map[string]string
	next http.RoundTripper
}

func (t headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.h {
		r.Header.Set(k, v)
	}
	return t.next.RoundTrip(r)
}

// connect opens an SDK client session against /mcp with key (and Origin, when set).
func (e *env) connect(key, origin string) (*mcp.ClientSession, error) {
	h := map[string]string{}
	if key != "" {
		h["Authorization"] = "Bearer " + key
	}
	if origin != "" {
		h["Origin"] = origin
	}
	tr := &mcp.StreamableClientTransport{Endpoint: e.Srv.URL + Path, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: headers{h: h, next: http.DefaultTransport}}}
	c := mcp.NewClient(&mcp.Implementation{Name: "opendesk-test", Version: "1"}, nil)
	return c.Connect(e.Ctx, tr, nil)
}

func (e *env) session(key string) *mcp.ClientSession {
	e.T.Helper()
	s, err := e.connect(key, "")
	if err != nil {
		e.T.Fatalf("connect: %v", err)
	}
	e.T.Cleanup(func() { _ = s.Close() })
	return s
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	return names
}

// call runs a tool and decodes its JSON text into out (when non-nil).
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if out != nil {
		if res.IsError {
			t.Fatalf("%s failed: %s", name, text(res))
		}
		if err := json.Unmarshal([]byte(text(res)), out); err != nil {
			t.Fatalf("%s output %q: %v", name, text(res), err)
		}
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func (e *env) post(hdr map[string]string) *http.Response {
	e.T.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req, _ := http.NewRequest(http.MethodPost, e.Srv.URL+Path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	res := e.post(nil)
	if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("no key: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("security headers missing")
	}
	res = e.post(map[string]string{"Authorization": "Bearer od_live_not-a-real-key"})
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Header.Get("WWW-Authenticate"), "invalid_token") {
		t.Fatalf("bad key: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	// A signed-in user's access token is not an API key.
	res = e.post(map[string]string{"Authorization": "Bearer " + e.WS.Owner.Token, auth.WorkspaceHeader: e.WS.ID.String()})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("user token: %d", res.StatusCode)
	}
	// The SDK client sees the refusal.
	if _, err := e.connect("", ""); err == nil {
		t.Fatal("connected without a key")
	}
	// A revoked key stops working.
	key := e.APIKey(e.WS, auth.ScopeTicketsRead)
	e.session(key)
	e.Exec(`UPDATE api_keys SET revoked_at = now() WHERE workspace_id = $1`, e.WS.ID)
	if res := e.post(map[string]string{"Authorization": "Bearer " + key}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", res.StatusCode)
	}
}

func TestOrigin(t *testing.T) {
	e := newEnv(t)
	key := e.APIKey(e.WS, auth.ScopeTicketsRead)
	if res := e.post(map[string]string{"Authorization": "Bearer " + key, "Origin": "https://evil.example"}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.StatusCode)
	}
	if _, err := e.connect(key, "https://evil.example"); err == nil {
		t.Fatal("connected from a foreign origin")
	}
	s, err := e.connect(key, appOrigin)
	if err != nil {
		t.Fatalf("allowed origin: %v", err)
	}
	defer s.Close()
	if n := toolNames(t, s); len(n) != 2 {
		t.Fatalf("tools: %v", n)
	}
}

func TestToolsFollowScopes(t *testing.T) {
	e := newEnv(t)
	got := toolNames(t, e.session(e.APIKey(e.WS, auth.ScopeTicketsRead)))
	if !slices.Equal(got, []string{"get_ticket", "search_tickets"}) {
		t.Fatalf("tickets:read tools: %v", got)
	}
	got = toolNames(t, e.session(e.APIKey(e.WS, auth.ScopeKBRead, auth.ScopeContactsRead)))
	if !slices.Equal(got, []string{"get_contact", "search_contacts", "search_knowledge"}) {
		t.Fatalf("kb and contacts tools: %v", got)
	}
	got = toolNames(t, e.session(e.APIKey(e.WS, auth.ScopeWikiRead)))
	if !slices.Equal(got, []string{"get_wiki_page", "search_wiki"}) {
		t.Fatalf("wiki:read tools: %v", got)
	}
	// Knowledge base scopes don't reach the wiki.
	if got = toolNames(t, e.session(e.APIKey(e.WS, auth.ScopeKBRead, auth.ScopeKBWrite))); slices.Contains(got, "search_wiki") {
		t.Fatalf("kb tools: %v", got)
	}
	// get_report reads tickets through the overview route: settings:read alone is not enough.
	if got = toolNames(t, e.session(e.APIKey(e.WS, auth.ScopeSettingsRead))); len(got) != 0 {
		t.Fatalf("settings:read tools: %v", got)
	}

	s := e.session(e.APIKey(e.WS, auth.AllScopes...))
	res, err := s.ListTools(e.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"search_tickets", "get_ticket", "reply_to_ticket", "draft_reply", "add_note", "update_ticket", "create_ticket",
		"search_contacts", "get_contact", "search_knowledge", "create_kb_draft",
		"search_wiki", "get_wiki_page", "create_wiki_page", "update_wiki_page", "get_report",
		"list_collab_conversations", "get_collab_conversation", "draft_collab_reply"}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" || tl.InputSchema == nil || tl.Annotations == nil {
			t.Fatalf("%s lacks a description, schema or annotations", tl.Name)
		}
		read := strings.HasPrefix(tl.Name, "search_") || strings.HasPrefix(tl.Name, "get_") || strings.HasPrefix(tl.Name, "list_")
		if tl.Annotations.ReadOnlyHint != read {
			t.Fatalf("%s readOnlyHint = %v", tl.Name, tl.Annotations.ReadOnlyHint)
		}
		if tl.Name == "update_ticket" && (tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Fatal("update_ticket should be marked destructive")
		}
		if tl.Name == "update_wiki_page" && (tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Fatal("update_wiki_page should be marked destructive")
		}
		if tl.Name == "add_note" && (tl.Annotations.DestructiveHint == nil || *tl.Annotations.DestructiveHint) {
			t.Fatal("add_note should not be marked destructive")
		}
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("all tools: %v", names)
	}
}

func TestReadTools(t *testing.T) {
	e := newEnv(t)
	tk := e.ticket("Refund for order 1042", "I was charged twice for order 1042.")
	other := e.ticket("Password reset", "I cannot sign in.")
	s := e.session(e.APIKey(e.WS, auth.ScopeTicketsRead))

	var list struct {
		Tickets []struct {
			ID      string `json:"id"`
			Number  int64  `json:"number"`
			Subject string `json:"subject"`
			Contact struct {
				Email string `json:"email"`
			} `json:"contact"`
		} `json:"tickets"`
		Count int `json:"count"`
	}
	call(t, s, "search_tickets", map[string]any{"query": "charged twice"}, &list)
	if list.Count != 1 || list.Tickets[0].ID != tk.ID.String() || list.Tickets[0].Contact.Email == "" {
		t.Fatalf("search: %+v", list)
	}
	call(t, s, "search_tickets", map[string]any{"status": []string{"open"}, "created_after": "2000-01-01", "limit": 50}, &list)
	if list.Count != 2 {
		t.Fatalf("date search: %+v", list)
	}

	var th struct {
		Ticket struct {
			Number int64  `json:"number"`
			Status string `json:"status"`
		} `json:"ticket"`
		Messages []struct {
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Author struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"author"`
		} `json:"messages"`
	}
	call(t, s, "get_ticket", map[string]any{"ticket": fmt.Sprintf("#%d", tk.Number)}, &th)
	if th.Ticket.Number != tk.Number || len(th.Messages) != 1 || th.Messages[0].Text != "I was charged twice for order 1042." ||
		th.Messages[0].Author.Type != "contact" || th.Messages[0].Author.Name != "Jane Doe" {
		t.Fatalf("get_ticket: %+v", th)
	}
	if strings.Contains(text(call(t, s, "get_ticket", map[string]any{"ticket": other.ID.String()}, nil)), "<p>") {
		t.Fatal("thread carries HTML")
	}
	// A deleted message and a note sent as a reply are left out entirely, never an empty entry.
	e.Exec(`INSERT INTO messages (workspace_id, ticket_id, kind, direction, author_type, body_html, body_text, deleted_at)
		VALUES ($1, $2, 'note', 'outbound', 'user', '<p>Deleted secret</p>', 'Deleted secret', now())`, e.WS.ID, tk.ID)
	res0 := call(t, s, "get_ticket", map[string]any{"ticket": fmt.Sprintf("#%d", tk.Number)}, &th)
	if len(th.Messages) != 1 || strings.Contains(text(res0), "Deleted secret") {
		t.Fatalf("get_ticket with a deleted message: %+v", th)
	}

	// Bad arguments and other workspaces' tickets are tool errors, not protocol errors.
	if res := call(t, s, "get_ticket", map[string]any{"ticket": "DROP TABLE"}, nil); !res.IsError {
		t.Fatal("bad ticket reference accepted")
	}
	if res := call(t, s, "search_tickets", map[string]any{"bogus": true}, nil); !res.IsError {
		t.Fatal("unknown argument accepted")
	}
	foreign := e.NewWorkspace()
	fs := e.session(e.APIKey(foreign, auth.ScopeTicketsRead))
	res := call(t, fs, "get_ticket", map[string]any{"ticket": tk.ID.String()}, nil)
	if !res.IsError || !strings.Contains(text(res), "404") {
		t.Fatalf("foreign ticket: %s", text(res))
	}
}

func TestWritesRunAsTheKey(t *testing.T) {
	e := newEnv(t)
	tk := e.ticket("Broken invoice", "The invoice PDF is empty.")
	s := e.session(e.APIKey(e.WS, auth.ScopeTicketsRead, auth.ScopeTicketsWrite))
	_, seq := e.Events(e.WS.ID, 0)

	var note struct {
		MessageID string `json:"message_id"`
		Kind      string `json:"kind"`
	}
	call(t, s, "add_note", map[string]any{"ticket": fmt.Sprintf("#%d", tk.Number), "body": "Checked the **billing** logs:\n\n- PDF job failed"}, &note)
	if note.MessageID == "" || note.Kind != "note" {
		t.Fatalf("add_note: %+v", note)
	}
	var authorType, kind, html string
	e.Scalar(`SELECT author_type FROM messages WHERE id = $1 AND workspace_id = $2`, &authorType, note.MessageID, e.WS.ID)
	e.Scalar(`SELECT kind FROM messages WHERE id = $1`, &kind, note.MessageID)
	e.Scalar(`SELECT body_html FROM messages WHERE id = $1`, &html, note.MessageID)
	if authorType != "api" || kind != "note" || !strings.Contains(html, "<strong>billing</strong>") || !strings.Contains(html, "<li>") {
		t.Fatalf("note stored as %s %s %q", authorType, kind, html)
	}
	types, seq := e.Events(e.WS.ID, seq)
	if !slices.Contains(types, "message.created") {
		t.Fatalf("events after add_note: %v", types)
	}
	// The write carried an Idempotency-Key, claimed for the key.
	var claimed int
	e.Scalar(`SELECT count(*) FROM idempotency_keys k JOIN api_keys a ON a.id = k.api_key_id
		WHERE k.workspace_id = $1 AND k.key LIKE 'mcp-%' AND k.path = $2`, &claimed, e.WS.ID, fmt.Sprintf("/v1/tickets/#%d/messages", tk.Number))
	if claimed != 1 {
		t.Fatalf("idempotency keys: %d", claimed)
	}

	// A drafted reply is staged on the ticket for the team, as HTML, and nothing is sent.
	var draft struct {
		TicketID string `json:"ticket_id"`
		By       string `json:"by"`
	}
	call(t, s, "draft_reply", map[string]any{"ticket": fmt.Sprintf("#%d", tk.Number), "body": "Hi, we are **on it**."}, &draft)
	var staged, replies int
	e.Scalar(`SELECT count(*) FROM ticket_drafts WHERE ticket_id = $1 AND body_html LIKE '%<strong>on it</strong>%' AND api_key_id IS NOT NULL`, &staged, tk.ID)
	e.Scalar(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND kind = 'reply' AND direction = 'outbound'`, &replies, tk.ID)
	if draft.TicketID != tk.ID.String() || draft.By == "" || staged != 1 || replies != 0 {
		t.Fatalf("draft_reply: %+v staged=%d replies=%d", draft, staged, replies)
	}

	var upd struct {
		Status   string   `json:"status"`
		Priority string   `json:"priority"`
		Labels   []string `json:"labels"`
	}
	call(t, s, "update_ticket", map[string]any{"ticket": tk.ID.String(), "status": "pending", "priority": "high", "add_labels": []string{"billing"}}, &upd)
	if upd.Status != "pending" || upd.Priority != "high" || !slices.Contains(upd.Labels, "billing") {
		t.Fatalf("update_ticket: %+v", upd)
	}
	var acts int
	e.Scalar(`SELECT count(*) FROM activities WHERE workspace_id = $1 AND ticket_id = $2 AND actor_type = 'api'`, &acts, e.WS.ID, tk.ID)
	if acts == 0 {
		t.Fatal("no activity attributed to the key")
	}
	if types, _ = e.Events(e.WS.ID, seq); !slices.Contains(types, "ticket.updated") || !slices.Contains(types, "activity.created") {
		t.Fatalf("events after update_ticket: %v", types)
	}

	var created struct {
		Number  int64 `json:"number"`
		Contact struct {
			Email string `json:"email"`
		} `json:"contact"`
		Channel string `json:"channel"`
	}
	call(t, s, "create_ticket", map[string]any{"email": "sam@example.com", "name": "Sam", "subject": "Follow up", "body": "Calling back tomorrow.", "kind": "note"}, &created)
	if created.Number == 0 || created.Contact.Email != "sam@example.com" || created.Channel != "api" {
		t.Fatalf("create_ticket: %+v", created)
	}
}

func TestScopeRefusals(t *testing.T) {
	e := newEnv(t)
	tk := e.ticket("Hello", "Hi there")
	ro := e.session(e.APIKey(e.WS, auth.ScopeTicketsRead))
	// A tool outside the key's scopes is not offered and cannot be called.
	res, err := ro.CallTool(e.Ctx, &mcp.CallToolParams{Name: "add_note", Arguments: map[string]any{"ticket": tk.ID.String(), "body": "x"}})
	if err == nil && !res.IsError {
		t.Fatal("add_note ran without tickets:write")
	}
	var n int
	e.Scalar(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND kind = 'note'`, &n, tk.ID)
	if n != 0 {
		t.Fatal("a note was written")
	}
	// The API's own checks apply inside a tool: members need settings:read.
	rw := e.session(e.APIKey(e.WS, auth.ScopeTicketsWrite))
	res = call(t, rw, "update_ticket", map[string]any{"ticket": tk.ID.String(), "assignee": e.WS.Agent.Email}, nil)
	if !res.IsError || !strings.Contains(text(res), "settings:read") {
		t.Fatalf("assignee by email without settings:read: %s", text(res))
	}
	// With settings:read the same call resolves the teammate.
	rws := e.session(e.APIKey(e.WS, auth.ScopeTicketsWrite, auth.ScopeSettingsRead))
	var upd struct {
		Assignee struct {
			ID string `json:"id"`
		} `json:"assignee"`
	}
	call(t, rws, "update_ticket", map[string]any{"ticket": tk.ID.String(), "assignee": e.WS.Agent.Email}, &upd)
	if upd.Assignee.ID != e.WS.Agent.ID.String() {
		t.Fatalf("assignee: %+v", upd)
	}
}

func TestKnowledgeAndContacts(t *testing.T) {
	e := newEnv(t)
	e.ticket("Shipping question", "Where is my parcel?")
	s := e.session(e.APIKey(e.WS, auth.ScopeKBRead, auth.ScopeKBWrite, auth.ScopeContactsRead))

	var draft struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	call(t, s, "create_kb_draft", map[string]any{"title": "Tracking a parcel", "body": "Open **Orders** and pick the parcel to see where it is."}, &draft)
	if draft.ID == "" || draft.Status != "draft" {
		t.Fatalf("create_kb_draft: %+v", draft)
	}
	var found struct {
		Articles []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"articles"`
	}
	call(t, s, "search_knowledge", map[string]any{"query": "parcel", "status": "any"}, &found)
	if len(found.Articles) != 1 || found.Articles[0].ID != draft.ID || !strings.Contains(found.Articles[0].Text, "Orders") {
		t.Fatalf("search_knowledge: %+v", found)
	}
	call(t, s, "search_knowledge", map[string]any{"query": "parcel"}, &found)
	if len(found.Articles) != 0 {
		t.Fatalf("drafts returned by default: %+v", found)
	}

	var cs struct {
		Contacts []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"contacts"`
	}
	call(t, s, "search_contacts", map[string]any{"query": "jane"}, &cs)
	if len(cs.Contacts) != 1 {
		t.Fatalf("search_contacts: %+v", cs)
	}
	var one struct {
		Contact struct {
			ID string `json:"id"`
		} `json:"contact"`
		Tickets []any `json:"recent_tickets"`
	}
	call(t, s, "get_contact", map[string]any{"contact": cs.Contacts[0].Email}, &one)
	if one.Contact.ID != cs.Contacts[0].ID || one.Tickets != nil {
		t.Fatalf("get_contact without tickets:read: %+v", one)
	}
}

func TestWikiTools(t *testing.T) {
	e := newEnv(t)
	s := e.session(e.APIKey(e.WS, auth.ScopeWikiRead, auth.ScopeWikiWrite))

	type out struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Path     string `json:"path"`
		Text     string `json:"text"`
		Version  int    `json:"version"`
		Subpages []struct {
			ID string `json:"id"`
		} `json:"subpages"`
	}
	var parent, child out
	call(t, s, "create_wiki_page", map[string]any{"title": "Policies"}, &parent)
	call(t, s, "create_wiki_page", map[string]any{"title": "Refund escalation", "parent": parent.ID,
		"body": "Refunds over **100 euros** go to the team lead."}, &child)
	if child.Path != "Policies" || child.Version != 1 {
		t.Fatalf("create_wiki_page: %+v", child)
	}
	var found struct {
		Pages []struct {
			ID      string `json:"id"`
			Path    string `json:"path"`
			Snippet string `json:"snippet"`
		} `json:"pages"`
	}
	call(t, s, "search_wiki", map[string]any{"query": "refund lead"}, &found)
	if len(found.Pages) != 1 || found.Pages[0].ID != child.ID || !strings.Contains(found.Pages[0].Snippet, "100 euros") {
		t.Fatalf("search_wiki: %+v", found)
	}
	var got out
	call(t, s, "get_wiki_page", map[string]any{"page": parent.ID}, &got)
	if len(got.Subpages) != 1 || got.Subpages[0].ID != child.ID {
		t.Fatalf("get_wiki_page: %+v", got)
	}
	call(t, s, "update_wiki_page", map[string]any{"page": child.ID, "body": "Refunds over 50 euros go to the lead.", "version": 1}, &got)
	if got.Version != 2 {
		t.Fatalf("update_wiki_page: %+v", got)
	}
	// A stale version is refused with the API's own answer.
	res := call(t, s, "update_wiki_page", map[string]any{"page": child.ID, "body": "Old", "version": 1}, nil)
	if !res.IsError || !strings.Contains(text(res), "page_changed") {
		t.Fatalf("stale update: %s", text(res))
	}
	call(t, s, "get_wiki_page", map[string]any{"page": child.ID}, &got)
	if !strings.Contains(got.Text, "50 euros") {
		t.Fatalf("after update: %+v", got)
	}

	// Read only: the write tools are not offered and do not run.
	ro := e.session(e.APIKey(e.WS, auth.ScopeWikiRead))
	res, err := ro.CallTool(e.Ctx, &mcp.CallToolParams{Name: "create_wiki_page", Arguments: map[string]any{"title": "Nope"}})
	if err == nil && !res.IsError {
		t.Fatal("create_wiki_page ran without wiki:write")
	}
	var n int
	e.Scalar(`SELECT count(*) FROM wiki_pages WHERE workspace_id = $1`, &n, e.WS.ID)
	if n != 2 {
		t.Fatalf("pages %d", n)
	}
	// Another workspace's key reads nothing of it.
	other := e.NewWorkspace()
	os := e.session(e.APIKey(other, auth.ScopeWikiRead))
	if res := call(t, os, "get_wiki_page", map[string]any{"page": child.ID}, nil); !res.IsError {
		t.Fatalf("other workspace read the page: %s", text(res))
	}
}
