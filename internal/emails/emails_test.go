package emails

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/mailer"
)

func TestPreviewsRender(t *testing.T) {
	s := Site{AppURL: "https://app.example", APIURL: "https://api.example", Contact: Contact}
	list := Previews(s, time.Date(2026, 10, 13, 9, 41, 0, 0, time.UTC))
	if len(list) < 19 {
		t.Fatalf("previews: %d", len(list))
	}
	for _, p := range list {
		m := p.Mail
		if m.Subject == "" || m.HTML == "" || m.Text == "" {
			t.Errorf("%s: empty part", p.Name)
		}
		if strings.Contains(m.HTML, "{{") || strings.Contains(m.Subject, "\n") || strings.Contains(m.Subject, "—") || strings.Contains(m.Text, "—") {
			t.Errorf("%s: template leftovers, a broken subject or an em dash", p.Name)
		}
		if strings.HasPrefix(p.Name, "notify") || strings.HasPrefix(p.Name, "digest") {
			if m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || !strings.Contains(m.Text, "/unsubscribe?token=") ||
				!strings.Contains(m.Text, "/settings/notifications?ws=") {
				t.Errorf("%s: unsubscribe missing", p.Name)
			}
		}
	}
}

func TestNotificationSubjects(t *testing.T) {
	s := Site{AppURL: "https://app.example", APIURL: "https://api.example"}
	tid := uuid.New()
	long := strings.Repeat("word ", 60)
	one := func(n Note) string {
		return Notifications(s, Batch{WorkspaceID: uuid.New(), Workspace: "Margin", Notes: []Note{n}}).Subject
	}
	n := Note{Type: "assigned", TicketID: &tid, TicketNumber: 1095, TicketSubject: "SAML with Okta", Data: map[string]any{"actor": map[string]any{"name": "Maya"}}}
	if got := one(n); got != "Maya assigned you #1095: SAML with Okta" {
		t.Errorf("assigned: %q", got)
	}
	n.TicketSubject = "Line one\r\nBcc: x@example.com " + long
	if got := one(n); strings.ContainsAny(got, "\r\n") || len([]rune(got)) > 120 {
		t.Errorf("subject not flattened and clipped: %q", got)
	}
	if got := one(Note{Type: "new_ticket", TicketID: &tid, TicketNumber: 7, TicketSubject: "Hi", Data: map[string]any{"contact": map[string]any{"name": "Sam"}}}); got != "New ticket from Sam #7: Hi" {
		t.Errorf("new ticket: %q", got)
	}
	if got := one(Note{Type: "new_ticket", TicketID: &tid, TicketNumber: 7, TicketSubject: "Hi"}); got != "New ticket #7: Hi" {
		t.Errorf("new ticket without a contact: %q", got)
	}
	if got := one(Note{Type: "ai_balance_out", Data: map[string]any{"remaining_usd": 0.0}}); got != "Your AI balance is used up in Margin" {
		t.Errorf("billing: %q", got)
	}
	// A long preview is cut to a short excerpt.
	m := Notifications(s, Batch{WorkspaceID: uuid.New(), Workspace: "Margin", Notes: []Note{{Type: "mentioned", TicketID: &tid, TicketNumber: 1, TicketSubject: "x",
		Data: map[string]any{"preview": long}}}})
	if strings.Contains(m.Text, long[:SnippetRunes+10]) {
		t.Error("preview not clipped")
	}
	// More than MaxListed: counted, with no per-ticket threading.
	var notes []Note
	for range MaxListed {
		notes = append(notes, n)
	}
	b := Notifications(s, Batch{WorkspaceID: uuid.New(), Workspace: "Margin", Notes: notes, More: 5})
	if b.Subject != "Maya assigned you #1095 and 24 more" || !strings.Contains(b.Text, "And 5 more") || b.Headers["References"] != "" {
		t.Errorf("list: %q %v", b.Subject, b.Headers)
	}
}

