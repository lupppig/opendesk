package placetel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/phone/provider"
	"github.com/useopendesk/opendesk/internal/phone/provider/placetel"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".form")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func parse(t *testing.T, b []byte) provider.Event {
	t.Helper()
	e, err := (placetel.Adapter{}).Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func changed(t *testing.T, name string, changes map[string]string) []byte {
	t.Helper()
	v, err := url.ParseQuery(strings.TrimSpace(string(fixture(t, name))))
	if err != nil {
		t.Fatal(err)
	}
	for k, s := range changes {
		v.Set(k, s)
	}
	return []byte(v.Encode())
}

func TestDescribeAndUnsupported(t *testing.T) {
	a := placetel.Adapter{}
	d := a.Describe()
	if d.Key != "placetel" || d.Name != "Placetel" || !d.ManualWebhook || d.InsightCards || len(d.Fields) != 1 || d.Fields[0].Name != "api_token" || !d.Fields[0].Secret {
		t.Fatalf("descriptor %+v", d)
	}
	ctx := context.Background()
	_, e1 := a.RegisterWebhook(ctx, nil, nil, "")
	_, e2 := a.WebhookActive(ctx, nil, nil, "")
	e3 := a.DeleteWebhook(ctx, nil, nil, "")
	e4 := a.Verify(nil, "")
	_, e5 := a.Content(ctx, nil, nil, "", "")
	e6 := a.PushCard(ctx, nil, nil, "", provider.Card{})
	for _, err := range []error{e1, e2, e3, e4, e5, e6} {
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Fatalf("unsupported: %v", err)
		}
	}
}

func TestParseFixtures(t *testing.T) {
	for _, tt := range []struct {
		name, direction, stage, reason string
		final, voicemail               bool
	}{
		{"incoming", "inbound", "ringing", "", false, false}, {"outgoing", "outbound", "ringing", "", false, false}, {"accepted", "inbound", "answered", "", false, false}, {"missed", "inbound", "ended", "missed", true, false}, {"voicemail", "inbound", "ended", "", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			e := parse(t, fixture(t, tt.name))
			c := e.Call
			if e.ID == "" || e.Kind != provider.KindCall || len(e.CallID) != 64 || e.Final != tt.final || c.Direction != tt.direction || c.Stage != tt.stage || c.Number != "02215550123" || c.LineID != "02215550456" || c.LineName != c.LineID || c.MissedReason != tt.reason || c.Voicemail != tt.voicemail || c.Duration != 0 || e.At.Before(before) {
				t.Fatalf("event %+v call %+v", e, c)
			}
			switch c.Stage {
			case "ringing":
				if c.StartedAt == nil {
					t.Fatal("missing start")
				}
			case "answered":
				if c.AnsweredAt == nil {
					t.Fatal("missing answer")
				}
			case "ended":
				if c.EndedAt == nil {
					t.Fatal("missing end")
				}
			}
			if c.Agent != nil || c.Caller != nil || c.Recording || c.RecordingURL != "" || c.VoicemailURL != "" {
				t.Fatal("unexpected content")
			}
		})
	}
}

func TestParseHungUp(t *testing.T) {
	for _, direction := range []string{"in", "out"} {
		t.Run(direction, func(t *testing.T) {
			e := parse(t, changed(t, "missed", map[string]string{"type": "accepted", "duration": "42", "direction": direction}))
			c := e.Call
			if !e.Final || c.Duration != 42 || c.AnsweredAt == nil || c.EndedAt.Sub(*c.AnsweredAt) != 42*time.Second || c.MissedReason != "" {
				t.Fatalf("call %+v", c)
			}
		})
	}
	for _, reason := range []string{"blocked", "busy", "canceled", "unavailable", "congestion"} {
		t.Run(reason, func(t *testing.T) {
			e := parse(t, changed(t, "missed", map[string]string{"type": reason, "direction": "out"}))
			if e.Call.MissedReason != reason {
				t.Fatal(e.Call)
			}
		})
	}
}

func TestParseAnonymous(t *testing.T) {
	for _, value := range []string{"", "anonymous", "ANONYMOUS", "unknown", "private", "withheld", "restricted"} {
		t.Run("number="+value, func(t *testing.T) {
			e := parse(t, changed(t, "incoming", map[string]string{"from": value}))
			if !e.Call.Anonymous || e.Call.Number != "" {
				t.Fatal(e.Call)
			}
		})
	}
}

