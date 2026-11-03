//go:build integration

package phone_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

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
	"github.com/useopendesk/opendesk/internal/phone/provider/placetel"
)

func newPlacetelHarness(t *testing.T) *harness {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if r.Method != http.MethodGet || (token != "Bearer placetel-token" && token != "Bearer placetel-token-2") {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/users?per_page=1":
			_, _ = w.Write([]byte(`[{"id":123}]`))
		case "/numbers?per_page=100&page=1":
			_, _ = w.Write([]byte(`[{"number":"02215550456","name":"Support"}]`))
		default:
			t.Errorf("unexpected provider endpoint: %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	var s *phone.Service
	e := hdtest.New(t, tickets.Register, resources.Register, func(d *deps.Deps, api huma.API, r chi.Router) {
		s = phone.New(d, placetel.Adapter{APIURL: server.URL})
		s.Register(api, r)
	})
	ws := e.NewWorkspace()
	h := &harness{e: e, ws: ws, s: s}
	e.Do("POST", "/v1/phone/connections", ws.Admin.As(ws), map[string]any{"provider": "placetel", "credentials": map[string]string{"api_token": "placetel-token"}}).Want(t, 201).JSON(t, &h.conn)
	return h
}

func placetelFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("provider/placetel/testdata/" + name + ".form")
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(b)
}

func postPlacetel(t *testing.T, h *harness, path string, body []byte) int {
	t.Helper()
	res, err := http.Post(h.e.Srv.URL+path, "application/x-www-form-urlencoded", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func placetelHook(t *testing.T, h *harness) string {
	t.Helper()
	u, err := url.Parse(h.conn.WebhookURL)
	if err != nil || u.Path == "" {
		t.Fatalf("webhook URL %q", h.conn.WebhookURL)
	}
	return u.Path
}

func TestPlacetelProviderDiscovery(t *testing.T) {
	e := hdtest.New(t, phone.Register)
	ws := e.NewWorkspace()
	var out struct {
		Data []phone.PhoneProvider `json:"data"`
	}
	e.Do("GET", "/v1/phone/providers", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &out)
	for _, key := range []string{"aircall", "voys", "placetel"} {
		if !slices.ContainsFunc(out.Data, func(p phone.PhoneProvider) bool { return p.Key == key }) {
			t.Fatalf("missing %s: %+v", key, out.Data)
		}
	}
	p := out.Data[slices.IndexFunc(out.Data, func(p phone.PhoneProvider) bool { return p.Key == "placetel" })]
	if p.Name != "Placetel" || !p.ManualWebhook || p.InsightCards || len(p.Fields) != 1 || p.Fields[0].Name != "api_token" || !p.Fields[0].Secret {
		t.Fatalf("provider %+v", p)
	}
}

func TestPlacetelManualWebhookConnection(t *testing.T) {
	h := newPlacetelHarness(t)
	c := h.conn
	if c.Provider != "placetel" || c.Name != "Placetel" || c.AccountName != "Placetel" || c.Status != "connected" || c.InsightCards || c.HealthStatus != "action_required" || c.HealthCode != phone.HealthNoWebhook || c.LastEventAt != nil {
		t.Fatalf("connection %+v", c)
	}
	base := "/v1/phone/hooks/placetel/" + c.ID.String()
	path := placetelHook(t, h)
	if !strings.HasPrefix(path, base+"/") || len(strings.TrimPrefix(path, base+"/")) != 43 {
		t.Fatalf("webhook URL %q", c.WebhookURL)
	}
	response := h.e.Do("GET", "/v1/phone/connections", h.ws.Agent.As(h.ws), nil).Want(t, 200)
	if strings.Contains(string(response.Body), "/v1/phone/hooks/") || strings.Contains(string(response.Body), "placetel-token") {
		t.Fatal("connection reveals webhook or credentials to agent")
	}
	h.e.Do("POST", "/v1/phone/connections/"+c.ID.String()+"/test", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &c)
	if c.HealthCode != phone.HealthNoWebhook {
		t.Fatalf("health after test %+v", c)
	}
	var lines struct {
		Data []phone.PhoneLine `json:"data"`
	}
	h.e.Do("GET", "/v1/phone/lines", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &lines)
	if len(lines.Data) != 1 || lines.Data[0] != (phone.PhoneLine{ID: "02215550456", Name: "Support", Provider: "placetel"}) {
		t.Fatalf("lines %+v", lines.Data)
	}
	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"missing secret", base, 401},
		{"wrong secret", base + "/" + strings.Repeat("A", 43), 401},
		{"wrong provider", "/v1/phone/hooks/voys/" + c.ID.String() + "/x", 404},
		{"unknown connection", "/v1/phone/hooks/placetel/" + uuid.NewString() + "/x", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := postPlacetel(t, h, tc.path, placetelFixture(t, "incoming")); got != tc.want {
				t.Fatalf("status %d, want %d", got, tc.want)
			}
		})
	}
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 0 {
		t.Fatalf("invalid callbacks queued %d jobs", n)
	}
	if got := postPlacetel(t, h, path, placetelFixture(t, "incoming")); got != 200 {
		t.Fatalf("valid callback status %d", got)
	}
	if n := len(h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)); n != 1 {
		t.Fatalf("valid callback queued %d jobs", n)
	}
}

