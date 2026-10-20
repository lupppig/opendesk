package app

import (
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/config"
)

// TestSecurityTxt: the file names the contact and policy and expires within a year.
func TestSecurityTxt(t *testing.T) {
	now := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
	got := securityTxt(&config.Config{SecurityContact: "mailto:security@example.com", SecurityPolicy: "https://example.com/policy"}, now)
	for _, want := range []string{"Contact: mailto:security@example.com\n", "Expires: 2027-04-17T00:00:00Z\n", "Policy: https://example.com/policy\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
