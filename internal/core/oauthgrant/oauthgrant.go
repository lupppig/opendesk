// Package oauthgrant holds what core and the MCP server share about OAuth grants
// (contracts/agent.md "opendesk MCP server", OAuth): the scopes an AI client may be
// granted, how a member's role bounds them, and revocation with its audit entries.
package oauthgrant

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
)

// Scopes an AI client may ask for: the API key scopes the MCP tools use. Settings
// writes and realtime are never granted over OAuth.
var Scopes = []string{auth.ScopeTicketsRead, auth.ScopeTicketsWrite, auth.ScopeContactsRead, auth.ScopeContactsWrite,
	auth.ScopeKBRead, auth.ScopeKBWrite, auth.ScopeWikiRead, auth.ScopeWikiWrite, auth.ScopeSettingsRead}

// ForRole is the part of Scopes a member with role can use: viewers read, agents and
// up also write tickets, contacts, articles and wiki pages.
func ForRole(r auth.Role) []string {
	switch {
	case r.AtLeast(auth.RoleAgent):
		return slices.Clone(Scopes)
	case r.AtLeast(auth.RoleViewer):
		return []string{auth.ScopeTicketsRead, auth.ScopeContactsRead, auth.ScopeKBRead, auth.ScopeWikiRead, auth.ScopeSettingsRead}
	}
	return nil
}

// Effective is granted bounded by role, in Scopes order.
func Effective(granted []string, r auth.Role) []string {
	allowed := ForRole(r)
	out := []string{}
	for _, s := range Scopes {
		if slices.Contains(granted, s) && slices.Contains(allowed, s) {
			out = append(out, s)
		}
	}
	return out
}

// Parse keeps the known scopes of a space-separated scope string, in Scopes order.
// Unknown scopes are ignored, as RFC 6749 allows.
func Parse(fields []string) []string {
	out := []string{}
	for _, s := range Scopes {
		if slices.Contains(fields, s) {
			out = append(out, s)
		}
	}
	return out
}

// Reasons recorded with oauth.grant_revoked.
const (
	ReasonUser          = "revoked"
	ReasonClient        = "client_revoked"
	ReasonReplaced      = "replaced"
	ReasonRefreshReused = "refresh_reused"
	ReasonCodeReused    = "code_reused"
	ReasonMemberRemoved = "member_removed"
	ReasonSignedOut     = "signed_out_everywhere"
)

// Revoke ends the active grants that match and audit-logs each one. wid and gid narrow
// the match when set; uid always applies unless zero (then gid must be set). It returns
// the revoked grant ids.
func Revoke(ctx context.Context, tx pgx.Tx, p *auth.Principal, uid, wid, gid uuid.UUID, reason string) ([]uuid.UUID, error) {
	if uid == uuid.Nil && gid == uuid.Nil {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `UPDATE oauth_grants g SET revoked_at = now()
		FROM oauth_clients c
		WHERE c.id = g.client_id AND g.revoked_at IS NULL
			AND ($1::uuid IS NULL OR g.user_id = $1)
			AND ($2::uuid IS NULL OR g.workspace_id = $2)
			AND ($3::uuid IS NULL OR g.id = $3)
		RETURNING g.id, g.workspace_id, g.user_id, c.name`, nullable(uid), nullable(wid), nullable(gid))
	if err != nil {
		return nil, err
	}
	type row struct {
		id, wid, uid uuid.UUID
		client       string
	}
	var revoked []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.wid, &r.uid, &r.client); err != nil {
			rows.Close()
			return nil, err
		}
		revoked = append(revoked, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(revoked))
	for _, r := range revoked {
		actor := p
		if actor == nil {
			actor = &auth.Principal{Kind: auth.KindUser, UserID: r.uid}
		}
		if err := audit.Log(ctx, tx, actor, r.wid, "oauth.grant_revoked", "oauth_grant", &r.id,
			map[string]any{"client": r.client, "user_id": r.uid, "reason": reason}); err != nil {
			return nil, err
		}
		ids = append(ids, r.id)
	}
	return ids, nil
}

func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