func TestPlacetelCredentialReplacementRotatesWebhook(t *testing.T) {
	h := newPlacetelHarness(t)
	oldURL := h.conn.WebhookURL
	oldPath := placetelHook(t, h)
	body := placetelFixture(t, "incoming")
	if got := postPlacetel(t, h, oldPath, body); got != http.StatusOK {
		t.Fatalf("initial callback status %d", got)
	}
	connectionPath := "/v1/phone/connections/" + h.conn.ID.String()
	h.e.Do("POST", connectionPath+"/test", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &h.conn)
	if h.conn.HealthStatus != "healthy" || h.conn.HealthCode != phone.HealthReceiving || h.conn.LastEventAt == nil {
		t.Fatalf("health before replacement %+v", h.conn)
	}

	h.e.Do("PATCH", connectionPath, h.ws.Admin.As(h.ws), map[string]any{
		"credentials": map[string]string{"api_token": "placetel-token-2"},
	}).Want(t, 200).JSON(t, &h.conn)
	if h.conn.WebhookURL == oldURL || h.conn.WebhookURL == "" {
		t.Fatalf("replacement did not return a new webhook URL: %+v", h.conn)
	}
	if h.conn.HealthStatus != "action_required" || h.conn.HealthCode != phone.HealthNoWebhook || h.conn.LastEventAt != nil {
		t.Fatalf("health after replacement %+v", h.conn)
	}
	if got := postPlacetel(t, h, oldPath, body); got != http.StatusUnauthorized {
		t.Fatalf("old callback status %d, want 401", got)
	}
	if got := postPlacetel(t, h, placetelHook(t, h), placetelFixture(t, "accepted")); got != http.StatusOK {
		t.Fatalf("replacement callback status %d", got)
	}
	h.e.Do("POST", connectionPath+"/test", h.ws.Admin.As(h.ws), nil).Want(t, 200).JSON(t, &h.conn)
	if h.conn.HealthStatus != "healthy" || h.conn.HealthCode != phone.HealthReceiving || h.conn.LastEventAt == nil {
		t.Fatalf("health after replacement callback %+v", h.conn)
	}
}