// A web push reads like the notification's email: its heading, the ticket on one short
// line, and the same link into its workspace.
func TestWebPushText(t *testing.T) {
	s := Site{AppURL: "https://app.example"}
	wid, tid := uuid.New(), uuid.New()
	title, body, link := WebPush(s, wid, Note{Type: "assigned", TicketID: &tid, TicketNumber: 1095,
		TicketSubject: "SAML with\r\nOkta", Data: map[string]any{"actor": map[string]any{"name": "Maya"}}})
	if title != "Maya assigned you a ticket" || body != "#1095 SAML with Okta" ||
		link != "https://app.example/inbox/mine/"+tid.String()+"?ws="+wid.String() {
		t.Errorf("assigned: %q %q %q", title, body, link)
	}
	_, body, _ = WebPush(s, wid, Note{Type: "mentioned", TicketID: &tid, TicketNumber: 1, TicketSubject: strings.Repeat("word ", 60)})
	if len([]rune(body)) > 120 {
		t.Errorf("long subject not clipped: %d runes", len([]rune(body)))
	}
	title, body, link = WebPush(s, wid, Note{Type: "ai_balance_out"})
	if title != "Your AI balance is used up" || body != "" || link != "https://app.example/settings/billing?ws="+wid.String() {
		t.Errorf("billing: %q %q %q", title, body, link)
	}
	// A new ticket nobody is assigned to opens in Unassigned and names its customer.
	title, body, link = WebPush(s, wid, Note{Type: "new_ticket", TicketID: &tid, TicketNumber: 1121, TicketSubject: "Gmail",
		Data: map[string]any{"channel": "email", "contact": map[string]any{"name": "Sam Ortiz"}}})
	if title != "New ticket from Sam Ortiz" || body != "#1121 Gmail" || link != "https://app.example/inbox/unassigned/"+tid.String()+"?ws="+wid.String() {
		t.Errorf("new ticket: %q %q %q", title, body, link)
	}
	_, body, _ = WebPush(s, wid, Note{Type: "collab_mention", TicketSubject: "Quarterly\nnumbers"})
	if body != "Quarterly numbers" {
		t.Errorf("collab: %q", body)
	}
}

// A list email groups by ticket, collapses one person's bulk assignment, leads with what
// is urgent, and its subject names the most pressing line and how many more there are.
func TestListGroupsAndLeads(t *testing.T) {
	s := Site{AppURL: "https://app.example"}
	now := time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC)
	ticket := func(n int64) *uuid.UUID { id := uuid.NewSHA1(uuid.Nil, []byte{byte(n)}); return &id }
	who := func(name string) map[string]any { return map[string]any{"name": name} }
	var notes []Note
	at := now
	add := func(n Note) {
		at = at.Add(-time.Minute)
		n.CreatedAt = at
		notes = append(notes, n)
	}
	add(Note{Type: "customer_replied", TicketID: ticket(1), TicketNumber: 1102, TicketSubject: "Export", Data: map[string]any{"contact": who("Dana Lee"), "preview": "Still missing"}})
	add(Note{Type: "sla_breached", TicketID: ticket(2), TicketNumber: 1110, TicketSubject: "Refund"})
	add(Note{Type: "customer_replied", TicketID: ticket(1), TicketNumber: 1102, TicketSubject: "Export", Data: map[string]any{"contact": who("Dana Lee"), "preview": "First"}})
	for i := range 4 {
		add(Note{Type: "assigned", TicketID: ticket(int64(10 + i)), TicketNumber: int64(1200 + i), TicketSubject: "Bulk", Data: map[string]any{"actor": who("Maya Chen")}})
	}
	add(Note{Type: "ai_balance_low", Data: map[string]any{"remaining_usd": 2.0}})
	add(Note{Type: "new_ticket", TicketID: ticket(30), TicketNumber: 1300, TicketSubject: "Hello", Data: map[string]any{"channel": "chat"}})
	add(Note{Type: "new_ticket", TicketID: ticket(31), TicketNumber: 1301, TicketSubject: "Hi", Data: map[string]any{"channel": "email"}})
	m := Notifications(s, Batch{WorkspaceID: uuid.New(), Workspace: "Margin", Notes: notes, Now: now,
		Urgent: func(t string) bool { return t == "sla_breached" }})
	if m.Subject != "First response target missed #1110 and 9 more" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, want := range []string{"NEEDS YOU NOW", "TICKETS", "WORKSPACE", "Dana Lee replied twice", "Maya Chen assigned you 4 tickets",
		"#1200, #1201, #1202 and 1 more", "> Still missing", "Since 14:50: 4 assigned, 2 new tickets, 2 replies, 1 missed target, 1 AI or billing notice"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("missing %q in\n%s", want, m.Text)
		}
	}
	if i, j := strings.Index(m.Text, "NEEDS YOU NOW"), strings.Index(m.Text, "TICKETS"); i > j {
		t.Error("urgent section is not first")
	}
	// Two assignments are not a bulk action.
	two := Notifications(s, Batch{WorkspaceID: uuid.New(), Workspace: "Margin", Now: now, Notes: notes[3:5]})
	if strings.Contains(two.Text, "assigned you 2 tickets") {
		t.Error("two assignments collapsed")
	}
}

