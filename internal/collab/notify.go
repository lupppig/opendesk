package collab

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

// Notification types (contracts/collab.md "Notifications"). They have no ticket; their data
// names the conversation, who acted ("by", named as "actor" by hd.Notify) and its subject.
const (
	// NotifyAssigned: {thread_id, account_id, by, subject}.
	NotifyAssigned = "collab_assigned"
	// NotifyMention: {thread_id, comment_id, by, subject, preview}.
	NotifyMention = "collab_mention"
)

// notify stores a Collab notification for a member, in tx: in the app, pushed and emailed
// by their preferences, like every notification.
func (s *service) notify(ctx context.Context, tx pgx.Tx, wid, userID uuid.UUID, typ string, data map[string]any) error {
	return hd.Notify(ctx, tx, wid, userID, typ, nil, data)
}
