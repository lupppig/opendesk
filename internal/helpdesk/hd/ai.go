package hd

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// AISettings lives in workspaces.settings.ai (contracts/ai.md).
type AISettings struct {
	Enabled       bool `json:"enabled"`
	AutoTriage    bool `json:"auto_triage"`
	AutoSummarize bool `json:"auto_summarize"`
	WidgetAnswers bool `json:"widget_answers"`
	// Assistant offers the assistant panel (Ollie) to the workspace's members.
	Assistant bool `json:"assistant"`
	// AssistantPeer lets members' Ollie ask a teammate's Ollie, for the members who opt in
	// (contracts/ai.md "Asking a teammate"). Off by default.
	AssistantPeer bool `json:"assistant_peer"`
	// AssistantAuto are the built-in changes Ollie makes without asking first (some of
	// AssistantAutoTools). Empty by default: every change waits for approval.
	AssistantAuto []string `json:"assistant_auto"`
	Tone          string   `json:"tone"`
	ReplyLanguage string   `json:"reply_language" enum:"customer,workspace"`
	Instructions  string   `json:"instructions"`
	// Models (contracts/ai.md "Models"); "" = the instance default.
	Model       string `json:"model"`
	FastModel   string `json:"fast_model"`
	WidgetModel string `json:"widget_model"`
	// The widget agent (contracts/agent.md). What it knows and how it talks is its bot's
	// (contracts/bots.md).
	WidgetMaxReplies    int      `json:"widget_max_replies"`
	WidgetBlockedTopics []string `json:"widget_blocked_topics"`
	WidgetMonthlyCapUSD float64  `json:"widget_monthly_cap_usd" doc:"US dollars of AI usage per month the widget agent may use; 0 = no cap"`
	WidgetCollectEmail  bool     `json:"widget_collect_email"`
}

// DefaultAISettings apply to keys a workspace has not set.
func DefaultAISettings() AISettings {
	return AISettings{Enabled: true, Assistant: true, AssistantAuto: []string{}, Tone: "friendly", ReplyLanguage: "customer",
		WidgetMaxReplies: DefaultWidgetMaxReplies, WidgetBlockedTopics: []string{}, WidgetCollectEmail: true}
}

// AI returns the workspace's AI settings with defaults for unset keys.
func (s Settings) AI() AISettings {
	a := DefaultAISettings()
	if len(s.AIRaw) > 0 {
		var m map[string]json.RawMessage
		if json.Unmarshal(s.AIRaw, &m) == nil {
			field := func(k string, v any) {
				if r, ok := m[k]; ok {
					_ = json.Unmarshal(r, v)
				}
			}
			field("enabled", &a.Enabled)
			field("auto_triage", &a.AutoTriage)
			field("auto_summarize", &a.AutoSummarize)
			field("widget_answers", &a.WidgetAnswers)
			field("assistant", &a.Assistant)
			field("assistant_peer", &a.AssistantPeer)
			field("assistant_auto", &a.AssistantAuto)
			field("tone", &a.Tone)
			field("reply_language", &a.ReplyLanguage)
			field("instructions", &a.Instructions)
			field("model", &a.Model)
			field("fast_model", &a.FastModel)
			field("widget_model", &a.WidgetModel)
			field("widget_max_replies", &a.WidgetMaxReplies)
			field("widget_blocked_topics", &a.WidgetBlockedTopics)
			field("widget_monthly_cap_usd", &a.WidgetMonthlyCapUSD)
			field("widget_collect_email", &a.WidgetCollectEmail)
		}
	}
	if a.WidgetMaxReplies < 1 || a.WidgetMaxReplies > 100 {
		a.WidgetMaxReplies = DefaultWidgetMaxReplies
	}
	// Only the changes that may run without asking count, whatever is stored.
	a.AssistantAuto = slices.DeleteFunc(slices.Clone(a.AssistantAuto), func(n string) bool { return !slices.Contains(AssistantAutoTools, n) })
	if a.AssistantAuto == nil {
		a.AssistantAuto = []string{}
	}
	if a.WidgetBlockedTopics == nil {
		a.WidgetBlockedTopics = []string{}
	}
	a.WidgetMonthlyCapUSD = max(a.WidgetMonthlyCapUSD, 0)
	if a.ReplyLanguage != "workspace" {
		a.ReplyLanguage = "customer"
	}
	if strings.TrimSpace(a.Tone) == "" {
		a.Tone = "friendly"
	}
	return a
}

