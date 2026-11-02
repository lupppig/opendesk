package voys_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/phone/provider"
	"github.com/useopendesk/opendesk/internal/phone/provider/voys"
	"github.com/useopendesk/opendesk/internal/phone/provider/voys/fake"
)

func recorded(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	out := make([][]byte, len(list))
	for i, m := range list {
		out[i] = m
	}
	return out
}

func parse(t *testing.T, body []byte) provider.Event {
	t.Helper()
	ev, err := voys.Adapter{}.Parse(body)
	if err != nil {
		t.Fatalf("parse %s: %v", body, err)
	}
	return ev
}

func TestParseAnsweredCall(t *testing.T) {
	bodies := recorded(t, "answered")
	var evs []provider.Event
	for _, b := range bodies {
		evs = append(evs, parse(t, b))
	}
	stages := []string{}
	for _, ev := range evs {
		if ev.Kind != provider.KindCall || ev.CallID != "ua9-grq-prd-1793095200.1001" || ev.ID == "" {
			t.Fatalf("event %+v", ev)
		}
		stages = append(stages, ev.Call.Stage)
	}
	if strings.Join(stages, ",") != "ringing,ringing,answered,ended" {
		t.Fatalf("stages %v", stages)
	}
	if evs[0].ID == evs[1].ID {
		t.Fatal("two events share an id")
	}
	created := evs[0]
	if created.At != time.Date(2026, 10, 27, 10, 0, 0, 972110000, time.UTC) {
		t.Fatalf("at %v", created.At)
	}
	c := created.Call
	if c.Direction != "inbound" || c.Number != "+32470123456" || c.LineID != "+31332022074" || c.LineName != "+31332022074" ||
		c.Caller == nil || c.Caller.Name != "Johan" || c.Agent != nil {
		t.Fatalf("created %+v", c)
	}
	// The phones that ring are not who answered.
	if evs[1].Call.Agent != nil {
		t.Fatalf("ringing agent %+v", evs[1].Call.Agent)
	}
	answered := evs[2].Call
	if answered.Agent == nil || answered.Agent.Email != "jane.doe@lamptwist.example" || answered.AnsweredAt == nil || !answered.AnsweredAt.Equal(evs[2].At) {
		t.Fatalf("answered %+v", answered)
	}
	ended := evs[3]
	if !ended.Final || ended.Call.EndedAt == nil || ended.Call.MissedReason != "" || ended.Call.Agent != nil {
		t.Fatalf("ended %+v %+v", ended, ended.Call)
	}
}

func TestParseMissedOutboundWithheld(t *testing.T) {
	missed := recorded(t, "missed")
	if ev := parse(t, missed[2]); !ev.Final || ev.Call.MissedReason != "no_answer" || ev.Call.Caller != nil {
		t.Fatalf("missed %+v %+v", ev, ev.Call)
	}

	out := recorded(t, "outbound")
	ev := parse(t, out[0])
	c := ev.Call
	// The customer is the one called; the line is the caller id; the agent made the call.
	if c.Direction != "outbound" || c.Number != "+32470999888" || c.LineID != "+31332022074" ||
		c.Agent == nil || c.Agent.Email != "jane.doe@lamptwist.example" || c.Agent.Name != "Jane Doe" {
		t.Fatalf("outbound %+v", c)
	}
	if ev := parse(t, out[1]); ev.Call.MissedReason != "busy" || !ev.Final {
		t.Fatalf("busy %+v", ev.Call)
	}

	w := recorded(t, "withheld")
	if ev := parse(t, w[0]); !ev.Call.Anonymous || ev.Call.Number != "" {
		t.Fatalf("withheld %+v", ev.Call)
	}
	if ev := parse(t, w[1]); ev.Call.MissedReason != "cancelled" {
		t.Fatalf("cancelled %+v", ev.Call)
	}
	for _, body := range []string{
		`{"call_id":"x","status":"created","direction":"inbound","caller":{"number":"","name":null},"destination":{"number":"+31332022074"}}`,
		`{"call_id":"x","status":"created","direction":"inbound","caller":{"number":"Anonymous"},"destination":{"number":"+31332022074"}}`,
	} {
		if ev := parse(t, []byte(body)); !ev.Call.Anonymous {
			t.Fatalf("withheld %s: %+v", body, ev.Call)
		}
	}
}

func TestParseTransfers(t *testing.T) {
	tr := recorded(t, "transfer")
	// The leg from one colleague to another reaches no customer.
	for _, b := range tr[2:4] {
		if _, err := (voys.Adapter{}).Parse(b); !errors.Is(err, provider.ErrIgnored) {
			t.Fatalf("internal leg: %v", err)
		}
	}
	warm := parse(t, tr[4])
	c := warm.Call
	if warm.CallID != "ua7-ams-prd-1793095200.5002" || warm.Continues != "ua7-ams-prd-1793095200.5001" || c.Stage != provider.StageAnswered ||
		c.Number != "+32470123456" || c.Agent == nil || c.Agent.Email != "piet@lamptwist.example" || c.LineID != "" {
		t.Fatalf("warm transfer %+v %+v", warm, c)
	}
	// Help page: a cold transfer rings the new target, who has not answered yet.
	cold := parse(t, []byte(`{
		"call_id": "ua4-grq-prd-1693576927.25584586",
		"merged_id": "ua4-grq-prd-1693576937.25585499",
		"timestamp": "2023-09-01T14:02:17.054643+00:00",
		"status": "cold-transfer",
		"version": "v2",
		"direction": "inbound",
		"caller": {"number": "+31507009920", "name": "Johan N", "account_number": null, "user_numbers": [], "user_emails": []},
		"destination": {"number": "629", "targets": [{"number": "629", "name": null, "account_number": "203", "user_numbers": ["629"], "user_emails": ["piet@lamptwist.example"]}]},
		"redirector": {"number": "+31332022074", "name": null, "account_number": "201", "user_numbers": ["610"], "user_emails": ["jane.doe@lamptwist.example"]}
	}`))
	if cold.Continues != "ua4-grq-prd-1693576937.25585499" || cold.Call.Stage != provider.StageAnswered || cold.Call.Agent != nil {
		t.Fatalf("cold transfer %+v %+v", cold, cold.Call)
	}
}

