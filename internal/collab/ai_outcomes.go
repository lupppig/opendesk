package collab

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// AI draft outcomes (contracts/collab.md "AI draft outcomes", GH#261): opendesk keeps the
// AI's last saved text of each AI draft and records what became of it, so the key that
// wrote it can read back how it did. The server sets the outcome, never the client; a
// person may add feedback, which never blocks a send. opendesk does not train, score or
// change any model.

const (
	OutcomeSentUnchanged = "sent_unchanged"
	OutcomeSentEdited    = "sent_edited"
	OutcomeDiscarded     = "discarded"
	OutcomeReplaced      = "replaced"
	OutcomeAbandoned     = "abandoned"

	maxClientRef      = 200
	maxFeedbackNote   = 1000
	outcomesDefault   = 50
	outcomesMax       = 200
	outcomesPerMinute = 60
)

var errOutcomesKeysOnly = httpx.E(403, "ai_outcomes_keys_only", "AI draft outcomes are read by the API key that wrote the drafts.")

// lockOutcomeCursor orders allocations of visible feed sequences by commit. Hold it
// until tx ends, before locking outcome rows or publishing events. Account locks come
// first; provider calls finish before this transaction starts. New, unsettled drafts
// may allocate sequences without this lock because they are absent from the feed.
func lockOutcomeCursor(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('collab_outcome_cursor', 0))`)
	return err
}

// AIDraftRef is, on a message, the AI draft it is or came from.
type AIDraftRef struct {
	ID uuid.UUID `json:"id"`
	// Client is the key's name when it last saved the draft.
	Client  string  `json:"client"`
	Outcome *string `json:"outcome"`
	// HasFeedback reports that a person has given feedback on it.
	HasFeedback bool `json:"has_feedback"`
}

// AIDraftOutcome is one item of a key's outcome feed.
type AIDraftOutcome struct {
	ID             uuid.UUID  `json:"id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	DraftID        uuid.UUID  `json:"draft_id"`
	Client         string     `json:"client"`
	ClientRef      string     `json:"client_ref"`
	AIBodyHTML     string     `json:"ai_body_html"`
	AISavedAt      time.Time  `json:"ai_saved_at"`
	TakenOverAt    *time.Time `json:"taken_over_at"`
	Outcome        *string    `json:"outcome"`
	OutcomeAt      *time.Time `json:"outcome_at"`
	// SentMessageID is the message sent instead of or from the draft; read it with the
	// conversation (GET /v1/collab/threads/{id}).
	SentMessageID *uuid.UUID `json:"sent_message_id"`
	Feedback      *struct {
		Reason           string    `json:"reason"`
		Note             string    `json:"note"`
		BetterAnswerHTML string    `json:"better_answer_html"`
		At               time.Time `json:"at"`
	} `json:"feedback"`
	Cursor string `json:"cursor"`
}

// recordAISave keeps the AI's text of a draft it just saved (message id), in tx. A draft a
// person took over, or that already has an outcome, is not the AI's to change.
func recordAISave(ctx context.Context, tx pgx.Tx, id uuid.UUID, ai *aiDrafter) error {
	_, err := tx.Exec(ctx, `INSERT INTO collab_ai_drafts (id, workspace_id, account_id, thread_id, api_key_id, member_id, client,
			draft_message_id, client_ref, ai_body_html)
		SELECT $2, m.workspace_id, m.account_id, m.thread_id, $3, $4, $5, m.id, $6, m.body_html
		FROM collab_messages m WHERE m.id = $1 AND m.draft
		ON CONFLICT (draft_message_id) DO UPDATE SET client = EXCLUDED.client, thread_id = EXCLUDED.thread_id,
			client_ref = CASE WHEN EXCLUDED.client_ref = '' THEN collab_ai_drafts.client_ref ELSE EXCLUDED.client_ref END,
			ai_body_html = EXCLUDED.ai_body_html, ai_saved_at = now()
		WHERE collab_ai_drafts.taken_over_at IS NULL AND collab_ai_drafts.outcome IS NULL
			AND collab_ai_drafts.api_key_id = EXCLUDED.api_key_id`,
		id, ids.New(), ai.APIKeyID, ai.P.UserID, ai.Client, clipRunes(strings.TrimSpace(ai.Ref), maxClientRef))
	return err
}

