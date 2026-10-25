package tickets

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// registerTyping adds the route that shows a chat's visitor an agent is writing a reply.
// The visitor's widget gets it as "typing" on its contact topic (contracts/realtime.md
// "Live events"): only for a chat ticket, only to that ticket's contact, and only to
// widgets of the ticket's brand.
func registerTyping(d *deps.Deps, api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "ticket-typing", Method: http.MethodPost, Path: "/v1/tickets/{id}/typing",
		Summary: "Show the customer you are writing", Tags: []string{"Tickets"}, DefaultStatus: http.StatusNoContent,
		Description: "Call while a teammate writes a reply on a chat ticket, at most every few seconds. The ticket's visitor sees " +
			"them typing in the chat widget. Other channels accept it and do nothing. Teammates on the ticket see typing through " +
			"the realtime gateway's own \"typing\" push."},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// Typing is a person's: an API key has no one to show.
			if p.Kind != auth.KindUser {
				return nil, httpx.Forbidden()
			}
			ev, name, err := typingTarget(ctx, d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if ev == nil || d.NATS == nil {
				return nil, nil
			}
			ev.Type = "agent.typing"
			ev.Data = map[string]any{"user": map[string]any{"id": p.UserID, "name": name}}
			events.PublishLive(d.NATS.Conn, *ev)
			return nil, nil
		})
}

// typingTarget loads the ticket the caller may see (a hidden one is 404) and, for a chat
// with a contact, the live event addressed to that contact with the ticket's private team
// and brand, plus the caller's name. A ticket of another channel returns no event.
func typingTarget(ctx context.Context, q db.Querier, p *auth.Principal, ref string) (*events.LiveEvent, string, error) {
	id, err := uuid.Parse(ref)
	if err != nil {
		return nil, "", httpx.NotFound("Ticket")
	}
	var (
		channel, name     string
		contact, team     *uuid.UUID
		assignee, private *uuid.UUID
		brand             uuid.UUID
	)
	err = q.QueryRow(ctx, `SELECT t.channel, t.contact_id, t.team_id, t.assignee_id, tm.id, t.brand_id,
			COALESCE((SELECT u.name FROM users u WHERE u.id = $3), '')
		FROM tickets t LEFT JOIN teams tm ON tm.id = t.team_id AND tm.private
		WHERE t.workspace_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`, p.WorkspaceID, id, p.UserID).
		Scan(&channel, &contact, &team, &assignee, &private, &brand, &name)
	if db.IsNotFound(err) {
		return nil, "", httpx.NotFound("Ticket")
	}
	if err != nil {
		return nil, "", err
	}
	ok, err := domain.CanSee(ctx, q, p, team, assignee)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", httpx.NotFound("Ticket")
	}
	if channel != "chat" || contact == nil {
		return nil, name, nil
	}
	return &events.LiveEvent{WorkspaceID: p.WorkspaceID, TicketID: id, ContactID: *contact, TeamID: private, BrandID: &brand}, name, nil
}