// AISettingsPatch is the body of PATCH /v1/ai/settings.
type AISettingsPatch struct {
	Enabled       *bool     `json:"enabled,omitempty"`
	AutoTriage    *bool     `json:"auto_triage,omitempty"`
	AutoSummarize *bool     `json:"auto_summarize,omitempty"`
	WidgetAnswers *bool     `json:"widget_answers,omitempty"`
	Assistant     *bool     `json:"assistant,omitempty"`
	AssistantPeer *bool     `json:"assistant_peer,omitempty"`
	AssistantAuto *[]string `json:"assistant_auto,omitempty" maxItems:"10" doc:"Built-in changes Ollie makes without asking: update_ticket, bulk_update_tickets, add_note, create_kb_draft, create_view, create_automation_draft. Replaces the list"`
	Tone          *string   `json:"tone,omitempty" maxLength:"200" doc:"friendly, professional, concise, or free text up to 200 characters"`
	ReplyLanguage *string   `json:"reply_language,omitempty" enum:"customer,workspace"`
	Instructions  *string   `json:"instructions,omitempty" maxLength:"2000"`
	Model         *string   `json:"model,omitempty" maxLength:"200" doc:"\"\" or an id from GET /v1/ai/models"`
	FastModel     *string   `json:"fast_model,omitempty" maxLength:"200"`
	WidgetModel   *string   `json:"widget_model,omitempty" maxLength:"200"`

	WidgetMaxReplies    *int      `json:"widget_max_replies,omitempty" minimum:"1" maximum:"100"`
	WidgetBlockedTopics *[]string `json:"widget_blocked_topics,omitempty" maxItems:"20"`
	WidgetMonthlyCapUSD *float64  `json:"widget_monthly_cap_usd,omitempty" minimum:"0" maximum:"10000" doc:"US dollars, rounded to cents"`
	WidgetCollectEmail  *bool     `json:"widget_collect_email,omitempty"`
}

// DefaultWidgetMaxReplies is how many replies the widget agent gives in one
// conversation before it hands off, unless the workspace chose otherwise.
const DefaultWidgetMaxReplies = 20

// AssistantAutoTools are the built-in assistant changes a workspace may let Ollie make
// without asking first. Replies and anything else that reaches someone outside, and
// changes through the API (api_change), are not among them: those always ask
// (contracts/ai.md "Approvals").
var AssistantAutoTools = []string{"update_ticket", "bulk_update_tickets", "add_note", "create_kb_draft", "create_view", "create_automation_draft"}

// AITones are the named tones; any other value is free text.
var AITones = []string{"friendly", "professional", "concise"}

