package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestDiscoverReturnsOnlyJoinedActiveChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.list" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"support","is_member":true},{"id":"C2","name":"private","is_member":false},{"id":"C3","name":"old","is_member":true,"is_archived":true}],"response_metadata":{"next_cursor":""}}`))
	}))
	defer server.Close()
	items, err := (Adapter{BaseURL: server.URL}).Discover(context.Background(), server.Client(), map[string]string{"bot_token": "token"})
	if err != nil || len(items) != 1 || items[0].ID != "C1" || items[0].Name != "support" {
		t.Fatalf("destinations: %+v, %v", items, err)
	}
}

func TestVerifyAndParseThreadCommand(t *testing.T) {
	body := []byte(`{"event_id":"Ev1","team_id":"T1","event":{"type":"message","user":"U1","channel":"C1","thread_ts":"10.1","ts":"10.2","text":"/reply Thanks for writing"}}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	secret := "secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	h := http.Header{"X-Slack-Request-Timestamp": []string{ts}, "X-Slack-Signature": []string{"v0=" + hex.EncodeToString(mac.Sum(nil))}}
	if err := (Adapter{}).Verify(h, body, map[string]string{"signing_secret": secret}); err != nil {
		t.Fatal(err)
	}
	result, err := (Adapter{}).Parse(h, body)
	if err != nil {
		t.Fatal(err)
	}
	events := result.Events
	if len(events) != 1 || events[0].Command != "reply" || events[0].Text != "Thanks for writing" || events[0].ExternalThreadID != "10.1" {
		t.Fatalf("event: %+v", events)
	}
	h.Set("X-Slack-Signature", "v0=bad")
	if err := (Adapter{}).Verify(h, body, map[string]string{"signing_secret": secret}); err == nil {
		t.Fatal("accepted invalid signature")
	}
}

func TestParseSlashCommandNamesTheTicket(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	tests := []struct {
		text, cmd, value string
		number           int64
	}{
		{"123 status solved", "status", "solved", 123},
		{"#42 reply Thanks for waiting", "reply", "Thanks for waiting", 42},
		{"7 what is this about?", "chat", "what is this about?", 7},
		{"link ABCDEF1234", "link", "ABCDEF1234", 0},
		{"note no number", "note", "no number", 0},
	}
	for _, tt := range tests {
		body := url.Values{"command": {"/opendesk"}, "text": {tt.text}, "trigger_id": {"tr1"}, "team_id": {"T1"}, "channel_id": {"C1"}, "user_id": {"U1"}}.Encode()
		result, err := (Adapter{}).Parse(h, []byte(body))
		if err != nil || len(result.Events) != 1 {
			t.Fatalf("%q: %+v, %v", tt.text, result.Events, err)
		}
		e := result.Events[0]
		if e.Command != tt.cmd || e.Value != tt.value || e.TicketNumber != tt.number || e.ExternalThreadID != "" || e.ExternalChannelID != "C1" {
			t.Fatalf("%q: event %+v", tt.text, e)
		}
	}
}

func TestParseURLVerification(t *testing.T) {
	result, err := (Adapter{}).Parse(http.Header{}, []byte(`{"type":"url_verification","challenge":"challenge-value"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != http.StatusOK || string(result.Body) != `{"challenge":"challenge-value"}` || len(result.Events) != 0 {
		t.Fatalf("result: %+v", result)
	}
}

func TestParseMentionAndIgnoreConversation(t *testing.T) {
	mention := []byte(`{"event_id":"Ev2","team_id":"T1","event":{"type":"app_mention","user":"U1","channel":"C1","thread_ts":"10.1","ts":"10.3","text":"<@UBOT> note Checking this"}}`)
	result, err := (Adapter{}).Parse(http.Header{}, mention)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].Command != "note" || result.Events[0].Text != "Checking this" {
		t.Fatalf("event: %+v", result.Events)
	}
	natural := []byte(`{"event_id":"Ev4","team_id":"T1","event":{"type":"app_mention","user":"U1","channel":"C1","thread_ts":"10.1","ts":"10.5","text":"<@UBOT> can you summarize this ticket?"}}`)
	result, err = (Adapter{}).Parse(http.Header{}, natural)
	if err != nil || len(result.Events) != 1 || result.Events[0].Command != "chat" || result.Events[0].Text != "can you summarize this ticket?" {
		t.Fatalf("natural event: %+v, %v", result.Events, err)
	}
	conversation := []byte(`{"event_id":"Ev3","team_id":"T1","event":{"type":"message","user":"U1","channel":"C1","thread_ts":"10.1","ts":"10.4","text":"reply whenever you have time"}}`)
	result, err = (Adapter{}).Parse(http.Header{}, conversation)
	if err != nil || len(result.Events) != 0 {
		t.Fatalf("casual message became command: %+v, %v", result.Events, err)
	}
}
