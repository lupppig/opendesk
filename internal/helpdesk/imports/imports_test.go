package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// fake is a source answering from memory.
type fake struct {
	mu       sync.Mutex
	scan     source.Scan
	tickets  []source.Ticket
	loads    map[string][]source.Message // what Load fills in, by ticket id
	articles []source.Article
	files    map[string][]byte // attachment contents by URL
	pageSize int
	// errs are answered by Tickets first, one per call.
	errs []error
	// updatedSince is what each Tickets call was asked.
	updatedSince []*time.Time
	checkErr     error
}

func (f *fake) factory(c source.Credentials, _ source.Options) (source.Source, error) {
	switch strings.TrimSpace(c["token"]) {
	case "":
		return nil, source.Invalid("token", "Enter the API token.")
	case "other":
		return &fake{scan: source.Scan{Account: source.Account{Key: "other", Name: "Other"}, Tickets: 0}, pageSize: 10}, nil
	}
	return f, nil
}

func (f *fake) Check(context.Context) (source.Scan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scan, f.checkErr
}

func (f *fake) Tickets(_ context.Context, cursor string, since, updatedSince *time.Time) (source.Page[source.Ticket], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updatedSince = append(f.updatedSince, updatedSince)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return source.Page[source.Ticket]{}, err
	}
	var list []source.Ticket
	for _, t := range f.tickets {
		if since != nil && t.CreatedAt.Before(*since) {
			continue
		}
		if updatedSince != nil && t.UpdatedAt.Before(*updatedSince) {
			continue
		}
		c := t
		c.Messages, c.Tags = slices.Clone(t.Messages), slices.Clone(t.Tags)
		list = append(list, c)
	}
	start, _ := strconv.Atoi(cursor)
	start = min(start, len(list))
	end := min(start+f.pageSize, len(list))
	page := source.Page[source.Ticket]{Items: list[start:end]}
	if end < len(list) {
		page.Next = strconv.Itoa(end)
	}
	return page, nil
}

func (f *fake) Load(_ context.Context, t *source.Ticket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Messages, t.Loaded = slices.Clone(f.loads[t.ID]), true
	return nil
}

func (f *fake) Articles(_ context.Context, cursor string) (source.Page[source.Article], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.articles == nil {
		return source.Page[source.Article]{}, source.ErrUnsupported
	}
	i, _ := strconv.Atoi(cursor)
	if i >= len(f.articles) {
		return source.Page[source.Article]{}, nil
	}
	page := source.Page[source.Article]{Items: []source.Article{f.articles[i]}}
	if i+1 < len(f.articles) {
		page.Next = strconv.Itoa(i + 1)
	}
	return page, nil
}

