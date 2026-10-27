package tickets_test

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

func list(t *testing.T, e *hdtest.Env, a hdtest.Auth, query string) httpx.Page[domain.Ticket] {
	t.Helper()
	var page httpx.Page[domain.Ticket]
	e.Do("GET", "/v1/tickets?"+query, a, nil).Want(t, 200).JSON(t, &page)
	return page
}

func idsOf(p httpx.Page[domain.Ticket]) []uuid.UUID {
	out := []uuid.UUID{}
	for _, t := range p.Data {
		out = append(out, t.ID)
	}
	return out
}

func sameSet(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func TestListFilters(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	team := addTeam(t, e, ws, ws.Agent.ID)
	agent := ws.Agent.As(ws)

	t1 := createTicket(t, e, ws, map[string]any{"subject": "Refund for order 1001", "priority": "urgent", "assignee_id": ws.Agent.ID.String(), "tags": []string{"billing"}})
	t2 := createTicket(t, e, ws, map[string]any{"subject": "Password reset", "priority": "low", "status": "pending", "team_id": team.String(),
		"body_html": "<p>The widget sparkles strangely</p>"})
	t3 := createTicket(t, e, ws, map[string]any{"subject": "Invoice question", "mailbox_id": mb.String(), "kind": "note", "priority": "high",
		"assignee_id": ws.Admin.ID.String()})
	t4 := createTicket(t, e, ws, map[string]any{"subject": "Buy cheap pills", "status": "open"})
	e.Do("PATCH", "/v1/tickets/"+t4.ID.String(), agent, map[string]any{"spam": true}).Want(t, 200)
	t5 := createTicket(t, e, ws, map[string]any{"subject": "Thanks!", "status": "solved", "contact": map[string]any{"email": "fan@example.com", "name": "Aysel Məmmədova"}})
	// Another workspace's ticket never appears.
	other := e.NewWorkspace()
	createTicket(t, e, other, map[string]any{"subject": "Refund for order 1001"})

	// t3 was written to an alias of its mailbox.
	e.Exec(`UPDATE tickets SET received_at = 'sales@acme.test' WHERE id = $1`, t3.ID)

	cases := []struct {
		q    string
		want []uuid.UUID
	}{
		{"", []uuid.UUID{t1.ID, t2.ID, t3.ID, t5.ID}},
		{"received_at=Sales%40Acme.test", []uuid.UUID{t3.ID}},
		{"received_at=other%40acme.test", []uuid.UUID{}},
		{"status=open,pending", []uuid.UUID{t1.ID, t2.ID, t3.ID}},
		{"status=solved", []uuid.UUID{t5.ID}},
		{"priority=urgent,high", []uuid.UUID{t1.ID, t3.ID}},
		{"assignee=me", []uuid.UUID{t1.ID}},
		{"assignee=none", []uuid.UUID{t2.ID, t5.ID}},
		{"assignee=" + ws.Admin.ID.String(), []uuid.UUID{t3.ID}},
		{"team_id=" + team.String(), []uuid.UUID{t2.ID}},
		{"mailbox_id=" + mb.String(), []uuid.UUID{t3.ID}},
		{"channel=email", []uuid.UUID{t3.ID}},
		{"channel=api", []uuid.UUID{t1.ID, t2.ID, t5.ID}},
		{"tag=BILLING", []uuid.UUID{t1.ID}},
		{"contact_id=" + t5.Contact.ID.String(), []uuid.UUID{t5.ID}},
		{"spam=true", []uuid.UUID{t4.ID}},
		{"spam=any&status=open", []uuid.UUID{t1.ID, t3.ID, t4.ID}},
		{"q=refund", []uuid.UUID{t1.ID}},
		{"q=refu", []uuid.UUID{t1.ID}},     // prefix on subject
		{"q=sparkles", []uuid.UUID{t2.ID}}, // message body search
		{"q=" + url.QueryEscape(fmt.Sprintf("#%d", t3.Number)), []uuid.UUID{t3.ID}},
		{"q=" + fmt.Sprint(t5.Number), []uuid.UUID{t5.ID}},
		{"q=aysel", []uuid.UUID{t5.ID}},                                 // customer name
		{"q=" + url.QueryEscape("Məmməd"), []uuid.UUID{t5.ID}},          // prefix on customer name
		{"q=" + url.QueryEscape("fan@example.com"), []uuid.UUID{t5.ID}}, // customer email
		{"q=nothingmatches", []uuid.UUID{}},
	}
	for _, c := range cases {
		got := idsOf(list(t, e, agent, c.q))
		if !sameSet(got, c.want) {
			t.Errorf("%q: got %v want %v", c.q, got, c.want)
		}
	}
	for _, bad := range []string{"status=bogus", "priority=extreme", "assignee=nope", "team_id=nope", "sort=random", "spam=maybe", "view_id=" + ids.New().String()} {
		if res := e.Do("GET", "/v1/tickets?"+bad, agent, nil); res.Status != 422 {
			t.Errorf("%s: %d %s", bad, res.Status, res.Body)
		}
	}

	// Sort by priority, desc then asc.
	got := idsOf(list(t, e, agent, "sort=priority"))
	if got[0] != t1.ID || got[1] != t3.ID || got[len(got)-1] != t2.ID {
		t.Fatalf("priority desc %v", got)
	}
	got = idsOf(list(t, e, agent, "sort=priority&order=asc"))
	if got[0] != t2.ID || got[len(got)-1] != t1.ID {
		t.Fatalf("priority asc %v", got)
	}
	// Created order.
	got = idsOf(list(t, e, agent, "sort=created_at&order=asc"))
	if !slices.Equal(got, []uuid.UUID{t1.ID, t2.ID, t3.ID, t5.ID}) {
		t.Fatalf("created asc %v", got)
	}

	// Due sort: tickets with deadlines first, soonest first, the rest last.
	pol := ids.New()
	e.Exec(`INSERT INTO sla_policies (id, workspace_id, name, first_response_minutes, resolution_minutes) VALUES ($1, $2, 'p', 60, 600)`, pol, ws.ID)
	e.Exec(`UPDATE tickets SET sla_policy_id = $2, first_response_due_at = now() + interval '3 hours', first_responded_at = NULL WHERE id = $1`, t2.ID, pol)
	e.Exec(`UPDATE tickets SET sla_policy_id = $2, first_response_due_at = now() + interval '1 hour', first_responded_at = NULL WHERE id = $1`, t3.ID, pol)
	got = idsOf(list(t, e, agent, "sort=due"))
	if got[0] != t3.ID || got[1] != t2.ID {
		t.Fatalf("due %v", got)
	}

	// Cursor pagination walks every ticket once, for each sort.
	for _, sort := range []string{"last_message_at", "created_at", "priority", "due"} {
		for _, order := range []string{"asc", "desc"} {
			var all []uuid.UUID
			cursor := ""
			for i := 0; i < 10; i++ {
				q := "limit=2&sort=" + sort + "&order=" + order + "&spam=any"
				if cursor != "" {
					q += "&cursor=" + url.QueryEscape(cursor)
				}
				page := list(t, e, agent, q)
				all = append(all, idsOf(page)...)
				if page.NextCursor == nil {
					break
				}
				cursor = *page.NextCursor
			}
			if !sameSet(all, []uuid.UUID{t1.ID, t2.ID, t3.ID, t4.ID, t5.ID}) {
				t.Errorf("%s %s pages: %v", sort, order, all)
			}
		}
	}
	e.Do("GET", "/v1/tickets?cursor=garbage", agent, nil).Want(t, 400)

	// Saved views: filters apply, explicit params override, personal views stay personal.
	shared := ids.New()
	e.Exec(`INSERT INTO views (id, workspace_id, name, filters) VALUES ($1, $2, 'Urgent', '{"priority":["urgent","high"]}')`, shared, ws.ID)
	mine := ids.New()
	e.Exec(`INSERT INTO views (id, workspace_id, user_id, name, filters) VALUES ($1, $2, $3, 'Mine', '{"assignee":"me"}')`, mine, ws.ID, ws.Admin.ID)
	if got := idsOf(list(t, e, agent, "view_id="+shared.String())); !sameSet(got, []uuid.UUID{t1.ID, t3.ID}) {
		t.Fatalf("view %v", got)
	}
	if got := idsOf(list(t, e, agent, "view_id="+shared.String()+"&priority=low")); !sameSet(got, []uuid.UUID{t2.ID}) {
		t.Fatalf("view override %v", got)
	}
	e.Do("GET", "/v1/tickets?view_id="+mine.String(), agent, nil).Want(t, 422)
	if got := idsOf(list(t, e, ws.Admin.As(ws), "view_id="+mine.String())); !sameSet(got, []uuid.UUID{t3.ID}) {
		t.Fatalf("personal view %v", got)
	}

	// Counts.
	var c struct {
		Unassigned, Mine, Open, Pending, OnHold, Solved, Spam int
		OnHoldJSON                                            int `json:"on_hold"`
		Views                                                 map[string]int
	}
	e.Do("GET", "/v1/tickets/counts", agent, nil).Want(t, 200).JSON(t, &c)
	if c.Unassigned != 1 || c.Mine != 1 || c.Open != 2 || c.Pending != 1 || c.Solved != 1 || c.Spam != 1 || c.OnHoldJSON != 0 {
		t.Fatalf("counts %+v", c)
	}
	if c.Views[shared.String()] != 2 || len(c.Views) != 1 {
		t.Fatalf("view counts %+v", c.Views)
	}
	e.Do("GET", "/v1/tickets/counts", ws.Admin.As(ws), nil).Want(t, 200).JSON(t, &c)
	if c.Views[mine.String()] != 1 || c.Mine != 1 {
		t.Fatalf("admin counts %+v", c)
	}
}

func TestMessagesAndMentions(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"subject": "Chat"})
	path := "/v1/tickets/" + tk.ID.String() + "/messages"
	agent := ws.Agent.As(ws)

	// Sanitized note with mentions by data attribute, by name and by email; self-mention ignored.
	body := fmt.Sprintf(`<p onclick="x">Hey <span data-mention="%s">@Adam</span>, @olivia owner and @%s please look. @Anna Agent <img src="javascript:alert(1)"><a href="javascript:x">l</a></p>`,
		ws.Admin.ID, ws.Viewer.Email)
	var note domain.Message
	e.Do("POST", path, agent, map[string]any{"kind": "note", "body_html": body}).Want(t, 201).JSON(t, &note)
	if strings.Contains(note.BodyHTML, "onclick") || strings.Contains(note.BodyHTML, "javascript") || !strings.Contains(note.BodyHTML, "data-mention") {
		t.Fatalf("sanitized %s", note.BodyHTML)
	}
	var users []uuid.UUID
	e.Scalar(`SELECT coalesce(array_agg(user_id ORDER BY user_id), '{}') FROM notifications WHERE ticket_id = $1 AND type = 'mentioned'`, &users, tk.ID)
	want := []uuid.UUID{ws.Admin.ID, ws.Owner.ID, ws.Viewer.ID}
	if !sameSet(users, want) {
		t.Fatalf("mentioned %v want %v", users, want)
	}
	var topics []string
	e.Scalar(`SELECT topics FROM events WHERE workspace_id = $1 AND type = 'notification.created' ORDER BY seq DESC LIMIT 1`, &topics, ws.ID)
	// Only the recipient's topic: the workspace channel gets the stand-in.
	if len(topics) != 2 || topics[0] != events.PrivateTopic || !strings.HasPrefix(topics[1], "user:") {
		t.Fatalf("notification topics %v", topics)
	}
	// A reply mentioning someone does not notify (mentions are for notes).
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "<p>@Adam Admin hello</p>"}).Want(t, 201)
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'mentioned'`, tk.ID); n != 3 {
		t.Fatalf("mentions %d", n)
	}

	// Attachments: must be this workspace's, unattached and the caller's own uploads.
	att := ids.New()
	e.Exec(`INSERT INTO attachments (id, workspace_id, filename, size_bytes, storage_key, uploaded_by) VALUES ($1, $2, 'a.txt', 3, 'k', $3)`, att, ws.ID, ws.Agent.ID)
	theirs := ids.New()
	e.Exec(`INSERT INTO attachments (id, workspace_id, filename, size_bytes, storage_key, uploaded_by) VALUES ($1, $2, 'c.txt', 3, 'k', $3)`, theirs, ws.ID, ws.Admin.ID)
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "x", "attachment_ids": []string{theirs.String()}}).Want(t, 422)
	key := e.APIKey(ws, "tickets:write", "tickets:read")
	e.Do("POST", path, hdtest.Auth{Bearer: key}, map[string]any{"kind": "note", "body_html": "x", "attachment_ids": []string{theirs.String()}}).Want(t, 422)
	other := e.NewWorkspace()
	foreign := ids.New()
	e.Exec(`INSERT INTO attachments (id, workspace_id, filename, size_bytes, storage_key) VALUES ($1, $2, 'b.txt', 3, 'k')`, foreign, other.ID)
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "x", "attachment_ids": []string{foreign.String()}}).Want(t, 422)
	var withAtt domain.Message
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "", "attachment_ids": []string{att.String()}, "status": "solved"}).
		Want(t, 201).JSON(t, &withAtt)
	if len(withAtt.Attachments) != 1 || !strings.Contains(withAtt.Attachments[0].URL, "sig=") {
		t.Fatalf("attachments %+v", withAtt.Attachments)
	}
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "again", "attachment_ids": []string{att.String()}}).Want(t, 422)
	if getTicket(t, e, ws, tk.ID).Status != "solved" {
		t.Fatal("status not applied after reply")
	}
	// Validation.
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "<script>x</script>"}).Want(t, 422)
	e.Do("POST", path, agent, map[string]any{"kind": "reply", "body_html": "x", "cc": []string{"a@b.co"}}).Want(t, 422)
	e.Do("POST", path, agent, map[string]any{"kind": "shout", "body_html": "x"}).Want(t, 422)
	e.Do("POST", path, ws.Viewer.As(ws), map[string]any{"kind": "note", "body_html": "x"}).Want(t, 403)

	var msgs struct{ Data []domain.Message }
	e.Do("GET", path, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &msgs)
	if len(msgs.Data) != 4 || msgs.Data[0].Kind != "reply" || msgs.Data[1].Kind != "note" {
		t.Fatalf("messages %d", len(msgs.Data))
	}
	var orig struct {
		FullHTML string `json:"full_html"`
	}
	e.Do("GET", "/v1/messages/"+msgs.Data[0].ID.String()+"/original", agent, nil).Want(t, 200).JSON(t, &orig)
	if orig.FullHTML != msgs.Data[0].BodyHTML {
		t.Fatalf("original %q", orig.FullHTML)
	}
	e.Exec(`UPDATE messages SET full_html = '<p>full</p><blockquote>quoted</blockquote>' WHERE id = $1`, msgs.Data[0].ID)
	e.Do("GET", "/v1/messages/"+msgs.Data[0].ID.String()+"/original", agent, nil).Want(t, 200).JSON(t, &orig)
	if !strings.Contains(orig.FullHTML, "quoted") {
		t.Fatalf("original %q", orig.FullHTML)
	}
	e.Do("GET", "/v1/messages/"+ids.New().String()+"/original", agent, nil).Want(t, 404)

	var acts struct{ Data []domain.Activity }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/activities", agent, nil).Want(t, 200).JSON(t, &acts)
	if len(acts.Data) == 0 || acts.Data[len(acts.Data)-1].Type != "status_changed" {
		t.Fatalf("activities %+v", acts.Data)
	}
	_ = time.Now
}

// Each sidebar row's stats count its own tickets: awaiting a reply, urgent and overdue.
func TestCountStats(t *testing.T) {
	e, ws := newEnv(t)
	team := addTeam(t, e, ws, ws.Agent.ID)
	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{ws.Admin.ID: hd.TeamLead})
	agent := ws.Agent.ID
	type tk struct {
		status, priority        string
		assignee, team          *uuid.UUID
		spam                    bool
		customerLag             *time.Duration // the customer's last message, this long before the last message
		responded               bool
		firstDue, resDue        *time.Duration // from now
		breachedFR, breachedRes bool
	}
	ago := func(d time.Duration) *time.Duration { return &d }
	cases := map[string]tk{
		"t1": {status: "open", priority: "urgent", team: &team, customerLag: ago(0)},
		"t2": {status: "pending", priority: "normal", assignee: &agent, team: &team, firstDue: ago(-time.Hour)},
		"t3": {status: "on_hold", priority: "normal", customerLag: ago(0), breachedRes: true},
		"t4": {status: "open", priority: "urgent", assignee: &agent, customerLag: ago(500 * time.Millisecond), responded: true,
			firstDue: ago(-time.Hour), resDue: ago(time.Hour)},
		"t5": {status: "open", priority: "normal", assignee: &agent, customerLag: ago(5 * time.Second)},
		"t6": {status: "open", priority: "urgent", spam: true, customerLag: ago(0), breachedFR: true},
		"t7": {status: "solved", priority: "urgent", customerLag: ago(0), breachedFR: true, resDue: ago(-time.Hour)},
		"t8": {status: "open", priority: "urgent", team: &priv},
	}
	for name, c := range cases {
		id := createTicket(t, e, ws, map[string]any{"subject": name}).ID
		var lastCustomer *time.Time
		last := time.Now().Add(-time.Minute)
		if c.customerLag != nil {
			v := last.Add(-*c.customerLag)
			lastCustomer = &v
		}
		due := func(d *time.Duration) *time.Time {
			if d == nil {
				return nil
			}
			v := time.Now().Add(*d)
			return &v
		}
		var responded *time.Time
		if c.responded {
			responded = &last
		}
		e.Exec(`UPDATE tickets SET status = $2, priority = $3, assignee_id = $4, team_id = $5, spam = $6, last_message_at = $7,
			last_customer_message_at = $8, first_responded_at = $9, first_response_due_at = $10, resolution_due_at = $11,
			sla_first_response_breached_at = CASE WHEN $12 THEN now() END, sla_resolution_breached_at = CASE WHEN $13 THEN now() END
			WHERE id = $1`, id, c.status, c.priority, c.assignee, c.team, c.spam, last, lastCustomer, responded,
			due(c.firstDue), due(c.resDue), c.breachedFR, c.breachedRes)
	}
	view := ids.New()
	e.Exec(`INSERT INTO views (id, workspace_id, name, filters) VALUES ($1, $2, 'Urgent', '{"priority":["urgent"]}')`, view, ws.ID)

	type stats struct{ Awaiting, Urgent, Overdue int }
	var c struct {
		Views map[string]int
		Teams map[string]struct{ Open, Unassigned int }
		Stats map[string]stats
	}
	e.Do("GET", "/v1/tickets/counts", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &c)
	want := map[string]stats{
		"ticket_folder:unassigned": {1, 1, 1}, // t1, t3 (t8 is in a team the agent cannot see)
		"ticket_folder:mine":       {1, 1, 1}, // t2, t4, t5
		"ticket_folder:open":       {2, 2, 0}, // t1, t4, t5
		"ticket_folder:pending":    {0, 0, 2}, // t2, t3
		"team:" + team.String():    {1, 1, 1}, // t1, t2
		"team:mine":                {1, 1, 1},
		"view:" + view.String():    {2, 3, 0}, // t1, t4, t7 (t8 is in a team the agent cannot see)
	}
	for k, w := range want {
		if got, ok := c.Stats[k]; !ok || got != w {
			t.Errorf("%s: got %+v want %+v", k, got, w)
		}
	}
	if _, ok := c.Stats["team:"+priv.String()]; ok || len(c.Stats) != len(want) {
		t.Errorf("stats rows %v", c.Stats)
	}
	if c.Views[view.String()] != 3 || c.Teams[team.String()].Open != 2 {
		t.Errorf("counts %+v %+v", c.Views, c.Teams)
	}
	// The private team's lead sees its row and its ticket in the view.
	e.Do("GET", "/v1/tickets/counts", ws.Admin.As(ws), nil).Want(t, 200).JSON(t, &c)
	if c.Stats["team:"+priv.String()] != (stats{0, 1, 0}) || c.Stats["view:"+view.String()] != (stats{2, 4, 0}) {
		t.Errorf("admin stats %+v", c.Stats)
	}
}

// A list's summary counts all of it, every page together, with the list's filters and the
// caller's visibility; awaiting follows the inbox row's rule (TicketRow.tsx awaitingReply).
func TestListSummary(t *testing.T) {
	e, ws := newEnv(t)
	team := addTeam(t, e, ws, ws.Agent.ID)
	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{ws.Admin.ID: hd.TeamLead})
	type tk struct {
		status, priority        string
		team                    *uuid.UUID
		spam, deleted           bool
		customerLag             *time.Duration // the customer's last message, this long before the last message
		responded               bool
		firstDue, resDue        *time.Duration // from now
		breachedFR, breachedRes bool
	}
	ago := func(d time.Duration) *time.Duration { return &d }
	cases := map[string]tk{
		"s1":  {status: "open", priority: "urgent", team: &team, customerLag: ago(0)},                         // awaiting, urgent
		"s2":  {status: "pending", priority: "normal", team: &team, customerLag: ago(500 * time.Millisecond)}, // awaiting (within a second)
		"s3":  {status: "open", priority: "normal", customerLag: ago(5 * time.Second)},                        // answered since
		"s4":  {status: "on_hold", priority: "normal", customerLag: ago(0), firstDue: ago(30 * time.Minute)},  // due soon, not awaiting
		"s5":  {status: "open", priority: "normal", firstDue: ago(-time.Hour)},                                // overdue
		"s6":  {status: "open", priority: "normal", responded: true, firstDue: ago(-time.Hour), resDue: ago(2 * time.Hour)},
		"s7":  {status: "pending", priority: "high", responded: true, resDue: ago(10 * time.Minute)},    // due soon
		"s8":  {status: "open", priority: "normal", firstDue: ago(30 * time.Minute), breachedRes: true}, // overdue: a breach is recorded
		"s9":  {status: "solved", priority: "urgent", customerLag: ago(0), firstDue: ago(-time.Hour)},   // urgent only
		"s10": {status: "open", priority: "urgent", spam: true, customerLag: ago(0)},
		"s11": {status: "open", priority: "urgent", team: &priv, customerLag: ago(0)},
		"s12": {status: "open", priority: "urgent", deleted: true, customerLag: ago(0)},
	}
	for name, c := range cases {
		id := createTicket(t, e, ws, map[string]any{"subject": name}).ID
		last := time.Now().Add(-time.Minute)
		var lastCustomer, responded *time.Time
		if c.customerLag != nil {
			v := last.Add(-*c.customerLag)
			lastCustomer = &v
		}
		if c.responded {
			responded = &last
		}
		due := func(d *time.Duration) *time.Time {
			if d == nil {
				return nil
			}
			v := time.Now().Add(*d)
			return &v
		}
		e.Exec(`UPDATE tickets SET status = $2, priority = $3, team_id = $4, spam = $5, last_message_at = $6,
			last_customer_message_at = $7, first_responded_at = $8, first_response_due_at = $9, resolution_due_at = $10,
			sla_first_response_breached_at = CASE WHEN $11 THEN now() END, sla_resolution_breached_at = CASE WHEN $12 THEN now() END,
			deleted_at = CASE WHEN $13 THEN now() END
			WHERE id = $1`, id, c.status, c.priority, c.team, c.spam, last, lastCustomer, responded,
			due(c.firstDue), due(c.resDue), c.breachedFR, c.breachedRes, c.deleted)
	}
	view := ids.New()
	e.Exec(`INSERT INTO views (id, workspace_id, name, filters) VALUES ($1, $2, 'Open', '{"status":["open"]}')`, view, ws.ID)

	// The web's awaitingReply, over the tickets the list returns.
	awaiting := func(tk domain.Ticket) bool {
		return (tk.Status == "open" || tk.Status == "pending") && tk.LastCustomerMessageAt != nil &&
			!tk.LastCustomerMessageAt.Before(tk.LastMessageAt.Add(-time.Second))
	}
	type counts struct{ Total, Awaiting, Urgent, Overdue, DueSoon int }
	summary := func(a hdtest.Auth, query string) tickets.TicketListSummary {
		t.Helper()
		var s tickets.TicketListSummary
		e.Do("GET", "/v1/tickets/summary?"+query, a, nil).Want(t, 200).JSON(t, &s)
		return s
	}
	agent, lead := ws.Agent.As(ws), ws.Admin.As(ws)
	for _, c := range []struct {
		who   string
		as    hdtest.Auth
		query string
		want  counts
	}{
		{"agent", agent, "", counts{9, 2, 2, 2, 2}},                         // s1..s9: s11 is a private team's
		{"lead", lead, "", counts{10, 3, 3, 2, 2}},                          // and s11
		{"viewer", ws.Viewer.As(ws), "", counts{9, 2, 2, 2, 2}},             // reads like the agent
		{"agent", agent, "status=open,pending", counts{7, 2, 1, 2, 1}},      // s1 s2 s3 s5 s6 s7 s8
		{"agent", agent, "priority=urgent", counts{2, 1, 2, 0, 0}},          // s1 s9
		{"agent", agent, "team_id=" + team.String(), counts{2, 2, 1, 0, 0}}, // s1 s2
		{"agent", agent, "view_id=" + view.String(), counts{5, 1, 1, 2, 0}}, // s1 s3 s5 s6 s8
		{"agent", agent, "view_id=" + view.String() + "&priority=urgent", counts{1, 1, 1, 0, 0}},
		{"agent", agent, "spam=true", counts{1, 1, 1, 0, 0}},                  // s10
		{"agent", agent, "sort=due&order=asc&limit=1", counts{9, 2, 2, 2, 2}}, // pagination and sort change nothing
	} {
		s := summary(c.as, c.query)
		if got := (counts{s.Total, s.Awaiting, s.Urgent, s.Overdue, s.DueSoon}); !s.Complete || got != c.want {
			t.Errorf("%s %q: got %+v complete %v, want %+v", c.who, c.query, got, s.Complete, c.want)
		}
		// The same tickets as the list, and awaiting as the list's rows show it.
		q, _ := url.ParseQuery(c.query)
		q.Del("sort")
		q.Del("order")
		q.Set("limit", "100")
		page := list(t, e, c.as, q.Encode())
		n := 0
		for _, tk := range page.Data {
			if awaiting(tk) {
				n++
			}
		}
		if len(page.Data) != s.Total || n != s.Awaiting {
			t.Errorf("%s %q: list has %d tickets, %d awaiting; summary %+v", c.who, c.query, len(page.Data), n, s)
		}
	}

	// A full-text search is not counted.
	if s := summary(agent, "q=s1"); s.Complete || s.Total != 0 {
		t.Errorf("search summary %+v", s)
	}
	// Past the cap the list is not counted; a shorter one still is.
	defer tickets.SetSummaryCap(3)()
	if s := summary(agent, ""); s.Complete || s.Total != 0 || s.Awaiting != 0 {
		t.Errorf("capped summary %+v", s)
	}
	if s := summary(agent, "status=solved"); !s.Complete || s.Total != 1 || s.Urgent != 1 {
		t.Errorf("solved summary %+v", s)
	}
	// The list's filter errors are the summary's.
	e.Do("GET", "/v1/tickets/summary?status=nope", agent, nil).Want(t, 422)
	e.Do("GET", "/v1/tickets/summary?view_id="+ids.New().String(), agent, nil).Want(t, 422)
}

// Folder counts cover every status: the open folders, Solved and Spam each count the caller's
// tickets, never a closed or deleted one, and a private team's only for its members and the
// ticket's assignee.
func TestFolderCounts(t *testing.T) {
	e, ws := newEnv(t)
	team := addTeam(t, e, ws, ws.Agent.ID)
	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{ws.Admin.ID: hd.TeamLead})
	agent, admin := ws.Agent.ID, ws.Admin.ID
	type tk struct {
		name                  string
		status                string
		spam, urgent, deleted bool
		assignee, team        *uuid.UUID
	}
	for _, c := range []tk{
		{name: "t1", status: "open", urgent: true},
		{name: "t2", status: "pending", assignee: &agent},
		{name: "t3", status: "on_hold", urgent: true},
		{name: "t4", status: "open", team: &team},
		{name: "t5", status: "pending", assignee: &admin, team: &team},
		{name: "t6", status: "solved", urgent: true},
		{name: "t7", status: "solved", assignee: &agent},
		{name: "t8", status: "closed", urgent: true},
		{name: "t9", status: "closed", spam: true},
		{name: "t10", status: "open", spam: true, urgent: true},
		{name: "t11", status: "on_hold", spam: true},
		{name: "t12", status: "solved", spam: true, assignee: &agent},
		{name: "t13", status: "open", urgent: true, team: &priv},
		{name: "t14", status: "solved", team: &priv},
		{name: "t15", status: "solved", spam: true, team: &priv},
		{name: "t16", status: "pending", urgent: true, assignee: &agent, team: &priv},
		{name: "t17", status: "solved", assignee: &agent, team: &priv},
		{name: "t18", status: "open", spam: true, assignee: &agent, team: &priv},
		{name: "t19", status: "open", deleted: true},
		{name: "t20", status: "solved", deleted: true},
		{name: "t21", status: "open", spam: true, deleted: true},
	} {
		id := createTicket(t, e, ws, map[string]any{"subject": c.name}).ID
		priority := "normal"
		if c.urgent {
			priority = "urgent"
		}
		e.Exec(`UPDATE tickets SET status = $2, spam = $3, priority = $4, assignee_id = $5, team_id = $6,
			deleted_at = CASE WHEN $7 THEN now() END WHERE id = $1`, id, c.status, c.spam, priority, c.assignee, c.team, c.deleted)
	}

	type folders struct{ Unassigned, Mine, Open, Pending, OnHold, Solved, Spam, MyTeams, MyTeamsUnassigned int }
	type urgent struct{ Unassigned, Mine, Open, Pending, MyTeams int }
	cases := []struct {
		who     string
		as      hdtest.Auth
		folders folders
		urgent  urgent
	}{
		// The agent: t13, t14 and t15 are a private team's; t16, t17 and t18 are theirs.
		{"agent", ws.Agent.As(ws),
			folders{
				Unassigned: 3, Mine: 2, Open: 2, Pending: 3, OnHold: 1, // t1 t3 t4; t2 t16; t1 t4; t2 t5 t16; t3
				Solved: 3, Spam: 4, // t6 t7 t17; t10 t11 t12 t18
				MyTeams: 2, MyTeamsUnassigned: 1, // t4 t5; t4
			},
			urgent{Unassigned: 2, Mine: 1, Open: 1, Pending: 2}}, // t1 t3; t16; t1; t3 t16
		// The private team's lead counts its tickets too.
		{"lead", ws.Admin.As(ws),
			folders{
				Unassigned: 4, Mine: 1, Open: 3, Pending: 3, OnHold: 1, // t1 t3 t4 t13; t5; t1 t4 t13; t2 t5 t16; t3
				Solved: 4, Spam: 5, // t6 t7 t14 t17; t10 t11 t12 t15 t18
				MyTeams: 2, MyTeamsUnassigned: 1, // t13 t16; t13
			},
			urgent{Unassigned: 3, Open: 2, Pending: 2, MyTeams: 2}}, // t1 t3 t13; t1 t13; t3 t16; t13 t16
	}
	for _, c := range cases {
		var got tickets.TicketCounts
		e.Do("GET", "/v1/tickets/counts", c.as, nil).Want(t, 200).JSON(t, &got)
		if f := (folders{got.Unassigned, got.Mine, got.Open, got.Pending, got.OnHold, got.Solved, got.Spam,
			got.MyTeams.Open, got.MyTeams.Unassigned}); f != c.folders {
			t.Errorf("%s folders: got %+v want %+v", c.who, f, c.folders)
		}
		if u := (urgent{got.Stats["ticket_folder:unassigned"].Urgent, got.Stats["ticket_folder:mine"].Urgent,
			got.Stats["ticket_folder:open"].Urgent, got.Stats["ticket_folder:pending"].Urgent, got.Stats["team:mine"].Urgent}); u != c.urgent {
			t.Errorf("%s urgent: got %+v want %+v", c.who, u, c.urgent)
		}
	}
}
