package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/integrations"
)

// AssistantMessageInput is the body of POST /v1/assistant/sessions/{id}/messages.
// The explicit name also keeps its generated OpenAPI schema distinct from the
// helpdesk ticket message input registered on the same Huma API.
type AssistantMessageInput struct {
	Text    string       `json:"text" minLength:"1" maxLength:"8000"`
	Context *PageContext `json:"context,omitempty"`
}

// DecisionInput is the body of POST /v1/assistant/actions/{id}/decision.
type DecisionInput struct {
	Decision string `json:"decision" enum:"approve,deny"`
	Token    string `json:"token" minLength:"1" maxLength:"4000" doc:"The token of the approval event"`
}

// Approval is a proposed change waiting for the user.
type Approval struct {
	ActionID  uuid.UUID `json:"action_id"`
	CallID    string    `json:"call_id"`
	Tool      string    `json:"tool"`
	Preview   Preview   `json:"preview"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Item is one transcript entry for the panel.
type Item struct {
	Role    string       `json:"role" enum:"user,assistant"`
	Text    string       `json:"text"`
	Steps   []Step       `json:"steps"`
	Context *PageContext `json:"context,omitempty"`
}

// RunResult is a run collected into one response (requests without event-stream).
type RunResult struct {
	Session  *Session  `json:"session"`
	Text     string    `json:"text"`
	Steps    []Step    `json:"steps"`
	Approval *Approval `json:"approval"`
	Status   string    `json:"status" enum:"complete,awaiting_approval"`
}

type emitFn func(event string, data any)

type collector struct {
	mu  sync.Mutex
	res RunResult
	idx map[string]int
}

func (c *collector) emit(event string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event {
	case "session":
		s := data.(Session)
		c.res.Session = &s
	case "text":
		c.res.Text += data.(map[string]string)["text"]
	case "tool":
		st := data.(Step)
		if c.idx == nil {
			c.idx = map[string]int{}
		}
		if i, ok := c.idx[st.ID]; ok {
			c.res.Steps[i] = st
		} else {
			c.idx[st.ID] = len(c.res.Steps)
			c.res.Steps = append(c.res.Steps, st)
		}
	case "approval":
		a := data.(Approval)
		c.res.Approval = &a
	case "done":
		c.res.Status = data.(map[string]string)["status"]
	}
}

func (c *collector) result() RunResult {
	if c.res.Steps == nil {
		c.res.Steps = []Step{}
	}
	return c.res
}

// --- SSE -----------------------------------------------------------------------------

func (s *Service) sse(h func(w http.ResponseWriter, r *http.Request, id string)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !strings.Contains(ctx.Header("Accept"), "text/event-stream") {
			next(ctx)
			return
		}
		r, w := humachi.Unwrap(ctx)
		h(w, r, ctx.Param("id"))
	}
}

// stream writes events lazily, so an error before the first one is a normal problem
// response with its status.
type stream struct {
	w       http.ResponseWriter
	r       *http.Request
	rc      *http.ResponseController
	mu      sync.Mutex
	started bool
}

func (e *stream) send(event string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		h := e.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		_ = e.rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
		e.w.WriteHeader(http.StatusOK)
		e.started = true
	}
	raw, _ := json.Marshal(data)
	_, _ = io.WriteString(e.w, "event: "+event+"\ndata: "+string(raw)+"\n\n")
	_ = e.rc.Flush()
}

func problem(err error) *httpx.Error {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he
	}
	var se huma.StatusError
	if errors.As(err, &se) && se.GetStatus() < 500 {
		return httpx.E(se.GetStatus(), "", err.Error())
	}
	return httpx.E(http.StatusInternalServerError, "internal", "Something went wrong on our side. Quote the request id if you contact support.")
}

func (e *stream) fail(err error) {
	he := problem(err)
	he.RequestID = httpx.RequestIDFrom(e.r.Context())
	e.mu.Lock()
	started := e.started
	e.mu.Unlock()
	if started {
		e.send("error", he)
		return
	}
	e.w.Header().Set("Content-Type", "application/problem+json")
	e.w.WriteHeader(he.Status)
	_ = json.NewEncoder(e.w).Encode(he)
}

func readBody(r *http.Request, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil {
		return httpx.BadRequest("could not read the request body")
	}
	if len(raw) > 64<<10 {
		return httpx.E(http.StatusRequestEntityTooLarge, "too_large", "request body too large")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return httpx.E(http.StatusUnprocessableEntity, "validation_failed", "request body must be valid JSON")
	}
	return nil
}

func (s *Service) streamMessage(w http.ResponseWriter, r *http.Request, id string) {
	es := &stream{w: w, r: r, rc: http.NewResponseController(w)}
	sid, err := uuid.Parse(id)
	if err != nil {
		es.fail(httpx.NotFound("Conversation"))
		return
	}
	var in AssistantMessageInput
	if err := readBody(r, &in); err != nil {
		es.fail(err)
		return
	}
	if err := s.sendMessage(r.Context(), sid, in, es.send); err != nil && !errors.Is(err, context.Canceled) {
		es.fail(err)
	}
}

func (s *Service) streamDecision(w http.ResponseWriter, r *http.Request, id string) {
	es := &stream{w: w, r: r, rc: http.NewResponseController(w)}
	aid, err := uuid.Parse(id)
	if err != nil {
		es.fail(errNotPending)
		return
	}
	var in DecisionInput
	if err := readBody(r, &in); err != nil {
		es.fail(err)
		return
	}
	if err := s.decide(r.Context(), aid, in, es.send); err != nil && !errors.Is(err, context.Canceled) {
		es.fail(err)
	}
}

// --- runs ----------------------------------------------------------------------------

func (s *Service) newRun(ctx context.Context, p *auth.Principal, page *PageContext) *runCtx {
	return &runCtx{svc: s, p: p, read: newCaller(s.api, p, false), page: page}
}

func (s *Service) sendMessage(ctx context.Context, sid uuid.UUID, in AssistantMessageInput, emit emitFn) error {
	p, err := s.require(ctx)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(strings.ToValidUTF8(in.Text, ""))
	if text == "" || utf8.RuneCountInString(text) > 8000 {
		return httpx.Validation("text must be 1 to 8,000 characters")
	}
	if !s.d.Limiter.Allow("assistant:"+p.UserID.String(), 30, time.Minute) {
		return httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many messages. Wait a moment.")
	}
	sess, err := loadSession(ctx, s.d.DB.Pool, p.WorkspaceID, p.UserID, sid, false)
	if err != nil {
		return httpx.Wrap(err)
	}
	// A balance too low for a turn is answered before anything is stored or streamed (a workspace on
	// its own key needs none).
	if err := s.ai.Reserve(ctx, p.WorkspaceID, "assistant"); err != nil {
		return ai.APIError(err)
	}
	if !s.begin(sid) {
		return errBusy
	}
	defer s.end(sid)
	rc := s.newRun(ctx, p, in.Context)

	// Page context: the open ticket is described once, when the message is sent.
	prefix := ""
	if in.Context != nil {
		line := ""
		if in.Context.TicketID != "" {
			if t, err := rc.getTicket(ctx, in.Context.TicketID); err == nil {
				line = t.line()
			}
		}
		prefix = contextBlock(in.Context, line)
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// A new message moves on from changes still waiting for approval.
		expired, err := expirePending(ctx, tx, p.WorkspaceID, sid)
		if err != nil {
			return err
		}
		for _, a := range expired {
			if err := resolvePending(ctx, tx, p.WorkspaceID, sid, a.TurnSeq, a.CallID,
				"The user sent a new message instead of approving, so this change was not made."); err != nil {
				return err
			}
		}
		if _, err := appendTurn(ctx, tx, p.WorkspaceID, sid, "user", turnBody{Text: text, Context: in.Context, Prefix: prefix}); err != nil {
			return err
		}
		if sess.Title == "" {
			sess.Title = deriveTitle(text)
			_, err = tx.Exec(ctx, `UPDATE assistant_sessions SET title = $2 WHERE id = $1`, sid, sess.Title)
		}
		return err
	})
	if err != nil {
		return httpx.Wrap(err)
	}
	emit("session", sess)
	return s.loop(ctx, rc, sid, emit)
}

// resolvePending replaces the pending result of callID in the results turn.
func resolvePending(ctx context.Context, q db.Querier, wid, sid uuid.UUID, seq int, callID, content string) error {
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT body FROM assistant_turns WHERE workspace_id = $1 AND session_id = $2 AND seq = $3 FOR UPDATE`,
		wid, sid, seq).Scan(&raw); err != nil {
		return err
	}
	var b turnBody
	_ = json.Unmarshal(raw, &b)
	for i := range b.Results {
		if b.Results[i].CallID == callID && b.Results[i].Pending {
			b.Results[i].Pending = false
			b.Results[i].Content = content
		}
	}
	return updateTurn(ctx, q, wid, sid, seq, b)
}

