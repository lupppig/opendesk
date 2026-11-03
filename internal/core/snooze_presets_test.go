package core

import (
	"reflect"
	"testing"
)

// TestSnoozePresets checks the snooze menu's times are stored as sent, each kind with only
// its own fields, and an empty list stores as empty (the app's defaults).
func TestSnoozePresets(t *testing.T) {
	in := []SnoozePreset{{Kind: "hours", Hours: 1}, {Kind: "days", Days: 3, Time: "09:00"}, {Kind: "week", Time: "23:59"}, {Kind: "days", Days: 365, Time: "00:00"}, {Kind: "hours", Hours: 168}}
	out, err := validateSettings(&CoreWorkspaceSettings{SnoozePresets: &in})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"kind": "hours", "hours": 1},
		{"kind": "days", "days": 3, "time": "09:00"},
		{"kind": "week", "time": "23:59"},
		{"kind": "days", "days": 365, "time": "00:00"},
		{"kind": "hours", "hours": 168},
	}
	if !reflect.DeepEqual(out["snooze_presets"], want) {
		t.Fatalf("stored %v", out["snooze_presets"])
	}

	empty := []SnoozePreset{}
	out, err = validateSettings(&CoreWorkspaceSettings{SnoozePresets: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out["snooze_presets"].([]map[string]any); !ok || len(got) != 0 {
		t.Fatalf("empty stored %#v", out["snooze_presets"])
	}

	out, err = validateSettings(&CoreWorkspaceSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["snooze_presets"]; ok {
		t.Fatal("a patch without snooze_presets changed them")
	}
}

// TestSnoozePresetsRefused checks what the snooze menu's times refuse: an unknown kind, a
// count out of range, a missing or malformed time, another kind's fields, and more than eight.
func TestSnoozePresetsRefused(t *testing.T) {
	for name, p := range map[string]SnoozePreset{
		"unknown kind":   {Kind: "month", Days: 30, Time: "09:00"},
		"no kind":        {Hours: 2},
		"zero hours":     {Kind: "hours"},
		"too many hours": {Kind: "hours", Hours: 169},
		"hours, time":    {Kind: "hours", Hours: 4, Time: "09:00"},
		"hours, days":    {Kind: "hours", Hours: 4, Days: 1},
		"zero days":      {Kind: "days", Time: "09:00"},
		"too many days":  {Kind: "days", Days: 366, Time: "09:00"},
		"days, no time":  {Kind: "days", Days: 1},
		"days, hours":    {Kind: "days", Days: 1, Hours: 2, Time: "09:00"},
		"time 24:00":     {Kind: "days", Days: 1, Time: "24:00"},
		"time 9:00":      {Kind: "days", Days: 1, Time: "9:00"},
		"time 09:60":     {Kind: "week", Time: "09:60"},
		"time with am":   {Kind: "week", Time: "09:00 am"},
		"week, no time":  {Kind: "week"},
		"week, days":     {Kind: "week", Days: 7, Time: "09:00"},
	} {
		in := []SnoozePreset{{Kind: "hours", Hours: 1}, p}
		if _, err := validateSettings(&CoreWorkspaceSettings{SnoozePresets: &in}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	nine := make([]SnoozePreset, MaxSnoozePresets+1)
	for i := range nine {
		nine[i] = SnoozePreset{Kind: "hours", Hours: i + 1}
	}
	if _, err := validateSettings(&CoreWorkspaceSettings{SnoozePresets: &nine}); err == nil {
		t.Fatal("accepted nine times")
	}
	eight := nine[:MaxSnoozePresets]
	if _, err := validateSettings(&CoreWorkspaceSettings{SnoozePresets: &eight}); err != nil {
		t.Fatalf("eight times: %v", err)
	}
}
