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

// firstTicket is the first ticket an import lists (cnv_1, last active mar1+1h).
func firstTicket(t *testing.T, s *front) source.Ticket {
	t.Helper()
	p, err := s.Tickets(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.Items[0]
}

// Load leaves threading headers to the import's threading phase: it asks for no raw email.
func TestLoadLeavesThreadingToItsPhase(t *testing.T) {
	f, s := setup(t)
	tk := firstTicket(t, s)
	if err := s.Load(context.Background(), &tk); err != nil {
		t.Fatal(err)
	}
	for _, m := range tk.Messages {
		if m.MessageID != "" || m.InReplyTo != "" || len(m.References) != 0 {
			t.Errorf("%s got threading headers from Load", m.ID)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits["/messages/msg_1"]+f.hits["/messages/msg_2"] != 0 {
		t.Errorf("raw email requests = %v", f.hits)
	}
}

func TestThreading(t *testing.T) {
	_, s := setup(t)
	var th source.Threader = s
	in, err := th.Threading(context.Background(), "msg_1")
	if err != nil {
		t.Fatal(err)
	}
	if in.MessageID != "<a1.bob@mail.example>" || in.InReplyTo != "" || len(in.References) != 0 {
		t.Errorf("inbound threading = %+v", in)
	}
	reply, err := th.Threading(context.Background(), "msg_2")
	if err != nil {
		t.Fatal(err)
	}
	if reply.MessageID != "<r2.leela@acme.example>" || reply.InReplyTo != "<a1.bob@mail.example>" ||
		fmt.Sprint(reply.References) != "[<a0.bob@mail.example> <a1.bob@mail.example>]" {
		t.Errorf("reply threading = %+v", reply)
	}
	for _, id := range []string{"cnv_1", "msg_1/../x", "msg_1?a=b"} {
		if _, err := s.Threading(context.Background(), id); err == nil {
			t.Errorf("%q was asked for", id)
		}
	}
}

func TestThreadingWithoutRawEmail(t *testing.T) {
	f, s := setup(t)
	f.rawMissing = true
	_, err := s.Threading(context.Background(), "msg_1")
	var re *source.RemoteError
	if !errors.As(err, &re) || re.Status != 404 {
		t.Fatalf("err = %v, want a 404", err)
	}
}

func TestThreadingRateLimited(t *testing.T) {
	f, s := setup(t)
	f.rawLimited = true
	_, err := s.Threading(context.Background(), "msg_1")
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
