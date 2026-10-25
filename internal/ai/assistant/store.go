package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Session is one conversation (contracts/ai.md, Assistant).
type Session struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PageContext is what the panel says about the page the user is on.
type PageContext struct {
	Path     string            `json:"path,omitempty" maxLength:"300"`
	Page     string            `json:"page,omitempty" maxLength:"60" doc:"inbox, ticket, view, contacts, kb, reports, settings, other"`
	TicketID string            `json:"ticket_id,omitempty" maxLength:"64" doc:"The open ticket (uuid)"`
	ViewID   string            `json:"view_id,omitempty" maxLength:"64"`
	Folder   string            `json:"folder,omitempty" maxLength:"60"`
	Filters  map[string]string `json:"filters,omitempty" maxProperties:"20"`
}

// turnBody is a stored turn. User turns hold Text (and Context) or Results; assistant
// turns hold Text and Calls. Tools carries per-call display data for the panel.
type turnBody struct {
	Text    string       `json:"text,omitempty"`
	Context *PageContext `json:"context,omitempty"`
	// Prefix is the rendered page context block sent ahead of Text.
	Prefix  string          `json:"prefix,omitempty"`
	Calls   []ai.ToolCall   `json:"calls,omitempty"`
	Results []storedResult  `json:"results,omitempty"`
	Tools   map[string]Step `json:"tools,omitempty"`
}

type storedResult struct {
	ai.ToolResult
	Pending bool `json:"pending,omitempty"`
}

// Step is how the panel shows one tool call.
type Step struct {
	ID       string     `json:"id"`
	Tool     string     `json:"tool"`
	Label    string     `json:"label"`
	Status   string     `json:"status" enum:"running,done,error,awaiting_approval,approved,denied,expired"`
	Summary  string     `json:"summary,omitempty"`
	Refs     []Ref      `json:"refs,omitempty"`
	ActionID *uuid.UUID `json:"action_id,omitempty"`
	// Peer is set on an ask of a teammate's Ollie: whose, and how it went.
	Peer *PeerStep `json:"peer,omitempty"`
	// Auto marks a change that ran without asking, because the workspace allows it;
	// Preview is what it ran.
	Auto    bool     `json:"auto,omitempty"`
	Preview *Preview `json:"preview,omitempty"`
}

// Ref is a clickable reference to something in the workspace.
type Ref struct {
	Kind   string `json:"kind" enum:"ticket,contact,article,view,automation,member,external" doc:"external: ID is the https link"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Number int64  `json:"number,omitempty"`
}

type turn struct {
	Seq  int
	Role string
	Body turnBody
}

func loadSession(ctx context.Context, q db.Querier, wid, uid, sid uuid.UUID, lock bool) (Session, error) {
	sql := `SELECT id, title, created_at, updated_at FROM assistant_sessions WHERE workspace_id = $1 AND user_id = $2 AND id = $3`
	if lock {
		sql += ` FOR UPDATE`
	}
	var s Session
	err := q.QueryRow(ctx, sql, wid, uid, sid).Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt)
	if db.IsNotFound(err) {
		return s, httpx.NotFound("Conversation")
	}
	return s, err
}

func createSession(ctx context.Context, q db.Querier, wid, uid uuid.UUID, title string) (Session, error) {
	s := Session{ID: ids.New(), Title: cleanTitle(title)}
	err := q.QueryRow(ctx, `INSERT INTO assistant_sessions (id, workspace_id, user_id, title) VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at`, s.ID, wid, uid, s.Title).Scan(&s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func loadTurns(ctx context.Context, q db.Querier, wid, sid uuid.UUID) ([]turn, error) {
	rows, err := q.Query(ctx, `SELECT seq, role, body FROM assistant_turns WHERE workspace_id = $1 AND session_id = $2 ORDER BY seq`, wid, sid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (turn, error) {
		var t turn
		var raw []byte
		if err := r.Scan(&t.Seq, &t.Role, &raw); err != nil {
			return t, err
		}
		_ = json.Unmarshal(raw, &t.Body)
		return t, nil
	})
}

func appendTurn(ctx context.Context, q db.Querier, wid, sid uuid.UUID, role string, b turnBody) (int, error) {
	var seq int
	err := q.QueryRow(ctx, `INSERT INTO assistant_turns (session_id, workspace_id, seq, role, body)
		VALUES ($1, $2, (SELECT COALESCE(max(seq), 0) + 1 FROM assistant_turns WHERE session_id = $1), $3, $4) RETURNING seq`,
		sid, wid, role, b).Scan(&seq)
	if err == nil {
		_, err = q.Exec(ctx, `UPDATE assistant_sessions SET updated_at = now() WHERE id = $1`, sid)
	}
	return seq, err
}

func updateTurn(ctx context.Context, q db.Querier, wid, sid uuid.UUID, seq int, b turnBody) error {
	_, err := q.Exec(ctx, `UPDATE assistant_turns SET body = $4 WHERE workspace_id = $1 AND session_id = $2 AND seq = $3`, wid, sid, seq, b)
	return err
}

// cleanTitle keeps a title to one line of 80 characters.
func cleanTitle(s string) string {
	s = strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > 80 {
		s = strings.TrimSpace(string([]rune(s)[:79])) + "…"
	}
	return s
}

// deriveTitle is a first-message title: the first sentence, shortened.
func deriveTitle(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if i := strings.IndexAny(t, ".?!\n"); i > 12 {
		t = t[:i]
	}
	return cleanTitle(t)
}

// Action is a proposed write waiting on, or decided by, the user.
type action struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	TurnSeq   int
	CallID    string
	Tool      string
	Args      json.RawMessage
	Preview   Preview
	Status    string
	ExpiresAt time.Time
}

func insertAction(ctx context.Context, q db.Querier, wid, uid uuid.UUID, a *action) error {
	a.ID = ids.New()
	a.Status = "pending"
	a.ExpiresAt = time.Now().Add(approvalTTL)
	_, err := q.Exec(ctx, `INSERT INTO assistant_actions (id, workspace_id, user_id, session_id, turn_seq, call_id, tool, args, preview, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, a.ID, wid, uid, a.SessionID, a.TurnSeq, a.CallID, a.Tool, a.Args, a.Preview, a.ExpiresAt)
	return err
}

