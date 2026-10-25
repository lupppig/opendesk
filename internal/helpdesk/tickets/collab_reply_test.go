package tickets_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
)

// TestReplyFromOwnAddress checks from_account (contracts/collab.md "Answering tickets"): a reply
// from the member's own Collab address is queued for the account, keeps the ticket's reply
// address, and is refused where it does not apply.
func TestReplyFromOwnAddress(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	tk := createTicket(t, e, ws, map[string]any{"channel": "email", "mailbox_id": mb.String(), "kind": "note",
		"contact": map[string]any{"email": "carla@customer.test", "name": "Carla"}})
	account := ids.New()
	addr := "anna-" + account.String()[24:] + "@acme.test"
	e.Exec(`INSERT INTO collab_accounts (id, workspace_id, owner_id, provider, email, name, display_name, visibility, credentials_enc, status)
		VALUES ($1, $2, $3, 'google', $4, $4, '{agent} at Acme', 'private', '\x00', 'active')`, account, ws.ID, ws.Agent.ID, addr)
	path := "/v1/tickets/" + tk.ID.String() + "/messages"
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"kind": "reply", "body_html": "<p>On it</p>", "from_account": map[string]any{"account_id": account.String(), "reply_to": "mailbox"}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	var msg domain.Message
	e.Do("POST", path, ws.Agent.As(ws), body(nil)).Want(t, 201).JSON(t, &msg)
	if msg.FromAccount == nil || msg.FromAccount.AccountID != account || msg.FromAccount.Email != addr || msg.FromAccount.ReplyTo != "mailbox" {
		t.Fatalf("from_account %+v", msg.FromAccount)
	}
	if msg.FromEmail != addr || msg.FromName != "Anna Agent at Acme" || msg.MailboxID != nil || msg.DeliveryStatus != "pending" {
		t.Fatalf("message %s %q %v %s", msg.FromEmail, msg.FromName, msg.MailboxID, msg.DeliveryStatus)
	}
	if got := getTicket(t, e, ws, tk.ID); got.MailboxID == nil || *got.MailboxID != mb {
		t.Fatalf("reply address changed: %v", got.MailboxID)
	}
	if n := countRows(t, e, `SELECT count(*) FROM river_job WHERE kind = 'collab.send_ticket_reply' AND args->>'message_id' = $1`, msg.ID.String()); n != 1 {
		t.Fatalf("collab send jobs: %d", n)
	}
	if n := countRows(t, e, `SELECT count(*) FROM river_job WHERE kind = 'email.send_message' AND args->>'message_id' = $1`, msg.ID.String()); n != 0 {
		t.Fatalf("mailbox send jobs: %d", n)
	}

	// Where it does not apply.
	e.Do("POST", path, ws.Agent.As(ws), body(map[string]any{"mailbox_id": mb.String()})).Want(t, 422)
	e.Do("POST", path, ws.Agent.As(ws), body(map[string]any{"kind": "note"})).Want(t, 422)
	chat := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note", "contact": map[string]any{"email": "c@example.com"}})
	e.Do("POST", "/v1/tickets/"+chat.ID.String()+"/messages", ws.Agent.As(ws), body(nil)).Want(t, 422)
	// The account is Anna's alone; viewers and API keys never send from one.
	e.Do("POST", path, ws.Admin.As(ws), body(nil)).Want(t, 404)
	e.Do("POST", path, ws.Viewer.As(ws), body(nil)).Want(t, 403)
	e.Do("POST", path, hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeTicketsWrite, auth.ScopeTicketsRead)}, body(nil)).Want(t, 403)
	e.Do("POST", path, ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>x</p>",
		"from_account": map[string]any{"account_id": uuid.NewString()}}).Want(t, 404)
	// Off for the workspace.
	e.SetSettings(ws, map[string]any{"collab_ticket_replies": false})
	e.Do("POST", path, ws.Agent.As(ws), body(nil)).Want(t, 409)
}
