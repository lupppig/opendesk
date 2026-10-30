package supportchannels

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestDispatchWorkerSnoozesForProviderRateLimit(t *testing.T) {
	const delay = 25 * time.Second
	err := snoozeRateLimit(fmt.Errorf("provider post: %w", &channel.RateLimited{RetryAfter: delay}))

	var snooze *rivertype.JobSnoozeError
	if !errors.As(err, &snooze) || snooze.Duration != delay {
		t.Fatalf("dispatch error = %v, want a %s snooze", err, delay)
	}
}

func TestDispatchWorkerPreservesOtherErrors(t *testing.T) {
	want := errors.New("provider unavailable")
	if got := snoozeRateLimit(want); got != want {
		t.Fatalf("dispatch error = %v, want original error", got)
	}
}

func TestMessageEventDecodesEventPayload(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		notes bool
		want  string
	}{
		{"customer", `{"message":{"kind":"reply","direction":"inbound","body_text":"I need help"}}`, false, "Customer: I need help"},
		{"agent", `{"message":{"kind":"reply","direction":"outbound","body_text":"On it"}}`, false, "Agent: On it"},
		{"note included", `{"message":{"kind":"note","direction":"outbound","body_text":"Escalate this"}}`, true, "Internal note: Escalate this"},
		{"note left out", `{"message":{"kind":"note","direction":"outbound","body_text":"Escalate this"}}`, false, ""},
		{"empty body", `{"message":{"kind":"reply","direction":"outbound","body_text":"  "}}`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, source, err := messageEvent([]byte(tt.data), shown{notes: tt.notes, customer: true, agent: true}, 3500, "https://app.test/ticket")
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != tt.want || (source != nil) != (tt.want != "") {
				t.Fatalf("message = %+v, source = %+v, want %q", got, source, tt.want)
			}
		})
	}
}

func TestMessageEventRejectsMalformedPayload(t *testing.T) {
	for _, data := range []string{`{"message":`, `{}`} {
		if _, _, err := messageEvent([]byte(data), shown{notes: true, customer: true, agent: true}, 3500, "https://app.test/ticket"); err == nil {
			t.Fatalf("accepted malformed payload %q", data)
		}
	}
}

func TestMessageEventKeepsAttachmentsAndLinksTruncatedText(t *testing.T) {
	id := "0192f0c4-0000-7000-8000-000000000000"
	data := `{"message":{"id":"0192f0c4-0000-7000-8000-000000000001","kind":"reply","direction":"inbound","body_text":"` + strings.Repeat("long ", 200) + `","attachments":[{"id":"` + id + `","filename":"screen.png","content_type":"image/png","size_bytes":42}]}}`
	got, source, err := messageEvent([]byte(data), shown{customer: true, agent: true}, 500, "https://app.test/ticket")
	if err != nil {
		t.Fatal(err)
	}
	if source == nil || len([]rune(got.Text)) > 500 || got.ViewURL == "" || len(got.Attachments) != 1 || got.Attachments[0].Name != "screen.png" || !strings.Contains(got.Attachments[0].URL, id) {
		t.Fatalf("rich message = %+v, source = %+v", got, source)
	}
}

func TestMessageEventKeepsEmptyDeletionMarker(t *testing.T) {
	data := `{"message":{"id":"0192f0c4-0000-7000-8000-000000000001","kind":"note","direction":"outbound","deleted_at":"2026-10-24T12:00:00Z"}}`
	got, source, err := messageEvent([]byte(data), shown{notes: true, customer: true, agent: true}, 3500, "https://app.test/ticket")
	if err != nil {
		t.Fatal(err)
	}
	if source == nil || source.DeletedAt == nil || !source.DeletedAt.Equal(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)) || got.Text != "Internal note: " {
		t.Fatalf("message = %+v, source = %+v", got, source)
	}
}

func TestAddressedOnlyByTheConnectionsOwnHandle(t *testing.T) {
	c := connection{ExternalAccountHandle: "@opendeskkbot"}
	in := channel.Inbound{Command: "chat", Mention: "@OpendeskKBot", Text: "@OpendeskKBot create a ticket", Value: "@OpendeskKBot create a ticket"}
	addressed(c, &in)
	if !in.Directed || in.Text != "create a ticket" || in.Value != "create a ticket" {
		t.Fatalf("bot mention: %+v", in)
	}
	other := channel.Inbound{Command: "chat", Mention: "@alice", Text: "@alice can you look?"}
	addressed(c, &other)
	if other.Directed || other.Text != "@alice can you look?" {
		t.Fatalf("someone else's mention: %+v", other)
	}
	reply := channel.Inbound{Command: "chat", Mention: "@opendeskkbot", Text: "confirm"}
	addressed(c, &reply)
	if !reply.Directed || reply.Text != "confirm" {
		t.Fatalf("reply to the bot: %+v", reply)
	}
}