func TestParseInvalidAndIgnored(t *testing.T) {
	for _, tt := range []struct {
		name    string
		body    []byte
		ignored bool
	}{
		{"bad escape", []byte("event=%zz"), false}, {"empty", nil, true}, {"unknown", changed(t, "incoming", map[string]string{"event": "Other"}), true},
		{"id", changed(t, "incoming", map[string]string{"call_id": "private data"}), false}, {"direction", changed(t, "incoming", map[string]string{"direction": "out"}), false},
		{"duration", changed(t, "missed", map[string]string{"duration": "-1"}), false}, {"overflow", changed(t, "missed", map[string]string{"duration": "99999999999999999"}), false}, {"cause", changed(t, "missed", map[string]string{"type": "private data"}), false},
		{"sip", changed(t, "outgoing", map[string]string{"to": "7777example@fpbx.de"}), true}, {"extension", changed(t, "outgoing", map[string]string{"to": "23"}), true}, {"line", changed(t, "incoming", map[string]string{"to": ""}), true},
		{"duplicate key", append(fixture(t, "incoming"), []byte("&direction=in")...), false}, {"large", []byte(strings.Repeat("x", (1<<20)+1)), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (placetel.Adapter{}).Parse(tt.body)
			if err == nil || errors.Is(err, provider.ErrIgnored) != tt.ignored {
				t.Fatalf("error %v", err)
			}
			if strings.Contains(err.Error(), "private data") {
				t.Fatal("payload in error")
			}
		})
	}
}

func TestStableEventIDs(t *testing.T) {
	body := fixture(t, "incoming")
	first := parse(t, body)
	for _, b := range [][]byte{body, changed(t, "incoming", map[string]string{"extra": "ignored", "peer": "private@fpbx.de"}), changed(t, "incoming", map[string]string{"call_id": strings.ToUpper(first.CallID)})} {
		if e := parse(t, b); e.ID != first.ID {
			t.Fatalf("retry id changed: %s %s", first.ID, e.ID)
		}
	}
	for _, name := range []string{"accepted", "missed", "voicemail"} {
		if e := parse(t, fixture(t, name)); e.ID == first.ID {
			t.Fatal("different events share id")
		}
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "peer") || strings.Contains(string(raw), "extra") {
		t.Fatal("raw fields retained")
	}
}

func TestAPI(t *testing.T) {
	creds := map[string]string{"api_token": "test-token"}
	t.Run("account and numbers", func(t *testing.T) {
		pages := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" || r.Method != "GET" {
				t.Error("request headers/method")
			}
			switch r.URL.Path {
			case "/users":
				if r.URL.Query().Get("per_page") != "1" {
					t.Error("user limit")
				}
				fmt.Fprint(w, `[{"id":123,"email":"never-retained@example.org"}]`)
			case "/numbers":
				pages++
				if r.URL.Query().Get("per_page") != "100" {
					t.Error("number limit")
				}
				if pages == 1 {
					rows := make([]map[string]string, 100)
					for i := range rows {
						rows[i] = map[string]string{"number": "02215550456", "name": "Support"}
					}
					_ = json.NewEncoder(w).Encode(rows)
				} else {
					fmt.Fprint(w, `[{"number":"02215550789","name":""},{"number":"23","name":"Internal"}]`)
				}
			default:
				t.Error("unexpected path")
			}
		}))
		defer srv.Close()
		a := placetel.Adapter{APIURL: srv.URL}
		account, err := a.Validate(context.Background(), srv.Client(), creds)
		if err != nil || account.Name != "Placetel" || account.ID != "" {
			t.Fatalf("account %+v %v", account, err)
		}
		lines, err := a.Lines(context.Background(), srv.Client(), creds)
		if err != nil || len(lines) != 101 || pages != 2 || lines[0].ID != "02215550456" || lines[0].Name != "Support" || lines[100].Name != "02215550789" {
			t.Fatalf("lines %d pages %d err %v", len(lines), pages, err)
		}
	})
	for _, tt := range []struct {
		name, body, retry string
		status            int
		refused, limited  bool
	}{
		{"unauthorized", "secret-token", "", 401, true, false}, {"forbidden", "secret-token", "", 403, true, false}, {"server", "secret-token", "", 500, false, false}, {"limited", "secret-token", "7", 429, false, true}, {"limited fallback", "secret-token", "99999", 429, false, true}, {"empty", "[]", "", 200, true, false}, {"invalid json", "secret-token", "", 200, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", tt.retry)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			_, err := (placetel.Adapter{APIURL: srv.URL}).Validate(context.Background(), srv.Client(), creds)
			var limited *provider.RateLimited
			if err == nil || provider.Refused(err) != tt.refused || errors.As(err, &limited) != tt.limited || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("error %v", err)
			}
			if limited != nil {
				want := time.Minute
				if tt.retry == "7" {
					want = 7 * time.Second
				}
				if limited.RetryAfter != want {
					t.Fatal(limited)
				}
			}
			if tt.status == 500 {
				var status *provider.StatusError
				if !errors.As(err, &status) || status.Status != 500 {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestInternationalNumbers(t *testing.T) {
	e := parse(t, changed(t, "incoming", map[string]string{"from": "+32470123456", "to": "00492215550456"}))
	if e.Call.Number != "+32470123456" || e.Call.LineID != "+492215550456" {
		t.Fatal(e.Call)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFixedAPIHostAndSafeTransportError(t *testing.T) {
	c := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.placetel.de" || r.URL.Path != "/v2/users" {
			t.Errorf("unexpected URL %s", r.URL)
		}
		return nil, errors.New("sensitive transport text")
	})}
	_, err := (placetel.Adapter{}).Validate(context.Background(), c, map[string]string{"api_token": "token", "api_url": "http://untrusted.example"})
	if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "token") {
		t.Fatalf("error %v", err)
	}
}
