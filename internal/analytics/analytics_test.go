package analytics

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/posthog/posthog-go"
)

const (
	testDistinctID  = "f2ac6d95-85c7-4ae6-9d38-5b6ed4a84560"
	testWorkspaceID = "56df7b43-fb68-4f39-bef9-813d985d4be4"
)

type recordingSink struct {
	messages []posthog.Message
}

func (s *recordingSink) Enqueue(message posthog.Message) error {
	s.messages = append(s.messages, message)
	return nil
}

func (s *recordingSink) Close() error { return nil }

func testClient() (*Client, *recordingSink) {
	sink := &recordingSink{}
	return &Client{ph: sink}, sink
}

func TestCaptureAllowsKnownEvents(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		properties map[string]any
		want       posthog.Properties
	}{
		{
			name: "signup", event: "user_signed_up",
			properties: map[string]any{"method": "google", "invited": false},
			want:       posthog.Properties{"method": "google", "invited": false},
		},
		{
			name: "login", event: "user_logged_in",
			properties: map[string]any{"method": "passkey", "mfa": true},
			want:       posthog.Properties{"method": "passkey", "mfa": true},
		},
		{name: "workspace", event: "workspace_created", want: posthog.Properties{}},
		{
			name: "invitation", event: "member_invited",
			properties: map[string]any{"role": "agent"},
			want:       posthog.Properties{"role": "agent"},
		},
		{
			name: "api key", event: "api_key_created",
			properties: map[string]any{"scope_count": 3, "expires": true},
			want:       posthog.Properties{"scope_count": 3, "expires": true},
		},
		{
			name: "MCP client", event: "ai_client_connected",
			properties: map[string]any{"scope_count": 7},
			want:       posthog.Properties{"scope_count": 7},
		},
		{
			name: "webhook", event: "webhook_created",
			properties: map[string]any{"all_events": false, "event_count": 2},
			want:       posthog.Properties{"all_events": false, "event_count": 2},
		},
		{
			name: "plan limit", event: "plan_limit_hit",
			properties: map[string]any{"resource": "members"},
			want:       posthog.Properties{"resource": "members"},
		},
		{
			name: "AI feature", event: "ai_feature_used",
			properties: map[string]any{"feature": "summarize", "own_key": true},
			want:       posthog.Properties{"feature": "summarize", "own_key": true},
		},
		{
			name: "top up", event: "ai_top_up_purchased",
			properties: map[string]any{"usd": 10.0},
			want:       posthog.Properties{"usd": 10.0},
		},
		{
			name: "recharge", event: "ai_recharged",
			properties: map[string]any{"usd": 2.5},
			want:       posthog.Properties{"usd": 2.5},
		},
		{
			name: "plan change", event: "subscription_plan_changed",
			properties: map[string]any{"from": "pro", "to": "collab", "interval": "year", "seats": 4},
			want:       posthog.Properties{"from": "pro", "to": "collab", "interval": "year", "seats": 4},
		},
		{
			name: "subscription start", event: "subscription_started",
			properties: map[string]any{"plan": "pro", "interval": "month", "seats": 2},
			want:       posthog.Properties{"plan": "pro", "interval": "month", "seats": 2},
		},
		{
			name: "subscription cancel", event: "subscription_canceled",
			properties: map[string]any{"status": "canceled", "interval": "year", "seats": 2},
			want:       posthog.Properties{"status": "canceled", "interval": "year", "seats": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, sink := testClient()
			client.Capture(testDistinctID, testWorkspaceID, tt.event, tt.properties)
			if len(sink.messages) != 1 {
				t.Fatalf("queued %d messages, want 1", len(sink.messages))
			}
			capture, ok := sink.messages[0].(posthog.Capture)
			if !ok {
				t.Fatalf("queued message has type %T, want posthog.Capture", sink.messages[0])
			}
			if capture.Event != tt.event {
				t.Fatalf("event = %q, want %q", capture.Event, tt.event)
			}
			if !reflect.DeepEqual(capture.Properties, tt.want) {
				t.Fatalf("properties = %#v, want %#v", capture.Properties, tt.want)
			}
		})
	}
}

func TestCaptureDropsUnapprovedProperties(t *testing.T) {
	client, sink := testClient()
	client.Capture(testDistinctID, testWorkspaceID, "user_logged_in", map[string]any{
		"method":  "password",
		"mfa":     true,
		"email":   "person@example.com",
		"message": "customer message text",
		"token":   "credential-value",
		"nested":  map[string]any{"body": "nested message text"},
	})
	client.Capture(testDistinctID, testWorkspaceID, "ai_feature_used", map[string]any{
		"feature": "customer message text",
		"model":   "custom model with private configuration",
		"own_key": false,
	})
	client.Capture(testDistinctID, testWorkspaceID, "unregistered_event", map[string]any{"message": "private"})

	if len(sink.messages) != 2 {
		t.Fatalf("queued %d messages, want 2 approved events", len(sink.messages))
	}
	login := sink.messages[0].(posthog.Capture)
	if want := (posthog.Properties{"method": "password", "mfa": true}); !reflect.DeepEqual(login.Properties, want) {
		t.Fatalf("login properties = %#v, want %#v", login.Properties, want)
	}
	ai := sink.messages[1].(posthog.Capture)
	if want := (posthog.Properties{"own_key": false}); !reflect.DeepEqual(ai.Properties, want) {
		t.Fatalf("AI properties = %#v, want %#v", ai.Properties, want)
	}
}

