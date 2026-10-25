package imports

import (
	"reflect"
	"testing"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

func TestDropNUL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"a\x00b", "ab"},
		{"\x00\x00lead", "lead"},
		{"trail\x00", "trail"},
		{"\x00", ""},
		{"héllo\x00 wörld", "héllo wörld"},
	} {
		if got := dropNUL(c.in); got != c.want {
			t.Errorf("dropNUL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScrubTicket(t *testing.T) {
	requester := &source.Person{ID: "u\x001", Email: "a\x00@b.test", Name: "Ann\x00 Lee", Phone: "+1\x00 555", Company: "Co\x00", Domain: "b\x00.test"}
	tags := []string{"ok", "bil\x00ling"}
	to := []string{"x\x00@y.test"}
	files := []source.Attachment{{ID: "f\x001", Name: "a\x00.txt", ContentType: "text/\x00plain", URL: "https://files.test/\x00a"}}
	tk := source.Ticket{
		ID: "1\x00", Ref: "#1\x00", URL: "https://src.test/1\x00", Subject: "Sub\x00ject",
		Requester: requester, Tags: tags,
		Messages: []source.Message{{
			ID: "m\x001", HTML: "<p>a\x00b</p>", Text: "a\x00b", Author: *requester,
			To: to, Cc: []string{"clean@y.test"}, Attachments: files, MessageID: "<m\x00@y.test>",
		}},
	}
	orig := tk
	scrubTicket(&tk)

	clean := source.Person{ID: "u\x001", Email: "a@b.test", Name: "Ann Lee", Phone: "+1 555", Company: "Co", Domain: "b.test"}
	want := source.Ticket{
		ID: "1\x00", Ref: "#1", URL: "https://src.test/1", Subject: "Subject",
		Requester: &clean, Tags: []string{"ok", "billing"},
		Messages: []source.Message{{
			ID: "m\x001", HTML: "<p>ab</p>", Text: "ab", Author: clean,
			To: []string{"x@y.test"}, Cc: []string{"clean@y.test"},
			Attachments: []source.Attachment{{ID: "f\x001", Name: "a.txt", ContentType: "text/plain", URL: "https://files.test/\x00a"}},
			MessageID:   "<m\x00@y.test>",
		}},
	}
	if !reflect.DeepEqual(tk, want) {
		t.Fatalf("scrubbed\n%+v\nwant\n%+v", tk, want)
	}

	// The source's own values are left as they are.
	if requester.Name != "Ann\x00 Lee" || tags[1] != "bil\x00ling" || to[0] != "x\x00@y.test" ||
		files[0].Name != "a\x00.txt" || orig.Messages[0].Text != "a\x00b" {
		t.Error("the source's values were changed in place")
	}
}

func TestScrubArticle(t *testing.T) {
	a := source.Article{ID: "a\x001", Title: "Re\x00set", HTML: "<p>St\x00eps</p>", Collection: "Acc\x00ount"}
	scrubArticle(&a)
	want := source.Article{ID: "a\x001", Title: "Reset", HTML: "<p>Steps</p>", Collection: "Account"}
	if a != want {
		t.Fatalf("scrubbed %+v, want %+v", a, want)
	}
}
