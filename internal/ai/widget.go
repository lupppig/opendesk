package ai

import (
	"context"
	"errors"
	"html"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

// The widget agent's job side (contracts/agent.md): when it answers, posting, handoff,
// visitor actions and the monthly cap. The model loop is in agent.go.

func isNoBalance(err error) bool { return errors.Is(err, plan.ErrNoBalance) }

// wantsPerson spots a request for a human in common languages without a model call.
var wantsPerson = regexp.MustCompile(`(?i)\b(human|real person|a person|live agent|an agent|representative|operator|someone real|talk to (?:someone|support|a person)|speak (?:to|with) (?:someone|a person|an agent)|mensch|mitarbeiter|humain|une personne|conseiller|humano|una persona|persona real|agente|operatore|atendente|uma pessoa|medewerker|człowiek|konsultant)\b`)

// Agent features: what the monthly agent cap counts.
var agentFeatures = []string{"answer", "answer_check", "answer_email"}

const (
	// needsHumanAt is the Jev confidence at which the visitor is handed to the team.
	needsHumanAt = 0.8
	actionTTL    = 15 * time.Minute
	// NotifyAICapReached is sent once a month when the agent's monthly cap stops it.
	NotifyAICapReached = "ai_cap_reached"
)

var errStale = errors.New("conversation changed")

// WidgetAnswerWorker runs ai.widget_answer jobs.
type WidgetAnswerWorker struct {
	river.WorkerDefaults[jobs.AIWidgetAnswerArgs]
	S *Service
}

func (w *WidgetAnswerWorker) Work(ctx context.Context, job *river.Job[jobs.AIWidgetAnswerArgs]) error {
	err := w.S.WidgetAnswer(ctx, job.Args)
	if err != nil && skippable(err) {
		w.S.d.Log.Info("widget answer skipped", "ticket_id", job.Args.TicketID, "reason", errReason(err))
		return nil
	}
	return err
}

// WidgetActionWorker runs ai.widget_action jobs: a visitor confirmed or declined an
// action, so the tool runs (or not) and the agent continues.
type WidgetActionWorker struct {
	river.WorkerDefaults[jobs.AIWidgetActionArgs]
	S *Service
}

func (w *WidgetActionWorker) Timeout(*river.Job[jobs.AIWidgetActionArgs]) time.Duration {
	return 3 * time.Minute
}

func (w *WidgetActionWorker) Work(ctx context.Context, job *river.Job[jobs.AIWidgetActionArgs]) error {
	err := w.S.WidgetAction(ctx, job.Args)
	if err != nil && skippable(err) {
		w.S.d.Log.Info("widget action reply skipped", "ticket_id", job.Args.TicketID, "reason", errReason(err))
		return nil
	}
	return err
}

// agentConv is a conversation the agent may reply in, with what a reply needs.
type agentConv struct {
	tc      *ticketCtx
	set     hd.AISettings
	bot     bots.Bot
	visitor AgentVisitor
	texts   []string // visitor messages, oldest first
	burst   int      // visitor messages since the last reply
	replies int      // agent replies so far
}

// loadAgentConv loads a chat conversation and reports nil when the agent must not reply:
// AI or the agent off, not an open unassigned chat, handed off, or a team member replied.
func (s *Service) loadAgentConv(ctx context.Context, wid, tid uuid.UUID) (*agentConv, error) {
	if !s.Enabled() {
		return nil, nil
	}
	st, err := hd.LoadSettings(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return nil, err
	}
	set := st.AI()
	if !set.Enabled || !set.WidgetAnswers {
		return nil, nil
	}
	tc, err := s.loadTicketCtx(ctx, s.d.DB.Pool, wid, tid, true)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	t := tc.T
	if t.Channel != domain.ChannelChat || t.Spam || t.Assignee != nil || t.Status == domain.StatusSolved || t.Status == domain.StatusClosed {
		return nil, nil
	}
	if v, _ := t.Custom["ai_handoff"].(bool); v {
		return nil, nil
	}
	bot, err := bots.ForWidget(ctx, s.d.DB.Pool, wid, t.WidgetID)
	if err != nil {
		return nil, err
	}
	ac := &agentConv{tc: tc, set: set, bot: bot}
	for _, m := range tc.Msgs {
		switch {
		case m.Author.Type == "user":
			return nil, nil // a person on the team is in the conversation
		case m.Direction == "inbound":
			ac.texts = append(ac.texts, messageText(m))
			ac.burst++
		case m.AI:
			ac.replies++
			ac.burst = 0
		}
	}
	if t.Contact != nil {
		ac.visitor.ContactID = t.Contact.ID
		var external *string
		var verified bool
		// Verified only by a session of the conversation's own widget, which checked user_hash
		// with that widget's secret (contracts/teams.md "Widget identity").
		if err := s.d.DB.Pool.QueryRow(ctx, `SELECT c.external_id, EXISTS (SELECT 1 FROM widget_sessions s
			WHERE s.workspace_id = c.workspace_id AND s.contact_id = c.id AND s.verified
			AND s.widget_id IS NOT DISTINCT FROM (SELECT k.widget_id FROM tickets k WHERE k.workspace_id = $1 AND k.id = $3))
			FROM contacts c WHERE c.workspace_id = $1 AND c.id = $2`, wid, t.Contact.ID, tid).Scan(&external, &verified); err != nil && !db.IsNotFound(err) {
			return nil, err
		}
		if verified && external != nil {
			ac.visitor.Verified, ac.visitor.UserID = true, *external
		}
		if tc.Contact != nil {
			ac.visitor.Name = tc.Contact.Name
			if tc.Contact.Email != nil && ac.visitor.Verified {
				ac.visitor.Email = *tc.Contact.Email
			}
		}
	}
	ac.visitor.ConversationID = tid
	return ac, nil
}

// input builds the loop's input for this conversation.
func (ac *agentConv) input(wid, tid uuid.UUID, pageURL string) agentInput {
	msgs := ac.tc.Msgs
	if len(msgs) > agentThreadMsgs {
		msgs = msgs[len(msgs)-agentThreadMsgs:]
	}
	tidCopy := tid
	return agentInput{wid: wid, ticketID: &tidCopy, brandID: ticketBrand(ac.tc.T), feature: "answer", set: ac.set, bot: ac.bot,
		company: ac.tc.Workspace.Name, thread: renderThread(msgs, "", agentThreadChars),
		query: agentQuery(ac.texts, ac.burst), pageURL: pageURL, visitor: ac.visitor}
}

// WidgetAnswer replies to the visitor's latest messages, hands off, or does nothing.
// It replies at most once per message and only while the message is the latest.
func (s *Service) WidgetAnswer(ctx context.Context, a jobs.AIWidgetAnswerArgs) error {
	var handled bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_widget_answers WHERE message_id = $1)`, a.MessageID).Scan(&handled); err != nil || handled {
		return err
	}
	ac, err := s.loadAgentConv(ctx, a.WorkspaceID, a.TicketID)
	if err != nil || ac == nil {
		return err
	}
	msgs := ac.tc.Msgs
	if len(msgs) == 0 {
		return nil
	}
	last := msgs[len(msgs)-1]
	if last.ID != a.MessageID || last.Direction != "inbound" {
		return nil // the visitor wrote again (that job replies) or someone replied
	}
	if ac.replies >= max(ac.set.WidgetMaxReplies, 1) {
		return s.handoff(ctx, a.WorkspaceID, a.TicketID, a.MessageID, handoffBot(ac.bot))
	}
	if wantsPerson.MatchString(messageText(last)) {
		return s.handoff(ctx, a.WorkspaceID, a.TicketID, a.MessageID, handoffBot(ac.bot))
	}
	if capped, err := s.agentCapped(ctx, a.WorkspaceID, ac.set); err != nil || capped {
		if capped {
			return s.decline(ctx, a.WorkspaceID, a.TicketID, a.MessageID)
		}
		return err
	}
	in := ac.input(a.WorkspaceID, a.TicketID, a.PageURL)
	if s.jevFor(ctx, a.WorkspaceID) {
		human, err := s.needsHuman(ctx, a.WorkspaceID, a.TicketID, msgs)
		if err != nil && !skippable(err) {
			return err
		}
		in.needsHuman = human
	}
	return s.replyAndPost(ctx, in, a.MessageID)
}

// WidgetAction runs a confirmed action's tool, records the outcome and lets the agent
// continue; a declined action only continues.
func (s *Service) WidgetAction(ctx context.Context, a jobs.AIWidgetActionArgs) error {
	var (
		serverID  uuid.UUID
		messageID *uuid.UUID
		tool      string
		title     string
		sealed    []byte
		status    string
	)
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT server_id, message_id, tool, title, args, status FROM ai_widget_actions
		WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`, a.WorkspaceID, a.TicketID, a.ActionID).
		Scan(&serverID, &messageID, &tool, &title, &sealed, &status)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	ac, err := s.loadAgentConv(ctx, a.WorkspaceID, a.TicketID)
	if err != nil {
		if a.Confirmed && status == "running" {
			s.finishAction(ctx, a, tool, "failed")
		}
		return err
	}
	result := ""
	switch {
	case a.Confirmed && status == "running":
		final, out := "failed", "The action could not be completed."
		var args []byte
		if ac == nil {
			out = "The conversation was handed to the team before the action ran."
		} else if args, err = s.d.Box.Open(sealed); err != nil {
			s.d.Log.Warn("widget action arguments unreadable", "action_id", a.ActionID)
		} else if tb := s.agentToolbox(); tb != nil {
			in := ac.input(a.WorkspaceID, a.TicketID, "")
			text, isErr := s.callAgentTool(ctx, in, AgentTool{ServerID: serverID, Name: tool, Title: title}, args)
			out = text
			if !isErr {
				final = "confirmed"
			}
		}
		s.finishAction(ctx, a, tool, final)
		result = block("action_result", []string{"tool", attr(title), "status", final}, untrusted(clip(out, agentToolOutChars)))
	case !a.Confirmed && status == "declined":
		s.finishAction(ctx, a, tool, "declined")
		result = block("action_result", []string{"tool", attr(title), "status", "declined"}, "The visitor declined; nothing was done.")
	default:
		return nil
	}
	if ac == nil || messageID == nil {
		return nil
	}
	if capped, err := s.agentCapped(ctx, a.WorkspaceID, ac.set); err != nil || capped {
		return err
	}
	in := ac.input(a.WorkspaceID, a.TicketID, "")
	in.extra = result
	return s.replyAndPost(ctx, in, *messageID)
}