func TestPlacetelCallLifecycle(t *testing.T) {
	for _, outcome := range []string{"answered", "missed"} {
		t.Run(outcome, func(t *testing.T) {
			h := newPlacetelHarness(t)
			h.e.Do("POST", "/v1/automations", h.ws.Admin.As(h.ws), map[string]any{
				"name": "Document ended call", "trigger": "call_ended", "enabled": true,
				"conditions": []map[string]any{{"field": "call_direction", "op": "equals", "value": "inbound"}, {"field": "call_outcome", "op": "equals", "value": outcome}},
				"actions":    []map[string]any{{"type": "add_note", "value": "<p>Placetel call ended.</p>"}},
			}).Want(t, 201)
			path := placetelHook(t, h)
			incoming := placetelFixture(t, "incoming")
			bodies := [][]byte{incoming}
			ended := placetelFixture(t, "missed")
			if outcome == "answered" {
				bodies = append(bodies, placetelFixture(t, "accepted"))
				ended = []byte(strings.ReplaceAll(strings.ReplaceAll(string(ended), "type=missed", "type=accepted"), "duration=0", "duration=30"))
			}
			bodies = append(bodies, ended)
			for _, body := range bodies {
				// The same callback and a reordered form with unrelated fields queue once.
				form, err := url.ParseQuery(string(body))
				if err != nil {
					t.Fatal(err)
				}
				form.Set("peer", "ignored@fpbx.de")
				for _, retry := range [][]byte{body, body, []byte(form.Encode())} {
					if got := postPlacetel(t, h, path, retry); got != 200 {
						t.Fatalf("status %d", got)
					}
				}
				queued := h.e.Jobs(jobs.PhoneInboundArgs{}.Kind(), h.ws.ID)
				if len(queued) != 1 {
					t.Fatalf("queued %d inbound jobs for one event", len(queued))
				}
				var args jobs.PhoneInboundArgs
				if err := json.Unmarshal(queued[0], &args); err != nil {
					t.Fatal(err)
				}
				// A retry of the worker is harmless too.
				for range 2 {
					if err := h.s.ProcessInbound(h.e.Ctx, args); err != nil {
						t.Fatal(err)
					}
				}
			}
			form, _ := url.ParseQuery(string(incoming))
			tk := h.ticketOfCall(t, form.Get("call_id"))
			entries := h.callEntries(t, tk.ID)
			if tk.Channel != domain.ChannelPhone || tk.Subject != "Call from 02215550123" || len(entries) != 1 {
				t.Fatalf("ticket %+v, entries %+v", tk, entries)
			}
			d := entries[0].Data
			if d["provider"] != "placetel" || d["direction"] != "inbound" || d["stage"] != "ended" || d["outcome"] != outcome || d["number"] != "02215550123" || d["line_id"] != "02215550456" || d["started_at"] == nil || d["ended_at"] == nil {
				t.Fatalf("call entry %+v", d)
			}
			if outcome == "answered" && (d["answered_at"] == nil || d["duration"] != float64(30)) || outcome == "missed" && d["missed_reason"] != "missed" {
				t.Fatalf("outcome fields %+v", d)
			}
			var calls, endedJobs int
			h.e.Scalar(`SELECT count(*) FROM phone_calls WHERE workspace_id=$1`, &calls, h.ws.ID)
			h.e.Scalar(`SELECT count(*) FROM river_job WHERE kind=$1 AND args->>'workspace_id'=$2 AND args->>'trigger'='call_ended'`, &endedJobs, jobs.TicketTriggerArgs{}.Kind(), h.ws.ID.String())
			if calls != 1 || endedJobs != 1 {
				t.Fatalf("calls %d, call_ended jobs %d", calls, endedJobs)
			}
			h.drain(t)
			// Replays after application cannot enqueue more work or run the automation twice.
			if got := postPlacetel(t, h, path, ended); got != 200 {
				t.Fatal(got)
			}
			h.drain(t)
			var notes int
			h.e.Scalar(`SELECT count(*) FROM messages WHERE ticket_id=$1 AND kind='note' AND body_text LIKE 'Placetel call ended.%'`, &notes, tk.ID)
			if notes != 1 {
				t.Fatalf("automation notes %d, want 1", notes)
			}
			if len(h.callEntries(t, tk.ID)) != 1 {
				t.Fatal("replay added a call entry")
			}
		})
	}
}
