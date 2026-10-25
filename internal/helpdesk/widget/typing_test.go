package widget_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
)

// A visitor typing reaches the agents of their own conversation only, through the API,
// which checks the conversation is theirs; a private team's chat names its team.
func TestVisitorTyping(t *testing.T) {
	e, ws := setup(t)
	next := e.Live(ws.ID)
	s := start(t, e, map[string]any{"key": ws.WidgetKey, "email": "typing@example.com"})
	va := hdtest.Auth{Visitor: s.VisitorToken}
	var created struct {
		Conversation struct {
			ID uuid.UUID `json:"id"`
		} `json:"conversation"`
	}
	e.Do("POST", "/v1/widget/conversations", va, map[string]any{"body": "Hello"}).Want(t, 201).JSON(t, &created)
	path := "/v1/widget/conversations/" + created.Conversation.ID.String() + "/typing"
	// Starting the conversation may publish previews of its own (the answer bot); drain them.
	for next() != nil {
	}

	e.Do("POST", path, va, nil).Want(t, 204)
	ev := next()
	if ev == nil || ev.Type != "customer.typing" || ev.TicketID != created.Conversation.ID || ev.ContactID != s.Contact.ID ||
		ev.WorkspaceID != ws.ID || ev.TeamID != nil || ev.BrandID == nil || *ev.BrandID != ws.BrandID {
		t.Fatalf("visitor typing event %+v", ev)
	}

	// Another visitor's conversation and unknown ids are not theirs.
	other := start(t, e, map[string]any{"key": ws.WidgetKey, "email": "other@example.com"})
	e.Do("POST", path, hdtest.Auth{Visitor: other.VisitorToken}, nil).Want(t, 404)
	e.Do("POST", "/v1/widget/conversations/"+ids.New().String()+"/typing", va, nil).Want(t, 404)
	e.Do("POST", "/v1/widget/conversations/nope/typing", va, nil).Want(t, 404)
	e.Do("POST", path, hdtest.Auth{}, nil).Want(t, 401)
	if ev := next(); ev != nil {
		t.Fatalf("refused typing published %+v", ev)
	}

	// A private team's chat carries the team, so only its members see the visitor typing.
	team := addTeam(e, ws, "Security", ws.Agent.ID)
	e.Exec(`UPDATE teams SET private = true WHERE id = $1`, team)
	e.Exec(`UPDATE tickets SET team_id = $2 WHERE id = $1`, created.Conversation.ID, team)
	e.Do("POST", path, va, nil).Want(t, 204)
	if ev := next(); ev == nil || ev.TeamID == nil || *ev.TeamID != team {
		t.Fatalf("private team typing event %+v", ev)
	}
}
