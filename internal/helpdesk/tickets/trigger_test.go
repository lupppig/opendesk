package tickets_test

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func addSLA(t *testing.T, e *hdtest.Env, ws hdtest.WS, priority any, fr, res, pos int, enabled bool) uuid.UUID {
	t.Helper()
	id := ids.New()
	e.Exec(`INSERT INTO sla_policies (id, workspace_id, name, priority, first_response_minutes, resolution_minutes, position, enabled)
		VALUES ($1, $2, 'policy', $3, $4, $5, $6, $7)`, id, ws.ID, priority, fr, res, pos, enabled)
	return id
}

func addAutomation(t *testing.T, e *hdtest.Env, ws hdtest.WS, trigger string, pos int, conds, acts any) uuid.UUID {
	t.Helper()
	id := ids.New()
	c, _ := json.Marshal(conds)
	a, _ := json.Marshal(acts)
	e.Exec(`INSERT INTO automations (id, workspace_id, name, trigger, conditions, actions, position) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, ws.ID, "auto "+trigger, trigger, c, a, pos)
	return id
}

func TestSLA(t *testing.T) {
	e, ws := newEnv(t)
	addSLA(t, e, ws, "urgent", 10, 100, 0, false) // disabled
	high := addSLA(t, e, ws, "high", 30, 240, 1, true)
	anyP := addSLA(t, e, ws, nil, 60, 1440, 2, true)

	tk := createTicket(t, e, ws, map[string]any{"priority": "high", "kind": "note"})
	runTriggers(t, e, ws.ID)
	got := getTicket(t, e, ws, tk.ID)
	if got.SLA == nil || got.SLA.PolicyID != high || got.SLA.Breached {
		t.Fatalf("sla %+v", got.SLA)
	}
	if d := got.SLA.FirstResponseDueAt.Sub(got.CreatedAt); d != 30*time.Minute {
		t.Fatalf("first response due after %v", d)
	}
	if d := got.SLA.ResolutionDueAt.Sub(got.CreatedAt); d != 240*time.Minute {
		t.Fatalf("resolution due after %v", d)
	}
	// Priority change re-picks the policy.
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"priority": "urgent"}).Want(t, 200)
	runTriggers(t, e, ws.ID)
	got = getTicket(t, e, ws, tk.ID)
	if got.SLA == nil || got.SLA.PolicyID != anyP {
		t.Fatalf("after priority change %+v", got.SLA)
	}

	// Breach scanner: once per kind, with an activity and a notification for the assignee.
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Admin.As(ws), map[string]any{"assignee_id": ws.Agent.ID.String()}).Want(t, 200)
	e.Exec(`UPDATE tickets SET first_response_due_at = now() - interval '1 minute', first_responded_at = NULL WHERE id = $1`, tk.ID)
	for i := 0; i < 2; i++ {
		if err := tickets.ScanSLA(e.Ctx, e.D); err != nil {
			t.Fatal(err)
		}
	}
	if n := countRows(t, e, `SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'sla_breached'`, tk.ID); n != 1 {
		t.Fatalf("breach activities %d", n)
	}
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'sla_breached' AND user_id = $2`, tk.ID, ws.Agent.ID); n != 1 {
		t.Fatalf("breach notifications %d", n)
	}
	if !getTicket(t, e, ws, tk.ID).SLA.Breached {
		t.Fatal("not shown as breached")
	}
	e.Exec(`UPDATE tickets SET resolution_due_at = now() - interval '1 minute' WHERE id = $1`, tk.ID)
	if err := tickets.ScanSLA(e.Ctx, e.D); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	e.Scalar(`SELECT array_agg(data->>'kind' ORDER BY data->>'kind') FROM activities WHERE ticket_id = $1 AND type = 'sla_breached'`, &kinds, tk.ID)
	if !slices.Equal(kinds, []string{"first_response", "resolution"}) {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestAutomations(t *testing.T) {
	e, ws := newEnv(t)
	team := addTeam(t, e, ws, ws.Agent.ID)
	main := addAutomation(t, e, ws, jobs.TriggerTicketCreated, 0,
		[]map[string]string{{"field": "subject", "op": "contains", "value": "REFUND"}, {"field": "channel", "op": "equals", "value": "api"}},
		[]map[string]string{{"type": "set_priority", "value": "urgent"}, {"type": "add_tag", "value": "billing"},
			{"type": "assign_user", "value": ws.Agent.ID.String()}, {"type": "assign_team", "value": team.String()},
			{"type": "add_note", "value": "<p>Auto triaged</p>"}, {"type": "send_reply", "value": "<p>We got your refund request.</p>"}})
	// Does not match: tag condition.
	skipped := addAutomation(t, e, ws, jobs.TriggerTicketCreated, 1,
		[]map[string]string{{"field": "tag", "op": "equals", "value": "nope"}},
		[]map[string]string{{"type": "set_status", "value": "closed"}})
	// Follows up on the chain: ticket_updated with tag billing.
	chained := addAutomation(t, e, ws, jobs.TriggerTicketUpdated, 0,
		[]map[string]string{{"field": "tag", "op": "contains", "value": "bill"}, {"field": "priority", "op": "equals", "value": "urgent"}},
		[]map[string]string{{"type": "set_status", "value": "pending"}})

	tk := createTicket(t, e, ws, map[string]any{"subject": "Refund please", "body_html": "<p>my order</p>"})
	runTriggers(t, e, ws.ID)
	got := getTicket(t, e, ws, tk.ID)
	if got.Priority != "urgent" || got.Assignee == nil || got.Assignee.ID != ws.Agent.ID || got.TeamID == nil || *got.TeamID != team ||
		len(got.Tags) != 1 || got.Status != "pending" {
		t.Fatalf("automated ticket %+v", got)
	}
	var runs []int64
	e.Scalar(`SELECT array_agg(run_count ORDER BY id = $2 DESC, id = $3 DESC) FROM automations WHERE id = ANY($1)`, &runs,
		[]uuid.UUID{main, chained, skipped}, main, chained)
	if !slices.Equal(runs, []int64{1, 1, 0}) {
		t.Fatalf("run counts %v", runs)
	}
	var msgs struct{ Data []domain.Message }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &msgs)
	if len(msgs.Data) != 3 || msgs.Data[1].Kind != "note" || msgs.Data[1].Author.Type != "system" || msgs.Data[2].Kind != "reply" {
		t.Fatalf("automation messages %+v", msgs.Data)
	}
	if n := countRows(t, e, `SELECT count(*) FROM activities WHERE ticket_id = $1 AND actor_type = 'automation' AND data->>'automation_id' = $2`,
		tk.ID, main.String()); n < 4 {
		t.Fatalf("automation activities %d", n)
	}
	// Automations do not notify the assignee as if a person assigned them? They do: the actor is not the assignee.
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'assigned'`, tk.ID); n != 1 {
		t.Fatalf("assigned notifications %d", n)
	}
	// Nothing re-queued: automation writes do not enqueue triggers.
	if n := len(e.Jobs(jobs.TicketTriggerArgs{}.Kind(), ws.ID)); n != 0 {
		t.Fatalf("%d triggers queued by automations", n)
	}
}

func TestAutomationLoopGuard(t *testing.T) {
	e, ws := newEnv(t)
	// Two ticket_updated automations that flip each other forever without a guard.
	a := addAutomation(t, e, ws, jobs.TriggerTicketUpdated, 0,
		[]map[string]string{{"field": "status", "op": "equals", "value": "open"}}, []map[string]string{{"type": "set_status", "value": "pending"}})
	b := addAutomation(t, e, ws, jobs.TriggerTicketUpdated, 1,
		[]map[string]string{{"field": "status", "op": "equals", "value": "pending"}}, []map[string]string{{"type": "set_status", "value": "open"}})
	tk := createTicket(t, e, ws, map[string]any{"kind": "note"})
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"priority": "high"}).Want(t, 200)
	runTriggers(t, e, ws.ID)
	var runs []int64
	e.Scalar(`SELECT array_agg(run_count ORDER BY position) FROM automations WHERE id = ANY($1)`, &runs, []uuid.UUID{a, b})
	if runs[0] != 1 || runs[1] != 1 {
		t.Fatalf("run counts %v: each automation fires once per chain", runs)
	}
	if got := getTicket(t, e, ws, tk.ID); got.Status != "open" {
		t.Fatalf("status %s", got.Status)
	}
}

func TestRoundRobinAndReplies(t *testing.T) {
	e, ws := newEnv(t)
	e.SetSettings(ws, map[string]any{"auto_assign": "round_robin"})
	pool := []uuid.UUID{ws.Owner.ID, ws.Admin.ID, ws.Agent.ID}
	slices.SortFunc(pool, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	var got []uuid.UUID
	for i := 0; i < 4; i++ {
		tk := createTicket(t, e, ws, map[string]any{"kind": "note"})
		runTriggers(t, e, ws.ID)
		a := getTicket(t, e, ws, tk.ID).Assignee
		if a == nil {
			t.Fatal("not assigned")
		}
		got = append(got, a.ID)
	}
	if !slices.Equal(got, append(pool, pool[0])) {
		t.Fatalf("rotation %v want %v", got, pool)
	}
	// With a team, only its members.
	team := addTeam(t, e, ws, ws.Admin.ID)
	tk := createTicket(t, e, ws, map[string]any{"kind": "note", "team_id": team.String()})
	runTriggers(t, e, ws.ID)
	if a := getTicket(t, e, ws, tk.ID).Assignee; a == nil || a.ID != ws.Admin.ID {
		t.Fatalf("team rotation %+v", a)
	}
	// customer_replied notifies the assignee.
	inbound(t, e, ws, tk.ID, "any update?")
	runTriggers(t, e, ws.ID)
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'customer_replied' AND user_id = $2`, tk.ID, ws.Admin.ID); n != 1 {
		t.Fatalf("customer_replied notifications %d", n)
	}
	// Viewers are never in the pool.
	if slices.Contains(got, ws.Viewer.ID) {
		t.Fatal("viewer assigned")
	}
}

