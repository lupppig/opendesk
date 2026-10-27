package googlechat

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestVerifyGoogleChatIDToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	audience := "https://opendesk.example/hooks/google_chat/connection"
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": audience, "email": chatIssuer, "email_verified": true,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	adapter := Adapter{KeySet: &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	credentials := map[string]string{"webhook_url": audience}
	if err := adapter.Verify(headers, nil, credentials); err != nil {
		t.Fatal(err)
	}
	credentials["webhook_url"] = "https://other.example/hooks"
	if err := adapter.Verify(headers, nil, credentials); err == nil {
		t.Fatal("accepted a token for another endpoint")
	}
}

func TestParseMessagesAndCardActions(t *testing.T) {
	adapter := Adapter{}
	parsed, err := adapter.Parse(nil, []byte(`{
		"type":"MESSAGE","eventTime":"2026-10-27T12:00:00Z",
		"user":{"name":"users/42"},"space":{"name":"spaces/room1","type":"DM"},
		"message":{"name":"spaces/room1/messages/msg1","text":"@opendesk reply Thanks",
		"argumentText":"reply Thanks","createTime":"2026-10-27T12:00:00Z",
		"thread":{"name":"spaces/room1/threads/thread1"},"sender":{"name":"users/42","type":"HUMAN"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Events) != 1 || parsed.Events[0].Command != "reply" || parsed.Events[0].Text != "Thanks" || !parsed.Events[0].Directed {
		t.Fatalf("message event: %+v", parsed)
	}

	parsed, err = adapter.Parse(nil, []byte(`{
		"type":"CARD_CLICKED","eventTime":"2026-10-27T12:01:00Z",
		"user":{"name":"users/42"},"space":{"name":"spaces/room1","type":"SPACE"},
		"message":{"name":"spaces/room1/messages/card1","text":"Confirm?",
		"thread":{"name":"spaces/room1/threads/thread1"},"sender":{"name":"users/bot","type":"BOT"}},
		"action":{"actionMethodName":"opendesk","parameters":[{"key":"command","value":"confirm"},{"key":"value","value":"nonce"}]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Control || len(parsed.Events) != 1 || parsed.Events[0].Command != "confirm" || parsed.Events[0].Value != "nonce" || parsed.Events[0].SourceMessageID != "spaces/room1/messages/card1" {
		t.Fatalf("card action: %+v", parsed)
	}

	parsed, err = adapter.Parse(nil, []byte(`{
		"type":"MESSAGE","eventTime":"2026-10-27T12:02:00Z",
		"user":{"name":"users/42"},"space":{"name":"spaces/dm1","type":"DM"},
		"message":{"name":"spaces/dm1/messages/msg2","text":"/opendesk link code123",
		"argumentText":"link code123","createTime":"2026-10-27T12:02:00Z","sender":{"name":"users/42","type":"HUMAN"},
		"slashCommand":{"commandName":"opendesk"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Events) != 1 || parsed.Events[0].Command != "link" || parsed.Events[0].Text != "code123" || parsed.Events[0].ExternalThreadID != "" {
		t.Fatalf("DM link event: %+v", parsed)
	}
}

func TestValidateAndDiscoverSpaces(t *testing.T) {
	var apiCalls int
	var tokenCalls int
	requestID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") == "" {
				t.Errorf("token request form: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-access-token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		apiCalls++
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/v1/spaces/dm1/messages" {
			var message map[string]any
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil || message["text"] != "Your account is linked." || message["thread"] != nil {
				t.Errorf("direct message body: %v, err %v", message, err)
			}
			_, _ = w.Write([]byte(`{"name":"spaces/dm1/messages/reply"}`))
			return
		}
		if r.URL.Path == "/v1/spaces/support/messages" {
			if got := r.URL.Query().Get("requestId"); !strings.HasPrefix(got, "opendesk-") {
				t.Errorf("request ID %q", got)
			} else if requestID == "" {
				requestID = got
			} else if got != requestID {
				t.Errorf("retry request ID %q, want %q", got, requestID)
			}
			var message map[string]any
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil || message["text"] == "" {
				t.Errorf("ticket message body: %v, err %v", message, err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"spaces/support/messages/root","thread":{"name":"spaces/support/threads/root"}}`))
			return
		}
		if r.URL.Path != "/v1/spaces" {
			t.Errorf("path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("filter") != "" && r.URL.Query().Get("filter") != `spaceType = "SPACE"` {
			t.Errorf("filter %q", r.URL.Query().Get("filter"))
		}
		_, _ = w.Write([]byte(`{"spaces":[{"name":"spaces/support","displayName":"Support","spaceType":"SPACE","spaceThreadingState":"THREADED_MESSAGES"}]}`))
	}))
	defer server.Close()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	account, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "opendesk-test", "private_key_id": "test", "private_key": string(keyPEM),
		"client_email": "opendesk@test.iam.gserviceaccount.com", "token_uri": "http://127.0.0.1:1/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := New()
	adapter.BaseURL = server.URL + "/v1"
	adapter.TokenURL = server.URL + "/token"
	credentials := map[string]string{"service_account_json": string(account)}
	client := &http.Client{}
	info, err := adapter.Validate(context.Background(), client, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "opendesk@test.iam.gserviceaccount.com" || info.Name != "opendesk-test" {
		t.Fatalf("account: %+v", info)
	}
	destinations, err := adapter.Discover(context.Background(), client, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if len(destinations) != 1 || destinations[0].ID != "spaces/support" || destinations[0].Name != "Support" || !destinations[0].Available {
		t.Fatalf("destinations: %+v", destinations)
	}
	if err := adapter.Post(context.Background(), client, map[string]string{"service_account_json": string(account), "channel_id": "spaces/dm1"}, "", "Your account is linked."); err != nil {
		t.Fatal(err)
	}
	thread, err := adapter.CreateThread(context.Background(), client, credentials, "spaces/support", channel.Ticket{ID: "101", Number: 101, Subject: "Order status"})
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "spaces/support/threads/root" || thread.RootID != "spaces/support/messages/root" {
		t.Fatalf("created thread: %+v", thread)
	}
	retry, err := adapter.CreateThread(context.Background(), client, credentials, "spaces/support", channel.Ticket{ID: "101", Number: 101, Subject: "Order status"})
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != thread.ID {
		t.Fatalf("retry thread: %+v, original %+v", retry, thread)
	}
	if apiCalls != 5 {
		t.Fatalf("API calls %d", apiCalls)
	}
	if tokenCalls != 1 {
		t.Fatalf("service account token requests %d", tokenCalls)
	}
}

func TestPostInteractiveUsesGoogleChatCards(t *testing.T) {
	var card []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		if r.URL.Path != "/v1/spaces/room/messages" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		card, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"spaces/room/messages/created"}`))
	}))
	defer server.Close()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	credentials := map[string]string{"service_account_json": `{"type":"service_account","project_id":"test","private_key_id":"id","private_key":"` + strings.ReplaceAll(string(keyPEM), "\n", "\\n") + `","client_email":"opendesk@test.iam.gserviceaccount.com","token_uri":"http://127.0.0.1:1/token"}`}
	adapter := New()
	adapter.BaseURL = server.URL + "/v1"
	adapter.TokenURL = server.URL + "/token"
	ref, err := adapter.PostInteractive(context.Background(), &http.Client{}, credentials, "spaces/room/threads/thread", channel.Message{
		Text: "Close this ticket?", Actions: []channel.Action{{Command: "confirm", Value: "nonce", Label: "Confirm", Style: "primary"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "spaces/room/messages/created" || !strings.Contains(string(card), `"function":"opendesk"`) || !strings.Contains(string(card), `"value":"nonce"`) {
		t.Fatalf("card response %s, ref %+v", card, ref)
	}
}

func TestParseExplicitTicketNumber(t *testing.T) {
	number, command, text := parseCommand("#42 status solved", true)
	if number != 42 || command != "status" || text != "solved" {
		t.Fatalf("parsed %d %q %q", number, command, text)
	}
}