// finishAction records a decided action: its final status, activity and audit entry.
func (s *Service) finishAction(ctx context.Context, a jobs.AIWidgetActionArgs, tool, status string) {
	ctx = context.WithoutCancel(ctx)
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if status != "declined" {
			if _, err := hd.SetAIActionStatus(ctx, tx, a.WorkspaceID, a.TicketID, a.ActionID, status, "running"); err != nil {
				return err
			}
		}
		data := map[string]any{"action_id": a.ActionID.String(), "tool": tool, "status": status}
		if _, err := domain.LogActivity(ctx, tx, a.WorkspaceID, a.TicketID, "ai", nil, "ai_action", data); err != nil {
			return err
		}
		return audit.Log(ctx, tx, nil, a.WorkspaceID, "ai.action", "ticket", &a.TicketID, map[string]any{"tool": tool, "status": status})
	})
	if err != nil {
		s.d.Log.Warn("record widget action", "action_id", a.ActionID, "err", err)
	}
}

// liveStream publishes a reply's live events, batching deltas.
type liveStream struct {
	s       *Service
	ev      events.LiveEvent
	replyID uuid.UUID
	mu      sync.Mutex
	buf     strings.Builder
	last    time.Time
}

// newLiveStream marks the stream with the ticket's private team, so the gateway shows the
// preview only to agents who may see the ticket.
func (s *Service) newLiveStream(ctx context.Context, wid, tid, contactID uuid.UUID) (*liveStream, error) {
	team, err := events.PrivateTeam(ctx, s.d.DB.Pool, wid, tid)
	if err != nil {
		return nil, err
	}
	// The brand keeps the preview off the visitor's widgets of other brands.
	var brand uuid.UUID
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT brand_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, tid).Scan(&brand); err != nil {
		return nil, err
	}
	return &liveStream{s: s, replyID: uuid.New(), ev: events.LiveEvent{WorkspaceID: wid, TicketID: tid, ContactID: contactID, TeamID: team,
		BrandID: &brand}}, nil
}

