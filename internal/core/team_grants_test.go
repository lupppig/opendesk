package core

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/migrations"
)

// grantFixture is a workspace with a private team and users on it.
type grantFixture struct {
	t     *testing.T
	e     *testEnv
	ctx   context.Context
	wid   uuid.UUID
	team  uuid.UUID
	owner *client
	users map[string]*client
	ids   map[string]uuid.UUID
	recv  string
}

func newGrantFixture(t *testing.T, names ...string) *grantFixture {
	e := newEnv(t)
	ctx := context.Background()
	owner, ownerEmail := e.signup("grantowner")
	f := &grantFixture{t: t, e: e, ctx: ctx, wid: uuid.MustParse(owner.newWorkspace("Grant Co")), team: uuid.New(), owner: owner,
		users: map[string]*client{}, ids: map[string]uuid.UUID{}}
	var oid uuid.UUID
	f.scalar(`SELECT id FROM users WHERE email = $1`, &oid, ownerEmail)
	f.ids["owner"] = oid
	recv := httptest.NewServer(echoChallenges(nil))
	t.Cleanup(recv.Close)
	f.recv = recv.URL
	f.exec(`INSERT INTO teams (id, workspace_id, name, private) VALUES ($1, $2, 'Security', true)`, f.team, f.wid)
	for _, n := range names {
		c, email := e.signup(n)
		c.workspace = f.wid.String()
		var id uuid.UUID
		f.scalar(`SELECT id FROM users WHERE email = $1`, &id, email)
		f.exec(`INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'admin')`, f.wid, id)
		f.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')`, f.team, id)
		f.users[n], f.ids[n] = c, id
	}
	return f
}

func (f *grantFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.e.d.DB.Pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *grantFixture) scalar(sql string, dst any, args ...any) {
	f.t.Helper()
	if err := f.e.d.DB.Pool.QueryRow(f.ctx, sql, args...).Scan(dst); err != nil {
		f.t.Fatal(err)
	}
}

// key creates an API key granted the team by user n and returns its id.
func (f *grantFixture) key(n string) uuid.UUID {
	f.t.Helper()
	r := f.users[n].do("POST", "/v1/api-keys", map[string]any{"name": "Key of " + n, "scopes": []string{"tickets:read"},
		"private_team_ids": []string{f.team.String()}})
	if r.Status != 201 {
		f.t.Fatalf("key: %d %s", r.Status, r.Raw)
	}
	if g := grantedBy(r); g != f.ids[n].String() {
		f.t.Fatalf("key granted by %q, want %s: %s", g, n, r.Raw)
	}
	return uuid.MustParse(r.str("id"))
}

// hook creates a webhook granted the team by user n and returns its id.
func (f *grantFixture) hook(n string) uuid.UUID {
	f.t.Helper()
	r := f.users[n].do("POST", "/v1/webhooks", map[string]any{"url": f.recv + "/" + n,
		"private_team_ids": []string{f.team.String()}})
	if r.Status != 201 {
		f.t.Fatalf("webhook: %d %s", r.Status, r.Raw)
	}
	if g := grantedBy(r); g != f.ids[n].String() {
		f.t.Fatalf("webhook granted by %q, want %s: %s", g, n, r.Raw)
	}
	return uuid.MustParse(r.str("id"))
}

// grantedBy reads the granter of a response's only private team.
func grantedBy(r resp) string {
	list, _ := r.Body["private_teams"].([]any)
	if len(list) != 1 {
		return ""
	}
	g, _ := list[0].(map[string]any)
	by, _ := g["granted_by"].(map[string]any)
	s, _ := by["id"].(string)
	return s
}

