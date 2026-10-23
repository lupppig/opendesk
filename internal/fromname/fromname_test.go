package fromname

import (
	"strings"
	"testing"
)

func TestFill(t *testing.T) {
	n := Names{Agent: "Maya Chen", Team: "Billing", Brand: "Margin", Mailbox: "Margin Support", Workspace: "Margin"}
	none := n
	none.Agent = ""
	cases := []struct {
		format string
		vars   []string
		names  Names
		want   string
	}{
		{"{agent} at Margin Support", Collab, n, "Maya Chen at Margin Support"},
		{"{agent} at {mailbox}", Collab, n, "Maya Chen at Margin Support"},
		{"{agent_first} from {workspace}", Collab, n, "Maya from Margin"},
		{"{agent} at {mailbox}", Collab, none, "Margin Support"},
		{"{agent_first} from {workspace}", Collab, none, "Margin"},
		{"{agent}", Collab, none, "Margin Support"},
		{"{workspace} - {agent}", Collab, none, "Margin"},
		// A variable another vocabulary has stays as written.
		{"{agent} {team}", Collab, n, "Maya Chen {team}"},
		{"{agent_first} from {team}", Helpdesk, n, "Maya from Billing"},
		{"{workspace}", Helpdesk, n, "{workspace}"},
	}
	for _, c := range cases {
		if got := Fill(c.format, c.vars, c.names); got != c.want {
			t.Errorf("Fill(%q, agent %q) = %q, want %q", c.format, c.names.Agent, got, c.want)
		}
	}
	if got := Fill(strings.Repeat("{agent}", 40), Collab, n); len([]rune(got)) > 200 {
		t.Errorf("not bounded: %d", len([]rune(got)))
	}
}

func TestClean(t *testing.T) {
	for _, f := range []string{"{agent} at Margin Support", "{agent_first} from {workspace}", "{mailbox}", "Support desk", ""} {
		if _, err := Clean("display_name", f, Collab); err != nil {
			t.Errorf("%q refused: %v", f, err)
		}
	}
	for _, f := range []string{"{agent} {team}", "{brand}", "{agent", "a <b>", "a\x01", strings.Repeat("a", 81)} {
		if _, err := Clean("display_name", f, Collab); err == nil {
			t.Errorf("%q accepted", f)
		}
	}
	if got, _ := Clean("x", "  {agent}   at  Margin ", Collab); got != "{agent} at Margin" {
		t.Errorf("spaces: %q", got)
	}
	_, err := Clean("display_name", "{x}", Collab)
	if err == nil || !strings.Contains(err.Error(), "{agent}, {agent_first}, {mailbox} and {workspace}") {
		t.Errorf("message: %v", err)
	}
	if !HasVars("{agent} at X", Collab) || HasVars("Margin {support}", Collab) || HasVars("Plain", Collab) {
		t.Error("HasVars")
	}
}