func (l *liveStream) publish(typ string, data map[string]any) {
	if l.s.d.NATS == nil {
		return
	}
	data["reply_id"], data["ticket_id"] = l.replyID, l.ev.TicketID
	ev := l.ev
	ev.Type, ev.Data = typ, data
	events.PublishLive(l.s.d.NATS.Conn, ev)
}

func (l *liveStream) delta(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(text)
	if l.buf.Len() >= 48 || time.Since(l.last) >= 80*time.Millisecond {
		l.flushLocked()
	}
}

func (l *liveStream) flushLocked() {
	if l.buf.Len() == 0 {
		return
	}
	l.publish("ai.reply.delta", map[string]any{"text": l.buf.String()})
	l.buf.Reset()
	l.last = time.Now()
}

func (l *liveStream) tool(name, status, _ string) {
	l.mu.Lock()
	l.flushLocked()
	l.mu.Unlock()
	l.publish("ai.reply.tool", map[string]any{"tool": name, "status": status})
}

func (l *liveStream) end(messageID, actionID *uuid.UUID, handoff bool) {
	l.mu.Lock()
	l.flushLocked()
	l.mu.Unlock()
	l.publish("ai.reply.ended", map[string]any{"message_id": messageID, "handoff": handoff, "action_id": actionID})
}

