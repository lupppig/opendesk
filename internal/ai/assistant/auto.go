package assistant

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/integrations"
)

// Changes that run without asking (contracts/ai.md "Approvals"). An admin chooses the
// built-in ones (settings.ai.assistant_auto) and, per integration, connected writes
// (auto_tools). They run on the same path as an approved change, as the user, and are
// recorded and audited like one. Whatever the settings say, these always ask: a change
// whose preview reaches someone outside (external), api_change, and every built-in write
// that is not one of hd.AssistantAutoTools.

// maxAutoPerMessage bounds the changes that run without asking for one message from the
// user; past it, writes are proposals again.
const maxAutoPerMessage = 20

type autoKey struct{}

// withAutoRun marks ctx as running a change without asking, for the tool's Apply.
func withAutoRun(ctx context.Context) context.Context { return context.WithValue(ctx, autoKey{}, true) }

// autoRunning reports whether Apply runs a change without asking.
func autoRunning(ctx context.Context) bool {
	v, _ := ctx.Value(autoKey{}).(bool)
	return v
}

// alwaysAsks reports a change that waits for approval whatever the settings say.
func alwaysAsks(t *tool, pv Preview) bool {
	if !t.Write || t.Apply == nil || pv.External {
		return true
	}
	if t.ext != nil {
		return false // a connected write: its integration's own setting decides
	}
	return t.Name == "api_change" || !slices.Contains(hd.AssistantAutoTools, t.Name)
}

// mayAuto says whether a proposed change runs without asking. fresh reads the settings
// again (just before Apply); otherwise the run's copy, from when it started, is used.
func (rc *runCtx) mayAuto(ctx context.Context, t *tool, pv Preview, fresh bool) bool {
	if alwaysAsks(t, pv) {
		return false
	}
	if t.ext != nil {
		if !fresh {
			return t.ext.AutoRun
		}
		ok, err := integrations.For(rc.svc.d).AutoRun(ctx, rc.p.WorkspaceID, t.ext.IntegrationID, t.ext.Remote)
		return err == nil && ok
	}
	if fresh || rc.auto == nil {
		rc.loadAuto(ctx)
	}
	return slices.Contains(rc.auto, t.Name)
}

// loadAuto reads the built-in changes the workspace lets run without asking.
func (rc *runCtx) loadAuto(ctx context.Context) {
	rc.auto = []string{}
	st, err := rc.svc.ai.WorkspaceAI(ctx, rc.p.WorkspaceID)
	if err == nil && st.Enabled && st.Assistant {
		rc.auto = append(rc.auto, st.AssistantAuto...)
	}
}

// runAuto applies a change the workspace lets run without asking, on the path of an
// approved one: an assistant_actions row (auto), the same idempotency key, the user's
// own permissions, and the assistant.action audit entry with data.auto. runErr is the
// change's own failure; err is a failure to record it.
func (s *Service) runAuto(ctx context.Context, rc *runCtx, sid uuid.UUID, seq int, c ai.ToolCall, t *tool, args json.RawMessage, pv Preview) (a *action, out output, runErr, err error) {
	p := rc.p
	a = &action{SessionID: sid, TurnSeq: seq, CallID: c.ID, Tool: t.Name, Args: args, Preview: pv}
	if err := insertAutoAction(ctx, s.d.DB.Pool, p.WorkspaceID, p.UserID, a); err != nil {
		return nil, output{}, nil, err
	}
	out, runErr = t.Apply(withAutoRun(ctx), rc, newCaller(s.api, p, true), args, "assistant-"+a.ID.String())
	final, summary := "done", out.Summary
	if runErr != nil {
		final, summary = "failed", toolErrorText(runErr)
	}
	// The outcome is recorded even when the request went away mid-change.
	ctx = context.WithoutCancel(ctx)
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := finishAction(ctx, tx, a.ID, final, map[string]any{"summary": summary}); err != nil {
			return err
		}
		ap := *p
		ap.Assistant = true
		return audit.Log(ctx, tx, &ap, p.WorkspaceID, "assistant.action", "assistant_action", &a.ID,
			map[string]any{"tool": a.Tool, "result": final, "targets": len(pv.Targets), "auto": true})
	})
	a.Status = final
	return a, out, runErr, err
}
