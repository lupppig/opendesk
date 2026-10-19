// Package events is the transactional outbox. See contracts/events.md.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ids"
)

const (
	VisibilityAgents = "agents"
	VisibilityPublic = "public"
)

// SubjectPrefix + workspace id is the NATS subject for a workspace's events.
const SubjectPrefix = "od.events."

// DisconnectSubject tells the realtime gateway to close a user's sockets.
const DisconnectSubject = "od.control.disconnect"

// Event is what a caller publishes. Topics beyond workspace:<wid> are optional.
type Event struct {
	WorkspaceID uuid.UUID
	Type        string
	Visibility  string // defaults to agents
	Topics      []string
	Data        any
	// TicketID marks an event about a ticket. When the ticket is in a private team the
	// event goes to that team's topic instead of the workspace's (contracts/teams.md).
	TicketID *uuid.UUID
	// TeamID scopes the event to a private team directly, without a ticket lookup.
	TeamID *uuid.UUID
	// Workspace delivers the event to the whole workspace even though its data names a
	// ticket. Only ticket.hidden uses it: it carries ids alone and tells everyone outside
	// the team to drop the ticket.
	Workspace bool
	// Users scopes the event to these people (contracts/events.md "Scoped to people"): it
	// goes to their user topics only, the workspace gets the stand-in, replay hides it from
	// everyone else and no webhook receives it. A non-nil empty list reaches nobody.
	Users []uuid.UUID
}

// TicketRef is a payload that names the ticket it is about. Publish scopes an event whose
// data is (or holds, in a map[string]any) a TicketRef like an event with TicketID.
type TicketRef interface{ EventTicketID() *uuid.UUID }

// ticketKeys are the map keys looked at first, in order, for the ticket an event is about.
var ticketKeys = []string{"ticket_id", "ticket", "notification", "message", "activity"}