// ValidateAIPatch checks a settings.ai patch and returns the keys to merge. allowedModel
// reports whether a model id is one the instance offers.
func ValidateAIPatch(p AISettingsPatch, allowedModel func(string) bool) (map[string]any, error) {
	out := map[string]any{}
	if p.Enabled != nil {
		out["enabled"] = *p.Enabled
	}
	if p.AutoTriage != nil {
		out["auto_triage"] = *p.AutoTriage
	}
	if p.AutoSummarize != nil {
		out["auto_summarize"] = *p.AutoSummarize
	}
	if p.WidgetAnswers != nil {
		out["widget_answers"] = *p.WidgetAnswers
	}
	if p.Assistant != nil {
		out["assistant"] = *p.Assistant
	}
	if p.AssistantPeer != nil {
		out["assistant_peer"] = *p.AssistantPeer
	}
	if p.AssistantAuto != nil {
		list := []string{}
		for _, n := range *p.AssistantAuto {
			if !slices.Contains(AssistantAutoTools, n) {
				return nil, httpx.Validation("assistant_auto takes only " + strings.Join(AssistantAutoTools, ", "))
			}
			if !slices.Contains(list, n) {
				list = append(list, n)
			}
		}
		out["assistant_auto"] = list
	}
	if p.Tone != nil {
		t := strings.Join(strings.Fields(*p.Tone), " ")
		if t == "" {
			t = "friendly"
		}
		if utf8.RuneCountInString(t) > 200 || strings.ContainsAny(t, "<>`") || strings.IndexFunc(t, unicode.IsControl) >= 0 {
			return nil, httpx.Validation("tone must be friendly, professional, concise, or up to 200 characters of plain text")
		}
		if slices.Contains(AITones, strings.ToLower(t)) {
			t = strings.ToLower(t)
		}
		out["tone"] = t
	}
	if p.ReplyLanguage != nil {
		if *p.ReplyLanguage != "customer" && *p.ReplyLanguage != "workspace" {
			return nil, httpx.Validation("reply_language must be customer or workspace")
		}
		out["reply_language"] = *p.ReplyLanguage
	}
	if p.Instructions != nil {
		s := strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(*p.Instructions, ""), "\r\n", "\n"))
		s = strings.Map(func(r rune) rune {
			if r != '\n' && r != '\t' && unicode.IsControl(r) {
				return -1
			}
			return r
		}, s)
		if utf8.RuneCountInString(s) > 2000 {
			return nil, httpx.Validation("instructions must be 2000 characters or less")
		}
		out["instructions"] = s
	}
	for key, v := range map[string]*string{"model": p.Model, "fast_model": p.FastModel, "widget_model": p.WidgetModel} {
		if v == nil {
			continue
		}
		m := strings.TrimSpace(*v)
		if m != "" && (allowedModel == nil || !allowedModel(m)) {
			return nil, httpx.Validation(key + " must be one of the models in GET /v1/ai/models")
		}
		out[key] = m
	}
	if p.WidgetMaxReplies != nil {
		if *p.WidgetMaxReplies < 1 || *p.WidgetMaxReplies > 100 {
			return nil, httpx.Validation("widget_max_replies must be from 1 to 100")
		}
		out["widget_max_replies"] = *p.WidgetMaxReplies
	}
	if p.WidgetBlockedTopics != nil {
		topics := []string{}
		for _, t := range *p.WidgetBlockedTopics {
			t = strings.Join(strings.Fields(t), " ")
			if t == "" {
				continue
			}
			if n := utf8.RuneCountInString(t); n < 2 || n > 100 || strings.ContainsAny(t, "<>`") || strings.IndexFunc(t, unicode.IsControl) >= 0 {
				return nil, httpx.Validation("each widget_blocked_topics entry must be 2 to 100 characters of plain text")
			}
			if !slices.ContainsFunc(topics, func(x string) bool { return strings.EqualFold(x, t) }) {
				topics = append(topics, t)
			}
		}
		if len(topics) > 20 {
			return nil, httpx.Validation("at most 20 widget_blocked_topics")
		}
		out["widget_blocked_topics"] = topics
	}
	if p.WidgetMonthlyCapUSD != nil {
		v := math.Round(*p.WidgetMonthlyCapUSD*100) / 100
		if math.IsNaN(v) || v < 0 || v > 10_000 {
			return nil, httpx.Validation("widget_monthly_cap_usd must be from 0 to 10000")
		}
		out["widget_monthly_cap_usd"] = v
	}
	if p.WidgetCollectEmail != nil {
		out["widget_collect_email"] = *p.WidgetCollectEmail
	}
	return out, nil
}