func TestPeriodicJobs(t *testing.T) {
	e, ws := newEnv(t)
	agent := ws.Agent.As(ws)
	snoozed := createTicket(t, e, ws, map[string]any{"status": "pending"})
	e.Do("PATCH", "/v1/tickets/"+snoozed.ID.String(), agent, map[string]any{"snoozed_until": time.Now().Add(time.Hour).Format(time.RFC3339)}).Want(t, 200)
	e.Exec(`UPDATE tickets SET snoozed_until = now() - interval '1 second' WHERE id = $1`, snoozed.ID)
	if err := tickets.WakeSnoozed(e.Ctx, e.D); err != nil {
		t.Fatal(err)
	}
	if got := getTicket(t, e, ws, snoozed.ID); got.Status != "open" || got.SnoozedUntil != nil {
		t.Fatalf("woken %+v", got)
	}

	old := createTicket(t, e, ws, map[string]any{"status": "solved"})
	recent := createTicket(t, e, ws, map[string]any{"status": "solved"})
	e.Exec(`UPDATE tickets SET solved_at = now() - interval '8 days' WHERE id = $1`, old.ID)
	e.Exec(`UPDATE tickets SET solved_at = now() - interval '2 days' WHERE id = $1`, recent.ID)
	if err := tickets.AutoClose(e.Ctx, e.D); err != nil {
		t.Fatal(err)
	}
	if getTicket(t, e, ws, old.ID).Status != "closed" || getTicket(t, e, ws, recent.ID).Status != "solved" {
		t.Fatal("auto-close by default delay")
	}
	e.SetSettings(ws, map[string]any{"auto_close_days": 1})
	if err := tickets.AutoClose(e.Ctx, e.D); err != nil {
		t.Fatal(err)
	}
	if getTicket(t, e, ws, recent.ID).Status != "closed" {
		t.Fatal("auto-close with configured delay")
	}
	never := createTicket(t, e, ws, map[string]any{"status": "solved"})
	e.Exec(`UPDATE tickets SET solved_at = now() - interval '90 days' WHERE id = $1`, never.ID)
	e.SetSettings(ws, map[string]any{"auto_close_days": 0})
	if err := tickets.AutoClose(e.Ctx, e.D); err != nil {
		t.Fatal(err)
	}
	if getTicket(t, e, ws, never.ID).Status != "solved" {
		t.Fatal("auto-close disabled")
	}

	stale, fresh := ids.New(), ids.New()
	key := "workspaces/" + ws.ID.String() + "/attachments/" + stale.String()
	if err := e.D.Storage.Put(e.Ctx, key, strings.NewReader("abc"), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	e.Exec(`INSERT INTO attachments (id, workspace_id, filename, size_bytes, storage_key, created_at) VALUES ($1, $2, 'a', 3, $3, now() - interval '25 hours')`, stale, ws.ID, key)
	e.Exec(`INSERT INTO attachments (id, workspace_id, filename, size_bytes, storage_key) VALUES ($1, $2, 'b', 3, 'x')`, fresh, ws.ID)
	if err := tickets.PurgeAttachments(e.Ctx, e.D); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, e, `SELECT count(*) FROM attachments WHERE id = ANY($1)`, []uuid.UUID{stale, fresh}); n != 1 {
		t.Fatalf("attachments left %d", n)
	}
	if _, err := e.D.Storage.Get(e.Ctx, key); err == nil {
		t.Fatal("object not removed")
	}
}