func TestCaptureAcceptsWorkspaceDistinctID(t *testing.T) {
	client, sink := testClient()
	client.Capture("workspace:"+testWorkspaceID, testWorkspaceID, "ai_feature_used", map[string]any{
		"feature": "answer",
		"own_key": false,
	})
	if len(sink.messages) != 1 {
		t.Fatalf("queued %d messages, want 1", len(sink.messages))
	}
	capture := sink.messages[0].(posthog.Capture)
	if capture.DistinctId != "workspace:"+testWorkspaceID {
		t.Fatalf("distinct ID = %q, want workspace-prefixed UUID", capture.DistinctId)
	}
	if got := capture.Groups["workspace"]; got != testWorkspaceID {
		t.Fatalf("workspace group = %#v, want %q", got, testWorkspaceID)
	}
}

func TestCaptureRejectsInvalidIdentifiersAndUnknownEvents(t *testing.T) {
	client, sink := testClient()
	client.Capture("person@example.com", testWorkspaceID, "user_logged_in", map[string]any{"method": "password"})
	client.Capture(testDistinctID, "person@example.com", "user_logged_in", map[string]any{"method": "password"})
	client.Capture(testDistinctID, testWorkspaceID, "unknown", nil)

	client.Capture(testDistinctID, testWorkspaceID, "user_logged_in", map[string]any{"method": "password", "mfa": true})
	if len(sink.messages) != 1 {
		t.Fatalf("queued %d messages, want only the event with valid identifiers", len(sink.messages))
	}
	capture, ok := sink.messages[0].(posthog.Capture)
	if !ok {
		t.Fatalf("queued message has type %T, want posthog.Capture", sink.messages[0])
	}
	if want := (posthog.Properties{"method": "password", "mfa": true}); !reflect.DeepEqual(capture.Properties, want) {
		t.Fatalf("properties = %#v, want %#v", capture.Properties, want)
	}
}

func TestPropertiesRejectNegativeCountsAndAmounts(t *testing.T) {
	tests := []struct {
		event    string
		property string
		value    any
	}{
		{event: "api_key_created", property: "scope_count", value: -1},
		{event: "ai_top_up_purchased", property: "usd", value: -1.0},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			properties, refused, ok := safeProperties(tt.event, map[string]any{tt.property: tt.value})
			if !ok || len(properties) != 0 || !reflect.DeepEqual(refused, []string{tt.property}) {
				t.Fatalf("properties = %#v, event accepted = %t; want empty properties and accepted event", properties, ok)
			}
		})
	}
}

// role stands in for auth.Role: call sites pass named string types.
type role string

// TestCallSiteShapesKeepEveryProperty sends what the app's call sites send, with the
// value types they use, and expects nothing refused.
func TestCallSiteShapesKeepEveryProperty(t *testing.T) {
	tests := []struct {
		event string
		props map[string]any
	}{
		{"user_signed_up", map[string]any{"method": "oidc", "invited": false}},
		{"user_logged_in", map[string]any{"method": "oidc", "mfa": true}},
		{"user_logged_in", map[string]any{"method": "passkey", "mfa": true}},
		{"member_invited", map[string]any{"role": role("agent")}},
		{"api_key_created", map[string]any{"scope_count": 3, "expires": true}},
		{"webhook_created", map[string]any{"all_events": false, "event_count": 2}},
		{"ai_feature_used", map[string]any{"feature": "draft_reply", "own_key": false}},
		{"subscription_started", map[string]any{"plan": "collab", "interval": "year", "seats": 4}},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			properties, refused, ok := safeProperties(tt.event, tt.props)
			if !ok || len(refused) != 0 || len(properties) != len(tt.props) {
				t.Fatalf("properties = %#v, refused = %v, accepted = %t", properties, refused, ok)
			}
		})
	}
}

func TestExceptionContainsNoErrorText(t *testing.T) {
	client, sink := testClient()
	client.Capture(testDistinctID, "", "$exception", map[string]any{
		"$exception_type":    "server_error",
		"$exception_message": "token=private and customer@example.com",
	})
	client.Exception(testDistinctID)

	if len(sink.messages) != 2 {
		t.Fatalf("queued %d messages, want 2 generic exceptions", len(sink.messages))
	}
	for _, message := range sink.messages {
		capture := message.(posthog.Capture)
		if capture.Event != "$exception" {
			t.Fatalf("event = %q, want $exception", capture.Event)
		}
		if want := (posthog.Properties{"$exception_type": "server_error"}); !reflect.DeepEqual(capture.Properties, want) {
			t.Fatalf("exception properties = %#v, want %#v", capture.Properties, want)
		}
	}
}

func TestDisabledClientDoesNotQueueEvents(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := New("", server.URL, nil)
	if client.Enabled() {
		t.Fatal("client enabled without POSTHOG_KEY")
	}
	client.Capture(testDistinctID, testWorkspaceID, "user_logged_in", map[string]any{"method": "password"})
	client.Exception(testDistinctID)
	client.Close()
	if got := requests.Load(); got != 0 {
		t.Fatalf("disabled telemetry sent %d requests, want 0", got)
	}
}
