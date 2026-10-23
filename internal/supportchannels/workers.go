package supportchannels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

type dispatchWorker struct {
	river.WorkerDefaults[jobs.SupportChannelDispatchArgs]
	s *Service
}

func (w *dispatchWorker) Timeout(*river.Job[jobs.SupportChannelDispatchArgs]) time.Duration {
	return 30 * time.Second
}
func (w *dispatchWorker) Work(ctx context.Context, j *river.Job[jobs.SupportChannelDispatchArgs]) error {
	return w.s.dispatch(ctx, j.Args)
}

type inboundWorker struct {
	river.WorkerDefaults[jobs.SupportChannelInboundArgs]
	s *Service
}

var errTicketCommandBusy = errors.New("an earlier support channel command is still processing")

func (w *inboundWorker) Timeout(*river.Job[jobs.SupportChannelInboundArgs]) time.Duration {
	return 5 * time.Minute
}
func (w *inboundWorker) Work(ctx context.Context, j *river.Job[jobs.SupportChannelInboundArgs]) error {
	err := w.s.processInbound(ctx, j.Args)
	if errors.Is(err, errTicketCommandBusy) || errors.Is(err, errConversationBusy) {
		// Waiting for an earlier command is not a failure: it uses no attempt.
		return river.JobSnooze(2 * time.Second)
	}
	return err
}

type deliveryTarget struct {
	destination
	Provider       string
	CredentialsEnc []byte
}

