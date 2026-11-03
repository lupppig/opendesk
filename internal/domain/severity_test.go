package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithoutSeverity(t *testing.T) {
	in := map[string]any{"type": "object", "required": []string{"ticket", "severity"}, "properties": map[string]any{
		"ticket":   map[string]any{"type": "string"},
		"severity": map[string]any{"type": "string", "enum": Severities},
		"changes": map[string]any{"type": "object", "properties": map[string]any{
			"severity": map[string]any{"type": "string"}, "priority": map[string]any{"type": "string"}}},
		"actions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
			"type":  map[string]any{"type": "string", "enum": []string{"set_priority", "set_severity", "add_tag"}},
			"value": map[string]any{"type": "string", "description": "A status, priority, severity, user id or label name."}}}},
	}}
	before, _ := json.Marshal(in)
	out, _ := json.Marshal(WithoutSeverity(in))
	if after, _ := json.Marshal(in); string(after) != string(before) {
		t.Fatal("the schema was changed in place")
	}
	s := string(out)
	if strings.Contains(s, "severity") || !strings.Contains(s, `"ticket"`) || !strings.Contains(s, "priority") || !strings.Contains(s, "set_priority") ||
		!strings.Contains(s, "add_tag") || !strings.Contains(s, "A status, priority, user id or label name.") {
		t.Fatalf("schema %s", s)
	}
	for in, want := range map[string]string{
		"Change a ticket's status, priority, severity, assignee, team or labels.": "Change a ticket's status, priority, assignee, team or labels.",
		"Raise the ticket's priority or severity, or add existing labels.":        "Raise the ticket's priority, or add existing labels.",
	} {
		if got := WithoutSeverityWords(in); got != want {
			t.Fatalf("%q: %q", in, got)
		}
	}
}
