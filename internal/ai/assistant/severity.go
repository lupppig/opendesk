package assistant

import (
	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/domain"
)

// withoutSeverity is the built-in tools for a workspace that does not use severity
// (settings severity_enabled false): no severity to set, filter on or automate with.
func withoutSeverity(specs []ai.ToolSpec) []ai.ToolSpec {
	out := make([]ai.ToolSpec, len(specs))
	for i, t := range specs {
		out[i] = ai.ToolSpec{Name: t.Name, Description: domain.WithoutSeverityWords(t.Description), Schema: domain.WithoutSeverity(t.Schema)}
	}
	return out
}
