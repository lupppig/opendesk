package discord

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestConfigureAndHealth(t *testing.T) {
	webhook := "https://example.test/hooks/discord/1"
	configured := false
	// The server already has a "status" command of its own: it stays as it is.
	registered := map[string]bool{"status": true}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/applications/@me" && r.Method == http.MethodGet:
			endpoint := ""
			if configured {
				endpoint = webhook
			}
			_, _ = w.Write([]byte(`{"id":"A1","interactions_endpoint_url":"` + endpoint + `"}`))
		case r.URL.Path == "/applications/@me" && r.Method == http.MethodPatch:
			configured = true
			_, _ = w.Write([]byte(`{"id":"A1"}`))
		case strings.HasSuffix(r.URL.Path, "/commands") && r.Method == http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			registered[body.Name] = true
			posts++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/commands") && r.Method == http.MethodGet:
			items := make([]map[string]string, 0, len(registered))
			for name := range registered {
				items = append(items, map[string]string{"name": name})
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := Adapter{BaseURL: server.URL}
	credentials := map[string]string{"bot_token": "token", "guild_id": "G1"}
	setup := channel.Setup{WebhookURL: webhook}
	if err := a.Configure(context.Background(), server.Client(), credentials, setup); err != nil {
		t.Fatal(err)
	}
	if !configured || len(registered) != len(commands) {
		t.Fatalf("configured=%v commands=%v", configured, registered)
	}
	if posts != len(commands)-1 {
		t.Fatalf("registered %d commands, want only the %d missing", posts, len(commands)-1)
	}
	health, err := a.Health(context.Background(), server.Client(), credentials, setup)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("health: %+v, %v", health, err)
	}
	// Configuring again registers nothing.
	if err := a.Configure(context.Background(), server.Client(), credentials, setup); err != nil || posts != len(commands)-1 {
		t.Fatalf("configured again: %d posts, %v", posts, err)
	}
}

func TestDiscoverAccountsAndChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me/guilds":
			_, _ = w.Write([]byte(`[{"id":"G1","name":"Help team"}]`))
		case "/guilds/G1/channels":
			_, _ = w.Write([]byte(`[{"id":"C1","name":"support","type":0},{"id":"C2","name":"voice","type":2}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := Adapter{BaseURL: server.URL}
	accounts, err := a.DiscoverAccounts(context.Background(), server.Client(), map[string]string{"bot_token": "token"})
	if err != nil || len(accounts) != 1 || accounts[0].ID != "G1" {
		t.Fatalf("accounts: %+v, %v", accounts, err)
	}
	items, err := a.Discover(context.Background(), server.Client(), map[string]string{"bot_token": "token", "guild_id": "G1"})
	if err != nil || len(items) != 1 || items[0].ID != "C1" {
		t.Fatalf("channels: %+v, %v", items, err)
	}
}

func TestVerifyAndParseCommand(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"I1","guild_id":"G1","channel_id":"TH1","type":2,"member":{"user":{"id":"U1"}},"data":{"name":"reply","options":[{"name":"text","value":"Hello"}]}}`)
	ts := "123"
	sig := ed25519.Sign(priv, append([]byte(ts), body...))
	h := http.Header{"X-Signature-Timestamp": []string{ts}, "X-Signature-Ed25519": []string{hex.EncodeToString(sig)}}
	if err = (Adapter{}).Verify(h, body, map[string]string{"public_key": hex.EncodeToString(pub)}); err != nil {
		t.Fatal(err)
	}
	result, err := (Adapter{}).Parse(h, body)
	if err != nil {
		t.Fatal(err)
	}
	events := result.Events
	if len(events) != 1 || events[0].AccountID != "G1" || events[0].ExternalUserID != "U1" || events[0].Command != "reply" || events[0].Text != "Hello" || result.Status != http.StatusOK {
		t.Fatalf("event: %+v", events)
	}
	body[0] ^= 1
	if err = (Adapter{}).Verify(h, body, map[string]string{"public_key": hex.EncodeToString(pub)}); err == nil {
		t.Fatal("accepted invalid signature")
	}
}

