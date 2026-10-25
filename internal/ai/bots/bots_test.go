package bots_test

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/migrations"
)

func TestMain(m *testing.M) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		os.Setenv("TEST_DATABASE_URL", "postgres://opendesk:opendesk@localhost:55432/opendesk_bots?sslmode=disable")
	}
	hdtest.Main(m)
}

type botList struct {
	Data []bots.Bot `json:"data"`
}

func env(t *testing.T) (*hdtest.Env, hdtest.WS) {
	t.Helper()
	e := hdtest.New(t, helpdesk.Register, bots.Register)
	return e, e.NewWorkspace()
}

func list(t *testing.T, e *hdtest.Env, a hdtest.Auth) []bots.Bot {
	t.Helper()
	var l botList
	e.Do("GET", "/v1/bots", a, nil).Want(t, 200).JSON(t, &l)
	return l.Data
}

var pipLook = map[string]any{"kind": "built", "shape": "squircle", "fill": map[string]any{"type": "solid", "color": "#22C55E", "to": "#16a34a"},
	"eyes": "happy", "mouth": "grin", "accessory": "sprout", "accent": "#f59e0b", "blush": true, "background": "soft", "emoji": ""}

func TestBotsCRUD(t *testing.T) {
	e, ws := env(t)
	admin, agent, viewer := ws.Admin.As(ws), ws.Agent.As(ws), ws.Viewer.As(ws)

	// A workspace always has its default bot, Ollie, which members can read.
	l := list(t, e, viewer)
	if len(l) != 1 || !l[0].IsDefault || l[0].Name != "Ollie" || l[0].Look != bots.OllieLook || l[0].Personality.Tone != "friendly" {
		t.Fatalf("default = %+v", l)
	}
	if len(l[0].Usage.Widgets) != 1 || l[0].Usage.Widgets[0].ID != ws.WidgetID {
		t.Fatalf("default usage = %+v", l[0].Usage)
	}
	ollie := l[0]

	_, seq := e.Events(ws.ID, 0)
	body := map[string]any{"name": "Pip", "role": "Support sidekick", "language": "en", "look": pipLook,
		"personality": map[string]any{"tone": "playful", "emoji": "lots", "instructions": "Mention the status page.", "blocked_topics": []string{"Pricing", "pricing"}}}
	e.Do("POST", "/v1/bots", agent, body).Want(t, 403)
	e.Do("POST", "/v1/bots", viewer, body).Want(t, 403)
	var pip bots.Bot
	e.Do("POST", "/v1/bots", admin, body).Want(t, 201).JSON(t, &pip)
	if pip.Name != "Pip" || pip.IsDefault || pip.Look.Fill.Color != "#22c55e" || pip.Personality.Emoji != "lots" ||
		pip.Personality.ReplyLength != "short" || !slices.Equal(pip.Personality.BlockedTopics, []string{"Pricing"}) {
		t.Fatalf("pip = %+v", pip)
	}
	if !strings.Contains(pip.AvatarURL, "/v1/public/bots/"+pip.ID.String()+"/avatar.png?v=") || pip.ImageURL != nil {
		t.Fatalf("avatar_url = %q", pip.AvatarURL)
	}
	if types, _ := e.Events(ws.ID, seq); !slices.Contains(types, "bot.created") {
		t.Fatalf("events = %v", types)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'bot.created'`, &n, ws.ID)
	if n != 1 {
		t.Fatalf("audit = %d", n)
	}

	// The look is checked against its allow-list; the personality and names too.
	for _, bad := range []map[string]any{
		{"name": "X", "look": merge(pipLook, map[string]any{"svg": "<svg onload=alert(1)>"})},
		{"name": "X", "look": merge(pipLook, map[string]any{"shape": "star"})},
		{"name": "X", "look": merge(pipLook, map[string]any{"accent": "red"})},
		{"name": "X", "look": merge(pipLook, map[string]any{"fill": map[string]any{"type": "solid", "color": "url(#x)", "to": "#000000"}})},
		{"name": "X", "look": merge(pipLook, map[string]any{"kind": "emoji", "emoji": "hi"})},
		{"name": "X", "look": merge(pipLook, map[string]any{"kind": "image"})},
		{"name": "<b>X</b>"},
		{"name": "X", "role": "a\x00b"},
		{"name": "X", "language": "english please"},
		{"name": "X", "personality": map[string]any{"tone": "custom"}},
		{"name": "X", "personality": map[string]any{"greeting": "<script>"}},
		{"name": "X", "personality": map[string]any{"instructions": strings.Repeat("a", 4001)}},
	} {
		e.Do("POST", "/v1/bots", admin, bad).Want(t, 422)
	}
	// An emoji face is fine.
	var fox bots.Bot
	e.Do("POST", "/v1/bots", admin, map[string]any{"name": "Fox", "look": merge(pipLook, map[string]any{"kind": "emoji", "emoji": "🦊"})}).Want(t, 201).JSON(t, &fox)

	// PATCH merges the personality and replaces the look whole.
	var got bots.Bot
	e.Do("PATCH", "/v1/bots/"+pip.ID.String(), agent, map[string]any{"name": "Pippa"}).Want(t, 403)
	e.Do("PATCH", "/v1/bots/"+pip.ID.String(), admin, map[string]any{"personality": map[string]any{"reply_length": "detailed"},
		"look": merge(pipLook, map[string]any{"eyes": "star"})}).Want(t, 200).JSON(t, &got)
	if got.Personality.ReplyLength != "detailed" || got.Personality.Emoji != "lots" || got.Look.Eyes != "star" {
		t.Fatalf("patched = %+v", got)
	}

	// Duplicate, reorder, default.
	var dup bots.Bot
	e.Do("POST", "/v1/bots/"+pip.ID.String()+"/duplicate", admin, nil).Want(t, 201).JSON(t, &dup)
	if dup.Name != "Pip copy" || dup.IsDefault || dup.Look.Eyes != "star" {
		t.Fatalf("dup = %+v", dup)
	}
	order := []string{fox.ID.String(), ollie.ID.String(), dup.ID.String(), pip.ID.String()}
	e.Do("POST", "/v1/bots/reorder", admin, map[string]any{"ids": order[:3]}).Want(t, 422)
	var rl botList
	e.Do("POST", "/v1/bots/reorder", admin, map[string]any{"ids": order}).Want(t, 200).JSON(t, &rl)
	for i, b := range rl.Data {
		if b.ID.String() != order[i] {
			t.Fatalf("order %d = %s", i, b.Name)
		}
	}
	e.Do("POST", "/v1/bots/"+pip.ID.String()+"/default", admin, nil).Want(t, 200)
	for _, b := range list(t, e, viewer) {
		if b.IsDefault != (b.ID == pip.ID) {
			t.Fatalf("default moved wrong: %+v", b)
		}
	}

	// The default bot and a bot that answers somewhere stay.
	e.Do("DELETE", "/v1/bots/"+pip.ID.String(), admin, nil).Want(t, 409)
	e.Do("PATCH", "/v1/widgets/"+ws.WidgetID.String(), admin, map[string]any{"bot_id": fox.ID.String()}).Want(t, 200)
	r := e.Do("DELETE", "/v1/bots/"+fox.ID.String(), admin, nil).Want(t, 409)
	if !strings.Contains(string(r.Body), "bot_in_use") || !strings.Contains(string(r.Body), "Website") {
		t.Fatalf("in use = %s", r.Body)
	}
	e.Do("DELETE", "/v1/bots/"+dup.ID.String(), agent, nil).Want(t, 403)
	e.Do("DELETE", "/v1/bots/"+dup.ID.String(), admin, nil).Want(t, 204)
	e.Do("GET", "/v1/bots/"+dup.ID.String(), viewer, nil).Want(t, 404)

	// Another workspace sees none of them and cannot use them.
	other := e.NewWorkspace()
	oa := other.Admin.As(other)
	e.Do("GET", "/v1/bots/"+pip.ID.String(), oa, nil).Want(t, 404)
	e.Do("PATCH", "/v1/bots/"+pip.ID.String(), oa, map[string]any{"name": "Mine"}).Want(t, 404)
	e.Do("DELETE", "/v1/bots/"+ollie.ID.String(), oa, nil).Want(t, 404)
	e.Do("PATCH", "/v1/widgets/"+other.WidgetID.String(), oa, map[string]any{"bot_id": pip.ID.String()}).Want(t, 422)
	e.Do("PATCH", "/v1/widgets/"+other.WidgetID.String(), oa, map[string]any{"bot_id": "not-an-id"}).Want(t, 422)
}

func TestBotLimitAndWidgetConfig(t *testing.T) {
	e, ws := env(t)
	admin := ws.Admin.As(ws)
	list(t, e, admin)
	for i := 0; i < bots.MaxBots-1; i++ {
		e.Exec(`INSERT INTO bots (workspace_id, name, look, position) VALUES ($1, $2, $3, $4)`, ws.ID, "Bot "+crypto.RandomString(4), bots.OllieLook, i+1)
	}
	r := e.Do("POST", "/v1/bots", admin, map[string]any{"name": "One more"}).Want(t, 422)
	if !strings.Contains(string(r.Body), "limit_reached") {
		t.Fatalf("limit = %s", r.Body)
	}

	// The widget shows the bot it answers as; null falls back to the default.
	cfg := e.Do("GET", "/v1/widget/config?key="+ws.WidgetKey, hdtest.Auth{}, nil).Want(t, 200).Map(t)
	if b := cfg["bot"].(map[string]any); b["name"] != "Ollie" || b["look"].(map[string]any)["shape"] != "blob" {
		t.Fatalf("config bot = %v", b)
	}
	var mid uuid.UUID
	e.Scalar(`SELECT id FROM bots WHERE workspace_id = $1 AND NOT is_default ORDER BY position LIMIT 1`, &mid, ws.ID)
	e.Exec(`UPDATE bots SET name = 'Margin', role = 'Concierge', personality = '{"greeting":"Good afternoon."}' WHERE id = $1`, mid)
	e.Do("PATCH", "/v1/widgets/"+ws.WidgetID.String(), admin, map[string]any{"bot_id": mid.String()}).Want(t, 200)
	cfg = e.Do("GET", "/v1/widget/config?key="+ws.WidgetKey, hdtest.Auth{}, nil).Want(t, 200).Map(t)
	if b := cfg["bot"].(map[string]any); b["name"] != "Margin" || b["role"] != "Concierge" || b["greeting"] != "Good afternoon." || b["id"] != mid.String() {
		t.Fatalf("config bot = %v", b)
	}
	var audited int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'widget.updated' AND data ->> 'bot_id' = $2`, &audited, ws.ID, mid.String())
	if audited != 1 {
		t.Fatalf("widget audit = %d", audited)
	}
	// Deleting a widget's bot is refused; a removed bot (FK) falls back to the default.
	e.Do("DELETE", "/v1/bots/"+mid.String(), admin, nil).Want(t, 409)
	e.Exec(`DELETE FROM bots WHERE id = $1`, mid)
	cfg = e.Do("GET", "/v1/widget/config?key="+ws.WidgetKey, hdtest.Auth{}, nil).Want(t, 200).Map(t)
	if b := cfg["bot"].(map[string]any); b["name"] != "Ollie" {
		t.Fatalf("fallback bot = %v", b)
	}
}

