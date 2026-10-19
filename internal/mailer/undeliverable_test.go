package mailer

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

type recorder struct{ sent []string }

func (r *recorder) Send(_ context.Context, m Mail) error {
	r.sent = append(r.sent, m.To)
	return nil
}

func TestUndeliverable(t *testing.T) {
	for to, want := range map[string]bool{
		"jordan@acme.example.invalid":              true,
		"Jordan Lee <jordan@acme.example.invalid>": true,
		"MAYA.CHEN@DEMO.EXAMPLE.INVALID":           true,
		"jordan@acme.com":                          false,
		"jordan@invalid.example.com":               false,
		"Jordan <jordan@invalidated.io>":           false,
	} {
		if got := Undeliverable(to); got != want {
			t.Errorf("Undeliverable(%q) = %v, want %v", to, got, want)
		}
	}
}

// Sample data's people (internal/sample) are on .invalid domains: the platform mailer
// drops their mail without an error.
func TestReservedSkipsInvalid(t *testing.T) {
	r := &recorder{}
	m := reserved{r, slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, to := range []string{"maya.chen@demo.example.invalid", "Owner <owner@example.com>", "Dana <dana@northwind.example.invalid>"} {
		if err := m.Send(context.Background(), Mail{To: to, Subject: "Hi"}); err != nil {
			t.Fatalf("send %s: %v", to, err)
		}
	}
	if len(r.sent) != 1 || r.sent[0] != "Owner <owner@example.com>" {
		t.Fatalf("sent %v, want only the deliverable address", r.sent)
	}
}
