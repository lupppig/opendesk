package templates

import (
	"strings"
	"testing"
	"time"
)

func vals(text map[string]string) *Values {
	return &Values{Text: text, HTML: map[string]string{}, Known: Known}
}

func TestRenderVariablesAndFilters(t *testing.T) {
	v := vals(map[string]string{"contact.first_name": "Jane", "team.name": "Billing", "agent.phone": ""})
	cases := map[string]string{
		"Hi {{contact.first_name}},":                              "Hi Jane,",
		"Hi {{ contact.first_name }},":                            "Hi Jane,",
		`Hi {{ agent.phone | default: "there" }}`:                 "Hi there",
		`Hi {{ agent.phone | default: 'there' | upcase }}`:        "Hi THERE",
		"{{ team.name | downcase }}":                              "billing",
		"{{ team.name | upcase }}":                                "BILLING",
		"{{ contact.last_name }}":                                 "", // known, no value
		"{{ contact.nmae }}":                                      "{{ contact.nmae }}",
		"function() { return {{x}} }":                             "function() { return {{x}} }",
		`{{ contact.first_name | default: &quot;there&quot; }}`:   "Jane",
		"{% if agent.phone %}Call {{agent.phone}}{% endif %}done": "done",
		"{% if contact.first_name %}Hi {{contact.first_name}}{% else %}Hello{% endif %}":    "Hi Jane",
		"{% unless agent.phone %}no phone{% endunless %}":                                   "no phone",
		`{% if team.name == "billing" %}B{% else %}O{% endif %}`:                            "B",
		`{% if team.name != "Billing" %}B{% else %}O{% endif %}`:                            "O",
		"{% if a.b %}x{% endif %}":                                                          "{% if a.b %}x{% endif %}",
		"{% if agent.phone %}open":                                                          "{% if agent.phone %}open",
		"stray {% endif %}":                                                                 "stray {% endif %}",
		"{% if contact.first_name %}{% if agent.phone %}p{% else %}n{% endif %}{% endif %}": "n",
	}
	for src, want := range cases {
		if got := Render(src, v, Text); got != want {
			t.Errorf("Render(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestRenderEscapesAndIsOnePass(t *testing.T) {
	v := vals(map[string]string{"contact.name": "{{ticket.subject}} <b>&", "ticket.subject": "secret"})
	got := Render("Hi {{contact.name}} / {{ticket.subject}}", v, HTML)
	if got != "Hi {{ticket.subject}} &lt;b&gt;&amp; / secret" {
		t.Fatalf("got %q", got)
	}
	if got := Render("{{contact.name}}", v, Text); got != "{{ticket.subject}} <b>&" {
		t.Fatalf("text: got %q", got)
	}
}

func TestHTMLValues(t *testing.T) {
	v := vals(nil)
	v.HTML["agent.signature"] = "<p>Alex<br>Support</p>"
	if got := Render("<div>{{ agent.signature }}</div>", v, HTML); got != "<div><p>Alex<br>Support</p></div>" {
		t.Fatalf("html: %q", got)
	}
	if got := Render("{{ agent.signature }}", v, Text); got != "Alex\nSupport" {
		t.Fatalf("text: %q", got)
	}
	v.HTML["agent.signature"] = "<p> </p>"
	if got := Render(`{{ agent.signature | default: "none" }}`, v, HTML); got != "none" {
		t.Fatalf("default: %q", got)
	}
}

func TestCheck(t *testing.T) {
	probs := Check("{{ contact.nmae }} {{ x | shout }} {% if agent.phone %}", KindSignature, nil)
	if len(probs) != 3 {
		t.Fatalf("problems: %+v", probs)
	}
	if p := Check("{{ contact.fields.plan }}", KindSignature, map[string]bool{"contact.fields.plan": true}); len(p) != 0 {
		t.Fatalf("field: %+v", p)
	}
	if p := Check("{{ contact.fields.any }}", KindSignature, nil); len(p) != 0 {
		t.Fatalf("any field without a list: %+v", p)
	}
	if p := Check("{{ contact.fields.gone }}", KindSignature, map[string]bool{}); len(p) != 1 {
		t.Fatalf("unknown field: %+v", p)
	}
	if p := Check("<p>hi</p>", KindLayout, nil); len(p) != 1 || !strings.Contains(p[0].Message, "content") {
		t.Fatalf("layout without content: %+v", p)
	}
	if p := Check("{{ survey }}", KindSignature, nil); len(p) != 1 {
		t.Fatalf("slot outside its home: %+v", p)
	}
	if p := Check("{{ agent.signature }}", KindPersonalSignature, nil); len(p) != 1 {
		t.Fatalf("self signature: %+v", p)
	}
	if p := Check("{{ survey }}", KindCSAT, nil); len(p) != 0 {
		t.Fatalf("csat: %+v", p)
	}
	ok := "{{ agent.name }}, {{ agent.title }}{% if agent.phone %} {{ agent.phone }}{% endif %} {{ agent.signature }} {{ mailbox.name }} {{ mailbox.email }} {{ workspace.name }} {{ date.today }}"
	if p := Check(ok, KindCollabSignature, nil); len(p) != 0 {
		t.Fatalf("collab signature: %+v", p)
	}
	for _, src := range []string{"{{ ticket.subject }}", "{{ contact.first_name }}", "{{ team.name }}", "{{ brand.name }}", "{{ contact.fields.plan }}"} {
		if p := Check(src, KindCollabSignature, nil); len(p) != 1 || !strings.Contains(p[0].Message, "nothing to read here") {
			t.Fatalf("collab signature %s: %+v", src, p)
		}
	}
	if p := Check("{{ agent.nmae }}", KindCollabSignature, nil); len(p) != 1 || !strings.Contains(p[0].Message, "agent.name") {
		t.Fatalf("collab did you mean: %+v", p)
	}
}

func TestSetMailbox(t *testing.T) {
	c := &Context{Values: &Values{Text: map[string]string{"agent.name": "Maya Chen"}, HTML: map[string]string{}, Known: Known},
		PersonalSignature: "<p>{{ agent.name }} for {{ mailbox.name }}</p>"}
	c.SetMailbox("Maya Chen at Margin {{ x }}", "support@margin.test")
	got := c.Fill("<p>{{ mailbox.email }}</p>{{ agent.signature }}")
	if !strings.Contains(got, "support@margin.test") || !strings.Contains(got, "Maya Chen for Maya Chen at Margin") || strings.Contains(got, "{{ x }}") {
		t.Fatalf("filled %q", got)
	}
}

func TestSanitizeDesignKeepsTags(t *testing.T) {
	in := `<p style="color: {{ brand.color | default: '#000' }}; padding: 4px">Hi {{ contact.first_name | default: "there" }}</p>` +
		`<a href="{{ brand.help_center_url }}">Help</a><a href="tel:{{agent.phone}}">Call</a><img src="{{agent.avatar_url}}" width="40">` +
		`<script>alert(1)</script><p style="background: url(javascript:x)">x</p><p onclick="x()">zqtpl0qz</p>`
	out := SanitizeDesign(in)
	for _, want := range []string{
		`style="color: {{ brand.color | default: &#39;#000&#39; }}; padding: 4px"`,
		`Hi {{ contact.first_name | default: "there" }}`,
		`href="{{ brand.help_center_url }}"`,
		`href="tel:{{agent.phone}}"`,
		`src="{{agent.avatar_url}}"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	for _, bad := range []string{"script", "javascript", "onclick", "zqtpl0qz"} {
		if strings.Contains(out, bad) {
			t.Errorf("kept %q: %s", bad, out)
		}
	}
	// Filled and cleaned for sending, the values land and relative links go.
	v := vals(map[string]string{"brand.color": "#2563EB", "contact.first_name": "Jane", "brand.help_center_url": "https://help.acme.com",
		"agent.phone": "+1 555", "agent.avatar_url": "https://api.test/v1/images/x"})
	sent := SanitizeForSend(Render(out, v, HTML))
	for _, want := range []string{`color: #2563eb`, `Hi Jane`, `href="https://help.acme.com"`, `src="https://api.test/v1/images/x"`} {
		if !strings.Contains(strings.ToLower(sent), strings.ToLower(want)) {
			t.Errorf("sent missing %q in %s", want, sent)
		}
	}
	if s := SanitizeForSend(`<a href="/relative">x</a>`); strings.Contains(s, "relative") {
		t.Errorf("relative link kept: %s", s)
	}
}

func TestRenderEmail(t *testing.T) {
	c := &Context{Values: vals(map[string]string{"brand.name": "Acme"}), Location: time.UTC, BrandColor: "#2563EB", BrandName: "Acme"}
	at := time.Date(2026, 10, 17, 14, 3, 0, 0, time.UTC)
	parts := Parts{Content: "<p>Thanks!</p>", ContentText: "Thanks!", Signature: "<p>Alex</p>",
		History: []HistoryItem{{Author: "Jane", At: at, HTML: `<p>Help <img src="https://x/y.png"></p>`}, {Author: "Alex", At: at, HTML: "<p>older</p>"}}}
	e := c.RenderEmail(DefaultDesign(), parts)
	for _, want := range []string{"<p>Thanks!</p>", "<p>Alex</p>", "On Sat, 17 Oct 2026 at 14:03, Jane wrote:", "a{color:#2563EB}"} {
		if !strings.Contains(e.HTML, want) {
			t.Errorf("html missing %q", want)
		}
	}
	if strings.Contains(e.HTML, "older") || strings.Contains(e.HTML, "y.png") {
		t.Errorf("history: only the last message, without images: %s", e.HTML)
	}
	if !strings.Contains(e.Text, "Thanks!\n\n-- \nAlex") || !strings.Contains(e.Text, "> Help") {
		t.Errorf("text: %q", e.Text)
	}

	d := DefaultDesign()
	d.Layout.Mode = "custom"
	d.CustomHTML = SanitizeDesign(`<div style="padding: 8px">{{ brand.name }}: {{ content }}</div><script>x</script>`)
	e = c.RenderEmail(d, Parts{Content: `<p><img src="cid:abc@opendesk"> hi</p>`})
	if !strings.Contains(e.HTML, `Acme: <p><img src="cid:abc@opendesk"> hi</p>`) || strings.Contains(e.HTML, "<script") {
		t.Errorf("custom: %s", e.HTML)
	}
}

func TestSurveyButtons(t *testing.T) {
	h, txt := SurveyButtons("https://app.test/csat/tok?score=", "#2563EB")
	if !strings.Contains(h, `href="https://app.test/csat/tok?score=5"`) || !strings.Contains(txt, "5 (Great): https://app.test/csat/tok?score=5") {
		t.Fatalf("%s\n%s", h, txt)
	}
}

func TestInert(t *testing.T) {
	v := vals(map[string]string{"contact.name": Inert("{{ agent.phone }}"), "agent.phone": "555"})
	once := Render("Hi {{ contact.name }}", v, HTML)
	if twice := Render(once, v, HTML); twice != once || strings.Contains(twice, "555") {
		t.Fatalf("filled twice: %q -> %q", once, twice)
	}
}