func (f *fake) Download(_ context.Context, a source.Attachment) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[a.URL]
	if !ok {
		return nil, &source.RemoteError{Status: 404}
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

var base = time.Date(2023, 5, 1, 9, 0, 0, 0, time.UTC)

// fixture is a Zendesk account: three tickets to import (one loaded on demand), one
// deleted, two articles. agentEmail is a member's address, written as the source has it.
func fixture(agentEmail string) *fake {
	bob := &source.Person{ID: "u1", Email: "Bob@Customer.test", Name: "Bob Buyer", Company: "Bobco", Domain: "bobco.test"}
	dora := &source.Person{ID: "u3", Email: "dora@customer.test", Name: "Dora"}
	carol := &source.Person{ID: "u2", Email: "carol@customer.test", Name: "Carol"}
	solved := base.Add(2 * time.Hour)
	return &fake{
		pageSize: 2,
		scan: source.Scan{Account: source.Account{Key: "acme", Name: "Acme Support", URL: "https://acme.zendesk.com"}, Tickets: 4, Articles: 2,
			HasArticles: true,
			Agents:      []source.Agent{{ID: "a1", Name: "Anna Source", Email: strings.ToUpper(agentEmail)}, {ID: "a2", Name: "Gone Agent", Email: "gone@old.test"}},
			Groups:      []source.Group{{ID: "g1", Name: "Billing"}, {ID: "g2", Name: "Nobody"}}},
		tickets: []source.Ticket{
			{ID: "101", Ref: "#101", URL: "https://acme.zendesk.com/agent/tickets/101", Subject: "Cannot log in", Status: "solved", Priority: "high",
				Channel: "email", Requester: bob, AssigneeID: "a1", GroupID: "g1", Tags: []string{"vip", "login"}, CSAT: 5,
				CreatedAt: base, UpdatedAt: solved, SolvedAt: &solved, Loaded: true,
				Messages: []source.Message{
					{ID: "101-1", Author: *bob, HTML: `<p>I cannot log in</p><script>alert(1)</script>`, CreatedAt: base},
					{ID: "101-2", Agent: true, AuthorID: "a1", Author: source.Person{Name: "Anna Source", Email: agentEmail},
						HTML: `<p>Try the <a href="https://files.test/f1">steps</a></p>`, CreatedAt: base.Add(time.Hour),
						Attachments: []source.Attachment{{ID: "f1", Name: "steps.txt", URL: "https://files.test/f1"}}},
					{ID: "101-3", Agent: true, Note: true, AuthorID: "a1", Text: "Escalate if it happens again", CreatedAt: base.Add(90 * time.Minute)},
					{ID: "101-4", Author: *bob, Text: "Works now, thanks", CreatedAt: solved},
				}},
			{ID: "102", Ref: "#102", Subject: "Invoice", Status: "pending", Priority: "normal", Channel: "email", Requester: dora,
				CreatedAt: base.Add(24 * time.Hour), UpdatedAt: base.Add(25 * time.Hour)},
			{ID: "103", Ref: "#103", Subject: "Deleted", Skip: true, CreatedAt: base.Add(30 * time.Hour), UpdatedAt: base.Add(30 * time.Hour), Loaded: true},
			{ID: "104", Ref: "#104", Subject: "", Status: "open", Priority: "weird", Channel: "chat", Requester: carol, Tags: []string{"vip"},
				CreatedAt: base.Add(48 * time.Hour), UpdatedAt: base.Add(48 * time.Hour), Loaded: true,
				Messages: []source.Message{{ID: "104-1", Author: *carol, Text: "Hi from the chat", CreatedAt: base.Add(48 * time.Hour)}}},
		},
		loads: map[string][]source.Message{"102": {
			{ID: "102-1", Author: *dora, Text: "Please send the invoice", CreatedAt: base.Add(24 * time.Hour)},
			{ID: "102-2", Agent: true, AuthorID: "a2", Text: "Sent it", CreatedAt: base.Add(25 * time.Hour)},
		}},
		articles: []source.Article{
			{ID: "art1", Title: "Reset your password", HTML: "<p>Steps</p>", Collection: "Account", Published: true, CreatedAt: base, UpdatedAt: base},
			{ID: "art2", Title: "Draft notes", HTML: "<p>Soon</p>", Collection: "Account", CreatedAt: base, UpdatedAt: base},
		},
		files: map[string][]byte{"https://files.test/f1": []byte("1. Clear cookies\n2. Sign in\n")},
	}
}

type env struct {
	*hdtest.Env
	s     *Service
	f     *fake
	ws    hdtest.WS
	team  uuid.UUID
	admin hdtest.Auth
}

func newEnv(t *testing.T) *env {
	t.Helper()
	var s *Service
	e := hdtest.New(t, func(d *deps.Deps, api huma.API, r chi.Router) {
		s = New(d)
		s.registerRoutes(api, r)
	}, tickets.Register)
	ws := e.NewWorkspace()
	team := ids.New()
	e.Exec(`INSERT INTO teams (id, workspace_id, name) VALUES ($1, $2, 'billing')`, team, ws.ID)
	f := fixture(ws.Agent.Email)
	s.Factories[Zendesk] = f.factory
	return &env{Env: e, s: s, f: f, ws: ws, team: team, admin: ws.Admin.As(ws)}
}

func (e *env) zendeskBody() map[string]any {
	return map[string]any{"source": "zendesk", "credentials": map[string]string{"subdomain": "acme", "email": "admin@acme.test", "token": "secret-token"},
		"mapping": map[string]any{"agents": map[string]any{"a1": e.ws.Agent.ID, "a2": nil}, "groups": map[string]any{"g1": e.team}}}
}

func (e *env) start(t *testing.T, body map[string]any) Import {
	t.Helper()
	var x Import
	e.Do("POST", "/v1/imports", e.admin, body).Want(t, 201).JSON(t, &x)
	return x
}

func (e *env) load(t *testing.T, id uuid.UUID) row {
	t.Helper()
	x, err := loadByID(e.Ctx, e.D.DB.Pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

// popJobs removes and returns the queued jobs of kind for an import.
func (e *env) popJobs(t *testing.T, kind string, id uuid.UUID) []json.RawMessage {
	t.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `DELETE FROM river_job WHERE kind = $1 AND args->>'import_id' = $2 RETURNING args`, kind, id.String())
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[json.RawMessage])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// work runs fn like River would: again at once while it snoozes for no time. It returns
// a snooze for a while (a rate limit, a retry) to the caller.
func work(t *testing.T, fn func() error) error {
	t.Helper()
	for range 100 {
		err := fn()
		var snooze *rivertype.JobSnoozeError
		if errors.As(err, &snooze) && snooze.Duration == 0 {
			continue
		}
		return err
	}
	t.Fatal("the job kept snoozing")
	return nil
}

// drive runs the import's queued run jobs until none are left.
func (e *env) drive(t *testing.T, id uuid.UUID) row {
	t.Helper()
	for range 20 {
		raws := e.popJobs(t, jobs.ImportRunArgs{}.Kind(), id)
		if len(raws) == 0 {
			return e.load(t, id)
		}
		for _, raw := range raws {
			var a jobs.ImportRunArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				t.Fatal(err)
			}
			if err := work(t, func() error { return e.s.run(e.Ctx, a) }); err != nil {
				t.Fatalf("run: %v", err)
			}
		}
	}
	t.Fatal("run jobs did not settle")
	return row{}
}

func (e *env) driveUndo(t *testing.T, id uuid.UUID) row {
	t.Helper()
	raws := e.popJobs(t, jobs.ImportUndoArgs{}.Kind(), id)
	if len(raws) != 1 {
		t.Fatalf("undo jobs %d", len(raws))
	}
	var a jobs.ImportUndoArgs
	if err := json.Unmarshal(raws[0], &a); err != nil {
		t.Fatal(err)
	}
	if err := work(t, func() error { return e.s.undo(e.Ctx, a) }); err != nil {
		t.Fatalf("undo: %v", err)
	}
	return e.load(t, id)
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	e.Scalar(sql, &n, args...)
	return n
}

func (e *env) ticketByRef(t *testing.T, ref string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.Scalar(`SELECT id FROM tickets WHERE workspace_id = $1 AND custom->'import'->>'id' = $2`, &id, e.ws.ID, ref)
	return id
}

func (e *env) labels(t *testing.T, ticket uuid.UUID) []string {
	t.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT g.name FROM ticket_tags x JOIN tags g ON g.id = x.tag_id WHERE x.ticket_id = $1 ORDER BY g.name`, ticket)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// quietly asserts nothing announced or queued per ticket since seq: imported history is
// written quietly, and nothing is sent.
func (e *env) quietly(t *testing.T, since int64) {
	t.Helper()
	types, _ := e.Events(e.ws.ID, since)
	for _, typ := range types {
		if strings.HasPrefix(typ, "ticket.") || strings.HasPrefix(typ, "message.") || strings.HasPrefix(typ, "contact.") ||
			strings.HasPrefix(typ, "activity.") || strings.HasPrefix(typ, "company.") {
			t.Errorf("published %s for imported history (all: %v)", typ, types)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM river_job WHERE args->>'workspace_id' = $1 AND kind = ANY($2)`, e.ws.ID.String(),
		[]string{jobs.SendMessageArgs{}.Kind(), jobs.SendTicketEmailArgs{}.Kind(), jobs.ChatFollowUpArgs{}.Kind(), jobs.TicketTriggerArgs{}.Kind(),
			jobs.WebhookDispatchArgs{}.Kind(), jobs.AIEmailAnswerArgs{}.Kind()}); n != 0 {
		t.Errorf("%d send, trigger or webhook jobs queued for imported history", n)
	}
	if n := e.count(t, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND delivery_status <> 'none'`, e.ws.ID); n != 0 {
		t.Errorf("%d imported messages are waiting to be sent", n)
	}
}

func TestCheckSource(t *testing.T) {
	e := newEnv(t)
	var c ImportCheck
	resp := e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "zendesk", "credentials": map[string]string{"token": "secret-token"}}).Want(t, 200)
	resp.JSON(t, &c)
	if c.Account.Name != "Acme Support" || c.Account.URL != "https://acme.zendesk.com" || !c.HasArticles {
		t.Fatalf("account %+v", c)
	}
	if c.Totals.Tickets == nil || *c.Totals.Tickets != 4 || c.Totals.Articles == nil || *c.Totals.Articles != 2 {
		t.Fatalf("totals %+v", c.Totals)
	}
	if len(c.Agents) != 2 || c.Agents[0].UserID == nil || *c.Agents[0].UserID != e.ws.Agent.ID || c.Agents[1].UserID != nil {
		t.Fatalf("agents %+v", c.Agents)
	}
	if c.Agents[0].Email != strings.ToLower(e.ws.Agent.Email) {
		t.Errorf("agent email %q", c.Agents[0].Email)
	}
	if len(c.Groups) != 2 || c.Groups[0].TeamID == nil || *c.Groups[0].TeamID != e.team || c.Groups[1].TeamID != nil {
		t.Fatalf("groups %+v", c.Groups)
	}
	if len(c.Samples) != 3 {
		t.Fatalf("samples %+v", c.Samples)
	}
	s0, s1, s2 := c.Samples[0], c.Samples[1], c.Samples[2]
	if s0.Ref != "#101" || s0.MessageCount != 4 || s0.Messages[0].Text != "I cannot log in" || s0.Requester == nil ||
		s0.Requester.Email != "bob@customer.test" || !s0.Messages[1].Agent || s0.Messages[1].Attachments != 1 || !s0.Messages[2].Note {
		t.Errorf("sample 101 %+v", s0)
	}
	if s1.Ref != "#102" || s1.MessageCount != 2 { // loaded on demand
		t.Errorf("sample 102 %+v", s1)
	}
	if s2.Ref != "#104" || s2.Subject != "Hi from the chat" { // the deleted one is left out; no subject reads the first words
		t.Errorf("sample 104 %+v", s2)
	}
	if strings.Contains(string(resp.Body), "secret-token") {
		t.Error("the check answered the credentials")
	}

	resp = e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "zendesk", "credentials": map[string]string{}}).Want(t, 422)
	if m := resp.Map(t); m["code"] != "validation_failed" || !strings.Contains(string(resp.Body), "body.credentials.token") {
		t.Errorf("missing field %s", resp.Body)
	}
	e.f.checkErr = source.ErrAuth
	if m := e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "zendesk", "credentials": map[string]string{"token": "x"}}).
		Want(t, 422).Map(t); m["code"] != "import_auth_failed" {
		t.Errorf("refused %v", m)
	}
	e.f.checkErr = &source.Unreachable{Err: errors.New("timeout")}
	if m := e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "zendesk", "credentials": map[string]string{"token": "x"}}).
		Want(t, 502).Map(t); m["code"] != "import_unreachable" {
		t.Errorf("unreachable %v", m)
	}
	e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "csv"}).Want(t, 422)
	if n := e.count(t, `SELECT count(*) FROM imports WHERE workspace_id = $1`, e.ws.ID); n != 0 {
		t.Errorf("a check stored %d imports", n)
	}
}

func TestRunImport(t *testing.T) {
	e := newEnv(t)
	_, seq := e.Events(e.ws.ID, 0)
	resp := e.Do("POST", "/v1/imports", e.admin, e.zendeskBody()).Want(t, 201)
	if strings.Contains(string(resp.Body), "secret-token") {
		t.Fatal("the import answered its credentials")
	}
	var x Import
	resp.JSON(t, &x)
	if x.Status != StatusRunning || x.Phase != PhaseArticles || x.Run != 1 || x.Options.Tag != "zendesk-import" || !x.Options.Articles ||
		x.StartedBy == nil || x.StartedBy.ID != e.ws.Admin.ID || !x.CanSync || x.Totals.Tickets == nil || *x.Totals.Tickets != 4 {
		t.Fatalf("created %+v", x)
	}
	var sealed []byte
	e.Scalar(`SELECT credentials_enc FROM imports WHERE id = $1`, &sealed, x.ID)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("secret-token")) {
		t.Fatal("credentials are not sealed")
	}

	done := e.drive(t, x.ID)
	if done.Status != StatusCompleted || done.Phase != PhaseDone || done.FinishedAt == nil || done.LastError != "" {
		t.Fatalf("finished %+v", done.Import)
	}
	want := ImportCounts{Tickets: 3, Messages: 7, Contacts: 3, Companies: 1, Articles: 2, Attachments: 1, Skipped: 1}
	if done.Counts != want {
		t.Fatalf("counts %+v, want %+v (problems %+v)", done.Counts, want, done.Problems)
	}
	if n := e.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1`, e.ws.ID); n != 3 {
		t.Fatalf("tickets %d", n)
	}

	// Ticket 101: its own time, status, assignee, team, rating and labels.
	t101 := e.ticketByRef(t, "101")
	var created, solvedAt, lastMessage time.Time
	var status, priority, channel string
	var assignee, team, contact *uuid.UUID
	var csat *int
	var ref, url string
	if err := e.D.DB.Pool.QueryRow(e.Ctx, `SELECT created_at, solved_at, last_message_at, status, priority, channel, assignee_id, team_id,
		contact_id, csat_score, custom->'import'->>'ref', custom->'import'->>'url' FROM tickets WHERE id = $1`, t101).
		Scan(&created, &solvedAt, &lastMessage, &status, &priority, &channel, &assignee, &team, &contact, &csat, &ref, &url); err != nil {
		t.Fatal(err)
	}
	if !created.Equal(base) || !solvedAt.Equal(base.Add(2*time.Hour)) || !lastMessage.Equal(base.Add(2*time.Hour)) {
		t.Errorf("times created %v solved %v last %v", created, solvedAt, lastMessage)
	}
	if status != "solved" || priority != "high" || channel != "email" || csat == nil || *csat != 5 || ref != "#101" || url == "" {
		t.Errorf("ticket 101 %s %s %s csat %v ref %q", status, priority, channel, csat, ref)
	}
	if assignee == nil || *assignee != e.ws.Agent.ID || team == nil || *team != e.team || contact == nil {
		t.Errorf("assignee %v team %v contact %v", assignee, team, contact)
	}
	if got := e.labels(t, t101); !slices.Equal(got, []string{"login", "vip", "zendesk-import"}) {
		t.Errorf("labels %q", got)
	}
	var bobEmail, bobName string
	var company *uuid.UUID
	e.D.DB.Pool.QueryRow(e.Ctx, `SELECT email, name, company_id FROM contacts WHERE id = $1`, *contact).Scan(&bobEmail, &bobName, &company)
	if bobEmail != "bob@customer.test" || bobName != "Bob Buyer" || company == nil {
		t.Errorf("contact %q %q company %v", bobEmail, bobName, company)
	}

	type msg struct {
		Kind, Direction, AuthorType, Body, FromName, FromEmail string
		User, Contact                                          *uuid.UUID
		Created                                                time.Time
		Files                                                  int
	}
	messages := func(ticket uuid.UUID) []msg {
		rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT kind, direction, author_type, body_html, from_name, from_email, author_user_id,
			author_contact_id, created_at, (SELECT count(*) FROM attachments a WHERE a.message_id = m.id)
			FROM messages m WHERE ticket_id = $1 ORDER BY created_at`, ticket)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (msg, error) {
			var m msg
			err := r.Scan(&m.Kind, &m.Direction, &m.AuthorType, &m.Body, &m.FromName, &m.FromEmail, &m.User, &m.Contact, &m.Created, &m.Files)
			return m, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ms := messages(t101)
	if len(ms) != 4 {
		t.Fatalf("messages %+v", ms)
	}
	if ms[0].Direction != "inbound" || ms[0].Contact == nil || *ms[0].Contact != *contact || strings.Contains(ms[0].Body, "<script") || !ms[0].Created.Equal(base) {
		t.Errorf("customer message %+v", ms[0])
	}
	if ms[1].Direction != "outbound" || ms[1].User == nil || *ms[1].User != e.ws.Agent.ID || ms[1].FromName != e.ws.Agent.Name || ms[1].Files != 1 ||
		strings.Contains(ms[1].Body, "files.test") || !strings.Contains(ms[1].Body, "/attachments/") {
		t.Errorf("agent reply %+v", ms[1])
	}
	if ms[2].Kind != "note" || ms[2].User == nil || *ms[2].User != e.ws.Agent.ID {
		t.Errorf("note %+v", ms[2])
	}
	// 102: loaded on demand; an agent mapped to no one keeps their name and address.
	ms = messages(e.ticketByRef(t, "102"))
	if len(ms) != 2 || ms[1].User != nil || ms[1].FromName != "Gone Agent" || ms[1].FromEmail != "gone@old.test" || ms[1].Direction != "outbound" {
		t.Errorf("102 messages %+v", ms)
	}
	e.Scalar(`SELECT status FROM tickets WHERE id = $1`, &status, e.ticketByRef(t, "102"))
	if status != "pending" {
		t.Errorf("102 status %s", status)
	}
	// 104: a chat with an unknown priority and no subject.
	var subject string
	e.D.DB.Pool.QueryRow(e.Ctx, `SELECT status, priority, channel, subject FROM tickets WHERE id = $1`, e.ticketByRef(t, "104")).
		Scan(&status, &priority, &channel, &subject)
	if status != "open" || priority != "normal" || channel != "chat" || subject != "Hi from the chat" {
		t.Errorf("104 %s %s %s %q", status, priority, channel, subject)
	}

	// Articles and their collection.
	var published, drafts, collections int
	e.Scalar(`SELECT count(*) FROM kb_articles WHERE workspace_id = $1 AND status = 'published'`, &published, e.ws.ID)
	e.Scalar(`SELECT count(*) FROM kb_articles WHERE workspace_id = $1 AND status = 'draft'`, &drafts, e.ws.ID)
	e.Scalar(`SELECT count(*) FROM kb_collections WHERE workspace_id = $1`, &collections, e.ws.ID)
	if published != 1 || drafts != 1 || collections != 1 {
		t.Errorf("articles %d published %d drafts, %d collections", published, drafts, collections)
	}

	e.quietly(t, seq)
	types, _ := e.Events(e.ws.ID, seq)
	if !slices.Contains(types, "import.created") || !slices.Contains(types, "import.updated") {
		t.Errorf("events %v", types)
	}
	var note string
	e.Scalar(`SELECT data->>'status' FROM notifications WHERE workspace_id = $1 AND user_id = $2 AND type = $3`, &note, e.ws.ID, e.ws.Admin.ID, NotifyImportFinished)
	if note != "completed" {
		t.Errorf("notification %q", note)
	}

	// Running it again from the start writes nothing twice.
	e.Exec(`UPDATE imports SET status = 'running', phase = 'articles', cursor = '{}', finished_at = NULL WHERE id = $1`, x.ID)
	if err := work(t, func() error { return e.s.run(e.Ctx, jobs.ImportRunArgs{ImportID: x.ID, Generation: done.Generation}) }); err != nil {
		t.Fatal(err)
	}
	again := e.load(t, x.ID)
	if again.Status != StatusCompleted || again.Counts.Tickets != 3 || again.Counts.Messages != 7 || again.Counts.Contacts != 3 || again.Counts.Articles != 2 {
		t.Errorf("again %+v", again.Counts)
	}
	for table, n := range map[string]int{"tickets": 3, "messages": 7, "contacts": 3, "companies": 1, "kb_articles": 2, "kb_collections": 1, "attachments": 1} {
		if got := e.count(t, `SELECT count(*) FROM `+table+` WHERE workspace_id = $1`, e.ws.ID); got != n {
			t.Errorf("%s: %d after running again, want %d", table, got, n)
		}
	}
	e.quietly(t, seq)

	// A second import of the same account finds everything already here.
	e.Exec(`UPDATE imports SET status = 'completed' WHERE id = $1`, x.ID)
	second := e.start(t, e.zendeskBody())
	sd := e.drive(t, second.ID)
	if sd.Status != StatusCompleted || sd.Counts.Tickets != 0 || sd.Counts.Messages != 0 || sd.Counts.Articles != 0 || sd.Counts.Contacts != 0 {
		t.Errorf("second import %+v", sd.Counts)
	}
	if n := e.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1`, e.ws.ID); n != 3 {
		t.Errorf("tickets after a second import %d", n)
	}
	// Undoing it removes nothing the first one brought.
	e.Do("POST", "/v1/imports/"+second.ID.String()+"/undo", e.admin, nil).Want(t, 200)
	if u := e.driveUndo(t, second.ID); u.Status != StatusUndone {
		t.Fatalf("undo second %s", u.Status)
	}
	if n := e.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1`, e.ws.ID); n != 3 {
		t.Errorf("tickets after undoing the second import %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM kb_articles WHERE workspace_id = $1`, e.ws.ID); n != 2 {
		t.Errorf("articles after undoing the second import %d", n)
	}
}

