package tickets

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

// mergeCandidates bounds the contact's earlier tickets a merge looks through.
const mergeCandidates = 50

// mergeIntoOpenTicket merges the run's ticket, new and opened by its contact, into the
// contact's most recent earlier ticket still being worked on (contracts/api.md
// "Automations"), as a manual merge does. It reports whether it merged.
//
// The earlier ticket is of the same brand and channel, open, pending or on hold, not spam,
// deleted or merged, created or last replied on within the action's window,
// and, with same_subject, about the same subject. A merge never changes who sees a
// message: both tickets are of one team, or neither is of a private team. Mail that failed
// sender checks is never merged into a customer's conversation.
func (r *triggerRun) mergeIntoOpenTicket(ctx context.Context, actor Actor, act hd.Action) (bool, error) {
	var contact, team *uuid.UUID
	var brand uuid.UUID
	var channel, subject string
	var created time.Time
	var fromCustomer, unverified bool
	err := r.tx.QueryRow(ctx, `SELECT t.contact_id, t.team_id, t.brand_id, t.channel, t.subject, t.created_at,
		EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = t.workspace_id AND m.ticket_id = t.id AND m.kind = 'reply' AND m.direction = 'inbound'),
		EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = t.workspace_id AND m.ticket_id = t.id AND m.direction = 'inbound' AND m.auth_failed)
		FROM tickets t WHERE t.workspace_id = $1 AND t.id = $2 AND t.deleted_at IS NULL AND t.merged_into_id IS NULL AND NOT t.spam
		AND t.status NOT IN ('solved', 'closed')`, r.wid, r.tid).
		Scan(&contact, &team, &brand, &channel, &subject, &created, &fromCustomer, &unverified)
	if db.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Follow-up merging applies to customer email conversations.
	if channel != domain.ChannelEmail || contact == nil || !fromCustomer || unverified {
		return false, nil
	}
	private, err := hd.TeamPrivate(ctx, r.tx, r.wid, team)
	if err != nil {
		return false, err
	}
	rows, err := r.tx.Query(ctx, `SELECT t.id, t.subject FROM tickets t
		WHERE t.workspace_id = $1 AND t.contact_id = $2 AND (t.created_at, t.id) < ($3, $4) AND t.brand_id = $5 AND t.channel = $6
		AND t.status IN ('open', 'pending', 'on_hold') AND NOT t.spam AND t.deleted_at IS NULL AND t.merged_into_id IS NULL
		AND GREATEST(t.created_at, t.last_message_at) > now() - make_interval(days => $7::int)
		AND (t.team_id IS NOT DISTINCT FROM $8::uuid OR (NOT $9::bool AND NOT EXISTS (
			SELECT 1 FROM teams g WHERE g.workspace_id = $1 AND g.id = t.team_id AND g.private)))
		ORDER BY GREATEST(t.created_at, t.last_message_at) DESC, t.created_at DESC, t.id DESC
		LIMIT $10`, r.wid, *contact, created, r.tid, brand, channel, act.MergeWindowDays(), team, private, mergeCandidates)
	if err != nil {
		return false, err
	}
	type earlier struct {
		ID      uuid.UUID
		Subject string
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[earlier])
	if err != nil {
		return false, err
	}
	var into *uuid.UUID
	for _, c := range list {
		if !act.MergeSameSubject() || sameSubject(subject, c.Subject) {
			into = &c.ID
			break
		}
	}
	if into == nil {
		return false, nil
	}
	// Changed since it was picked (merged, solved, deleted): no merge this time.
	var status string
	err = r.tx.QueryRow(ctx, `SELECT status FROM tickets WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		AND merged_into_id IS NULL AND NOT spam AND status IN ('open', 'pending', 'on_hold') FOR UPDATE`, r.wid, *into).Scan(&status)
	if db.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := mergeTickets(ctx, r.tx, r.d, r.wid, r.tid, *into, actor); err != nil {
		return false, err
	}
	// The customer wrote again: as for a reply, the ticket is open again and whoever works
	// it hears of it.
	if status != domain.StatusOpen {
		open := domain.StatusOpen
		if _, err := applyPatch(ctx, r.tx, r.d, r.wid, *into, actor, Patch{Status: &open}, applyOpts{Lenient: true}); err != nil {
			return false, err
		}
	}
	if err := notifyCustomerReplied(ctx, r.tx, r.d, r.wid, *into, r.messageID, nil); err != nil {
		return false, err
	}
	return true, nil
}

// sameSubject reports whether two tickets are about the same subject: equal without reply
// prefixes ("Re:", "AW:", "TR:"), ticket tags ("[#123]"), extra spaces and case. A ticket
// without a subject is about nothing in particular, so it matches none.
func sameSubject(a, b string) bool {
	a, b = mergeSubject(a), mergeSubject(b)
	return a != "" && a == b
}

func mergeSubject(s string) string {
	s = strings.ToLower(email.BaseSubject(s))
	if s == strings.ToLower(email.NoSubject) {
		return ""
	}
	return s
}