// replyAndPost runs the loop with live events and posts the reply if nothing changed
// since trigger (the message replied to) was the latest message.
func (s *Service) replyAndPost(ctx context.Context, in agentInput, trigger uuid.UUID) error {
	live, err := s.newLiveStream(ctx, in.wid, *in.ticketID, in.visitor.ContactID)
	if err != nil {
		return err
	}
	live.publish("ai.reply.started", map[string]any{"bot": map[string]any{"id": in.bot.IDPtr(), "name": in.bot.Name,
		"look": in.bot.Look, "avatar_url": in.bot.AvatarURL}})
	res, err := s.runAgent(ctx, in, agentEvents{delta: live.delta, tool: live.tool})
	if err != nil {
		live.end(nil, nil, false)
		return err
	}
	messageID, actionID, err := s.postReply(ctx, in, trigger, res)
	if errors.Is(err, errStale) {
		live.end(nil, nil, false)
		return nil
	}
	if err != nil {
		live.end(nil, nil, false)
		return err
	}
	live.end(messageID, actionID, res.Handoff)
	return nil
}

// postReply posts the agent's reply under the ticket lock, with its action card and the
// handoff when the agent asked for one. errStale means the conversation moved on.
func (s *Service) postReply(ctx context.Context, in agentInput, trigger uuid.UUID, res agentResult) (*uuid.UUID, *uuid.UUID, error) {
	wid, tid := in.wid, *in.ticketID
	body, cited := finishReply(res.Text, res.Passages)
	if body == "" && res.Action != nil && res.Action.Summary != "" {
		body = hd.SanitizeRich("<p>" + html.EscapeString(res.Action.Summary) + "</p>")
	}
	var messageID, actionID *uuid.UUID
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// Re-check under the ticket lock that nothing changed while the model ran.
		var assignee *uuid.UUID
		var custom map[string]any
		var subject string
		if err := tx.QueryRow(ctx, `SELECT assignee_id, custom, subject FROM tickets WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`,
			wid, tid).Scan(&assignee, &custom, &subject); err != nil {
			return err
		}
		var latest uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM messages WHERE workspace_id = $1 AND ticket_id = $2 AND kind = 'reply' AND deleted_at IS NULL
			ORDER BY created_at DESC, id DESC LIMIT 1`, wid, tid).Scan(&latest); err != nil {
			return err
		}
		if v, _ := custom["ai_handoff"].(bool); v || assignee != nil || latest != trigger {
			return errStale
		}
		articleIDs := []uuid.UUID{}
		for _, p := range cited {
			if id, err := uuid.Parse(p.ID); err == nil && p.Kind == "article" {
				articleIDs = append(articleIDs, id)
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO ai_widget_answers (message_id, workspace_id, ticket_id, answered, handoff, article_ids)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`, trigger, wid, tid, body != "", res.Handoff, articleIDs)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errStale
		}
		if body != "" {
			body := s.fillAnswer(ctx, tx, wid, tid, in.bot.Name, body)
			nm := domain.NewMessage{WorkspaceID: wid, TicketID: tid, Kind: "reply", Direction: "outbound", AuthorType: "system",
				BodyHTML: body, BodyText: hd.HTMLToText(body), Subject: subject, Trigger: "none", AI: true, FromName: in.bot.Name, BotID: in.bot.IDPtr()}
			var aid uuid.UUID
			if res.Action != nil {
				aid = uuid.New()
				nm.AIAction = &domain.AIAction{ID: aid, Title: clip(firstNonEmpty(res.Action.Tool.Title, res.Action.Tool.displayName()), 120),
					Summary: res.Action.Summary, Status: "pending", Tool: res.Action.Tool.displayName()}
			}
			m, err := domain.AddMessage(ctx, tx, s.d.Jobs, nm)
			if err != nil {
				return err
			}
			messageID = &m.ID
			if res.Action != nil {
				sealed, err := s.d.Box.Seal(res.Action.Args)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO ai_widget_actions (id, workspace_id, ticket_id, message_id, server_id, tool, title, summary, args, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, aid, wid, tid, m.ID, res.Action.Tool.ServerID, res.Action.Tool.Name,
					nm.AIAction.Title, res.Action.Summary, sealed, time.Now().Add(actionTTL)); err != nil {
					return err
				}
				actionID = &aid
			}
			if _, err := tx.Exec(ctx, `UPDATE ai_widget_answers SET reply_id = $2 WHERE message_id = $1`, trigger, m.ID); err != nil {
				return err
			}
			tools := res.ToolsUsed
			if tools == nil {
				tools = []string{}
			}
			if _, err := domain.LogActivity(ctx, tx, wid, tid, "ai", nil, "ai_answered",
				map[string]any{"message_id": m.ID.String(), "sources": toSources(cited), "tools": tools}); err != nil {
				return err
			}
		}
		if res.Handoff {
			if _, err := hd.MarkAIHandoff(ctx, tx, wid, tid, "ai", handoffBot(in.bot)); err != nil && !db.IsNotFound(err) {
				return err
			}
		}
		return nil
	})
	return messageID, actionID, err
}