func TestSync(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, e.zendeskBody())
	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 409) // still running
	first := e.drive(t, x.ID)
	if first.Status != StatusCompleted {
		t.Fatalf("first run %s %s", first.Status, first.LastError)
	}
	_, seq := e.Events(e.ws.ID, 0)

	// Nothing changed.
	var synced Import
	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 200).JSON(t, &synced)
	if synced.Status != StatusRunning || synced.Run != 2 || synced.Phase != PhaseArticles {
		t.Fatalf("sync %+v", synced)
	}
	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 409)
	e.f.updatedSince = nil
	nothing := e.drive(t, x.ID)
	if nothing.Status != StatusCompleted || nothing.Counts.Tickets != 3 || nothing.Counts.Updated != 0 || nothing.Counts.Messages != 7 {
		t.Fatalf("empty sync %+v", nothing.Counts)
	}
	if len(e.f.updatedSince) == 0 || e.f.updatedSince[0] == nil || !e.f.updatedSince[0].Before(first.StartedAt) {
		t.Errorf("sync asked for tickets updated since %v (first run started %v)", e.f.updatedSince, first.StartedAt)
	}

	// Ticket 101 has a new reply and is open again; 105 is new.
	now := time.Now().UTC().Truncate(time.Second)
	e.f.mu.Lock()
	t101 := &e.f.tickets[0]
	t101.Status, t101.SolvedAt, t101.UpdatedAt = "open", nil, now
	t101.Messages = append(t101.Messages, source.Message{ID: "101-5", Author: *t101.Requester, Text: "It broke again", CreatedAt: now.Add(-time.Minute)})
	e.f.tickets = append(e.f.tickets, source.Ticket{ID: "105", Ref: "#105", Subject: "New one", Status: "open", Channel: "email",
		Requester: &source.Person{ID: "u9", Email: "new@customer.test"}, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, Loaded: true,
		Messages: []source.Message{{ID: "105-1", Text: "Hello", Author: source.Person{Email: "new@customer.test"}, CreatedAt: now.Add(-time.Hour)}}})
	e.f.mu.Unlock()

	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 200)
	after := e.drive(t, x.ID)
	if after.Status != StatusCompleted || after.Run != 3 || after.Counts.Tickets != 4 || after.Counts.Updated != 1 || after.Counts.Messages != 9 {
		t.Fatalf("sync %+v", after.Counts)
	}
	id := e.ticketByRef(t, "101")
	var status string
	var messages int
	var solved *time.Time
	e.D.DB.Pool.QueryRow(e.Ctx, `SELECT status, solved_at, (SELECT count(*) FROM messages WHERE ticket_id = t.id) FROM tickets t WHERE id = $1`, id).
		Scan(&status, &solved, &messages)
	if status != "open" || solved != nil || messages != 5 {
		t.Errorf("101 after sync: %s solved %v, %d messages", status, solved, messages)
	}
	if n := e.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1`, e.ws.ID); n != 4 {
		t.Errorf("tickets %d", n)
	}
	e.quietly(t, seq)

	// Without credentials there is nothing to sync from.
	var gone Import
	e.Do("DELETE", "/v1/imports/"+x.ID.String()+"/credentials", e.admin, nil).Want(t, 200).JSON(t, &gone)
	if gone.CanSync {
		t.Error("can_sync after removing the credentials")
	}
	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 409)
}

func TestCSVImport(t *testing.T) {
	e := newEnv(t)
	csvData := "Ticket ID,Subject,Body,From email,From name,Author type,Status,Priority,Tags,Created at\n" +
		"C-1,Refund,I want a refund,dan@customer.test,Dan,customer,solved,high,billing,2024-01-10 08:00:00\n" +
		"C-1,,\"Refund sent.\nAnything else?\"," + strings.ToUpper(e.ws.Agent.Email) + ",Anna,agent,,,,2024-01-10 09:00:00\n" +
		"C-2,Hello,Just saying hi,erin@customer.test,Erin,,open,,,2024-01-11 10:00:00\n"

	e.Upload("/v1/imports/csv", e.admin, "export.xlsx", []byte(csvData)).Want(t, 422)
	e.Upload("/v1/imports/csv", e.ws.Agent.As(e.ws), "export.csv", []byte(csvData)).Want(t, 403)

	var up ImportCSVUpload
	e.Upload("/v1/imports/csv", e.admin, "export.csv", []byte(csvData)).Want(t, 201).JSON(t, &up)
	if up.Rows != 3 || len(up.Columns) != 10 || up.Suggested.TicketID != "Ticket ID" || up.Suggested.Author != "Author type" ||
		up.Suggested.FromEmail != "From email" || len(up.Preview) != 2 || up.Preview[0].MessageCount != 2 {
		t.Fatalf("upload %+v", up)
	}
	if up.Columns[1].Name != "Subject" || !slices.Equal(up.Columns[1].Samples, []string{"Refund", "Hello"}) {
		t.Errorf("columns %+v", up.Columns)
	}

	var c ImportCheck
	e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "csv", "upload_id": up.UploadID}).Want(t, 200).JSON(t, &c)
	if c.Totals.Tickets == nil || *c.Totals.Tickets != 2 || len(c.Agents) != 1 || c.Agents[0].UserID == nil || *c.Agents[0].UserID != e.ws.Agent.ID {
		t.Fatalf("check %+v", c)
	}
	e.Do("POST", "/v1/imports/check", e.admin, map[string]any{"source": "csv", "upload_id": up.UploadID,
		"mapping": map[string]any{"body": "Nope"}}).Want(t, 422)

	// Another workspace cannot use the upload.
	other := e.NewWorkspace()
	e.Do("POST", "/v1/imports", other.Admin.As(other), map[string]any{"source": "csv", "upload_id": up.UploadID}).Want(t, 404)

	var x Import
	e.Do("POST", "/v1/imports", e.admin, map[string]any{"source": "csv", "upload_id": up.UploadID}).Want(t, 201).JSON(t, &x)
	if x.Source != CSV || x.Phase != PhaseTickets || x.Options.Articles || x.Options.Tag != "csv-import" || x.CanSync || x.Mapping.CSV == nil {
		t.Fatalf("csv import %+v", x)
	}
	if n := e.count(t, `SELECT count(*) FROM import_uploads WHERE id = $1`, up.UploadID); n != 0 {
		t.Error("the upload row stayed")
	}
	var key string
	e.Scalar(`SELECT storage_key FROM imports WHERE id = $1`, &key, x.ID)

	done := e.drive(t, x.ID)
	if done.Status != StatusCompleted || done.Counts.Tickets != 2 || done.Counts.Messages != 3 || done.Counts.Contacts != 2 {
		t.Fatalf("csv done %s %+v %+v", done.Status, done.Counts, done.Problems)
	}
	if done.StorageKey != nil {
		t.Error("the file is still recorded after the import finished")
	}
	if rc, err := e.D.Storage.Get(e.Ctx, key); err == nil {
		rc.Close()
		t.Error("the file is still stored after the import finished")
	}
	id := e.ticketByRef(t, "C-1")
	var status, priority string
	var created time.Time
	e.D.DB.Pool.QueryRow(e.Ctx, `SELECT status, priority, created_at FROM tickets WHERE id = $1`, id).Scan(&status, &priority, &created)
	if status != "solved" || priority != "high" || !created.Equal(time.Date(2024, 1, 10, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("C-1 %s %s %v", status, priority, created)
	}
	var author *uuid.UUID
	var body string
	e.D.DB.Pool.QueryRow(e.Ctx, `SELECT author_user_id, body_text FROM messages WHERE ticket_id = $1 AND direction = 'outbound'`, id).Scan(&author, &body)
	if author == nil || *author != e.ws.Agent.ID || body != "Refund sent.\nAnything else?" {
		t.Errorf("agent row %v %q", author, body)
	}
	if got := e.labels(t, id); !slices.Equal(got, []string{"billing", "csv-import"}) {
		t.Errorf("labels %q", got)
	}
	e.quietly(t, 0)

	// The upload went with the import; CSV imports run once.
	e.Do("POST", "/v1/imports", e.admin, map[string]any{"source": "csv", "upload_id": up.UploadID}).Want(t, 404)
	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 409)
}

func TestCSVImportKeepsMessageIDs(t *testing.T) {
	e := newEnv(t)
	csvData := "Ticket ID,Body,From email,Author type,Created at,Message-ID,In-Reply-To,References\n" +
		"M-1,Where is my order?,dan@customer.test,customer,2024-02-01 08:00,<o1@customer.test>,,\n" +
		"M-1,It ships today.,anna@team.test,agent,2024-02-01 09:00,<o2@team.test>,<o1@customer.test>,<o1@customer.test>\n" +
		"M-2,The same email again,dan@customer.test,customer,2024-02-02 08:00,<o1@customer.test>,,\n"
	var up ImportCSVUpload
	e.Upload("/v1/imports/csv", e.admin, "export.csv", []byte(csvData)).Want(t, 201).JSON(t, &up)
	if up.Suggested.MessageID != "Message-ID" || up.Suggested.InReplyTo != "In-Reply-To" || up.Suggested.References != "References" {
		t.Fatalf("suggested %+v", up.Suggested)
	}
	var x Import
	e.Do("POST", "/v1/imports", e.admin, map[string]any{"source": "csv", "upload_id": up.UploadID}).Want(t, 201).JSON(t, &x)
	if done := e.drive(t, x.ID); done.Status != StatusCompleted || done.Counts.Messages != 3 {
		t.Fatalf("done %s %+v %+v", done.Status, done.Counts, done.Problems)
	}
	type row struct {
		msgID     *string
		inReplyTo string
		refs      []string
	}
	read := func(ticket uuid.UUID) []row {
		rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT email_message_id, in_reply_to, "references" FROM messages WHERE ticket_id = $1 ORDER BY created_at`, ticket)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.msgID, &r.inReplyTo, &r.refs); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	m1 := read(e.ticketByRef(t, "M-1"))
	if len(m1) != 2 || m1[0].msgID == nil || *m1[0].msgID != "<o1@customer.test>" || m1[1].msgID == nil || *m1[1].msgID != "<o2@team.test>" ||
		m1[1].inReplyTo != "<o1@customer.test>" || !slices.Equal(m1[1].refs, []string{"<o1@customer.test>"}) {
		t.Errorf("M-1 %+v", m1)
	}
	// A Message-ID another message holds stays with that one; the row still comes over.
	if m2 := read(e.ticketByRef(t, "M-2")); len(m2) != 1 || m2[0].msgID != nil {
		t.Errorf("M-2 %+v", m2)
	}
}

