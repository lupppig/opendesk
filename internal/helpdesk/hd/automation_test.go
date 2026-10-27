package hd_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

func TestPhrases(t *testing.T) {
	got := hd.Phrases("  Your parcel has shipped \r\n\n\tnewsletter\n   \ninvoice #")
	if strings.Join(got, "|") != "Your parcel has shipped|newsletter|invoice #" {
		t.Fatalf("Phrases = %q", got)
	}
	if got := hd.Phrases(" \n\r\n"); len(got) != 0 {
		t.Fatalf("Phrases of blank lines = %q", got)
	}
}

func TestValidatePhraseConditions(t *testing.T) {
	validate := func(cs ...hd.Condition) ([]hd.Condition, error) {
		return hd.ValidateConditions(context.Background(), nil, uuid.Nil, cs)
	}
	lines := func(n, size int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%0*d", size, i)
		}
		return strings.Join(out, "\n")
	}

	got, err := validate(
		hd.Condition{Field: "subject", Op: "any", Value: " Your parcel has shipped \r\n\n NEWSLETTER \n"},
		hd.Condition{Field: "body", Op: "none", Value: "refund"},
		hd.Condition{Field: "from_email", Op: "any", Value: "@carrier.example\n@bank.example"},
		hd.Condition{Field: "received_at", Op: "none", Value: "Billing@Shop.example\nlegal@shop.example"},
		hd.Condition{Field: "tag", Op: "any", Value: "carrier\nnewsletter"},
		hd.Condition{Field: "subject", Op: "any", Value: lines(100, 49)},
		hd.Condition{Field: "subject", Op: "contains", Value: strings.Repeat("x", 500)},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Each phrase trimmed, empty lines dropped; received_at lowercased like its other ops.
	if got[0].Value != "Your parcel has shipped\nNEWSLETTER" || got[3].Value != "billing@shop.example\nlegal@shop.example" {
		t.Fatalf("stored values %q, %q", got[0].Value, got[3].Value)
	}

	bad := []hd.Condition{
		{Field: "subject", Op: "any", Value: ""},
		{Field: "body", Op: "none", Value: " \n \r\n"},
		{Field: "subject", Op: "any", Value: lines(101, 3)},
		{Field: "subject", Op: "any", Value: "ok\n" + strings.Repeat("x", 501)},
		{Field: "subject", Op: "any", Value: lines(11, 499)},
		{Field: "status", Op: "any", Value: "open"},
		{Field: "channel", Op: "none", Value: "email"},
		{Field: "assignee_id", Op: "any", Value: "none"},
		{Field: "subject", Op: "contains", Value: strings.Repeat("x", 501)},
		{Field: "subject", Op: "all", Value: "a\nb"},
	}
	for i, c := range bad {
		if _, err := validate(c); err == nil {
			t.Errorf("bad[%d] %s %s accepted", i, c.Field, c.Op)
		}
	}
}

func TestValidateMergeAction(t *testing.T) {
	validate := func(as ...hd.Action) ([]hd.Action, error) {
		return hd.ValidateActions(context.Background(), nil, uuid.Nil, as)
	}
	off := false
	got, err := validate(
		hd.Action{Type: "merge_into_open_ticket", Value: ""},
		hd.Action{Type: "merge_into_open_ticket", Value: " 30 ", SameSubject: &off, Field: "x"},
		hd.Action{Type: "mark_spam", Value: "", SameSubject: &off},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The window defaults to 7 days and same_subject to true; both are stored.
	if got[0].Value != "7" || got[0].SameSubject == nil || !*got[0].SameSubject || got[0].MergeWindowDays() != 7 {
		t.Fatalf("defaults %+v", got[0])
	}
	if got[1].Value != "30" || got[1].MergeSameSubject() || got[1].Field != "" || got[2].SameSubject != nil {
		t.Fatalf("stored %+v, %+v", got[1], got[2])
	}
	for _, v := range []string{"0", "31", "-1", "7d", "1.5"} {
		if _, err := validate(hd.Action{Type: "merge_into_open_ticket", Value: v}); err == nil {
			t.Errorf("window %q accepted", v)
		}
	}
	merge := []hd.Action{{Type: "add_tag", Value: "dup"}, {Type: "merge_into_open_ticket", Value: "7"}}
	if err := hd.ValidateActionTrigger("ticket_created", merge); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"customer_replied", "agent_replied", "ticket_updated"} {
		if err := hd.ValidateActionTrigger(trigger, merge); err == nil {
			t.Errorf("merge accepted on %s", trigger)
		}
		if err := hd.ValidateActionTrigger(trigger, merge[:1]); err != nil {
			t.Errorf("%s: %v", trigger, err)
		}
	}
}
