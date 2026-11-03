package phone_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/resources"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/phone"
	"github.com/useopendesk/opendesk/internal/phone/provider/aircall"
	"github.com/useopendesk/opendesk/internal/phone/provider/aircall/fake"
)

func TestMain(m *testing.M) { hdtest.Main(m) }

var creds = map[string]string{"api_id": "id-1", "api_token": "token-1"}

type harness struct {
	e    *hdtest.Env
	ws   hdtest.WS
	s    *phone.Service
	f    *fake.Aircall
	conn phone.PhoneConnectionView
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := fake.New(t, creds["api_id"], creds["api_token"])
	var s *phone.Service
	e := hdtest.New(t, tickets.Register, resources.Register, func(d *deps.Deps, api huma.API, r chi.Router) {
		s = phone.New(d, aircall.Adapter{BaseURL: f.URL()})
		s.Register(api, r)
	})
	ws := e.NewWorkspace()
	// Open 09:00 to 17:00 in Brussels on weekdays.
	day := []map[string]string{{"start": "09:00", "end": "17:00"}}
	e.SetSettings(ws, map[string]any{"timezone": "Europe/Brussels", "business_hours": map[string]any{"enabled": true, "holidays": []any{},
		"schedule": map[string]any{"mon": day, "tue": day, "wed": day, "thu": day, "fri": day, "sat": []any{}, "sun": []any{}}}})
	h := &harness{e: e, ws: ws, s: s, f: f}
	e.Do("POST", "/v1/phone/connections", ws.Admin.As(ws), map[string]any{"provider": "aircall", "credentials": creds}).
		Want(t, 201).JSON(t, &h.conn)
	return h
}

// token is the secret Aircall puts in our webhook's events.
func (h *harness) token(t *testing.T) string {
	t.Helper()
	for _, w := range h.f.Webhooks() {
		if strings.Contains(w.URL, h.conn.ID.String()) {
			return w.Token
		}
	}
	t.Fatal("no webhook registered for the connection")
	return ""
}

// fixtureBase is the earliest time in the fixtures (Tuesday 27 October 2026, 10:00 UTC).
const fixtureBase = 1793095200

