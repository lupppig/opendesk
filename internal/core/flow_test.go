package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// TestAccountFlow walks the whole core surface: sign up, workspace, invite and accept,
// API key, webhook, events replay, realtime token, 2FA, password reset, deletion.
func TestAccountFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, ownerEmail := e.signup("owner")
	if subj, text := e.lastMail(ownerEmail); subj != "Confirm your email" || !strings.Contains(text, "http://app.test/verify?token=") {
		t.Fatalf("verify mail: %s %s", subj, text)
	}
	_, text := e.lastMail(ownerEmail)
	if r := e.client().do("POST", "/v1/auth/email/verify", map[string]any{"token": tokenFromText(text, "token=")}); r.Status != 204 {
		t.Fatalf("verify: %d %s", r.Status, r.Raw)
	}
	wid := owner.newWorkspace("Acme Support")
	ws := owner.do("GET", "/v1/workspace", nil)
	if !strings.HasPrefix(ws.str("widget_key"), "wk_") || !strings.HasPrefix(ws.str("slug"), "acme-support") {
		t.Fatalf("workspace: %s", ws.Raw)
	}
	var views, tags, sla int
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM views WHERE workspace_id = $1), (SELECT count(*) FROM tags WHERE workspace_id = $1),
		(SELECT count(*) FROM sla_policies WHERE workspace_id = $1)`, wid).Scan(&views, &tags, &sla)
	if views != 3 || tags < 3 || sla != 1 {
		t.Fatalf("seed: views=%d tags=%d sla=%d", views, tags, sla)
	}
	if r := owner.do("PATCH", "/v1/workspace", map[string]any{"settings": map[string]any{"brand_color": "#112233", "timezone": "Europe/Berlin"}}); r.Status != 200 {
		t.Fatalf("patch: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PATCH", "/v1/workspace", map[string]any{"settings": map[string]any{"timezone": "Mars/Base"}}); r.Status != 422 {
		t.Fatalf("bad timezone: %d", r.Status)
	}

	// Invite a new person, who registers and accepts.
	agentEmail := uniqueEmail(t, "agent")
	inv := owner.do("POST", "/v1/invitations", map[string]any{"email": agentEmail, "role": "agent"})
	if inv.Status != 201 {
		t.Fatalf("invite: %d %s", inv.Status, inv.Raw)
	}
	_, text = e.lastMail(agentEmail)
	tok := tokenFromText(text, "/invite?token=")
	look := e.client().do("POST", "/v1/invitations/lookup", map[string]any{"token": tok})
	if look.str("workspace_name") != "Acme Support" || look.str("role") != "agent" {
		t.Fatalf("lookup: %s", look.Raw)
	}
	agent := e.client()
	r := agent.do("POST", "/v1/auth/register", map[string]any{"email": agentEmail, "password": goodPassword, "name": "Agent Smith", "invite_token": tok})
	agent.token = r.str("access_token")
	acc := agent.do("POST", "/v1/invitations/accept", map[string]any{"token": tok})
	if acc.Status != 200 || acc.str("role") != "agent" {
		t.Fatalf("accept: %d %s", acc.Status, acc.Raw)
	}
	if r := agent.do("POST", "/v1/invitations/accept", map[string]any{"token": tok}); r.Status != 404 {
		t.Fatalf("invitation reused: %d", r.Status)
	}
	agent.workspace = wid
	if r := agent.do("PATCH", "/v1/workspace", map[string]any{"name": "Hijacked"}); r.Status != 403 {
		t.Fatalf("agent edited settings: %d", r.Status)
	}
	members := owner.do("GET", "/v1/members", nil)
	if !strings.Contains(string(members.Raw), agentEmail) {
		t.Fatalf("members: %s", members.Raw)
	}

	// API key and webhook.
	key := owner.do("POST", "/v1/api-keys", map[string]any{"name": "Integration", "scopes": []string{"realtime", "tickets:read"}})
	if key.Status != 201 || !strings.HasPrefix(key.str("prefix"), "od_live_") {
		t.Fatalf("key: %d %s", key.Status, key.Raw)
	}
	kc := e.client()
	kc.token = key.str("secret")
	rt := kc.do("POST", "/v1/realtime/token", nil)
	if rt.Status != 200 || rt.str("url") != "ws://gw.test/socket" {
		t.Fatalf("key realtime token: %d %s", rt.Status, rt.Raw)
	}
	if r := owner.do("POST", "/v1/api-keys", map[string]any{"name": "Bad", "scopes": []string{"admin:all"}}); r.Status != 422 {
		t.Fatalf("unknown scope: %d", r.Status)
	}
	hook := owner.do("POST", "/v1/webhooks", map[string]any{"url": "http://127.0.0.1:1/hook", "events": []string{"*"}})
	if hook.Status != 201 {
		t.Fatalf("webhook: %d %s", hook.Status, hook.Raw)
	}
	// Its address cannot answer the verification challenge: no events until it is verified.
	if hook.Body["verified"] != false {
		t.Fatalf("unreachable webhook verified: %s", hook.Raw)
	}
	if r := owner.do("POST", "/v1/webhooks/"+hook.str("id")+"/test", nil); r.Status != 409 || r.str("code") != "webhook_unverified" {
		t.Fatalf("test before verification: %d %s", r.Status, r.Raw)
	}
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE webhooks SET verified_at = now() WHERE id = $1`, hook.str("id")); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("POST", "/v1/webhooks/"+hook.str("id")+"/test", nil); r.Status != 202 || r.str("event_type") != "webhook.test" {
		t.Fatalf("test: %d %s", r.Status, r.Raw)
	}
	// Webhook events reach the owner and admins alone: a tickets:read key replays the stand-in.
	evs := kc.do("GET", "/v1/events?since=0", nil)
	if evs.Status != 200 || !strings.Contains(string(evs.Raw), "member.added") || strings.Contains(string(evs.Raw), "webhook.created") ||
		strings.Contains(string(evs.Raw), "127.0.0.1:1/hook") || !strings.Contains(string(evs.Raw), "event.hidden") {
		t.Fatalf("events: %d %s", evs.Status, evs.Raw)
	}
	if evs := owner.do("GET", "/v1/events?since=0", nil); evs.Status != 200 || !strings.Contains(string(evs.Raw), "webhook.created") {
		t.Fatalf("owner events: %d %s", evs.Status, evs.Raw)
	}
	audit := owner.do("GET", "/v1/audit-logs?limit=100", nil)
	for _, a := range []string{"workspace.created", "workspace.updated", "invitation.created", "member.joined", "api_key.created", "webhook.created"} {
		if !strings.Contains(string(audit.Raw), `"`+a+`"`) {
			t.Errorf("audit lacks %s", a)
		}
	}

	// Removing the agent.
	if r := owner.do("DELETE", "/v1/members/"+acc.str("workspace", "id"), nil); r.Status != 404 {
		t.Fatalf("remove by wrong id: %d", r.Status)
	}
	agentID := agent.do("GET", "/v1/me", nil).str("user", "id")
	if r := owner.do("DELETE", "/v1/members/"+agentID, nil); r.Status != 204 {
		t.Fatalf("remove: %d %s", r.Status, r.Raw)
	}

	// 2FA on, then sign in with it.
	setup := owner.do("POST", "/v1/auth/totp/setup", nil)
	code, _ := totp.GenerateCode(setup.str("secret"), time.Now())
	if r := owner.do("POST", "/v1/auth/totp/enable", map[string]any{"code": code}); r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Raw)
	}
	c := e.client()
	lr := c.do("POST", "/v1/auth/login", map[string]any{"email": ownerEmail, "password": goodPassword})
	next, _ := totp.GenerateCode(setup.str("secret"), time.Now().Add(30*time.Second))
	mr := c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": lr.str("mfa_token"), "code": next})
	if mr.Status != 200 {
		t.Fatalf("mfa: %d %s", mr.Status, mr.Raw)
	}

	// Password reset through the emailed link.
	if r := e.client().do("POST", "/v1/auth/password/forgot", map[string]any{"email": ownerEmail}); r.Status != 204 {
		t.Fatalf("forgot: %d", r.Status)
	}
	if r := e.client().do("POST", "/v1/auth/password/forgot", map[string]any{"email": uniqueEmail(t, "ghost")}); r.Status != 204 {
		t.Fatalf("forgot unknown: %d", r.Status)
	}
	if err := (&forgotWorker{s: e.s}).Work(ctx, jobOf(forgotArgs{Email: ownerEmail})); err != nil {
		t.Fatal(err)
	}
	subj, text := e.lastMail(ownerEmail)
	if subj != "Reset your opendesk password" {
		t.Fatalf("reset mail: %s", subj)
	}
	reset := tokenFromText(text, "/reset?token=")
	newPw := "a brand new passphrase 77"
	if r := e.client().do("POST", "/v1/auth/password/reset", map[string]any{"token": reset, "password": newPw}); r.Status != 204 {
		t.Fatalf("reset: %d %s", r.Status, r.Raw)
	}
	if r := e.client().do("POST", "/v1/auth/password/reset", map[string]any{"token": reset, "password": newPw}); r.Status != 400 {
		t.Fatalf("reset token reused: %d", r.Status)
	}
	if r := c.do("POST", "/v1/auth/refresh", nil); r.Status != 401 {
		t.Fatalf("session survived reset: %d", r.Status)
	}

	// Delete the account: the owner's sole workspace goes with it.
	c2 := e.client()
	lr = c2.do("POST", "/v1/auth/login", map[string]any{"email": ownerEmail, "password": newPw})
	// Every step in the current window is spent; rewind the marker to stand in for
	// waiting for the next one.
	rewind := func() {
		_, _ = e.d.DB.Pool.Exec(ctx, `UPDATE users SET totp_last_step = 0 WHERE email = $1`, ownerEmail)
	}
	rewind()
	code, _ = totp.GenerateCode(setup.str("secret"), time.Now())
	mr = c2.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": lr.str("mfa_token"), "code": code})
	c2.token = mr.str("access_token")
	if r := c2.do("DELETE", "/v1/me", map[string]any{"password": newPw}); r.Status != 403 {
		t.Fatalf("delete without code: %d %s", r.Status, r.Raw)
	}
	var n int
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1`, wid).Scan(&n)
	if n != 1 {
		t.Fatalf("members before delete: %d", n)
	}
	rewind()
	code, _ = totp.GenerateCode(setup.str("secret"), time.Now())
	if r := c2.do("DELETE", "/v1/me", map[string]any{"password": newPw, "code": code}); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM workspaces WHERE id = $1`, wid).Scan(&n)
	if n != 0 {
		t.Fatal("workspace survived owner deletion")
	}
}

func jobOf[T river.JobArgs](args T) *river.Job[T] {
	return &river.Job[T]{Args: args, JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 5}}
}