func (s *Service) specs(role auth.Role) []ai.ToolSpec {
	var out []ai.ToolSpec
	for _, t := range s.order {
		if !role.AtLeast(t.MinRole) {
			continue
		}
		out = append(out, ai.ToolSpec{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	return out
}

func (s *Service) system(ctx context.Context, p *auth.Principal) []ai.Block {
	var userName, wsName string
	_ = s.d.DB.Pool.QueryRow(ctx, `SELECT u.name, w.name FROM users u, workspaces w WHERE u.id = $1 AND w.id = $2`, p.UserID, p.WorkspaceID).
		Scan(&userName, &wsName)
	return []ai.Block{{Text: systemPrompt(s.catalog()), Cache: true}, {Text: sessionPrompt(p, userName, wsName, time.Now())}}
}

func chatTurns(ts []turn) []ai.ChatTurn {
	out := make([]ai.ChatTurn, 0, len(ts))
	for _, t := range ts {
		ct := ai.ChatTurn{Role: t.Role, Calls: t.Body.Calls}
		if t.Role == "user" {
			ct.Text = t.Body.Prefix + t.Body.Text
			for _, r := range t.Body.Results {
				res := r.ToolResult
				if r.Pending {
					res.Content = "Waiting for the user's approval."
				}
				ct.Results = append(ct.Results, res)
			}
		} else {
			ct.Text = t.Body.Text
		}
		out = append(out, ct)
	}
	return out
}

type proposal struct {
	call ai.ToolCall
	tool *tool
	args json.RawMessage
	pv   Preview
}

// loop runs model turns until the model answers without tools, proposes a change, or
// reaches maxSteps.
func (s *Service) loop(ctx context.Context, rc *runCtx, sid uuid.UUID, emit emitFn) error {
	p := rc.p
	system := s.system(ctx, p)
	specs := s.specs(p.Role)
	ext := rc.external(ctx)
	for _, name := range rc.extOrder {
		t := ext[name]
		specs = append(specs, ai.ToolSpec{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	for step := 0; step < maxSteps; step++ {
		turns, err := loadTurns(ctx, s.d.DB.Pool, p.WorkspaceID, sid)
		if err != nil {
			return httpx.Wrap(err)
		}
		resp, err := s.ai.AssistantTurn(ctx, p.WorkspaceID, &p.UserID, ai.ChatRequest{System: system, Turns: chatTurns(turns), Tools: specs, MaxTokens: 2048},
			func(t string) { emit("text", map[string]string{"text": t}) })
		if err != nil {
			return ai.APIError(err)
		}
		seq, err := appendTurn(ctx, s.d.DB.Pool, p.WorkspaceID, sid, "assistant", turnBody{Text: resp.Text, Calls: resp.Calls})
		if err != nil {
			return httpx.Wrap(err)
		}
		if len(resp.Calls) == 0 {
			emit("done", map[string]string{"status": "complete"})
			return nil
		}
		steps := map[string]Step{}
		var results []storedResult
		var prop *proposal
		for _, c := range resp.Calls {
			t := rc.lookup(ctx, c.Name)
			if t == nil || !p.Role.AtLeast(t.MinRole) {
				results = append(results, storedResult{ToolResult: ai.ToolResult{CallID: c.ID, Content: "There is no such tool for this user.", IsError: true}})
				continue
			}
			st := Step{ID: c.ID, Tool: c.Name, Label: t.Active, Status: "running"}
			emit("tool", st)
			if t.Write && prop != nil {
				st.Status, st.Summary = "error", "Waits for the change before it"
				results = append(results, storedResult{ToolResult: ai.ToolResult{CallID: c.ID, IsError: true,
					Content: "Not run: one change waits for approval at a time. Propose it again after the user decides on the first."}})
				steps[c.ID] = st
				emit("tool", st)
				continue
			}
			if !t.Write {
				out, err := t.Read(ctx, rc, c.Input)
				st.Label = t.Done
				if err != nil {
					st.Status, st.Summary = "error", toolErrorText(err)
					results = append(results, storedResult{ToolResult: ai.ToolResult{CallID: c.ID, Content: "Error: " + toolErrorContent(err), IsError: true}})
				} else {
					st.Status, st.Summary, st.Refs = "done", out.Summary, out.Refs
					results = append(results, storedResult{ToolResult: ai.ToolResult{CallID: c.ID, Content: out.Content}})
				}
				steps[c.ID] = st
				emit("tool", st)
				continue
			}
			args, pv, err := t.Propose(ctx, rc, c.Input)
			if err != nil {
				st.Status, st.Summary = "error", toolErrorText(err)
				results = append(results, storedResult{ToolResult: ai.ToolResult{CallID: c.ID, Content: "Not proposed: " + toolErrorText(err), IsError: true}})
				steps[c.ID] = st
				emit("tool", st)
				continue
			}
			raw, _ := json.Marshal(args)
			prop = &proposal{call: c, tool: t, args: raw, pv: pv}
			results = append(results, storedResult{ToolResult: ai.ToolResult{CallID: c.ID}, Pending: true})
			st.Status, st.Label = "awaiting_approval", pv.Title
			steps[c.ID] = st
		}
		var approval *Approval
		err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			rseq, err := appendTurn(ctx, tx, p.WorkspaceID, sid, "user", turnBody{Results: results})
			if err != nil {
				return err
			}
			if prop != nil {
				a := &action{SessionID: sid, TurnSeq: rseq, CallID: prop.call.ID, Tool: prop.tool.Name, Args: prop.args, Preview: prop.pv}
				if err := insertAction(ctx, tx, p.WorkspaceID, p.UserID, a); err != nil {
					return err
				}
				st := steps[prop.call.ID]
				st.ActionID = &a.ID
				steps[prop.call.ID] = st
				tok, err := s.approvalToken(p, a)
				if err != nil {
					return err
				}
				approval = &Approval{ActionID: a.ID, CallID: a.CallID, Tool: a.Tool, Preview: a.Preview, Token: tok, ExpiresAt: a.ExpiresAt}
			}
			return updateTurn(ctx, tx, p.WorkspaceID, sid, seq, turnBody{Text: resp.Text, Calls: resp.Calls, Tools: steps})
		})
		if err != nil {
			return httpx.Wrap(err)
		}
		if approval != nil {
			emit("tool", steps[prop.call.ID])
			emit("approval", *approval)
			emit("done", map[string]string{"status": "awaiting_approval"})
			return nil
		}
	}
	emit("done", map[string]string{"status": "complete"})
	return nil
}

// toolErrorContent is toolErrorText, with a connected tool's own account of the
// failure (inside its data block) for the model.
func toolErrorContent(err error) string {
	var tf *toolFailure
	if errors.As(err, &tf) {
		return tf.Content
	}
	return toolErrorText(err)
}

// toolErrorText is a safe, short reason for the model and the panel.
func toolErrorText(err error) string {
	var tf *toolFailure
	if errors.As(err, &tf) {
		return tf.Summary
	}
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusForbidden:
			return "You are not allowed to do this."
		case http.StatusNotFound:
			return "Not found in this workspace."
		}
		if ae.Detail != "" {
			return clip(ae.Detail, 300)
		}
		return http.StatusText(ae.Status)
	}
	var he *httpx.Error
	if errors.As(err, &he) {
		return clip(he.Detail, 300)
	}
	return "Something went wrong."
}

func (s *Service) approvalToken(p *auth.Principal, a *action) (string, error) {
	tok, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAssistantApproval, SessionID: p.SessionID.String(),
		Workspace: p.WorkspaceID.String(), RegisteredClaims: jwtID(a.ID)}, p.UserID.String(), time.Until(a.ExpiresAt))
	return tok, err
}

