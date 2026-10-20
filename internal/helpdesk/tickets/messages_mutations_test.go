package tickets_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

func createInternalNote(t *testing.T, e *hdtest.Env, ws hdtest.WS, ticketID string) domain.Message {
	t.Helper()
	var note domain.Message
	e.Do("POST", "/v1/tickets/"+ticketID+"/messages", ws.Agent.As(ws),
		map[string]any{"kind": "note", "body_html": "<p>Original note</p>"}).Want(t, 201).JSON(t, &note)
	return note
}

func TestEditInternalNote(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	note := createInternalNote(t, e, ws, tk.ID.String())
	path := "/v1/tickets/" + fmt.Sprint(tk.Number) + "/messages/" + note.ID.String()
	_, seq := e.Events(ws.ID, 0)

	var edited domain.Message
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]string{"body_html": "<p>Corrected note</p><script>alert(1)</script>"}).
		Want(t, 200).JSON(t, &edited)
	if edited.BodyText != "Corrected note" || edited.EditedAt == nil || strings.Contains(edited.BodyHTML, "script") {
		t.Fatalf("edited note %+v", edited)
	}
	var activityData string
	if err := e.D.DB.Pool.QueryRow(e.Ctx, `SELECT data::text FROM activities WHERE ticket_id = $1 AND type = 'note_edited'`, tk.ID).
		Scan(&activityData); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(activityData, note.ID.String()) || strings.Contains(activityData, "Corrected note") {
		t.Fatalf("activity data contains unexpected content: %s", activityData)
	}
	if types, _ := e.Events(ws.ID, seq); !slices.Contains(types, "activity.created") || !slices.Contains(types, "message.updated") {
		t.Fatalf("edit events %v", types)
	}
}

func TestDeleteInternalNoteRemovesAttachmentsAndPublishesEvents(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	note := createInternalNote(t, e, ws, tk.ID.String())
	key := "workspaces/" + ws.ID.String() + "/attachments/note-delete-test"
	if err := e.D.Storage.Put(e.Ctx, key, strings.NewReader("attachment"), int64(len("attachment")), "text/plain"); err != nil {
		t.Fatal(err)
	}
	e.Exec(`INSERT INTO attachments (workspace_id, message_id, filename, size_bytes, storage_key) VALUES ($1, $2, 'note.txt', 10, $3)`, ws.ID, note.ID, key)
	_, seq := e.Events(ws.ID, 0)

	path := "/v1/tickets/" + tk.ID.String() + "/messages/" + note.ID.String()
	e.Do("DELETE", path, ws.Agent.As(ws), nil).Want(t, 204)
	if n := countRows(t, e, `SELECT count(*) FROM messages WHERE id = $1`, note.ID); n != 0 {
		t.Fatalf("message rows left %d", n)
	}
	if n := countRows(t, e, `SELECT count(*) FROM attachments WHERE message_id = $1`, note.ID); n != 0 {
		t.Fatalf("attachment rows left %d", n)
	}
	if _, err := e.D.Storage.Get(e.Ctx, key); err == nil {
		t.Fatal("attachment object was not removed")
	}
	var activityData string
	if err := e.D.DB.Pool.QueryRow(e.Ctx, `SELECT data::text FROM activities WHERE ticket_id = $1 AND type = 'note_deleted'`, tk.ID).
		Scan(&activityData); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(activityData, note.ID.String()) || strings.Contains(activityData, "Original note") {
		t.Fatalf("activity data contains unexpected content: %s", activityData)
	}
	if types, _ := e.Events(ws.ID, seq); !slices.Contains(types, "activity.created") || !slices.Contains(types, "message.deleted") {
		t.Fatalf("delete events %v", types)
	}
}

func TestInternalNoteMutationPermissionsAndKinds(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	note := createInternalNote(t, e, ws, tk.ID.String())
	path := "/v1/tickets/" + tk.ID.String() + "/messages/" + note.ID.String()
	other := e.AddUser(ws, auth.RoleAgent, "Other Agent")
	e.Do("PATCH", path, other.As(ws), map[string]string{"body_html": "<p>Changed</p>"}).Want(t, 403)
	e.Do("DELETE", path, other.As(ws), nil).Want(t, 403)
	e.Do("DELETE", path, ws.Viewer.As(ws), nil).Want(t, 403)
	e.Do("DELETE", path, ws.Admin.As(ws), nil).Want(t, 204)

	reply := inbound(t, e, ws, tk.ID, "Customer reply")
	replyPath := "/v1/tickets/" + tk.ID.String() + "/messages/" + reply.ID.String()
	e.Do("PATCH", replyPath, ws.Agent.As(ws), map[string]string{"body_html": "<p>Changed</p>"}).Want(t, 422)
	e.Do("DELETE", replyPath, ws.Admin.As(ws), nil).Want(t, 422)
}

