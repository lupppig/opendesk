package supportchannels

import (
	"testing"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

func TestMessageCreatedTextDecodesEventPayload(t *testing.T) {
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
			got, err := messageCreatedText([]byte(tt.data), tt.notes)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMessageCreatedTextRejectsMalformedPayload(t *testing.T) {
	for _, data := range []string{`{"message":`, `{}`} {
		if _, err := messageCreatedText([]byte(data), true); err == nil {
			t.Fatalf("accepted malformed payload %q", data)
		}
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
