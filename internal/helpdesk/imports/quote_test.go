package imports

import (
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// All fixtures are made up.

// An imported email reply shows what its author wrote, with the quoted history kept whole
// behind it, as a live inbound reply does.
func TestImportedReplyCutsQuotedHistory(t *testing.T) {
	gmail := source.Message{
		HTML: `<div dir="ltr">Thanks, that works.</div><br><div class="gmail_quote"><div class="gmail_attr">On Mon, Mar 2, 2026 at 9:00 AM Leela &lt;help@acme.example&gt; wrote:<br></div>` +
			`<blockquote class="gmail_quote">Try turning it off and on again.</blockquote></div>`,
		Text: "Thanks, that works.\n\nOn Mon, Mar 2, 2026 at 9:00 AM Leela <help@acme.example> wrote:\n> Try turning it off and on again.\n",
	}
	plain := source.Message{Text: "Sounds good.\n\nOn Tue, Mar 3, 2026, Bob <bob@mail.example> wrote:\n> Can we meet?\n"}
	for name, m := range map[string]source.Message{"gmail": gmail, "plain": plain} {
		body, full, text := (&writer{}).body(m, nil, "Re: It broke", true)
		if full == "" {
			t.Fatalf("%s: nothing cut: %q", name, body)
		}
		if strings.Contains(body, "off and on") || strings.Contains(body, "Can we meet") ||
			strings.Contains(text, "off and on") || strings.Contains(text, "Can we meet") {
			t.Errorf("%s: quoted history shown: body %q text %q", name, body, text)
		}
		if !strings.Contains(full, "off and on") && !strings.Contains(full, "Can we meet") {
			t.Errorf("%s: full lost the history: %q", name, full)
		}
		if !strings.Contains(body, "works") && !strings.Contains(body, "Sounds good") {
			t.Errorf("%s: reply lost: %q", name, body)
		}
	}

	// Notes and chats keep everything; so does a forward, whose forwarded mail is the point.
	if _, full, _ := (&writer{}).body(gmail, nil, "Re: It broke", false); full != "" {
		t.Error("a message that is not an email reply was cut")
	}
	if body, full, _ := (&writer{}).body(gmail, nil, "Fwd: It broke", true); full != "" || !strings.Contains(body, "off and on") {
		t.Errorf("a forward was cut: %q", body)
	}
	// A reply with nothing quoted is stored as before.
	if body, full, _ := (&writer{}).body(source.Message{HTML: "<p>Just this.</p>"}, nil, "Hi", true); full != "" || !strings.Contains(body, "Just this") {
		t.Errorf("nothing to cut: body %q full %q", body, full)
	}
}
