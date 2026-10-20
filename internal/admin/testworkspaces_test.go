package admin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) < 20 || auth.CheckPassword(pw) != nil || strings.ContainsAny(pw, "0O1lIo") || seen[pw] {
			t.Fatalf("password %q", pw)
		}
		seen[pw] = true
	}
}

func TestSampleRequestCheck(t *testing.T) {
	r := SampleRequest{Size: "small", Kinds: []string{"kb", "tickets", "kb"}}
	if err := r.check(); err != nil || strings.Join(r.Kinds, ",") != "tickets,kb" {
		t.Fatalf("kinds %v, %v", r.Kinds, err)
	}
	for _, bad := range []SampleRequest{{Size: "huge", Kinds: []string{"kb"}}, {Size: "small"}, {Size: "small", Kinds: []string{"spam"}}} {
		if err := bad.check(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if seedFor(uuid.MustParse("0190c3a2-0000-7000-8000-000000000001"), 1) == seedFor(uuid.MustParse("0190c3a2-0000-7000-8000-000000000001"), 2) {
		t.Error("batches share a seed")
	}
}

// seedJob finds the queued sample data job of a workspace.
func (e *env) seedJob(wid string) (jobs.SeedTestWorkspaceArgs, bool) {
	e.t.Helper()
	var raw []byte
	err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT args FROM river_job WHERE kind = 'admin.seed_test_workspace'
		AND args->>'workspace_id' = $1 ORDER BY id DESC LIMIT 1`, wid).Scan(&raw)
	if err != nil {
		return jobs.SeedTestWorkspaceArgs{}, false
	}
	var a jobs.SeedTestWorkspaceArgs
	_ = json.Unmarshal(raw, &a)
	return a, true
}

func sampleOf(r resp, path ...string) map[string]any {
	var v any = r.Body
	for _, p := range path {
		v = v.(map[string]any)[p]
	}
	return v.(map[string]any)["sample_data"].(map[string]any)
}

func TestTestWorkspaces(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	staff := e.person("staff")
	e.twoFactor(staff)
	e.grant(staff, RoleSuper)
	panel := e.signIn(staff)
	helper := e.person("support")
	e.twoFactor(helper)
	e.grant(helper, RoleSupport)
	support := e.signIn(helper)
	viewer := e.signInAs(t, RoleViewer)

	email := "demo-" + crypto.RandomString(6) + "@acme.test.invalid"
	body := map[string]any{"name": "Acme demo", "owner": map[string]any{"email": email, "name": "Alex Demo"}, "plan": "collab",
		"sample": map[string]any{"size": "small", "kinds": []string{"team", "kb", "tickets"}}, "delete_after_days": 7, "password_days": 2,
		"reason": "Sales demo for Acme"}
	with := func(code string) map[string]any {
		out := map[string]any{}
		for k, v := range body {
			out[k] = v
		}
		out["code"] = code
		return out
	}

	// Permission and step-up.
	want(t, "viewer", viewer.do("POST", "/v1/admin/test-workspaces", with("123456")), 403, "forbidden")
	want(t, "no code", panel.do("POST", "/v1/admin/test-workspaces", body), 403, "mfa_required")
	want(t, "wrong code", panel.do("POST", "/v1/admin/test-workspaces", with("000000")), 401, "invalid_code")
	real := with(e.code(staff))
	real["owner"] = map[string]any{"email": "someone-" + crypto.RandomString(6) + "@example.com"}
	want(t, "a new account needs .invalid", panel.do("POST", "/v1/admin/test-workspaces", real), 422, "test_email_required")

	// A new test account, collab, with sample data queued.
	cr := support.do("POST", "/v1/admin/test-workspaces", with(e.code(helper)))
	want(t, "create", cr, 201)
	wid := cr.str("workspace", "workspace", "id")
	password := cr.str("owner", "password")
	if len(password) < 20 || cr.str("owner", "email") != email || cr.Body["owner"].(map[string]any)["new_account"] != true ||
		cr.str("sign_in_url") != "http://app.test/login?email="+strings.ReplaceAll(email, "@", "%40") || cr.str("workspace", "plan") != "collab" {
		t.Fatalf("create: %s", cr.Raw)
	}
	expires, _ := time.Parse(time.RFC3339, cr.str("workspace", "test_expires_at"))
	if d := time.Until(expires); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("test_expires_at %s", expires)
	}
	if pe, _ := time.Parse(time.RFC3339, cr.str("owner", "password_expires_at")); time.Until(pe) < 2*24*time.Hour-time.Minute || time.Until(pe) > 2*24*time.Hour+time.Minute {
		t.Fatalf("password_expires_at %s", pe)
	}
	if s := sampleOf(cr, "workspace"); s["status"] != "running" || s["size"] != "small" || s["batches"] != 1.0 {
		t.Fatalf("sample_data: %v", s)
	}
	var verified, testAccount bool
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL, test_account FROM users WHERE email = $1`, email).Scan(&verified, &testAccount); err != nil ||
		!verified || !testAccount {
		t.Fatalf("account: verified %v test %v %v", verified, testAccount, err)
	}
	var mails int
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE strpos(args::text, $1) > 0`, email).Scan(&mails); err != nil || mails != 0 {
		t.Fatalf("a test account gets no email: %d %v", mails, err)
	}

	// Complimentary Collab until the workspace's expiry; flagged test.
	w := viewer.do("GET", "/v1/admin/workspaces/"+wid, nil)
	want(t, "get", w, 200)
	if w.str("plan") != "collab" || w.str("collab", "access") != "complimentary" || w.Body["test"] != true || w.str("test_expires_at") == "" {
		t.Fatalf("workspace: %s", w.Raw)
	}
	if until, _ := time.Parse(time.RFC3339, w.str("collab", "until")); until.Sub(expires).Abs() > time.Second {
		t.Fatalf("collab until %s, expires %s", until, expires)
	}

	// The job is queued with the batch; running it records the counts.
	job, ok := e.seedJob(wid)
	if !ok || job.Batch != 1 || job.Size != "small" || strings.Join(job.Kinds, ",") != "tickets,kb,team" {
		t.Fatalf("seed job: %+v %v", job, ok)
	}
	want(t, "add while seeding", support.do("POST", "/v1/admin/workspaces/"+wid+"/sample-data", map[string]any{"size": "small", "kinds": []string{"chat"}}), 409, "seeding")
	if err := RunSeed(ctx, e.d, job); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Generated teammates are test accounts too, so they go with the workspace.
	var members, notTest int
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE NOT u.test_account) FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1`, wid).Scan(&members, &notTest); err != nil || notTest != 0 {
		t.Fatalf("members %d, not test accounts %d, %v", members, notTest, err)
	}
	list := viewer.do("GET", "/v1/admin/test-workspaces", nil)
	want(t, "list", list, 200)
	var row map[string]any
	for _, x := range list.Body["data"].([]any) {
		if m := x.(map[string]any); m["workspace"].(map[string]any)["id"] == wid {
			row = m
		}
	}
	if row == nil {
		t.Fatalf("not listed: %s", list.Raw)
	}
	s := row["sample_data"].(map[string]any)
	if s["status"] != "done" || s["seeded_at"] == nil || s["counts"] == nil || row["reason"] != "Sales demo for Acme" ||
		row["owner"].(map[string]any)["test_account"] != true || row["created_by"].(map[string]any)["email"] != helper.email {
		t.Fatalf("row: %v", row)
	}

	// The generated password signs in, until it expires; then the answer is a wrong password's.
	app := e.client("http://app.test")
	want(t, "login", app.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": password}), 200)
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE users SET password_expires_at = now() - interval '1 second' WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	expired := app.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": password})
	wrong := app.do("POST", "/v1/auth/login", map[string]any{"email": staff.email, "password": "not the password at all"})
	want(t, "expired", expired, 401, "invalid_credentials")
	if expired.str("detail") != wrong.str("detail") {
		t.Fatalf("expired %s, wrong %s", expired.Raw, wrong.Raw)
	}

	// Renew the password (step-up), and keep the workspace.
	// A generated password lives two days at most (CASA 1.1.2).
	want(t, "renew too long", support.do("PATCH", "/v1/admin/test-workspaces/"+wid, map[string]any{"password_days": 3, "code": "123456"}), 422)
	want(t, "viewer renew", viewer.do("PATCH", "/v1/admin/test-workspaces/"+wid, map[string]any{"password_days": 2, "code": "123456"}), 403)
	want(t, "renew without code", support.do("PATCH", "/v1/admin/test-workspaces/"+wid, map[string]any{"password_days": 2}), 403, "mfa_required")
	rn := support.do("PATCH", "/v1/admin/test-workspaces/"+wid, map[string]any{"password_days": 2, "delete_after_days": nil, "code": e.code(helper)})
	want(t, "renew", rn, 200)
	renewed := rn.str("owner", "password")
	if renewed == "" || renewed == password || rn.Body["workspace"].(map[string]any)["test_expires_at"] != nil {
		t.Fatalf("renew: %s", rn.Raw)
	}
	want(t, "new password", app.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": renewed}), 200)
	want(t, "old password", app.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": password}), 401)
	if c := viewer.do("GET", "/v1/admin/workspaces/"+wid, nil); c.Body["collab"].(map[string]any)["until"] != nil {
		t.Fatalf("collab until kept: %s", c.Raw)
	}

	// Another batch once the first is done.
	add := support.do("POST", "/v1/admin/workspaces/"+wid+"/sample-data", map[string]any{"size": "small", "kinds": []string{"chat", "chat"}})
	want(t, "add data", add, 202)
	if s := sampleOf(add); s["status"] != "running" || s["batches"] != 2.0 || s["counts"] == nil {
		t.Fatalf("add: %s", add.Raw)
	}
	if job, _ := e.seedJob(wid); job.Batch != 2 || job.Kinds[0] != "chat" || len(job.Kinds) != 1 {
		t.Fatalf("second job: %+v", job)
	}

	// An existing account owns a pro test workspace: no password, unlimited, an AI grant.
	owner := e.person("owner")
	pr := panel.do("POST", "/v1/admin/test-workspaces", map[string]any{"name": "Pro trial", "owner": map[string]any{"user_id": owner.id},
		"plan": "pro", "reason": "Trial for an existing customer", "code": e.code(staff)})
	want(t, "existing owner", pr, 201)
	proID := pr.str("workspace", "workspace", "id")
	if pr.Body["owner"].(map[string]any)["password"] != nil || pr.Body["owner"].(map[string]any)["new_account"] != false || pr.str("owner", "email") != owner.email ||
		sampleOf(pr, "workspace")["status"] != "none" {
		t.Fatalf("existing owner: %s", pr.Raw)
	}
	pw := viewer.do("GET", "/v1/admin/workspaces/"+proID, nil)
	ent := pw.Body["entitlements"].(map[string]any)
	if ent["max_mailboxes"] != 0.0 || ent["max_members"] != 0.0 || pw.Body["ai"].(map[string]any)["purchased_usd"] != 50.0 {
		t.Fatalf("pro: %s", pw.Raw)
	}
	want(t, "owner keeps their password", e.client("http://app.test").do("POST", "/v1/auth/login", map[string]any{"email": owner.email, "password": goodPassword}), 200)
	want(t, "renew a real account", panel.do("PATCH", "/v1/admin/test-workspaces/"+proID, map[string]any{"password_days": 2, "code": e.code(staff)}), 409, "not_test_account")

	// Sample data only goes into test workspaces.
	reg := owner.app.do("POST", "/v1/workspaces", map[string]any{"name": "Real " + crypto.RandomString(4)})
	want(t, "real workspace", reg, 201)
	regID, regName := reg.str("id"), reg.str("name")
	want(t, "not test", support.do("POST", "/v1/admin/workspaces/"+regID+"/sample-data", map[string]any{"size": "small", "kinds": []string{"kb"}}), 409, "not_test")
	want(t, "not test patch", support.do("PATCH", "/v1/admin/test-workspaces/"+regID, map[string]any{"delete_after_days": 3, "code": e.code(helper)}), 404)

	// Filters and the overview leave test workspaces and accounts out of the counts.
	tf := viewer.do("GET", "/v1/admin/workspaces?filter=test", nil)
	if !strings.Contains(string(tf.Raw), wid) || !strings.Contains(string(tf.Raw), proID) || strings.Contains(string(tf.Raw), regID) {
		t.Fatalf("filter test: %s", tf.Raw)
	}
	// Other packages' tests may add accounts meanwhile, so the counts are bracketed: every
	// real account and workspace, and none of the test ones.
	count := func(sql string) float64 {
		var n float64
		if err := e.d.DB.Pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	usersBefore, wsBefore := count(`SELECT count(*) FROM users WHERE NOT test_account`), count(`SELECT count(*) FROM workspaces WHERE NOT test`)
	after := panel.do("GET", "/v1/admin/summary", nil)
	usersAfter, wsAfter := count(`SELECT count(*) FROM users WHERE NOT test_account`), count(`SELECT count(*) FROM workspaces WHERE NOT test`)
	if count(`SELECT count(*) FROM users WHERE test_account`) == 0 || count(`SELECT count(*) FROM workspaces WHERE test`) < 2 {
		t.Fatal("no test data to leave out")
	}
	au, aw := after.Body["users"].(map[string]any)["total"].(float64), after.Body["workspaces"].(map[string]any)["total"].(float64)
	if au < usersBefore || au > usersAfter || aw < wsBefore || aw > wsAfter {
		t.Fatalf("summary counts test data: users %v (real %v..%v), workspaces %v (real %v..%v)", au, usersBefore, usersAfter, aw, wsBefore, wsAfter)
	}

	// Audit entries, without the password or the address.
	audit := viewer.do("GET", "/v1/admin/audit?limit=100", nil)
	raw := string(audit.Raw)
	for _, a := range []string{"test_workspace.created", "test_workspace.changed", "test_workspace.sample_added"} {
		if !strings.Contains(raw, `"`+a+`"`) {
			t.Fatalf("no %s: %s", a, raw)
		}
	}
	if strings.Contains(raw, password) || strings.Contains(raw, renewed) || strings.Contains(raw, email) {
		t.Fatalf("audit holds a secret or address: %s", raw)
	}

	// Support deletes test workspaces only; the test account goes with its workspace, a
	// real owner stays.
	want(t, "support deletes a real workspace", support.do("DELETE", "/v1/admin/workspaces/"+regID, map[string]any{"confirm_name": regName, "code": e.code(helper)}), 403)
	want(t, "viewer deletes", viewer.do("DELETE", "/v1/admin/workspaces/"+wid, map[string]any{"confirm_name": "Acme demo", "code": "123456"}), 403)
	var teamIDs []uuid.UUID
	rows, _ := e.d.DB.Pool.Query(ctx, `SELECT user_id FROM members WHERE workspace_id = $1`, wid)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		teamIDs = append(teamIDs, id)
	}
	rows.Close()
	want(t, "delete test", support.do("DELETE", "/v1/admin/workspaces/"+wid, map[string]any{"confirm_name": "Acme demo", "code": e.code(helper)}), 204)
	var left int
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1 OR id = ANY($2)`, email, teamIDs).Scan(&left)
	if left != 0 || len(teamIDs) < 2 {
		t.Fatalf("%d of the workspace's %d test accounts outlived it", left, len(teamIDs))
	}
	want(t, "delete pro", panel.do("DELETE", "/v1/admin/workspaces/"+proID, map[string]any{"confirm_name": "Pro trial", "code": e.code(staff)}), 204)
	want(t, "real owner stays", e.client("http://app.test").do("POST", "/v1/auth/login", map[string]any{"email": owner.email, "password": goodPassword}), 200)

	// The cleanup deletes expired test workspaces and their test accounts, as the system.
	email2 := "expiring-" + crypto.RandomString(6) + "@test.invalid"
	ex := panel.do("POST", "/v1/admin/test-workspaces", map[string]any{"name": "Short lived", "owner": map[string]any{"email": email2},
		"plan": "free", "delete_after_days": 1, "reason": "Cleanup test", "code": e.code(staff)})
	want(t, "create expiring", ex, 201)
	exID := ex.str("workspace", "workspace", "id")
	kept := panel.do("POST", "/v1/admin/test-workspaces", map[string]any{"name": "Kept", "owner": map[string]any{"user_id": owner.id},
		"plan": "free", "delete_after_days": nil, "reason": "Kept", "code": e.code(staff)})
	want(t, "create kept", kept, 201)
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE workspaces SET test_expires_at = now() - interval '1 minute' WHERE id = $1`, exID); err != nil {
		t.Fatal(err)
	}
	n, err := ExpireTestWorkspaces(ctx, e.d)
	if err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	want(t, "expired gone", viewer.do("GET", "/v1/admin/workspaces/"+exID, nil), 404)
	want(t, "kept stays", viewer.do("GET", "/v1/admin/workspaces/"+kept.str("workspace", "workspace", "id"), nil), 200)
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1 OR id = $2`, email2, owner.id).Scan(&left)
	if left != 1 {
		t.Fatalf("after cleanup %d of the expired test account and the real owner remain, want only the owner", left)
	}
	ea := viewer.do("GET", "/v1/admin/audit?action=test_workspace.expired&target_id="+exID, nil)
	if !strings.Contains(string(ea.Raw), `"type":"system"`) {
		t.Fatalf("expired audit: %s", ea.Raw)
	}
}
