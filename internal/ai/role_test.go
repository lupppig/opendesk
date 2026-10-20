package ai

import (
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/domain"
)

// A side conversation's messages are private like notes, and the other side's answers are
// never taken as the team's own word.
func TestRoleOfSideConversation(t *testing.T) {
	th := uuid.New()
	cases := []struct {
		m    domain.Message
		want string
	}{
		{domain.Message{Kind: "note", Direction: "inbound", ThreadID: &th}, "third_party"},
		{domain.Message{Kind: "note", Direction: "outbound", ThreadID: &th}, "side_conversation"},
		{domain.Message{Kind: "note", Direction: "outbound"}, "internal_note"},
		{domain.Message{Kind: "reply", Direction: "inbound"}, "customer"},
	}
	for _, c := range cases {
		if got := role(c.m); got != c.want {
			t.Errorf("role(%s %s, thread %v) = %q, want %q", c.m.Kind, c.m.Direction, c.m.ThreadID != nil, got, c.want)
		}
	}
}
