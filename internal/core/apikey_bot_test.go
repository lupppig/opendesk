package core

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ids"
)

// An API key may speak as one of the workspace's bots (contracts/api.md "API keys"):
// chosen when it is made or later, only for a key that writes tickets, and audit-logged.
func TestAPIKeySpeaksAsBotSetting(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, _ := e.signup("keybot")
	wid := c.newWorkspace("Key Bot Co")
	bot := func(ws string, name string) string {
		id := ids.New()
		if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO bots (id, workspace_id, name, look) VALUES ($1, $2, $3, '{}')`, id, ws, name); err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	julia, pip := bot(wid, "Julia"), bot(wid, "Pip")
	o, _ := e.signup("keybotother")
	elsewhere := bot(o.newWorkspace("Elsewhere Co"), "Stranger")

	write := []string{"tickets:read", "tickets:write"}
	r := c.do("POST", "/v1/api-keys", map[string]any{"name": "Replai", "scopes": write, "bot_id": julia})
	if r.Status != 201 || r.str("bot_id") != julia || r.str("secret") == "" {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	key := r.str("id")
	// A key that cannot write tickets has nothing to say as a bot; a bot must be the workspace's.
	for name, body := range map[string]map[string]any{
		"read only":       {"name": "Reader", "scopes": []string{"tickets:read"}, "bot_id": julia},
		"other workspace": {"name": "Stray", "scopes": write, "bot_id": elsewhere},
		"not an id":       {"name": "Typo", "scopes": write, "bot_id": "julia"},
	} {
		if r := c.do("POST", "/v1/api-keys", body); r.Status != 422 {
			t.Fatalf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	if r := c.do("POST", "/v1/api-keys", map[string]any{"name": "Plain", "scopes": write}); r.Status != 201 || r.Body["bot_id"] != nil {
		t.Fatalf("without a bot: %d %s", r.Status, r.Raw)
	}
	reader := c.do("POST", "/v1/api-keys", map[string]any{"name": "Reader", "scopes": []string{"tickets:read"}}).str("id")

	// PATCH changes it, leaves it as it is when left out, and null clears it.
	path := "/v1/api-keys/" + key
	if r := c.do("PATCH", path, map[string]any{"bot_id": pip}); r.Status != 200 || r.str("bot_id") != pip {
		t.Fatalf("switch: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", path, map[string]any{"name": "Replai prod"}); r.Status != 200 || r.str("bot_id") != pip {
		t.Fatalf("rename kept the bot: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", path, map[string]any{"bot_id": elsewhere}); r.Status != 422 {
		t.Fatalf("other workspace's bot: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", "/v1/api-keys/"+reader, map[string]any{"bot_id": julia}); r.Status != 422 {
		t.Fatalf("read-only key: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", path, map[string]any{"bot_id": nil}); r.Status != 200 || r.Body["bot_id"] != nil {
		t.Fatalf("clear: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", path, map[string]any{"bot_id": julia}); r.Status != 200 {
		t.Fatalf("set again: %d %s", r.Status, r.Raw)
	}

	// The list shows it, and the audit log says which bot each change chose.
	r = c.do("GET", "/v1/api-keys", nil)
	found := false
	for _, row := range r.Body["data"].([]any) {
		if k := row.(map[string]any); k["id"] == key {
			found = k["bot_id"] == julia
		}
	}
	if !found {
		t.Fatalf("list: %s", r.Raw)
	}
	var bots []*string
	rows, err := e.d.DB.Pool.Query(ctx, `SELECT data->>'bot_id' FROM audit_logs WHERE workspace_id = $1 AND entity_id = $2
		AND action IN ('api_key.created', 'api_key.updated') ORDER BY created_at, id`, wid, uuid.MustParse(key))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b *string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		bots = append(bots, b)
	}
	rows.Close()
	want := []*string{&julia, &pip, &pip, nil, &julia}
	if len(bots) != len(want) {
		t.Fatalf("audit = %d rows", len(bots))
	}
	for i := range want {
		if (bots[i] == nil) != (want[i] == nil) || bots[i] != nil && *bots[i] != *want[i] {
			t.Fatalf("audit row %d = %v", i, bots[i])
		}
	}
}

func TestAPIKeyBotAndScopesUpdatedTogether(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, _ := e.signup("keybotscopes")
	wid := c.newWorkspace("Bot Scopes Co")
	bot := ids.New()
	if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO bots (id, workspace_id, name, look) VALUES ($1, $2, 'Helper', '{}')`, bot, wid); err != nil {
		t.Fatal(err)
	}
	created := c.do("POST", "/v1/api-keys", map[string]any{"name": "Helper key", "scopes": []string{"tickets:read", "tickets:write"}})
	if created.Status != 201 {
		t.Fatalf("create: %d %s", created.Status, created.Raw)
	}
	path := "/v1/api-keys/" + created.str("id")
	changed := c.do("PATCH", path, map[string]any{"bot_id": bot.String(), "scopes": []string{"tickets:read", "tickets:write", "contacts:read"}})
	if changed.Status != 200 || changed.str("bot_id") != bot.String() {
		t.Fatalf("combined update: %d %s", changed.Status, changed.Raw)
	}
	scopes := changed.Body["scopes"].([]any)
	if len(scopes) != 3 {
		t.Fatalf("scopes not saved: %s", changed.Raw)
	}
	cleared := c.do("PATCH", path, map[string]any{"bot_id": nil, "scopes": []string{"tickets:read"}})
	if cleared.Status != 200 || cleared.Body["bot_id"] != nil || len(cleared.Body["scopes"].([]any)) != 1 {
		t.Fatalf("combined removal: %d %s", cleared.Status, cleared.Raw)
	}
	invalid := c.do("PATCH", path, map[string]any{"bot_id": bot.String(), "scopes": []string{"contacts:read"}})
	if invalid.Status != 422 {
		t.Fatalf("bot requires write in final scopes: %d %s", invalid.Status, invalid.Raw)
	}
	current := c.do("GET", path, nil)
	if current.Status != 200 || current.Body["bot_id"] != nil || len(current.Body["scopes"].([]any)) != 1 || current.Body["scopes"].([]any)[0] != "tickets:read" {
		t.Fatalf("refused update changed key: %d %s", current.Status, current.Raw)
	}
}
