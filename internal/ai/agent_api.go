package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

// The agent's admin routes (contracts/agent.md "Playground", "Report").

// PlaygroundInput is the body of POST /v1/ai/agent/playground.
type PlaygroundInput struct {
	Messages []PlaygroundMessage `json:"messages" minItems:"1" maxItems:"40"`
	Settings *AgentDraft         `json:"settings,omitempty" doc:"Unsaved drafts of these settings"`
	Visitor  *PlaygroundVisitor  `json:"visitor,omitempty"`
}

// AgentDraft is unsaved agent settings the playground and test runs use instead of the
// saved ones.
type AgentDraft struct {
	WidgetModel         *string   `json:"widget_model,omitempty"`
	WidgetBlockedTopics *[]string `json:"widget_blocked_topics,omitempty"`
	Tone                *string   `json:"tone,omitempty"`
	ReplyLanguage       *string   `json:"reply_language,omitempty"`
	Instructions        *string   `json:"instructions,omitempty"`
	// BotID answers as this bot of the workspace; empty = the default bot.
	BotID *string `json:"bot_id,omitempty" doc:"Answer as this bot (contracts/bots.md); empty = the default bot"`
	// Bot is an unsaved edit of that bot.
	Bot *bots.Draft `json:"bot,omitempty" doc:"Unsaved edits of the bot: name, role, language, personality"`
}

// PlaygroundVisitor is who the agent talks to in the playground and in tests.
type PlaygroundVisitor struct {
	Name     string `json:"name,omitempty" maxLength:"100"`
	Email    string `json:"email,omitempty" maxLength:"254"`
	Verified bool   `json:"verified,omitempty"`
	UserID   string `json:"user_id,omitempty" maxLength:"200"`
}

type PlaygroundMessage struct {
	Role string `json:"role" enum:"visitor,agent"`
	Text string `json:"text" maxLength:"4000"`
}

// PlaygroundAction is a tool call the playground did not run.
type PlaygroundAction struct {
	Tool    string `json:"tool"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// PlaygroundOutput is the playground's done event (and the JSON response).
type PlaygroundOutput struct {
	TextHTML string            `json:"text_html"`
	Sources  []CitedSource     `json:"sources"`
	Handoff  bool              `json:"handoff"`
	Action   *PlaygroundAction `json:"action"`
	// Procedures are the ones the agent read for this reply.
	Procedures []ProcedureRef `json:"procedures"`
	USD        float64        `json:"usd" doc:"What the reply cost, in US dollars"`
}

// AgentReport is GET /v1/ai/agent/report.
type AgentReport struct {
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to"`
	Conversations int                `json:"conversations"`
	Replies       int                `json:"replies"`
	Resolved      int                `json:"resolved"`
	Handoffs      int                `json:"handoffs"`
	Ratings       AgentRatings       `json:"ratings"`
	USD           float64            `json:"usd" doc:"AI usage of the agent, in US dollars"`
	ByDay         []AgentReportDay   `json:"by_day"`
	Tools         []AgentToolUse     `json:"tools"`
	Gaps          []AgentQuestionGap `json:"gaps"`
	// Channels splits the totals by channel (email_agent_report.go).
	Channels AgentChannels `json:"channels"`
}

type AgentRatings struct {
	Up   int `json:"up"`
	Down int `json:"down"`
}

type AgentReportDay struct {
	Date          string `json:"date"`
	Conversations int    `json:"conversations"`
	Replies       int    `json:"replies"`
	Resolved      int    `json:"resolved"`
	Handoffs      int    `json:"handoffs"`
}