func (s *Service) dispatch(ctx context.Context, a jobs.SupportChannelDispatchArgs) error {
	var typ string
	var data []byte
	var at time.Time
	var privateTeam, ticketID *uuid.UUID
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT type,data,created_at,team_id,COALESCE(NULLIF(data->>'ticket_id','')::uuid,NULLIF(data->'ticket'->>'id','')::uuid,NULLIF(data->'message'->>'ticket_id','')::uuid) FROM events WHERE workspace_id=$1 AND seq=$2`, a.WorkspaceID, a.EventSeq).Scan(&typ, &data, &at, &privateTeam, &ticketID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if ticketID == nil {
		return nil
	}
	if privateTeam != nil {
		return nil
	}
	t, err := domain.LoadTicket(ctx, s.d.DB.Pool, a.WorkspaceID, *ticketID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.Channel != domain.ChannelEmail {
		return nil
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT DISTINCT ON (d.connection_id) d.id,d.connection_id,d.team_id,d.external_channel_id,d.external_channel_name,d.enabled,d.include_notes,d.created_at,c.provider,c.credentials_enc
		FROM support_channel_destinations d JOIN support_channel_connections c ON c.id=d.connection_id
		WHERE d.workspace_id=$1 AND d.enabled AND (d.team_id IS NOT DISTINCT FROM $2 OR d.team_id IS NULL)
		ORDER BY d.connection_id,(d.team_id IS NOT NULL) DESC`, a.WorkspaceID, t.TeamID)
	if err != nil {
		return err
	}
	targets, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (deliveryTarget, error) {
		var d deliveryTarget
		err := row.Scan(&d.ID, &d.ConnectionID, &d.TeamID, &d.ExternalChannelID, &d.ExternalChannelName, &d.Enabled, &d.IncludeNotes, &d.CreatedAt, &d.Provider, &d.CredentialsEnc)
		return d, err
	})
	if err != nil {
		return err
	}
	for _, d := range targets {
		if err := s.dispatchTo(ctx, a, typ, data, at, t, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) dispatchTo(ctx context.Context, a jobs.SupportChannelDispatchArgs, typ string, data []byte, at time.Time, t domain.Ticket, d deliveryTarget) error {
	var err error
	adapter, ok := s.registry.Get(d.Provider)
	if !ok {
		return nil
	}
	creds := map[string]string{}
	if err = s.d.Box.OpenJSON(d.CredentialsEnc, &creds); err != nil {
		return err
	}
	creds["channel_id"] = d.ExternalChannelID
	var delivered *time.Time
	err = s.d.DB.Pool.QueryRow(ctx, `INSERT INTO support_channel_deliveries(destination_id,event_seq) VALUES($1,$2) ON CONFLICT(destination_id,event_seq) DO UPDATE SET attempts=support_channel_deliveries.attempts+1 RETURNING delivered_at`, d.ID, a.EventSeq).Scan(&delivered)
	if err != nil {
		return err
	}
	if delivered != nil {
		return nil
	}
	ct := toChannelTicket(s.d.Cfg.AppURL, t)
	var thread channel.Thread
	var last, historySeq int64
	var historyBefore, historyPosted *time.Time
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT COALESCE(external_thread_id,''),COALESCE(external_root_message_id,''),COALESCE(external_channel_id,''),last_event_seq,history_event_seq,history_before,history_posted_at FROM support_channel_threads WHERE destination_id=$1 AND ticket_id=$2`, d.ID, t.ID).Scan(&thread.ID, &thread.RootID, &thread.ChannelID, &last, &historySeq, &historyBefore, &historyPosted)
	if db.IsNotFound(err) {
		// The history covers what came before this event. Its own message, and
		// any written moments before it, are still on their way and post in full.
		before := historyCutoff(typ, data, at).Add(-historySettle)
		claim, claimErr := s.d.DB.Pool.Exec(ctx, `INSERT INTO support_channel_threads(id,workspace_id,connection_id,destination_id,ticket_id,last_event_seq,history_event_seq,history_before) VALUES($1,$2,$3,$4,$5,0,$6,$7) ON CONFLICT(destination_id,ticket_id) DO NOTHING`, ids.New(), a.WorkspaceID, d.ConnectionID, d.ID, t.ID, a.EventSeq, before)
		if claimErr != nil {
			return claimErr
		}
		if claim.RowsAffected() == 0 {
			return errors.New("support channel thread is being created")
		}
		thread, err = adapter.CreateThread(ctx, s.http, creds, d.ExternalChannelID, ct)
		if err != nil {
			_, _ = s.d.DB.Pool.Exec(ctx, `DELETE FROM support_channel_threads WHERE destination_id=$1 AND ticket_id=$2 AND external_thread_id IS NULL`, d.ID, t.ID)
			return s.connectionError(ctx, d.ConnectionID, err)
		}
		inbound := thread.ChannelID
		if inbound == "" {
			inbound = d.ExternalChannelID
		}
		_, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET external_channel_id=$3,external_thread_id=$4,external_root_message_id=$5,card_event_seq=$6,updated_at=now() WHERE destination_id=$1 AND ticket_id=$2`, d.ID, t.ID, inbound, thread.ID, thread.RootID, a.EventSeq)
		if err != nil {
			return err
		}
		last, historySeq, historyBefore, historyPosted = 0, a.EventSeq, &before, nil
	} else if err != nil {
		return err
	}
	if thread.ID == "" {
		return errors.New("support channel thread is being created")
	}
	if thread.ChannelID != "" {
		creds["channel_id"] = thread.ChannelID
	}
	if historyPosted == nil {
		claimed, claimErr := s.claimHistoryPost(ctx, d.ID, t.ID)
		if claimErr != nil {
			return claimErr
		}
		if claimed {
			defer s.releaseHistoryPost(d.ID, t.ID)
			before := at
			if historyBefore != nil {
				before = *historyBefore
			}
			text, posted, historyErr := s.recentHistory(ctx, a.WorkspaceID, t.ID, d.IncludeNotes, before)
			if historyErr != nil {
				return historyErr
			}
			if text != "" {
				if err = adapter.Post(ctx, s.http, creds, thread.ID, text); err != nil {
					return s.connectionError(ctx, d.ConnectionID, err)
				}
			}
			if _, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET history_posted_at=now(),history_message_ids=$3,history_processing_expires_at=NULL,updated_at=now() WHERE destination_id=$1 AND ticket_id=$2`, d.ID, t.ID, posted); err != nil {
				return err
			}
		}
	}
	switch typ {
	case "message.created":
		if a.EventSeq < historySeq {
			// An event from before the thread posts only if the history left it out.
			inHistory, historyErr := s.inHistory(ctx, d.ID, t.ID, data)
			if historyErr != nil {
				return historyErr
			}
			if inHistory {
				break
			}
		}
		text, decodeErr := messageCreatedText(data, d.IncludeNotes)
		if decodeErr != nil {
			return decodeErr
		}
		if text != "" {
			if err = adapter.Post(ctx, s.http, creds, thread.ID, text); err != nil {
				return s.connectionError(ctx, d.ConnectionID, err)
			}
		}
	case "ticket.updated":
		if thread.RootID != "" {
			claimed, claimErr := s.claimCardUpdate(ctx, d.ID, t.ID, a.EventSeq)
			if claimErr != nil {
				return claimErr
			}
			if !claimed {
				break
			}
			defer s.releaseCardUpdate(d.ID, t.ID, a.EventSeq)
			if err = adapter.Update(ctx, s.http, creds, thread.RootID, ct); err != nil {
				// A card that can't be edited is retried, but says nothing about
				// the connection: posting may well still work.
				s.d.Log.Warn("support channel card update failed", "connection_id", d.ConnectionID, "ticket_id", t.ID)
				return fmt.Errorf("support channel card update: %w", err)
			}
			if _, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET card_event_seq=$3,card_processing_seq=NULL,card_processing_expires_at=NULL,updated_at=now()
				WHERE destination_id=$1 AND ticket_id=$2 AND card_processing_seq=$3`, d.ID, t.ID, a.EventSeq); err != nil {
				return err
			}
		}
	}
	_, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET last_event_seq=GREATEST(last_event_seq,$2),updated_at=now() WHERE destination_id=$1 AND ticket_id=$3`, d.ID, a.EventSeq, t.ID)
	if err == nil {
		_, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_deliveries SET delivered_at=now() WHERE destination_id=$1 AND event_seq=$2`, d.ID, a.EventSeq)
	}
	if err == nil {
		_, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET status='connected',last_error='',updated_at=now() WHERE id=$1 AND last_error=$2`, d.ConnectionID, deliveryError)
	}
	return err
}

func (s *Service) claimCardUpdate(ctx context.Context, destinationID, ticketID uuid.UUID, eventSeq int64) (bool, error) {
	var claimed int64
	err := s.d.DB.Pool.QueryRow(ctx, `UPDATE support_channel_threads SET
		card_processing_seq=$3,card_processing_expires_at=now()+interval '1 minute'
		WHERE destination_id=$1 AND ticket_id=$2 AND card_event_seq<$3
		AND (card_processing_seq IS NULL OR card_processing_expires_at<now()) RETURNING card_processing_seq`,
		destinationID, ticketID, eventSeq).Scan(&claimed)
	if err == nil {
		return true, nil
	}
	if !db.IsNotFound(err) {
		return false, err
	}
	var current int64
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT card_event_seq FROM support_channel_threads WHERE destination_id=$1 AND ticket_id=$2`, destinationID, ticketID).Scan(&current)
	if err != nil {
		return false, err
	}
	if current >= eventSeq {
		return false, nil
	}
	return false, errors.New("support channel ticket card is being updated")
}