// decline records that the agent did not reply to this message; the team handles it.
func (s *Service) decline(ctx context.Context, wid, tid, mid uuid.UUID) error {
	_, err := s.d.DB.Pool.Exec(ctx, `INSERT INTO ai_widget_answers (message_id, workspace_id, ticket_id, answered) VALUES ($1, $2, $3, false)
		ON CONFLICT DO NOTHING`, mid, wid, tid)
	return err
}

// handoffBot is the bot as a handoff records it.
func handoffBot(b bots.Bot) hd.HandoffBot { return hd.HandoffBot{ID: b.IDPtr(), Name: b.Name} }

// handoff stops the agent for this conversation and alerts the team, without a reply.
// bot is the bot handing over.
func (s *Service) handoff(ctx context.Context, wid, tid, mid uuid.UUID, bot hd.HandoffBot) error {
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO ai_widget_answers (message_id, workspace_id, ticket_id, answered, handoff) VALUES ($1, $2, $3, false, true)
			ON CONFLICT DO NOTHING`, mid, wid, tid); err != nil {
			return err
		}
		_, err := hd.MarkAIHandoff(ctx, tx, wid, tid, "ai", bot)
		if db.IsNotFound(err) {
			return nil
		}
		return err
	})
	return err
}

// agentCapped reports whether the agent used its monthly cap, notifying owners and
// admins the first time each month.
func (s *Service) agentCapped(ctx context.Context, wid uuid.UUID, set hd.AISettings) (bool, error) {
	if set.WidgetMonthlyCapUSD <= 0 {
		return false, nil
	}
	month := time.Now().UTC()
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	var used plan.Micro
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(micro_usd), 0)::bigint FROM ai_usage
		WHERE workspace_id = $1 AND feature = ANY($2) AND created_at >= $3`, wid, agentFeatures, month).Scan(&used); err != nil {
		return false, err
	}
	if used < plan.FromUSD(set.WidgetMonthlyCapUSD) {
		return false, nil
	}
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// One notification a month: the advisory lock serializes concurrent jobs.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('ai_cap:' || $1::text, 0))`, wid); err != nil {
			return err
		}
		var sent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notifications WHERE workspace_id = $1 AND type = $2 AND created_at >= $3)`,
			wid, NotifyAICapReached, month).Scan(&sent); err != nil || sent {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM members WHERE workspace_id = $1 AND role IN ('owner', 'admin')`, wid)
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, uid := range users {
			if err := hd.Notify(ctx, tx, wid, uid, NotifyAICapReached, nil, map[string]any{"cap_usd": set.WidgetMonthlyCapUSD}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.d.Log.Warn("agent cap notification", "workspace_id", wid, "err", err)
	}
	return true, nil
}

// needsHuman asks Jev whether the visitor needs a person on the team.
func (s *Service) needsHuman(ctx context.Context, wid, tid uuid.UUID, msgs []domain.Message) (bool, error) {
	recent := msgs
	if len(recent) > 6 {
		recent = recent[len(recent)-6:]
	}
	conv := make([]map[string]string, 0, len(recent))
	for _, m := range recent {
		from := "visitor"
		if m.Direction == "outbound" {
			from = "assistant"
		}
		conv = append(conv, map[string]string{"from": from, "text": clip(strings.ToValidUTF8(messageText(m), ""), 1500)})
	}
	state := map[string]any{"conversation": conv, "latest_message": conv[len(conv)-1]["text"]}
	qs := map[string]decide.Question{
		"needs_human": decide.Noul(ask("Does the visitor need a person on the support team to handle `latest_message`, rather than an automatic assistant?"),
			"They ask for a person, need an exception, a refund decision or a judgement call, or are upset.",
			"An assistant with the company's help content and tools can handle it."),
	}
	c := s.newCall(wid, nil, &tid, "answer_check")
	res, err := c.decide(ctx, decide.Request{State: state, Questions: qs})
	if err != nil {
		return false, err
	}
	c.charge(ctx)
	return res.Answers["needs_human"].Noul >= needsHumanAt, nil
}