// TicketOf returns the ticket an event's data names: a TicketRef, or a map[string]any with
// "ticket_id" (uuid.UUID, *uuid.UUID or string) or a TicketRef value. nil when none.
func TicketOf(data any) *uuid.UUID {
	if id := refTicket(data); id != nil {
		return id
	}
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range ticketKeys {
		if id := valueTicket(m[k]); id != nil {
			return id
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if id := refTicket(m[k]); id != nil {
			return id
		}
	}
	return nil
}

func valueTicket(v any) *uuid.UUID {
	switch x := v.(type) {
	case uuid.UUID:
		if x != uuid.Nil {
			return &x
		}
	case *uuid.UUID:
		if x != nil && *x != uuid.Nil {
			id := *x
			return &id
		}
	case string:
		if id, err := uuid.Parse(x); err == nil && id != uuid.Nil {
			return &id
		}
	}
	return refTicket(v)
}

func refTicket(v any) *uuid.UUID {
	r, ok := v.(TicketRef)
	if !ok {
		return nil
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil
	}
	id := r.EventTicketID()
	if id == nil || *id == uuid.Nil {
		return nil
	}
	return id
}

// Envelope is the stored and transmitted form.
type Envelope struct {
	ID          uuid.UUID       `json:"id"`
	Seq         int64           `json:"seq"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	Type        string          `json:"type"`
	Visibility  string          `json:"visibility"`
	Topics      []string        `json:"topics"`
	Data        json.RawMessage `json:"data"`
	OccurredAt  time.Time       `json:"occurred_at"`
}

// Topic helpers.
func WorkspaceTopic(wid uuid.UUID) string { return "workspace:" + wid.String() }
func UserTopic(uid uuid.UUID) string      { return "user:" + uid.String() }

// ContactTopic is a visitor's widget topic for events about a chat of brand bid. The gateway
// pushes them to `contact:<wid>:<cid>` sockets of that brand only (contracts/realtime.md).
func ContactTopic(wid, cid, bid uuid.UUID) string {
	return "contact:" + wid.String() + ":" + cid.String() + ":" + bid.String()
}
func TeamTopic(wid, tid uuid.UUID) string { return "team:" + wid.String() + ":" + tid.String() }

// PrivateTopic marks an envelope scoped to people: the gateway gives the workspace channel
// only the stand-in and pushes the envelope to the listed user topics.
const PrivateTopic = "private"

// Publish writes ev in tx and returns the envelope. The relay delivers it after commit.
func Publish(ctx context.Context, tx pgx.Tx, ev Event) (Envelope, error) {
	if ev.Visibility == "" {
		ev.Visibility = VisibilityAgents
	}
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal event %s: %w", ev.Type, err)
	}
	// An event about a ticket in a private team goes to that team (contracts/teams.md
	// "Visibility"): the ticket comes from TicketID, else from the data.
	team := ev.TeamID
	ticket := ev.TicketID
	if ticket == nil && team == nil && !ev.Workspace {
		ticket = TicketOf(ev.Data)
	}
	if ev.Workspace {
		team, ticket = nil, nil
	}
	if team == nil && ticket != nil {
		tid, err := PrivateTeam(ctx, tx, ev.WorkspaceID, *ticket)
		if err != nil {
			return Envelope{}, fmt.Errorf("event team: %w", err)
		}
		team = tid
	}
	topics := []string{WorkspaceTopic(ev.WorkspaceID)}
	if team != nil {
		topics = []string{TeamTopic(ev.WorkspaceID, *team)}
	}
	var users []uuid.UUID
	if ev.Users != nil {
		team = nil
		users = []uuid.UUID{}
		topics = []string{PrivateTopic}
		for _, u := range ev.Users {
			if u != uuid.Nil && !slices.Contains(users, u) {
				users = append(users, u)
				topics = append(topics, UserTopic(u))
			}
		}
	}
	for _, t := range ev.Topics {
		if users != nil && !strings.HasPrefix(t, "user:") {
			continue
		}
		if !slices.Contains(topics, t) {
			topics = append(topics, t)
		}
	}
	env := Envelope{ID: ids.New(), WorkspaceID: ev.WorkspaceID, Type: ev.Type, Visibility: ev.Visibility,
		Topics: topics, Data: data, OccurredAt: time.Now().UTC()}
	if err := tx.QueryRow(ctx, `UPDATE workspaces SET event_seq = event_seq + 1 WHERE id = $1 RETURNING event_seq`,
		ev.WorkspaceID).Scan(&env.Seq); err != nil {
		return Envelope{}, fmt.Errorf("next event seq: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (workspace_id, seq, id, type, visibility, topics, data, created_at, team_id, user_ids)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		env.WorkspaceID, env.Seq, env.ID, env.Type, env.Visibility, env.Topics, data, env.OccurredAt, team, users); err != nil {
		return Envelope{}, fmt.Errorf("insert event: %w", err)
	}
	// Wake the relay at commit; it also polls, so a lost notify only adds latency.
	if _, err := tx.Exec(ctx, `SELECT pg_notify('od_events', '')`); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

// HiddenType is the stand-in for an event of a private team the reader cannot see: it keeps
// seq contiguous and carries no data (contracts/teams.md "Visibility").
const HiddenType = "event.hidden"

// Hide returns the stand-in for e.
func Hide(e Envelope) Envelope {
	return Envelope{ID: e.ID, Seq: e.Seq, WorkspaceID: e.WorkspaceID, Type: HiddenType, Visibility: VisibilityAgents,
		Data: json.RawMessage(`{}`), OccurredAt: e.OccurredAt}
}

// Since returns events after seq for replay. resync=true when the gap is older than retention.
// Events of the hidden private teams, and events scoped to people other than viewer (nil:
// every such event), are replaced by their stand-in.
func Since(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, wid uuid.UUID, since int64, limit int, includeAgents bool, hidden []uuid.UUID, viewer *uuid.UUID) (out []Envelope, latest int64, resync bool, err error) {
	if err = q.QueryRow(ctx, `SELECT event_seq FROM workspaces WHERE id = $1`, wid).Scan(&latest); err != nil {
		return nil, 0, false, err
	}
	if since >= latest {
		return []Envelope{}, latest, false, nil
	}
	var oldest *int64
	if err = q.QueryRow(ctx, `SELECT min(seq) FROM events WHERE workspace_id = $1`, wid).Scan(&oldest); err != nil {
		return nil, 0, false, err
	}
	if oldest == nil || *oldest > since+1 {
		return []Envelope{}, latest, true, nil
	}
	rows, err := q.Query(ctx, `SELECT id, seq, workspace_id, type, visibility, topics, data, created_at,
		COALESCE(team_id = ANY($5::uuid[]), false) OR (user_ids IS NOT NULL AND NOT COALESCE($6::uuid = ANY(user_ids), false)) FROM events
		WHERE workspace_id = $1 AND seq > $2 AND ($3 OR visibility = 'public') ORDER BY seq LIMIT $4`, wid, since, includeAgents, limit+1, hidden, viewer)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Envelope
		var hide bool
		if err = rows.Scan(&e.ID, &e.Seq, &e.WorkspaceID, &e.Type, &e.Visibility, &e.Topics, &e.Data, &e.OccurredAt, &hide); err != nil {
			return nil, 0, false, err
		}
		if hide {
			e = Hide(e)
		}
		out = append(out, e)
	}
	if len(out) > limit {
		// Too far behind to page through; the client should refetch.
		return []Envelope{}, latest, true, rows.Err()
	}
	if out == nil {
		out = []Envelope{}
	}
	return out, latest, false, rows.Err()
}