// recent moves the fixtures' times by whole weeks to within the last seven days, where the
// webhook takes events, keeping their weekday and time of day.
func recent(t *testing.T, body []byte) []byte {
	t.Helper()
	week := int64(7 * 24 * 3600)
	shift := (time.Now().Unix() - fixtureBase) / week * week
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(x any) {
		switch m := x.(type) {
		case map[string]any:
			for k, val := range m {
				if n, ok := val.(json.Number); ok && slices.Contains([]string{"timestamp", "started_at", "answered_at", "ended_at", "posted_at"}, k) {
					if i, err := n.Int64(); err == nil && i > 0 {
						m[k] = i + shift
					}
					continue
				}
				walk(val)
			}
		case []any:
			for _, e := range m {
				walk(e)
			}
		}
	}
	walk(v)
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// recorded reads a lifecycle fixture, signed with our token, with the agent's email set to
// the workspace agent's and its times moved to this week.
func (h *harness) recorded(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("provider/aircall/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	tok := h.token(t)
	out := make([][]byte, len(list))
	for i, m := range list {
		s := strings.ReplaceAll(string(m), `"hook-token-1"`, `"`+tok+`"`)
		out[i] = recent(t, []byte(strings.ReplaceAll(s, "anna@example.com", h.ws.Agent.Email)))
	}
	return out
}

func (h *harness) send(t *testing.T, body []byte) int {
	t.Helper()
	res, err := http.Post(h.e.Srv.URL+"/v1/phone/hooks/aircall/"+h.conn.ID.String(), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// drain runs the queued phone events, insight cards and ticket triggers until none are left.
func (h *harness) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		n := 0
		for _, raw := range h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID) {
			var a jobs.PhoneInboundArgs
			_ = json.Unmarshal(raw, &a)
			if err := h.s.ProcessInbound(h.e.Ctx, a); err != nil {
				t.Fatalf("inbound: %v", err)
			}
			n++
		}
		for _, raw := range h.e.Jobs(jobs.PhoneInsightCardArgs{}.Kind(), h.ws.ID) {
			var a jobs.PhoneInsightCardArgs
			_ = json.Unmarshal(raw, &a)
			if err := h.s.PushCard(h.e.Ctx, a); err != nil {
				t.Fatalf("card: %v", err)
			}
			n++
		}
		for _, raw := range h.e.Jobs(jobs.TicketTriggerArgs{}.Kind(), h.ws.ID) {
			var a jobs.TicketTriggerArgs
			_ = json.Unmarshal(raw, &a)
			if err := tickets.RunTrigger(h.e.Ctx, h.e.D, a); err != nil {
				t.Fatalf("trigger %s: %v", a.Trigger, err)
			}
			n++
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("jobs did not settle")
}

func (h *harness) sendAll(t *testing.T, bodies [][]byte) {
	t.Helper()
	for _, b := range bodies {
		if got := h.send(t, b); got != 200 {
			t.Fatalf("webhook answered %d", got)
		}
		h.drain(t)
	}
}

func (h *harness) callEntries(t *testing.T, ticketID uuid.UUID) []domain.Activity {
	t.Helper()
	var out struct {
		Data []domain.Activity `json:"data"`
	}
	h.e.Do("GET", "/v1/tickets/"+ticketID.String()+"/activities", h.ws.Agent.As(h.ws), nil).Want(t, 200).JSON(t, &out)
	var calls []domain.Activity
	for _, a := range out.Data {
		if a.Type == phone.ActivityCall {
			calls = append(calls, a)
		}
	}
	return calls
}

func (h *harness) ticketOfCall(t *testing.T, external string) domain.Ticket {
	t.Helper()
	var tid uuid.UUID
	h.e.Scalar(`SELECT ticket_id FROM phone_calls WHERE workspace_id = $1 AND external_call_id = $2`, &tid, h.ws.ID, external)
	tk, err := domain.LoadTicket(h.e.Ctx, h.e.D.DB.Pool, h.ws.ID, tid)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestConnectManagesOnlyItsOwnWebhook(t *testing.T) {
	f := fake.New(t, creds["api_id"], creds["api_token"])
	f.AddWebhook("gorgias-1", "https://gorgias.example.com/aircall", "theirs")
	var s *phone.Service
	e := hdtest.New(t, func(d *deps.Deps, api huma.API, r chi.Router) {
		s = phone.New(d, aircall.Adapter{BaseURL: f.URL()})
		s.Register(api, r)
	})
	_ = s
	ws := e.NewWorkspace()
	body := map[string]any{"provider": "aircall", "credentials": creds}

	e.Do("POST", "/v1/phone/connections", ws.Agent.As(ws), body).Want(t, 403)
	e.Do("POST", "/v1/phone/connections", ws.Admin.As(ws), map[string]any{"provider": "aircall",
		"credentials": map[string]string{"api_id": "id-1", "api_token": "wrong"}}).Want(t, 422)

	var c phone.PhoneConnectionView
	r := e.Do("POST", "/v1/phone/connections", ws.Admin.As(ws), body).Want(t, 201)
	r.JSON(t, &c)
	if c.Name != "LampTwist" || c.HealthStatus != "healthy" || c.HealthCode != phone.HealthReceiving || c.ReuseDays != 7 || !c.InsightCards {
		t.Fatalf("connection %+v", c)
	}
	if strings.Contains(string(r.Body), "token-1") || !strings.HasSuffix(c.WebhookURL, "/v1/phone/hooks/aircall/"+c.ID.String()) {
		t.Fatalf("credentials or webhook URL: %s", r.Body)
	}
	e.Do("POST", "/v1/phone/connections", ws.Admin.As(ws), body).Want(t, 409)
	var list struct {
		Data []phone.PhoneConnectionView `json:"data"`
	}
	e.Do("GET", "/v1/phone/connections", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 1 {
		t.Fatalf("list %+v", list)
	}
	// The webhook URL reaches the connection: admins only, never a viewer or a key that
	// can't change settings.
	for name, a := range map[string]hdtest.Auth{
		"viewer":        ws.Viewer.As(ws),
		"agent":         ws.Agent.As(ws),
		"read-only key": {Bearer: e.APIKey(ws, auth.ScopeSettingsRead), Workspace: ws.ID},
		"admin":         ws.Admin.As(ws),
		"settings key":  {Bearer: e.APIKey(ws, auth.ScopeSettingsRead, auth.ScopeSettingsWrite), Workspace: ws.ID},
	} {
		r := e.Do("GET", "/v1/phone/connections", a, nil).Want(t, 200)
		admin := name == "admin" || name == "settings key"
		if has := strings.Contains(string(r.Body), "/v1/phone/hooks/"); has != admin {
			t.Fatalf("%s: webhook URL in the list = %v: %s", name, has, r.Body)
		}
	}
	ours := func() []fake.Webhook {
		var out []fake.Webhook
		for _, w := range f.Webhooks() {
			if w.ID != "gorgias-1" {
				out = append(out, w)
			}
		}
		return out
	}
	if len(ours()) != 1 || f.Webhook("gorgias-1") == nil {
		t.Fatalf("webhooks %+v", f.Webhooks())
	}
	first := ours()[0].ID

	// Aircall disabled ours after failed deliveries: a test registers a new one.
	f.Disable(first)
	e.Do("POST", "/v1/phone/connections/"+c.ID.String()+"/test", ws.Admin.As(ws), nil).Want(t, 200).JSON(t, &c)
	if now := ours(); c.HealthStatus != "healthy" || len(now) != 1 || now[0].ID == first || !now[0].Active {
		t.Fatalf("repair %+v %+v", c, now)
	}
	var lines struct {
		Data []phone.PhoneLine `json:"data"`
	}
	e.Do("GET", "/v1/phone/lines", ws.Admin.As(ws), nil).Want(t, 200).JSON(t, &lines)
	if len(lines.Data) != 2 || lines.Data[0].Name != "FR - B2B" {
		t.Fatalf("lines %+v", lines)
	}

	e.Do("PATCH", "/v1/phone/connections/"+c.ID.String(), ws.Admin.As(ws), map[string]any{"reuse_days": 0, "assign_answered": true}).
		Want(t, 200).JSON(t, &c)
	if c.ReuseDays != 0 || !c.AssignAnswered {
		t.Fatalf("patch %+v", c)
	}

	e.Do("DELETE", "/v1/phone/connections/"+c.ID.String(), ws.Admin.As(ws), nil).Want(t, 204)
	if len(ours()) != 0 || f.Webhook("gorgias-1") == nil {
		t.Fatalf("after disconnect %+v", f.Webhooks())
	}
	for _, req := range f.Requests() {
		if req == "GET /v1/webhooks" || req == "DELETE /v1/webhooks/gorgias-1" {
			t.Fatalf("touched webhooks that are not ours: %s", req)
		}
	}
	var audits int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action IN ('phone.connected', 'phone.disconnected')`, &audits, ws.ID)
	if audits != 2 {
		t.Fatalf("audit entries %d", audits)
	}
	var published, toAdmins int
	e.Scalar(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type LIKE 'phone_connection.%'`, &published, ws.ID)
	e.Scalar(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type LIKE 'phone_connection.%'
		AND user_ids @> ARRAY[$2::uuid, $3::uuid] AND NOT user_ids && ARRAY[$4::uuid, $5::uuid]`,
		&toAdmins, ws.ID, ws.Owner.ID, ws.Admin.ID, ws.Agent.ID, ws.Viewer.ID)
	if published == 0 || toAdmins != published {
		t.Fatalf("phone_connection events: %d, %d to admins only", published, toAdmins)
	}
}

func TestWebhookVerification(t *testing.T) {
	h := newHarness(t)
	body := h.recorded(t, "answered")[0]
	if got := h.send(t, bytes.ReplaceAll(body, []byte(h.token(t)), []byte("forged"))); got != 401 {
		t.Fatalf("forged token: %d", got)
	}
	res, _ := http.Post(h.e.Srv.URL+"/v1/phone/hooks/aircall/"+uuid.NewString(), "application/json", bytes.NewReader(body))
	if res.StatusCode != 404 {
		t.Fatalf("unknown connection: %d", res.StatusCode)
	}
	ignored := []byte(`{"resource":"user","event":"user.opened","timestamp":1,"token":"` + h.token(t) + `","data":{"id":1}}`)
	if got := h.send(t, ignored); got != 200 {
		t.Fatalf("ignored event: %d", got)
	}
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 0 {
		t.Fatalf("an ignored or forged event was queued: %d", n)
	}
	// Requests that fail count against their address, apart from the connection's limit:
	// a flood of forged events from one address is refused there, and the provider's own
	// events from another address still come in.
	cfg := *h.e.D.Cfg
	h.e.D.Cfg.TrustProxy, h.e.D.Cfg.ProxyHops = true, 0
	t.Cleanup(func() { *h.e.D.Cfg = cfg })
	from := func(ip, path string, b []byte) int {
		req, _ := http.NewRequest("POST", h.e.Srv.URL+path, bytes.NewReader(b))
		req.Header.Set("CF-Connecting-IP", ip)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	// Addresses of this run only (2001:db8::/32): the limiter lives as long as the process,
	// so under -count a fixed address would start the next run already over its limit.
	addr := func() string {
		u := uuid.New()
		return net.IP(append([]byte{0x20, 0x01, 0x0d, 0xb8}, u[4:]...)).String()
	}
	flood, unknown, provider := addr(), addr(), addr()
	hook := "/v1/phone/hooks/aircall/" + h.conn.ID.String()
	forged := bytes.ReplaceAll(body, []byte(h.token(t)), []byte("forged"))
	codes := map[int]int{}
	for i := 0; i < 40; i++ {
		codes[from(flood, hook, forged)]++
	}
	for i := 0; i < 40; i++ {
		codes[from(unknown, "/v1/phone/hooks/aircall/"+uuid.NewString(), body)]++
	}
	if codes[401] != 30 || codes[404] != 30 || codes[429] != 20 {
		t.Fatalf("forged and unknown: %v", codes)
	}
	if got := from(provider, hook, ignored); got != 200 {
		t.Fatalf("the provider's event after a flood from elsewhere: %d", got)
	}

	// An event older than the week its id would be kept is acknowledged and dropped.
	old := bytes.Replace(h.recorded(t, "answered")[1], []byte(`"timestamp":`), []byte(`"timestamp":1,"was":`), 1)
	if got := h.send(t, old); got != 200 {
		t.Fatalf("an old event: %d", got)
	}
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 0 {
		t.Fatalf("an event more than a week old was queued: %d", n)
	}

	// A retry of the same event is stored and queued once.
	h.send(t, body)
	h.send(t, body)
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 1 {
		t.Fatalf("retries queued %d jobs", n)
	}
}

func TestAnsweredCallIsOneEntryFromRingingToSummary(t *testing.T) {
	h := newHarness(t)
	h.e.Do("PATCH", "/v1/phone/connections/"+h.conn.ID.String(), h.ws.Admin.As(h.ws), map[string]any{"assign_answered": true}).Want(t, 200)
	_, seq := h.e.Events(h.ws.ID, 0)
	bodies := h.recorded(t, "answered")
	h.sendAll(t, bodies)

	tk := h.ticketOfCall(t, "1290001")
	if tk.Channel != domain.ChannelPhone || tk.Subject != "Call from +32470123456" || tk.Contact == nil {
		t.Fatalf("ticket %+v", tk)
	}
	contact, err := domain.LoadContact(h.e.Ctx, h.e.D.DB.Pool, h.ws.ID, tk.Contact.ID)
	if err != nil || contact.Phone != "+32470123456" {
		t.Fatalf("contact %+v %v", contact, err)
	}
	if tk.Assignee == nil || tk.Assignee.ID != h.ws.Agent.ID {
		t.Fatalf("an answered call assigns its ticket to who took it: %+v", tk.Assignee)
	}
	entries := h.callEntries(t, tk.ID)
	if len(entries) != 1 {
		t.Fatalf("%d call entries", len(entries))
	}
	d := entries[0].Data
	agent, _ := d["agent"].(map[string]any)
	if d["outcome"] != "answered" || d["stage"] != "ended" || d["direction"] != "inbound" || d["duration"] != float64(190) ||
		agent["id"] != h.ws.Agent.ID.String() || d["line"] != "FR - Support" || d["outside_hours"] != false {
		t.Fatalf("entry %+v", d)
	}
	if d["recording_url"] != "https://short.aircall.io/r/AbC" || d["asset_url"] != "https://assets.aircall.io/calls/1290001/recording" ||
		d["links_expire_at"] == nil {
		t.Fatalf("links %+v", d)
	}
	// direct_link is an API URL, not a page for people: it is not kept.
	if _, ok := d["link"]; ok {
		t.Fatalf("links %+v", d)
	}
	if tags, _ := d["tags"].([]any); len(tags) != 1 || tags[0] != "Order" {
		t.Fatalf("tags %+v", d["tags"])
	}
	if comments, _ := d["comments"].([]any); len(comments) != 1 {
		t.Fatalf("comments %+v", d["comments"])
	}
	types, _ := h.e.Events(h.ws.ID, seq)
	if !slices.Contains(types, "activity.created") || !slices.Contains(types, "activity.updated") || !slices.Contains(types, "contact.created") {
		t.Fatalf("events %v", types)
	}
	// The ringing call showed the caller in Aircall.
	if cards := h.f.Cards("1290001"); len(cards) != 1 {
		t.Fatalf("insight cards %+v", cards)
	} else if title := cards[0]["contents"].([]any)[0].(map[string]any); title["text"] != "+32470123456" {
		t.Fatalf("card title %+v", title)
	}

	// Aircall's retry of the final event changes nothing and runs no automation again.
	h.send(t, bodies[len(bodies)-1])
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 0 {
		t.Fatalf("a retried event was queued again: %d", n)
	}
	var triggered int
	h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id = $1 AND ended_trigger_at IS NOT NULL`, &triggered, h.ws.ID)
	if triggered != 1 {
		t.Fatalf("ended triggers %d", triggered)
	}

	// The AI summary and transcript land on the same entry.
	h.sendAll(t, h.recorded(t, "intelligence"))
	d = h.callEntries(t, tk.ID)[0].Data
	if !strings.Contains(d["summary"].(string), "replacement bulb") || !strings.HasPrefix(d["transcript"].(string), "Agent: Bonjour") {
		t.Fatalf("intelligence %+v", d)
	}
}

func TestEventsOutOfOrderSettleTheSame(t *testing.T) {
	h := newHarness(t)
	bodies := h.recorded(t, "answered")
	for i := len(bodies) - 1; i >= 0; i-- {
		if got := h.send(t, bodies[i]); got != 200 {
			t.Fatal(got)
		}
	}
	h.drain(t)
	tk := h.ticketOfCall(t, "1290001")
	entries := h.callEntries(t, tk.ID)
	if len(entries) != 1 || entries[0].Data["outcome"] != "answered" || entries[0].Data["stage"] != "ended" {
		t.Fatalf("entries %+v", entries)
	}
	// The ring came after the end: too late for a card.
	if cards := h.f.Cards("1290001"); len(cards) != 0 {
		t.Fatalf("cards %+v", cards)
	}
}

func TestMissedCallOutsideHoursReopensTheTicket(t *testing.T) {
	h := newHarness(t)
	// The caller is known, with a number written another way, and has a solved ticket.
	var tk domain.Ticket
	h.e.Do("POST", "/v1/tickets", h.ws.Agent.As(h.ws), map[string]any{"subject": "Broken bulb", "channel": "api", "kind": "note",
		"body_html": "<p>Earlier</p>", "status": "solved"}).Want(t, 201).JSON(t, &tk)
	cid := uuid.New()
	h.e.Exec(`INSERT INTO contacts (id, workspace_id, name, phone) VALUES ($1, $2, 'Jane Doe', '0032 470 12 34 56')`, cid, h.ws.ID)
	h.e.Exec(`UPDATE tickets SET contact_id = $2, status = 'solved', solved_at = now() - interval '1 day' WHERE id = $1`, tk.ID, cid)

	// Validation: call conditions need the call_ended trigger.
	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Wrong", "trigger": "ticket_created",
		"conditions": []map[string]any{{"field": "call_outcome", "op": "equals", "value": "missed"}},
		"actions":    []map[string]any{{"type": "set_priority", "value": "high"}}}).Want(t, 422)
	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Wrong value", "trigger": "call_ended",
		"conditions": []map[string]any{{"field": "call_time", "op": "equals", "value": "lunch"}},
		"actions":    []map[string]any{{"type": "set_priority", "value": "high"}}}).Want(t, 422)

	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Missed call outside opening hours", "trigger": "call_ended",
		"conditions": []map[string]any{
			{"field": "call_outcome", "op": "equals", "value": "missed"},
			{"field": "call_time", "op": "equals", "value": "outside_business_hours"},
			{"field": "call_direction", "op": "equals", "value": "inbound"},
		},
		"actions": []map[string]any{
			{"type": "set_status", "value": "open"},
			{"type": "add_note", "value": "<p>Missed call outside opening hours: call back.</p>"},
			{"type": "set_priority", "value": "high"},
		}}).Want(t, 201)
	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Tag the reason", "trigger": "call_ended",
		"conditions": []map[string]any{{"field": "call_missed_reason", "op": "equals", "value": "out_of_opening_hours"},
			{"field": "call_line", "op": "contains", "value": "support"}},
		"actions": []map[string]any{{"type": "add_tag", "value": "after-hours"}}}).Want(t, 201)
	// A line is picked by its id (Aircall's number 501 is "FR - Support"), not its name.
	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "By name", "trigger": "call_ended",
		"conditions": []map[string]any{{"field": "call_line", "op": "equals", "value": "FR - Support"}},
		"actions":    []map[string]any{{"type": "add_tag", "value": "by-name"}}}).Want(t, 422)
	for line, tag := range map[string]string{"501": "support-line", "502": "b2b-line"} {
		h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Line " + line, "trigger": "call_ended",
			"conditions": []map[string]any{{"field": "call_line", "op": "equals", "value": line}},
			"actions":    []map[string]any{{"type": "add_tag", "value": tag}}}).Want(t, 201)
	}
	// An answered-call rule must not fire.
	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Answered", "trigger": "call_ended",
		"conditions": []map[string]any{{"field": "call_outcome", "op": "equals", "value": "answered"}},
		"actions":    []map[string]any{{"type": "add_tag", "value": "answered"}}}).Want(t, 201)

	h.sendAll(t, h.recorded(t, "missed_out_of_hours"))

	got := h.ticketOfCall(t, "1290002")
	if got.ID != tk.ID {
		t.Fatalf("the call went on %s, not the caller's recent ticket %s", got.ID, tk.ID)
	}
	if got.Status != "open" || got.Priority != "high" {
		t.Fatalf("ticket %s %s", got.Status, got.Priority)
	}
	var tags []string
	for _, tg := range got.Tags {
		tags = append(tags, tg.Name)
	}
	if !slices.Contains(tags, "after-hours") || slices.Contains(tags, "answered") || !slices.Contains(tags, "support-line") || slices.Contains(tags, "b2b-line") {
		t.Fatalf("tags %v", tags)
	}
	var notes int
	h.e.Scalar(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND kind = 'note' AND body_text LIKE 'Missed call outside%'`, &notes, tk.ID)
	if notes != 1 {
		t.Fatalf("notes %d", notes)
	}
	d := h.callEntries(t, tk.ID)[0].Data
	if d["outcome"] != "missed" || d["missed_reason"] != "out_of_opening_hours" || d["outside_hours"] != true || d["caller_name"] != "" {
		t.Fatalf("entry %+v", d)
	}
	var contacts int
	h.e.Scalar(`SELECT count(*) FROM contacts WHERE workspace_id = $1`, &contacts, h.ws.ID)
	if contacts != 1 {
		t.Fatalf("a known caller got a second contact: %d", contacts)
	}
}

func TestVoicemailOutboundAndPlacement(t *testing.T) {
	h := newHarness(t)
	h.sendAll(t, h.recorded(t, "voicemail"))
	vm := h.ticketOfCall(t, "1290003")
	d := h.callEntries(t, vm.ID)[0].Data
	if d["outcome"] != "voicemail" || d["voicemail"] != true || d["missed_reason"] != "no_available_agent" || vm.Subject != "Call from +33612345678" {
		t.Fatalf("voicemail %+v %q", d, vm.Subject)
	}

	// The first call opens a ticket; the outbound call to the same number goes on it while it is open.
	h.sendAll(t, h.recorded(t, "answered"))
	in := h.ticketOfCall(t, "1290001")
	h.sendAll(t, h.recorded(t, "outbound"))
	out := h.ticketOfCall(t, "1290004")
	if out.ID != in.ID || len(h.callEntries(t, in.ID)) != 2 {
		t.Fatalf("outbound call on %s, open ticket %s", out.ID, in.ID)
	}
	d = h.callEntries(t, in.ID)[1].Data
	if d["direction"] != "outbound" || d["outcome"] != "answered" {
		t.Fatalf("outbound %+v", d)
	}

	// Solved, and with reuse off, the next call opens a new ticket.
	h.e.Exec(`UPDATE tickets SET status = 'solved' WHERE id = $1`, in.ID)
	h.e.Do("PATCH", "/v1/phone/connections/"+h.conn.ID.String(), h.ws.Admin.As(h.ws), map[string]any{"reuse_days": 0}).Want(t, 200)
	next := h.recorded(t, "outbound")
	for i := range next {
		next[i] = bytes.ReplaceAll(next[i], []byte("1290004"), []byte("1290005"))
	}
	h.sendAll(t, next)
	if again := h.ticketOfCall(t, "1290005"); again.ID == in.ID || again.Subject != "Call to +32470123456" {
		t.Fatalf("after solving: %s %q", again.ID, again.Subject)
	}

	// A withheld number opens a ticket without a contact.
	anon := recent(t, []byte(`{"resource":"call","event":"call.ended","timestamp":1793095200,"token":"`+h.token(t)+
		`","data":{"id":1290006,"direction":"inbound","raw_digits":"anonymous","started_at":1793095200,"ended_at":1793095210}}`))
	h.sendAll(t, [][]byte{anon})
	if tk := h.ticketOfCall(t, "1290006"); tk.Contact != nil || tk.Subject != "Call from a withheld number" {
		t.Fatalf("anonymous %+v", tk)
	}
}