func TestParsePing(t *testing.T) {
	result, err := (Adapter{}).Parse(http.Header{}, []byte(`{"id":"I1","type":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != http.StatusOK || string(result.Body) != `{"type":1}` || len(result.Events) != 0 {
		t.Fatalf("result: %+v", result)
	}
}

func TestParseAskCommandAsConversation(t *testing.T) {
	body := []byte(`{"id":"I2","guild_id":"G1","channel_id":"TH1","type":2,"member":{"user":{"id":"U1"}},"data":{"name":"ask","options":[{"name":"text","value":"Summarize this"}]}}`)
	result, err := (Adapter{}).Parse(http.Header{}, body)
	if err != nil || len(result.Events) != 1 || result.Events[0].Command != "chat" || result.Events[0].Text != "Summarize this" {
		t.Fatalf("event: %+v, %v", result.Events, err)
	}
}

func TestParseComponentInteraction(t *testing.T) {
	body := []byte(`{"id":"I3","guild_id":"G1","channel_id":"TH1","type":3,"member":{"user":{"id":"U1"}},"data":{"custom_id":"opendesk:priority","values":["urgent"]},"message":{"id":"M1"}}`)
	result, err := (Adapter{}).Parse(http.Header{}, body)
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("parse: %+v, %v", result, err)
	}
	event := result.Events[0]
	if event.Command != "priority" || event.Value != "urgent" || event.SourceMessageID != "M1" || string(result.Body) != `{"type":6}` || !result.Control {
		t.Fatalf("component: %+v body=%s", event, result.Body)
	}

	body = []byte(`{"id":"I4","application_id":"A1","token":"tok","guild_id":"G1","channel_id":"TH1","type":3,"member":{"user":{"id":"U1"}},"data":{"custom_id":"opendesk:confirm:n0nce"},"message":{"id":"M2"}}`)
	result, _ = (Adapter{}).Parse(http.Header{}, body)
	if len(result.Events) != 1 || result.Events[0].Command != "confirm" || result.Events[0].Value != "n0nce" || result.Events[0].Respond != "A1/tok" {
		t.Fatalf("confirm: %+v", result.Events)
	}

	body = []byte(`{"id":"I5","guild_id":"G1","channel_id":"TH1","type":3,"member":{"user":{"id":"U1"}},"data":{"custom_id":"other:thing"},"message":{"id":"M2"}}`)
	result, _ = (Adapter{}).Parse(http.Header{}, body)
	if len(result.Events) != 0 || string(result.Body) != `{"type":6}` {
		t.Fatalf("unknown component: %+v", result)
	}
}

func TestEditsInArchivedThreadsAreSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":50083,"message":"Thread is archived"}`))
	}))
	defer server.Close()
	a := Adapter{BaseURL: server.URL}
	credentials := map[string]string{"bot_token": "token", "channel_id": "TH1"}
	if err := a.Update(context.Background(), server.Client(), credentials, "M1", channel.Ticket{Number: 1}); err != nil {
		t.Fatalf("card: %v", err)
	}
	if err := a.ClearActions(context.Background(), server.Client(), credentials, channel.Inbound{SourceMessageID: "M2"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
}

func TestRichMutationsUseTicketThreadChannel(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	a := Adapter{BaseURL: server.URL}
	credentials := map[string]string{"bot_token": "token", "channel_id": "PARENT"}
	if err := a.UpdateRich(context.Background(), server.Client(), credentials, "THREAD", "M1", channel.RichMessage{Text: "edited"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteRich(context.Background(), server.Client(), credentials, "THREAD", "M1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"PATCH /channels/THREAD/messages/M1", "DELETE /channels/THREAD/messages/M1"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestRichPayloadKeepsImageEmbedsEnabled(t *testing.T) {
	payload := richPayload(channel.RichMessage{Text: "Screenshot", Attachments: []channel.Attachment{{
		Name: "screen.png", SizeBytes: 42, URL: "https://api.test/attachment/1", Preview: true,
	}}})
	if _, suppressed := payload["flags"]; suppressed {
		t.Fatalf("payload suppresses its own embeds: %#v", payload)
	}
	if embeds := payload["embeds"].([]any); len(embeds) != 1 {
		t.Fatalf("embeds = %#v", embeds)
	}
}

func TestPostReturnsProviderRateLimitDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"retry_after":2.5}`))
	}))
	defer server.Close()

	err := (Adapter{BaseURL: server.URL}).Post(context.Background(), server.Client(), map[string]string{"bot_token": "token", "channel_id": "C1"}, "T1", "hello")
	var limited *channel.RateLimited
	if !errors.As(err, &limited) || limited.RetryAfter != 2500*time.Millisecond {
		t.Fatalf("post error = %v, want provider rate limit of 2.5s", err)
	}
}

func TestTicketPayloadHasNativeControls(t *testing.T) {
	payload := ticketPayload(channel.Ticket{Number: 42, Subject: "Printer", Customer: "Rae", Assignee: "Jo", Status: "open", Priority: "high", URL: "https://app.test/t/42"})
	components, ok := payload["components"].([]any)
	if !ok || len(components) != 3 {
		t.Fatalf("components: %#v", payload["components"])
	}
}

func TestConfigureLeavesAnotherConnectionsEndpoint(t *testing.T) {
	patched := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/applications/@me" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"A1","interactions_endpoint_url":"https://example.test/hooks/discord/other"}`))
		case r.Method == http.MethodPatch:
			patched = true
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	a := Adapter{BaseURL: server.URL}
	setup := channel.Setup{WebhookURL: "https://example.test/hooks/discord/mine", Foreign: func(url string) bool { return strings.HasSuffix(url, "/other") }}
	credentials := map[string]string{"bot_token": "token", "guild_id": "G1"}
	if err := a.Configure(context.Background(), server.Client(), credentials, setup); !errors.Is(err, channel.ErrInUse) || patched {
		t.Fatalf("configure: err=%v patched=%v", err, patched)
	}
	if _, err := a.Health(context.Background(), server.Client(), credentials, setup); !errors.Is(err, channel.ErrInUse) {
		t.Fatalf("health: %v", err)
	}
	setup.Foreign = func(string) bool { return false }
	if err := a.Configure(context.Background(), server.Client(), credentials, setup); err != nil || !patched {
		t.Fatalf("a stale endpoint was not replaced: err=%v patched=%v", err, patched)
	}
}
