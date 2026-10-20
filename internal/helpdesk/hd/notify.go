package hd

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
)

// pushJobs enqueues push deliveries in the notification's transaction. It is set at boot
// on an instance that can push (internal/app); without it notifications stay in-app.
var pushJobs atomic.Pointer[river.Client[pgx.Tx]]

// ConfigurePush makes Notify enqueue a push for every notification of a user with a
// registered phone (contracts/native.md "Push"). nil turns it off.
func ConfigurePush(jc *river.Client[pgx.Tx]) { pushJobs.Store(jc) }

// Notification types.
const (
	NotifyAssigned        = "assigned"
	NotifyMentioned       = "mentioned"
	NotifyCustomerReplied = "customer_replied"
	// NotifyThreadReplied: someone outside the team answered a third-party thread.
	NotifyThreadReplied = "thread_replied"
	NotifySLABreached   = "sla_breached"
	// NotifyTeamJoined tells a team's leads that an admin joined it (contracts/teams.md).
	NotifyTeamJoined = "team_joined"
)

type NotificationTicket struct {
	ID      uuid.UUID `json:"id"`
	Number  int64     `json:"number"`
	Subject string    `json:"subject"`
}

// Notification is the contract shape (contracts/api.md).
type Notification struct {
	ID        uuid.UUID           `json:"id"`
	Type      string              `json:"type"`
	Ticket    *NotificationTicket `json:"ticket"`
	Data      map[string]any      `json:"data"`
	ReadAt    *time.Time          `json:"read_at"`
	CreatedAt time.Time           `json:"created_at"`
}

// EventTicketID scopes notification.created to the ticket's private team (events.TicketRef).
func (n Notification) EventTicketID() *uuid.UUID {
	if n.Ticket == nil {
		return nil
	}
	id := n.Ticket.ID
	return &id
}

// Notify stores a notification for a workspace member and publishes notification.created
// to user:<uid>. A user who is no longer a member, or who may not see the ticket (it is in
// a private team they are not on and not assigned to), is skipped silently.
func Notify(ctx context.Context, tx pgx.Tx, wid, userID uuid.UUID, typ string, ticketID *uuid.UUID, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	var member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE workspace_id = $1 AND user_id = $2)`, wid, userID).Scan(&member); err != nil {
		return fmt.Errorf("notify member check: %w", err)
	}
	if !member {
		return nil
	}
	// Name who acted ("by", a user id) so the notification reads without another lookup.
	if by, ok := data["by"].(string); ok && data["actor"] == nil {
		if uid, err := uuid.Parse(by); err == nil {
			var name string
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, uid).Scan(&name); err == nil {
				data["actor"] = map[string]any{"id": uid.String(), "name": name}
			}
		}
	}
	n := Notification{ID: ids.New(), Type: typ, Data: data, CreatedAt: time.Now().UTC()}
	if ticketID != nil {
		var t NotificationTicket
		var hidden bool
		err := tx.QueryRow(ctx, `SELECT t.id, t.number, t.subject,
			COALESCE(tm.private AND t.assignee_id IS DISTINCT FROM $3
				AND NOT EXISTS (SELECT 1 FROM team_members x WHERE x.team_id = tm.id AND x.user_id = $3), false)
			FROM tickets t LEFT JOIN teams tm ON tm.id = t.team_id
			WHERE t.workspace_id = $1 AND t.id = $2`, wid, *ticketID, userID).
			Scan(&t.ID, &t.Number, &t.Subject, &hidden)
		if err != nil {
			return fmt.Errorf("notify ticket: %w", err)
		}
		if hidden {
			return nil
		}
		n.Ticket = &t
	}
	// The email waits (contracts/api.md "Notification preferences and emails"): reading the
	// notification in the app first means it is never emailed.
	due, err := notifyprefs.EmailDueFor(ctx, tx, wid, userID, typ, n.CreatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notifications (id, workspace_id, user_id, type, ticket_id, data, created_at, email_due_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, n.ID, wid, userID, typ, ticketID, data, n.CreatedAt, due); err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}
	// An urgent one may cut a cooldown short: the sweep decides again for this member at
	// its due time instead of waiting for the hold to end.
	if due != nil && notifyprefs.Urgent(typ) {
		if _, err := tx.Exec(ctx, `UPDATE notification_email_state SET next_check_at = LEAST(next_check_at, $3)
			WHERE workspace_id = $1 AND user_id = $2 AND next_check_at > $3`, wid, userID, *due); err != nil {
			return err
		}
	}
	if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "notification.created",
		Topics: []string{events.UserTopic(userID)}, Data: map[string]any{"notification": n}}); err != nil {
		return err
	}
	return enqueuePush(ctx, tx, wid, userID, n.ID)
}

// enqueuePush queues the push for a stored notification when the user has a phone.
func enqueuePush(ctx context.Context, tx pgx.Tx, wid, userID, notificationID uuid.UUID) error {
	jc := pushJobs.Load()
	if jc == nil {
		return nil
	}
	var phone bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_devices WHERE user_id = $1)`, userID).Scan(&phone); err != nil {
		return fmt.Errorf("notify push devices: %w", err)
	}
	if !phone {
		return nil
	}
	_, err := jc.InsertTx(ctx, tx, jobs.PushNotificationArgs{WorkspaceID: wid, UserID: userID, NotificationID: notificationID},
		&river.InsertOpts{MaxAttempts: 3})
	return err
}
