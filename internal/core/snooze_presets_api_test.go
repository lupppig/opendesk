package core

import (
	"encoding/json"
	"testing"
)

// The snooze menu's times through the API: absent at first (the app's defaults), set by
// an admin, kept when another setting changes, refused when malformed or too many, and
// back to the defaults with an empty list.
func TestSnoozePresetsSetting(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("snooze")
	c.newWorkspace("Snooze Co")
	presets := func(r resp) string {
		s, _ := r.Body["settings"].(map[string]any)
		raw, _ := json.Marshal(s["snooze_presets"])
		return string(raw)
	}

	if r := c.do("GET", "/v1/workspace", nil); r.Status != 200 || presets(r) != "null" {
		t.Fatalf("new workspace: %d %s", r.Status, r.Raw)
	}
	set := []map[string]any{{"kind": "days", "days": 1, "time": "09:00"}, {"kind": "days", "days": 3, "time": "09:00"}, {"kind": "days", "days": 7, "time": "09:00"}}
	raw, _ := json.Marshal(set)
	want := string(raw)
	if r := c.do("PATCH", "/v1/workspace", map[string]any{"settings": map[string]any{"snooze_presets": set}}); r.Status != 200 || presets(r) != want {
		t.Fatalf("set: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PATCH", "/v1/workspace", map[string]any{"settings": map[string]any{"csat_enabled": true}}); r.Status != 200 || presets(r) != want {
		t.Fatalf("another setting: %d %s", r.Status, r.Raw)
	}

	nine := make([]map[string]any, 9)
	for i := range nine {
		nine[i] = map[string]any{"kind": "hours", "hours": i + 1}
	}
	for name, bad := range map[string]any{
		"time 9:00":   []map[string]any{{"kind": "days", "days": 1, "time": "9:00"}},
		"no days":     []map[string]any{{"kind": "days", "time": "09:00"}},
		"hours, time": []map[string]any{{"kind": "hours", "hours": 2, "time": "09:00"}},
		"kind month":  []map[string]any{{"kind": "month", "days": 30, "time": "09:00"}},
		"nine":        nine,
	} {
		if r := c.do("PATCH", "/v1/workspace", map[string]any{"settings": map[string]any{"snooze_presets": bad}}); r.Status != 422 {
			t.Fatalf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	if r := c.do("GET", "/v1/workspace", nil); presets(r) != want {
		t.Fatalf("after refusals: %s", r.Raw)
	}

	if r := c.do("PATCH", "/v1/workspace", map[string]any{"settings": map[string]any{"snooze_presets": []any{}}}); r.Status != 200 || presets(r) != "[]" {
		t.Fatalf("reset: %d %s", r.Status, r.Raw)
	}
}