func (s *Service) releaseCardUpdate(destinationID, ticketID uuid.UUID, eventSeq int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET card_processing_seq=NULL,card_processing_expires_at=NULL
		WHERE destination_id=$1 AND ticket_id=$2 AND card_processing_seq=$3`, destinationID, ticketID, eventSeq)
}

func (s *Service) claimHistoryPost(ctx context.Context, destinationID, ticketID uuid.UUID) (bool, error) {
	var claimed time.Time
	err := s.d.DB.Pool.QueryRow(ctx, `UPDATE support_channel_threads SET history_processing_expires_at=now()+interval '1 minute'
		WHERE destination_id=$1 AND ticket_id=$2 AND history_posted_at IS NULL
		AND (history_processing_expires_at IS NULL OR history_processing_expires_at<now())
		RETURNING history_processing_expires_at`, destinationID, ticketID).Scan(&claimed)
	if err == nil {
		return true, nil
	}
	if !db.IsNotFound(err) {
		return false, err
	}
	var posted *time.Time
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT history_posted_at FROM support_channel_threads WHERE destination_id=$1 AND ticket_id=$2`, destinationID, ticketID).Scan(&posted)
	if err != nil {
		return false, err
	}
	if posted != nil {
		return false, nil
	}
	return false, errors.New("support channel recent history is being posted")
}

func (s *Service) releaseHistoryPost(destinationID, ticketID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET history_processing_expires_at=NULL
		WHERE destination_id=$1 AND ticket_id=$2 AND history_posted_at IS NULL`, destinationID, ticketID)
}

// historySettle keeps the newest messages out of a new thread's history: one
// written in the same transaction as the event that created the thread, or
// just before it, still has its own delivery to come and posts in full. The
// history's message ids keep an older event from posting a message twice.
const historySettle = 15 * time.Second

// historyCutoff is the instant before which a new thread's history is drawn:
// the event's own time, or a message.created event's message's.
func historyCutoff(typ string, data []byte, at time.Time) time.Time {
	if typ == "message.created" {
		var ev messageCreatedEvent
		if json.Unmarshal(data, &ev) == nil && ev.Message != nil && !ev.Message.CreatedAt.IsZero() && ev.Message.CreatedAt.Before(at) {
			return ev.Message.CreatedAt
		}
	}
	return at
}

// inHistory reports whether the history posted to a thread holds the event's message.
func (s *Service) inHistory(ctx context.Context, destinationID, ticketID uuid.UUID, data []byte) (bool, error) {
	var ev messageCreatedEvent
	if json.Unmarshal(data, &ev) != nil || ev.Message == nil {
		return false, nil
	}
	var ok bool
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT $3::uuid=ANY(history_message_ids) FROM support_channel_threads WHERE destination_id=$1 AND ticket_id=$2`,
		destinationID, ticketID, ev.Message.ID).Scan(&ok)
	return ok, err
}

type historyLine struct {
	ID        uuid.UUID
	Kind      string
	Direction string
	Body      string
	CreatedAt time.Time
}

