package core

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// An admin changes an existing key's scopes (contracts/api.md "API keys"): removing is
// immediate, adding asks for the same recent sign-in as making a key, every change is
// audit-logged with what it added and removed, and an API key never changes a key.
func TestAPIKeyScopesChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, email := e.signup("keyscopes")
	c.workspace = c.newWorkspace("Key Scopes Co")

	r := c.do("POST", "/v1/api-keys", map[string]any{"name": "Claimy", "scopes": []string{"tickets:read"}})
	if r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	key := r.str("id")
	path := "/v1/api-keys/" + key
	admin := c.do("POST", "/v1/api-keys", map[string]any{"name": "Admin tool", "scopes": []string{"settings:read", "settings:write"}})
	if admin.Status != 201 {
		t.Fatalf("create the settings key: %d %s", admin.Status, admin.Raw)
	}
	scopesOf := func(r resp) []string {
		var out []string
		for _, s := range r.Body["scopes"].([]any) {
			out = append(out, s.(string))
		}
		return out
	}

	// Adding with a fresh sign-in; the list comes back sorted, without duplicates.
	r = c.do("PATCH", path, map[string]any{"scopes": []string{"tickets:write", "tickets:read", "tickets:write"}})
	if r.Status != 200 || !slices.Equal(scopesOf(r), []string{"tickets:read", "tickets:write"}) {
		t.Fatalf("widen: %d %s", r.Status, r.Raw)
	}
	for name, scopes := range map[string][]string{"unknown": {"tickets:read", "tickets:admin"}, "none": {}} {
		if r := c.do("PATCH", path, map[string]any{"scopes": scopes}); r.Status != 422 {
			t.Fatalf("%s scope: %d %s", name, r.Status, r.Raw)
		}
	}
	// The same scopes again change nothing and log nothing.
	if r := c.do("PATCH", path, map[string]any{"scopes": []string{"tickets:read", "tickets:write"}}); r.Status != 200 {
		t.Fatalf("unchanged: %d %s", r.Status, r.Raw)
	}
	// A rename alone leaves the scopes as they are.
	if r := c.do("PATCH", path, map[string]any{"name": "Claimy robots"}); r.Status != 200 || !slices.Equal(scopesOf(r), []string{"tickets:read", "tickets:write"}) {
		t.Fatalf("rename: %d %s", r.Status, r.Raw)
	}

	// An API key cannot change a key, not even one allowed to write settings.
	keyCaller := e.client()
	keyCaller.token = admin.str("secret")
	if r := keyCaller.do("PATCH", path, map[string]any{"scopes": []string{"tickets:read"}}); r.Status != 403 {
		t.Fatalf("as an API key: %d %s", r.Status, r.Raw)
	}

	// An old sign-in still removes scopes, but adding one asks to sign in again.
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE sessions SET created_at = now() - interval '13 hours'
		WHERE user_id = (SELECT id FROM users WHERE email = $1)`, email); err != nil {
		t.Fatal(err)
	}
	if r := c.do("PATCH", path, map[string]any{"scopes": []string{"tickets:read"}}); r.Status != 200 || !slices.Equal(scopesOf(r), []string{"tickets:read"}) {
		t.Fatalf("narrow with an old sign-in: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", path, map[string]any{"scopes": []string{"tickets:read", "contacts:read"}}); r.Status != 403 || r.str("code") != "reauth_required" {
		t.Fatalf("widen with an old sign-in: %d %s", r.Status, r.Raw)
	}
	if r := c.do("GET", path, nil); !slices.Equal(scopesOf(r), []string{"tickets:read"}) {
		t.Fatalf("a refused change kept the scopes: %s", r.Raw)
	}

	// The audit log has one row per change, with what it added and removed.
	type change struct{ From, To, Added, Removed []string }
	rows, err := e.d.DB.Pool.Query(ctx, `SELECT data FROM audit_logs WHERE entity_id = $1 AND action = 'api_key.scopes_changed'
		ORDER BY created_at, id`, uuid.MustParse(key))
	if err != nil {
		t.Fatal(err)
	}
	var got []change
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var ch change
		if err := json.Unmarshal(raw, &ch); err != nil {
			t.Fatal(err)
		}
		got = append(got, ch)
	}
	rows.Close()
	want := []change{
		{From: []string{"tickets:read"}, To: []string{"tickets:read", "tickets:write"}, Added: []string{"tickets:write"}, Removed: []string{}},
		{From: []string{"tickets:read", "tickets:write"}, To: []string{"tickets:read"}, Added: []string{}, Removed: []string{"tickets:write"}},
	}
	if len(got) != len(want) {
		t.Fatalf("audit = %+v", got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if !slices.Equal(g.From, w.From) || !slices.Equal(g.To, w.To) || !slices.Equal(g.Added, w.Added) || !slices.Equal(g.Removed, w.Removed) {
			t.Fatalf("audit row %d = %+v, want %+v", i, g, w)
		}
	}
}

// collab:draft lets a key draft replies as the member who made it, so only that member gives
// it to an existing key.
func TestAPIKeyCollabDraftOnlyFromItsCreator(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, _ := e.signup("keycollab")
	c.workspace = c.newWorkspace("Key Collab Co")
	mine := c.do("POST", "/v1/api-keys", map[string]any{"name": "Mine", "scopes": []string{"tickets:read"}}).str("id")
	if r := c.do("PATCH", "/v1/api-keys/"+mine, map[string]any{"scopes": []string{"collab:draft", "tickets:read"}}); r.Status != 200 {
		t.Fatalf("creator adds collab:draft: %d %s", r.Status, r.Raw)
	}
	orphan := c.do("POST", "/v1/api-keys", map[string]any{"name": "Orphan", "scopes": []string{"tickets:read"}}).str("id")
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE api_keys SET created_by = NULL WHERE id = $1`, uuid.MustParse(orphan)); err != nil {
		t.Fatal(err)
	}
	if r := c.do("PATCH", "/v1/api-keys/"+orphan, map[string]any{"scopes": []string{"collab:draft", "tickets:read"}}); r.Status != 403 {
		t.Fatalf("someone else adds collab:draft: %d %s", r.Status, r.Raw)
	}
	// Taking it away is open to any admin.
	if r := c.do("PATCH", "/v1/api-keys/"+mine, map[string]any{"scopes": []string{"tickets:read"}}); r.Status != 200 {
		t.Fatalf("remove collab:draft: %d %s", r.Status, r.Raw)
	}
}
