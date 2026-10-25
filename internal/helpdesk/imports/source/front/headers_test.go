package front

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// All fixtures are made up.

// atClock sets the clock threading headers are measured from, for one test.
func atClock(t *testing.T, at time.Time) {
	t.Helper()
	old := headerClock
	headerClock = func() time.Time { return at }
	t.Cleanup(func() { headerClock = old })
}

// firstTicket is the first ticket an import lists (cnv_1, last active mar1+1h).
func firstTicket(t *testing.T, s *front) source.Ticket {
	t.Helper()
	p, err := s.Tickets(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.Items[0]
}

func TestLoadReadsThreadingHeaders(t *testing.T) {
	f, s := setup(t)
	atClock(t, time.Unix(mar1+day, 0))
	tk := firstTicket(t, s)
	if err := s.Load(context.Background(), &tk); err != nil {
		t.Fatal(err)
	}
	in, reply := tk.Messages[0], tk.Messages[3]
	if in.MessageID != "<a1.bob@mail.example>" || in.InReplyTo != "" || len(in.References) != 0 {
		t.Errorf("inbound threading = %q %q %v", in.MessageID, in.InReplyTo, in.References)
	}
	if reply.MessageID != "<r2.leela@acme.example>" || reply.InReplyTo != "<a1.bob@mail.example>" ||
		fmt.Sprint(reply.References) != "[<a0.bob@mail.example> <a1.bob@mail.example>]" {
		t.Errorf("reply threading = %q %q %v", reply.MessageID, reply.InReplyTo, reply.References)
	}
	// Comments are notes, not emails, and the draft is left out: neither is asked for.
	for _, m := range tk.Messages[1:3] {
		if m.MessageID != "" {
			t.Errorf("note %s got a Message-ID", m.ID)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits["/messages/msg_1"] != 1 || f.hits["/messages/msg_2"] != 1 || f.hits["/messages/msg_3"] != 0 {
		t.Errorf("raw email requests = %v", f.hits)
	}
}

func TestLoadSkipsHeadersOfOldConversations(t *testing.T) {
	f, s := setup(t)
	atClock(t, time.Unix(mar1+3600, 0).Add(headerWindow+time.Minute))
	tk := firstTicket(t, s)
	if err := s.Load(context.Background(), &tk); err != nil {
		t.Fatal(err)
	}
	if tk.Messages[0].MessageID != "" {
		t.Errorf("old conversation got a Message-ID: %q", tk.Messages[0].MessageID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits["/messages/msg_1"]+f.hits["/messages/msg_2"] != 0 {
		t.Errorf("raw email requests = %v", f.hits)
	}
}

func TestLoadSkipsChatHeaders(t *testing.T) {
	f, s := setup(t)
	atClock(t, time.Unix(mar1+2*day, 0))
	chat := source.Ticket{ID: "cnv_5", UpdatedAt: time.Unix(mar1+2*day, 0)}
	if err := s.Load(context.Background(), &chat); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits["/messages/msg_9"] != 0 || chat.Messages[0].MessageID != "" {
		t.Errorf("a chat message was asked for its email headers")
	}
}

func TestLoadWithoutRawEmail(t *testing.T) {
	f, s := setup(t)
	f.rawMissing = true
	atClock(t, time.Unix(mar1+day, 0))
	tk := firstTicket(t, s)
	if err := s.Load(context.Background(), &tk); err != nil {
		t.Fatalf("a missing raw email failed the load: %v", err)
	}
	if tk.Messages[0].MessageID != "" || tk.Messages[3].MessageID != "<r2.leela@acme.example>" {
		t.Errorf("threading = %q, %q", tk.Messages[0].MessageID, tk.Messages[3].MessageID)
	}
}

func TestLoadRateLimitedOnRawEmail(t *testing.T) {
	f, s := setup(t)
	f.rawLimited = true
	atClock(t, time.Unix(mar1+day, 0))
	tk := firstTicket(t, s)
	err := s.Load(context.Background(), &tk)
	var rl *source.RateLimited
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Fatalf("err = %v, want a rate limit of 7s", err)
	}
}

func TestParseThreading(t *testing.T) {
	cases := []struct {
		raw, id, reply, refs string
	}{
		{"Message-ID: <x@a.example>\r\n\r\nbody", "<x@a.example>", "", "[]"},
		{"message-id:  <x@a.example> (comment)\r\nIn-Reply-To: <p@a.example> <q@a.example>\r\nReferences: <o@a.example>\r\n\t<p@a.example>\r\n\r\n",
			"<x@a.example>", "<p@a.example>", "[<o@a.example> <p@a.example>]"},
		{"Subject: none\r\n\r\n", "", "", "[]"},
		{"Message-ID: not-an-id\r\n\r\n", "", "", "[]"},
	}
	for _, c := range cases {
		got, err := parseThreading(strings.NewReader(c.raw))
		if err != nil {
			t.Fatalf("%q: %v", c.raw, err)
		}
		if got.MessageID != c.id || got.InReplyTo != c.reply || fmt.Sprint(got.References) != c.refs && !(c.refs == "[]" && got.References == nil) {
			t.Errorf("%q = %+v", c.raw, got)
		}
	}
	if _, err := parseThreading(strings.NewReader("")); err == nil {
		t.Error("an empty email parsed")
	}
}