var csatLink = regexp.MustCompile(`/csat#t=([A-Za-z0-9_-]+)&score=$`)

func TestCSAT(t *testing.T) {
	e, ws := newEnv(t)
	e.SetSettings(ws, map[string]any{"csat_enabled": true, "brand_color": "#0A0A0A"})
	e.Exec(`UPDATE brands SET name = 'Margin Help', color = '#2563EB' WHERE workspace_id = $1`, ws.ID)
	tk := createTicket(t, e, ws, map[string]any{"contact": map[string]any{"email": "rate@example.com"}})
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"status": "solved"}).Want(t, 200)
	mails := e.Jobs(jobs.SendTicketEmailArgs{}.Kind(), ws.ID)
	if len(mails) != 1 {
		t.Fatalf("csat mails %d", len(mails))
	}
	var mail jobs.SendTicketEmailArgs
	_ = json.Unmarshal(mails[0], &mail)
	// The brand's survey email, filled when it goes out (internal/email tests what it reads).
	m := csatLink.FindStringSubmatch(mail.SurveyURL)
	if m == nil || !mail.AutoGenerated || !mail.Template || !strings.HasPrefix(mail.SurveyURL, "http://app.test/csat#t=") ||
		mail.Subject != "How did we do?" || !strings.Contains(mail.HTML, "{{ survey }}") {
		t.Fatalf("mail %+v", mail)
	}
	token := m[1]
	if n := countRows(t, e, `SELECT count(*) FROM tickets WHERE id = $1 AND csat_token_hash IS NOT NULL`, tk.ID); n != 1 {
		t.Fatal("token not stored")
	}
	var stored []byte
	e.Scalar(`SELECT csat_token_hash FROM tickets WHERE id = $1`, &stored, tk.ID)
	if strings.Contains(string(stored), token) {
		t.Fatal("token stored in plain")
	}
	// Re-solving within a day sends nothing new.
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"status": "open"}).Want(t, 200)
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"status": "solved"}).Want(t, 200)
	if n := len(e.Jobs(jobs.SendTicketEmailArgs{}.Kind(), ws.ID)); n != 0 {
		t.Fatalf("duplicate csat mails %d", n)
	}

	var view struct {
		WorkspaceName string `json:"workspace_name"`
		Ticket        struct{ Number int64 }
		Score         *int
		Comment       string
	}
	e.Do("POST", "/v1/public/csat/survey", hdtest.Auth{}, map[string]any{"token": token}).Want(t, 200).JSON(t, &view)
	if view.Score != nil || view.Ticket.Number != tk.Number {
		t.Fatalf("view %+v", view)
	}
	e.Do("POST", "/v1/public/csat/rating", hdtest.Auth{}, map[string]any{"token": token, "score": 6}).Want(t, 422)
	e.Do("POST", "/v1/public/csat/rating", hdtest.Auth{}, map[string]any{"token": token, "score": 4, "comment": "<b>Quick</b> help"}).Want(t, 200).JSON(t, &view)
	if view.Score == nil || *view.Score != 4 || view.Comment != "Quick help" {
		t.Fatalf("answered %+v", view)
	}
	got := getTicket(t, e, ws, tk.ID)
	if got.CSAT == nil || got.CSAT.Score != 4 {
		t.Fatalf("ticket csat %+v", got.CSAT)
	}
	if n := countRows(t, e, `SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'csat_received'`, tk.ID); n != 1 {
		t.Fatalf("csat activities %d", n)
	}
	e.Do("POST", "/v1/public/csat/survey", hdtest.Auth{}, map[string]any{"token": strings.Repeat("x", 43)}).Want(t, 404)
	e.Exec(`UPDATE tickets SET csat_requested_at = now() - interval '31 days' WHERE id = $1`, tk.ID)
	e.Do("POST", "/v1/public/csat/survey", hdtest.Auth{}, map[string]any{"token": token}).Want(t, 404)

	// A brand's own survey email is the one that goes out.
	e.Exec(`INSERT INTO email_templates (workspace_id, brand_id, kind, subject, body_html)
		SELECT workspace_id, id, 'csat', 'Rate us', '<p>Rate {{ team.name }}</p>{{ survey }}' FROM brands WHERE workspace_id = $1`, ws.ID)
	tt := createTicket(t, e, ws, map[string]any{"contact": map[string]any{"email": "team@example.com"}})
	e.Do("PATCH", "/v1/tickets/"+tt.ID.String(), ws.Agent.As(ws), map[string]any{"status": "solved"}).Want(t, 200)
	teamMails := e.Jobs(jobs.SendTicketEmailArgs{}.Kind(), ws.ID)
	if len(teamMails) != 1 {
		t.Fatalf("custom csat mails %d", len(teamMails))
	}
	var teamMail jobs.SendTicketEmailArgs
	_ = json.Unmarshal(teamMails[0], &teamMail)
	if teamMail.Subject != "Rate us" || teamMail.HTML != "<p>Rate {{ team.name }}</p>{{ survey }}" {
		t.Fatalf("custom csat %+v", teamMail)
	}

	e.SetSettings(ws, map[string]any{"csat_enabled": false})
	tk2 := createTicket(t, e, ws, map[string]any{"contact": map[string]any{"email": "quiet@example.com"}})
	e.Do("PATCH", "/v1/tickets/"+tk2.ID.String(), ws.Agent.As(ws), map[string]any{"status": "solved"}).Want(t, 200)
	if n := len(e.Jobs(jobs.SendTicketEmailArgs{}.Kind(), ws.ID)); n != 0 {
		t.Fatal("survey sent while disabled")
	}
}