// recentHistory is the bounded context posted once when a ticket first gets a
// provider thread: its five latest messages from before the event that created
// the thread, with the ids of the messages it holds.
func (s *Service) recentHistory(ctx context.Context, workspaceID, ticketID uuid.UUID, includeNotes bool, before time.Time) (string, []uuid.UUID, error) {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT id,kind,direction,body_text,created_at FROM messages
		WHERE ticket_id=$2 AND workspace_id=$1 AND created_at<$4 AND ($3 OR kind<>'note') AND body_text<>''
		ORDER BY created_at DESC,id DESC LIMIT 5`, workspaceID, ticketID, includeNotes, before)
	if err != nil {
		return "", nil, err
	}
	lines, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (historyLine, error) {
		var line historyLine
		return line, row.Scan(&line.ID, &line.Kind, &line.Direction, &line.Body, &line.CreatedAt)
	})
	if err != nil {
		return "", nil, err
	}
	posted := make([]uuid.UUID, 0, len(lines))
	if len(lines) == 0 {
		return "", posted, nil
	}
	var out strings.Builder
	out.WriteString("Recent history\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		who := "Agent"
		if line.Direction == "inbound" {
			who = "Customer"
		}
		if line.Kind == "note" {
			who = "Internal note"
		}
		fmt.Fprintf(&out, "\n%s · %s\n%s\n", who, line.CreatedAt.UTC().Format("2 Jan 15:04"), clip(line.Body, 500))
		posted = append(posted, line.ID)
	}
	return clip(out.String(), 3200), posted, nil
}

type messageCreatedEvent struct {
	Message *domain.Message `json:"message"`
}

// messageCreatedText is the line posted for a new message, or "" for nothing.
// Internal notes are posted only to a destination that includes them.
func messageCreatedText(data []byte, includeNotes bool) (string, error) {
	var ev messageCreatedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return "", fmt.Errorf("decode message.created event: %w", err)
	}
	if ev.Message == nil {
		return "", errors.New("decode message.created event: message is missing")
	}
	if ev.Message.Kind == "note" && !includeNotes {
		return "", nil
	}
	body := strings.TrimSpace(ev.Message.BodyText)
	if body == "" {
		return "", nil
	}
	who := "Agent"
	if ev.Message.Direction == "inbound" {
		who = "Customer"
	}
	if ev.Message.Kind == "note" {
		who = "Internal note"
	}
	return who + ": " + clip(body, 3500), nil
}

func toChannelTicket(app string, t domain.Ticket) channel.Ticket {
	customer := "Unknown customer"
	if t.Contact != nil {
		customer = t.Contact.Name
	}
	assignee := "Unassigned"
	if t.Assignee != nil {
		assignee = t.Assignee.Name
	}
	return channel.Ticket{ID: t.ID.String(), Number: t.Number, Subject: t.Subject, Customer: customer, Status: t.Status, Priority: t.Priority, Assignee: assignee, URL: strings.TrimRight(app, "/") + "/inbox/all/" + t.ID.String()}
}

func (s *Service) processInbound(ctx context.Context, a jobs.SupportChannelInboundArgs) error {
	c, err := s.loadConnection(ctx, a.ConnectionID)
	if db.IsNotFound(err) {
		// A deleted connection took its events with it; the ticket's lane skips them.
		return nil
	}
	if err != nil {
		return err
	}
	var raw []byte
	var done *time.Time
	var orderedTicket *uuid.UUID
	var commandSeq *int64
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT payload,processed_at,ticket_id,ticket_command_seq FROM support_channel_inbound_events WHERE connection_id=$1 AND external_event_id=$2`, a.ConnectionID, a.ExternalEventID).Scan(&raw, &done, &orderedTicket, &commandSeq)
	if db.IsNotFound(err) || done != nil {
		return nil
	}
	if err != nil {
		return err
	}
	if orderedTicket != nil && commandSeq != nil {
		claimed, err := s.claimTicketCommand(ctx, *orderedTicket, *commandSeq)
		if err != nil {
			return err
		}
		if claimed {
			defer s.releaseTicketCommand(*orderedTicket, *commandSeq)
		}
	}
	// Every path below that doesn't ask for a retry finishes the event, so it
	// cannot hold its ticket's lane.
	var in channel.Inbound
	if err = json.Unmarshal(raw, &in); err != nil {
		s.d.Log.Warn("support channel event is unreadable", "connection_id", c.ID)
		return s.finishInbound(ctx, a)
	}
	adapter, ok := s.registry.Get(c.Provider)
	if !ok {
		return s.finishInbound(ctx, a)
	}
	creds, err := s.credentials(c)
	if err != nil {
		return err
	}
	creds["channel_id"] = in.ExternalChannelID
	r := &reply{s: s, a: adapter, creds: creds, in: &in}
	if err = s.handleInbound(ctx, c, r); err != nil {
		return err
	}
	r.acknowledge(ctx)
	return s.finishInbound(ctx, a)
}

