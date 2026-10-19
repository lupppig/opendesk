package collab

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
)

func (s *service) registerStatusRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "get-collab", Method: http.MethodGet, Path: "/v1/collab",
		Summary: "Whether Collab is on, and what can be connected"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CollabStatus }, error) {
			// The only Collab route without the Collab check: it says whether it is on.
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, "")
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if p.Kind != auth.KindUser || p.Assistant || p.GrantID != uuid.Nil {
				return nil, httpx.Wrap(auth.ErrForbidden)
			}
			ent, err := s.d.Plan.Entitlements(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := CollabStatus{Enabled: ent.Collab, Providers: s.providersOffered()}
			if !ent.Collab {
				reason := "plan"
				if !s.d.Cfg.Collab || (s.d.Plan != nil && s.d.Plan.CollabOff) {
					reason = "disabled"
				}
				out.Reason = &reason
			}
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM collab_accounts a WHERE a.workspace_id = $1 AND `+
				accountVisibleSQL("a", "$2"), p.WorkspaceID, p.UserID).Scan(&out.Accounts); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CollabStatus }{out}, nil
		})
}