func TestReportsAndSearch(t *testing.T) {
	e, ws := newEnv(t)
	e.SetSettings(ws, map[string]any{"timezone": "Europe/Budapest"})
	mb := addMailbox(t, e, ws)
	a := createTicket(t, e, ws, map[string]any{"subject": "Shipping delay", "assignee_id": ws.Agent.ID.String(), "kind": "note"})
	b := createTicket(t, e, ws, map[string]any{"subject": "Shipping label", "mailbox_id": mb.String(), "contact": map[string]any{"email": "shipper@example.com", "name": "Sam Shipper"}})
	c := createTicket(t, e, ws, map[string]any{"subject": "Old one", "kind": "note"})
	e.Exec(`UPDATE tickets SET created_at = now() - interval '2 hours', first_responded_at = now() - interval '1 hour', solved_at = now(), status = 'solved', csat_score = 5 WHERE id = $1`, a.ID)
	e.Exec(`UPDATE tickets SET created_at = now() - interval '4 hours', first_responded_at = now() - interval '1 hour' WHERE id = $1`, b.ID)
	e.Exec(`UPDATE tickets SET created_at = now() - interval '60 days' WHERE id = $1`, c.ID)
	e.Exec(`INSERT INTO activities (id, workspace_id, ticket_id, actor_type, type, data) VALUES ($1, $2, $3, 'system', 'sla_breached', '{"kind":"resolution"}')`, ids.New(), ws.ID, b.ID)

	var o tickets.ReportOverview
	e.Do("GET", "/v1/reports/overview", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &o)
	if o.Created != 2 || o.Solved != 1 || o.OpenNow != 2 || o.CSATCount != 1 || o.CSATAvg == nil || *o.CSATAvg != 5 || o.SLABreached != 1 {
		t.Fatalf("overview %+v", o)
	}
	if o.FirstResponseP50Minutes == nil || *o.FirstResponseP50Minutes != 120 || o.ResolutionP50Minutes == nil || *o.ResolutionP50Minutes != 120 {
		t.Fatalf("p50 %v %v", o.FirstResponseP50Minutes, o.ResolutionP50Minutes)
	}
	if len(o.ByDay) < 30 || o.ByChannel["email"] != 1 || o.ByChannel["api"] != 1 || o.ByChannel["chat"] != 0 {
		t.Fatalf("by_day %d by_channel %v", len(o.ByDay), o.ByChannel)
	}
	total := 0
	for _, d := range o.ByDay {
		total += d.Created
	}
	if total != 2 {
		t.Fatalf("by_day created %d", total)
	}
	if len(o.ByAgent) != 1 || o.ByAgent[0].User.ID != ws.Agent.ID || o.ByAgent[0].Solved != 1 {
		t.Fatalf("by_agent %+v", o.ByAgent)
	}
	today := time.Now().In(mustLoc("Europe/Budapest")).Format("2006-01-02")
	e.Do("GET", "/v1/reports/overview?from="+today+"&to="+today, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &o)
	if len(o.ByDay) != 1 || o.ByDay[0].Date != today {
		t.Fatalf("one day %+v", o.ByDay)
	}
	e.Do("GET", "/v1/reports/overview?from=2026-02-01&to=2026-01-01", ws.Viewer.As(ws), nil).Want(t, 422)
	e.Do("GET", "/v1/reports/overview?from=yesterday", ws.Viewer.As(ws), nil).Want(t, 422)

	// Search: five of each kind; another workspace's rows never appear.
	e.Exec(`INSERT INTO kb_articles (id, workspace_id, title, slug, body_html, status) VALUES ($1, $2, 'Shipping times', 'shipping-times', '<p>days</p>', 'published')`, ids.New(), ws.ID)
	other := e.NewWorkspace()
	createTicket(t, e, other, map[string]any{"subject": "Shipping everywhere"})
	var res tickets.SearchResults
	e.Do("GET", "/v1/search?q=shipping", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &res)
	if len(res.Tickets) != 2 || len(res.Contacts) != 0 || len(res.Articles) != 1 {
		t.Fatalf("search %d %d %d", len(res.Tickets), len(res.Contacts), len(res.Articles))
	}
	e.Do("GET", "/v1/search?q=ship", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &res)
	if len(res.Tickets) != 2 || len(res.Contacts) != 1 {
		t.Fatalf("prefix search %d %d", len(res.Tickets), len(res.Contacts))
	}
	key := e.APIKey(ws, auth.ScopeKBRead)
	e.Do("GET", "/v1/search?q=shipping", hdtest.Auth{Bearer: key}, nil).Want(t, 200).JSON(t, &res)
	if len(res.Tickets) != 0 || len(res.Contacts) != 0 || len(res.Articles) != 1 {
		t.Fatalf("scoped search %+v", res)
	}
	e.Do("GET", "/v1/search?q=shipping", hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeRealtime)}, nil).Want(t, 403)
	e.Do("GET", "/v1/search", ws.Agent.As(ws), nil).Want(t, 422)
}

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}
