package tickets_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
)

// addBot stores a bot of ws named name with signOff as its email sign-off.
func addBot(t *testing.T, e *hdtest.Env, ws hdtest.WS, name, signOff string) uuid.UUID {
	t.Helper()
	look, err := json.Marshal(bots.OllieLook)
	if err != nil {
		t.Fatal(err)
	}
	pers, err := json.Marshal(map[string]any{"sign_off": signOff})
	if err != nil {
		t.Fatal(err)
	}
	id := ids.New()
	e.Exec(`INSERT INTO bots (id, workspace_id, name, look, personality) VALUES ($1, $2, $3, $4, $5)`, id, ws.ID, name, look, pers)
	return id
}

// keyAs makes an API key of ws that speaks as bot (none when bot is nil) and returns its id and secret.
func keyAs(t *testing.T, e *hdtest.Env, ws hdtest.WS, name string, bot *uuid.UUID, scopes ...string) (uuid.UUID, string) {
	t.Helper()
	secret := "od_live_" + crypto.RandomToken(32)
	id := ids.New()
	e.Exec(`INSERT INTO api_keys (id, workspace_id, name, prefix, key_hash, scopes, bot_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, ws.ID, name, secret[:12], crypto.HashToken(secret), scopes, bot)
	return id, secret
}

func TestAPIKeySpeaksAsBot(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	julia := addBot(t, e, ws, "Julia", "Warm regards,\nJulia from {{ brand.name }}")
	keyID, key := keyAs(t, e, ws, "Replai", &julia, auth.ScopeTicketsRead, auth.ScopeTicketsWrite)
	tk := createTicket(t, e, ws, map[string]any{"mailbox_id": mb.String(), "contact": map[string]any{"email": "carla@example.com", "name": "Carla"}})
	path := "/v1/tickets/" + tk.ID.String() + "/messages"
	as := hdtest.Auth{Bearer: key}

	// A reply is the bot's: its name, its face, the key that wrote it, the bot as the agent
	// in the reply's variables and its sign-off at the end, kept literally.
	var reply domain.Message
	e.Do("POST", path, as, map[string]any{"kind": "reply", "body_html": "<p>Hi Carla, I'm {{ agent.first_name }}. It shipped today.</p>"}).
		Want(t, 201).JSON(t, &reply)
	if reply.Author.Type != "api" || reply.AI || reply.Author.Bot == nil || reply.Author.Bot.ID != julia || reply.Author.Bot.Name != "Julia" ||
		reply.Author.APIKey == nil || reply.Author.APIKey.ID != keyID || reply.Author.APIKey.Name != "Replai" || reply.FromName != "Julia" {
		t.Fatalf("reply author %+v from %q ai %v", reply.Author, reply.FromName, reply.AI)
	}
	if reply.DeliveryStatus != "pending" || !strings.Contains(reply.BodyText, "I'm Julia.") ||
		!strings.HasSuffix(reply.BodyText, "Warm regards,\nJulia from {\u200b{ brand.name }}") ||
		!strings.Contains(reply.BodyHTML, "<p>Warm regards,<br>Julia from {\u200b{ brand.name }}</p>") {
		t.Fatalf("reply body %q / %q", reply.BodyHTML, reply.BodyText)
	}

	// A reply that already ends with the sign-off is not signed twice.
	e.Exec(`UPDATE bots SET personality = jsonb_build_object('sign_off', $2::text) WHERE id = $1`, julia, "Warm regards,\nJulia")
	e.Do("POST", path, as, map[string]any{"kind": "reply", "body_html": "<p>One more thing.</p><p>Warm  regards,<br>JULIA</p>"}).
		Want(t, 201).JSON(t, &reply)
	if n := strings.Count(strings.ToLower(reply.BodyText), "warm regards"); n != 1 {
		t.Fatalf("signed %d times: %q", n, reply.BodyText)
	}

	// A note is the bot's too, without a sign-off; the team reads who wrote it.
	var note domain.Message
	e.Do("POST", path, as, map[string]any{"kind": "note", "body_html": "<p>Checked the carrier.</p>"}).Want(t, 201).JSON(t, &note)
	if note.Author.Bot == nil || note.Author.Bot.ID != julia || note.Author.APIKey == nil || note.FromName != "Julia" || note.BodyText != "Checked the carrier." {
		t.Fatalf("note %+v %q", note.Author, note.BodyText)
	}
	var msgs struct{ Data []domain.Message }
	e.Do("GET", path, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &msgs)
	last := msgs.Data[len(msgs.Data)-1]
	if last.ID != note.ID || last.Author.Bot == nil || last.Author.APIKey == nil || last.Author.APIKey.Name != "Replai" {
		t.Fatalf("timeline %+v", last.Author)
	}
	// What a customer may see of it names neither the key nor its id.
	if pub := domain.PublicMessage(reply); pub.Author.APIKey != nil || pub.Author.Bot == nil {
		t.Fatalf("public author %+v", pub.Author)
	}

	// A reply that is not an email carries no sign-off.
	other := createTicket(t, e, ws, map[string]any{"kind": "note"})
	e.Do("POST", "/v1/tickets/"+other.ID.String()+"/messages", as, map[string]any{"kind": "reply", "body_html": "<p>On it.</p>"}).
		Want(t, 201).JSON(t, &reply)
	if reply.Author.Bot == nil || reply.DeliveryStatus != "none" || reply.BodyText != "On it." {
		t.Fatalf("api ticket reply %+v %q", reply.Author, reply.BodyText)
	}

	// The first message of a ticket the key opens is the bot's as well.
	var opened domain.Ticket
	e.Do("POST", "/v1/tickets", as, map[string]any{"subject": "Your order", "mailbox_id": mb.String(),
		"contact": map[string]any{"email": "eve@example.com"}, "body_html": "<p>Your order is ready.</p>"}).Want(t, 201).JSON(t, &opened)
	e.Do("GET", "/v1/tickets/"+opened.ID.String()+"/messages", as, nil).Want(t, 200).JSON(t, &msgs)
	first := msgs.Data[0]
	if first.Author.Bot == nil || first.Author.Bot.ID != julia || first.Author.APIKey == nil || !strings.HasSuffix(first.BodyText, "Warm regards,\nJulia") {
		t.Fatalf("opening message %+v %q", first.Author, first.BodyText)
	}

	// A key that speaks as no bot writes as itself, as before, and is still named.
	plainID, plain := keyAs(t, e, ws, "Sync", nil, auth.ScopeTicketsWrite)
	e.Do("POST", path, hdtest.Auth{Bearer: plain}, map[string]any{"kind": "reply", "body_html": "<p>Hello</p>"}).Want(t, 201).JSON(t, &reply)
	if reply.Author.Type != "api" || reply.Author.Bot != nil || reply.Author.APIKey == nil || reply.Author.APIKey.ID != plainID ||
		reply.FromName != "Acme Support" || reply.BodyText != "Hello" {
		t.Fatalf("plain key reply %+v %q %q", reply.Author, reply.FromName, reply.BodyText)
	}

	// A teammate's reply names no key and no bot.
	e.Do("POST", path, ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>Hi</p>"}).Want(t, 201).JSON(t, &reply)
	if reply.Author.Bot != nil || reply.Author.APIKey != nil {
		t.Fatalf("agent reply %+v", reply.Author)
	}
}