// The insight card is read by anyone in Aircall: it names no ticket of a private team, and
// no spam or deleted one.
func TestInsightCardLeavesOutPrivateSpamAndDeletedTickets(t *testing.T) {
	h := newHarness(t)
	cid := uuid.New()
	h.e.Exec(`INSERT INTO contacts (id, workspace_id, name, phone) VALUES ($1, $2, 'Jane Doe', '+32 470 12 34 56')`, cid, h.ws.ID)
	team := uuid.New()
	h.e.Exec(`INSERT INTO teams (id, workspace_id, name, private) VALUES ($1, $2, 'Legal', true)`, team, h.ws.ID)
	ticket := func(subject, status string) uuid.UUID {
		var tk domain.Ticket
		h.e.Do("POST", "/v1/tickets", h.ws.Admin.As(h.ws), map[string]any{"subject": subject, "channel": "api", "kind": "note",
			"body_html": "<p>Earlier</p>", "status": status}).Want(t, 201).JSON(t, &tk)
		h.e.Exec(`UPDATE tickets SET contact_id = $2 WHERE id = $1`, tk.ID, cid)
		return tk.ID
	}
	visible := ticket("Visible order", "solved")
	spam := ticket("Spam offer", "solved")
	h.e.Exec(`UPDATE tickets SET spam = true WHERE id = $1`, spam)
	deleted := ticket("Deleted thread", "solved")
	h.e.Exec(`UPDATE tickets SET deleted_at = now() WHERE id = $1`, deleted)
	private := ticket("Legal claim", "open")
	h.e.Exec(`UPDATE tickets SET team_id = $2 WHERE id = $1`, private, team)
	hidden := ticket("Other legal claim", "solved")
	h.e.Exec(`UPDATE tickets SET team_id = $2 WHERE id = $1`, hidden, team)

	h.sendAll(t, h.recorded(t, "answered")[:1])
	if got := h.ticketOfCall(t, "1290001"); got.ID != private {
		t.Fatalf("the call went on %s, not the open ticket %s", got.ID, private)
	}
	cards := h.f.Cards("1290001")
	if len(cards) != 1 {
		t.Fatalf("cards %+v", cards)
	}
	contents := cards[0]["contents"].([]any)
	if title := contents[0].(map[string]any); title["text"] != "Jane Doe" {
		t.Fatalf("title %+v", title)
	}
	var lines []string
	for _, c := range contents[1:] {
		item := c.(map[string]any)
		lines = append(lines, item["label"].(string)+": "+item["text"].(string))
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Earlier: #") || !strings.Contains(lines[0], "Visible order") {
		t.Fatalf("card lines %v (visible ticket %s)", lines, visible)
	}
}

