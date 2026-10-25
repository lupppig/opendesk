package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/httpx"
)

// AssistantName is the name the product gives its AI everywhere: the assistant, and the
// actor of every change AI makes (auto-triage, widget answers, the ai_triage action).
// GET /v1/ai/config reports it as assistant_name so the web app and emails read it from
// one place.
const AssistantName = "Ollie"

// ToolSpec is a function the model may call. Schema is a JSON Schema object.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is one function call the model asked for.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult answers a ToolCall. Content is what the model reads.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ChatTurn is one turn of a tool-using conversation. A user turn carries Text and/or
// Results (answers to the previous assistant turn's Calls); an assistant turn carries
// Text and/or Calls.
type ChatTurn struct {
	Role    string       `json:"role"`
	Text    string       `json:"text,omitempty"`
	Calls   []ToolCall   `json:"calls,omitempty"`
	Results []ToolResult `json:"results,omitempty"`
}

// ChatRequest is one model call with tools.
type ChatRequest struct {
	System    []Block
	Turns     []ChatTurn
	Tools     []ToolSpec
	MaxTokens int
	Model     string
}

// ChatResponse is the model's turn: text and/or tool calls.
type ChatResponse struct {
	Text         string
	Calls        []ToolCall
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	StopReason   string
}

// ToolProvider is a Provider that supports tool use (both built-in providers do).
type ToolProvider interface {
	Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResponse, error)
}

// ErrNoTools means the configured provider cannot call tools.
var ErrNoTools = errors.New("ai provider does not support tools")

// Models reports the default and fast model names.
func (s *Service) Models() (def, fast string) { return s.models.Default, s.models.Fast }

// WorkspaceAI loads the workspace's AI settings and refuses when AI is off (404
// ai_disabled, 403 ai_off).
func (s *Service) WorkspaceAI(ctx context.Context, wid uuid.UUID) (AISettingsView, error) {
	a, err := s.workspaceAI(ctx, wid)
	return AISettingsView{Enabled: a.Enabled, Assistant: a.Assistant, AssistantPeer: a.AssistantPeer, AssistantAuto: a.AssistantAuto}, err
}

// AISettingsView is the part of settings.ai other packages read.
type AISettingsView struct {
	Enabled   bool
	Assistant bool
	// AssistantPeer: members' Ollie may ask a teammate's (contracts/ai.md "Asking a teammate").
	AssistantPeer bool
	// AssistantAuto: the built-in changes Ollie makes without asking (contracts/ai.md "Approvals").
	AssistantAuto []string
}

// Complete runs one billed model call without tools for feature, attributed to userID: it
// checks the AI balance covers the call's estimate and charges only when the call
// succeeded. A Model of "" picks the workspace's model for the feature's tier. Usage rows
// hold counts only; on the workspace's own key nothing is charged.
func (s *Service) Complete(ctx context.Context, wid uuid.UUID, userID *uuid.UUID, feature string, req Request) (Response, error) {
	if !s.Enabled() {
		return Response{}, errDisabled
	}
	c := s.newCall(wid, userID, nil, feature)
	resp, err := c.run(ctx, req, nil)
	if err != nil {
		return Response{}, err
	}
	c.charge(ctx)
	return resp, nil
}

// ParseJSON decodes a model's JSON output, tolerating a code fence or stray prose
// (ErrBadOutput when there is none).
func ParseJSON(text string, v any) error { return parseJSON(text, v) }

// AssistantTurn runs one billed model turn with tools: it checks the AI balance covers
// the turn's estimate, streams text through onDelta, and charges only when the turn
// succeeded. Usage rows hold counts only. On the workspace's own key nothing is charged.
func (s *Service) AssistantTurn(ctx context.Context, wid uuid.UUID, userID *uuid.UUID, req ChatRequest, onDelta func(string)) (ChatResponse, error) {
	if !s.Enabled() {
		return ChatResponse{}, errDisabled
	}
	c := s.newCall(wid, userID, nil, "assistant")
	req.Model = c.resolveModel(ctx, req.Model)
	if err := c.reserve(ctx, req.Model, chatTokens(req), req.MaxTokens); err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.chat(ctx, req, onDelta)
	if err != nil {
		return ChatResponse{}, err
	}
	c.charge(ctx)
	return resp, nil
}

// Reserve checks before a run that the workspace can pay for a typical call of a
// feature on its model: its AI balance, or nothing when it uses its own key.
func (s *Service) Reserve(ctx context.Context, wid uuid.UUID, feature string) error {
	c := s.newCall(wid, nil, nil, feature)
	model := c.resolveModel(ctx, "")
	t := typicalTokens[feature]
	return c.reserve(ctx, model, t.in, t.out)
}

// toolsFor reports whether the workspace's provider can call tools (ErrNoTools if not).
func (s *Service) toolsFor(ctx context.Context, wid uuid.UUID) error {
	rt, err := s.routeFor(ctx, wid)
	if err != nil {
		return err
	}
	if _, ok := rt.p.(ToolProvider); !ok {
		return ErrNoTools
	}
	return nil
}

// chat runs one tool-using model turn on the call's route (reserve and resolveModel
// first). It records a failed own key and sets c.resp for charge.
func (c *call) chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResponse, error) {
	rt, err := c.route(ctx)
	if err != nil {
		return ChatResponse{}, err
	}
	tp, ok := rt.p.(ToolProvider)
	if !ok {
		return ChatResponse{}, ErrNoTools
	}
	start := time.Now()
	resp, err := tp.Chat(ctx, req, onDelta)
	c.onKey = rt.own
	err = c.s.keyResult(ctx, c.wid, rt, err)
	if resp.Model == "" {
		resp.Model = req.Model
	}
	c.s.d.Log.Debug("ai call", "feature", c.feature, "model", resp.Model, "input_tokens", resp.InputTokens,
		"output_tokens", resp.OutputTokens, "tool_calls", len(resp.Calls), "ms", time.Since(start).Milliseconds(), "ok", err == nil)
	if err != nil {
		return ChatResponse{}, err
	}
	c.resp = Response{Model: resp.Model, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, CostUSD: resp.CostUSD}
	return resp, nil
}

// APIError maps an AI error to its problem response (contracts/ai.md).
func APIError(err error) error {
	if errors.Is(err, ErrNoTools) {
		return errUnsupported
	}
	return apiErr(err)
}

// DataBlock wraps untrusted content in a delimited <tag attrs>…</tag> block whose
// content cannot close or open a block (see contracts/ai.md).
func DataBlock(tag string, attrs []string, body string) string {
	return block(tag, attrs, untrusted(body))
}

// DataAttr neutralizes a value for a block's opening tag.
func DataAttr(s string) string { return attr(s) }

// Untrusted neutralizes delimiters in stored text placed inside a data block.
func Untrusted(s string) string { return untrusted(s) }

// MarkdownToHTML converts the light markdown the model writes to sanitized HTML.
func MarkdownToHTML(md string) string { return markdownToHTML(md) }

var errUnsupported = httpx.E(http.StatusNotImplemented, "ai_unsupported", "The configured AI provider cannot use tools.")