// markTakenOver records a person's first save of an AI draft (message id), in tx.
func markTakenOver(ctx context.Context, tx pgx.Tx, id, by uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE collab_ai_drafts SET taken_over_by = $2, taken_over_at = now()
		WHERE draft_message_id = $1 AND taken_over_at IS NULL`, id, by)
	return err
}

// settleSent records, in tx, a message a person just sent (sent): the AI draft it was
// sent from (draft, when set) is sent_unchanged or sent_edited, by its words against the
// AI's; any other AI draft of the conversation still open is replaced. A draft already
// replaced or abandoned that is then sent takes its final outcome.
func settleSent(ctx context.Context, tx pgx.Tx, draft *uuid.UUID, sent, by uuid.UUID) error {
	if err := lockOutcomeCursor(ctx, tx); err != nil {
		return err
	}
	var thread uuid.UUID
	var sentHTML string
	if err := tx.QueryRow(ctx, `SELECT thread_id, body_html FROM collab_messages WHERE id = $1`, sent).Scan(&thread, &sentHTML); err != nil {
		return err
	}
	exclude := uuid.Nil
	if draft != nil {
		exclude = *draft
		var rec uuid.UUID
		var aiHTML string
		err := tx.QueryRow(ctx, `SELECT id, ai_body_html FROM collab_ai_drafts WHERE draft_message_id = $1
			AND (outcome IS NULL OR outcome IN ('replaced', 'abandoned')) FOR UPDATE`, *draft).Scan(&rec, &aiHTML)
		switch {
		case db.IsNotFound(err):
		case err != nil:
			return err
		default:
			outcome := OutcomeSentEdited
			if draftWords(aiHTML) == draftWords(sentHTML) {
				outcome = OutcomeSentUnchanged
			}
			if _, err := tx.Exec(ctx, `UPDATE collab_ai_drafts SET outcome = $2, outcome_at = now(), outcome_by = $3,
				sent_message_id = $4, seq = nextval('collab_ai_drafts_seq') WHERE id = $1`, rec, outcome, by, sent); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(ctx, `UPDATE collab_ai_drafts SET outcome = 'replaced', outcome_at = now(), outcome_by = $3,
		sent_message_id = $4, seq = nextval('collab_ai_drafts_seq')
		WHERE thread_id = $1 AND outcome IS NULL AND draft_message_id <> $2`, thread, exclude, by, sent)
	return err
}

// settleDiscarded records a person discarding an AI draft (message id), in tx.
func settleDiscarded(ctx context.Context, tx pgx.Tx, id, by uuid.UUID) error {
	if err := lockOutcomeCursor(ctx, tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE collab_ai_drafts SET outcome = 'discarded', outcome_at = now(), outcome_by = $2,
		seq = nextval('collab_ai_drafts_seq')
		WHERE draft_message_id = $1 AND (outcome IS NULL OR outcome IN ('replaced', 'abandoned'))`, id, by)
	return err
}

// settleDone records, in tx, conversations a person marked done: their AI drafts still
// open were abandoned (sending or discarding one later still records that).
func settleDone(ctx context.Context, tx pgx.Tx, threads []uuid.UUID, by uuid.UUID) error {
	if err := lockOutcomeCursor(ctx, tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE collab_ai_drafts SET outcome = 'abandoned', outcome_at = now(), outcome_by = $2,
		seq = nextval('collab_ai_drafts_seq')
		WHERE thread_id = ANY($1) AND outcome IS NULL`, threads, by)
	return err
}

// draftWords is what a person wrote in a draft's HTML, to tell an edit from a send as
// saved: its text without the signature block and quoted history, spaces collapsed.
func draftWords(html string) string {
	doc := email.ParseHTML(html)
	if doc == nil {
		return strings.Join(strings.Fields(email.HTMLToText(html)), " ")
	}
	var strip func(n *xhtml.Node)
	strip = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == xhtml.ElementNode && (c.DataAtom == atom.Blockquote || hasClass(c, "od-signature") ||
				hasClass(c, "gmail_quote") || hasClass(c, "gmail_signature")) {
				n.RemoveChild(c)
			} else {
				strip(c)
			}
			c = next
		}
	}
	strip(doc)
	return strings.Join(strings.Fields(email.HTMLToText(email.RenderHTML(doc))), " ")
}

func hasClass(n *xhtml.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// aiDraftRefs finds the AI drafts the given messages are (a draft) or came from (a sent
// message), by message id.
func aiDraftRefs(ctx context.Context, q db.Querier, list []*messageRow) (map[uuid.UUID]*AIDraftRef, error) {
	out := map[uuid.UUID]*AIDraftRef{}
	var drafts, sent []uuid.UUID
	for _, m := range list {
		if m.Draft {
			drafts = append(drafts, m.ID)
		} else if m.SentBy != nil {
			sent = append(sent, m.ID)
		}
	}
	if len(drafts)+len(sent) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, draft_message_id, sent_message_id, outcome, client, feedback_at IS NOT NULL
		FROM collab_ai_drafts
		WHERE draft_message_id = ANY($1)
			OR (sent_message_id = ANY($2) AND outcome IN ('sent_unchanged', 'sent_edited'))`, drafts, sent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r AIDraftRef
		var draft uuid.UUID
		var sentID *uuid.UUID
		if err := rows.Scan(&r.ID, &draft, &sentID, &r.Outcome, &r.Client, &r.HasFeedback); err != nil {
			return nil, err
		}
		if sentID != nil && r.Outcome != nil && strings.HasPrefix(*r.Outcome, "sent_") {
			out[*sentID] = &r
		} else {
			out[draft] = &r
		}
	}
	return out, rows.Err()
}