type AgentToolUse struct {
	Tool   string `json:"tool"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

type AgentQuestionGap struct {
	Question string `json:"question"`
	Count    int    `json:"count"`
}

const (
	playgroundPerMinute = 30
	playgroundTimeout   = 3 * time.Minute
)

func (s *Service) registerAgent(api huma.API) {
	tags := []string{"AI"}
	s.registerAgentQuality(api)

	huma.Register(api, huma.Operation{OperationID: "ai-agent-playground", Method: http.MethodPost, Path: "/v1/ai/agent/playground",
		Summary: "Test the widget agent", Description: "Runs the widget agent on a made-up conversation, without a visitor or a ticket. " +
			"Read-only tools run; any other tool comes back as `action`. Send `Accept: text/event-stream` to stream `delta`, `tool`, " +
			"`sources` and a final `done` event with this response.",
		Tags: tags, Middlewares: huma.Middlewares{s.sse(s.streamPlayground)}},
		func(ctx context.Context, in *struct{ Body PlaygroundInput }) (*struct{ Body PlaygroundOutput }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.Playground(ctx, p, in.Body, agentEvents{})
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body PlaygroundOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-agent-report", Method: http.MethodGet, Path: "/v1/ai/agent/report",
		Summary: "Widget agent report", Description: "Conversations, replies, resolutions, handoffs, ratings, AI usage in US dollars, tool use and unanswered questions.", Tags: tags},
		func(ctx context.Context, in *struct {
			From string `query:"from" doc:"RFC 3339 time or YYYY-MM-DD (default: 30 days ago)"`
			To   string `query:"to" doc:"RFC 3339 time (exclusive) or YYYY-MM-DD (that day included) (default: now)"`
		}) (*struct{ Body AgentReport }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			from, to, err := reportRange(in.From, in.To)
			if err != nil {
				return nil, err
			}
			r, err := s.agentReport(ctx, p, from, to)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body AgentReport }{r}, nil
		})
}

func (s *Service) streamPlayground(w http.ResponseWriter, r *http.Request, _ string) {
	es := newEventStream(w, r)
	ctx := r.Context()
	p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
	if err != nil {
		es.fail(httpx.Wrap(err))
		return
	}
	var in PlaygroundInput
	if err := decodeBody(r, &in); err != nil {
		es.fail(err)
		return
	}
	ev := agentEvents{
		delta: func(t string) { es.send("delta", map[string]string{"text": t}) },
		tool: func(name, status, summary string) {
			es.send("tool", map[string]string{"name": name, "status": status, "summary": summary})
		},
		sources: func(ps []Passage) { es.send("sources", map[string]any{"sources": toSources(ps)}) },
	}
	out, err := s.Playground(ctx, p, in, ev)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		es.fail(err)
		return
	}
	es.send("done", out)
}

// Playground runs the agent on a made-up conversation.
func (s *Service) Playground(ctx context.Context, p *auth.Principal, in PlaygroundInput, ev agentEvents) (*PlaygroundOutput, error) {
	if !s.Enabled() {
		return nil, errDisabled
	}
	if n := len(in.Messages); n < 1 || n > 40 || in.Messages[n-1].Role != "visitor" {
		return nil, httpx.Validation("messages must be 1 to 40, the last from the visitor")
	}
	key := p.WorkspaceID.String()
	if uid := p.ActorUserID(); uid != nil {
		key = uid.String()
	}
	if !s.d.Limiter.Allow("ai:playground:"+key, playgroundPerMinute, time.Minute) {
		return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many playground runs. Try again in a minute.")
	}
	ai, err := s.agentTrial(ctx, p.WorkspaceID, in.Messages, in.Settings, in.Visitor)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, playgroundTimeout)
	defer cancel()
	res, err := s.runAgent(ctx, ai, ev)
	if err != nil {
		return nil, err
	}
	body, cited := finishReply(res.Text, res.Passages)
	out := &PlaygroundOutput{TextHTML: body, Sources: toSources(cited), Handoff: res.Handoff, Procedures: procedureRefs(res.Procedures), USD: res.USD.USD()}
	if a := res.Action; a != nil {
		out.Action = &PlaygroundAction{Tool: a.Tool.displayName(), Title: firstNonEmpty(a.Tool.Title, a.Tool.displayName()), Summary: a.Summary}
	}
	return out, nil
}

// agentTrial builds the agent's input for a made-up conversation (the playground and
// test runs): the saved settings with the drafts on top, no ticket, read-only tools.
func (s *Service) agentTrial(ctx context.Context, wid uuid.UUID, msgs []PlaygroundMessage, draft *AgentDraft, v *PlaygroundVisitor) (agentInput, error) {
	if n := len(msgs); n < 1 || n > 40 || msgs[n-1].Role != "visitor" {
		return agentInput{}, httpx.Validation("messages must be 1 to 40, the last from the visitor")
	}
	st, err := hd.LoadSettings(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return agentInput{}, err
	}
	set, err := s.draftSettings(ctx, wid, st.AI(), draft)
	if err != nil {
		return agentInput{}, err
	}
	var company string
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, wid).Scan(&company); err != nil {
		return agentInput{}, err
	}
	bot, err := s.trialBot(ctx, wid, draft)
	if err != nil {
		return agentInput{}, err
	}
	var thread strings.Builder
	texts, burst := []string{}, 0
	for i, m := range msgs {
		text := strings.TrimSpace(strings.ToValidUTF8(m.Text, ""))
		if text == "" || utf8.RuneCountInString(text) > 4000 {
			return agentInput{}, httpx.Validation("each message text must be 1 to 4000 characters")
		}
		from := "ai_assistant"
		if m.Role == "visitor" {
			from = "customer"
			texts = append(texts, text)
			burst++
		} else {
			burst = 0
		}
		thread.WriteString(block("message", []string{"n", strconv.Itoa(i + 1), "from", from}, clip(untrusted(text), perMessageChars)))
		thread.WriteString("\n")
	}
	ai := agentInput{wid: wid, feature: "playground", set: set, bot: bot, company: company,
		thread: block("ticket_thread", nil, strings.TrimSpace(thread.String())), query: agentQuery(texts, burst), playground: true}
	if v != nil {
		ai.visitor = AgentVisitor{Verified: v.Verified, Name: clip(strings.TrimSpace(v.Name), 100)}
		if v.Verified {
			ai.visitor.UserID, ai.visitor.Email = clip(strings.TrimSpace(v.UserID), 200), clip(strings.TrimSpace(v.Email), 254)
		}
	}
	if set.WidgetModel != "" {
		ai.model = set.WidgetModel
	}
	return ai, nil
}

// trialBot is the bot a trial answers as: the one the draft names (of this workspace),
// else the default, with the draft's unsaved edits.
func (s *Service) trialBot(ctx context.Context, wid uuid.UUID, d *AgentDraft) (bots.Bot, error) {
	var id *uuid.UUID
	var edit *bots.Draft
	if d != nil {
		var err error
		if id, err = bots.ParseRef(ctx, s.d.DB.Pool, wid, d.BotID); err != nil {
			return bots.Bot{}, err
		}
		edit = d.Bot
	}
	b, err := bots.Resolve(ctx, s.d.DB.Pool, wid, id)
	if err != nil {
		return b, err
	}
	return b.WithDraft(edit)
}

// draftSettings applies unsaved settings on top of the saved ones.
func (s *Service) draftSettings(ctx context.Context, wid uuid.UUID, set hd.AISettings, d *AgentDraft) (hd.AISettings, error) {
	if d == nil {
		return set, nil
	}
	patch, err := hd.ValidateAIPatch(hd.AISettingsPatch{WidgetModel: d.WidgetModel,
		WidgetBlockedTopics: d.WidgetBlockedTopics, Tone: d.Tone, ReplyLanguage: d.ReplyLanguage, Instructions: d.Instructions}, s.modelCheck(ctx, wid))
	if err != nil {
		return set, err
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return set, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return set, err
	}
	for k, v := range patch {
		m[k] = v
	}
	if raw, err = json.Marshal(m); err != nil {
		return set, err
	}
	var out hd.AISettings
	if err := json.Unmarshal(raw, &out); err != nil {
		return set, err
	}
	return out, nil
}

// reportRange parses from and to like /v1/billing/usage; the default is the last 30 days.
func reportRange(fromQ, toQ string) (time.Time, time.Time, error) {
	parse := func(v string) (time.Time, error) {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UTC(), nil
		}
		return time.Parse(time.DateOnly, v)
	}
	now := time.Now().UTC()
	from, to := now.AddDate(0, 0, -30), now
	var err error
	if fromQ != "" {
		if from, err = parse(fromQ); err != nil {
			return from, to, httpx.Validation("from must be an RFC 3339 time or a YYYY-MM-DD date.")
		}
	}
	if toQ != "" {
		if to, err = parse(toQ); err != nil {
			return from, to, httpx.Validation("to must be an RFC 3339 time or a YYYY-MM-DD date.")
		}
		if _, dateErr := time.Parse(time.DateOnly, toQ); dateErr == nil {
			to = to.AddDate(0, 0, 1)
		}
	}
	if !to.After(from) || to.Sub(from) > 367*24*time.Hour {
		return from, to, httpx.Validation("to must be after from, at most a year apart.")
	}
	return from, to, nil
}

func (s *Service) agentReport(ctx context.Context, p *auth.Principal, from, to time.Time) (AgentReport, error) {
	q := s.d.DB.Pool
	wid := p.WorkspaceID
	r := AgentReport{From: from, To: to, ByDay: []AgentReportDay{}, Tools: []AgentToolUse{}, Gaps: []AgentQuestionGap{}}
	// Agent replies in the range, by day, with the conversations they belong to.
	const replies = `WITH r AS (
			SELECT m.ticket_id, m.created_at, m.ai_rating FROM messages m
			WHERE m.workspace_id = $1 AND m.ai AND m.kind = 'reply' AND m.created_at >= $2 AND m.created_at < $3),
		conv AS (
			SELECT r.ticket_id, min(r.created_at) AS first_at,
				bool_or(t.status IN ('solved', 'closed') AND NOT EXISTS (SELECT 1 FROM messages u
					WHERE u.workspace_id = $1 AND u.ticket_id = r.ticket_id AND u.kind = 'reply' AND u.author_type = 'user')) AS resolved
			FROM r JOIN tickets t ON t.workspace_id = $1 AND t.id = r.ticket_id GROUP BY r.ticket_id)`
	if err := q.QueryRow(ctx, replies+`
		SELECT (SELECT count(*) FROM conv)::int, (SELECT count(*) FROM r)::int, (SELECT count(*) FROM conv WHERE resolved)::int,
			(SELECT count(*) FROM r WHERE ai_rating = 'up')::int, (SELECT count(*) FROM r WHERE ai_rating = 'down')::int`,
		wid, from, to).Scan(&r.Conversations, &r.Replies, &r.Resolved, &r.Ratings.Up, &r.Ratings.Down); err != nil {
		return r, err
	}
	if err := q.QueryRow(ctx, `SELECT count(*)::int FROM activities WHERE workspace_id = $1 AND type = 'ai_handoff'
		AND created_at >= $2 AND created_at < $3`, wid, from, to).Scan(&r.Handoffs); err != nil {
		return r, err
	}
	var usd plan.Micro
	if err := q.QueryRow(ctx, `SELECT COALESCE(sum(micro_usd), 0)::bigint FROM ai_usage WHERE workspace_id = $1
		AND feature = ANY($4) AND created_at >= $2 AND created_at < $3`, wid, from, to, agentFeatures).Scan(&usd); err != nil {
		return r, err
	}
	r.USD = usd.USD()
	channels, err := s.agentReportChannels(ctx, wid, from, to)
	if err != nil {
		return r, err
	}
	r.Channels = channels
	rows, err := q.Query(ctx, replies+`,
		days AS (
			SELECT date_trunc('day', created_at AT TIME ZONE 'UTC')::date AS d, count(*) AS replies, count(DISTINCT ticket_id) AS convs FROM r GROUP BY 1),
		res AS (
			SELECT date_trunc('day', first_at AT TIME ZONE 'UTC')::date AS d, count(*) FILTER (WHERE resolved) AS resolved FROM conv GROUP BY 1),
		hand AS (
			SELECT date_trunc('day', created_at AT TIME ZONE 'UTC')::date AS d, count(*) AS handoffs FROM activities
			WHERE workspace_id = $1 AND type = 'ai_handoff' AND created_at >= $2 AND created_at < $3 GROUP BY 1)
		SELECT to_char(x.d, 'YYYY-MM-DD'), COALESCE(days.convs, 0)::int, COALESCE(days.replies, 0)::int, COALESCE(res.resolved, 0)::int, COALESCE(hand.handoffs, 0)::int
		FROM (SELECT d FROM days UNION SELECT d FROM res UNION SELECT d FROM hand) x
		LEFT JOIN days ON days.d = x.d LEFT JOIN res ON res.d = x.d LEFT JOIN hand ON hand.d = x.d ORDER BY x.d`, wid, from, to)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var d AgentReportDay
		if err := rows.Scan(&d.Date, &d.Conversations, &d.Replies, &d.Resolved, &d.Handoffs); err != nil {
			rows.Close()
			return r, err
		}
		r.ByDay = append(r.ByDay, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	rows, err = q.Query(ctx, `SELECT tool, count(*)::int, count(*) FILTER (WHERE NOT ok)::int FROM ai_tool_calls
		WHERE workspace_id = $1 AND created_at >= $2 AND created_at < $3 GROUP BY tool ORDER BY 2 DESC, 1 LIMIT 100`, wid, from, to)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var t AgentToolUse
		if err := rows.Scan(&t.Tool, &t.Calls, &t.Errors); err != nil {
			rows.Close()
			return r, err
		}
		r.Tools = append(r.Tools, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	// Questions the agent handed off or did not answer, grouped by their text. The text of a
	// private team's conversation is left out for whoever may not see it.
	hidden, err := domain.HiddenTeams(ctx, q, p)
	if err != nil {
		return r, err
	}
	rows, err = q.Query(ctx, `SELECT left(btrim(regexp_replace(m.body_text, '\s+', ' ', 'g')), 200) AS question, count(*)::int, max(a.created_at)
		FROM ai_widget_answers a JOIN messages m ON m.workspace_id = a.workspace_id AND m.id = a.message_id AND m.direction = 'inbound'
		JOIN tickets t ON t.workspace_id = m.workspace_id AND t.id = m.ticket_id
		WHERE a.workspace_id = $1 AND (a.handoff OR NOT a.answered) AND a.created_at >= $2 AND a.created_at < $3
			AND `+domain.VisibleSQLUser("t", 4, 5)+`
		GROUP BY 1 HAVING left(btrim(regexp_replace(m.body_text, '\s+', ' ', 'g')), 200) <> '' ORDER BY 3 DESC LIMIT 20`, wid, from, to, hidden, p.UserID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var g AgentQuestionGap
		var at time.Time
		if err := rows.Scan(&g.Question, &g.Count, &at); err != nil {
			return r, err
		}
		r.Gaps = append(r.Gaps, g)
	}
	if err := rows.Err(); err != nil && !db.IsNotFound(err) {
		return r, err
	}
	return r, nil
}
