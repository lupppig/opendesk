package collab

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Who sees what (contracts/collab.md "Who sees what"). Every predicate here takes the
// viewer's user id as a SQL parameter and is used inside a query already scoped to the
// Principal's workspace.

// accountVisibleSQL is a predicate on collab_accounts alias a: the user (SQL parameter u)
// sees the account: it is theirs, or it is shared with everyone, one of their teams or them.
func accountVisibleSQL(a, u string) string {
	return fmt.Sprintf(`(%[1]s.owner_id = %[2]s OR (%[1]s.visibility = 'shared' AND EXISTS (
		SELECT 1 FROM collab_shares sh WHERE sh.account_id = %[1]s.id AND (sh.everyone OR sh.user_id = %[2]s
			OR sh.team_id IN (SELECT tm.team_id FROM team_members tm WHERE tm.user_id = %[2]s)))))`, a, u)
}

// threadSharedSQL is a predicate on collab_threads alias t: the conversation is shared with
// the user (SQL parameter u) on its own, or assigned to them.
func threadSharedSQL(t, u string) string {
	return fmt.Sprintf(`(EXISTS (SELECT 1 FROM collab_shares ts WHERE ts.thread_id = %[1]s.id AND (ts.everyone OR ts.user_id = %[2]s
			OR ts.team_id IN (SELECT tm.team_id FROM team_members tm WHERE tm.user_id = %[2]s)))
		OR EXISTS (SELECT 1 FROM collab_assignees ca WHERE ca.thread_id = %[1]s.id AND ca.user_id = %[2]s))`, t, u)
}

// threadVisibleSQL is a predicate on collab_threads alias t: the user (SQL parameter u) sees
// the conversation through its account or on its own.
func threadVisibleSQL(t, u string) string {
	return fmt.Sprintf(`(EXISTS (SELECT 1 FROM collab_accounts va WHERE va.id = %[1]s.account_id AND %[3]s) OR %[2]s)`,
		t, threadSharedSQL(t, u), accountVisibleSQL("va", u))
}

