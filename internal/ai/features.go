package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Sentiment and triage enums (contracts/ai.md).
var (
	Sentiments = hd.Sentiments
	Categories = []string{"question", "problem", "bug", "billing", "account", "feature_request", "feedback", "sales",
		"shipping", "refund", "cancellation", "spam", "other"}
	priorityRank = map[string]int{"low": 0, "normal": 1, "high": 2, "urgent": 3}
)

// parseJSON decodes a model's JSON output, tolerating a code fence or stray prose.
func parseJSON(text string, v any) error {
	t := stripFence(text)
	if i, j := strings.Index(t, "{"), strings.LastIndex(t, "}"); i >= 0 && j > i {
		t = t[i : j+1]
	}
	if err := json.Unmarshal([]byte(t), v); err != nil {
		return ErrBadOutput
	}
	return nil
}

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

var (
	strT   = map[string]any{"type": "string"}
	strArr = map[string]any{"type": "array", "items": strT}
	boolT  = map[string]any{"type": "boolean"}
)

func enumT(vals []string) map[string]any { return map[string]any{"type": "string", "enum": vals} }

// cleanLine is model text reduced to one plain line of at most n runes.
func cleanLine(s string, n int) string {
	return hd.Truncate(strings.Join(strings.Fields(hd.HTMLToText(s)), " "), n)
}

// canonicalLang validates a BCP 47 tag, returning "" when it is not one.
func canonicalLang(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 35 {
		return ""
	}
	if s == "und" {
		return s
	}
	t, err := language.Parse(s)
	if err != nil {
		return ""
	}
	return t.String()
}

// --- Draft ------------------------------------------------------------------------

type DraftInput struct {
	Instructions string `json:"instructions,omitempty" maxLength:"2000"`
	Tone         string `json:"tone,omitempty" maxLength:"200"`
}

type DraftOutput struct {
	BodyHTML string     `json:"body_html"`
	BodyText string     `json:"body_text"`
	Sources  []KBSource `json:"sources"`
}

func checkLen(field, v string, n int) error {
	if utf8.RuneCountInString(v) > n {
		return httpx.Validation(fmt.Sprintf("%s must be %d characters or less", field, n))
	}
	return nil
}

