package core

import "testing"

// TestUndoSendSeconds checks both undo windows (email and chat) take only the offered
// lengths, off included.
func TestUndoSendSeconds(t *testing.T) {
	for _, n := range []int{0, 5, 10, 20, 30} {
		out, err := validateSettings(&CoreWorkspaceSettings{UndoSendSeconds: &n})
		if err != nil {
			t.Fatalf("%d: %v", n, err)
		}
		if out["undo_send_seconds"] != n {
			t.Fatalf("%d: stored %v", n, out["undo_send_seconds"])
		}
		out, err = validateSettings(&CoreWorkspaceSettings{UndoSendChatSecs: &n})
		if err != nil {
			t.Fatalf("chat %d: %v", n, err)
		}
		if out["undo_send_chat_seconds"] != n {
			t.Fatalf("chat %d: stored %v", n, out["undo_send_chat_seconds"])
		}
	}
	for _, n := range []int{-1, 3, 31, 600} {
		if _, err := validateSettings(&CoreWorkspaceSettings{UndoSendSeconds: &n}); err == nil {
			t.Fatalf("%d: accepted", n)
		}
		if _, err := validateSettings(&CoreWorkspaceSettings{UndoSendChatSecs: &n}); err == nil {
			t.Fatalf("chat %d: accepted", n)
		}
	}
}

// TestMessageDeleteSetting checks the agents' delete rule takes only its three values.
func TestMessageDeleteSetting(t *testing.T) {
	for _, v := range []string{"off", "own", "all"} {
		if _, err := validateSettings(&CoreWorkspaceSettings{MessageDelete: &v}); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	bad := "everyone"
	if _, err := validateSettings(&CoreWorkspaceSettings{MessageDelete: &bad}); err == nil {
		t.Fatal("accepted everyone")
	}
}

// TestHiddenStatuses checks a workspace can hide statuses from the agents' menus, but never
// open or solved, and never a priority.
func TestHiddenStatuses(t *testing.T) {
	out, err := validateSettings(&CoreWorkspaceSettings{StatusLabels: map[string]DisplayOverride{
		"pending": {Hidden: true}, "on_hold": {Hidden: true}, "solved": {Label: "Closed"}, "closed": {Hidden: true}}})
	if err != nil {
		t.Fatal(err)
	}
	got := out["status_labels"].(map[string]any)
	if got["pending"].(map[string]any)["hidden"] != true || got["solved"].(map[string]any)["label"] != "Closed" || got["closed"].(map[string]any)["hidden"] != true {
		t.Fatalf("stored %v", got)
	}
	for name, in := range map[string]map[string]DisplayOverride{
		"open":   {"open": {Hidden: true}},
		"solved": {"solved": {Hidden: true}},
	} {
		if _, err := validateSettings(&CoreWorkspaceSettings{StatusLabels: in}); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, err := validateSettings(&CoreWorkspaceSettings{PriorityLabels: map[string]DisplayOverride{"low": {Hidden: true}}}); err == nil {
		t.Fatal("hidden priority accepted")
	}
}

// TestSendAndFinishButton checks the composer's send and finish button is stored on and off.
func TestSendAndFinishButton(t *testing.T) {
	for _, v := range []bool{true, false} {
		out, err := validateSettings(&CoreWorkspaceSettings{SendAndFinish: &v})
		if err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		if out["send_and_finish_button"] != v {
			t.Fatalf("%v: stored %v", v, out["send_and_finish_button"])
		}
	}
	out, err := validateSettings(&CoreWorkspaceSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["send_and_finish_button"]; ok {
		t.Fatal("stored without being set")
	}
}

// TestSeveritySetting checks severity can be turned off and on, and stays unset otherwise.
func TestSeveritySetting(t *testing.T) {
	for _, v := range []bool{true, false} {
		out, err := validateSettings(&CoreWorkspaceSettings{SeverityEnabled: &v})
		if err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		if out["severity_enabled"] != v {
			t.Fatalf("%v: stored %v", v, out["severity_enabled"])
		}
	}
	out, err := validateSettings(&CoreWorkspaceSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["severity_enabled"]; ok {
		t.Fatal("stored without being set")
	}
}
