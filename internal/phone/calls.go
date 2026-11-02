package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/routing"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/phone/provider"
)

// Call outcomes, set once a call has ended.
const (
	OutcomeAnswered  = "answered"
	OutcomeMissed    = "missed"
	OutcomeVoicemail = "voicemail"
)

// ActivityCall is the timeline entry of a call (contracts/phone.md "Timeline").
const ActivityCall = "call"

// callEndedDelay is how long after a call's final event its call_ended automations run:
// the tags and comments the team adds in the provider right after hanging up are then in
// the facts they read (read when they run, not when they are queued).
const callEndedDelay = time.Minute

// errCallUnknown: an AI event about a call whose own events are not in yet. River retries.
var errCallUnknown = errors.New("phone event about a call not seen yet")

type phoneCall struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	ConnectionID   *uuid.UUID
	Provider       string
	ExternalID     string
	TicketID       *uuid.UUID
	ActivityID     *uuid.UUID
	ContactID      *uuid.UUID
	Direction      string
	Stage          string
	Outcome        string
	MissedReason   string
	Number         string
	LineID         string
	LineName       string
	AgentUserID    *uuid.UUID
	AgentName      string
	CallerName     string
	StartedAt      *time.Time
	AnsweredAt     *time.Time
	EndedAt        *time.Time
	Duration       int
	HasRecording   bool
	HasVoicemail   bool
	AssetURL       string
	RecordingURL   string
	VoicemailURL   string
	LinksExpireAt  *time.Time
	Tags           []string
	TagsAt         *time.Time
	Comments       []provider.Comment
	CommentsAt     *time.Time
	Summary        string
	Transcript     string
	OutsideHours   bool
	EndedTriggerAt *time.Time
	CardQueuedAt   *time.Time
}

const callSelect = `SELECT id, workspace_id, connection_id, provider, external_call_id, ticket_id, activity_id, contact_id, direction,
	stage, outcome, missed_reason, number, line_id, line_name, agent_user_id, agent_name, caller_name, started_at, answered_at, ended_at,
	duration_seconds, has_recording, has_voicemail, asset_url, recording_url, voicemail_url, links_expire_at, tags, comments,
	summary, transcript, outside_hours, ended_trigger_at, card_queued_at, tags_at, comments_at
	FROM phone_calls`

func scanCall(row pgx.Row) (phoneCall, error) {
	var c phoneCall
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.ConnectionID, &c.Provider, &c.ExternalID, &c.TicketID, &c.ActivityID, &c.ContactID, &c.Direction,
		&c.Stage, &c.Outcome, &c.MissedReason, &c.Number, &c.LineID, &c.LineName, &c.AgentUserID, &c.AgentName, &c.CallerName, &c.StartedAt, &c.AnsweredAt,
		&c.EndedAt, &c.Duration, &c.HasRecording, &c.HasVoicemail, &c.AssetURL, &c.RecordingURL, &c.VoicemailURL, &c.LinksExpireAt,
		&c.Tags, &c.Comments, &c.Summary, &c.Transcript, &c.OutsideHours, &c.EndedTriggerAt, &c.CardQueuedAt, &c.TagsAt, &c.CommentsAt)
	return c, err
}

type inboundWorker struct {
	river.WorkerDefaults[jobs.PhoneInboundArgs]
	s *Service
}

func (w *inboundWorker) Work(ctx context.Context, job *river.Job[jobs.PhoneInboundArgs]) error {
	err := w.s.ProcessInbound(ctx, job.Args)
	var limited *provider.RateLimited
	if errors.As(err, &limited) {
		if time.Since(job.CreatedAt) > 24*time.Hour {
			return river.JobCancel(errors.New("phone provider retry deadline exceeded"))
		}
		return river.JobSnooze(limited.RetryAfter)
	}
	return err
}

var errCallTicketMoved = errors.New("call ticket moved during inbound processing")

// ProcessInbound applies one stored provider event to its call, once: the event row is
// locked and marked processed in the same transaction as everything it changed.
func (s *Service) ProcessInbound(ctx context.Context, a jobs.PhoneInboundArgs) error {
	for {
		err := s.processInbound(ctx, a)
		if !errors.Is(err, errCallTicketMoved) {
			return err
		}
	}
}

