package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/crypto"
)

func TestAdminWorkspaceCollab(t *testing.T) {
	e := newEnv(t)
	owner := e.person("owner")
	r := owner.app.do("POST", "/v1/workspaces", map[string]any{"name": "Acme " + crypto.RandomString(4)})
	want(t, "create workspace", r, 201)
	wid := r.str("id")
	owner.app.workspace = wid
	support := e.signInAs(t, RoleSupport)
	viewer := e.signInAs(t, RoleViewer)
	path := "/v1/admin/workspaces/" + wid + "/collab"
	listed := func(filter string) bool {
		l := viewer.do("GET", "/v1/admin/workspaces?filter="+filter+"&q="+url.QueryEscape(owner.email), nil)
		want(t, "list "+filter, l, 200)
		return strings.Contains(string(l.Raw), wid)
	}

	w := viewer.do("GET", "/v1/admin/workspaces/"+wid, nil)
	want(t, "get workspace", w, 200)
	if w.str("collab", "access") != "none" || w.Body["collab"].(map[string]any)["enabled"] != false ||
		w.Body["collab"].(map[string]any)["accounts"] != 0.0 || listed("collab") {
		t.Fatalf("collab before: %s", w.Raw)
	}
	want(t, "viewer cannot change collab", viewer.do("PUT", path, map[string]any{"access": "offered"}), 403)

	// Offered: the workspace may subscribe; nothing is on yet.
	o := support.do("PUT", path, map[string]any{"access": "offered", "note": "Beta customer"})
	want(t, "offer", o, 200)
	if o.str("collab", "access") != "offered" || o.Body["collab"].(map[string]any)["enabled"] != false || o.str("plan") != "free" ||
		o.str("collab", "note") != "Beta customer" || !listed("collab") || !listed("free") {
		t.Fatalf("offered: %s", o.Raw)
	}

	// An end date is for complimentary access, in the future.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	want(t, "until with offered", support.do("PUT", path, map[string]any{"access": "offered", "until": future.Format(time.RFC3339)}), 422)
	want(t, "until in the past", support.do("PUT", path, map[string]any{"access": "complimentary", "until": past}), 422)
	want(t, "until not a time", support.do("PUT", path, map[string]any{"access": "complimentary", "until": "next week"}), 422)
	want(t, "unknown access", support.do("PUT", path, map[string]any{"access": "forever"}), 422)
	want(t, "long note", support.do("PUT", path, map[string]any{"access": "offered", "note": strings.Repeat("x", 201)}), 422)

	// Complimentary: the collab plan without paying, until the date.
	c := support.do("PUT", path, map[string]any{"access": "complimentary", "until": future.Format(time.RFC3339), "note": "Trial"})
	want(t, "complimentary", c, 200)
	ent := c.Body["entitlements"].(map[string]any)
	if c.str("plan") != "collab" || c.str("collab", "access") != "complimentary" || c.Body["collab"].(map[string]any)["enabled"] != true ||
		c.str("subscription", "plan") != "free" || ent["max_mailboxes"] != 0.0 || ent["max_members"] != 0.0 || ent["monthly_ai_usd"] != 20.0 {
		t.Fatalf("complimentary: %s", c.Raw)
	}
	if until, err := time.Parse(time.RFC3339, c.str("collab", "until")); err != nil || !until.Equal(future) {
		t.Fatalf("until %q, want %s", c.str("collab", "until"), future)
	}
	if !listed("collab") || listed("free") || listed("paid") {
		t.Fatal("complimentary workspace listed under the wrong filter")
	}
	row := viewer.do("GET", "/v1/admin/workspaces?q="+url.QueryEscape(owner.email), nil)
	if !strings.Contains(string(row.Raw), `"plan":"collab"`) {
		t.Fatalf("row plan: %s", row.Raw)
	}

	// Removing access turns it off.
	n := support.do("PUT", path, map[string]any{"access": "none"})
	want(t, "remove", n, 200)
	if n.str("plan") != "free" || n.Body["collab"].(map[string]any)["enabled"] != false || n.Body["collab"].(map[string]any)["until"] != nil ||
		listed("collab") {
		t.Fatalf("removed: %s", n.Raw)
	}

	// Audited in both logs (the note only in the admin's), and the app hears billing.updated.
	audit := owner.app.do("GET", "/v1/audit-logs?limit=100", nil)
	if !strings.Contains(string(audit.Raw), `"workspace.collab_changed"`) || !strings.Contains(string(audit.Raw), `"instance_admin"`) ||
		strings.Contains(string(audit.Raw), "Beta customer") {
		t.Fatalf("workspace audit: %s", audit.Raw)
	}
	aa := viewer.do("GET", "/v1/admin/audit?action=workspace.collab_changed&target_id="+wid, nil)
	want(t, "admin audit", aa, 200)
	if strings.Count(string(aa.Raw), `"workspace.collab_changed"`) != 3 || !strings.Contains(string(aa.Raw), "Beta customer") {
		t.Fatalf("admin audit: %s", aa.Raw)
	}
	var events int
	if err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'billing.updated'`, wid).
		Scan(&events); err != nil || events != 3 {
		t.Fatalf("billing.updated events: %d %v", events, err)
	}
}
