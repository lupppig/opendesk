package core

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestMemberProfile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, _ := e.signup("lead")
	wid := owner.newWorkspace("Profile Co")
	agent, _ := e.signup("helper")
	me := agent.do("GET", "/v1/me", nil)
	agentID := me.Body["user"].(map[string]any)["id"].(string)
	if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'agent')`, wid, agentID); err != nil {
		t.Fatal(err)
	}
	agent.workspace = wid

	r := agent.do("PATCH", "/v1/members/me/profile", map[string]any{"title": "  Support   engineer ", "phone": "+1 555 0100",
		"signature_html": `<p onclick="x()">{{ agent.name }}, {{ agent.title }}</p><script>x</script>`})
	if r.Status != 200 {
		t.Fatalf("self: %d %s", r.Status, r.Raw)
	}
	prof := r.Body["profile"].(map[string]any)
	if prof["title"] != "Support engineer" || prof["phone"] != "+1 555 0100" || prof["pronouns"] != "" ||
		prof["signature_html"] != "<p>{{ agent.name }}, {{ agent.title }}</p>" {
		t.Fatalf("profile %+v", prof)
	}
	// A personal signature cannot include itself; markup is refused in the lines.
	if r := agent.do("PATCH", "/v1/members/me/profile", map[string]any{"signature_html": "<p>{{ agent.signature }}</p>"}); r.Status != 422 {
		t.Fatalf("self signature: %d", r.Status)
	}
	if r := agent.do("PATCH", "/v1/members/me/profile", map[string]any{"title": "<b>x</b>"}); r.Status != 422 {
		t.Fatalf("markup: %d", r.Status)
	}
	if r := agent.do("PATCH", "/v1/members/me/profile", map[string]any{"pronouns": strings.Repeat("x", 41)}); r.Status != 422 {
		t.Fatalf("long: %d", r.Status)
	}
	// Agents edit only their own; the owner anyone's.
	ownerID := owner.do("GET", "/v1/me", nil).Body["user"].(map[string]any)["id"].(string)
	if r := agent.do("PATCH", "/v1/members/"+ownerID+"/profile", map[string]any{"title": "x"}); r.Status != 403 {
		t.Fatalf("other: %d", r.Status)
	}
	if r := owner.do("PATCH", "/v1/members/"+agentID+"/profile", map[string]any{"pronouns": "they/them"}); r.Status != 200 ||
		r.Body["profile"].(map[string]any)["pronouns"] != "they/them" || r.Body["profile"].(map[string]any)["title"] != "Support engineer" {
		t.Fatalf("admin: %d %s", r.Status, r.Raw)
	}
	// An admin edits agents' profiles, never the owner's or another admin's.
	admin, adminID := e.memberOf(uuid.MustParse(wid), "profadmin", "admin")
	if r := admin.do("PATCH", "/v1/members/"+agentID+"/profile", map[string]any{"title": "Lead"}); r.Status != 200 {
		t.Fatalf("admin on agent: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", "/v1/members/"+ownerID+"/profile", map[string]any{"signature_html": "<p>x</p>"}); r.Status != 403 {
		t.Fatalf("admin on owner: %d", r.Status)
	}
	other, _ := e.memberOf(uuid.MustParse(wid), "profadmin2", "admin")
	if r := other.do("PATCH", "/v1/members/"+adminID.String()+"/profile", map[string]any{"title": "x"}); r.Status != 403 {
		t.Fatalf("admin on admin: %d", r.Status)
	}
	var events int
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'member.updated'`, wid).Scan(&events)
	if events < 2 {
		t.Fatalf("member.updated %d", events)
	}
}
