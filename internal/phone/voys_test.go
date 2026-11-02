package phone_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/resources"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/phone"
	"github.com/useopendesk/opendesk/internal/phone/provider"
	"github.com/useopendesk/opendesk/internal/phone/provider/aircall"
	aircallfake "github.com/useopendesk/opendesk/internal/phone/provider/aircall/fake"
	"github.com/useopendesk/opendesk/internal/phone/provider/voys"
	"github.com/useopendesk/opendesk/internal/phone/provider/voys/fake"
)

type voysHarness struct {
	*harness
	v *fake.Voys
}

// newVoysHarness connects the workspace to a fake Voys (and has Aircall compiled in, as
// the instance does).
func newVoysHarness(t *testing.T) *voysHarness {
	t.Helper()
	v := fake.New(t, "voys-token")
	a := aircallfake.New(t, creds["api_id"], creds["api_token"])
	var s *phone.Service
	e := hdtest.New(t, tickets.Register, resources.Register, func(d *deps.Deps, api huma.API, r chi.Router) {
		s = phone.New(d, aircall.Adapter{BaseURL: a.URL()}, voys.Adapter{APIURL: v.API(), StorageURL: v.Storage()})
		s.Register(api, r)
	})
	ws := e.NewWorkspace()
	h := &voysHarness{harness: &harness{e: e, ws: ws, s: s, f: a}, v: v}
	e.Do("POST", "/v1/phone/connections", ws.Admin.As(ws), map[string]any{"provider": "voys", "credentials": map[string]string{"api_token": "voys-token"}}).
		Want(t, 201).JSON(t, &h.conn)
	return h
}

// hookPath is the path of the connection's webhook URL.
func (h *voysHarness) hookPath(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(h.conn.WebhookURL)
	if err != nil || u.Path == "" {
		t.Fatalf("webhook URL %q", h.conn.WebhookURL)
	}
	return u.Path
}