func (s *Service) processInbound(ctx context.Context, a jobs.PhoneInboundArgs) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var raw []byte
		var done *time.Time
		err := tx.QueryRow(ctx, `SELECT payload, processed_at FROM phone_inbound_events WHERE connection_id = $1 AND external_event_id = $2 FOR UPDATE`,
			a.ConnectionID, a.ExternalEventID).Scan(&raw, &done)
		if db.IsNotFound(err) || err == nil && done != nil {
			return nil // the connection is gone, or the event was applied
		}
		if err != nil {
			return err
		}
		c, err := scanConnection(tx.QueryRow(ctx, connectionSelect+` WHERE id = $1`, a.ConnectionID))
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		suspended, err := domain.WorkspaceSuspended(ctx, tx, c.WorkspaceID)
		if err != nil {
			return err
		}
		var ev provider.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return fmt.Errorf("phone event payload: %w", err)
		}
		if !suspended {
			switch ev.Kind {
			case provider.KindCall:
				err = s.applyCall(ctx, tx, c, ev)
			case provider.KindSummary, provider.KindTranscript:
				err = s.applyIntelligence(ctx, tx, c, ev)
			}
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE phone_inbound_events SET processed_at = now() WHERE connection_id = $1 AND external_event_id = $2`,
			a.ConnectionID, a.ExternalEventID)
		return err
	})
}

func stageRank(s string) int {
	switch s {
	case provider.StageRinging:
		return 1
	case provider.StageAnswered:
		return 2
	case provider.StageEnded:
		return 3
	}
	return 0
}

// merge folds what one event saw into the call. Events arrive late, twice or out of order,
// so the call only moves forward: a stage never goes back, a time once known stays, and an
// event without a value leaves the one the call has.
func merge(c *phoneCall, ev provider.Event) {
	x := ev.Call
	if x == nil {
		return
	}
	if stageRank(x.Stage) > stageRank(c.Stage) {
		c.Stage = x.Stage
	}
	if ev.Final {
		c.Stage = provider.StageEnded
	}
	if c.Number == "" && !x.Anonymous {
		c.Number = NormalizeNumber(x.Number)
	}
	if x.LineID != "" {
		c.LineID = x.LineID
	}
	if x.LineName != "" {
		c.LineName = x.LineName
	}
	// The start is the earliest the call was seen: the provider's start, else the time of
	// its first event, which a later event with the provider's start corrects.
	started := x.StartedAt
	if started == nil && !ev.At.IsZero() {
		at := ev.At
		started = &at
	}
	if started != nil && (c.StartedAt == nil || started.Before(*c.StartedAt)) {
		c.StartedAt = started
	}
	if c.AnsweredAt == nil && x.AnsweredAt != nil {
		c.AnsweredAt = x.AnsweredAt
	}
	if x.EndedAt != nil {
		c.EndedAt = x.EndedAt
	}
	if x.Duration > c.Duration {
		c.Duration = x.Duration
	}
	if x.MissedReason != "" {
		c.MissedReason = x.MissedReason
	}
	c.HasVoicemail = c.HasVoicemail || x.Voicemail
	c.HasRecording = c.HasRecording || x.Recording
	if x.AssetURL != "" {
		c.AssetURL = x.AssetURL
	}
	if x.RecordingURL != "" || x.VoicemailURL != "" {
		if x.RecordingURL != "" {
			c.RecordingURL = x.RecordingURL
		}
		if x.VoicemailURL != "" {
			c.VoicemailURL = x.VoicemailURL
		}
		if x.LinksValid > 0 {
			at := ev.At
			if at.IsZero() {
				at = time.Now().UTC()
			}
			exp := at.Add(x.LinksValid)
			c.LinksExpireAt = &exp
		}
	}
	// Tags and comments come whole with each event, an empty list included, so the list of
	// the latest event wins: one older than the list the call has never replaces it.
	if x.Tags != nil && newer(ev.At, c.TagsAt) {
		at := ev.At
		c.Tags, c.TagsAt = x.Tags, &at
	}
	if x.Comments != nil && newer(ev.At, c.CommentsAt) {
		at := ev.At
		c.Comments, c.CommentsAt = x.Comments, &at
	}
	if x.Agent != nil {
		if n := displayname.Clean(x.Agent.Name, displayname.Person); n != "" {
			c.AgentName = n
		}
	}
	if x.Caller != nil {
		if n := displayname.Clean(x.Caller.Name, displayname.Person); n != "" {
			c.CallerName = n
		}
	}
	c.Outcome = outcome(*c)
}

// newer reports whether an event at at is as recent as the one a value came with.
func newer(at time.Time, stored *time.Time) bool {
	return stored == nil || !at.Before(*stored)
}

// outcome is how an ended call went: a voicemail was left, someone answered, or nobody
// did. A call still going has none.
func outcome(c phoneCall) string {
	switch {
	case c.Stage != provider.StageEnded:
		return ""
	case c.HasVoicemail:
		return OutcomeVoicemail
	case c.AnsweredAt != nil:
		return OutcomeAnswered
	}
	return OutcomeMissed
}

func (s *Service) lockCall(ctx context.Context, tx pgx.Tx, c PhoneConnection, externalID string) (phoneCall, error) {
	before, err := scanCall(tx.QueryRow(ctx, callSelect+` WHERE workspace_id = $1 AND provider = $2 AND external_call_id = $3`, c.WorkspaceID, c.Provider, externalID))
	if err != nil {
		return before, err
	}
	if before.TicketID != nil {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM tickets WHERE workspace_id = $1 AND id = $2 FOR NO KEY UPDATE`, c.WorkspaceID, *before.TicketID).Scan(&id); err != nil {
			return phoneCall{}, err
		}
	}
	after, err := scanCall(tx.QueryRow(ctx, callSelect+` WHERE workspace_id = $1 AND provider = $2 AND external_call_id = $3 FOR UPDATE`, c.WorkspaceID, c.Provider, externalID))
	if err != nil {
		return after, err
	}
	if (before.TicketID == nil) != (after.TicketID == nil) || before.TicketID != nil && *before.TicketID != *after.TicketID {
		return phoneCall{}, errCallTicketMoved
	}
	return after, nil
}