func TestBotForMailbox(t *testing.T) {
	e, ws := env(t)
	admin := ws.Admin.As(ws)
	var pip bots.Bot
	e.Do("POST", "/v1/bots", admin, map[string]any{"name": "Pip", "look": pipLook}).Want(t, 201).JSON(t, &pip)
	mb := ids.New()
	e.Exec(`INSERT INTO mailboxes (id, workspace_id, name, email, inbound_kind, forwarding_token, outbound_kind, brand_id, bot_id)
		VALUES ($1, $2, 'Support', $3, 'forwarding', $4, 'system', $5, $6)`, mb, ws.ID, "s-"+crypto.RandomString(6)+"@acme.test",
		"t-"+crypto.RandomString(8), ws.BrandID, pip.ID)
	ctx := context.Background()
	got, err := bots.ForMailbox(ctx, e.D.DB.Pool, ws.ID, &mb)
	if err != nil || got.ID != pip.ID {
		t.Fatalf("for mailbox = %v %v", got.Name, err)
	}
	// Another workspace's mailbox id never resolves to its bot.
	other := e.NewWorkspace()
	if got, err = bots.ForMailbox(ctx, e.D.DB.Pool, other.ID, &mb); err != nil || got.ID == pip.ID {
		t.Fatalf("cross workspace = %v %v", got.Name, err)
	}
	if err := bots.Check(ctx, e.D.DB.Pool, other.ID, pip.ID); err == nil {
		t.Fatal("check passed for another workspace's bot")
	}
	for _, b := range list(t, e, admin) {
		if b.ID == pip.ID && (len(b.Usage.Mailboxes) != 1 || b.Usage.Mailboxes[0].ID != mb) {
			t.Fatalf("usage = %+v", b.Usage)
		}
	}
}