func TestParseIntelligenceAndIgnored(t *testing.T) {
	ai := recorded(t, "intelligence")
	rec := parse(t, ai[0])
	if rec.Kind != provider.KindCall || rec.Call == nil || !rec.Call.Recording || rec.Call.Direction != "" || rec.Call.RecordingURL != "" {
		t.Fatalf("recording %+v %+v", rec, rec.Call)
	}
	// The notification's own text is never taken: it is fetched from Voys.
	tr := parse(t, ai[1])
	if tr.Kind != provider.KindTranscript || tr.Transcript != "" || tr.CallID != "ua9-grq-prd-1793095200.1001" {
		t.Fatalf("transcript %+v", tr)
	}
	sum := parse(t, ai[2])
	if sum.Kind != provider.KindSummary || sum.Summary != "" {
		t.Fatalf("summary %+v", sum)
	}
	for _, name := range []string{"internal"} {
		for _, b := range recorded(t, name) {
			if _, err := (voys.Adapter{}).Parse(b); !errors.Is(err, provider.ErrIgnored) {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	for _, body := range []string{
		`{"call_id":"x","status":"voicemail","direction":"inbound"}`,
		`{"call_id":"x","something":"else"}`,
	} {
		if _, err := (voys.Adapter{}).Parse([]byte(body)); !errors.Is(err, provider.ErrIgnored) {
			t.Fatalf("%s: %v", body, err)
		}
	}
	if _, err := (voys.Adapter{}).Parse([]byte(`{"status":"ringing"}`)); err == nil || errors.Is(err, provider.ErrIgnored) {
		t.Fatalf("an event without a call id: %v", err)
	}
}

func TestNoWebhookAPI(t *testing.T) {
	a := voys.Adapter{}
	d := a.Describe()
	if d.Key != "voys" || !d.ManualWebhook || d.InsightCards || len(d.Fields) != 1 || !d.Fields[0].Secret {
		t.Fatalf("descriptor %+v", d)
	}
	ctx := context.Background()
	if _, err := a.RegisterWebhook(ctx, http.DefaultClient, nil, "https://x"); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := a.Verify([]byte(`{}`), "secret"); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatal("Verify accepts a body: the URL carries the secret")
	}
	if err := a.PushCard(ctx, http.DefaultClient, nil, "x", provider.Card{}); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestAPIAgainstFakeVoys(t *testing.T) {
	f := fake.New(t, "tok-1")
	a := voys.Adapter{APIURL: f.API(), StorageURL: f.Storage()}
	ctx := context.Background()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	account, err := a.Validate(ctx, client, map[string]string{"api_token": "tok-1"})
	if err != nil || account.ID != fake.Client || account.Name != "Lamp & Twist BV" {
		t.Fatalf("validate %+v %v", account, err)
	}
	if _, err := a.Validate(ctx, client, map[string]string{"api_token": "wrong"}); !provider.Refused(err) {
		t.Fatalf("wrong token: %v", err)
	}
	f.AddPartner("partner")
	_, err = a.Validate(ctx, client, map[string]string{"api_token": "partner"})
	var refused *provider.AccountRefused
	if !errors.As(err, &refused) || !provider.Refused(err) || !strings.Contains(refused.Detail, "partner") {
		t.Fatalf("partner token: %v", err)
	}

	creds := map[string]string{"api_token": "tok-1", provider.AccountIDKey: account.ID}
	lines, err := a.Lines(ctx, client, creds)
	if err != nil || len(lines) != 2 || lines[0] != (provider.Line{ID: "+31332022074", Name: "Support desk"}) || lines[1].Name != "Sales" {
		t.Fatalf("lines %+v %v", lines, err)
	}

	f.SetIntelligence("ua9-grq-prd-1793095200.1001", "Speaker 1: Hallo.\nSpeaker 2: Goedemorgen.", "The customer asked about a bulb.")
	text, err := a.Content(ctx, client, creds, provider.KindTranscript, "ua9-grq-prd-1793095200.1001")
	if err != nil || text != "Speaker 1: Hallo.\nSpeaker 2: Goedemorgen." {
		t.Fatalf("transcript %q %v", text, err)
	}
	text, err = a.Content(ctx, client, creds, provider.KindSummary, "ua9-grq-prd-1793095200.1001")
	if err != nil || text != "The customer asked about a bulb." {
		t.Fatalf("summary %q %v", text, err)
	}
	var se *provider.StatusError
	if _, err := a.Content(ctx, client, creds, provider.KindTranscript, "unknown"); !errors.As(err, &se) || se.Status != 404 {
		t.Fatalf("no transcript: %v", err)
	}
	if _, err := a.Content(ctx, client, map[string]string{"api_token": "tok-1"}, provider.KindTranscript, "x"); err == nil {
		t.Fatal("content without the connection's client")
	}
	for _, r := range f.Requests() {
		if strings.Contains(r, "webhook") {
			t.Fatalf("asked about webhooks: %s", r)
		}
	}

	f.Limit = true
	_, err = a.Content(ctx, client, creds, provider.KindSummary, "ua9-grq-prd-1793095200.1001")
	var limited *provider.RateLimited
	if !errors.As(err, &limited) || limited.RetryAfter != time.Minute {
		t.Fatalf("throttled: %v", err)
	}
}