func TestDeleteNoteKeepsSharedAttachmentObject(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	note := createInternalNote(t, e, ws, tk.ID.String())
	otherTicket := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	otherNote := createInternalNote(t, e, ws, otherTicket.ID.String())
	key := "workspaces/" + ws.ID.String() + "/attachments/shared-note-test"
	if err := e.D.Storage.Put(e.Ctx, key, strings.NewReader("shared"), int64(len("shared")), "text/plain"); err != nil {
		t.Fatal(err)
	}
	e.Exec(`INSERT INTO attachments (workspace_id, message_id, filename, size_bytes, storage_key) VALUES ($1, $2, 'one.txt', 6, $3)`, ws.ID, note.ID, key)
	e.Exec(`INSERT INTO attachments (workspace_id, message_id, filename, size_bytes, storage_key) VALUES ($1, $2, 'two.txt', 6, $3)`, ws.ID, otherNote.ID, key)
	e.Do("DELETE", "/v1/tickets/"+tk.ID.String()+"/messages/"+note.ID.String(), ws.Agent.As(ws), nil).Want(t, 204)
	if _, err := e.D.Storage.Get(e.Ctx, key); err != nil {
		t.Fatalf("shared attachment object removed: %v", err)
	}
}

func TestInternalNoteMutationRefusals(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	note := createInternalNote(t, e, ws, tk.ID.String())
	path := "/v1/tickets/" + tk.ID.String() + "/messages/" + note.ID.String()

	// An admin deletes any note, but edits only their own.
	e.Do("PATCH", path, ws.Admin.As(ws), map[string]string{"body_html": "<p>Changed</p>"}).Want(t, 403)
	// The routes are for signed-in members, whatever the key's scopes.
	key := e.APIKey(ws, "tickets:write", "tickets:read")
	e.Do("PATCH", path, hdtest.Auth{Bearer: key}, map[string]string{"body_html": "<p>Changed</p>"}).Want(t, 403)
	e.Do("DELETE", path, hdtest.Auth{Bearer: key}, nil).Want(t, 403)
	// A note is reached through its own ticket only.
	other := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	e.Do("DELETE", "/v1/tickets/"+other.ID.String()+"/messages/"+note.ID.String(), ws.Agent.As(ws), nil).Want(t, 404)
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]string{"body_html": "<p> </p>"}).Want(t, 422)

	// A ticket of a private team the caller is not on is not there at all.
	hidden, _ := privateTicket(t, e, ws)
	var secret domain.Message
	e.Do("POST", "/v1/tickets/"+hidden.ID.String()+"/messages", ws.Admin.As(ws),
		map[string]any{"kind": "note", "body_html": "<p>Team only</p>"}).Want(t, 201).JSON(t, &secret)
	e.Do("DELETE", "/v1/tickets/"+hidden.ID.String()+"/messages/"+secret.ID.String(), ws.Agent.As(ws), nil).Want(t, 404)
}

func TestEditingANoteNotifiesOnlyNewMentions(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	note := createInternalNote(t, e, ws, tk.ID.String())
	path := "/v1/tickets/" + tk.ID.String() + "/messages/" + note.ID.String()
	mention := func(u hdtest.User, name string) string {
		return fmt.Sprintf(`<span data-type="mention" data-id="%s" data-mention="%s">@%s</span>`, u.ID, u.ID, name)
	}
	count := func(uid any) int {
		return countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'mentioned' AND user_id = $2`, tk.ID, uid)
	}

	e.Do("PATCH", path, ws.Agent.As(ws), map[string]string{"body_html": "<p>Ask " + mention(ws.Admin, "Adam") + "</p>"}).Want(t, 200)
	if count(ws.Admin.ID) != 1 {
		t.Fatalf("admin mentions %d", count(ws.Admin.ID))
	}
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]string{"body_html": "<p>Ask " + mention(ws.Admin, "Adam") + " and " + mention(ws.Owner, "Olivia") + " today</p>"}).
		Want(t, 200)
	if count(ws.Admin.ID) != 1 || count(ws.Owner.ID) != 1 {
		t.Fatalf("mentions admin %d owner %d", count(ws.Admin.ID), count(ws.Owner.ID))
	}
	var preview string
	e.Scalar(`SELECT data->>'preview' FROM notifications WHERE ticket_id = $1 AND user_id = $2`, &preview, tk.ID, ws.Admin.ID)
	if !strings.Contains(preview, "today") {
		t.Fatalf("admin's mention still reads %q", preview)
	}

	e.Do("DELETE", path, ws.Agent.As(ws), nil).Want(t, 204)
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'mentioned'`, tk.ID); n != 0 {
		t.Fatalf("mentions left after delete %d", n)
	}
}