// A blocked contact's calls are recorded, without their content, but reach no ticket and
// run nothing, like their email.
func TestBlockedContactsCallsReachNoTicket(t *testing.T) {
	h := newHarness(t)
	cid := uuid.New()
	h.e.Exec(`INSERT INTO contacts (id, workspace_id, name, phone, blocked) VALUES ($1, $2, 'Spam Caller', '+32 470 12 34 56', true)`, cid, h.ws.ID)
	h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{"name": "Every call", "trigger": "call_ended",
		"conditions": []map[string]any{{"field": "call_direction", "op": "equals", "value": "inbound"}},
		"actions":    []map[string]any{{"type": "set_priority", "value": "high"}}}).Want(t, 201)

	for _, b := range append(h.recorded(t, "answered"), h.recorded(t, "intelligence")...) {
		if got := h.send(t, b); got != 200 {
			t.Fatalf("webhook answered %d", got)
		}
	}
	var triggers int
	for _, raw := range h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID) {
		var a jobs.PhoneInboundArgs
		_ = json.Unmarshal(raw, &a)
		if err := h.s.ProcessInbound(h.e.Ctx, a); err != nil {
			t.Fatalf("inbound: %v", err)
		}
	}
	triggers += len(h.e.Jobs(jobs.TicketTriggerArgs{}.Kind(), h.ws.ID))
	if cards := len(h.e.Jobs(jobs.PhoneInsightCardArgs{}.Kind(), h.ws.ID)); triggers != 0 || cards != 0 {
		t.Fatalf("a blocked contact's call queued %d triggers and %d cards", triggers, cards)
	}
	var ticket, activity *uuid.UUID
	var contact uuid.UUID
	var number, summary, transcript string
	h.e.Scalar(`SELECT ticket_id FROM phone_calls WHERE workspace_id = $1 AND external_call_id = '1290001'`, &ticket, h.ws.ID)
	h.e.Scalar(`SELECT activity_id FROM phone_calls WHERE workspace_id = $1 AND external_call_id = '1290001'`, &activity, h.ws.ID)
	h.e.Scalar(`SELECT contact_id FROM phone_calls WHERE workspace_id = $1 AND external_call_id = '1290001'`, &contact, h.ws.ID)
	h.e.Scalar(`SELECT number || '|' || caller_name || '|' || stage FROM phone_calls WHERE workspace_id = $1 AND external_call_id = '1290001'`, &number, h.ws.ID)
	h.e.Scalar(`SELECT summary FROM phone_calls WHERE workspace_id = $1 AND external_call_id = '1290001'`, &summary, h.ws.ID)
	h.e.Scalar(`SELECT transcript FROM phone_calls WHERE workspace_id = $1 AND external_call_id = '1290001'`, &transcript, h.ws.ID)
	if ticket != nil || activity != nil || contact != cid {
		t.Fatalf("blocked call on ticket %v, entry %v, contact %s", ticket, activity, contact)
	}
	if number != "||ended" || summary != "" || transcript != "" {
		t.Fatalf("a blocked contact's call kept its content: %q %q %q", number, summary, transcript)
	}
	var tickets, contacts int
	h.e.Scalar(`SELECT count(*) FROM tickets WHERE workspace_id = $1`, &tickets, h.ws.ID)
	h.e.Scalar(`SELECT count(*) FROM contacts WHERE workspace_id = $1`, &contacts, h.ws.ID)
	if tickets != 0 || contacts != 1 {
		t.Fatalf("tickets %d, contacts %d", tickets, contacts)
	}
}

