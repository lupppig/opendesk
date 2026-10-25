package core

import (
	"regexp"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

// TestTimeFormat: a person's clock is "auto" until they choose one, and their
// notification emails read times on it (24-hour unless they chose 12-hour).
func TestTimeFormat(t *testing.T) {
	f := newNotifyFixture(t, "timefmt")
	// Two notifications make a list email, whose summary line names a time.
	t1 := f.ticket("SAML with Okta", nil, &f.uid)
	t2 := f.ticket("Export to CSV", nil, &f.uid)
	f.notify(hd.NotifyAssigned, &t1, nil)
	f.notify(hd.NotifyAssigned, &t2, nil)
	// Sent at times of their own, away from other tests' emails under the instance send rate.
	first := time.Now().Add(97 * time.Minute)
	t.Cleanup(func() { f.exec(`DELETE FROM notification_email_log WHERE user_id = $1`, f.uid) })
	f.send(first)
	m := f.mails()
	if len(m) != 1 {
		t.Fatalf("mails: %d", len(m))
	}
	if !regexp.MustCompile(`Since \d{2}:\d{2}: `).MatchString(m[0].Text) || regexp.MustCompile(`\d (AM|PM)\b`).MatchString(m[0].Text) {
		t.Fatalf("default clock is 24-hour:\n%s", m[0].Text)
	}

	if got := f.c.do("GET", "/v1/me", nil).str("user", "time_format"); got != "auto" {
		t.Fatalf("default time_format %q", got)
	}
	if r := f.c.do("PATCH", "/v1/me", map[string]any{"time_format": "13h"}); r.Status != 422 {
		t.Fatalf("unknown time_format: %d", r.Status)
	}
	r := f.c.do("PATCH", "/v1/me", map[string]any{"time_format": "12h"})
	if r.Status != 200 || r.str("time_format") != "12h" {
		t.Fatalf("set 12h: %d %v", r.Status, r.Body)
	}
	if got := f.c.do("GET", "/v1/me", nil).str("user", "time_format"); got != "12h" {
		t.Fatalf("stored time_format %q", got)
	}
	// A name change leaves the clock alone.
	if r := f.c.do("PATCH", "/v1/me", map[string]any{"name": "Alex Kim"}); r.str("time_format") != "12h" {
		t.Fatalf("name change reset time_format: %v", r.Body)
	}
	t3 := f.ticket("Refund", nil, &f.uid)
	t4 := f.ticket("Invoice address", nil, &f.uid)
	f.notify(hd.NotifyAssigned, &t3, nil)
	f.notify(hd.NotifyAssigned, &t4, nil)
	f.send(first.Add(3 * time.Hour)) // past the first email's cooldown
	m = f.mails()
	if len(m) != 2 || !regexp.MustCompile(`Since \d{1,2}:\d{2} (AM|PM): `).MatchString(m[1].Text) {
		t.Fatalf("12-hour clock:\n%s", m[len(m)-1].Text)
	}
}