// has reports whether the key's or webhook's stored private_team_ids include the team, and
// checks its grant row agrees.
func (f *grantFixture) has(table string, id uuid.UUID) bool {
	f.t.Helper()
	var listed, granted bool
	f.scalar(`SELECT $2 = ANY(private_team_ids) FROM `+table+` WHERE id = $1`, &listed, id, f.team)
	col := map[string]string{"api_keys": grantAPIKey, "webhooks": grantWebhook}[table]
	f.scalar(`SELECT EXISTS (SELECT 1 FROM private_team_grants WHERE `+col+` = $1 AND team_id = $2)`, &granted, id, f.team)
	if listed != granted {
		f.t.Fatalf("%s %s: listed %v, granted %v", table, id, listed, granted)
	}
	return listed
}

// keySees reports whether the API key sees the team's tickets.
func (f *grantFixture) keySees(id uuid.UUID) bool {
	f.t.Helper()
	p := &auth.Principal{Kind: auth.KindAPIKey, APIKeyID: id, WorkspaceID: f.wid}
	hidden, err := domain.HiddenTeams(f.ctx, f.e.d.DB.Pool, p)
	if err != nil {
		f.t.Fatal(err)
	}
	return !slices.Contains(hidden, f.team)
}

// delivered publishes an event of the team, dispatches it and reports whether the webhook
// got it.
func (f *grantFixture) delivered(hook uuid.UUID) bool {
	f.t.Helper()
	var seq int64
	var eventID uuid.UUID
	err := pgx.BeginFunc(f.ctx, f.e.d.DB.Pool, func(tx pgx.Tx) error {
		env, err := events.Publish(f.ctx, tx, events.Event{WorkspaceID: f.wid, Type: "team.updated", TeamID: &f.team, Data: map[string]any{}})
		seq, eventID = env.Seq, env.ID
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	dw := &dispatchWorker{s: f.e.s}
	if err := dw.Work(f.ctx, &river.Job[jobs.WebhookDispatchArgs]{Args: jobs.WebhookDispatchArgs{WorkspaceID: f.wid, EventSeq: seq}}); err != nil {
		f.t.Fatal(err)
	}
	var n int
	f.scalar(`SELECT count(*) FROM webhook_deliveries WHERE webhook_id = $1 AND event_id = $2`, &n, hook, eventID)
	return n == 1
}

// lapseAudited reports whether the key's or webhook's loss of the team was audit-logged.
func (f *grantFixture) lapseAudited(id uuid.UUID) bool {
	f.t.Helper()
	var ok bool
	f.scalar(`SELECT EXISTS (SELECT 1 FROM audit_logs WHERE workspace_id = $1 AND entity_id = $2
		AND action IN ('api_key.updated', 'webhook.updated') AND data ->> 'reason' = 'granter_left_team'
		AND data -> 'removed_private_team_ids' ? $3)`, &ok, f.wid, id, f.team.String())
	return ok
}

func (f *grantFixture) setMembers(names ...string) {
	f.t.Helper()
	members := []map[string]any{}
	for _, n := range names {
		members = append(members, map[string]any{"user_id": f.ids[n], "role": "member"})
	}
	if r := f.owner.do("PATCH", "/v1/teams/"+f.team.String(), map[string]any{"members": members}); r.Status != 200 {
		f.t.Fatalf("set members: %d %s", r.Status, r.Raw)
	}
}

// TestGrantFollowsGranter: a key's or webhook's private team lasts while whoever granted it
// is on the team, whichever way they stop being on it.
func TestGrantFollowsGranter(t *testing.T) {
	f := newGrantFixture(t, "ana", "ben", "cy", "dee", "eve")
	keyAna, hookAna := f.key("ana"), f.hook("ana")
	keyBen := f.key("ben")
	keyCy, hookDee := f.key("cy"), f.hook("dee")

	// The grant survives while its granter is on the team: others leaving, and the granter's
	// own role changing, leave it alone.
	f.setMembers("ana", "ben", "cy", "dee")
	f.exec(`UPDATE team_members SET role = 'observer' WHERE team_id = $1 AND user_id = $2`, f.team, f.ids["ana"])
	if !f.has("api_keys", keyAna) || !f.has("webhooks", hookAna) || !f.keySees(keyAna) || !f.delivered(hookAna) {
		t.Fatal("a grant lapsed while its granter is on the team")
	}
	if r := f.owner.do("GET", "/v1/api-keys/"+keyAna.String(), nil); grantedBy(r) != f.ids["ana"].String() {
		t.Fatalf("get key: %s", r.Raw)
	}
	if r := f.owner.do("GET", "/v1/webhooks/"+hookAna.String(), nil); grantedBy(r) != f.ids["ana"].String() {
		t.Fatalf("get webhook: %s", r.Raw)
	}

	// A member of the team taking over the grant keeps it alive after the first granter leaves.
	if r := f.users["cy"].do("PATCH", "/v1/api-keys/"+keyBen.String(), map[string]any{"private_team_ids": []string{f.team.String()}}); r.Status != 200 ||
		grantedBy(r) != f.ids["cy"].String() {
		t.Fatalf("re-grant: %d %s", r.Status, r.Raw)
	}
	// Someone off the team keeping the list as it is changes nothing.
	if r := f.owner.do("PATCH", "/v1/api-keys/"+keyBen.String(), map[string]any{"private_team_ids": []string{f.team.String()}}); r.Status != 200 ||
		grantedBy(r) != f.ids["cy"].String() {
		t.Fatalf("unchanged list: %d %s", r.Status, r.Raw)
	}
	if r := f.users["ben"].do("POST", "/v1/teams/"+f.team.String()+"/leave", nil); r.Status != 204 {
		t.Fatalf("ben leaves: %d %s", r.Status, r.Raw)
	}
	if !f.has("api_keys", keyBen) {
		t.Fatal("a re-granted team lapsed with the first granter")
	}

	// Leaving the team.
	if r := f.users["ana"].do("POST", "/v1/teams/"+f.team.String()+"/leave", nil); r.Status != 204 {
		t.Fatalf("ana leaves: %d %s", r.Status, r.Raw)
	}
	if f.has("api_keys", keyAna) || f.has("webhooks", hookAna) || f.keySees(keyAna) || f.delivered(hookAna) {
		t.Fatal("a grant outlived its granter leaving the team")
	}
	if !f.lapseAudited(keyAna) || !f.lapseAudited(hookAna) {
		t.Fatal("the lapsed grants were not audit-logged")
	}
	if r := f.owner.do("GET", "/v1/webhooks/"+hookAna.String(), nil); r.Status != 200 || len(r.ids("private_team_ids")) != 0 {
		t.Fatalf("webhook after lapse: %d %s", r.Status, r.Raw)
	}

	// Being removed from the team (cy also holds keyBen's grant).
	f.setMembers("cy", "dee")
	if !f.has("api_keys", keyCy) || !f.has("api_keys", keyBen) {
		t.Fatal("grants of someone still on the team lapsed")
	}
	f.setMembers("dee")
	if f.has("api_keys", keyCy) || f.has("api_keys", keyBen) || f.keySees(keyCy) {
		t.Fatal("a grant outlived its granter's removal from the team")
	}

	// Being removed from the workspace.
	f.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')`, f.team, f.ids["eve"])
	keyEve := f.key("eve")
	if r := f.owner.do("DELETE", "/v1/members/"+f.ids["eve"].String(), nil); r.Status != 204 {
		t.Fatalf("remove eve: %d %s", r.Status, r.Raw)
	}
	if f.has("api_keys", keyEve) || f.keySees(keyEve) || !f.lapseAudited(keyEve) {
		t.Fatal("a grant outlived its granter's removal from the workspace")
	}

	// Deleting the account.
	if !f.has("webhooks", hookDee) || !f.delivered(hookDee) {
		t.Fatal("dee's grant lapsed early")
	}
	if r := f.users["dee"].do("DELETE", "/v1/me", map[string]any{"password": goodPassword}); r.Status != 204 {
		t.Fatalf("delete account: %d %s", r.Status, r.Raw)
	}
	if f.has("webhooks", hookDee) || f.delivered(hookDee) || !f.lapseAudited(hookDee) {
		t.Fatal("a grant outlived its granter's account")
	}
}

// TestGrantNeedsGranterOnTeam: a change that takes someone off a team without dropping
// their grants does not commit.
func TestGrantNeedsGranterOnTeam(t *testing.T) {
	f := newGrantFixture(t, "fay")
	f.key("fay")
	_, err := f.e.d.DB.Pool.Exec(f.ctx, `DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, f.team, f.ids["fay"])
	if err == nil || !strings.Contains(err.Error(), "private_team_grants_granter_on_team") {
		t.Fatalf("removal kept a dangling grant: %v", err)
	}
}

// TestGrantBackfill runs the migration's backfill: an existing grant is kept only while the
// key's or webhook's creator is on the team, and recorded as theirs.
func TestGrantBackfill(t *testing.T) {
	f := newGrantFixture(t, "gus", "hal")
	other, public := uuid.New(), uuid.New()
	f.exec(`INSERT INTO teams (id, workspace_id, name, private) VALUES ($1, $3, 'Legal', true), ($2, $3, 'Support', false)`, other, public, f.wid)
	f.exec(`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, f.team, f.ids["hal"])
	src, err := migrations.FS.ReadFile("000624_private_team_grants.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(src), "-- Existing grants")
	if i < 0 {
		t.Fatal("backfill not found")
	}
	backfill := string(src[i:])
	keyGus, keyHal, hookGus, hookNone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tx, err := f.e.d.DB.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	all := []uuid.UUID{f.team, other, public}
	for _, k := range []struct {
		id uuid.UUID
		by *uuid.UUID
	}{{keyGus, ptr(f.ids["gus"])}, {keyHal, ptr(f.ids["hal"])}} {
		if _, err := tx.Exec(f.ctx, `INSERT INTO api_keys (id, workspace_id, name, prefix, key_hash, scopes, created_by, private_team_ids)
			VALUES ($1, $2, 'Old', 'od_live_x', $3, '{tickets:read}', $4, $5)`, k.id, f.wid, []byte(k.id.String()), k.by, all); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range []struct {
		id uuid.UUID
		by *uuid.UUID
	}{{hookGus, ptr(f.ids["gus"])}, {hookNone, nil}} {
		if _, err := tx.Exec(f.ctx, `INSERT INTO webhooks (id, workspace_id, url, secret_enc, created_by, private_team_ids)
			VALUES ($1, $2, 'https://hooks.example.com/old', '\x00', $3, $4)`, h.id, f.wid, h.by, all); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(f.ctx, backfill); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		table string
		id    uuid.UUID
		want  []uuid.UUID
	}{{"api_keys", keyGus, []uuid.UUID{f.team}}, {"api_keys", keyHal, nil}, {"webhooks", hookGus, []uuid.UUID{f.team}}, {"webhooks", hookNone, nil}} {
		var got []uuid.UUID
		if err := tx.QueryRow(f.ctx, `SELECT private_team_ids FROM `+c.table+` WHERE id = $1`, c.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
			t.Fatalf("%s %s: %v, want %v", c.table, c.id, got, c.want)
		}
		var by []uuid.UUID
		if err := tx.QueryRow(f.ctx, `SELECT COALESCE(array_agg(granted_by), '{}') FROM private_team_grants WHERE api_key_id = $1 OR webhook_id = $1`,
			c.id).Scan(&by); err != nil {
			t.Fatal(err)
		}
		if len(by) != len(c.want) || (len(by) == 1 && by[0] != f.ids["gus"]) {
			t.Fatalf("%s %s granted by %v", c.table, c.id, by)
		}
	}
	// The backfilled grant follows its granter like any other.
	if _, err := tx.Exec(f.ctx, `DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, f.team, f.ids["gus"]); err != nil {
		t.Fatal(err)
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: f.ids["owner"], WorkspaceID: f.wid}
	keys, err := dropLapsedGrants(f.ctx, tx, p, f.wid)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(keys, keyGus) {
		t.Fatalf("lapsed keys %v", keys)
	}
	if _, err := tx.Exec(f.ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		t.Fatalf("grants left without a granter: %v", err)
	}
}
