package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
)

// TestToolsWithoutSeverity checks a workspace that does not use severity is offered the
// ticket tools without it, and one that does keeps it.
func TestToolsWithoutSeverity(t *testing.T) {
	e := newEnv(t)
	schemas := func() string {
		res, err := e.session(e.APIKey(e.WS, auth.AllScopes...)).ListTools(e.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, tl := range res.Tools {
			if tl.Name == "get_report" {
				continue // it reads the overview, whose breakdowns the API keeps
			}
			raw, _ := json.Marshal(tl.InputSchema)
			b.WriteString(tl.Name + " " + tl.Description + " " + string(raw) + "\n")
		}
		return b.String()
	}
	if s := schemas(); !strings.Contains(s, `"severity"`) {
		t.Fatalf("severity missing while on: %s", s)
	}
	e.Exec(`UPDATE workspaces SET settings = settings || '{"severity_enabled": false}'::jsonb WHERE id = $1`, e.WS.ID)
	s := schemas()
	if strings.Contains(strings.ToLower(s), "severit") || !strings.Contains(s, `"priority"`) {
		t.Fatalf("tools offer severity while off: %s", s)
	}
}