func (s *service) registerOutcomeRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "list-collab-ai-draft-outcomes", Method: http.MethodGet,
		Path: "/v1/collab/ai-drafts/outcomes", Summary: "Read what became of this key's AI drafts"},
		func(ctx context.Context, in *struct {
			Cursor string `query:"cursor" doc:"cursor of the last item read; left out, from the start"`
			Limit  int    `query:"limit" minimum:"0" maximum:"200" doc:"How many (1 to 200; default 50)"`
		}) (*struct {
			Body struct {
				Data       []AIDraftOutcome `json:"data"`
				NextCursor *string          `json:"next_cursor"`
			}
		}, error) {
			p, err := s.requireDrafter(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !viaAIClient(p) {
				return nil, errOutcomesKeysOnly
			}
			if !s.d.Limiter.Allow("collab-ai-outcomes:"+p.APIKeyID.String(), outcomesPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			var after int64
			if in.Cursor != "" {
				if after, err = strconv.ParseInt(in.Cursor, 10, 64); err != nil || after < 0 {
					return nil, httpx.Validation("cursor is not one this feed gave.")
				}
			}
			limit := in.Limit
			if limit == 0 {
				limit = outcomesDefault
			}
			limit = min(limit, outcomesMax)
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT d.id, d.thread_id, d.draft_message_id, d.client, d.client_ref, d.ai_body_html,
					d.ai_saved_at, d.taken_over_at, d.outcome, d.outcome_at, d.sent_message_id, d.feedback_reason, d.feedback_note,
					d.better_answer_html, d.feedback_at, d.seq
				FROM collab_ai_drafts d JOIN collab_threads t ON t.id = d.thread_id
				WHERE d.workspace_id = $1 AND d.api_key_id = $3 AND d.seq > $4
					AND (d.outcome IS NOT NULL OR d.feedback_at IS NOT NULL) AND `+threadVisibleSQL("t", "$2")+`
				ORDER BY d.seq LIMIT $5`, p.WorkspaceID, p.UserID, p.APIKeyID, after, limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AIDraftOutcome, error) {
				var o AIDraftOutcome
				var reason *string
				var note, better string
				var at *time.Time
				var seq int64
				if err := r.Scan(&o.ID, &o.ConversationID, &o.DraftID, &o.Client, &o.ClientRef, &o.AIBodyHTML, &o.AISavedAt,
					&o.TakenOverAt, &o.Outcome, &o.OutcomeAt, &o.SentMessageID, &reason, &note, &better, &at, &seq); err != nil {
					return o, err
				}
				if reason != nil && at != nil {
					o.Feedback = &struct {
						Reason           string    `json:"reason"`
						Note             string    `json:"note"`
						BetterAnswerHTML string    `json:"better_answer_html"`
						At               time.Time `json:"at"`
					}{*reason, note, better, *at}
				}
				o.Cursor = strconv.FormatInt(seq, 10)
				return o, nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data       []AIDraftOutcome `json:"data"`
					NextCursor *string          `json:"next_cursor"`
				}
			}{}
			if len(list) > limit {
				list = list[:limit]
				c := list[len(list)-1].Cursor
				out.Body.NextCursor = &c
			}
			if list == nil {
				list = []AIDraftOutcome{}
			}
			out.Body.Data = list
			return out, nil
		})

	collabOp(api, huma.Operation{OperationID: "collab-ai-draft-feedback", Method: http.MethodPut,
		Path: "/v1/collab/ai-drafts/{id}/feedback", Summary: "Say how an AI draft could be better", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID   string `path:"id" doc:"The AI draft (ai_draft.id on its message)"`
			Body struct {
				Reason           string `json:"reason" enum:"wrong_facts,wrong_tone,missing_info,should_not_answer,other"`
				Note             string `json:"note,omitempty" maxLength:"1000"`
				BetterAnswerHTML string `json:"better_answer_html,omitempty" maxLength:"100000"`
			}
		}) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "AI draft")
			if err != nil {
				return nil, err
			}
			var thread uuid.UUID
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT thread_id FROM collab_ai_drafts WHERE id = $1 AND workspace_id = $2`,
				id, p.WorkspaceID).Scan(&thread); err != nil {
				return nil, notFound(err, "AI draft")
			}
			if _, err := visibleThread(ctx, s.d.DB.Pool, p, thread); err != nil {
				return nil, notFound(err, "AI draft")
			}
			b := in.Body
			better := ""
			if strings.TrimSpace(b.BetterAnswerHTML) != "" {
				better = email.SanitizeHTML(b.BetterAnswerHTML)
				if email.HTMLToText(better) == "" {
					better = ""
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockOutcomeCursor(ctx, tx); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE collab_ai_drafts SET feedback_reason = $2, feedback_note = $3, better_answer_html = $4,
					feedback_by = $5, feedback_at = now(), seq = nextval('collab_ai_drafts_seq') WHERE id = $1`,
					id, b.Reason, clipRunes(strings.TrimSpace(b.Note), maxFeedbackNote), better, p.UserID); err != nil {
					return err
				}
				// The words stay out of the log: the entry names the draft and the reason.
				return audit.Log(ctx, tx, p, p.WorkspaceID, "collab.ai_draft_feedback", "collab_ai_draft", &id,
					map[string]any{"reason": b.Reason, "thread_id": thread, "better_answer": better != ""})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}