// handleInbound runs one normalized action. It returns nil once the action is
// answered, and an error only when it should run again.
func (s *Service) handleInbound(ctx context.Context, c connection, r *reply) error {
	in := r.in
	creds := r.creds
	if in.AccountID != "" && in.AccountID != c.ExternalAccountID {
		return nil
	}
	addressed(c, in)
	if in.Command == "link" {
		if err := s.linkIdentity(ctx, c, *in); err != nil {
			r.notice(ctx, "That link code is invalid or expired.")
			return nil
		}
		r.notice(ctx, "Your account is linked to opendesk.")
		return nil
	}
	var p *auth.Principal
	var err error
	if in.TicketNumber > 0 {
		// A command outside any thread names its ticket. Only a linked agent may
		// resolve a number, and it resolves to the ticket's mirrored thread on this
		// connection, so the command then runs exactly as it would in the thread.
		if p, err = s.externalPrincipal(ctx, c, in.ExternalUserID); err != nil {
			r.notice(ctx, "Link your opendesk account before using this command.")
			return nil
		}
		err = s.d.DB.Pool.QueryRow(ctx, `SELECT t.external_channel_id,t.external_thread_id FROM support_channel_threads t
			JOIN tickets k ON k.workspace_id=t.workspace_id AND k.id=t.ticket_id
			WHERE t.connection_id=$1 AND t.workspace_id=$2 AND k.number=$3 AND t.external_thread_id IS NOT NULL AND t.external_channel_id IS NOT NULL
			ORDER BY (t.external_channel_id=$4) DESC, t.updated_at DESC LIMIT 1`,
			c.ID, c.WorkspaceID, in.TicketNumber, in.ExternalChannelID).Scan(&in.ExternalChannelID, &in.ExternalThreadID)
		if db.IsNotFound(err) {
			r.notice(ctx, "That ticket is not available.")
			return nil
		}
		if err != nil {
			return err
		}
		creds["channel_id"] = in.ExternalChannelID
	}
	control := in.Command == "confirm" || in.Command == "cancel"
	var tid uuid.UUID
	var includeNotes bool
	var teamID *uuid.UUID
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT t.ticket_id,d.include_notes,d.team_id FROM support_channel_threads t JOIN support_channel_destinations d ON d.id=t.destination_id
		WHERE t.connection_id=$1 AND t.external_channel_id=$2 AND t.external_thread_id=$3`, c.ID, in.ExternalChannelID, in.ExternalThreadID).Scan(&tid, &includeNotes, &teamID)
	if db.IsNotFound(err) {
		creating := control || in.Command == "chat" && in.Directed
		if in.Command == "chat" && !in.Directed {
			// A reply to Ollie's question or preview needs no mention where the
			// provider delivers it (Telegram topics).
			if creating, err = s.pendingCreation(ctx, c.ID, *in); err != nil {
				return err
			}
		}
		if creating {
			err = s.d.DB.Pool.QueryRow(ctx, `SELECT team_id FROM support_channel_destinations
				WHERE connection_id=$1 AND workspace_id=$2 AND external_channel_id=$3 AND enabled`, c.ID, c.WorkspaceID, in.ExternalChannelID).Scan(&teamID)
			if err == nil && p == nil {
				if p, err = s.externalPrincipal(ctx, c, in.ExternalUserID); err != nil {
					r.notice(ctx, "Link your opendesk account before asking Ollie to create a ticket.")
					return nil
				}
			}
			if err == nil {
				if control {
					return s.answerControl(ctx, c, r, p, conversationTarget{TeamID: teamID})
				}
				answer, converseErr := s.converseCreate(ctx, p, c.ID, teamID, *in)
				if converseErr != nil {
					if errors.Is(converseErr, errConversationBusy) {
						return converseErr
					}
					if errors.Is(converseErr, errConversationUnavailable) {
						s.d.Log.Warn("support channel assistant failed", "connection_id", c.ID)
						r.notice(ctx, "Ollie couldn't answer just now. Try again later.")
						return nil
					}
					r.notice(ctx, publicError(converseErr))
					return nil
				}
				r.message(ctx, answer)
				return nil
			}
			if !db.IsNotFound(err) {
				return err
			}
		}
		switch {
		case control:
			r.notice(ctx, nothingWaiting)
		case in.ExternalThreadID == "":
			// Outside any thread (a slash command): the ticket number is the way in.
			r.notice(ctx, "Start with the ticket number, like 123 status solved, or write inside the ticket's thread.")
		case in.Command != "chat":
			r.notice(ctx, "Run ticket commands inside an opendesk ticket thread.")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if p == nil {
		if p, err = s.externalPrincipal(ctx, c, in.ExternalUserID); err != nil {
			r.notice(ctx, "Link your opendesk account before using this command.")
			return nil
		}
	}
	if control {
		return s.answerControl(ctx, c, r, p, conversationTarget{TicketID: &tid})
	}
	if in.Command == "chat" {
		answer, err := s.converse(ctx, p, c.ID, tid, includeNotes, *in)
		if err != nil {
			if errors.Is(err, errConversationBusy) {
				return err
			}
			if errors.Is(err, errConversationUnavailable) {
				s.d.Log.Warn("support channel assistant failed", "connection_id", c.ID, "ticket_id", tid)
				r.notice(ctx, "Ollie couldn't answer just now. Try again, or use an explicit ticket command.")
				return nil
			}
			r.notice(ctx, publicError(err))
			return nil
		}
		r.message(ctx, answer)
		return nil
	}
	fromCard := in.SourceMessageID != ""
	if fromCard && in.Command == "status" && strings.TrimSpace(in.Value) == domain.StatusClosed {
		// Closing from the card asks first. A typed command is deliberate and runs at once.
		if _, err = domain.LoadVisibleTicket(ctx, s.d.DB.Pool, p, tid); err != nil {
			r.notice(ctx, publicError(err))
			return nil
		}
		preview, ok, err := s.previewClose(ctx, p, c.ID, *in)
		if err != nil {
			if errors.Is(err, errConversationBusy) {
				return err
			}
			r.notice(ctx, publicError(err))
			return nil
		}
		if !ok {
			r.notice(ctx, preview.Text)
			return nil
		}
		r.message(ctx, preview)
		return nil
	}
	if err = s.applyCommand(ctx, p, tid, *in); err != nil {
		r.notice(ctx, publicError(err))
		return nil
	}
	if !fromCard {
		// A card's own edit is the answer to a control.
		r.post(ctx, "Done in opendesk.")
	}
	return nil
}

// answerControl settles a Confirm or Cancel button.
func (s *Service) answerControl(ctx context.Context, c connection, r *reply, p *auth.Principal, target conversationTarget) error {
	answer, outcome, err := s.settleControl(ctx, p, c.ID, target, *r.in)
	if err != nil {
		if errors.Is(err, errConversationBusy) {
			return err
		}
		r.notice(ctx, publicError(err))
		return nil
	}
	if outcome != pendingNotYours {
		r.clearActions(ctx)
	}
	if outcome == pendingSettled {
		r.message(ctx, answer)
		return nil
	}
	r.notice(ctx, answer.Text)
	return nil
}

// addressed marks a message that opens with the connection's own handle as
// directed at the bot, and drops the handle from its text.
func addressed(c connection, in *channel.Inbound) {
	if in.Command != "chat" || in.Mention == "" || !strings.EqualFold(in.Mention, c.ExternalAccountHandle) {
		return
	}
	in.Directed = true
	in.Text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.Text), in.Mention))
	in.Value = in.Text
}

// pendingCreation reports whether the sender has a ticket waiting for their
// confirm or cancel in this thread.
func (s *Service) pendingCreation(ctx context.Context, connectionID uuid.UUID, in channel.Inbound) (bool, error) {
	var ok bool
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_channel_conversations
		WHERE connection_id=$1 AND external_channel_id=$2 AND external_thread_id=$3 AND external_user_id=$4
		AND pending_action->>'command'='create_ticket' AND pending_expires_at>now())`,
		connectionID, in.ExternalChannelID, in.ExternalThreadID, in.ExternalUserID).Scan(&ok)
	return ok, err
}

