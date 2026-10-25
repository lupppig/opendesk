package collab

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
)

func (s *service) registerDetailRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "get-collab-message-details", Method: http.MethodGet, Path: "/v1/collab/messages/{id}/details",
		Summary: "How a message came: its sender checks and headers"},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body MessageDetails }, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Message")
			if err != nil {
				return nil, err
			}
			m, err := scanMessage(s.d.DB.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.workspace_id = $1 AND m.id = $2`,
				p.WorkspaceID, id))
			if err != nil {
				return nil, notFound(err, "Message")
			}
			t, err := visibleThread(ctx, s.d.DB.Pool, p, m.ThreadID)
			if err != nil {
				return nil, notFound(err, "Message")
			}
			a, err := loadAccount(ctx, s.d.DB.Pool, p.WorkspaceID, t.AccountID)
			if err != nil {
				return nil, notFound(err, "Message")
			}
			raw, err := s.loadRaw(ctx, a, m, t.RemoteID)
			if err != nil {
				s.log.Warn("collab message source", "message_id", m.ID, "err", err)
				return nil, httpx.E(http.StatusBadGateway, "provider_error", "The message could not be read from the mail provider. Try again.")
			}
			return &struct{ Body MessageDetails }{messageDetails(raw, a.Provider, m.From.Email, m.outgoing())}, nil
		})
}
