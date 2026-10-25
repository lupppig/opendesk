package front

import "testing"

const (
	attInlineURL = "https://example.api.frontapp.com/messages/msg_1/download/fil_3f"
	attOtherURL  = "https://example.api.frontapp.com/messages/msg_1/download/fil_zz"
)

func testAtt(id, url, cid string) attachment {
	a := attachment{ID: id, URL: url}
	a.Metadata.CID = cid
	a.Metadata.IsInline = cid != ""
	return a
}

func TestCleanBody(t *testing.T) {
	atts := []attachment{
		testAtt("fil_3f", attInlineURL, "abc123@front"),
		testAtt("fil_zz", attOtherURL, ""),
	}
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "front-cid matches an attachment",
			raw:  `<p>hi <img src="/us-west-1/api/1/companies/co/attachments/deadbeef?resource_link_id=9" front-cid="abc123@front" alt="logo"></p>`,
			want: `<p>hi <img src="` + attInlineURL + `" alt="logo"></p>`,
		},
		{
			name: "cid link matches",
			raw:  `<img src="cid:abc123@front">`,
			want: `<img src="` + attInlineURL + `">`,
		},
		{
			name: "cid link matches with angle brackets and case",
			raw:  `<img src="cid:<ABC123@front>">`,
			want: `<img src="` + attInlineURL + `">`,
		},
		{
			name: "cid link unmatched becomes placeholder",
			raw:  `<p><img src="cid:missing@front"></p>`,
			want: `<p>[image]</p>`,
		},
		{
			name: "resource_link_id is the base36 attachment id",
			raw:  `<img src="/us-west-1/api/1/companies/co/attachments/deadbeef?resource_link_id=123">`,
			want: `<img src="` + attInlineURL + `">`,
		},
		{
			name: "relative Front path unmatched becomes escaped alt",
			raw:  `<img src="/us-west-1/api/1/companies/co/attachments/deadbeef?resource_link_id=7" alt="a <b> & c">`,
			want: `a &lt;b&gt; &amp; c`,
		},
		{
			name: "other relative link becomes placeholder",
			raw:  `x<img src="images/pic.png">y`,
			want: `x[image]y`,
		},
		{
			name: "read receipt is dropped",
			raw:  `<p>bye</p><img src="https://app.frontapp.com/api/1/noauth/companies/co/seen/abc/def.gif" width="1" height="1">`,
			want: `<p>bye</p>`,
		},
		{
			name: "proxy is unwrapped from the fragment",
			raw:  `<img src="https://ext.frontusercontent.com/v1/proxy/tok123#https://cdn.example.com/a.png?x=1&amp;y=2" alt="pic">`,
			want: `<img src="https://cdn.example.com/a.png?x=1&amp;y=2" alt="pic">`,
		},
		{
			name: "proxy without fragment becomes placeholder",
			raw:  `<img src="https://ext.frontusercontent.com/v1/proxy/tok123">`,
			want: `[image]`,
		},
		{
			name: "proxied read receipt is dropped",
			raw:  `<img src="https://ext.frontusercontent.com/v1/proxy/tok#https://app.frontapp.com/api/1/noauth/x/seen/y.gif">`,
			want: ``,
		},
		{
			name: "remote https image untouched",
			raw:  `<img  SRC="https://cdn.example.com/b.png"  alt=x>`,
			want: `<img  SRC="https://cdn.example.com/b.png"  alt=x>`,
		},
		{
			name: "data image untouched",
			raw:  `<img src="data:image/png;base64,AAAA">`,
			want: `<img src="data:image/png;base64,AAAA">`,
		},
		{
			name: "body without images passes through byte for byte",
			raw:  "<div class=\"a\">Hi &amp; bye<br/>\n<a href='x'>link</a><!-- c --></div>",
			want: "<div class=\"a\">Hi &amp; bye<br/>\n<a href='x'>link</a><!-- c --></div>",
		},
		{
			name: "other markup around a rewritten image is kept",
			raw:  `<div style="a"><b>x</b><img src="cid:abc123@front"/><br></div>`,
			want: `<div style="a"><b>x</b><img src="` + attInlineURL + `"><br></div>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanBody(tc.raw, atts); got != tc.want {
				t.Errorf("cleanBody:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestCleanBodyNoAttachments(t *testing.T) {
	raw := `<img src="cid:abc123@front" alt="sig">`
	if got := cleanBody(raw, nil); got != "sig" {
		t.Errorf("got %q, want alt text", got)
	}
}

func TestCleanText(t *testing.T) {
	const pixel = "https://app.frontapp.com/api/1/noauth/companies/acme/seen/msg_1a2b/hash/abc"
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"no receipt passes through", "Thanks!\nSee https://example.com/a\n", "Thanks!\nSee https://example.com/a\n"},
		{"bracketed receipt line dropped", "Thanks!\n\n—\nLeela\n\n[" + pixel + "]\n", "Thanks!\n\n—\nLeela\n\n"},
		{"crlf kept", "Hi\r\n[" + pixel + "]\r\nBye\r\n", "Hi\r\nBye\r\n"},
		{"quoted receipt line dropped", "Reply\n> Earlier\n> [" + pixel + "]\n> More\n", "Reply\n> Earlier\n> More\n"},
		{"mid-line receipt removed, text kept", "Sent [" + pixel + "] from Front", "Sent  from Front"},
		{"bare receipt removed", "Bye " + pixel, "Bye "},
		{"subdomain receipt removed", "[https://acme.frontapp.com/api/1/noauth/seen/x]\nok", "ok"},
		{"other noauth link kept", "[https://app.frontapp.com/api/1/noauth/companies/acme/attachments/9]", "[https://app.frontapp.com/api/1/noauth/companies/acme/attachments/9]"},
		{"lookalike host kept", "[https://frontapp.com.evil.test/noauth/seen/x]", "[https://frontapp.com.evil.test/noauth/seen/x]"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := cleanText(c.raw); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestToMessageCleansText(t *testing.T) {
	m := toMessage(message{ID: "msg_1", Body: "<p>Hi</p>", Text: "Hi\n\n[https://app.frontapp.com/api/1/noauth/companies/acme/seen/msg_1/h]\n"})
	if m.Text != "Hi\n\n" || m.HTML != "<p>Hi</p>" {
		t.Errorf("message = %q / %q", m.Text, m.HTML)
	}
}