// cleanInstructions trims guidance text and drops control characters other than
// newline and tab.
func cleanInstructions(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\r\n", "\n"))
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// AIHooks are AI features the helpdesk calls without importing internal/ai, which
// installs them at registration. Every hook is optional.
type AIHooks struct {
	// AutoTriage triages a new ticket when settings.ai.auto_triage is on. It runs before
	// the ticket_created automations, outside their transaction, and applies its result
	// in its own transaction. It is a no-op when the ticket was triaged already.
	AutoTriage func(ctx context.Context, wid, tid uuid.UUID) error
	// Triage runs the ai_triage automation action inside the trigger transaction and
	// returns the ticket fields it changed. It returns (nil, nil) when it skips (AI off,
	// no AI balance, provider unavailable); an error means tx failed.
	Triage func(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID) ([]string, error)
	// Decide answers ai_decide conditions: the probability of yes for each question, in
	// one decision call inside the trigger transaction. A question it could not answer
	// (no decision engine, AI off, no AI balance, provider failure) is absent from the map;
	// an error means tx failed.
	Decide func(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID, questions []string) (map[string]float64, error)
}

var aiHooks atomic.Pointer[AIHooks]

// SetAIHooks installs the AI hooks (internal/ai does this at registration).
func SetAIHooks(h *AIHooks) { aiHooks.Store(h) }

// GetAIHooks returns the installed hooks, or nil.
func GetAIHooks() *AIHooks { return aiHooks.Load() }

// AIEmbeddingsOn reports whether semantic indexing runs on this instance.
func AIEmbeddingsOn(cfg *config.Config) bool {
	return cfg != nil && cfg.AIEnabled() && cfg.EmbeddingsDriver != "" && cfg.EmbeddingsDriver != "none"
}

// EnqueueAIEmbed queues (re)indexing of an article or ticket in tx. The worker skips
// unchanged content and removes embeddings of content that is gone.
func EnqueueAIEmbed(ctx context.Context, tx pgx.Tx, jc domain.Jobs, cfg *config.Config, wid uuid.UUID, kind string, id uuid.UUID) error {
	if jc == nil || !AIEmbeddingsOn(cfg) {
		return nil
	}
	_, err := jc.InsertTx(ctx, tx, jobs.AIEmbedArgs{WorkspaceID: wid, Type: kind, RefID: id},
		&river.InsertOpts{MaxAttempts: 5, ScheduledAt: time.Now().Add(5 * time.Second)})
	return err
}

// EnqueueAIWidgetAnswer queues the widget answer bot for a visitor message when the
// workspace turned it on. It only inserts a job; the request never waits for the model.
func EnqueueAIWidgetAnswer(ctx context.Context, tx pgx.Tx, jc domain.Jobs, cfg *config.Config, wid, tid, mid uuid.UUID, pageURL string) error {
	if jc == nil || cfg == nil || !cfg.AIEnabled() {
		return nil
	}
	s, err := LoadSettings(ctx, tx, wid)
	if err != nil {
		return err
	}
	if a := s.AI(); !a.Enabled || !a.WidgetAnswers {
		return nil
	}
	// A short delay lets a burst of quick messages get one reply about all of them: the
	// job of an earlier message sees it is no longer the latest and does nothing.
	_, err = jc.InsertTx(ctx, tx, jobs.AIWidgetAnswerArgs{WorkspaceID: wid, TicketID: tid, MessageID: mid, PageURL: pageURL},
		&river.InsertOpts{MaxAttempts: 2, ScheduledAt: time.Now().Add(1500 * time.Millisecond),
			UniqueOpts: river.UniqueOpts{ByArgs: true}})
	return err
}

// HandoffBot is the bot that hands a conversation to the team; ID is nil for the built-in default.
type HandoffBot struct {
	ID   *uuid.UUID
	Name string
}

// NotifyAIHandoff is the notification type sent when a visitor asks for a person.
const NotifyAIHandoff = "ai_handoff"

// MarkAIHandoff hands a chat over from the answer bot to the team: it sets
// custom.ai_handoff, logs an ai_handoff activity, notifies the assignee (or the team's
// members) and publishes ticket.updated. bot is the bot that handed over
// (custom.ai_handoff_bot and the activity's data.bot and data.bot_id), zero when the
// visitor asked. It reports false when the ticket was already handed off.
func MarkAIHandoff(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID, actorType string, bot HandoffBot) (bool, error) {
	var custom map[string]any
	var assignee, team *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT custom, assignee_id, team_id FROM tickets WHERE workspace_id = $1 AND id = $2
		AND deleted_at IS NULL FOR UPDATE`, wid, tid).Scan(&custom, &assignee, &team); err != nil {
		return false, err
	}
	if v, _ := custom["ai_handoff"].(bool); v {
		return false, nil
	}
	patch := map[string]any{"ai_handoff": true, "ai_handoff_at": time.Now().UTC().Format(time.RFC3339)}
	data := map[string]any{}
	if bot.Name != "" {
		patch["ai_handoff_bot"] = bot.Name
		data["bot"] = bot.Name
	}
	if bot.ID != nil {
		data["bot_id"] = bot.ID.String()
	}
	if _, err := tx.Exec(ctx, `UPDATE tickets SET custom = custom || $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		wid, tid, patch); err != nil {
		return false, err
	}
	if _, err := domain.LogActivity(ctx, tx, wid, tid, actorType, nil, "ai_handoff", data); err != nil {
		return false, err
	}
	// The assignee, else the ticket's team's leads and members (not observers), else every
	// member who can reply. Notify skips anyone who may not see the ticket.
	var recipients []uuid.UUID
	if assignee != nil {
		recipients = []uuid.UUID{*assignee}
	} else {
		var err error
		if recipients, err = TicketAudience(ctx, tx, wid, team, 50); err != nil {
			return false, err
		}
	}
	var contact map[string]any
	var cid uuid.UUID
	var cname string
	if err := tx.QueryRow(ctx, `SELECT c.id, c.name FROM tickets t JOIN contacts c ON c.id = t.contact_id AND c.workspace_id = t.workspace_id
		WHERE t.workspace_id = $1 AND t.id = $2`, wid, tid).Scan(&cid, &cname); err == nil {
		contact = map[string]any{"id": cid.String(), "name": cname}
	}
	for _, uid := range recipients {
		data := map[string]any{"via": actorType}
		if contact != nil {
			data["contact"] = contact
		}
		if err := Notify(ctx, tx, wid, uid, NotifyAIHandoff, &tid, data); err != nil {
			return false, err
		}
	}
	return true, domain.PublishTicketUpdated(ctx, tx, wid, tid, []string{"custom"})
}
