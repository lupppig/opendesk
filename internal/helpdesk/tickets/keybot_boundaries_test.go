package tickets_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/helpdesk/widget"
)

func TestAPIKeyBotCustomerEventsAndAgentRefetch(t *testing.T) {
	e := hdtest.New(t, tickets.Register, widget.Register)
	ws := e.NewWorkspace()
	var session struct {
		VisitorToken string `json:"visitor_token"`
	}
	e.Do("POST", "/v1/widget/session", hdtest.Auth{}, map[string]any{"key": ws.WidgetKey, "email": "visitor@example.test"}).Want(t, 200).JSON(t, &session)
	visitor := hdtest.Auth{Visitor: session.VisitorToken}
	var conversation struct {
		Conversation struct {
			ID string `json:"id"`
		} `json:"conversation"`
	}
	e.Do("POST", "/v1/widget/conversations", visitor, map[string]any{"body": "Hello"}).Want(t, 201).JSON(t, &conversation)
	bot := addBot(t, e, ws, "Julia", "Thanks, Julia")
	keyID, key := keyAs(t, e, ws, "Private automation name", &bot, auth.ScopeTicketsRead, auth.ScopeTicketsWrite)
	path := "/v1/tickets/" + conversation.Conversation.ID + "/messages"
	var reply domain.Message
	e.Do("POST", path, hdtest.Auth{Bearer: key}, map[string]any{"kind": "reply", "body_html": "<p>Hello from Julia</p>"}).Want(t, 201).JSON(t, &reply)
	if reply.Author.APIKey == nil || reply.Author.APIKey.ID != keyID {
		t.Fatal("POST omitted the key")
	}
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error { return domain.PublishMessageUpdated(e.Ctx, tx, ws.ID, reply.ID) }); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"message.created", "message.updated"} {
		var data []byte
		var visibility string
		e.Scalar(`SELECT data FROM events WHERE workspace_id=$1 AND type=$2 AND data->'message'->>'id'=$3`, &data, ws.ID, kind, reply.ID.String())
		e.Scalar(`SELECT visibility FROM events WHERE workspace_id=$1 AND type=$2 AND data->'message'->>'id'=$3`, &visibility, ws.ID, kind, reply.ID.String())
		var event struct {
			Message domain.Message `json:"message"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if visibility != "public" || event.Message.Author.APIKey != nil || event.Message.Author.Bot == nil || event.Message.Author.Bot.ID != bot || event.Message.AI || event.Message.Author.Type != "api" {
			t.Fatalf("%s public author: %+v", kind, event.Message.Author)
		}
		if strings.Contains(string(data), keyID.String()) || strings.Contains(string(data), "Private automation name") {
			t.Fatalf("%s disclosed key metadata", kind)
		}
	}
	var agent struct{ Data []domain.Message }
	e.Do("GET", path, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &agent)
	last := agent.Data[len(agent.Data)-1]
	if last.Author.APIKey == nil || last.Author.APIKey.ID != keyID || last.Author.APIKey.Name != "Private automation name" || last.AI {
		t.Fatalf("agent refetch author: %+v", last.Author)
	}
	var public struct{ Data []domain.Message }
	e.Do("GET", "/v1/widget/conversations/"+conversation.Conversation.ID+"/messages", visitor, nil).Want(t, 200).JSON(t, &public)
	last = public.Data[len(public.Data)-1]
	if last.Author.APIKey != nil || last.Author.Bot == nil || last.Author.Bot.ID != bot || last.AI {
		t.Fatalf("widget author: %+v", last.Author)
	}
	e.Do("POST", "/v1/widget/conversations/"+conversation.Conversation.ID+"/messages/"+reply.ID.String()+"/rating", visitor, map[string]any{"rating": "up"}).Want(t, 404)
}

func TestBotDeletionReplyLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, status, keyChange string
		deleted, blocked        bool
	}{
		{"pending revoked", "pending", "revoked", false, true},
		{"pending detached", "pending", "detached", false, true},
		{"pending expired", "pending", "expired", false, true},
		{"failed", "failed", "revoked", false, true},
		{"delivered", "sent", "revoked", false, false},
		{"bounced", "bounced", "revoked", false, false},
		{"nonqueued", "none", "revoked", false, false},
		{"deleted pending", "pending", "revoked", true, false},
		{"deleted failed", "failed", "revoked", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := hdtest.New(t, tickets.Register, bots.Register)
			ws := e.NewWorkspace()
			bot := addBot(t, e, ws, "Julia", "")
			keyID, key := keyAs(t, e, ws, "Replai", &bot, auth.ScopeTicketsRead, auth.ScopeTicketsWrite)
			mb := addMailbox(t, e, ws)
			tk := createTicket(t, e, ws, map[string]any{"mailbox_id": mb.String(), "contact": map[string]any{"email": "customer@example.test"}})
			var reply domain.Message
			e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", hdtest.Auth{Bearer: key}, map[string]any{"kind": "reply", "body_html": "<p>It shipped</p>"}).Want(t, 201).JSON(t, &reply)
			e.Exec(`UPDATE messages SET delivery_status=$2, deleted_at=CASE WHEN $3 THEN now() ELSE NULL END WHERE id=$1`, reply.ID, tc.status, tc.deleted)
			switch tc.keyChange {
			case "revoked":
				e.Exec(`UPDATE api_keys SET revoked_at=now() WHERE id=$1`, keyID)
			case "expired":
				e.Exec(`UPDATE api_keys SET expires_at=now()-interval '1 minute' WHERE id=$1`, keyID)
			case "detached":
				e.Exec(`UPDATE api_keys SET bot_id=NULL WHERE id=$1`, keyID)
			}
			result := e.Do("DELETE", "/v1/bots/"+bot.String(), ws.Admin.As(ws), nil)
			if tc.blocked {
				var problem struct{ Code, Detail string }
				result.Want(t, 409).JSON(t, &problem)
				if problem.Code != "bot_in_use" || !strings.Contains(problem.Detail, "replies") {
					t.Fatalf("problem: %+v", problem)
				}
				e.Exec(`UPDATE messages SET deleted_at=now() WHERE id=$1`, reply.ID)
				e.Do("DELETE", "/v1/bots/"+bot.String(), ws.Admin.As(ws), nil).Want(t, 204)
			} else {
				result.Want(t, 204)
			}
		})
	}
}

func TestBotDeletionQueuedChatFollowUp(t *testing.T) {
	for _, state := range []string{"available", "pending", "retryable", "running", "scheduled", "cancelled", "completed", "discarded"} {
		t.Run(state, func(t *testing.T) {
			e := hdtest.New(t, tickets.Register, bots.Register)
			ws := e.NewWorkspace()
			bot := addBot(t, e, ws, "Julia", "")
			keyID, key := keyAs(t, e, ws, "Replai", &bot, auth.ScopeTicketsWrite)
			tk := createTicket(t, e, ws, map[string]any{"kind": "note", "contact": map[string]any{"email": "away@example.test"}})
			e.Exec(`UPDATE tickets SET channel='chat' WHERE id=$1`, tk.ID)
			var reply domain.Message
			e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", hdtest.Auth{Bearer: key}, map[string]any{"kind": "reply", "body_html": "<p>Hello</p>"}).Want(t, 201).JSON(t, &reply)
			if reply.DeliveryStatus != "none" {
				t.Fatalf("delivery: %s", reply.DeliveryStatus)
			}
			var n int
			e.Scalar(`SELECT count(*) FROM river_job WHERE kind='email.chat_follow_up' AND args->>'message_id'=$1`, &n, reply.ID.String())
			if n != 1 {
				t.Fatalf("follow-up jobs: %d", n)
			}
			e.Exec(`UPDATE river_job SET state=$2::text::river_job_state, finalized_at=CASE WHEN $2 IN ('cancelled','completed','discarded') THEN now() ELSE NULL END WHERE kind='email.chat_follow_up' AND args->>'message_id'=$1`, reply.ID.String(), state)
			e.Exec(`UPDATE api_keys SET revoked_at=now() WHERE id=$1`, keyID)
			status := 409
			if state == "cancelled" || state == "completed" || state == "discarded" {
				status = 204
			}
			e.Do("DELETE", "/v1/bots/"+bot.String(), ws.Admin.As(ws), nil).Want(t, status)
		})
	}
}

func TestAPIKeyBotTicketAuthorization(t *testing.T) {
	e, ws := newEnv(t)
	other := e.NewWorkspace()
	bot := addBot(t, e, ws, "Julia", "Thanks, Julia")
	_, key := keyAs(t, e, ws, "Replai", &bot, auth.ScopeTicketsRead, auth.ScopeTicketsWrite)
	hidden := createTicket(t, e, other, map[string]any{"kind": "note"})
	e.Do("POST", "/v1/tickets/"+hidden.ID.String()+"/messages", hdtest.Auth{Bearer: key}, map[string]any{"kind": "reply", "body_html": "<p>x</p>"}).Want(t, 404)
	_, read := keyAs(t, e, ws, "Reader", &bot, auth.ScopeTicketsRead)
	own := createTicket(t, e, ws, map[string]any{"kind": "note"})
	e.Do("POST", "/v1/tickets/"+own.ID.String()+"/messages", hdtest.Auth{Bearer: read}, map[string]any{"kind": "reply", "body_html": "<p>x</p>"}).Want(t, 403)
	team := addTeam(t, e, ws, ws.Agent.ID)
	e.Exec(`UPDATE teams SET private=true WHERE id=$1`, team)
	e.Exec(`UPDATE tickets SET team_id=$2 WHERE id=$1`, own.ID, team)
	e.Do("POST", "/v1/tickets/"+own.ID.String()+"/messages", hdtest.Auth{Bearer: key}, map[string]any{"kind": "reply", "body_html": "<p>x</p>"}).Want(t, 404)
	e.Do("POST", "/v1/tickets/"+own.ID.String()+"/messages", ws.Viewer.As(ws), map[string]any{"kind": "reply", "body_html": "<p>x</p>"}).Want(t, 403)
}