func (s *Service) linkIdentity(ctx context.Context, c connection, in channel.Inbound) error {
	code := strings.ToUpper(strings.TrimSpace(in.Text))
	if code == "" || in.ExternalUserID == "" {
		return pgx.ErrNoRows
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var wid, uid uuid.UUID
		err := tx.QueryRow(ctx, `DELETE FROM support_channel_link_codes WHERE code_hash=$1 AND workspace_id=$2 AND expires_at>now() RETURNING workspace_id,user_id`, crypto.HashToken(code), c.WorkspaceID).Scan(&wid, &uid)
		if err != nil {
			return err
		}
		// One external account per person per connection, and one person per external
		// account: whatever this link replaces is unlinked, and audited as such.
		rows, err := tx.Query(ctx, `DELETE FROM support_channel_identities WHERE connection_id=$1 AND (user_id=$2 OR external_user_id=$3) RETURNING user_id,external_user_id`, c.ID, uid, in.ExternalUserID)
		if err != nil {
			return err
		}
		type identity struct {
			UserID   uuid.UUID
			External string
		}
		replaced, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (identity, error) {
			var x identity
			return x, r.Scan(&x.UserID, &x.External)
		})
		if err != nil {
			return err
		}
		// The person whose code was used acted; the audit entries name them.
		actor := &auth.Principal{Kind: auth.KindUser, UserID: uid, WorkspaceID: wid}
		for _, old := range replaced {
			if old.UserID == uid && old.External == in.ExternalUserID {
				continue
			}
			if err := audit.Log(ctx, tx, actor, wid, "support_channel.identity_unlinked", "support_channel", &c.ID,
				map[string]any{"provider": c.Provider, "external_user_id": old.External, "user_id": old.UserID}); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO support_channel_identities(id,workspace_id,connection_id,external_user_id,user_id) VALUES($1,$2,$3,$4,$5)`, ids.New(), wid, c.ID, in.ExternalUserID, uid); err != nil {
			return err
		}
		return audit.Log(ctx, tx, actor, wid, "support_channel.identity_linked", "support_channel", &c.ID,
			map[string]any{"provider": c.Provider, "external_user_id": in.ExternalUserID})
	})
}
func (s *Service) externalPrincipal(ctx context.Context, c connection, external string) (*auth.Principal, error) {
	var uid uuid.UUID
	var role string
	var us, ws bool
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT i.user_id,m.role,u.suspended_at IS NOT NULL,w.suspended_at IS NOT NULL FROM support_channel_identities i JOIN members m ON m.workspace_id=i.workspace_id AND m.user_id=i.user_id JOIN users u ON u.id=i.user_id JOIN workspaces w ON w.id=i.workspace_id WHERE i.connection_id=$1 AND i.external_user_id=$2`, c.ID, external).Scan(&uid, &role, &us, &ws)
	if err != nil {
		return nil, err
	}
	r := auth.Role(role)
	if us || ws || !r.AtLeast(auth.RoleAgent) {
		return nil, auth.ErrForbidden
	}
	return &auth.Principal{Kind: auth.KindUser, UserID: uid, WorkspaceID: c.WorkspaceID, Role: r}, nil
}