func (s *Service) applyCall(ctx context.Context, tx pgx.Tx, c PhoneConnection, ev provider.Event) error {
	if ev.Call == nil || ev.CallID == "" {
		return nil
	}
	if ev.Continues != "" {
		if err := s.continueCall(ctx, tx, c, ev.Continues, ev.CallID); err != nil {
			return err
		}
	}
	if ev.Call.Direction == "" {
		// An event that doesn't know the call's direction (Voys' recording event) only
		// updates a call already seen.
		var known bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM phone_calls WHERE workspace_id = $1 AND provider = $2 AND external_call_id = $3)`,
			c.WorkspaceID, c.Provider, ev.CallID).Scan(&known); err != nil {
			return err
		}
		if !known {
			return errCallUnknown
		}
	} else if _, err := tx.Exec(ctx, `INSERT INTO phone_calls (id, workspace_id, connection_id, provider, external_call_id, direction)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (workspace_id, provider, external_call_id) DO NOTHING`,
		ids.New(), c.WorkspaceID, c.ID, c.Provider, ev.CallID, ev.Call.Direction); err != nil {
		return fmt.Errorf("insert call: %w", err)
	}
	call, err := s.lockCall(ctx, tx, c, ev.CallID)
	if err != nil {
		return fmt.Errorf("lock call: %w", err)
	}
	attached := call.TicketID != nil
	merge(&call, ev)
	if a := ev.Call.Agent; a != nil && a.Email != "" {
		if uid, err := s.memberByEmail(ctx, tx, c.WorkspaceID, a.Email); err != nil {
			return err
		} else if uid != nil {
			call.AgentUserID = uid
		}
	}
	if call.TicketID == nil {
		if err := s.place(ctx, tx, c, &call); err != nil {
			return err
		}
	}
	if call.TicketID == nil {
		// A blocked contact's call goes on no ticket, writes no entry and runs nothing. Its
		// row is kept, without what was said or who said it, so its later events are known.
		return s.saveCall(ctx, tx, withoutContent(call))
	}
	if call.OutsideHours, err = s.outsideHours(ctx, tx, call); err != nil {
		return err
	}
	if err := s.writeEntry(ctx, tx, &call); err != nil {
		return err
	}
	final := ev.Final && call.EndedTriggerAt == nil
	if final {
		now := time.Now().UTC()
		call.EndedTriggerAt = &now
	}
	card := c.InsightCards && s.describe(c.Provider).InsightCards && call.Direction == "inbound" && call.Stage == provider.StageRinging && call.CardQueuedAt == nil
	if card {
		now := time.Now().UTC()
		call.CardQueuedAt = &now
	}
	if err := s.saveCall(ctx, tx, call); err != nil {
		return err
	}
	if !attached || final {
		// The ticket moves up the lists that sort by activity, and its viewers refresh.
		if _, err := tx.Exec(ctx, `UPDATE tickets SET updated_at = now() WHERE workspace_id = $1 AND id = $2`, c.WorkspaceID, *call.TicketID); err != nil {
			return err
		}
		if err := domain.PublishTicketUpdated(ctx, tx, c.WorkspaceID, *call.TicketID, []string{"calls"}); err != nil {
			return err
		}
	}
	if final {
		if c.AssignAnswered && call.Outcome == OutcomeAnswered && call.AgentUserID != nil {
			if err := tickets.AssignAnsweredCall(ctx, tx, s.d, c.WorkspaceID, *call.TicketID, *call.AgentUserID); err != nil {
				return err
			}
		}
		callID := call.ID
		if _, err := s.d.Jobs.InsertTx(ctx, tx, jobs.TicketTriggerArgs{WorkspaceID: c.WorkspaceID, TicketID: *call.TicketID,
			Trigger: jobs.TriggerCallEnded, CallID: &callID}, &river.InsertOpts{ScheduledAt: time.Now().Add(callEndedDelay)}); err != nil {
			return fmt.Errorf("queue trigger: %w", err)
		}
	}
	if card {
		if _, err := s.d.Jobs.InsertTx(ctx, tx, jobs.PhoneInsightCardArgs{WorkspaceID: c.WorkspaceID, CallID: call.ID},
			&river.InsertOpts{MaxAttempts: 3}); err != nil {
			return fmt.Errorf("queue insight card: %w", err)
		}
	}
	return nil
}

// continueCall moves the call documented under the id from to the id to, under which a
// transfer joined it with another call goes on, so their events make one call on one
// ticket. A call already known under to stays as it is.
func (s *Service) continueCall(ctx context.Context, tx pgx.Tx, c PhoneConnection, from, to string) error {
	// Locked like any event of the call, ticket first, so this never waits on an event
	// of the call that waits on it.
	if _, err := s.lockCall(ctx, tx, c, from); db.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("lock continued call: %w", err)
	}
	_, err := tx.Exec(ctx, `UPDATE phone_calls SET external_call_id = $4, updated_at = now()
		WHERE workspace_id = $1 AND provider = $2 AND external_call_id = $3
		AND NOT EXISTS (SELECT 1 FROM phone_calls WHERE workspace_id = $1 AND provider = $2 AND external_call_id = $4)`,
		c.WorkspaceID, c.Provider, from, to)
	return err
}

// applyIntelligence adds a provider's AI summary or transcript to its call's entry.
func (s *Service) applyIntelligence(ctx context.Context, tx pgx.Tx, c PhoneConnection, ev provider.Event) error {
	call, err := s.lockCall(ctx, tx, c, ev.CallID)
	if db.IsNotFound(err) {
		return errCallUnknown
	}
	if err != nil {
		return err
	}
	if call.TicketID == nil {
		return nil // a blocked contact's call keeps no content
	}
	// A transcript event only announces it, and so does a summary event without its text
	// (a provider whose events are not signed): the text comes from the provider.
	if ev.Kind == provider.KindTranscript || ev.Summary == "" {
		a, ok := s.adapters[c.Provider]
		if !ok {
			return nil
		}
		creds, err := s.credentials(c)
		if err != nil {
			return err
		}
		text, err := a.Content(ctx, s.http, creds, ev.Kind, ev.CallID)
		if err != nil {
			return err
		}
		if ev.Kind == provider.KindTranscript {
			ev.Transcript = text
		} else {
			ev.Summary = text
		}
	}
	if ev.Summary != "" {
		call.Summary = ev.Summary
	}
	if ev.Transcript != "" {
		call.Transcript = ev.Transcript
	}
	if err := s.saveCall(ctx, tx, call); err != nil {
		return err
	}
	return s.writeEntry(ctx, tx, &call)
}

func (s *Service) saveCall(ctx context.Context, tx pgx.Tx, c phoneCall) error {
	_, err := tx.Exec(ctx, `UPDATE phone_calls SET ticket_id = $2, activity_id = $3, contact_id = $4, stage = $5, outcome = $6,
		missed_reason = $7, number = $8, line_name = $9, agent_user_id = $10, agent_name = $11, caller_name = $12, started_at = $13,
		answered_at = $14, ended_at = $15, duration_seconds = $16, has_recording = $17, has_voicemail = $18, asset_url = $19,
		summary = $20, transcript = $21, outside_hours = $22, ended_trigger_at = $23, card_queued_at = $24, line_id = $25,
		recording_url = $26, voicemail_url = $27, links_expire_at = $28, tags = $29, comments = $30, tags_at = $31,
		comments_at = $32, updated_at = now()
		WHERE id = $1`, c.ID, c.TicketID, c.ActivityID, c.ContactID, c.Stage, c.Outcome, c.MissedReason, c.Number, c.LineName,
		c.AgentUserID, c.AgentName, c.CallerName, c.StartedAt, c.AnsweredAt, c.EndedAt, c.Duration, c.HasRecording, c.HasVoicemail,
		c.AssetURL, c.Summary, c.Transcript, c.OutsideHours, c.EndedTriggerAt, c.CardQueuedAt, c.LineID,
		c.RecordingURL, c.VoicemailURL, c.LinksExpireAt, nonNil(c.Tags), commentsOrEmpty(c.Comments), c.TagsAt, c.CommentsAt)
	if err != nil {
		return fmt.Errorf("save call: %w", err)
	}
	return nil
}

// withoutContent is a call with only its facts: no number, names, links, labels, comments
// or AI text.
func withoutContent(c phoneCall) phoneCall {
	c.Number, c.CallerName, c.AgentName, c.Summary, c.Transcript = "", "", "", "", ""
	c.AssetURL, c.RecordingURL, c.VoicemailURL, c.LinksExpireAt = "", "", "", nil
	c.Tags, c.Comments = []string{}, []provider.Comment{}
	return c
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func commentsOrEmpty(c []provider.Comment) []provider.Comment {
	if c == nil {
		return []provider.Comment{}
	}
	return c
}

// memberByEmail maps a provider user to the member with the same email (owners, admins
// and agents; never a viewer).
func (s *Service) memberByEmail(ctx context.Context, tx pgx.Tx, wid uuid.UUID, email string) (*uuid.UUID, error) {
	var uid uuid.UUID
	err := tx.QueryRow(ctx, `SELECT m.user_id FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND u.email = $2::citext AND m.role <> 'viewer'`, wid, email).Scan(&uid)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &uid, nil
}

// place finds the call's contact by its number (creating one for a new number) and its
// ticket: the contact's open ticket, else their latest one solved within the connection's
// reuse_days (when it was solved, not last touched), else a new ticket on the phone channel. A withheld number
// always opens a new ticket without a contact. A blocked contact's call gets no ticket,
// like their email (internal/email).
func (s *Service) place(ctx context.Context, tx pgx.Tx, c PhoneConnection, call *phoneCall) error {
	wid := c.WorkspaceID
	contactName := ""
	if call.Number != "" {
		pc, err := domain.ContactByPhone(ctx, tx, wid, call.Number, call.CallerName)
		if err != nil {
			return err
		}
		cid := pc.ID
		call.ContactID = &cid
		if pc.Blocked {
			return nil
		}
		var tid uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM tickets WHERE workspace_id = $1 AND contact_id = $2 AND deleted_at IS NULL
			AND merged_into_id IS NULL AND NOT spam
			AND (status IN ('open', 'pending', 'on_hold') OR ($3::int > 0 AND status = 'solved' AND solved_at > now() - make_interval(days => $3::int)))
			ORDER BY (status <> 'solved') DESC, CASE WHEN status = 'solved' THEN solved_at END DESC NULLS LAST, updated_at DESC LIMIT 1 FOR NO KEY UPDATE`, wid, cid, c.ReuseDays).Scan(&tid)
		if err == nil {
			call.TicketID = &tid
			return nil
		}
		if !db.IsNotFound(err) {
			return fmt.Errorf("find ticket: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM contacts WHERE workspace_id = $1 AND id = $2`, wid, cid).Scan(&contactName); err != nil {
			return err
		}
	}
	subject := Subject(call.Direction, contactName, call.Number)
	brand, err := domain.ResolveBrand(ctx, tx, wid, nil, nil, nil)
	if err != nil {
		return err
	}
	res, err := routing.Resolve(ctx, tx, routing.Input{WorkspaceID: wid, Channel: domain.ChannelPhone, BrandID: brand,
		Subject: subject, ContactID: call.ContactID})
	if err != nil {
		return err
	}
	t, err := domain.CreateTicket(ctx, tx, domain.NewTicket{WorkspaceID: wid, Subject: subject, Channel: domain.ChannelPhone,
		ContactID: call.ContactID, TeamID: res.TeamID, Routed: res.Routed, BrandID: &brand, RequiredSkillIDs: res.RequiredSkillIDs})
	if err != nil {
		return err
	}
	if rule := res.RuleID(); rule != nil {
		if err := routing.Count(ctx, tx, *rule); err != nil {
			return err
		}
	}
	if _, err := s.d.Jobs.InsertTx(ctx, tx, jobs.TicketTriggerArgs{WorkspaceID: wid, TicketID: t.ID, Trigger: jobs.TriggerTicketCreated}, nil); err != nil {
		return fmt.Errorf("queue trigger: %w", err)
	}
	call.TicketID = &t.ID
	return nil
}