// Draft writes a reply to the ticket's latest customer message.
func (s *Service) Draft(ctx context.Context, p *auth.Principal, rawID string, in DraftInput, onDelta func(string)) (*DraftOutput, error) {
	if err := checkLen("instructions", in.Instructions, 2000); err != nil {
		return nil, err
	}
	if err := checkLen("tone", in.Tone, 200); err != nil {
		return nil, err
	}
	set, err := s.workspaceAI(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	tid, err := resolveVisibleTicket(ctx, s.d.DB.Pool, p, rawID)
	if err != nil {
		return nil, err
	}
	tc, err := s.loadTicketCtx(ctx, s.d.DB.Pool, p.WorkspaceID, tid, false)
	if err != nil {
		return nil, err
	}
	query := tc.T.Subject + "\n" + clip(lastCustomerText(tc.Msgs), 2000)
	sources, err := s.retrieveKB(ctx, p.WorkspaceID, ticketBrand(tc.T), query, 4, false)
	if err != nil {
		return nil, err
	}
	replies, err := s.savedReplies(ctx, p, tc.T.TeamID, query, 3)
	if err != nil {
		return nil, err
	}
	agent := ""
	if uid := p.ActorUserID(); uid != nil {
		if u, err := domain.LoadUserRef(ctx, s.d.DB.Pool, *uid); err == nil {
			agent = u.Name
		}
	}
	signoff := "End with a short sign-off line using the agent's first name"
	if f := firstName(agent); f != "" {
		signoff += " (" + attr(f) + ")"
	}
	signoff += "."
	switch {
	case tc.T.Channel == domain.ChannelChat:
		signoff = "This is a live chat: no sign-off or signature."
	case tc.Signature:
		signoff = "A signature is appended automatically when the reply is sent: do not add a sign-off name or signature block."
	}
	task := "Write the reply to the customer now. " + signoff
	request := ""
	if ins := strings.TrimSpace(in.Instructions); ins != "" {
		request = block("agent_request", nil, "The agent asks: "+angle.Replace(ins))
		task = request + "\n" + task
	}
	thread := renderThread(tc.Msgs, tc.Summary, threadChars)
	// The facts the reply needs, read from the workspace's connected tools first (draft_tools.go).
	looked, err := s.draftLookup(ctx, p, tid, set, strings.Join([]string{renderTicket(tc.T), renderContact(tc.Contact), thread, request}, "\n"))
	if err != nil {
		return nil, err
	}
	blocks := []Block{
		{Text: renderTicket(tc.T) + "\n" + renderContact(tc.Contact) + "\n" + renderKB(sources) + "\n" + replies, Cache: true},
		{Text: thread},
	}
	if looked != "" {
		blocks = append(blocks, Block{Text: looked})
	}
	req := Request{Model: s.models.Default, MaxTokens: 6000, Effort: "medium",
		System:   []Block{{Text: draftSystem, Cache: true}, {Text: guidance(tc.Workspace.Name, set, in.Tone, tc.language()), Cache: true}},
		Messages: []Message{{Role: "user", Blocks: append(blocks, Block{Text: task})}}}
	c := s.newCall(p.WorkspaceID, p.ActorUserID(), &tid, "draft_reply")
	resp, err := c.run(ctx, req, onDelta)
	if err != nil {
		return nil, err
	}
	text := TidyReply(stripFence(resp.Text))
	bodyHTML := markdownToHTML(text)
	if hd.HTMLToText(bodyHTML) == "" {
		return nil, ErrBadOutput
	}
	c.charge(ctx)
	return &DraftOutput{BodyHTML: bodyHTML, BodyText: hd.HTMLToText(bodyHTML), Sources: publicSources(sources)}, nil
}

// savedReplies picks the saved replies most related to query, rendered as a data block.
// Only the replies the composer offers on this ticket are considered: those with no team
// and those of the ticket's team, never a private team p may not see (contracts/teams.md
// "Settings scoped to a team").
func (s *Service) savedReplies(ctx context.Context, p *auth.Principal, teamID *uuid.UUID, query string, n int) (string, error) {
	hidden, err := domain.HiddenTeams(ctx, s.d.DB.Pool, p)
	if err != nil {
		return "", err
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT title, body_html FROM saved_replies
		WHERE workspace_id = $1 AND (team_id IS NULL OR (team_id = $2 AND NOT team_id = ANY($3::uuid[])))
		ORDER BY updated_at DESC LIMIT 300`, p.WorkspaceID, teamID, hidden)
	if err != nil {
		return "", err
	}
	type sr struct {
		title, text string
		cov         float64
	}
	var list []sr
	words := keywords(query)
	for rows.Next() {
		var r sr
		var body string
		if err := rows.Scan(&r.title, &body); err != nil {
			rows.Close()
			return "", err
		}
		r.text = hd.HTMLToText(body)
		r.cov = coverage(words, r.title+" "+r.text, 8)
		if r.cov >= minCoverageDraft {
			list = append(list, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", nil
	}
	slices.SortStableFunc(list, func(a, b sr) int {
		switch {
		case a.cov > b.cov:
			return -1
		case a.cov < b.cov:
			return 1
		}
		return 0
	})
	var parts []string
	for _, r := range list[:min(n, len(list))] {
		parts = append(parts, block("reply", []string{"title", r.title}, clip(untrusted(r.text), 3000)))
	}
	return block("saved_replies", nil, strings.Join(parts, "\n")), nil
}

// --- Ask (copilot) -----------------------------------------------------------------

type AskInput struct {
	Question string `json:"question" minLength:"1" maxLength:"2000"`
}

type AskOutput struct {
	AnswerHTML string     `json:"answer_html"`
	Sources    []KBSource `json:"sources"`
}

func (s *Service) Ask(ctx context.Context, p *auth.Principal, rawID string, in AskInput, onDelta func(string)) (*AskOutput, error) {
	q := strings.TrimSpace(in.Question)
	if q == "" {
		return nil, httpx.Validation("question is required")
	}
	if err := checkLen("question", q, 2000); err != nil {
		return nil, err
	}
	set, err := s.workspaceAI(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	tid, err := resolveVisibleTicket(ctx, s.d.DB.Pool, p, rawID)
	if err != nil {
		return nil, err
	}
	tc, err := s.loadTicketCtx(ctx, s.d.DB.Pool, p.WorkspaceID, tid, false)
	if err != nil {
		return nil, err
	}
	sources, err := s.retrieveKB(ctx, p.WorkspaceID, ticketBrand(tc.T), q+"\n"+tc.T.Subject, 4, false)
	if err != nil {
		return nil, err
	}
	set.ReplyLanguage = "customer"
	req := Request{Model: s.models.Default, MaxTokens: 4000, Effort: "medium",
		System: []Block{{Text: askSystem, Cache: true}, {Text: guidance(tc.Workspace.Name, set, "concise", ""), Cache: true}},
		Messages: []Message{{Role: "user", Blocks: []Block{
			{Text: renderTicket(tc.T) + "\n" + renderContact(tc.Contact) + "\n" + renderKB(sources), Cache: true},
			{Text: renderThread(tc.Msgs, tc.Summary, threadChars)},
			{Text: block("agent_request", nil, "The agent's question: "+angle.Replace(q))},
		}}}}
	c := s.newCall(p.WorkspaceID, p.ActorUserID(), &tid, "copilot")
	resp, err := c.run(ctx, req, onDelta)
	if err != nil {
		return nil, err
	}
	h := markdownToHTML(resp.Text)
	if hd.HTMLToText(h) == "" {
		return nil, ErrBadOutput
	}
	c.charge(ctx)
	return &AskOutput{AnswerHTML: h, Sources: publicSources(sources)}, nil
}

// --- Summary -----------------------------------------------------------------------

type SummaryOutput struct {
	Summary   string    `json:"summary"`
	KeyPoints []string  `json:"key_points"`
	Sentiment string    `json:"sentiment" enum:"positive,neutral,negative,frustrated"`
	UpdatedAt time.Time `json:"updated_at"`
}

var summarySchema = obj(map[string]any{"summary": strT, "key_points": strArr, "sentiment": enumT(Sentiments)},
	"summary", "key_points", "sentiment")

// Summarize writes custom.ai_summary and publishes ticket.updated. userID is nil for
// automatic summaries.
func (s *Service) Summarize(ctx context.Context, wid uuid.UUID, userID *uuid.UUID, tid uuid.UUID) (*SummaryOutput, error) {
	set, err := s.workspaceAI(ctx, wid)
	if err != nil {
		return nil, err
	}
	tc, err := s.loadTicketCtx(ctx, s.d.DB.Pool, wid, tid, false)
	if err != nil {
		return nil, err
	}
	set.ReplyLanguage = "workspace"
	req := Request{Model: s.models.Default, MaxTokens: 3000, Effort: "low", JSONSchema: summarySchema, SchemaName: "ticket_summary",
		System: []Block{{Text: summarySystem, Cache: true}, {Text: guidance(tc.Workspace.Name, set, "concise", ""), Cache: true}},
		Messages: []Message{{Role: "user", Blocks: []Block{
			{Text: renderTicket(tc.T) + "\n" + renderContact(tc.Contact)},
			// The previous summary is not reused: a fresh summary reads the whole thread.
			{Text: renderThread(tc.Msgs, "", threadChars)},
			{Text: "Summarize this ticket as JSON."},
		}}}}
	c := s.newCall(wid, userID, &tid, "summarize")
	resp, err := c.run(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Summary   string   `json:"summary"`
		KeyPoints []string `json:"key_points"`
		Sentiment string   `json:"sentiment"`
	}
	if err := parseJSON(resp.Text, &out); err != nil {
		return nil, err
	}
	res := &SummaryOutput{Summary: cleanLine(out.Summary, 1000), KeyPoints: []string{}, Sentiment: out.Sentiment, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
	if res.Summary == "" {
		return nil, ErrBadOutput
	}
	for _, k := range out.KeyPoints {
		if k = cleanLine(k, 300); k != "" && len(res.KeyPoints) < 8 {
			res.KeyPoints = append(res.KeyPoints, k)
		}
	}
	if !slices.Contains(Sentiments, res.Sentiment) {
		res.Sentiment = "neutral"
	}
	stored := StoredSummary{Text: res.Summary, KeyPoints: res.KeyPoints, Sentiment: res.Sentiment, UpdatedAt: res.UpdatedAt,
		MessageCount: tc.T.MessageCount}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tickets SET custom = custom || jsonb_build_object('ai_summary', $3::jsonb, 'ai_sentiment', $4::text),
			updated_at = now() WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, wid, tid, stored, res.Sentiment)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Ticket")
		}
		return domain.PublishTicketUpdated(ctx, tx, wid, tid, []string{"custom"})
	})
	if err != nil {
		return nil, err
	}
	c.charge(ctx)
	return res, nil
}

// --- Rewrite and translate ----------------------------------------------------------

type RewriteInput struct {
	HTML         string `json:"html" minLength:"1" maxLength:"100000"`
	Mode         string `json:"mode" enum:"friendlier,more_formal,shorter,longer,fix_grammar,simplify"`
	Instructions string `json:"instructions,omitempty" maxLength:"1000"`
}

type RewriteOutput struct {
	HTML string `json:"html"`
}

func (s *Service) Rewrite(ctx context.Context, p *auth.Principal, in RewriteInput, onDelta func(string)) (*RewriteOutput, error) {
	mode, ok := rewriteModes[in.Mode]
	if !ok {
		return nil, httpx.Validation("mode must be one of friendlier, more_formal, shorter, longer, fix_grammar, simplify")
	}
	if err := checkLen("instructions", in.Instructions, 1000); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(in.HTML) > 100000 {
		return nil, httpx.Validation("html must be 100000 characters or less")
	}
	draft := hd.SanitizeRich(in.HTML)
	if hd.HTMLToText(draft) == "" {
		return nil, httpx.Validation("html is required")
	}
	if _, err := s.workspaceAI(ctx, p.WorkspaceID); err != nil {
		return nil, err
	}
	task := "Instruction: " + mode
	if ins := strings.TrimSpace(in.Instructions); ins != "" {
		task += "\n" + block("agent_request", nil, "Also: "+angle.Replace(ins))
	}
	req := Request{Model: s.models.Fast, MaxTokens: 6000, Effort: "low",
		System: []Block{{Text: rewriteSystem, Cache: true}},
		Messages: []Message{{Role: "user", Blocks: []Block{
			// The draft is HTML by design here; only the block delimiters are neutralized.
			{Text: "<draft>\n" + neutralizeDelims(draft) + "\n</draft>"},
			{Text: task + "\nReturn only the rewritten HTML."},
		}}}}
	c := s.newCall(p.WorkspaceID, p.ActorUserID(), nil, "rewrite")
	resp, err := c.run(ctx, req, onDelta)
	if err != nil {
		return nil, err
	}
	out := hd.SanitizeRich(stripFence(resp.Text))
	if !htmlLike.MatchString(out) {
		out = markdownToHTML(resp.Text)
	}
	if hd.HTMLToText(out) == "" {
		return nil, ErrBadOutput
	}
	c.charge(ctx)
	return &RewriteOutput{HTML: out}, nil
}

// neutralizeDelims defangs block tags in HTML that must otherwise stay HTML.
var delimTag = strings.NewReplacer("</draft", "‹/draft", "<draft", "‹draft", "</text", "‹/text", "<text", "‹text",
	"</agent_request", "‹/agent_request", "<agent_request", "‹agent_request", "<workspace_guidance", "‹workspace_guidance",
	"</workspace_guidance", "‹/workspace_guidance")

func neutralizeDelims(s string) string { return delimTag.Replace(s) }

type TranslateInput struct {
	HTML   *string `json:"html,omitempty" maxLength:"100000"`
	Text   *string `json:"text,omitempty" maxLength:"50000"`
	Target string  `json:"target" minLength:"2" maxLength:"35" doc:"BCP 47 language tag"`
}

type TranslateOutput struct {
	HTML           *string `json:"html,omitempty"`
	Text           *string `json:"text,omitempty"`
	SourceLanguage string  `json:"source_language"`
}

var translateSchema = obj(map[string]any{"translation": strT, "source_language": strT}, "translation", "source_language")

func (s *Service) Translate(ctx context.Context, p *auth.Principal, in TranslateInput) (*TranslateOutput, error) {
	if (in.HTML == nil) == (in.Text == nil) {
		return nil, httpx.Validation("send exactly one of html or text")
	}
	target := canonicalLang(in.Target)
	if target == "" || target == "und" {
		return nil, httpx.Validation("target must be a BCP 47 language tag like en, de or pt-BR")
	}
	var content string
	isHTML := in.HTML != nil
	if isHTML {
		content = hd.SanitizeRich(*in.HTML)
		if utf8.RuneCountInString(*in.HTML) > 100000 {
			return nil, httpx.Validation("html must be 100000 characters or less")
		}
	} else {
		content = strings.TrimSpace(*in.Text)
		if utf8.RuneCountInString(content) > 50000 {
			return nil, httpx.Validation("text must be 50000 characters or less")
		}
	}
	if strings.TrimSpace(hd.HTMLToText(content)) == "" {
		return nil, httpx.Validation("nothing to translate")
	}
	if _, err := s.workspaceAI(ctx, p.WorkspaceID); err != nil {
		return nil, err
	}
	body := neutralizeDelims(content)
	if !isHTML {
		body = untrusted(content)
	}
	format := "plain text"
	if isHTML {
		format = "HTML"
	}
	req := Request{Model: s.models.Fast, MaxTokens: 12000, Effort: "low", JSONSchema: translateSchema, SchemaName: "translation",
		System: []Block{{Text: translateSystem, Cache: true}},
		Messages: []Message{{Role: "user", Blocks: []Block{
			{Text: block("text", []string{"format", format}, body)},
			{Text: "Target language: " + target + ". Return JSON."},
		}}}}
	c := s.newCall(p.WorkspaceID, p.ActorUserID(), nil, "translate")
	resp, err := c.run(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Translation    string `json:"translation"`
		SourceLanguage string `json:"source_language"`
	}
	if err := parseJSON(resp.Text, &out); err != nil {
		return nil, err
	}
	res := &TranslateOutput{SourceLanguage: canonicalLang(out.SourceLanguage)}
	if res.SourceLanguage == "" {
		res.SourceLanguage = "und"
	}
	if isHTML {
		h := hd.SanitizeRich(out.Translation)
		if hd.HTMLToText(h) == "" {
			return nil, ErrBadOutput
		}
		res.HTML = &h
	} else {
		t := strings.TrimSpace(hd.HTMLToText(out.Translation))
		if t == "" {
			return nil, ErrBadOutput
		}
		res.Text = &t
	}
	c.charge(ctx)
	return res, nil
}

// --- KB article draft ---------------------------------------------------------------

type KBDraftInput struct {
	TicketIDs []string `json:"ticket_ids,omitempty" maxItems:"10"`
	Topic     string   `json:"topic,omitempty" maxLength:"300"`
}

type KBDraftOutput struct {
	Title    string `json:"title"`
	BodyHTML string `json:"body_html"`
	Excerpt  string `json:"excerpt"`
}

var kbDraftSchema = obj(map[string]any{"title": strT, "body_html": strT, "excerpt": strT}, "title", "body_html", "excerpt")

func (s *Service) KBDraft(ctx context.Context, p *auth.Principal, in KBDraftInput) (*KBDraftOutput, error) {
	topic := strings.Join(strings.Fields(in.Topic), " ")
	if len(in.TicketIDs) == 0 && topic == "" {
		return nil, httpx.Validation("send ticket_ids, a topic, or both")
	}
	if len(in.TicketIDs) > 10 {
		return nil, httpx.Validation("at most 10 tickets")
	}
	if err := checkLen("topic", topic, 300); err != nil {
		return nil, err
	}
	set, err := s.workspaceAI(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	var wsName, wsSlug string
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT name, slug FROM workspaces WHERE id = $1`, p.WorkspaceID).Scan(&wsName, &wsSlug); err != nil {
		return nil, err
	}
	var parts []string
	seen := map[uuid.UUID]bool{}
	for _, raw := range in.TicketIDs {
		id, err := hd.ParseRef(raw, "ticket_ids")
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := resolveVisibleTicket(ctx, s.d.DB.Pool, p, id.String()); isNotFound(err) {
			return nil, httpx.Validation("ticket not found in this workspace")
		} else if err != nil {
			return nil, err
		}
		tc, err := s.loadTicketCtx(ctx, s.d.DB.Pool, p.WorkspaceID, id, true)
		if isNotFound(err) {
			return nil, httpx.Validation("ticket not found in this workspace")
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, block("resolved_ticket", []string{"subject", tc.T.Subject}, renderThread(tc.Msgs, "", 12000)))
	}
	tickets := block("tickets", nil, strings.Join(parts, "\n"))
	if len(parts) == 0 {
		tickets = block("tickets", nil, "(none: write from the topic and the knowledge base only)")
	}
	related, err := s.retrieveKB(ctx, p.WorkspaceID, nil, topic+" "+strings.Join(parts, " "), 3, false)
	if err != nil {
		return nil, err
	}
	set.ReplyLanguage = "workspace"
	task := "Write the article as JSON."
	if topic != "" {
		task = block("agent_request", nil, "Topic: "+angle.Replace(topic)) + "\n" + task
	}
	if len(related) > 0 {
		task += " Existing related articles are in <knowledge_base>: do not duplicate them; link to them where useful."
	}
	req := Request{Model: s.models.Default, MaxTokens: 10000, Effort: "medium", JSONSchema: kbDraftSchema, SchemaName: "kb_article",
		System:   []Block{{Text: kbDraftSystem, Cache: true}, {Text: guidance(wsName, set, "professional", ""), Cache: true}},
		Messages: []Message{{Role: "user", Blocks: []Block{{Text: renderKB(related)}, {Text: tickets}, {Text: task}}}}}
	c := s.newCall(p.WorkspaceID, p.ActorUserID(), nil, "kb_draft")
	resp, err := c.run(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	var out KBDraftOutput
	if err := parseJSON(resp.Text, &out); err != nil {
		return nil, err
	}
	out.Title = tidyText(cleanLine(out.Title, 200))
	out.BodyHTML = tidyHTML(hd.SanitizeRich(out.BodyHTML))
	out.Excerpt = tidyText(cleanLine(out.Excerpt, 300))
	if out.Title == "" || hd.HTMLToText(out.BodyHTML) == "" {
		return nil, ErrBadOutput
	}
	c.charge(ctx)
	return &out, nil
}