func (s *Service) applyCommand(ctx context.Context, p *auth.Principal, tid uuid.UUID, in channel.Inbound) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		return s.applyCommandTx(ctx, tx, p, tid, in)
	})
}

func (s *Service) applyCommandTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, tid uuid.UUID, in channel.Inbound) error {
	if _, err := domain.LoadVisibleTicket(ctx, tx, p, tid); err != nil {
		return err
	}
	switch in.Command {
	case "reply", "note":
		kind := in.Command
		if strings.TrimSpace(in.Text) == "" {
			return httpx.Validation("Message text is required.")
		}
		body := "<p>" + strings.ReplaceAll(html.EscapeString(in.Text), "\n", "<br>") + "</p>"
		_, err := tickets.PostMessage(ctx, tx, s.d, p, tid, tickets.MessageInput{Kind: kind, BodyHTML: body})
		return err
	case "assign", "assign_self":
		_, err := tickets.ApplyPatch(ctx, tx, s.d, p, tid, tickets.Patch{Assignee: tickets.Opt[uuid.UUID]{Set: true, Value: p.UserID}})
		return err
	case "status":
		v := strings.TrimSpace(in.Value)
		_, err := tickets.ApplyPatch(ctx, tx, s.d, p, tid, tickets.Patch{Status: &v})
		return err
	case "priority":
		v := strings.TrimSpace(in.Value)
		_, err := tickets.ApplyPatch(ctx, tx, s.d, p, tid, tickets.Patch{Priority: &v})
		return err
	default:
		return httpx.Validation("Unknown command. Use reply, note, assign, status or priority.")
	}
}

func (s *Service) finishInbound(ctx context.Context, a jobs.SupportChannelInboundArgs) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var ticketID *uuid.UUID
		var seq *int64
		err := tx.QueryRow(ctx, `UPDATE support_channel_inbound_events SET processed_at=COALESCE(processed_at,now())
			WHERE connection_id=$1 AND external_event_id=$2 RETURNING ticket_id,ticket_command_seq`,
			a.ConnectionID, a.ExternalEventID).Scan(&ticketID, &seq)
		if db.IsNotFound(err) || err == nil && (ticketID == nil || seq == nil) {
			return nil
		}
		if err != nil {
			return err
		}
		// The lane moves past this command. One the lane already passed leaves
		// the head, and whoever processes it, as they are.
		_, err = tx.Exec(ctx, `UPDATE support_channel_ticket_lanes SET
			next_process_seq=GREATEST(next_process_seq,$2+1),
			processing_seq=CASE WHEN processing_seq=$2 THEN NULL ELSE processing_seq END,
			processing_expires_at=CASE WHEN processing_seq=$2 THEN NULL ELSE processing_expires_at END,
			updated_at=now()
			WHERE ticket_id=$1`, *ticketID, *seq)
		return err
	})
}

// ticketLaneWait bounds how long an unfinished command holds its ticket's
// lane. A job River discarded, or one whose provider is gone, never finishes.
const ticketLaneWait = 10 * time.Minute