// Subject names a ticket a call opened. It is the team's to rename.
func Subject(direction, name, number string) string {
	who := name
	if who == "" {
		who = number
	}
	if who == "" {
		return "Call from a withheld number"
	}
	if direction == "outbound" {
		return "Call to " + who
	}
	return "Call from " + who
}

// outsideHours reports whether the call started outside the business hours of its
// ticket's team, else the workspace's. Hours that are off are never outside.
func (s *Service) outsideHours(ctx context.Context, tx pgx.Tx, call phoneCall) (bool, error) {
	if call.StartedAt == nil || call.TicketID == nil {
		return false, nil
	}
	var team *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT team_id FROM tickets WHERE workspace_id = $1 AND id = $2`, call.WorkspaceID, *call.TicketID).Scan(&team); err != nil {
		return false, err
	}
	bh, loc, err := hd.HoursFor(ctx, tx, call.WorkspaceID, team)
	if err != nil {
		return false, err
	}
	if !bh.Enabled {
		return false, nil
	}
	return !hd.OpenAt(bh, loc, *call.StartedAt), nil
}

// entryData is the call entry's data (contracts/phone.md "Timeline").
func (s *Service) entryData(ctx context.Context, tx pgx.Tx, c phoneCall) (map[string]any, error) {
	ts := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	d := map[string]any{
		"call_id": c.ID.String(), "provider": c.Provider, "direction": c.Direction, "stage": c.Stage, "outcome": c.Outcome,
		"missed_reason": c.MissedReason, "number": c.Number, "line": c.LineName, "line_id": c.LineID, "caller_name": c.CallerName,
		"agent": nil, "agent_name": c.AgentName, "started_at": ts(c.StartedAt), "answered_at": ts(c.AnsweredAt), "ended_at": ts(c.EndedAt),
		"duration": c.Duration, "recording": c.HasRecording, "voicemail": c.HasVoicemail, "asset_url": c.AssetURL,
		"recording_url": c.RecordingURL, "voicemail_url": c.VoicemailURL, "links_expire_at": ts(c.LinksExpireAt),
		"tags": nonNil(c.Tags), "comments": commentsOrEmpty(c.Comments),
		"summary": c.Summary, "transcript": c.Transcript, "outside_hours": c.OutsideHours,
	}
	if c.AgentUserID != nil {
		u, err := domain.LoadUserRef(ctx, tx, *c.AgentUserID)
		if err != nil && !db.IsNotFound(err) {
			return nil, err
		}
		if err == nil {
			d["agent"] = u
		}
	}
	// Stored as JSON reads it back, so comparing with the stored entry is exact.
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	return out, json.Unmarshal(raw, &out)
}

// writeEntry creates the call's timeline entry, or updates it when something it shows
// changed (activity.updated), so one call is one entry from ringing to its recording.
func (s *Service) writeEntry(ctx context.Context, tx pgx.Tx, c *phoneCall) error {
	if c.TicketID == nil {
		return nil
	}
	data, err := s.entryData(ctx, tx, *c)
	if err != nil {
		return err
	}
	if c.ActivityID != nil {
		var stored map[string]any
		err := tx.QueryRow(ctx, `SELECT data FROM activities WHERE workspace_id = $1 AND id = $2`, c.WorkspaceID, *c.ActivityID).Scan(&stored)
		if err == nil {
			a, _ := json.Marshal(stored)
			b, _ := json.Marshal(data)
			if string(a) == string(b) {
				return nil
			}
			_, err = domain.UpdateActivity(ctx, tx, c.WorkspaceID, *c.ActivityID, data)
			return err
		}
		if !db.IsNotFound(err) {
			return err
		}
		c.ActivityID = nil // the entry went with its ticket: a new one is written
	}
	a, err := domain.LogActivity(ctx, tx, c.WorkspaceID, *c.TicketID, "system", nil, ActivityCall, data)
	if err != nil {
		return err
	}
	c.ActivityID = &a.ID
	_, err = tx.Exec(ctx, `UPDATE phone_calls SET activity_id = $2 WHERE id = $1`, c.ID, a.ID)
	return err
}