// decide applies the user's decision on a proposed change and continues the run.
func (s *Service) decide(ctx context.Context, id uuid.UUID, in DecisionInput, emit emitFn) error {
	p, err := s.require(ctx)
	if err != nil {
		return err
	}
	if in.Decision != "approve" && in.Decision != "deny" {
		return httpx.Validation("decision must be approve or deny")
	}
	// The token binds the decision to this action, this user, this sign-in and this
	// workspace. It reaches only the user's browser, never the model.
	c, err := s.d.Tokens.Verify(in.Token, auth.PurposeAssistantApproval)
	if err != nil || c.ID != id.String() || c.Subject != p.UserID.String() || c.SessionID != p.SessionID.String() || c.Workspace != p.WorkspaceID.String() {
		return errBadToken
	}
	var sid uuid.UUID
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT session_id FROM assistant_actions WHERE workspace_id = $1 AND user_id = $2 AND id = $3`,
		p.WorkspaceID, p.UserID, id).Scan(&sid); err != nil {
		if isNotFound(err) {
			return errNotPending
		}
		return httpx.Wrap(err)
	}
	if !s.begin(sid) {
		return errBusy
	}
	defer s.end(sid)
	status := "approved"
	if in.Decision == "deny" {
		status = "denied"
	}
	a, err := claimAction(ctx, s.d.DB.Pool, p.WorkspaceID, p.UserID, id, status)
	if err != nil {
		return err
	}
	rc := s.newRun(ctx, p, nil)
	t := rc.lookup(ctx, a.Tool)
	st := Step{ID: a.CallID, Tool: a.Tool, Label: a.Preview.Title, ActionID: &a.ID}
	content := "The user declined this change, so it was not made. Do not retry it unless they ask."
	final := "denied"
	if status == "approved" {
		st.Status = "running"
		emit("tool", st)
		var out output
		err = errNotPending
		if t != nil && t.Write && p.Role.AtLeast(t.MinRole) {
			out, err = t.Apply(ctx, rc, newCaller(s.api, p, true), a.Args, "assistant-"+a.ID.String())
		} else if t != nil {
			err = &apiError{Status: http.StatusForbidden}
		} else if integrations.IsTool(a.Tool) {
			err = httpx.Validation("This connected tool is turned off or disconnected now.")
		}
		if err != nil {
			final, content = "failed", "The user approved, but it failed: "+toolErrorContent(err)
			st.Status, st.Summary = "error", toolErrorText(err)
		} else {
			final, content = "done", out.Content
			st.Status, st.Summary, st.Refs, st.Label = "done", out.Summary, out.Refs, t.Done
		}
	} else {
		st.Status = "denied"
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := finishAction(ctx, tx, a.ID, final, map[string]any{"summary": st.Summary}); err != nil {
			return err
		}
		if err := resolvePending(ctx, tx, p.WorkspaceID, a.SessionID, a.TurnSeq, a.CallID, content); err != nil {
			return err
		}
		if final == "done" || final == "failed" {
			// What the assistant did, attributed to ai on behalf of the user. Counts only.
			ap := *p
			ap.Assistant = true
			return audit.Log(ctx, tx, &ap, p.WorkspaceID, "assistant.action", "assistant_action", &a.ID,
				map[string]any{"tool": a.Tool, "result": final, "targets": len(a.Preview.Targets)})
		}
		return nil
	})
	if err != nil {
		return httpx.Wrap(err)
	}
	emit("tool", st)
	return s.loop(ctx, rc, a.SessionID, emit)
}

// --- transcripts ----------------------------------------------------------------------

func (s *Service) transcript(ctx context.Context, p *auth.Principal, sid uuid.UUID) (*Transcript, error) {
	sess, err := loadSession(ctx, s.d.DB.Pool, p.WorkspaceID, p.UserID, sid, false)
	if err != nil {
		return nil, err
	}
	turns, err := loadTurns(ctx, s.d.DB.Pool, p.WorkspaceID, sid)
	if err != nil {
		return nil, err
	}
	statuses, err := actionStatuses(ctx, s.d.DB.Pool, p.WorkspaceID, sid)
	if err != nil {
		return nil, err
	}
	tr := &Transcript{Session: sess, Items: []Item{}}
	for _, t := range turns {
		if t.Role == "user" {
			if t.Body.Text != "" {
				tr.Items = append(tr.Items, Item{Role: "user", Text: t.Body.Text, Steps: []Step{}, Context: t.Body.Context})
			}
			continue
		}
		it := Item{Role: "assistant", Text: t.Body.Text, Steps: []Step{}}
		for _, c := range t.Body.Calls {
			st, ok := t.Body.Tools[c.ID]
			if !ok {
				continue
			}
			if st.ActionID != nil {
				switch statuses[*st.ActionID] {
				case "done":
					st.Status = "done"
					if tl := s.tools[st.Tool]; tl != nil {
						st.Label = tl.Done
					}
				case "failed":
					st.Status = "error"
				case "denied":
					st.Status = "denied"
				case "expired":
					st.Status = "expired"
				case "approved":
					st.Status = "running"
				}
			}
			it.Steps = append(it.Steps, st)
		}
		if it.Text != "" || len(it.Steps) > 0 {
			tr.Items = append(tr.Items, it)
		}
	}
	a, err := pendingAction(ctx, s.d.DB.Pool, p.WorkspaceID, sid)
	if err != nil {
		return nil, err
	}
	if a != nil {
		tok, err := s.approvalToken(p, a)
		if err != nil {
			return nil, err
		}
		tr.Approval = &Approval{ActionID: a.ID, CallID: a.CallID, Tool: a.Tool, Preview: a.Preview, Token: tok, ExpiresAt: a.ExpiresAt}
	}
	return tr, nil
}

func isNotFound(err error) bool { return db.IsNotFound(err) }

func jwtID(id uuid.UUID) jwt.RegisteredClaims { return jwt.RegisteredClaims{ID: id.String()} }
