package assistant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/integrations"
)

func TestExternalToolFollowsReadOnly(t *testing.T) {
	// integrations.Tools has already folded an admin's ask first into ReadOnly.
	if w := externalTool(integrations.Tool{Name: "linear__search", ReadOnly: false}); !w.Write || w.Read != nil || w.Propose == nil {
		t.Fatalf("a tool that is not read-only is not a write: %+v", w)
	}
	if r := externalTool(integrations.Tool{Name: "linear__search", ReadOnly: true}); r.Write || r.Read == nil {
		t.Fatalf("a read-only tool is not a read: %+v", r)
	}
}

func TestExternalPreviewCarriesTheCompleteArguments(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"zeta":"last key first",`)
	for i := 0; i < 14; i++ {
		fmt.Fprintf(&b, `"f%02d":"value %d",`, i, i)
	}
	b.WriteString(`"long":"` + strings.Repeat("x", 150) + `",`)
	b.WriteString(`"notes":"line one\nline two",`)
	b.WriteString(`"html":"<b>&</b>","empty":null}`)
	raw := json.RawMessage(b.String())
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	pv := externalPreview(integrations.Tool{Integration: "Linear", Title: "Create issue"}, fields, raw)
	if len(pv.Changes) != 12 || pv.Body == "" {
		t.Fatalf("summary = %d changes, body %q", len(pv.Changes), pv.Body)
	}
	// 19 fields: 12 changes and a body shown, the rest (with the empty one) counted.
	if pv.Omitted != len(fields)-13 {
		t.Fatalf("omitted = %d of %d", pv.Omitted, len(fields))
	}
	// The arguments are the sent text, only indented: same order, same escapes.
	var want bytes.Buffer
	_ = json.Indent(&want, raw, "", "  ")
	if pv.Arguments != want.String() || !strings.HasPrefix(pv.Arguments, "{\n  \"zeta\"") || !strings.Contains(pv.Arguments, `"<b>&</b>"`) {
		t.Fatalf("arguments = %s", pv.Arguments)
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, []byte(pv.Arguments))
	if compact.String() != string(raw) {
		t.Fatalf("arguments differ from the input:\n%s\n%s", compact.String(), raw)
	}
	if pv.Clipped {
		t.Fatal("nothing was clipped")
	}

	// The body is taken, so a second long value is a change cut at 300 characters.
	long := map[string]any{"a": "line one\nline two", "b": strings.Repeat("y", 400)}
	longRaw, _ := json.Marshal(long)
	pv = externalPreview(integrations.Tool{Integration: "Linear", Title: "Create issue"}, long, longRaw)
	if len(pv.Changes) != 1 || !pv.Clipped || pv.Omitted != 0 || !strings.Contains(pv.Arguments, strings.Repeat("y", 400)) {
		t.Fatalf("long value = %+v", pv)
	}
	big := map[string]any{"title": "Short", "description": strings.Repeat("z", 7000)}
	bigRaw, _ := json.Marshal(big)
	pv = externalPreview(integrations.Tool{Integration: "Linear", Title: "Create issue"}, big, bigRaw)
	if !pv.Clipped || pv.Omitted != 0 || !strings.Contains(pv.Arguments, strings.Repeat("z", 7000)) {
		t.Fatalf("clipped body: clipped %v omitted %d", pv.Clipped, pv.Omitted)
	}
}

func TestExternalArgsKeepTheText(t *testing.T) {
	in := `{"b":1,"a":{"y":true,"x":"<p>"}}`
	stored, _ := json.Marshal(externalArgs{Remote: "create_issue", Args: in})
	var back externalArgs
	if err := json.Unmarshal(stored, &back); err != nil || string(back.input()) != in {
		t.Fatalf("round trip = %q %v", back.input(), err)
	}
	// Proposals stored before Args still run.
	old := externalArgs{}
	_ = json.Unmarshal([]byte(`{"remote":"x","input":{"a":1}}`), &old)
	if string(old.input()) != `{"a":1}` {
		t.Fatalf("old input = %s", old.input())
	}
}

func TestRepeatedKey(t *testing.T) {
	for in, want := range map[string]string{
		`{}`:                                 "",
		`{"a":1,"b":2}`:                      "",
		`{"a":1,"a":2}`:                      "a",
		`{"a":{"x":1},"b":{"x":2}}`:          "",
		`{"a":{"x":1,"x":2}}`:                "x",
		`{"a":[{"x":1},{"x":2}],"b":[]}`:     "",
		`{"a":[{"x":1,"x":2}]}`:              "x",
		`{"a":[1,[2,{"k":3}]],"b":{},"a":4}`: "a",
		`{"a":"a","b":"a"}`:                  "",
	} {
		if got := repeatedKey([]byte(in)); got != want {
			t.Errorf("repeatedKey(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestAPIRequestPreview(t *testing.T) {
	a := apiArgs{Method: "PATCH", Path: "/v1/members/me/profile", Body: map[string]any{"signature_html": "<p>Jane</p>", "title": "Lead"}}
	got := a.request()
	var back struct {
		Method string         `json:"method"`
		Path   string         `json:"path"`
		Body   map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(got), &back); err != nil || back.Method != "PATCH" || back.Body["title"] != "Lead" ||
		!strings.Contains(got, "<p>Jane</p>") || strings.Contains(got, "query") {
		t.Fatalf("request = %s", got)
	}
}

func TestScrubURL(t *testing.T) {
	cases := []struct{ in, want string }{
		// Tokens in the path.
		{"https://hooks.slack.com/services/" + "T024BE7LD/B01ABCDEFGH/aBcDeFgHiJkLmNoPqRsTuV12",
			"https://hooks.slack.com/services/" + "T024BE7LD/B01ABCDEFGH/[hidden]"},
		{"https://discord.com/api/webhooks/123456789012345678/Ab3_dEf-GhIjKlMnOpQrStUvWxYz0123456789aBcDeFgHiJkLmNoPqRsTuVwXyZ01",
			"https://discord.com/api/webhooks/123456789012345678/[hidden]"},
		{"https://api.telegram.org/bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw/sendMessage",
			"https://api.telegram.org/[hidden]/sendMessage"},
		{"https://hook.eu1.make.com/abcdefghijklmnop1234567890abcdef", "https://hook.eu1.make.com/[hidden]"},
		{"https://example.com/hooks/" + strings.Repeat("ab12", 10), "https://example.com/hooks/[hidden]"},
		// Userinfo, query values and fragment values.
		{"https://user:pa55word@example.com/hook", "https://example.com/hook"},
		{"https://example.com/hook?token=abc&mode=x&flag", "https://example.com/hook?token=[hidden]&mode=[hidden]&flag"},
		{"http://example.com/cb#access_token=abc&state=1", "http://example.com/cb#access_token=[hidden]&state=[hidden]"},
		{"HTTPS://Example.com:8443/a?k=v", "https://Example.com:8443/a?k=[hidden]"},
		// Our own links, ids and ordinary slugs survive.
		{"https://app.opendesk.sh/tickets/0190c3a2-7b1e-7cc0-9f3e-2a4b5c6d7e8f", ""},
		{"https://api.opendesk.sh/v1/tickets/0190c3a2-7b1e-7cc0-9f3e-2a4b5c6d7e8f/links", ""},
		{"https://acme.opendesk.help/articles/how-to-reset-your-password-in-2024", ""},
		{"https://acme.opendesk.help/articles/0190c3a2-7b1e-7cc0-9f3e-2a4b5c6d7e8f-refund-policy", ""},
		{"https://linear.app/acme/issue/ACM-7/login-loop-after-sso-sign-in", ""},
		{"https://github.com/acme/web/pull/1234", ""},
		{"https://example.com/images/logo-2x-dark-background.png", ""},
		{"https://example.com/", ""},
		// Not an absolute http(s) URL: left alone.
		{"hello world", ""},
		{"see https://example.com/?token=abc", ""},
		{"ftp://user:pw@example.com/x", ""},
		{"/v1/tickets?token=abc", ""},
		{"mailto:someone@example.com", ""},
	}
	for _, c := range cases {
		want := c.want
		if want == "" {
			want = c.in
		}
		if got := scrubURL(c.in); got != want {
			t.Errorf("scrubURL(%q)\n got %q\nwant %q", c.in, got, want)
		}
	}
}

func TestRedactedHidesCredentialsInURLs(t *testing.T) {
	raw := json.RawMessage(`{"data":[{"id":"0190c3a2-7b1e-7cc0-9f3e-2a4b5c6d7e8f","url":"https://hooks.slack.com/services/` + `T024BE7LD/B01ABCDEFGH/aBcDeFgHiJkLmNoPqRsTuV12",
		"secret":"whsec_1","events":["ticket.created"],"links":["https://user:pw@example.com/x?sig=abc"]}]}`)
	got := redacted(raw)
	for _, leak := range []string{"aBcDeFgHiJkLmNoPqRsTuV12", "whsec_1", "pw@", "sig=abc"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted leaks %q: %s", leak, got)
		}
	}
	for _, keep := range []string{"0190c3a2-7b1e-7cc0-9f3e-2a4b5c6d7e8f", "hooks.slack.com/services/T024BE7LD", "ticket.created", "sig=[hidden]"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("redacted lost %q: %s", keep, got)
		}
	}
}
