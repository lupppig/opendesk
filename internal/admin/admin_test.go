package admin

import (
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/deps"
)

// TestRolePermissions: viewers read, support handles accounts and abuse, only super
// turns off 2FA, deletes and manages admins.
func TestRolePermissions(t *testing.T) {
	superOnly := []string{PermUserMFA, PermUserDelete, PermWorkspaceDelete, PermAdminsManage}
	support := []string{PermUserSessions, PermUserSuspend, PermWorkspaceSuspend, PermWorkspaceLimits, PermWorkspaceTest, PermJobsManage}
	for _, p := range append(append([]string{PermRead}, support...), superOnly...) {
		if !RoleSuper.Can(p) {
			t.Errorf("super cannot %s", p)
		}
	}
	for _, p := range support {
		if !RoleSupport.Can(p) || RoleViewer.Can(p) {
			t.Errorf("%s: support %v viewer %v", p, RoleSupport.Can(p), RoleViewer.Can(p))
		}
	}
	for _, p := range superOnly {
		if RoleSupport.Can(p) || RoleViewer.Can(p) {
			t.Errorf("%s is super only", p)
		}
	}
	if !RoleViewer.Can(PermRead) || Role("owner").Can(PermRead) || Role("owner").Valid() {
		t.Error("unknown roles must have no permission")
	}
}

// TestRedactArgs: a system email job shows its shape, never the email or its links.
func TestRedactArgs(t *testing.T) {
	in := map[string]any{"to": "a@example.com", "subject": "Reset your password", "html": "<a href=https://x/reset?token=abc>",
		"text": "https://x/reset?token=abc", "headers": map[string]any{"List-Unsubscribe": "<https://x>"},
		"workspace_id": "w1", "nested": map[string]any{"refresh_token": "r", "count": 3.0}, "note": strings.Repeat("x", 300)}
	out := redactArgs(in).(map[string]any)
	for _, k := range []string{"subject", "html", "text", "headers"} {
		if out[k] != "[redacted]" {
			t.Errorf("%s = %v", k, out[k])
		}
	}
	if out["to"] != "a@example.com" || out["workspace_id"] != "w1" {
		t.Errorf("ids and addresses stay: %v", out)
	}
	n := out["nested"].(map[string]any)
	if n["refresh_token"] != "[redacted]" || n["count"] != 3.0 {
		t.Errorf("nested = %v", n)
	}
	if out["note"] != "[300 characters]" {
		t.Errorf("long string = %v", out["note"])
	}
}

// TestHealthChecks: a stalled relay or queue fails, a slow one warns.
func TestHealthChecks(t *testing.T) {
	sec := func(n int64) *int64 { return &n }
	base := AdminHealth{Database: AdminHealthDatabase{OK: true, SchemaVersion: 631}, NATS: AdminHealthNATS{OK: true}, Mail: AdminHealthMail{Driver: "ses"}}
	status := func(h AdminHealth, name string) string {
		for _, c := range checks(h) {
			if c.Name == name {
				return c.Status
			}
		}
		return ""
	}
	if s := status(base, "Event relay"); s != "ok" {
		t.Errorf("idle relay: %s", s)
	}
	h := base
	h.Relay = AdminHealthRelay{Unpublished: 4, OldestUnpublishedS: sec(60)}
	if s := status(h, "Event relay"); s != "warn" {
		t.Errorf("slow relay: %s", s)
	}
	h.Relay.OldestUnpublishedS = sec(600)
	if s := status(h, "Event relay"); s != "fail" {
		t.Errorf("stalled relay: %s", s)
	}
	h = base
	h.Jobs = AdminHealthJobs{Available: 10, OldestAvailableS: sec(3600)}
	if s := status(h, "Background jobs"); s != "fail" {
		t.Errorf("stalled jobs: %s", s)
	}
	h = base
	h.Database.Dirty = true
	if s := status(h, "Database"); s != "fail" {
		t.Errorf("dirty schema: %s", s)
	}
	h = base
	h.Mail.Driver = "log"
	if s := status(h, "Platform mail"); s != "warn" {
		t.Errorf("log mailer: %s", s)
	}
	h = base
	h.NATS.OK = false
	if s := status(h, "NATS"); s != "fail" {
		t.Errorf("nats down: %s", s)
	}
}

// TestCheckOrigin: the auth routes answer only the panel's origin.
func TestCheckOrigin(t *testing.T) {
	s := &svc{d: &deps.Deps{Cfg: &config.Config{AdminURL: "https://admin.example.com"}}}
	if err := s.checkOrigin(reqInfo{Origin: "https://admin.example.com"}); err != nil {
		t.Errorf("panel origin refused: %v", err)
	}
	for _, o := range []string{"", "https://app.example.com", "https://admin.example.com.evil.test", "null"} {
		if err := s.checkOrigin(reqInfo{Origin: o}); err == nil {
			t.Errorf("origin %q accepted", o)
		}
	}
}

func TestLikePattern(t *testing.T) {
	if got := likePattern(`50%_off\`); got != `%50\%\_off\\%` {
		t.Errorf("likePattern = %q", got)
	}
}
