package tickets_test

import (
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
)

// An agent typing on a chat reaches that chat's visitor through the API, which checks the
// ticket: never another contact, a ticket the agent cannot see, or a ticket of another channel.
func TestAgentTyping(t *testing.T) {
	e, ws := newEnv(t)
	next := e.Live(ws.ID)
	tk := createTicket(t, e, ws, map[string]any{"kind": "note", "contact": map[string]any{"email": "visitor@example.com"}})
	e.Exec(`UPDATE tickets SET channel = 'chat' WHERE id = $1`, tk.ID)
	path := "/v1/tickets/" + tk.ID.String() + "/typing"

	e.Do("POST", path, ws.Agent.As(ws), nil).Want(t, 204)
	ev := next()
	if ev == nil || ev.Type != "agent.typing" || ev.TicketID != tk.ID || ev.ContactID != tk.Contact.ID || ev.WorkspaceID != ws.ID ||
		ev.BrandID == nil || *ev.BrandID != ws.BrandID || ev.TeamID != nil {
		t.Fatalf("agent typing event %+v", ev)
	}
	user, _ := ev.Data.(map[string]any)["user"].(map[string]any)
	if user["id"] != ws.Agent.ID.String() || user["name"] != "Anna Agent" {
		t.Fatalf("typing user %v", ev.Data)
	}

	// Viewers, API keys and unknown tickets send nothing.
	e.Do("POST", path, ws.Viewer.As(ws), nil).Want(t, 403)
	e.Do("POST", path, hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeTicketsWrite)}, nil).Want(t, 403)
	e.Do("POST", "/v1/tickets/"+ids.New().String()+"/typing", ws.Agent.As(ws), nil).Want(t, 404)
	e.Do("POST", "/v1/tickets/nope/typing", ws.Agent.As(ws), nil).Want(t, 404)
	// A ticket of another workspace is not there.
	other := e.NewWorkspace()
	e.Do("POST", path, other.Agent.As(other), nil).Want(t, 404)

	// A private team's chat is 404 to outsiders; for its members the event names the team.
	hidden, team := privateTicket(t, e, ws)
	e.Exec(`UPDATE tickets SET channel = 'chat', contact_id = $2 WHERE id = $1`, hidden.ID, tk.Contact.ID)
	e.Do("POST", "/v1/tickets/"+hidden.ID.String()+"/typing", ws.Agent.As(ws), nil).Want(t, 404)
	e.Do("POST", "/v1/tickets/"+hidden.ID.String()+"/typing", ws.Admin.As(ws), nil).Want(t, 204)
	if ev := next(); ev == nil || ev.TicketID != hidden.ID || ev.TeamID == nil || *ev.TeamID != team {
		t.Fatalf("private team typing event %+v", ev)
	}

	// An email ticket has no visitor to tell.
	email := createTicket(t, e, ws, map[string]any{"kind": "note", "contact": map[string]any{"email": "mail@example.com"}})
	e.Do("POST", "/v1/tickets/"+email.ID.String()+"/typing", ws.Agent.As(ws), nil).Want(t, 204)
	if ev := next(); ev != nil {
		t.Fatalf("typing on a ticket of another channel published %+v", ev)
	}
}
