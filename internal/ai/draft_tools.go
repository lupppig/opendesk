package ai

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/integrations"
)

// Draft lookups (contracts/ai.md "Drafts and connected tools"): before a draft is written, a
// short tool loop on the workspace's fast model reads the facts the reply needs (an order, a
// delivery, a refund) from the workspace's connected tools. Only tools that run without
// approval as reads are offered (their server marks them read-only and no admin set them to
// ask first), never a write, not even one set to run without asking, and every call runs as a
// read, which the integration checks again when it runs. The draft itself is then written as
// before, streamed, with what the tools returned as a data block.

const (
	// draftLookupTurns bounds the model turns that may call tools.
	draftLookupTurns = 3
	// draftLookupCalls bounds the tool calls of one draft, all turns together.
	draftLookupCalls = 6
	// draftLookupTime bounds the whole lookup; the draft is written after it either way.
	draftLookupTime      = 40 * time.Second
	draftLookupMaxTokens = 1000
	// draftLookupOutChars bounds one tool's output, and draftLookupAllChars all of them, as
	// the draft reads them.
	draftLookupOutChars = 6000
	draftLookupAllChars = 20000
)

// draftLookupTools are the connected tools a draft may read with, by name: reads that run
// without approval. Only users with at least the agent role get them, as with Ollie.
func (s *Service) draftLookupTools(ctx context.Context, p *auth.Principal) map[string]integrations.Tool {
	if p.Kind != auth.KindUser || !p.Role.AtLeast(auth.RoleAgent) {
		return nil
	}
	ts, err := s.connectedTools().Tools(ctx, p.WorkspaceID)
	if err != nil {
		s.d.Log.Warn("draft lookup: connected tools unavailable", "workspace_id", p.WorkspaceID, "err", err)
		return nil
	}
	out := map[string]integrations.Tool{}
	for _, t := range ts {
		if t.ReadOnly && !t.AutoRun && t.Name != "" {
			out[t.Name] = t
		}
	}
	return out
}

// draftLookup runs the lookup for a draft and returns what the tools gave as a
// <tool_results> block, or "" when nothing was looked up. data is the ticket, the customer,
// the thread and the agent's request, already in data blocks. A failure of the lookup
// (provider, tools, time) leaves the draft without it; only the caller's cancellation is
// returned.
func (s *Service) draftLookup(ctx context.Context, p *auth.Principal, tid uuid.UUID, set hd.AISettings, data string) (string, error) {
	if !set.DraftTools {
		return "", nil
	}
	tools := s.draftLookupTools(ctx, p)
	if len(tools) == 0 {
		return "", nil
	}
	if err := s.toolsFor(ctx, p.WorkspaceID); err != nil {
		if !errors.Is(err, ErrNoTools) {
			s.d.Log.Warn("draft lookup skipped", "workspace_id", p.WorkspaceID, "err", err)
		}
		return "", nil
	}
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	slices.Sort(names)
	specs := make([]ToolSpec, 0, len(names))
	for _, n := range names {
		t := tools[n]
		specs = append(specs, ToolSpec{Name: n, Description: clip(t.Description, 1000), Schema: cloneSchema(t.Schema)})
	}

	lctx, cancel := context.WithTimeout(ctx, draftLookupTime)
	defer cancel()
	start := time.Now()
	turns := []ChatTurn{{Role: "user", Text: data + "\n\n" + "Look up what the reply needs now, or answer done."}}
	var found []string
	calls, room, turn := 0, draftLookupAllChars, 0
	for ; turn < draftLookupTurns && calls < draftLookupCalls; turn++ {
		c := s.newCall(p.WorkspaceID, p.ActorUserID(), &tid, "draft_reply")
		if rt, err := c.route(lctx); err == nil {
			c.model = s.tierModelOn(lctx, rt, set, tierFast)
		}
		req := ChatRequest{System: []Block{{Text: draftLookupSystem, Cache: true}}, Turns: turns, Tools: specs, MaxTokens: draftLookupMaxTokens}
		req.Model = c.resolveModel(lctx, "")
		if err := c.reserve(lctx, req.Model, chatTokens(req), req.MaxTokens); err != nil {
			s.d.Log.Info("draft lookup stopped", "workspace_id", p.WorkspaceID, "reason", errReason(err))
			break
		}
		resp, err := c.chat(lctx, req, nil)
		if err != nil {
			s.d.Log.Info("draft lookup stopped", "workspace_id", p.WorkspaceID, "reason", errReason(err))
			break
		}
		c.charge(ctx)
		if len(resp.Calls) == 0 {
			break
		}
		turns = append(turns, ChatTurn{Role: "assistant", Text: resp.Text, Calls: resp.Calls})
		results := make([]ToolResult, 0, len(resp.Calls))
		for _, call := range resp.Calls {
			t, ok := tools[call.Name]
			switch {
			case !ok:
				results = append(results, ToolResult{CallID: call.ID, Content: "This tool is not available.", IsError: true})
			case calls >= draftLookupCalls || lctx.Err() != nil:
				results = append(results, ToolResult{CallID: call.ID, Content: "Not run: the lookup limit was reached.", IsError: true})
			default:
				calls++
				out, isErr := s.draftCall(lctx, p.WorkspaceID, t, call.Input)
				name := t.Integration + ": " + t.Title
				if room > 0 {
					out = clip(out, min(draftLookupOutChars, room))
					room -= utf8.RuneCountInString(out)
				} else {
					out, isErr = "Not shown: the earlier lookups filled the space for tool results.", true
				}
				attrs := []string{"tool", name, "input", clip(string(call.Input), 300)}
				if isErr {
					attrs = append(attrs, "error", "true")
				}
				found = append(found, block("tool_output", attrs, untrusted(out)))
				results = append(results, ToolResult{CallID: call.ID, IsError: isErr, Content: block("tool_output", []string{"tool", name}, untrusted(out))})
			}
		}
		turns = append(turns, ChatTurn{Role: "user", Results: results})
	}
	if err := ctx.Err(); err != nil {
		return "", err // the agent stopped the draft
	}
	// Counts and timings only: arguments and outputs are never logged.
	s.d.Log.Debug("ai draft lookup", "workspace_id", p.WorkspaceID, "turns", turn, "tool_calls", calls, "ms", time.Since(start).Milliseconds())
	if len(found) == 0 {
		return "", nil
	}
	return block("tool_results", []string{"looked_up", strconv.Itoa(len(found))}, strings.Join(found, "\n")), nil
}

// draftCall runs one connected tool as a read. The output is the tool's own text; a failure
// is said in a line the model reads as no information.
func (s *Service) draftCall(ctx context.Context, wid uuid.UUID, t integrations.Tool, args json.RawMessage) (string, bool) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	out, isErr, err := s.connectedTools().Call(ctx, wid, t.IntegrationID, t.Remote, args, integrations.CallRead)
	if err != nil {
		return "The tool could not be reached, so it tells nothing.", true
	}
	if strings.TrimSpace(out) == "" {
		out = "(no content)"
	}
	return out, isErr
}
