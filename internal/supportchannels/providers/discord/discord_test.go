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

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestConfigureAndHealth(t *testing.T) {
	webhook := "https://example.test/hooks/discord/1"
	configured := false
	registered := map[string]bool{}
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
	health, err := a.Health(context.Background(), server.Client(), credentials, setup)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("health: %+v, %v", health, err)
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
