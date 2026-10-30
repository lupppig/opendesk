package supportchannels

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func TestDestinationRoutingAPI(t *testing.T) {
	e := hdtest.New(t, Register)
	ws := e.NewWorkspace()
	connectionID := ids.New()
	credentials, err := e.D.Box.SealJSON(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(`INSERT INTO support_channel_connections(id,workspace_id,provider,name,external_account_id,credentials_enc)
		VALUES($1,$2,'slack','Support','T1',$3)`, connectionID, ws.ID, credentials)
	path := "/v1/support-channels/destinations"
	create := func(body map[string]any, want int) destination {
		t.Helper()
		body["connection_id"] = connectionID.String()
		var d destination
		res := e.Do(http.MethodPost, path, ws.Admin.As(ws), body).Want(t, want)
		if want == http.StatusCreated {
			res.JSON(t, &d)
		}
		return d
	}

	plain := create(map[string]any{"external_channel_id": "C1"}, http.StatusCreated)
	if plain.Mode != modeRoute || len(plain.Conditions) != 0 || !slices.Equal(plain.Mirror, defaultMirror) {
		t.Fatalf("defaults = %+v", plain)
	}
	vip := []map[string]any{{"field": "tag", "op": "equals", "value": "vip"}}
	create(map[string]any{"external_channel_id": "C2", "conditions": vip}, http.StatusCreated)
	// The same team, priority and conditions again could never post.
	create(map[string]any{"external_channel_id": "C3", "conditions": vip}, http.StatusConflict)
	// A copy may repeat them: it posts alongside the route.
	escalation := create(map[string]any{"external_channel_id": "C4", "mode": "copy", "priority": "urgent",
		"conditions": []map[string]any{{"field": "time", "op": "equals", "value": "outside_business_hours"}},
		"mirror":     []string{"priority_changes", "customer_replies", "priority_changes"}}, http.StatusCreated)
	if escalation.Mode != modeCopy || !slices.Equal(escalation.Mirror, []string{mirrorCustomerReplies, mirrorPriority}) {
		t.Fatalf("escalation = %+v", escalation)
	}
	for _, bad := range []map[string]any{
		{"external_channel_id": "C5", "mode": "both"},
		{"external_channel_id": "C5", "mirror": []string{"everything"}},
		{"external_channel_id": "C5", "conditions": []map[string]any{{"field": "ai_decide", "op": "gt", "value": "0.5", "question": "Is it?"}}},
		{"external_channel_id": "C5", "conditions": []map[string]any{{"field": "time", "op": "contains", "value": "business"}}},
	} {
		e.Do(http.MethodPost, path, ws.Admin.As(ws), withConnection(bad, connectionID)).Want(t, http.StatusUnprocessableEntity)
	}
	// A mailbox of another workspace, or none at all, is refused as automations refuse it.
	e.Do(http.MethodPost, path, ws.Admin.As(ws), withConnection(map[string]any{"external_channel_id": "C5",
		"conditions": []map[string]any{{"field": "mailbox_id", "op": "equals", "value": ids.New().String()}}}, connectionID)).Want(t, http.StatusUnprocessableEntity)

	// Turning the copy into a route that repeats the plain route is refused.
	e.Do(http.MethodPatch, path+"/"+escalation.ID.String(), ws.Admin.As(ws), map[string]any{
		"mode": "route", "conditions": []map[string]any{},
	}).Want(t, http.StatusOK)
	e.Do(http.MethodPatch, path+"/"+plain.ID.String(), ws.Admin.As(ws), map[string]any{
		"conditions": vip,
	}).Want(t, http.StatusConflict)
	var patched destination
	e.Do(http.MethodPatch, path+"/"+plain.ID.String(), ws.Admin.As(ws), map[string]any{
		"mirror": []string{"status_changes"}, "include_notes": true,
	}).Want(t, http.StatusOK).JSON(t, &patched)
	if !patched.IncludeNotes || !slices.Equal(patched.Mirror, []string{mirrorStatus}) {
		t.Fatalf("patched = %+v", patched)
	}
}

func withConnection(body map[string]any, id uuid.UUID) map[string]any {
	body["connection_id"] = id.String()
	return body
}

// routingTicket creates an email ticket and returns it with its ticket.created event.
func routingTicket(t *testing.T, w *workflow, nt domain.NewTicket) (domain.Ticket, int64) {
	t.Helper()
	nt.WorkspaceID, nt.Channel = w.ws.ID, domain.ChannelEmail
	if nt.Subject == "" {
		nt.Subject = "Routing"
	}
	var ticket domain.Ticket
	if err := w.e.D.DB.Tx(w.e.Ctx, func(tx pgx.Tx) error {
		var err error
		ticket, err = domain.CreateTicket(w.e.Ctx, tx, nt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var seq int64
	w.e.Scalar(`SELECT seq FROM events WHERE workspace_id=$1 AND type='ticket.created' AND data->'ticket'->>'id'=$2`, &seq, w.ws.ID, ticket.ID.String())
	return ticket, seq
}

func (w *workflow) dispatchSeq(t *testing.T, seq int64) []string {
	t.Helper()
	before := len(w.fake.channels)
	if err := w.s.dispatch(w.e.Ctx, jobs.SupportChannelDispatchArgs{WorkspaceID: w.ws.ID, EventSeq: seq}); err != nil {
		t.Fatal(err)
	}
	return slices.Clone(w.fake.channels[before:])
}

func (w *workflow) addDestination(t *testing.T, channelID, mode string, priority *string, conditions []hd.Condition, mirror []string) uuid.UUID {
	t.Helper()
	if conditions == nil {
		conditions = []hd.Condition{}
	}
	if mirror == nil {
		mirror = defaultMirror
	}
	id := ids.New()
	w.e.Exec(`INSERT INTO support_channel_destinations(id,workspace_id,connection_id,priority,external_channel_id,mode,conditions,mirror)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, w.ws.ID, w.c.ID, priority, channelID, mode, conditions, mirror)
	return id
}

func TestCopiesPostAlongsideTheRouteAndFollowTheirThread(t *testing.T) {
	w := newWorkflow(t)
	urgent := "urgent"
	w.addDestination(t, "escalations", modeCopy, &urgent, nil, nil)
	w.addDestination(t, "vip", modeRoute, nil, []hd.Condition{{Field: "tag", Op: "equals", Value: "vip"}}, nil)

	_, seq := routingTicket(t, w, domain.NewTicket{Priority: "normal"})
	if got := w.dispatchSeq(t, seq); !slices.Equal(got, []string{"C1"}) {
		t.Fatalf("normal ticket posted to %v, want the fallback only", got)
	}

	ticket, seq := routingTicket(t, w, domain.NewTicket{Priority: urgent})
	got := w.dispatchSeq(t, seq)
	slices.Sort(got)
	if !slices.Equal(got, []string{"C1", "escalations"}) {
		t.Fatalf("urgent ticket posted to %v, want the fallback and the escalation copy", got)
	}
	// Once its thread exists, the copy keeps following the ticket below urgent.
	w.e.Exec(`UPDATE tickets SET priority='normal' WHERE id=$1`, ticket.ID)
	w.e.Exec(`DELETE FROM support_channel_deliveries WHERE event_seq=$1`, seq)
	if got := w.dispatchSeq(t, seq); len(got) != 0 {
		t.Fatalf("existing threads created %v again", got)
	}
	var delivered int
	w.e.Scalar(`SELECT count(*) FROM support_channel_deliveries WHERE event_seq=$1 AND delivered_at IS NOT NULL`, &delivered, seq)
	if delivered != 2 {
		t.Fatalf("delivered to %d destinations, want both threads", delivered)
	}

	tag := ids.New()
	w.e.Exec(`INSERT INTO tags(id,workspace_id,name) VALUES($1,$2,'VIP')`, tag, w.ws.ID)
	ticket, vipSeq := routingTicket(t, w, domain.NewTicket{Priority: "low"})
	w.e.Exec(`INSERT INTO ticket_tags(ticket_id,tag_id) VALUES($1,$2)`, ticket.ID, tag)
	if got := w.dispatchSeq(t, vipSeq); !slices.Equal(got, []string{"vip"}) {
		t.Fatalf("tagged ticket posted to %v, want the vip route ahead of the fallback", got)
	}
}

func TestBusinessHoursChooseTheRoute(t *testing.T) {
	w := newWorkflow(t)
	team, always := ids.New(), ids.New()
	// Enabled hours with no open time are never open; hours that are off always are.
	w.e.Exec(`INSERT INTO teams(id,workspace_id,name,business_hours) VALUES($1,$3,'Night','{"enabled":true,"schedule":{}}'),($2,$3,'Always','{"enabled":false,"schedule":{}}')`, team, always, w.ws.ID)
	w.addDestination(t, "after-hours", modeRoute, nil, []hd.Condition{{Field: timeField, Op: "equals", Value: "outside_business_hours"}}, nil)

	_, seq := routingTicket(t, w, domain.NewTicket{TeamID: &team})
	if got := w.dispatchSeq(t, seq); !slices.Equal(got, []string{"after-hours"}) {
		t.Fatalf("closed team posted to %v, want after-hours", got)
	}
	_, seq = routingTicket(t, w, domain.NewTicket{TeamID: &always})
	if got := w.dispatchSeq(t, seq); !slices.Equal(got, []string{"C1"}) {
		t.Fatalf("team without hours posted to %v, want the fallback", got)
	}
}

func TestMirrorChoosesWhatAThreadShows(t *testing.T) {
	w := newWorkflow(t)
	w.e.Exec(`UPDATE support_channel_destinations SET mirror='{customer_replies,status_changes}' WHERE connection_id=$1`, w.c.ID)

	customer, _, err := messageEvent([]byte(`{"message":{"id":"`+ids.New().String()+`","kind":"reply","direction":"inbound","body_text":"Help"}}`), shown{customer: true}, 3500, "")
	if err != nil || customer.Text == "" {
		t.Fatalf("customer reply = %+v, %v", customer, err)
	}
	_, source, err := messageEvent([]byte(`{"message":{"id":"`+ids.New().String()+`","kind":"reply","direction":"outbound","body_text":"On it"}}`), shown{customer: true}, 3500, "")
	if err != nil || source != nil {
		t.Fatalf("agent reply mirrored without agent_replies: %v, %v", source, err)
	}

	if err := w.e.D.DB.Tx(w.e.Ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(w.e.Ctx, `UPDATE tickets SET status='solved',priority='high' WHERE id=$1`, w.ticket.ID); err != nil {
			return err
		}
		return domain.PublishTicketUpdated(w.e.Ctx, tx, w.ws.ID, w.ticket.ID, []string{"status", "priority"})
	}); err != nil {
		t.Fatal(err)
	}
	var seq int64
	w.e.Scalar(`SELECT max(seq) FROM events WHERE workspace_id=$1 AND type='ticket.updated'`, &seq, w.ws.ID)
	posts := len(w.fake.posts)
	w.dispatchSeq(t, seq)
	if len(w.fake.posts) != posts+1 || w.fake.posts[posts] != "Ticket updated: status is solved." {
		t.Fatalf("activity posts = %q", w.fake.posts[posts:])
	}
}

func TestActivityText(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"ticket":  map[string]any{"status": "on_hold", "priority": "urgent", "assignee": map[string]any{"name": "Ada"}},
		"changes": []string{"status", "priority", "assignee_id", "tags"},
	})
	for _, tc := range []struct {
		mirror []string
		want   string
	}{
		{nil, ""},
		{[]string{mirrorCustomerReplies}, ""},
		{[]string{mirrorStatus}, "Ticket updated: status is on hold."},
		{mirrorOptions, "Ticket updated: status is on hold, priority is urgent, assigned to Ada."},
	} {
		got, err := activityText(data, tc.mirror)
		if err != nil || got != tc.want {
			t.Errorf("activityText(%v) = %q, %v; want %q", tc.mirror, got, err, tc.want)
		}
	}
	unassigned, _ := activityText([]byte(`{"ticket":{},"changes":["assignee_id"]}`), []string{mirrorAssignee})
	if !strings.HasSuffix(unassigned, "unassigned.") {
		t.Errorf("unassigned = %q", unassigned)
	}
}

func TestPickTargetsKeepsOneRoutePerConnection(t *testing.T) {
	team := ids.New()
	high := "high"
	a, b := ids.New(), ids.New()
	route := func(conn uuid.UUID, mod func(*routeCandidate)) routeCandidate {
		c := routeCandidate{deliveryTarget: deliveryTarget{destination: destination{ID: ids.New(), ConnectionID: conn, Mode: modeRoute}}}
		if mod != nil {
			mod(&c)
		}
		return c
	}
	fallback := route(a, nil)
	withTeam := route(a, func(c *routeCandidate) { c.TeamID = &team })
	existing := route(a, func(c *routeCandidate) { c.existing = true })
	priority := route(a, func(c *routeCandidate) { c.Priority = &high })
	conditional := route(a, func(c *routeCandidate) { c.Conditions = []hd.Condition{{Field: "tag"}} })
	copied := route(a, func(c *routeCandidate) { c.Mode = modeCopy })
	other := route(b, nil)

	for _, tc := range []struct {
		name string
		in   []routeCandidate
		want []uuid.UUID
	}{
		{"team beats an existing thread", []routeCandidate{existing, withTeam}, []uuid.UUID{withTeam.ID}},
		{"existing thread beats priority", []routeCandidate{priority, existing}, []uuid.UUID{existing.ID}},
		{"priority beats conditions", []routeCandidate{conditional, priority}, []uuid.UUID{priority.ID}},
		{"conditions beat the fallback", []routeCandidate{fallback, conditional}, []uuid.UUID{conditional.ID}},
		{"copies and other connections post too", []routeCandidate{fallback, copied, other}, []uuid.UUID{copied.ID, fallback.ID, other.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []uuid.UUID
			for _, d := range pickTargets(tc.in) {
				got = append(got, d.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
