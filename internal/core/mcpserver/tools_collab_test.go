package mcpserver

import (
	"slices"
	"testing"
)

// A reply's recipients follow the composer: the sender or its Reply-To, the recipients of
// mail the account wrote, and everyone else on reply all, never the account's own addresses.
func TestReplyRecipients(t *testing.T) {
	d := &apiMailDetail{}
	d.Account.Email = "sales@acme.test"
	d.Account.Identities = append(d.Account.Identities, struct {
		Email   string `json:"email"`
		CanSend bool   `json:"can_send"`
	}{Email: "Orders@acme.test", CanSend: true})
	in := &apiMailMessage{From: mailAddress{Email: "Frank@Example.com"},
		To: []mailAddress{{Email: "orders@acme.test"}, {Email: "gina@example.com"}}, Cc: []mailAddress{{Email: "hal@example.com"}, {Email: "frank@example.com"}}}

	to, cc := replyRecipients(d, in, false)
	if !slices.Equal(to, []string{"frank@example.com"}) || len(cc) != 0 {
		t.Fatalf("reply: %v %v", to, cc)
	}
	to, cc = replyRecipients(d, in, true)
	if !slices.Equal(to, []string{"frank@example.com"}) || !slices.Equal(cc, []string{"gina@example.com", "hal@example.com"}) {
		t.Fatalf("reply all: %v %v", to, cc)
	}
	in.ReplyTo = []mailAddress{{Email: "desk@example.com"}}
	if to, _ = replyRecipients(d, in, false); !slices.Equal(to, []string{"desk@example.com"}) {
		t.Fatalf("reply-to: %v", to)
	}
	out := &apiMailMessage{Outgoing: true, From: mailAddress{Email: "sales@acme.test"}, To: []mailAddress{{Email: "ivy@example.com"}}}
	if to, _ = replyRecipients(d, out, false); !slices.Equal(to, []string{"ivy@example.com"}) {
		t.Fatalf("own mail: %v", to)
	}
}
