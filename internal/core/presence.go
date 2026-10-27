package core

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
)

// Online time (contracts/api.md "Agents report"). The app's heartbeats, while a member's
// availability is online, are kept in agent_presence as stretches of time: a heartbeat
// extends the member's open stretch when the last one came less than PresenceGap ago, and
// otherwise closes it and starts a new one. A change of availability away from online ends
// the stretch. Viewers are never assigned work, so their time is not recorded.

// PresenceGap is the longest silence between two heartbeats of one stretch. The web app
// beats every 2 minutes while a tab is visible and the iPhone app every 5 while it is open.
const PresenceGap = 10 * time.Minute

// recordPresence notes that the member is in the app now. The member row is share-locked,
// so a change of availability waits for it and the stretch never outlives the status.
func recordPresence(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID) error {
	var availability, role string
	err := tx.QueryRow(ctx, `SELECT availability, role FROM members WHERE workspace_id = $1 AND user_id = $2 FOR SHARE`,
		wid, uid).Scan(&availability, &role)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if availability != "online" || role == "viewer" {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE agent_presence SET ended_at = now()
		WHERE workspace_id = $1 AND user_id = $2 AND open AND ended_at >= now() - make_interval(secs => $3)`, wid, uid, PresenceGap.Seconds())
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	// The open stretch, if any, ended at its last heartbeat.
	if _, err := tx.Exec(ctx, `UPDATE agent_presence SET open = false WHERE workspace_id = $1 AND user_id = $2 AND open`, wid, uid); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_presence (workspace_id, user_id) VALUES ($1, $2)
		ON CONFLICT (workspace_id, user_id) WHERE open DO NOTHING`, wid, uid)
	return err
}

// closePresence ends the member's open stretch when their availability leaves online. A
// member who changed it themselves was in the app until now; a change made by someone
// else ends the stretch at the member's last heartbeat.
func closePresence(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID, self bool) error {
	_, err := tx.Exec(ctx, `UPDATE agent_presence
		SET open = false, ended_at = CASE WHEN $3 AND ended_at >= now() - make_interval(secs => $4) THEN now() ELSE ended_at END
		WHERE workspace_id = $1 AND user_id = $2 AND open`, wid, uid, self, PresenceGap.Seconds())
	return err
}
