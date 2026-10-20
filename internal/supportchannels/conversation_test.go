package supportchannels

import (
	"encoding/json"
	"testing"

	"github.com/useopendesk/opendesk/internal/ai"
)

func TestDecodeConversationAction(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"value": "pending"})
	action, err := decodeConversationAction(ai.ToolCall{Name: "status", Input: input})
	if err != nil || action.Command != "status" || action.Value != "pending" {
		t.Fatalf("action: %+v, %v", action, err)
	}
	bad, _ := json.Marshal(map[string]string{"value": "deleted"})
	if _, err = decodeConversationAction(ai.ToolCall{Name: "status", Input: bad}); err == nil {
		t.Fatal("accepted invalid status")
	}
	unknown, _ := json.Marshal(map[string]string{"text": "hello", "recipient": "attacker"})
	if _, err = decodeConversationAction(ai.ToolCall{Name: "reply", Input: unknown}); err == nil {
		t.Fatal("accepted an unknown action field")
	}
	create, _ := json.Marshal(map[string]string{"subject": "Printer is offline", "description": "Third floor"})
	action, err = decodeConversationAction(ai.ToolCall{Name: "create_ticket", Input: create})
	if err != nil || action.Subject != "Printer is offline" || action.Description != "Third floor" {
		t.Fatalf("create action: %+v, %v", action, err)
	}
	empty, _ := json.Marshal(map[string]string{"subject": " "})
	if _, err = decodeConversationAction(ai.ToolCall{Name: "create_ticket", Input: empty}); err == nil {
		t.Fatal("accepted a ticket without a subject")
	}
}

func TestConversationConfirmation(t *testing.T) {
	if confirmation("Go ahead!") != "confirm" || confirmation("never mind") != "cancel" || confirmation("maybe") != "" {
		t.Fatal("confirmation phrases were not classified safely")
	}
	if !needsConfirmation(conversationAction{Command: "reply"}) || !needsConfirmation(conversationAction{Command: "status", Value: "closed"}) || !needsConfirmation(conversationAction{Command: "create_ticket"}) {
		t.Fatal("consequential actions did not require confirmation")
	}
	if needsConfirmation(conversationAction{Command: "note"}) {
		t.Fatal("internal note unexpectedly required confirmation")
	}
}

func TestConversationHistoryIsBounded(t *testing.T) {
	var turns []conversationTurn
	for i := 0; i < 20; i++ {
		turns = appendTurns(turns, "user", "assistant")
	}
	if len(turns) != conversationTurnLimit {
		t.Fatalf("turn count = %d", len(turns))
	}
}

func TestConversationToolRequiredIsArray(t *testing.T) {
	for _, tool := range conversationTools() {
		required, ok := tool.Schema["required"].([]string)
		if !ok || required == nil {
			t.Fatalf("%s required must serialize as an array", tool.Name)
		}
	}
}

func TestCreateConversationOnlyOffersTicketCreation(t *testing.T) {
	tools := createTicketTools()
	if len(tools) != 1 || tools[0].Name != "create_ticket" {
		t.Fatalf("tools = %+v", tools)
	}
	required, ok := tools[0].Schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "subject" {
		t.Fatalf("required = %#v", tools[0].Schema["required"])
	}
}
