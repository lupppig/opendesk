package tickets

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/templates"
)

// TicketVariables are a ticket's template values with the caller as the agent
// (contracts/templates.md).
type TicketVariables struct {
	Values        map[string]string `json:"values"`
	SignatureHTML string            `json:"signature_html" doc:"The signature the caller's email reply from mailbox_id would carry"`
}

// registerTemplates adds the composer's template routes: a ticket's values and filling a
// saved reply for it.
func registerTemplates(d *deps.Deps, api huma.API) {
	tags := []string{"Tickets"}

	huma.Register(api, huma.Operation{OperationID: "get-ticket-variables", Method: http.MethodGet, Path: "/v1/tickets/{id}/variables",
		Summary: "Get a ticket's template values", Tags: tags},
		func(ctx context.Context, in *struct {
			ID        string `path:"id"`
			MailboxID string `query:"mailbox_id" doc:"The address the reply goes out from; default: the ticket's"`
		}) (*struct{ Body TicketVariables }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := resolveVisible(ctx, d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			ref := templates.Ref{WorkspaceID: p.WorkspaceID, TicketID: &id, AuthorUserID: p.ActorUserID()}
			if strings.TrimSpace(in.MailboxID) != "" {
				mb, err := hd.ParseRef(in.MailboxID, "mailbox_id")
				if err != nil {
					return nil, err
				}
				if err := hd.CheckRef(ctx, d.DB.Pool, p.WorkspaceID, "mailbox", mb); err != nil {
					return nil, err
				}
				// A private team's address, and its signature, are for that team's members.
				ok, err := domain.CanSendFrom(ctx, d.DB.Pool, p, mb)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if !ok {
					return nil, httpx.NotFound("Mailbox")
				}
				ref.MailboxID = &mb
			}
			c, err := templates.Load(ctx, d.DB.Pool, d.Cfg, ref)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			values := map[string]string{}
			for k, v := range c.Values.Text {
				values[k] = v
			}
			for k, v := range c.Values.HTML {
				values[k] = v
			}
			for k, l := range c.Values.Lists {
				values[k] = strings.Join(l, ", ")
			}
			return &struct{ Body TicketVariables }{TicketVariables{Values: values, SignatureHTML: c.Signature()}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "render-ticket-template", Method: http.MethodPost, Path: "/v1/tickets/{id}/render",
		Summary: "Fill a template for a ticket", Tags: tags,
		Description: "Fills a saved reply's variables for this ticket with the caller as the agent, sanitized for a reply."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				BodyHTML string `json:"body_html" maxLength:"131072"`
				Thread   bool   `json:"thread,omitempty" doc:"Fill for a third-party thread: the ticket's brand, team and address and the caller, never the customer's or the ticket's details"`
			}
		}) (*struct {
			Body struct {
				BodyHTML string `json:"body_html"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := resolveVisible(ctx, d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			ref := templates.Ref{WorkspaceID: p.WorkspaceID, TicketID: &id, AuthorUserID: p.ActorUserID()}
			if in.Body.Thread {
				if ref, err = threadTemplateRef(ctx, d.DB.Pool, p.WorkspaceID, id, p.ActorUserID(), nil); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			html, err := hd.FillReply(ctx, d.DB.Pool, d.Cfg, ref, in.Body.BodyHTML)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					BodyHTML string `json:"body_html"`
				}
			}{}
			out.Body.BodyHTML = html
			return out, nil
		})
}