// visibleAccount loads an account the caller sees; anything else is not found.
func visibleAccount(ctx context.Context, q db.Querier, p *auth.Principal, id uuid.UUID) (*accountRow, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM collab_accounts a
		WHERE a.workspace_id = $1 AND a.id = $2 AND `+accountVisibleSQL("a", "$3"), p.WorkspaceID, id, p.UserID))
}

// canManageAccount reports whether the caller manages the account: its owner, or for a
// shared account a workspace admin.
func canManageAccount(p *auth.Principal, a *accountRow) bool {
	if a.OwnerID != nil && *a.OwnerID == p.UserID {
		return true
	}
	return a.Visibility == VisibilityShared && p.Role.AtLeast(auth.RoleAdmin)
}

// manageableAccount loads an account the caller manages. An admin finds shared accounts
// they are not in the audience of, so they can manage accounts of people who left.
func manageableAccount(ctx context.Context, q db.Querier, p *auth.Principal, id uuid.UUID) (*accountRow, error) {
	a, err := loadAccount(ctx, q, p.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	if !canManageAccount(p, a) {
		if _, err := visibleAccount(ctx, q, p, id); err != nil {
			return nil, err // not even visible: not found
		}
		return nil, auth.ErrForbidden
	}
	return a, nil
}

// visibleThread loads a conversation the caller sees.
func visibleThread(ctx context.Context, q db.Querier, p *auth.Principal, id uuid.UUID) (*threadRow, error) {
	return scanThread(q.QueryRow(ctx, `SELECT `+threadColumns+` FROM collab_threads t
		WHERE t.workspace_id = $1 AND t.id = $2 AND `+threadVisibleSQL("t", "$3"), p.WorkspaceID, id, p.UserID))
}

// visibleThreads loads the conversations the caller sees among ids, in no order.
func visibleThreads(ctx context.Context, q db.Querier, p *auth.Principal, ids []uuid.UUID) ([]*threadRow, error) {
	rows, err := q.Query(ctx, `SELECT `+threadColumns+` FROM collab_threads t
		WHERE t.workspace_id = $1 AND t.id = ANY($2) AND `+threadVisibleSQL("t", "$3"), p.WorkspaceID, ids, p.UserID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*threadRow, error) { return scanThread(r) })
}

// canAct reports whether the caller may act on what they see (agent or above).
func canAct(p *auth.Principal) bool { return p.Role.AtLeast(auth.RoleAgent) }

// accountAudience is who sees an account: its owner and, when shared, its audience,
// expanded to members of the workspace.
func accountAudience(ctx context.Context, q db.Querier, wid, accountID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		SELECT a.owner_id FROM collab_accounts a WHERE a.id = $2 AND a.workspace_id = $1 AND a.owner_id IS NOT NULL
		UNION
		SELECT m.user_id FROM collab_accounts a
			JOIN collab_shares sh ON sh.account_id = a.id
			JOIN members m ON m.workspace_id = a.workspace_id AND (sh.everyone OR m.user_id = sh.user_id
				OR m.user_id IN (SELECT tm.user_id FROM team_members tm WHERE tm.team_id = sh.team_id))
			WHERE a.id = $2 AND a.workspace_id = $1 AND a.visibility = 'shared'`, wid, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// threadsAudience is who sees the conversations on their own: their shares and assignees.
func threadsAudience(ctx context.Context, q db.Querier, wid uuid.UUID, threadIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(threadIDs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT m.user_id FROM collab_shares ts
			JOIN members m ON m.workspace_id = ts.workspace_id AND (ts.everyone OR m.user_id = ts.user_id
				OR m.user_id IN (SELECT tm.user_id FROM team_members tm WHERE tm.team_id = ts.team_id))
			WHERE ts.workspace_id = $1 AND ts.thread_id = ANY($2)
		UNION
		SELECT ca.user_id FROM collab_assignees ca JOIN collab_threads t ON t.id = ca.thread_id
			WHERE t.workspace_id = $1 AND ca.thread_id = ANY($2)`, wid, threadIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// loadAudience reads the audience an account or a conversation is shared with.
func loadAudience(ctx context.Context, q db.Querier, wid uuid.UUID, accountID, threadID *uuid.UUID) (Audience, error) {
	out := Audience{TeamIDs: []uuid.UUID{}, UserIDs: []uuid.UUID{}}
	rows, err := q.Query(ctx, `SELECT everyone, team_id, user_id FROM collab_shares
		WHERE workspace_id = $1 AND (account_id = $2 OR thread_id = $3) ORDER BY created_at`, wid, accountID, threadID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var everyone bool
		var team, user *uuid.UUID
		if err := rows.Scan(&everyone, &team, &user); err != nil {
			return out, err
		}
		switch {
		case everyone:
			out.Everyone = true
		case team != nil:
			out.TeamIDs = append(out.TeamIDs, *team)
		case user != nil:
			out.UserIDs = append(out.UserIDs, *user)
		}
	}
	return out, rows.Err()
}

// checkAudience refuses teams and people that are not the workspace's, and drops repeats.
func checkAudience(ctx context.Context, q db.Querier, wid uuid.UUID, a Audience) (Audience, error) {
	out := Audience{Everyone: a.Everyone, TeamIDs: []uuid.UUID{}, UserIDs: []uuid.UUID{}}
	for _, id := range a.TeamIDs {
		if !slices.Contains(out.TeamIDs, id) {
			out.TeamIDs = append(out.TeamIDs, id)
		}
	}
	for _, id := range a.UserIDs {
		if !slices.Contains(out.UserIDs, id) {
			out.UserIDs = append(out.UserIDs, id)
		}
	}
	if len(out.TeamIDs) > 50 || len(out.UserIDs) > 200 {
		return out, errAudienceTooLarge
	}
	var n int
	if len(out.TeamIDs) > 0 {
		if err := q.QueryRow(ctx, `SELECT count(*) FROM teams WHERE workspace_id = $1 AND id = ANY($2)`, wid, out.TeamIDs).Scan(&n); err != nil {
			return out, err
		}
		if n != len(out.TeamIDs) {
			return out, errUnknownTeam
		}
	}
	if len(out.UserIDs) > 0 {
		if err := q.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND user_id = ANY($2)`, wid, out.UserIDs).Scan(&n); err != nil {
			return out, err
		}
		if n != len(out.UserIDs) {
			return out, errUnknownMember
		}
	}
	return out, nil
}

// saveAudience replaces the shares of an account or a conversation, in tx.
func saveAudience(ctx context.Context, tx pgx.Tx, wid uuid.UUID, accountID, threadID *uuid.UUID, a Audience, by uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM collab_shares WHERE workspace_id = $1 AND (account_id = $2 OR thread_id = $3)`,
		wid, accountID, threadID); err != nil {
		return err
	}
	insert := func(everyone bool, team, user *uuid.UUID) error {
		_, err := tx.Exec(ctx, `INSERT INTO collab_shares (id, workspace_id, account_id, thread_id, everyone, team_id, user_id, shared_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, ids.New(), wid, accountID, threadID, everyone, team, user, by)
		return err
	}
	if a.Everyone {
		if err := insert(true, nil, nil); err != nil {
			return err
		}
	}
	for _, id := range a.TeamIDs {
		if err := insert(false, &id, nil); err != nil {
			return err
		}
	}
	for _, id := range a.UserIDs {
		if err := insert(false, nil, &id); err != nil {
			return err
		}
	}
	return nil
}