// insertAutoAction records a change that runs without asking, as it starts: never
// pending, so no decision can claim it. finishAction ends it done or failed.
func insertAutoAction(ctx context.Context, q db.Querier, wid, uid uuid.UUID, a *action) error {
	a.ID = ids.New()
	a.Status = "approved"
	a.ExpiresAt = time.Now()
	_, err := q.Exec(ctx, `INSERT INTO assistant_actions (id, workspace_id, user_id, session_id, turn_seq, call_id, tool, args, preview,
		status, auto, decided_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'approved', true, now(), $10)`,
		a.ID, wid, uid, a.SessionID, a.TurnSeq, a.CallID, a.Tool, a.Args, a.Preview, a.ExpiresAt)
	return err
}

// autoActionsSince counts the changes that ran without asking since the session's
// latest message from the user.
func autoActionsSince(ctx context.Context, q db.Querier, wid, sid uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM assistant_actions WHERE workspace_id = $1 AND session_id = $2 AND auto
		AND turn_seq > COALESCE((SELECT max(seq) FROM assistant_turns WHERE session_id = $2 AND role = 'user' AND body ? 'text'), 0)`,
		wid, sid).Scan(&n)
	return n, err
}

// claimAction moves a pending, unexpired action of this user to decision, once. It is
// the single gate between a proposal and its execution.
func claimAction(ctx context.Context, q db.Querier, wid, uid, id uuid.UUID, decision string) (action, error) {
	var a action
	var preview []byte
	err := q.QueryRow(ctx, `UPDATE assistant_actions SET status = $4, decided_at = now()
		WHERE workspace_id = $1 AND user_id = $2 AND id = $3 AND status = 'pending' AND expires_at > now()
		RETURNING id, session_id, turn_seq, call_id, tool, args, preview, status, expires_at`, wid, uid, id, decision).
		Scan(&a.ID, &a.SessionID, &a.TurnSeq, &a.CallID, &a.Tool, &a.Args, &preview, &a.Status, &a.ExpiresAt)
	if db.IsNotFound(err) {
		return a, errNotPending
	}
	_ = json.Unmarshal(preview, &a.Preview)
	return a, err
}

func finishAction(ctx context.Context, q db.Querier, id uuid.UUID, status string, result any) error {
	_, err := q.Exec(ctx, `UPDATE assistant_actions SET status = $2, result = $3 WHERE id = $1`, id, status, result)
	return err
}

// expirePending closes a session's open proposals (a new message moves on from them).
func expirePending(ctx context.Context, q db.Querier, wid, sid uuid.UUID) ([]action, error) {
	rows, err := q.Query(ctx, `UPDATE assistant_actions SET status = 'expired', decided_at = now()
		WHERE workspace_id = $1 AND session_id = $2 AND status = 'pending' RETURNING id, turn_seq, call_id, tool`, wid, sid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (action, error) {
		var a action
		err := r.Scan(&a.ID, &a.TurnSeq, &a.CallID, &a.Tool)
		return a, err
	})
}

func pendingAction(ctx context.Context, q db.Querier, wid, sid uuid.UUID) (*action, error) {
	var a action
	var preview []byte
	err := q.QueryRow(ctx, `SELECT id, session_id, turn_seq, call_id, tool, args, preview, status, expires_at FROM assistant_actions
		WHERE workspace_id = $1 AND session_id = $2 AND status = 'pending' AND expires_at > now() ORDER BY created_at DESC LIMIT 1`, wid, sid).
		Scan(&a.ID, &a.SessionID, &a.TurnSeq, &a.CallID, &a.Tool, &a.Args, &preview, &a.Status, &a.ExpiresAt)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(preview, &a.Preview)
	return &a, nil
}

func actionStatuses(ctx context.Context, q db.Querier, wid, sid uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := q.Query(ctx, `SELECT id, CASE WHEN status = 'pending' AND expires_at <= now() THEN 'expired' ELSE status END
		FROM assistant_actions WHERE workspace_id = $1 AND session_id = $2`, wid, sid)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var st string
		if err := rows.Scan(&id, &st); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = st
	}
	rows.Close()
	return out, rows.Err()
}
