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

func (w *inboundWorker) Timeout(*river.Job[jobs.SupportChannelInboundArgs]) time.Duration {
	return 5 * time.Minute
}
func (w *inboundWorker) Work(ctx context.Context, j *river.Job[jobs.SupportChannelInboundArgs]) error {
	return w.s.processInbound(ctx, j.Args)
}

type deliveryTarget struct {
	destination
	Provider       string
	CredentialsEnc []byte
}

func (s *Service) dispatch(ctx context.Context, a jobs.SupportChannelDispatchArgs) error {
	var typ string
	var data []byte
	var privateTeam, ticketID *uuid.UUID
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT type,data,team_id,COALESCE(NULLIF(data->>'ticket_id','')::uuid,NULLIF(data->'ticket'->>'id','')::uuid,NULLIF(data->'message'->>'ticket_id','')::uuid) FROM events WHERE workspace_id=$1 AND seq=$2`, a.WorkspaceID, a.EventSeq).Scan(&typ, &data, &privateTeam, &ticketID)
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
		if err := s.dispatchTo(ctx, a, typ, data, t, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) dispatchTo(ctx context.Context, a jobs.SupportChannelDispatchArgs, typ string, data []byte, t domain.Ticket, d deliveryTarget) error {
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
	var last int64
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT COALESCE(external_thread_id,''),COALESCE(external_root_message_id,''),last_event_seq FROM support_channel_threads WHERE destination_id=$1 AND ticket_id=$2`, d.ID, t.ID).Scan(&thread.ID, &thread.RootID, &last)
	if db.IsNotFound(err) {
		claim, claimErr := s.d.DB.Pool.Exec(ctx, `INSERT INTO support_channel_threads(id,workspace_id,connection_id,destination_id,ticket_id,last_event_seq) VALUES($1,$2,$3,$4,$5,0) ON CONFLICT(destination_id,ticket_id) DO NOTHING`, ids.New(), a.WorkspaceID, d.ConnectionID, d.ID, t.ID)
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
		_, err = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_threads SET external_channel_id=$3,external_thread_id=$4,external_root_message_id=$5,updated_at=now() WHERE destination_id=$1 AND ticket_id=$2`, d.ID, t.ID, inbound, thread.ID, thread.RootID)
		if err != nil {
			return err
		}
		last = 0
	} else if err != nil {
		return err
	}
	if thread.ID == "" {
		return errors.New("support channel thread is being created")
	}
	switch typ {
	case "message.created":
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
			if err = adapter.Update(ctx, s.http, creds, thread.RootID, ct); err != nil {
				return s.connectionError(ctx, d.ConnectionID, err)
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
		return nil
	}
	if err != nil {
		return err
	}
	var raw []byte
	var done *time.Time
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT payload,processed_at FROM support_channel_inbound_events WHERE connection_id=$1 AND external_event_id=$2`, a.ConnectionID, a.ExternalEventID).Scan(&raw, &done)
	if db.IsNotFound(err) || done != nil {
		return nil
	}
	if err != nil {
		return err
	}
	var in channel.Inbound
	if err = json.Unmarshal(raw, &in); err != nil {
		return err
	}
	adapter, ok := s.registry.Get(c.Provider)
	if !ok {
		return nil
	}
	creds, err := s.credentials(c)
	if err != nil {
		return err
	}
	creds["channel_id"] = in.ExternalChannelID
	if in.AccountID != "" && in.AccountID != c.ExternalAccountID {
		return s.finishInbound(ctx, a)
	}
	if in.Command == "link" {
		err = s.linkIdentity(ctx, c, in)
		if err == nil {
			s.replyToProvider(ctx, adapter, creds, in, "Your account is linked to opendesk.")
		}
		if err != nil {
			s.replyToProvider(ctx, adapter, creds, in, "That link code is invalid or expired.")
		}
		return s.finishInbound(ctx, a)
	}
	var p *auth.Principal
	if in.TicketNumber > 0 {
		// A command outside any thread names its ticket. Only a linked agent may
		// resolve a number, and it resolves to the ticket's mirrored thread on this
		// connection, so the command then runs exactly as it would in the thread.
		if p, err = s.externalPrincipal(ctx, c, in.ExternalUserID); err != nil {
			s.replyToProvider(ctx, adapter, creds, in, "Link your opendesk account before using this command.")
			return s.finishInbound(ctx, a)
		}
		err = s.d.DB.Pool.QueryRow(ctx, `SELECT t.external_channel_id,t.external_thread_id FROM support_channel_threads t
			JOIN tickets k ON k.workspace_id=t.workspace_id AND k.id=t.ticket_id
			WHERE t.connection_id=$1 AND t.workspace_id=$2 AND k.number=$3 AND t.external_thread_id IS NOT NULL AND t.external_channel_id IS NOT NULL
			ORDER BY (t.external_channel_id=$4) DESC, t.updated_at DESC LIMIT 1`,
			c.ID, c.WorkspaceID, in.TicketNumber, in.ExternalChannelID).Scan(&in.ExternalChannelID, &in.ExternalThreadID)
		if db.IsNotFound(err) {
			s.replyToProvider(ctx, adapter, creds, in, "That ticket is not available.")
			return s.finishInbound(ctx, a)
		}
		if err != nil {
			return err
		}
		creds["channel_id"] = in.ExternalChannelID
	}
	var tid uuid.UUID
	var includeNotes bool
	var teamID *uuid.UUID
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT t.ticket_id,d.include_notes,d.team_id FROM support_channel_threads t JOIN support_channel_destinations d ON d.id=t.destination_id
		WHERE t.connection_id=$1 AND t.external_channel_id=$2 AND t.external_thread_id=$3`, c.ID, in.ExternalChannelID, in.ExternalThreadID).Scan(&tid, &includeNotes, &teamID)
	if db.IsNotFound(err) {
		if in.Command == "chat" && in.Directed {
			if p == nil {
				if p, err = s.externalPrincipal(ctx, c, in.ExternalUserID); err != nil {
					s.replyToProvider(ctx, adapter, creds, in, "Link your opendesk account before asking Ollie to create a ticket.")
					return s.finishInbound(ctx, a)
				}
			}
			err = s.d.DB.Pool.QueryRow(ctx, `SELECT team_id FROM support_channel_destinations
				WHERE connection_id=$1 AND workspace_id=$2 AND external_channel_id=$3 AND enabled`, c.ID, c.WorkspaceID, in.ExternalChannelID).Scan(&teamID)
			if err == nil {
				answer, converseErr := s.converseCreate(ctx, p, c.ID, teamID, in)
				if converseErr != nil {
					if errors.Is(converseErr, errConversationBusy) {
						return converseErr
					}
					if errors.Is(converseErr, errConversationUnavailable) {
						s.d.Log.Warn("support channel assistant failed", "connection_id", c.ID)
						s.replyToProvider(ctx, adapter, creds, in, "Ollie couldn't answer just now. Try again later.")
						return s.finishInbound(ctx, a)
					}
					s.replyToProvider(ctx, adapter, creds, in, publicError(converseErr))
					return s.finishInbound(ctx, a)
				}
				s.replyToProvider(ctx, adapter, creds, in, answer)
				return s.finishInbound(ctx, a)
			}
			if !db.IsNotFound(err) {
				return err
			}
		}
		switch {
		case in.ExternalThreadID == "":
			// Outside any thread (a slash command): the ticket number is the way in.
			s.replyToProvider(ctx, adapter, creds, in, "Start with the ticket number, like 123 status solved, or write inside the ticket's thread.")
		case in.Command != "chat":
			s.replyToProvider(ctx, adapter, creds, in, "Run ticket commands inside an opendesk ticket thread.")
		}
		return s.finishInbound(ctx, a)
	}
	if err != nil {
		return err
	}
	if p == nil {
		if p, err = s.externalPrincipal(ctx, c, in.ExternalUserID); err != nil {
			s.replyToProvider(ctx, adapter, creds, in, "Link your opendesk account before using this command.")
			return s.finishInbound(ctx, a)
		}
	}
	if in.Command == "chat" {
		answer, err := s.converse(ctx, p, c.ID, tid, includeNotes, in)
		if err != nil {
			if errors.Is(err, errConversationBusy) {
				return err
			}
			if errors.Is(err, errConversationUnavailable) {
				s.d.Log.Warn("support channel assistant failed", "connection_id", c.ID, "ticket_id", tid)
				s.replyToProvider(ctx, adapter, creds, in, "Ollie couldn't answer just now. Try again, or use an explicit ticket command.")
				return s.finishInbound(ctx, a)
			}
			s.replyToProvider(ctx, adapter, creds, in, publicError(err))
			return s.finishInbound(ctx, a)
		}
		s.replyToProvider(ctx, adapter, creds, in, answer)
		return s.finishInbound(ctx, a)
	}
	err = s.applyCommand(ctx, p, tid, in)
	if err != nil {
		s.replyToProvider(ctx, adapter, creds, in, publicError(err))
		return s.finishInbound(ctx, a)
	}
	s.replyToProvider(ctx, adapter, creds, in, "Done in opendesk.")
	return s.finishInbound(ctx, a)
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
	_, err := s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_inbound_events SET processed_at=now() WHERE connection_id=$1 AND external_event_id=$2`, a.ConnectionID, a.ExternalEventID)
	return err
}
func (s *Service) replyToProvider(ctx context.Context, a channel.Adapter, cr map[string]string, in channel.Inbound, text string) {
	// An empty thread posts to the channel itself (cr["channel_id"]).
	_ = a.Post(ctx, s.http, cr, in.ExternalThreadID, text)
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
