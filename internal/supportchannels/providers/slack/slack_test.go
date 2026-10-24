package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
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

func TestParseRootMentionStartsConversationThread(t *testing.T) {
	body := []byte(`{"type":"event_callback","event_id":"Ev5","team_id":"T1","event":{"type":"app_mention","user":"U1","channel":"C1","ts":"20.2","text":"<@UBOT> create a ticket for the broken printer"}}`)
	got, err := (Adapter{}).Parse(http.Header{}, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("events = %+v", got.Events)
	}
	e := got.Events[0]
	if e.Command != "chat" || e.ExternalThreadID != "20.2" || e.Text != "create a ticket for the broken printer" || !e.Directed {
		t.Fatalf("event = %+v", e)
	}
}

func TestParseAttachmentAndNoteEdit(t *testing.T) {
	created := []byte(`{"type":"event_callback","event_id":"E1","team_id":"T1","event":{"type":"app_mention","user":"U1","channel":"C1","thread_ts":"10","ts":"11","text":"<@B1> note see this","files":[{"id":"F1","name":"screen.png","mimetype":"image/png","size":42,"url_private_download":"https://files.slack.com/files-pri/F1"}]}}`)
	result, err := (Adapter{}).Parse(http.Header{}, created)
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("created: %+v, %v", result, err)
	}
	in := result.Events[0]
	if in.Command != "note" || in.Text != "see this" || in.ExternalMessageID != "11" || len(in.Files) != 1 || in.Files[0].Name != "screen.png" {
		t.Fatalf("created event: %+v", in)
	}

	edited := []byte(`{"type":"event_callback","event_id":"E2","team_id":"T1","event":{"type":"message","subtype":"message_changed","channel":"C1","message":{"user":"U1","thread_ts":"10","ts":"11","text":"<@B1> note corrected"}}}`)
	result, err = (Adapter{}).Parse(http.Header{}, edited)
	if err != nil || len(result.Events) != 1 || result.Events[0].Mutation != "edit" || result.Events[0].Text != "corrected" {
		t.Fatalf("edited: %+v, %v", result, err)
	}

	deleted := []byte(`{"type":"event_callback","event_id":"E3","team_id":"T1","event":{"type":"message","subtype":"message_deleted","channel":"C1","deleted_ts":"11","previous_message":{"user":"U1","thread_ts":"10","ts":"11","text":"<@B1> note corrected"}}}`)
	result, err = (Adapter{}).Parse(http.Header{}, deleted)
	if err != nil || len(result.Events) != 1 || result.Events[0].Mutation != "delete" || result.Events[0].ExternalMessageID != "11" || result.Events[0].ExternalUserID != "U1" {
		t.Fatalf("deleted: %+v, %v", result, err)
	}
}

func TestRichPayloadKeepsViewLinkWithinSlackBlockLimit(t *testing.T) {
	payload := richPayload("C1", "T1", "", channel.RichMessage{
		Text:    strings.Repeat("a", 2500),
		ViewURL: "https://app.test/ticket/1",
		Attachments: []channel.Attachment{
			{Name: strings.Repeat("n", 200), SizeBytes: 42, URL: "https://api.test/file/1", Preview: true},
		},
	})
	text := payload["text"].(string)
	if len([]rune(text)) > 3000 || !strings.Contains(text, "View in opendesk") {
		t.Fatalf("text length = %d, text = %q", len([]rune(text)), text)
	}
	blocks := payload["blocks"].([]any)
	section := blocks[0].(map[string]any)["text"].(map[string]string)["text"]
	if section != text {
		t.Fatal("section and fallback text differ")
	}
}

func TestParseBlockActions(t *testing.T) {
	payload := `{"type":"block_actions","trigger_id":"T9","response_url":"https://hooks.slack.com/actions/T/1/x","team":{"id":"TEAM"},"user":{"id":"U1"},"channel":{"id":"C1"},"container":{"message_ts":"10.1","thread_ts":"10.1"},"actions":[{"action_id":"opendesk:status","selected_option":{"value":"pending"}}]}`
	body := []byte(url.Values{"payload": {payload}}.Encode())
	result, err := (Adapter{}).Parse(http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}, body)
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("parse: %+v, %v", result, err)
	}
	event := result.Events[0]
	if event.EventID != "T9" || event.Command != "status" || event.Value != "pending" || event.ExternalThreadID != "10.1" || event.SourceMessageID != "10.1" || event.Respond == "" || !result.Control {
		t.Fatalf("event: %+v", event)
	}

	payload = `{"type":"block_actions","trigger_id":"T10","team":{"id":"TEAM"},"user":{"id":"U1"},"channel":{"id":"C1"},"message":{"ts":"11.1","text":"I'll close this ticket."},"container":{"message_ts":"11.1","thread_ts":"10.1"},"actions":[{"action_id":"opendesk:confirm","value":"n0nce"}]}`
	body = []byte(url.Values{"payload": {payload}}.Encode())
	result, err = (Adapter{}).Parse(http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}, body)
	if err != nil || result.Events[0].Command != "confirm" || result.Events[0].Value != "n0nce" || result.Events[0].SourceMessageID != "11.1" || result.Events[0].SourceText != "I'll close this ticket." {
		t.Fatalf("confirm: %+v, %v", result.Events, err)
	}

	// The link button, and any action that isn't one of opendesk's controls, is
	// acknowledged without a command.
	for _, action := range []string{"opendesk:open", "opendesk:delete", "someone_elses"} {
		payload = `{"type":"block_actions","trigger_id":"T11","team":{"id":"TEAM"},"user":{"id":"U1"},"channel":{"id":"C1"},"container":{"message_ts":"10.1"},"actions":[{"action_id":"` + action + `"}]}`
		body = []byte(url.Values{"payload": {payload}}.Encode())
		result, err = (Adapter{}).Parse(http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}, body)
		if err != nil || len(result.Events) != 0 {
			t.Fatalf("%s: %+v, %v", action, result, err)
		}
	}
}

func TestOpenButtonHasItsOwnActionID(t *testing.T) {
	payload := ticketPayload("C1", "", channel.Ticket{Number: 42, Subject: "Printer", URL: "https://app.test/t/42"})
	blocks := payload["blocks"].([]any)
	elements := blocks[2].(map[string]any)["elements"].([]any)
	open := elements[len(elements)-1].(map[string]any)
	if open["url"] != "https://app.test/t/42" || open["action_id"] != "opendesk:open" {
		t.Fatalf("open button: %#v", open)
	}
}

func TestRespondOnlyToSlackHooks(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer server.Close()
	a := Adapter{BaseURL: server.URL}
	if err := a.Respond(context.Background(), server.Client(), nil, server.URL+"/actions/1", "That ticket is not available."); err != nil {
		t.Fatal(err)
	}
	if got["response_type"] != "ephemeral" || got["text"] != "That ticket is not available." {
		t.Fatalf("response: %#v", got)
	}
	if err := (Adapter{}).Respond(context.Background(), server.Client(), nil, server.URL+"/actions/1", "x"); err == nil {
		t.Fatal("answered through a URL that isn't Slack's")
	}
}

func TestTicketPayloadHasNativeControls(t *testing.T) {
	payload := ticketPayload("C1", "", channel.Ticket{Number: 42, Subject: "Printer", Customer: "Rae", Assignee: "Jo", Status: "open", Priority: "high", URL: "https://app.test/t/42"})
	blocks, ok := payload["blocks"].([]any)
	if !ok || len(blocks) != 3 {
		t.Fatalf("blocks: %#v", payload["blocks"])
	}
}