// claimTicketCommand lets a ticket's commands run in the order their callbacks
// were committed. The head is the first earlier command that is unprocessed,
// still exists and is younger than ticketLaneWait; the lane advances past any
// other, so a deleted, discarded or abandoned event cannot stall the ticket.
// It reports false, with no error, when the command runs without the lane: the
// ticket has none, or the lane already passed this command.
func (s *Service) claimTicketCommand(ctx context.Context, ticketID uuid.UUID, seq int64) (bool, error) {
	var claimed, busy bool
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var next int64
		var processing *int64
		var leased bool
		err := tx.QueryRow(ctx, `SELECT next_process_seq,processing_seq,COALESCE(processing_expires_at>now(),false)
			FROM support_channel_ticket_lanes WHERE ticket_id=$1 FOR UPDATE`, ticketID).Scan(&next, &processing, &leased)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if seq < next {
			return nil
		}
		var head *int64
		if err = tx.QueryRow(ctx, `SELECT min(ticket_command_seq) FROM support_channel_inbound_events
			WHERE ticket_id=$1 AND ticket_command_seq>=$2 AND ticket_command_seq<$3
			AND processed_at IS NULL AND received_at>now()-make_interval(secs=>$4)`,
			ticketID, next, seq, ticketLaneWait.Seconds()).Scan(&head); err != nil {
			return err
		}
		if head != nil {
			busy = true
			if *head > next {
				_, err = tx.Exec(ctx, `UPDATE support_channel_ticket_lanes SET next_process_seq=$2,updated_at=now() WHERE ticket_id=$1`, ticketID, *head)
			}
			return err
		}
		if processing != nil && *processing == seq && leased {
			busy = true
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE support_channel_ticket_lanes SET next_process_seq=$2,processing_seq=$2,
			processing_expires_at=now()+interval '6 minutes',updated_at=now() WHERE ticket_id=$1`, ticketID, seq)
		claimed = err == nil
		return err
	})
	if err == nil && busy {
		err = errTicketCommandBusy
	}
	return claimed, err
}

func (s *Service) releaseTicketCommand(ticketID uuid.UUID, seq int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_ticket_lanes SET processing_seq=NULL,processing_expires_at=NULL,updated_at=now()
		WHERE ticket_id=$1 AND processing_seq=$2`, ticketID, seq)
}

// reply is how an inbound action is answered: in its thread for everyone, or
// privately to its sender where the provider allows it.
type reply struct {
	s        *Service
	a        channel.Adapter
	creds    map[string]string
	in       *channel.Inbound
	answered bool
}

// post writes text in the action's thread (or its channel, outside a thread).
func (r *reply) post(ctx context.Context, text string) {
	_ = r.a.Post(ctx, r.s.http, r.creds, r.in.ExternalThreadID, text)
}

// message posts text with its controls where the provider has them.
func (r *reply) message(ctx context.Context, m channel.Message) {
	interactive, ok := r.a.(channel.InteractiveAdapter)
	if !ok || len(m.Actions) == 0 {
		r.post(ctx, m.Text)
		return
	}
	_, _ = interactive.PostInteractive(ctx, r.s.http, r.creds, r.in.ExternalThreadID, m)
}

// notice tells the sender something only they need, such as an error or that
// their account isn't linked: privately where the provider allows, otherwise
// in the thread.
func (r *reply) notice(ctx context.Context, text string) {
	if responder, ok := r.a.(channel.Responder); ok && r.in.Respond != "" && !r.answered {
		r.answered = true
		if responder.Respond(ctx, r.s.http, r.creds, r.in.Respond, text) == nil {
			return
		}
	}
	r.post(ctx, text)
}

// acknowledge answers an action that got no private answer, for providers that
// wait for one (a Telegram button keeps loading until then).
func (r *reply) acknowledge(ctx context.Context) {
	if responder, ok := r.a.(channel.Responder); ok && r.in.Respond != "" && !r.answered {
		r.answered = true
		_ = responder.Respond(ctx, r.s.http, r.creds, r.in.Respond, "")
	}
}

// clearActions removes the controls from the message whose button was used.
func (r *reply) clearActions(ctx context.Context) {
	if interactive, ok := r.a.(channel.InteractiveAdapter); ok && r.in.SourceMessageID != "" {
		_ = interactive.ClearActions(ctx, r.s.http, r.creds, *r.in)
	}
}

// deliveryError is a connection's error after a failed delivery. Each path clears
// only its own error: a delivery doesn't hide that the provider refused the bot.
const deliveryError = "Couldn't deliver to the provider."

func (s *Service) connectionError(ctx context.Context, id uuid.UUID, cause error) error {
	_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET status='error',last_error=$2,updated_at=now() WHERE id=$1 AND (status<>'error' OR last_error=$2)`, id, deliveryError)
	return fmt.Errorf("support channel delivery: %w", cause)
}
func publicError(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Detail
	}
	if db.IsNotFound(err) {
		return "That ticket is not available."
	}
	return "Couldn't apply that change in opendesk."
}
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