// The company line shows only when the instance sets it.
func TestLegalLineOnlyWhenSet(t *testing.T) {
	for _, m := range []mailer.Mail{VerifyEmail(Site{AppURL: "https://a"}, "https://a/v"), Notifications(Site{AppURL: "https://a"}, Batch{Workspace: "W", Notes: SampleNotes(time.Now())})} {
		if strings.Contains(m.HTML+m.Text, "Mindroot") || strings.Contains(m.HTML+m.Text, "16543299") || strings.Contains(m.HTML+m.Text, "Registered office") {
			t.Error("company shown without config")
		}
	}
	s := Site{AppURL: "https://a", Legal: "Acme Ltd, registered in England and Wales, company number 1. Registered office: 1 High St."}
	if m := VerifyEmail(s, "https://a/v"); !strings.Contains(m.HTML, "company number 1") || !strings.Contains(m.Text, "company number 1") {
		t.Error("company line missing")
	}
}

// Times read on the recipient's clock: 24-hour unless they chose 12-hour, in their zone.
func TestWhenFollowsClock(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	now := time.Date(2026, 10, 13, 18, 30, 0, 0, time.UTC) // 14:30 in New York
	cases := []struct {
		clock string
		at    time.Time
		want  string
	}{
		{"", now.Add(-25 * time.Minute), "14:05"},
		{"auto", now.Add(-25 * time.Minute), "14:05"},
		{"24h", now.Add(-25 * time.Minute), "14:05"},
		{"12h", now.Add(-25 * time.Minute), "2:05 PM"},
		{"12h", now.Add(-14*time.Hour - 25*time.Minute), "12:05 AM"},
		{"24h", now.Add(-26 * time.Hour), "Mon 12:30"},
		{"12h", now.Add(-26 * time.Hour), "Mon 12:30 PM"},
		{"12h", now.Add(-10 * 24 * time.Hour), "Oct 3"},
	}
	for _, c := range cases {
		if got := when(c.at, now, loc, c.clock); got != c.want {
			t.Errorf("when(%v, %q) = %q, want %q", c.at, c.clock, got, c.want)
		}
	}
	tid := uuid.New()
	notes := []Note{
		{Type: "assigned", TicketID: &tid, TicketNumber: 1, TicketSubject: "a", CreatedAt: now.Add(-5 * time.Minute)},
		{Type: "assigned", TicketID: &tid, TicketNumber: 1, TicketSubject: "a", CreatedAt: now.Add(-25 * time.Minute)},
	}
	b := Batch{WorkspaceID: uuid.New(), Workspace: "Margin", Notes: notes, Location: loc, Now: now}
	if m := Notifications(Site{AppURL: "https://app.example"}, b); !strings.Contains(m.Text, "Since 14:05:") {
		t.Errorf("default clock summary:\n%s", m.Text)
	}
	b.Clock = "12h"
	if m := Notifications(Site{AppURL: "https://app.example"}, b); !strings.Contains(m.Text, "Since 2:05 PM:") || !strings.Contains(m.Text, "2:25 PM") {
		t.Errorf("12-hour summary:\n%s", m.Text)
	}
}