func TestMailboxBotPatch(t *testing.T) {
	e := hdtest.New(t, helpdesk.Register, bots.Register, email.Register)
	ws := e.NewWorkspace()
	admin := ws.Admin.As(ws)
	var pip bots.Bot
	e.Do("POST", "/v1/bots", admin, map[string]any{"name": "Pip", "look": pipLook}).Want(t, 201).JSON(t, &pip)
	mb := ids.New()
	e.Exec(`INSERT INTO mailboxes (id, workspace_id, name, email, inbound_kind, forwarding_token, outbound_kind, brand_id)
		VALUES ($1, $2, 'Support', $3, 'forwarding', $4, 'system', $5)`, mb, ws.ID, "s-"+crypto.RandomString(6)+"@acme.test",
		"t-"+crypto.RandomString(8), ws.BrandID)
	path := "/v1/mailboxes/" + mb.String()

	var out struct {
		BotID *uuid.UUID `json:"bot_id"`
	}
	e.Do("PATCH", path, admin, map[string]any{"bot_id": pip.ID}).Want(t, 200).JSON(t, &out)
	if out.BotID == nil || *out.BotID != pip.ID {
		t.Fatalf("bot_id = %v", out.BotID)
	}
	var data string
	e.Scalar(`SELECT data::text FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated' ORDER BY created_at DESC LIMIT 1`, &data, ws.ID)
	if !strings.Contains(data, pip.ID.String()) || !strings.Contains(data, `"bot_id"`) {
		t.Fatalf("audit = %s", data)
	}

	// A bot of another workspace, or not a bot at all, is refused and nothing changes.
	other := e.NewWorkspace()
	oadmin := other.Admin.As(other)
	var theirs bots.Bot
	e.Do("POST", "/v1/bots", oadmin, map[string]any{"name": "Theirs"}).Want(t, 201).JSON(t, &theirs)
	e.Do("PATCH", path, admin, map[string]any{"bot_id": theirs.ID}).Want(t, 422)
	e.Do("PATCH", path, admin, map[string]any{"bot_id": "nope"}).Want(t, 422)
	// Another workspace can't touch this mailbox; a viewer can't change it.
	e.Do("PATCH", path, oadmin, map[string]any{"bot_id": theirs.ID}).Want(t, 404)
	e.Do("PATCH", path, ws.Viewer.As(ws), map[string]any{"bot_id": nil}).Want(t, 403)
	var stored *uuid.UUID
	e.Scalar(`SELECT bot_id FROM mailboxes WHERE id = $1`, &stored, mb)
	if stored == nil || *stored != pip.ID {
		t.Fatalf("stored = %v", stored)
	}

	// null follows the default again; a patch without bot_id leaves it.
	e.Do("PATCH", path, admin, map[string]any{"bot_id": pip.ID}).Want(t, 200)
	e.Do("PATCH", path, admin, map[string]any{"name": "Help"}).Want(t, 200).JSON(t, &out)
	if out.BotID == nil {
		t.Fatal("bot_id cleared by an unrelated patch")
	}
	e.Do("PATCH", path, admin, map[string]any{"bot_id": nil}).Want(t, 200).JSON(t, &out)
	if out.BotID != nil {
		t.Fatalf("bot_id after null = %v", out.BotID)
	}
}

