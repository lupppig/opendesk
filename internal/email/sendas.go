package email

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Gmail's send-as list (contracts/teams.md "Aliases"): a Google connection that may read
// it (a sync grant, https://mail.google.com/, or an api grant, gmail.modify) records the
// account's send-as addresses at connect, on "Check again" and about daily while the
// mailbox syncs. Every alias then sends as itself exactly when Gmail lists it as verified,
// and the mailbox page offers the other addresses as aliases to add. A send grant
// (gmail.send) cannot read the list: an admin says which aliases Gmail sends as.

const (
	// sendAsRefresh is how old the list may get before a sync reads it again.
	sendAsRefresh = 24 * time.Hour
	// sendAsChecksPerMinute bounds "Check again" per mailbox.
	sendAsChecksPerMinute = 6
)

// readSendAs reads the connected account's send-as addresses from Gmail.
func (s *service) readSendAs(ctx context.Context, m *mailboxRow) ([]SendAsAddress, error) {
	var list struct {
		SendAs []struct {
			Email              string `json:"sendAsEmail"`
			DisplayName        string `json:"displayName"`
			IsPrimary          bool   `json:"isPrimary"`
			VerificationStatus string `json:"verificationStatus"`
		} `json:"sendAs"`
	}
	if err := s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailURL("/settings/sendAs"), 1<<20, nil, &list); err != nil {
		return nil, err
	}
	out := []SendAsAddress{}
	for _, a := range list.SendAs {
		addr := strings.ToLower(strings.TrimSpace(a.Email))
		if !validAddress(addr) || len(out) >= 100 {
			continue
		}
		// The primary address needs no verification; another is verified once its owner
		// confirmed it ("accepted").
		out = append(out, SendAsAddress{Email: addr, DisplayName: displayname.CleanOr(a.DisplayName, displayname.Workspace, ""),
			Verified: a.IsPrimary || strings.EqualFold(a.VerificationStatus, "accepted")})
	}
	return out, nil
}

// checkSendAs reads Gmail's send-as list for the mailbox and stores it, setting each
// alias's send_as from it. It does nothing for a connection that cannot read the list. An
// admin's check (p) is audit-logged.
func (s *service) checkSendAs(ctx context.Context, p *auth.Principal, wid, id uuid.UUID) error {
	m, err := loadMailbox(ctx, s.d.DB.Pool, wid, id)
	if err != nil {
		return err
	}
	if !m.readsSendAs() {
		return nil
	}
	list, err := s.readSendAs(ctx, m)
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		m, err := scanMailbox(tx.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wid, id))
		if err != nil {
			return err
		}
		if !m.readsSendAs() {
			return nil
		}
		before := m.AliasSettings
		m.AliasSettings = make(map[string]AliasSettings, len(before))
		for k, v := range before {
			m.AliasSettings[k] = v
		}
		m.sendAs = &storedSendAs{Account: strings.ToLower(m.Config.OAuthEmail), CheckedAt: timeNow().UTC(), Addresses: list}
		applySendAsList(m)
		if _, err := tx.Exec(ctx, `UPDATE mailboxes SET provider_send_as = $3, alias_settings = $4 WHERE workspace_id = $1 AND id = $2`,
			wid, id, m.sendAs, m.AliasSettings); err != nil {
			return err
		}
		if p != nil {
			if err := audit.Log(ctx, tx, p, wid, "mailbox.updated", "mailbox", &id, map[string]any{
				"changes": []string{"alias_settings"}, "alias_settings": aliasAudit(m), "send_as_checked": true}); err != nil {
				return err
			}
		}
		return s.publishMailbox(ctx, tx, wid, id, "mailbox.updated")
	})
}

// refreshSendAs reads Gmail's list again after a mailbox synced without trouble, once it is
// a day old. Best effort: a failure is tried again after the same wait, and never fails the
// sync.
func (s *service) refreshSendAs(ctx context.Context, m *mailboxRow) {
	if !m.readsSendAs() {
		return
	}
	if l := m.sendAsList(); l != nil && timeNow().Sub(l.CheckedAt) < sendAsRefresh {
		return
	}
	// Claim the check first, so a failing one is not repeated every minute.
	tag, err := s.d.DB.Pool.Exec(ctx, `UPDATE mailboxes SET provider_send_as = coalesce(provider_send_as, '{}'::jsonb) ||
		jsonb_build_object('tried_at', now())
		WHERE workspace_id = $1 AND id = $2 AND status = 'active' AND last_error = ''
		AND coalesce((provider_send_as ->> 'tried_at')::timestamptz, '-infinity') < now() - $3::interval`,
		m.WorkspaceID, m.ID, "24 hours")
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	if err := s.checkSendAs(ctx, nil, m.WorkspaceID, m.ID); err != nil {
		s.log.Info("gmail send-as list not read", "workspace_id", m.WorkspaceID, "mailbox_id", m.ID, "reason", safeError(err))
	}
}

func (s *service) registerSendAsRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "check-mailbox-send-as", Method: http.MethodPost, Path: "/v1/mailboxes/{id}/send-as/check",
		Summary: "Read Gmail's send-as addresses again", Tags: []string{"Mailboxes"},
		Description: "For a Google mailbox whose connection may read Gmail's send-as list: reads it now, sets each alias's send_as from it and answers the mailbox. A send-only connection cannot read it (422 send_as_unreadable)."},
		func(ctx context.Context, in *idInput) (*mailboxBody, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			m, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapMailboxErr(err)
			}
			if !m.readsSendAs() {
				return nil, httpx.E(http.StatusUnprocessableEntity, "send_as_unreadable",
					"Google does not share the send-as list with this connection. Add the addresses as aliases by hand.")
			}
			if !s.d.Limiter.Allow("mailbox-send-as:"+id.String(), sendAsChecksPerMinute, time.Minute) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Wait a minute before checking again.")
			}
			if err := s.checkSendAs(ctx, p, p.WorkspaceID, id); err != nil {
				return nil, sendAsFailure(err)
			}
			out, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &mailboxBody{s.api(out, true, a)}, nil
		})
}

// sendAsFailure is the answer to a list Gmail did not give: its own refusal, or a fixed message.
func sendAsFailure(err error) error {
	var msg string
	switch e := err.(type) {
	case *sendError:
		msg = e.msg
	case *apiError:
		msg = e.msg
	}
	if msg == "" {
		return httpx.Wrap(err)
	}
	return httpx.E(http.StatusBadGateway, "provider_error", msg)
}