func (h *voysHarness) post(t *testing.T, path string, body []byte) int {
	t.Helper()
	res, err := http.Post(h.e.Srv.URL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

var voysStamp = regexp.MustCompile(`"timestamp":\s*"([^"]+)"`)

// recorded reads a Voys fixture with its times moved by whole weeks to within the last
// seven days and its colleagues' emails set to the workspace's agent (Jane) and admin
// (Piet).
func (h *voysHarness) recorded(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("provider/voys/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	week := 7 * 24 * time.Hour
	shift := time.Duration((time.Now().Unix()-fixtureBase)/int64(week.Seconds())) * week
	out := make([][]byte, len(list))
	for i, m := range list {
		s := strings.ReplaceAll(string(m), "jane.doe@lamptwist.example", h.ws.Agent.Email)
		s = strings.ReplaceAll(s, "piet@lamptwist.example", h.ws.Admin.Email)
		out[i] = voysStamp.ReplaceAllFunc([]byte(s), func(b []byte) []byte {
			at, err := time.Parse(time.RFC3339Nano, string(voysStamp.FindSubmatch(b)[1]))
			if err != nil {
				t.Fatal(err)
			}
			return []byte(`"timestamp":"` + at.Add(shift).Format(time.RFC3339Nano) + `"`)
		})
	}
	return out
}

func (h *voysHarness) sendAll(t *testing.T, bodies [][]byte) {
	t.Helper()
	for _, b := range bodies {
		if got := h.post(t, h.hookPath(t), b); got != 200 {
			t.Fatalf("webhook answered %d", got)
		}
		h.drain(t)
	}
}

func (h *voysHarness) connection(t *testing.T) phone.PhoneConnectionView {
	t.Helper()
	var list struct {
		Data []phone.PhoneConnectionView `json:"data"`
	}
	h.e.Do("GET", "/v1/phone/connections", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &list)
	for _, c := range list.Data {
		if c.ID == h.conn.ID {
			return c
		}
	}
	t.Fatal("connection gone")
	return phone.PhoneConnectionView{}
}

// Voys has no API for its call notifications: connecting registers nothing, the URL an
// admin pastes in Freedom carries the secret, and the connection waits for its first call.
func TestVoysConnectionWaitsForTheFirstCall(t *testing.T) {
	h := newVoysHarness(t)
	c := h.conn
	if c.Provider != "voys" || c.Name != "Lamp & Twist BV" || c.AccountName != "Lamp & Twist BV" || c.InsightCards ||
		c.HealthStatus != "action_required" || c.HealthCode != phone.HealthNoWebhook || c.LastEventAt != nil {
		t.Fatalf("connection %+v", c)
	}
	prefix := "/v1/phone/hooks/voys/" + c.ID.String() + "/"
	path := h.hookPath(t)
	if !strings.HasPrefix(path, prefix) || len(strings.TrimPrefix(path, prefix)) != 43 {
		t.Fatalf("webhook URL %q", c.WebhookURL)
	}
	var providers struct {
		Data []phone.PhoneProvider `json:"data"`
	}
	h.e.Do("GET", "/v1/phone/providers", h.ws.Viewer.As(h.ws), nil).Want(t, 200).JSON(t, &providers)
	if i := slices.IndexFunc(providers.Data, func(p phone.PhoneProvider) bool { return p.Key == "voys" }); i < 0 ||
		!providers.Data[i].ManualWebhook || providers.Data[i].InsightCards || len(providers.Data[i].Fields) != 1 {
		t.Fatalf("providers %+v", providers.Data)
	}
	// The URL carries the secret: admins only.
	r := h.e.Do("GET", "/v1/phone/connections", h.ws.Agent.As(h.ws), nil).Want(t, 200)
	if strings.Contains(string(r.Body), "/v1/phone/hooks/") || strings.Contains(string(r.Body), "voys-token") {
		t.Fatalf("agent sees %s", r.Body)
	}
	// A partner's token opens no customer account.
	h.v.AddPartner("partner-token")
	other := h.e.NewWorkspace()
	res := h.e.Do("POST", "/v1/phone/connections", other.Admin.As(other), map[string]any{"provider": "voys",
		"credentials": map[string]string{"api_token": "partner-token"}}).Want(t, 422)
	if !strings.Contains(string(res.Body), "partner") {
		t.Fatalf("partner token: %s", res.Body)
	}

	// A test checks the token and still waits.
	h.e.Do("POST", "/v1/phone/connections/"+c.ID.String()+"/test", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &c)
	if c.HealthCode != phone.HealthNoWebhook || c.Status != "connected" {
		t.Fatalf("after a test %+v", c)
	}
	var lines struct {
		Data []phone.PhoneLine `json:"data"`
	}
	h.e.Do("GET", "/v1/phone/lines", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &lines)
	if len(lines.Data) != 2 || lines.Data[0] != (phone.PhoneLine{ID: "+31332022075", Name: "Sales", Provider: "voys"}) {
		t.Fatalf("lines %+v", lines.Data)
	}

	// The first call event makes it receiving, live for admins.
	_, seq := h.e.Events(h.ws.ID, 0)
	h.sendAll(t, h.recorded(t, "answered")[:1])
	if c = h.connection(t); c.HealthStatus != "healthy" || c.HealthCode != phone.HealthReceiving || c.LastEventAt == nil {
		t.Fatalf("after the first call %+v", c)
	}
	if types, _ := h.e.Events(h.ws.ID, seq); !slices.Contains(types, "phone_connection.updated") {
		t.Fatalf("events %v", types)
	}
	h.e.Do("POST", "/v1/phone/connections/"+c.ID.String()+"/test", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &c)
	if c.HealthCode != phone.HealthReceiving {
		t.Fatalf("a test after calls came in %+v", c)
	}

	// A new URL: the old one stops working, and the connection waits for a call at the new one.
	old := h.hookPath(t)
	h.e.Do("POST", "/v1/phone/connections/"+c.ID.String()+"/rotate-webhook", h.ws.Agent.As(h.ws), nil).Want(t, 403)
	h.e.Do("POST", "/v1/phone/connections/"+c.ID.String()+"/rotate-webhook", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &h.conn)
	if h.hookPath(t) == old || h.conn.HealthCode != phone.HealthNoWebhook || h.conn.LastEventAt != nil {
		t.Fatalf("after rotating %+v", h.conn)
	}
	if got := h.post(t, old, h.recorded(t, "answered")[1]); got != 401 {
		t.Fatalf("the old URL answered %d", got)
	}
	h.sendAll(t, h.recorded(t, "answered")[1:2])
	var rotated int
	h.e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'phone.webhook_rotated'`, &rotated, h.ws.ID)
	if rotated != 1 {
		t.Fatalf("rotation audits %d", rotated)
	}

	// A new token of the same customer keeps the URL; one of another customer gets a new
	// URL, so the old customer's notifications stop being taken in.
	h.v.AddClient("voys-token-2", fake.Client, "Lamp & Twist BV")
	keep := h.hookPath(t)
	h.e.Do("PATCH", "/v1/phone/connections/"+c.ID.String(), h.ws.Admin.As(h.ws), map[string]any{"credentials": map[string]string{"api_token": "voys-token-2"}}).
		Want(t, 200).JSON(t, &h.conn)
	if h.hookPath(t) != keep || h.conn.HealthCode != phone.HealthReceiving {
		t.Fatalf("a new token of the same customer %+v", h.conn)
	}
	h.v.AddClient("voys-token-3", uuid.NewString(), "Other BV")
	h.e.Do("PATCH", "/v1/phone/connections/"+c.ID.String(), h.ws.Admin.As(h.ws), map[string]any{"credentials": map[string]string{"api_token": "voys-token-3"}}).
		Want(t, 200).JSON(t, &h.conn)
	if h.hookPath(t) == keep || h.conn.HealthCode != phone.HealthNoWebhook || h.conn.AccountName != "Other BV" {
		t.Fatalf("a token of another customer %+v", h.conn)
	}

	h.e.Do("DELETE", "/v1/phone/connections/"+c.ID.String(), h.ws.Admin.As(h.ws), nil).Want(t, 204)
	for _, req := range h.v.Requests() {
		if !strings.HasPrefix(req, "GET /api/v2/users/auth-context") && !strings.HasPrefix(req, "GET /api/v2/user/") &&
			!strings.HasPrefix(req, "GET /api/v2/clients/") && !strings.HasPrefix(req, "GET /transcription-storage/") {
			t.Fatalf("asked Voys %s", req)
		}
	}
	// Aircall's webhook is opendesk's to register: it has no URL to rotate.
	a := newHarness(t)
	a.e.Do("POST", "/v1/phone/connections/"+a.conn.ID.String()+"/rotate-webhook", a.ws.Admin.As(a.ws), nil).Want(t, 422)
}

func TestVoysWebhookVerification(t *testing.T) {
	h := newVoysHarness(t)
	body := h.recorded(t, "answered")[0]
	base := "/v1/phone/hooks/voys/" + h.conn.ID.String()
	for path, want := range map[string]int{
		base:                                 401, // no secret
		base + "/" + strings.Repeat("A", 43): 401,
		"/v1/phone/hooks/aircall/" + h.conn.ID.String() + "/x": 404,
		"/v1/phone/hooks/voys/" + uuid.NewString() + "/x":      404,
	} {
		if got := h.post(t, path, body); got != want {
			t.Fatalf("%s: %d, want %d", path, got, want)
		}
	}
	// Aircall's URL carries no secret.
	a := newHarness(t)
	if got := h.post(t, "/v1/phone/hooks/aircall/"+a.conn.ID.String()+"/x", a.recorded(t, "answered")[0]); got != 404 {
		t.Fatalf("aircall with a secret: %d", got)
	}
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 0 {
		t.Fatalf("unverified events queued: %d", n)
	}
	// Wrong secrets count against their address, like every failed verification.
	cfg := *h.e.D.Cfg
	h.e.D.Cfg.TrustProxy, h.e.D.Cfg.ProxyHops = true, 0
	t.Cleanup(func() { *h.e.D.Cfg = cfg })
	codes := map[int]int{}
	for i := 0; i < 40; i++ {
		req, _ := http.NewRequest("POST", h.e.Srv.URL+base+"/forged", bytes.NewReader(body))
		req.Header.Set("CF-Connecting-IP", "198.51.100.7")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		codes[res.StatusCode]++
	}
	if codes[401] != 30 || codes[429] != 10 {
		t.Fatalf("forged: %v", codes)
	}
	// The provider's address is shared: another connection's verified events from it still land.
	req, _ := http.NewRequest("POST", h.e.Srv.URL+h.hookPath(t), bytes.NewReader(body))
	req.Header.Set("CF-Connecting-IP", "[IP]")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("verified event after failures from its address: %d", res.StatusCode)
	}

	// An event more than a week old is acknowledged and dropped; a retry is queued once.
	old := voysStamp.ReplaceAll(body, []byte(`"timestamp":"2020-01-01T00:00:00+00:00"`))
	if got := h.post(t, h.hookPath(t), old); got != 200 {
		t.Fatalf("old event: %d", got)
	}
	h.post(t, h.hookPath(t), body)
	h.post(t, h.hookPath(t), body)
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 1 {
		t.Fatalf("queued %d", n)
	}
	// A call between colleagues is acknowledged and dropped.
	for _, b := range h.recorded(t, "internal") {
		if got := h.post(t, h.hookPath(t), b); got != 200 {
			t.Fatalf("internal call: %d", got)
		}
	}
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 0 {
		t.Fatalf("an internal call was queued: %d", n)
	}
}

func TestVoysAnsweredCallWithIntelligence(t *testing.T) {
	h := newVoysHarness(t)
	h.e.Do("PATCH", "/v1/phone/connections/"+h.conn.ID.String(), h.ws.Admin.As(h.ws), map[string]any{"assign_answered": true}).Want(t, 200)
	h.sendAll(t, h.recorded(t, "answered"))
	tk := h.ticketOfCall(t, "ua9-grq-prd-1793095200.1001")
	if tk.Channel != domain.ChannelPhone || tk.Subject != "Call from Johan" || tk.Contact == nil {
		t.Fatalf("ticket %+v", tk)
	}
	if tk.Assignee == nil || tk.Assignee.ID != h.ws.Agent.ID {
		t.Fatalf("assignee %+v", tk.Assignee)
	}
	entries := h.callEntries(t, tk.ID)
	if len(entries) != 1 {
		t.Fatalf("%d entries", len(entries))
	}
	d := entries[0].Data
	agent, _ := d["agent"].(map[string]any)
	if d["provider"] != "voys" || d["outcome"] != "answered" || d["number"] != "+32470123456" || d["line_id"] != "+31332022074" ||
		d["caller_name"] != "Johan" || agent["id"] != h.ws.Agent.ID.String() || d["answered_at"] == nil || d["ended_at"] == nil || d["asset_url"] != "" {
		t.Fatalf("entry %+v", d)
	}
	// No insight cards: Voys shows none.
	if n := len(h.f.Requests()); n != 0 {
		t.Fatalf("Aircall was asked: %v", h.f.Requests())
	}

	// The transcript and summary come from Voys' API, never from the unsigned notification.
	h.v.SetIntelligence("ua9-grq-prd-1793095200.1001", "Speaker 1: Hallo, met Johan.\nSpeaker 2: Goedemorgen.", "Johan asked for a replacement bulb.")
	h.sendAll(t, h.recorded(t, "intelligence"))
	d = h.callEntries(t, tk.ID)[0].Data
	if d["transcript"] != "Speaker 1: Hallo, met Johan.\nSpeaker 2: Goedemorgen." || d["summary"] != "Johan asked for a replacement bulb." ||
		d["recording"] != true || d["recording_url"] != "" {
		t.Fatalf("intelligence %+v", d)
	}
	if strings.Contains(d["summary"].(string), "notification") {
		t.Fatalf("the notification's text was taken: %+v", d)
	}
	var calls int
	h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id = $1`, &calls, h.ws.ID)
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}

func TestVoysMissedOutboundAndWithheld(t *testing.T) {
	h := newVoysHarness(t)
	h.sendAll(t, h.recorded(t, "missed"))
	d := h.callEntries(t, h.ticketOfCall(t, "ua9-grq-prd-1793095200.2001").ID)[0].Data
	if d["outcome"] != "missed" || d["missed_reason"] != "no_answer" || d["agent"] != nil {
		t.Fatalf("missed %+v", d)
	}
	var triggers int
	h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id = $1 AND ended_trigger_at IS NOT NULL`, &triggers, h.ws.ID)
	if triggers != 1 {
		t.Fatalf("call_ended triggers %d", triggers)
	}

	h.sendAll(t, h.recorded(t, "outbound"))
	tk := h.ticketOfCall(t, "ua4-ams-prd-1793095200.3001")
	d = h.callEntries(t, tk.ID)[0].Data
	agent, _ := d["agent"].(map[string]any)
	if tk.Subject != "Call to +32470999888" || d["direction"] != "outbound" || d["outcome"] != "missed" || d["missed_reason"] != "busy" ||
		d["line_id"] != "+31332022074" || agent["id"] != h.ws.Agent.ID.String() {
		t.Fatalf("outbound %+v %+v", tk, d)
	}

	h.sendAll(t, h.recorded(t, "withheld"))
	tk = h.ticketOfCall(t, "ua9-grq-prd-1793095200.6001")
	if tk.Subject != "Call from a withheld number" || tk.Contact != nil {
		t.Fatalf("withheld %+v", tk)
	}
}

// A warm transfer joins the customer's call with a colleague's call to a colleague and goes
// on under the second one's id: it stays one call on one ticket, taken by the colleague it
// was transferred to.
func TestVoysTransferIsOneCall(t *testing.T) {
	h := newVoysHarness(t)
	h.sendAll(t, h.recorded(t, "transfer"))
	var calls, tickets int
	h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id = $1`, &calls, h.ws.ID)
	h.e.Scalar(`SELECT count(*) FROM tickets WHERE workspace_id = $1 AND channel = 'phone'`, &tickets, h.ws.ID)
	if calls != 1 || tickets != 1 {
		t.Fatalf("%d calls on %d tickets", calls, tickets)
	}
	tk := h.ticketOfCall(t, "ua7-ams-prd-1793095200.5002")
	entries := h.callEntries(t, tk.ID)
	if len(entries) != 1 {
		t.Fatalf("%d entries", len(entries))
	}
	d := entries[0].Data
	agent, _ := d["agent"].(map[string]any)
	if d["outcome"] != "answered" || d["stage"] != "ended" || agent["id"] != h.ws.Admin.ID.String() || d["line_id"] != "+31332022074" {
		t.Fatalf("entry %+v", d)
	}
	// The transcript of the call that goes on reaches the same entry.
	h.v.SetIntelligence("ua7-ams-prd-1793095200.5002", "Speaker 1: Doorverbonden.", "Transferred to the admin.")
	h.sendAll(t, [][]byte{[]byte(`{"call_id":"ua7-ams-prd-1793095200.5002","summary":"x"}`)})
	if d := h.callEntries(t, tk.ID)[0].Data; d["summary"] != "Transferred to the admin." {
		t.Fatalf("summary %+v", d)
	}
}

// Voys' answers decide only what it said: a throttled fetch waits, and an event about a call
// opendesk never saw (a colleague's call) makes none.
func TestVoysFetchesWaitAndUnknownCallsMakeNothing(t *testing.T) {
	h := newVoysHarness(t)
	h.sendAll(t, h.recorded(t, "answered"))
	h.v.Limit = true
	if got := h.post(t, h.hookPath(t), h.recorded(t, "intelligence")[1]); got != 200 {
		t.Fatal(got)
	}
	queued := h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)
	if len(queued) != 1 {
		t.Fatalf("queued %d", len(queued))
	}
	var a jobs.PhoneInboundArgs
	_ = json.Unmarshal(queued[0], &a)
	err := h.s.ProcessInbound(h.e.Ctx, a)
	var limited *provider.RateLimited
	if !errors.As(err, &limited) || limited.RetryAfter != time.Minute {
		t.Fatalf("throttled transcript: %v", err)
	}
	h.v.Limit = false

	if got := h.post(t, h.hookPath(t), []byte(`{"call_id":"ua4-ams-prd-1793095200.4001","recording_url":"https://api.voys.nl/x"}`)); got != 200 {
		t.Fatal(got)
	}
	queued = h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)
	if len(queued) != 1 {
		t.Fatalf("queued %d", len(queued))
	}
	_ = json.Unmarshal(queued[0], &a)
	if err := h.s.ProcessInbound(h.e.Ctx, a); err == nil {
		t.Fatal("a recording of an unknown call was applied")
	}
	var calls int
	h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id = $1`, &calls, h.ws.ID)
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}