func TestAvatarEndpoints(t *testing.T) {
	e, ws := env(t)
	admin := ws.Admin.As(ws)
	var pip bots.Bot
	e.Do("POST", "/v1/bots", admin, map[string]any{"name": "Pip", "look": pipLook}).Want(t, 201).JSON(t, &pip)

	r := e.Do("GET", "/v1/public/bots/"+pip.ID.String()+"/avatar.svg", hdtest.Auth{}, nil).Want(t, 200)
	if string(r.Body) != bots.RenderSVG(pip.Look) || r.Header.Get("Content-Type") != "image/svg+xml" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("svg %d %v", len(r.Body), r.Header)
	}
	for _, s := range []string{"", "?s=128", "?s=256"} {
		r = e.Do("GET", "/v1/public/bots/"+pip.ID.String()+"/avatar.png"+s, hdtest.Auth{}, nil).Want(t, 200)
		if !bytes.HasPrefix(r.Body, []byte("\x89PNG")) || r.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("png %q", s)
		}
	}
	e.Do("GET", "/v1/public/bots/ollie/avatar.svg", hdtest.Auth{}, nil).Want(t, 200)
	e.Do("GET", "/v1/public/bots/"+uuid.NewString()+"/avatar.svg", hdtest.Auth{}, nil).Want(t, 404)
	e.Do("GET", "/v1/public/bots/nope/avatar.svg", hdtest.Auth{}, nil).Want(t, 404)
}