func TestLifecycle(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, e.zendeskBody())
	path := "/v1/imports/" + x.ID.String()
	if m := e.Do("POST", "/v1/imports", e.admin, e.zendeskBody()).Want(t, 409).Map(t); m["code"] != "import_running" {
		t.Fatalf("second import %v", m)
	}
	first := e.popJobs(t, jobs.ImportRunArgs{}.Kind(), x.ID)
	if len(first) != 1 {
		t.Fatalf("queued %d", len(first))
	}

	var p Import
	e.Do("POST", path+"/pause", e.admin, nil).Want(t, 200).JSON(t, &p)
	if p.Status != StatusPaused {
		t.Fatalf("paused %s", p.Status)
	}
	e.Do("POST", path+"/pause", e.admin, nil).Want(t, 409)
	e.Do("POST", "/v1/imports", e.admin, e.zendeskBody()).Want(t, 409) // paused still holds the workspace
	// The job queued before the pause does nothing.
	if err := e.s.run(e.Ctx, jobs.ImportRunArgs{ImportID: x.ID, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1`, e.ws.ID); n != 0 {
		t.Fatalf("a paused import wrote %d tickets", n)
	}

	e.Do("POST", path+"/resume", e.admin, map[string]any{"credentials": map[string]string{"token": "other"}}).Want(t, 422)
	var r Import
	e.Do("POST", path+"/resume", e.admin, map[string]any{}).Want(t, 200).JSON(t, &r)
	if r.Status != StatusRunning {
		t.Fatalf("resumed %s", r.Status)
	}
	e.Do("POST", path+"/resume", e.admin, nil).Want(t, 409)
	e.Do("POST", path+"/undo", e.admin, nil).Want(t, 409)
	e.Do("DELETE", path+"/credentials", e.admin, nil).Want(t, 409)
	if err := e.s.run(e.Ctx, jobs.ImportRunArgs{ImportID: x.ID, Generation: 1}); err != nil { // an older generation
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM kb_articles WHERE workspace_id = $1`, e.ws.ID); n != 0 {
		t.Fatalf("an old job wrote %d articles", n)
	}

	// The source refuses the credentials: paused until new ones come.
	e.f.errs = []error{source.ErrAuth}
	refused := e.drive(t, x.ID)
	if refused.Status != StatusPaused || !refused.NeedsCredentials || refused.LastError == "" || refused.Counts.Articles != 2 {
		t.Fatalf("refused %+v", refused.Import)
	}
	var note string
	e.Scalar(`SELECT data->>'status' FROM notifications WHERE workspace_id = $1 AND user_id = $2 AND type = $3`, &note, e.ws.ID, e.ws.Admin.ID, NotifyImportFinished)
	if note != "needs_credentials" {
		t.Errorf("notification %q", note)
	}
	e.Do("POST", path+"/resume", e.admin, map[string]any{"credentials": map[string]string{"token": "new-token"}}).Want(t, 200).JSON(t, &r)
	if r.NeedsCredentials || r.LastError != "" {
		t.Fatalf("resumed with new credentials %+v", r)
	}
	// Only the token was sent again: the other fields kept their stored values.
	var sealed []byte
	e.Scalar(`SELECT credentials_enc FROM imports WHERE id = $1`, &sealed, x.ID)
	var kept map[string]string
	if err := e.s.d.Box.OpenJSON(sealed, &kept); err != nil || kept["token"] != "new-token" || kept["subdomain"] != "acme" || kept["email"] != "admin@acme.test" {
		t.Fatalf("stored credentials %v %v", kept, err)
	}

	// The source asks to slow down: the job waits it out.
	e.f.errs = []error{&source.RateLimited{RetryAfter: time.Minute}}
	jobsNow := e.popJobs(t, jobs.ImportRunArgs{}.Kind(), x.ID)
	if len(jobsNow) != 1 {
		t.Fatalf("queued %d", len(jobsNow))
	}
	var a jobs.ImportRunArgs
	_ = json.Unmarshal(jobsNow[0], &a)
	var snooze *rivertype.JobSnoozeError
	if err := work(t, func() error { return e.s.run(e.Ctx, a) }); !errors.As(err, &snooze) || snooze.Duration < 50*time.Second {
		t.Fatalf("rate limit: %v", err)
	}
	waiting := e.load(t, x.ID)
	if waiting.Status != StatusRunning || waiting.WaitingUntil == nil {
		t.Fatalf("waiting %+v", waiting.Import)
	}
	if err := e.s.run(e.Ctx, a); !errors.As(err, &snooze) {
		t.Fatalf("ran while waiting: %v", err)
	}
	e.Exec(`UPDATE imports SET waiting_until = NULL WHERE id = $1`, x.ID)
	if err := work(t, func() error { return e.s.run(e.Ctx, a) }); err != nil {
		t.Fatal(err)
	}
	done := e.load(t, x.ID)
	if done.Status != StatusCompleted || done.Counts.Tickets != 3 || done.Counts.Articles != 2 {
		t.Fatalf("done %s %+v", done.Status, done.Counts)
	}
	e.Do("POST", path+"/cancel", e.admin, nil).Want(t, 409)
	e.Do("POST", path+"/pause", e.admin, nil).Want(t, 409)

	// Cancel: what is there stays, the credentials go, the job stops.
	y := e.start(t, e.zendeskBody())
	ypath := "/v1/imports/" + y.ID.String()
	var c Import
	e.Do("POST", ypath+"/cancel", e.admin, nil).Want(t, 200).JSON(t, &c)
	if c.Status != StatusCanceled || c.CanSync || c.FinishedAt == nil {
		t.Fatalf("canceled %+v", c)
	}
	if n := e.count(t, `SELECT count(*) FROM imports WHERE id = $1 AND credentials_enc IS NULL`, y.ID); n != 1 {
		t.Error("credentials kept after cancel")
	}
	if got := e.drive(t, y.ID); got.Status != StatusCanceled {
		t.Fatalf("a canceled import ran: %s", got.Status)
	}
	e.Do("POST", ypath+"/cancel", e.admin, nil).Want(t, 409)
	e.Do("POST", ypath+"/resume", e.admin, nil).Want(t, 409)
	e.start(t, e.zendeskBody()) // nothing holds the workspace now

	var list struct{ Data []Import }
	e.Do("GET", "/v1/imports", e.admin, nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 3 {
		t.Errorf("list %d", len(list.Data))
	}
}

func TestUndo(t *testing.T) {
	e := newEnv(t)
	// Carol and the vip label were here before the import.
	carol := ids.New()
	e.Exec(`INSERT INTO contacts (id, workspace_id, name, email) VALUES ($1, $2, 'Carol', 'carol@customer.test')`, carol, e.ws.ID)
	var vip uuid.UUID
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		var err error
		vip, err = domain.CreateLabel(e.Ctx, tx, e.ws.ID, "vip")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	x := e.start(t, e.zendeskBody())
	done := e.drive(t, x.ID)
	if done.Status != StatusCompleted || done.Counts.Contacts != 2 {
		t.Fatalf("import %s %+v", done.Status, done.Counts)
	}
	var bob, dora uuid.UUID
	e.Scalar(`SELECT id FROM contacts WHERE workspace_id = $1 AND email = 'bob@customer.test'`, &bob, e.ws.ID)
	e.Scalar(`SELECT id FROM contacts WHERE workspace_id = $1 AND email = 'dora@customer.test'`, &dora, e.ws.ID)
	var attachmentKey string
	e.Scalar(`SELECT storage_key FROM attachments WHERE workspace_id = $1`, &attachmentKey, e.ws.ID)
	// Bob writes in again, not through the import.
	var own domain.Ticket
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		var err error
		own, err = domain.CreateTicket(e.Ctx, tx, domain.NewTicket{WorkspaceID: e.ws.ID, Subject: "Another question", Channel: "email", ContactID: &bob})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.Exec(`INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1, $2)`, own.ID, vip)
	// The team writes on imported ticket 102: it stays, with what it uses.
	written := e.ticketByRef(t, "102")
	e.Do("POST", "/v1/tickets/"+written.String()+"/messages", e.ws.Agent.As(e.ws),
		map[string]any{"kind": "note", "body_html": "<p>Followed up by phone</p>"}).Want(t, 201)

	path := "/v1/imports/" + x.ID.String()
	var u Import
	e.Do("POST", path+"/undo", e.admin, nil).Want(t, 200).JSON(t, &u)
	if u.Status != StatusUndoing {
		t.Fatalf("undo %s", u.Status)
	}
	e.Do("POST", path+"/undo", e.admin, nil).Want(t, 409)
	e.Do("POST", "/v1/imports", e.admin, e.zendeskBody()).Want(t, 409)

	undone := e.driveUndo(t, x.ID)
	if undone.Status != StatusUndone || len(undone.Credentials) != 0 {
		t.Fatalf("undone %+v", undone.Import)
	}
	var left []uuid.UUID
	rows, _ := e.D.DB.Pool.Query(e.Ctx, `SELECT id FROM tickets WHERE workspace_id = $1`, e.ws.ID)
	left, _ = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if len(left) != 2 || !slices.Contains(left, own.ID) || !slices.Contains(left, written) {
		t.Fatalf("tickets left %v, want %s and %s", left, own.ID, written)
	}
	for what, want := range map[string]int{
		`SELECT count(*) FROM messages WHERE workspace_id = $1`:                                   3, // 102's two and the note
		`SELECT count(*) FROM attachments WHERE workspace_id = $1`:                                0,
		`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND email = 'bob@customer.test'`:   1, // has a ticket of his own
		`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND email = 'carol@customer.test'`: 1, // was here before
		`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND email = 'dora@customer.test'`:  1, // on 102
		`SELECT count(*) FROM companies WHERE workspace_id = $1`:                                  1, // Bob is still at Bobco
		`SELECT count(*) FROM tags WHERE workspace_id = $1 AND name = 'vip'`:                      1,
		`SELECT count(*) FROM tags WHERE workspace_id = $1 AND name = 'login'`:                    0,
		`SELECT count(*) FROM tags WHERE workspace_id = $1 AND name = 'zendesk-import'`:           1, // on 102
		`SELECT count(*) FROM kb_articles WHERE workspace_id = $1`:                                0,
		`SELECT count(*) FROM kb_collections WHERE workspace_id = $1`:                             0,
		`SELECT count(*) FROM import_records WHERE workspace_id = $1`:                             0,
	} {
		if got := e.count(t, what, e.ws.ID); got != want {
			t.Errorf("%s = %d, want %d", what, got, want)
		}
	}
	if rc, err := e.D.Storage.Get(e.Ctx, attachmentKey); err == nil {
		rc.Close()
		t.Error("the attachment file stayed")
	}
	e.Do("POST", path+"/undo", e.admin, nil).Want(t, 409)
	e.Do("POST", path+"/sync", e.admin, nil).Want(t, 409)
	// Importing again brings back what went, as new; 102 was forgotten, so it comes again.
	again := e.drive(t, e.start(t, e.zendeskBody()).ID)
	if again.Status != StatusCompleted || again.Counts.Tickets != 3 {
		t.Fatalf("again %s %+v", again.Status, again.Counts)
	}
}

func TestOneBadTicketDoesNotStopTheRest(t *testing.T) {
	e := newEnv(t)
	bob := &source.Person{ID: "u1", Email: "bob@customer.test", Name: "Bob"}
	e.f.articles = nil
	e.f.tickets = []source.Ticket{
		// The database refuses this message's id: the ticket is rolled back whole.
		{ID: "1", Ref: "#1", Subject: "Broken", Status: "open", Requester: bob, Tags: []string{"first"}, CreatedAt: base, UpdatedAt: base, Loaded: true,
			Messages: []source.Message{{ID: "bad\xff", Author: *bob, Text: "Hello", CreatedAt: base}}},
		{ID: "2", Ref: "#2", Subject: "Fine", Status: "open", Requester: bob, Tags: []string{"first"}, CreatedAt: base, UpdatedAt: base, Loaded: true,
			Messages: []source.Message{{ID: "2-1", Author: *bob, Text: "Hello again", CreatedAt: base}}},
	}
	x := e.start(t, e.zendeskBody())
	done := e.drive(t, x.ID)
	if done.Status != StatusCompleted {
		t.Fatalf("status %s %s", done.Status, done.LastError)
	}
	if done.Counts.Tickets != 1 || done.Counts.Failed != 1 || done.Counts.Contacts != 1 || done.Counts.Messages != 1 {
		t.Errorf("counts %+v problems %+v", done.Counts, done.Problems)
	}
	if len(done.Problems) != 1 || done.Problems[0].Item != "Ticket #1" || strings.Contains(done.Problems[0].Message, "SQLSTATE") {
		t.Errorf("problems %+v", done.Problems)
	}
	if n := e.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND contact_id IS NOT NULL`, e.ws.ID); n != 1 {
		t.Errorf("tickets with their contact %d", n)
	}
}

func TestIsolationAndRoles(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, e.zendeskBody())
	path := "/v1/imports/" + x.ID.String()

	other := e.NewWorkspace()
	oa := other.Admin.As(other)
	e.Do("GET", path, oa, nil).Want(t, 404)
	for _, action := range []string{"pause", "resume", "cancel", "sync", "undo"} {
		e.Do("POST", path+"/"+action, oa, map[string]any{}).Want(t, 404)
	}
	e.Do("DELETE", path+"/credentials", oa, nil).Want(t, 404)
	var list struct{ Data []Import }
	e.Do("GET", "/v1/imports", oa, nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Errorf("another workspace lists %d imports", len(list.Data))
	}
	e.Do("GET", "/v1/imports/not-an-id", e.admin, nil).Want(t, 404)
	if got := e.load(t, x.ID); got.Status != StatusRunning {
		t.Fatalf("another workspace changed the import: %s", got.Status)
	}

	for _, u := range []hdtest.User{e.ws.Agent, e.ws.Viewer} {
		a := u.As(e.ws)
		e.Do("POST", "/v1/imports", a, e.zendeskBody()).Want(t, 403)
		e.Do("POST", "/v1/imports/check", a, map[string]any{"source": "zendesk", "credentials": map[string]string{"token": "x"}}).Want(t, 403)
		e.Do("GET", "/v1/imports", a, nil).Want(t, 403)
		e.Do("GET", path, a, nil).Want(t, 403)
		e.Do("POST", path+"/pause", a, nil).Want(t, 403)
	}
	key := hdtest.Auth{Bearer: e.APIKey(e.ws, auth.ScopeSettingsRead, auth.ScopeSettingsWrite)}
	e.Do("POST", "/v1/imports/check", key, map[string]any{"source": "zendesk", "credentials": map[string]string{"token": "x"}}).Want(t, 403)
	e.Do("POST", path+"/pause", key, nil).Want(t, 403)
	e.Do("GET", path, key, nil).Want(t, 200) // reading is settings:read
	e.Do("GET", path, e.ws.Owner.As(e.ws), nil).Want(t, 200)
}