// call_ended automations wait a minute after the call's end, for the labels the team adds
// right after hanging up.
func TestCallEndedAutomationsWaitAMinute(t *testing.T) {
	h := newHarness(t)
	for _, b := range h.recorded(t, "answered") {
		if got := h.send(t, b); got != 200 {
			t.Fatal(got)
		}
	}
	for _, raw := range h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID) {
		var a jobs.PhoneInboundArgs
		_ = json.Unmarshal(raw, &a)
		if err := h.s.ProcessInbound(h.e.Ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	var later, total int
	h.e.Scalar(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'workspace_id' = $2 AND args->>'trigger' = 'call_ended'`,
		&total, jobs.TicketTriggerArgs{}.Kind(), h.ws.ID.String())
	h.e.Scalar(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'workspace_id' = $2 AND args->>'trigger' = 'call_ended'
		AND scheduled_at > now() + interval '50 seconds'`, &later, jobs.TicketTriggerArgs{}.Kind(), h.ws.ID.String())
	if total != 1 || later != 1 {
		t.Fatalf("call_ended jobs %d, %d of them a minute out", total, later)
	}
}

// The reuse window counts from when a ticket was solved: a ticket solved long ago that
// was touched since (a label, a note) is not reused.
func TestReuseCountsFromWhenTheTicketWasSolved(t *testing.T) {
	h := newHarness(t)
	var tk domain.Ticket
	h.e.Do("POST", "/v1/tickets", h.ws.Agent.As(h.ws), map[string]any{"subject": "Old order", "channel": "api", "kind": "note",
		"body_html": "<p>Earlier</p>", "status": "solved"}).Want(t, 201).JSON(t, &tk)
	cid := uuid.New()
	h.e.Exec(`INSERT INTO contacts (id, workspace_id, name, phone) VALUES ($1, $2, 'Jane Doe', '+32 470 12 34 56')`, cid, h.ws.ID)
	h.e.Exec(`UPDATE tickets SET contact_id = $2, status = 'solved', solved_at = now() - interval '10 days', updated_at = now() WHERE id = $1`, tk.ID, cid)
	h.sendAll(t, h.recorded(t, "answered"))
	if got := h.ticketOfCall(t, "1290001"); got.ID == tk.ID {
		t.Fatal("a ticket solved ten days ago was reused with a seven day window")
	}
}

// A contact's number written with a trunk "(0)" after the country code matches the call,
// through the digits index.
func TestContactNumberWithTrunkZeroMatches(t *testing.T) {
	h := newHarness(t)
	cid := uuid.New()
	h.e.Exec(`INSERT INTO contacts (id, workspace_id, name, phone) VALUES ($1, $2, 'Jane Doe', '+32 (0)470 12 34 56')`, cid, h.ws.ID)
	h.sendAll(t, h.recorded(t, "answered"))
	if tk := h.ticketOfCall(t, "1290001"); tk.Contact == nil || tk.Contact.ID != cid {
		t.Fatalf("the call did not find the contact: %+v", tk.Contact)
	}
	var contacts int
	h.e.Scalar(`SELECT count(*) FROM contacts WHERE workspace_id = $1`, &contacts, h.ws.ID)
	if contacts != 1 {
		t.Fatalf("contacts %d", contacts)
	}
	// The lookup's expression is the index's.
	tx, err := h.e.D.DB.Pool.Begin(h.e.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(h.e.Ctx)
	if _, err := tx.Exec(h.e.Ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(h.e.Ctx, `EXPLAIN SELECT id FROM contacts WHERE workspace_id = $1 AND phone <> '' AND `+domain.PhoneDigitsSQL+` = ANY($2)`,
		h.ws.ID, []string{"32470123456"})
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	matched := false
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
		// The digits are in the index condition, not a filter after it.
		matched = matched || strings.Contains(line, "Index Cond") && strings.Contains(line, "regexp_replace")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "contacts_phone_digits_idx") || !matched {
		t.Fatalf("the lookup does not use the digits index:\n%s", plan.String())
	}
}

// A call's content goes with its ticket: removing the ticket removes the call's row and
// its entry, as it removes the ticket's messages.
func TestCallContentGoesWithItsTicket(t *testing.T) {
	h := newHarness(t)
	h.sendAll(t, append(h.recorded(t, "answered"), h.recorded(t, "intelligence")...))
	tk := h.ticketOfCall(t, "1290001")
	h.e.Exec(`DELETE FROM tickets WHERE workspace_id = $1 AND id = $2`, h.ws.ID, tk.ID)
	var calls, entries int
	h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id = $1`, &calls, h.ws.ID)
	h.e.Scalar(`SELECT count(*) FROM activities WHERE workspace_id = $1 AND type = 'call'`, &entries, h.ws.ID)
	if calls != 0 || entries != 0 {
		t.Fatalf("after the ticket went: %d calls, %d entries", calls, entries)
	}
}

// ours lists the webhooks pointing at the harness's connection.
func (h *harness) ours() []fake.Webhook {
	var out []fake.Webhook
	for _, w := range h.f.Webhooks() {
		if strings.Contains(w.URL, h.conn.ID.String()) {
			out = append(out, w)
		}
	}
	return out
}

// New credentials of the same account keep the webhook; credentials of another account
// remove it from the old account, with the old credentials, and register one in the new.
func TestNewCredentialsOfAnotherAccountMoveTheWebhook(t *testing.T) {
	h := newHarness(t)
	first := h.ours()
	if len(first) != 1 || first[0].Account != "id-1" {
		t.Fatalf("webhooks %+v", first)
	}
	h.f.AddAccount("id-1", "token-1b", "LampTwist")
	var c phone.PhoneConnectionView
	h.e.Do("PATCH", "/v1/phone/connections/"+h.conn.ID.String(), h.ws.Admin.As(h.ws),
		map[string]any{"credentials": map[string]string{"api_id": "id-1", "api_token": "token-1b"}}).Want(t, 200).JSON(t, &c)
	if now := h.ours(); len(now) != 1 || now[0].ID != first[0].ID || c.HealthStatus != "healthy" {
		t.Fatalf("a new key of the same account replaced the webhook: %+v %+v", now, c)
	}

	h.f.AddAccount("id-2", "token-2", "Other company")
	h.e.Do("PATCH", "/v1/phone/connections/"+h.conn.ID.String(), h.ws.Admin.As(h.ws),
		map[string]any{"credentials": map[string]string{"api_id": "id-2", "api_token": "token-2"}}).Want(t, 200).JSON(t, &c)
	now := h.ours()
	if len(now) != 1 || now[0].Account != "id-2" || c.HealthStatus != "healthy" || c.AccountName != "Other company" {
		t.Fatalf("after moving to another account: %+v %+v", now, c)
	}
	if !slices.Contains(h.f.Requests(), "DELETE /v1/webhooks/"+first[0].ID) {
		t.Fatalf("the old account's webhook was not removed: %v", h.f.Requests())
	}
	// Events of the new webhook are verified with its token.
	if got := h.send(t, h.recorded(t, "answered")[0]); got != 200 {
		t.Fatalf("an event of the new webhook: %d", got)
	}
}

// Tests at the same moment run one after the other: a disabled webhook is replaced once.
func TestConcurrentTestsReplaceTheWebhookOnce(t *testing.T) {
	h := newHarness(t)
	h.f.Disable(h.ours()[0].ID)
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = h.e.Do("POST", "/v1/phone/connections/"+h.conn.ID.String()+"/test", h.ws.Admin.As(h.ws), nil).Status
		}()
	}
	wg.Wait()
	for _, code := range codes {
		if code != 200 {
			t.Fatalf("tests answered %v", codes)
		}
	}
	if now := h.ours(); len(now) != 1 || !now[0].Active {
		t.Fatalf("webhooks after concurrent tests %+v", now)
	}
}

// The provider's numbers are asked at most every ten minutes per connection, and again
// once the credentials change.
func TestLinesAreCached(t *testing.T) {
	h := newHarness(t)
	numbers := func() int {
		n := 0
		for _, r := range h.f.Requests() {
			if r == "GET /v1/numbers" {
				n++
			}
		}
		return n
	}
	var lines struct {
		Data []phone.PhoneLine `json:"data"`
	}
	for i := 0; i < 3; i++ {
		h.e.Do("GET", "/v1/phone/lines", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &lines)
	}
	if numbers() != 1 || len(lines.Data) != 2 {
		t.Fatalf("numbers asked %d times, lines %+v", numbers(), lines)
	}
	h.f.AddAccount("id-1", "token-1b", "LampTwist")
	h.e.Do("PATCH", "/v1/phone/connections/"+h.conn.ID.String(), h.ws.Admin.As(h.ws),
		map[string]any{"credentials": map[string]string{"api_id": "id-1", "api_token": "token-1b"}}).Want(t, 200)
	h.e.Do("GET", "/v1/phone/lines", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &lines)
	if numbers() != 2 {
		t.Fatalf("new credentials did not list the numbers again: %d", numbers())
	}
}
