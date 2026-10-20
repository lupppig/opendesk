package core

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func TestWebhookSeesTeam(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if !webhookSeesTeam(nil, nil) || !webhookSeesTeam([]uuid.UUID{a}, nil) {
		t.Fatal("events outside a private team reach every webhook")
	}
	if webhookSeesTeam(nil, &a) || webhookSeesTeam([]uuid.UUID{b}, &a) {
		t.Fatal("a private team's event reached a webhook without the team")
	}
	if !webhookSeesTeam([]uuid.UUID{b, a}, &a) {
		t.Fatal("a granted webhook missed its team's event")
	}
}

func TestSameIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if !sameIDs([]string{a.String(), b.String()}, []uuid.UUID{b, a}) || !sameIDs(nil, []uuid.UUID{}) {
		t.Fatal("same lists differ")
	}
	if sameIDs([]string{a.String()}, []uuid.UUID{b}) || sameIDs([]string{a.String()}, nil) {
		t.Fatal("different lists match")
	}
}

// ids reads a response's private_team_ids.
func (r resp) ids(key string) []string {
	var out []string
	list, _ := r.Body[key].([]any)
	for _, v := range list {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func TestPrivateTeamGrants(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, ownerEmail := e.signup("grants")
	wid := uuid.MustParse(owner.newWorkspace("Grants Co"))
	admin, adminEmail := e.signup("grantadmin")
	admin.workspace = wid.String()
	var ownerID, adminID uuid.UUID
	for email, id := range map[string]*uuid.UUID{ownerEmail: &ownerID, adminEmail: &adminID} {
		if err := e.d.DB.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'admin')`, wid, adminID); err != nil {
		t.Fatal(err)
	}
	private, other, public := uuid.New(), uuid.New(), uuid.New()
	if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO teams (id, workspace_id, name, private) VALUES
		($1, $4, 'Security', true), ($2, $4, 'Legal', true), ($3, $4, 'Support', false)`, private, other, public, wid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'observer')`, private, ownerID); err != nil {
		t.Fatal(err)
	}

	recv := httptest.NewServer(echoChallenges(nil))
	defer recv.Close()

	// Webhooks: only a member of the team adds it; the ids must be private teams.
	if r := admin.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL, "private_team_ids": []string{private.String()}}); r.Status != 403 {
		t.Fatalf("non-member granted a webhook: %d %s", r.Status, r.Raw)
	}
	for _, bad := range []string{public.String(), uuid.NewString(), "nope"} {
		if r := owner.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL, "private_team_ids": []string{bad}}); r.Status != 422 {
			t.Fatalf("granted %s: %d %s", bad, r.Status, r.Raw)
		}
	}
	r := owner.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL, "private_team_ids": []string{private.String()}})
	if r.Status != 201 || len(r.ids("private_team_ids")) != 1 || r.ids("private_team_ids")[0] != private.String() {
		t.Fatalf("member grant: %d %s", r.Status, r.Raw)
	}
	granted := r.str("id")
	r = owner.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL})
	if r.Status != 201 || r.ids("private_team_ids") != nil {
		t.Fatalf("plain webhook: %d %s", r.Status, r.Raw)
	}
	plain := r.str("id")

	// A private team's event reaches only the granted webhook; others reach both.
	var teamSeq, wsSeq int64
	err := pgx.BeginFunc(ctx, e.d.DB.Pool, func(tx pgx.Tx) error {
		env, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "team.updated", TeamID: &private, Data: map[string]any{}})
		if err != nil {
			return err
		}
		teamSeq = env.Seq
		env, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "team.updated", Data: map[string]any{}})
		wsSeq = env.Seq
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	dw := &dispatchWorker{s: e.s}
	for _, seq := range []int64{teamSeq, wsSeq} {
		if err := dw.Work(ctx, &river.Job[jobs.WebhookDispatchArgs]{Args: jobs.WebhookDispatchArgs{WorkspaceID: wid, EventSeq: seq}}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(hook string) int {
		var n int
		if err := e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries WHERE webhook_id = $1`, hook).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(granted) != 2 || count(plain) != 1 {
		t.Fatalf("deliveries: granted %d plain %d", count(granted), count(plain))
	}

	// An admin off the team may remove it but not add it back.
	if r := admin.do("PATCH", "/v1/webhooks/"+granted, map[string]any{"private_team_ids": []string{}}); r.Status != 200 || r.ids("private_team_ids") != nil {
		t.Fatalf("admin removal: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", "/v1/webhooks/"+granted, map[string]any{"private_team_ids": []string{private.String()}}); r.Status != 403 {
		t.Fatalf("admin re-grant: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PATCH", "/v1/webhooks/"+plain, map[string]any{"private_team_ids": []string{private.String()}}); r.Status != 200 {
		t.Fatalf("member grant on update: %d %s", r.Status, r.Raw)
	}
	// A grant kept as is passes for anyone; adding another team needs that team too.
	if r := admin.do("PATCH", "/v1/webhooks/"+plain, map[string]any{"private_team_ids": []string{private.String()}, "enabled": false}); r.Status != 200 {
		t.Fatalf("unchanged grant: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PATCH", "/v1/webhooks/"+plain, map[string]any{"private_team_ids": []string{private.String(), other.String()}}); r.Status != 403 {
		t.Fatalf("grant of a team the owner is not on: %d %s", r.Status, r.Raw)
	}

	// API keys follow the same rule and audit the change.
	if r := admin.do("POST", "/v1/api-keys", map[string]any{"name": "CI", "scopes": []string{"tickets:read"}, "private_team_ids": []string{private.String()}}); r.Status != 403 {
		t.Fatalf("non-member granted a key: %d %s", r.Status, r.Raw)
	}
	r = owner.do("POST", "/v1/api-keys", map[string]any{"name": "CI", "scopes": []string{"tickets:read"}, "private_team_ids": []string{private.String()}})
	if r.Status != 201 || len(r.ids("private_team_ids")) != 1 {
		t.Fatalf("member key grant: %d %s", r.Status, r.Raw)
	}
	key := r.str("id")
	var stored []uuid.UUID
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT private_team_ids FROM api_keys WHERE id = $1`, key).Scan(&stored); err != nil || len(stored) != 1 || stored[0] != private {
		t.Fatalf("stored grant: %v %v", stored, err)
	}
	if r := admin.do("PATCH", "/v1/api-keys/"+key, map[string]any{"private_team_ids": []string{}}); r.Status != 200 || r.ids("private_team_ids") != nil || r.str("name") != "CI" {
		t.Fatalf("admin key removal: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", "/v1/api-keys/"+key, map[string]any{"private_team_ids": []string{private.String()}}); r.Status != 403 {
		t.Fatalf("admin key re-grant: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PATCH", "/v1/api-keys/"+key, map[string]any{"name": "CI 2"}); r.Status != 200 || r.str("name") != "CI 2" {
		t.Fatalf("rename: %d %s", r.Status, r.Raw)
	}
	var audited int
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action IN ('api_key.updated', 'webhook.updated')`,
		wid).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited < 4 {
		t.Fatalf("audited %d updates", audited)
	}
}