// TestBotsBackfill runs the migration over workspaces as they were before bots: the
// default bot takes the agent name, picture, tone and instructions, a widget with its own
// bot name gets its own bot, and the old fields are gone.
func TestBotsBackfill(t *testing.T) {
	e, ws := env(t)
	up, err := migrations.FS.ReadFile("000625_bots.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrations.FS.ReadFile("000625_bots.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := e.D.DB.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(down))
	img := ids.New()
	second := ids.New()
	exec(`UPDATE workspaces SET settings = '{"widget":{"bot_name":"Sunny"},"ai":{"tone":"Like a pirate","widget_instructions":"Say ahoy."}}',
		brand_images = jsonb_build_object('widget_bot', 'img:' || $2::text) WHERE id = $1`, ws.ID, img)
	exec(`INSERT INTO images (id, workspace_id, owner_type, owner_id, slot, source, storage_key, sha256)
		VALUES ($1, $2, 'workspace', $2, 'widget_bot', 'upload', 'k', 'x')`, img, ws.ID)
	exec(`UPDATE widgets SET settings = '{"bot_name":"Margin"}' WHERE id = $1`, ws.WidgetID)
	exec(`INSERT INTO widgets (id, workspace_id, brand_id, name, key) VALUES ($1, $2, $3, 'Docs', $4)`, second, ws.ID, ws.BrandID, "wk_"+crypto.RandomString(20))
	exec(string(up))

	var name, tone, custom, ins, avatar, kind string
	if err := tx.QueryRow(ctx, `SELECT name, personality ->> 'tone', personality ->> 'custom_tone', personality ->> 'instructions', avatar, look ->> 'kind'
		FROM bots WHERE workspace_id = $1 AND is_default`, ws.ID).Scan(&name, &tone, &custom, &ins, &avatar, &kind); err != nil {
		t.Fatal(err)
	}
	if name != "Sunny" || tone != "custom" || custom != "Like a pirate" || ins != "Say ahoy." || avatar != "img:"+img.String() || kind != "image" {
		t.Fatalf("default = %q %q %q %q %q %q", name, tone, custom, ins, avatar, kind)
	}
	var owner string
	if err := tx.QueryRow(ctx, `SELECT owner_type FROM images WHERE id = $1`, img).Scan(&owner); err != nil || owner != "bot" {
		t.Fatalf("image owner = %q %v", owner, err)
	}
	var first, other *string
	if err := tx.QueryRow(ctx, `SELECT (SELECT b.name FROM widgets g JOIN bots b ON b.id = g.bot_id WHERE g.id = $1),
		(SELECT b.name FROM widgets g JOIN bots b ON b.id = g.bot_id WHERE g.id = $2)`, ws.WidgetID, second).Scan(&first, &other); err != nil {
		t.Fatal(err)
	}
	if first == nil || *first != "Margin" || other != nil {
		t.Fatalf("widget bots = %v %v", first, other)
	}
	var left bool
	if err := tx.QueryRow(ctx, `SELECT w.settings -> 'widget' ? 'bot_name' OR w.settings -> 'ai' ? 'widget_instructions' OR w.brand_images ? 'widget_bot'
		OR EXISTS (SELECT 1 FROM widgets g WHERE g.workspace_id = w.id AND g.settings ? 'bot_name') FROM workspaces w WHERE w.id = $1`, ws.ID).Scan(&left); err != nil || left {
		t.Fatalf("old fields left = %v %v", left, err)
	}
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
