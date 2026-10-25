package emails

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/mailer"
)

// MaxListed is how many notifications one email lists; the rest are counted.
const MaxListed = 20

// compactAfter is the list length above which items leave out their excerpts.
const compactAfter = 8

// SnippetRunes bounds a quoted excerpt.
const SnippetRunes = 140

// Note is one notification to email. Data is the notification's data (contracts/api.md);
// only names, counts and the short preview are read from it.
type Note struct {
	ID            uuid.UUID
	Type          string
	Data          map[string]any
	TicketID      *uuid.UUID
	TicketNumber  int64
	TicketSubject string
	CreatedAt     time.Time
}

// Batch is what one notification email carries: one note gets its own email, several a list.
type Batch struct {
	WorkspaceID uuid.UUID
	Workspace   string
	// Cadence names the schedule that sent it: instant, hourly or daily.
	Cadence string
	// Notes, newest first, at most MaxListed; More counts the others.
	Notes []Note
	More  int
	// Location formats times; UTC when nil.
	Location *time.Location
	// Clock is the recipient's time format (users.time_format): "12h" reads 2:05 PM,
	// anything else 14:05.
	Clock string
	// Now is the send time, for formatting; time.Now when zero.
	Now time.Time
	// UnsubscribeToken is a notification_unsubscribe token for the recipient and workspace.
	UnsubscribeToken string
	// Test marks a sample email sent from the settings page.
	Test bool
	// Urgent reports the types that lead a list under "Needs you now"; nil means none.
	Urgent func(typ string) bool
}

// Notifications builds a notification email: a specific one for a single notification,
// a list for several.
func Notifications(s Site, b Batch) mailer.Mail {
	if b.Location == nil {
		b.Location = time.UTC
	}
	if b.Now.IsZero() {
		b.Now = time.Now()
	}
	ws := b.WorkspaceID.String()
	footer := mailer.Footer{
		Links: []mailer.Link{
			{Label: "Notification settings", URL: s.App("/settings/notifications", "ws", ws)},
		},
		Contact: s.Contact,
	}
	if b.UnsubscribeToken != "" {
		footer.Links = append(footer.Links, mailer.Link{Label: "Unsubscribe", URL: s.App("/unsubscribe", "token", b.UnsubscribeToken)})
	}
	// Newest first, whatever order the caller had them in.
	b.Notes = slices.Clone(b.Notes)
	slices.SortStableFunc(b.Notes, func(x, y Note) int { return y.CreatedAt.Compare(x.CreatedAt) })
	var e mailer.Email
	if len(b.Notes) == 1 && b.More == 0 {
		e = single(s, b, b.Notes[0])
		footer.Reason = "You get this email because email notifications for " + typeLabel(b.Notes[0].Type) + " are on in " + b.Workspace + "."
	} else {
		e = list(s, b)
		footer.Reason = "You get this email because email notifications are on in " + b.Workspace + "."
	}
	if b.Test {
		e.Subject = "Test: " + e.Subject
		e.Intro = []string{"This is a test from your notification settings. The tickets and names below are made up; real notification emails look like this."}
	}
	e.Subject = mailer.HeaderSafe(e.Subject)
	e.Eyebrow = b.Workspace
	e.Footer = footer
	m := s.build(e)
	m.Headers = map[string]string{}
	if b.UnsubscribeToken != "" && s.APIURL != "" {
		m.Headers["List-Unsubscribe"] = "<" + strings.TrimRight(s.APIURL, "/") + "/v1/notifications/unsubscribe/one-click?" +
			url.Values{"token": {b.UnsubscribeToken}}.Encode() + ">"
		m.Headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
	}
	// Emails about one ticket thread together in mail clients that follow References.
	if t := oneTicket(b.Notes); t != nil && b.More == 0 {
		m.Headers["References"] = "<ticket-" + t.String() + "@" + mailDomain(s) + ">"
	}
	return m
}

func single(s Site, b Batch, n Note) mailer.Email {
	d := describe(n)
	e := mailer.Email{Heading: d.heading, Intro: d.intro, Facts: d.facts}
	link := noteURL(s, b.WorkspaceID, n)
	if n.TicketID != nil {
		ref := fmt.Sprintf("#%d", n.TicketNumber)
		subject := clip(n.TicketSubject, 80)
		e.Subject = d.subject + " " + ref + ": " + subject
		e.Ticket = &mailer.TicketCard{Ref: ref, Subject: n.TicketSubject, Meta: d.meta, Snippet: snippet(n)}
		e.Button = &mailer.Button{Label: "Open ticket", URL: link}
		e.Preheader = strings.TrimSpace(ref + " " + n.TicketSubject)
		if sn := snippet(n); sn != "" {
			e.Preheader = sn
		}
	} else {
		e.Subject = d.heading
		if d.subjectTail {
			e.Subject += " in " + b.Workspace
		}
		e.Button = &mailer.Button{Label: d.cta, URL: link}
		e.Preheader = firstOr(d.intro, d.heading)
	}
	return e
}

// bulkAfter is how many assignments by one person in one email collapse into one line.
const bulkAfter = 3

// group is one line of a list email: a ticket's notifications, one person's bulk
// assignment, or a notification without a ticket.
type group struct {
	notes  []Note // newest first
	bulk   bool
	urgent bool
}

func (g group) latest() Note { return g.notes[0] }

// groupNotes collapses notes (newest first) into lines: several assignments by one person
// become one, then whatever is left is grouped by ticket. Groups keep the order of their
// newest notification.
func groupNotes(notes []Note, urgent func(string) bool) []group {
	// One person's assignments on bulkAfter or more different tickets are a bulk action.
	byActor := map[string][]int{}
	tickets := map[string]map[uuid.UUID]bool{}
	for i, n := range notes {
		if n.Type == "assigned" && n.TicketID != nil {
			a := nameIn(n.Data, "actor")
			byActor[a] = append(byActor[a], i)
			if tickets[a] == nil {
				tickets[a] = map[uuid.UUID]bool{}
			}
			tickets[a][*n.TicketID] = true
		}
	}
	inBulk := map[int]bool{}
	var out []group
	for i, n := range notes {
		if inBulk[i] {
			continue
		}
		if n.Type == "assigned" && n.TicketID != nil {
			if a := nameIn(n.Data, "actor"); len(tickets[a]) >= bulkAfter {
				idx := byActor[a]
				g := group{bulk: true}
				for _, j := range idx {
					g.notes = append(g.notes, notes[j])
					inBulk[j] = true
				}
				out = append(out, g)
				continue
			}
		}
		if n.TicketID != nil {
			found := false
			for k := range out {
				if !out[k].bulk && out[k].notes[0].TicketID != nil && *out[k].notes[0].TicketID == *n.TicketID {
					out[k].notes = append(out[k].notes, n)
					found = true
					break
				}
			}
			if found {
				continue
			}
		}
		out = append(out, group{notes: []Note{n}})
	}
	for k := range out {
		for _, n := range out[k].notes {
			if urgent != nil && urgent(n.Type) {
				out[k].urgent = true
			}
		}
	}
	return out
}

// times is "", " twice" or " 3 times".
func times(n int) string {
	switch {
	case n <= 1:
		return ""
	case n == 2:
		return " twice"
	}
	return fmt.Sprintf(" %d times", n)
}

// groupTitle says what happened in a group: "Dana Lee replied twice, Maya Chen mentioned you".
func groupTitle(g group) string {
	if g.bulk {
		who := or(nameIn(g.latest().Data, "actor"), "Someone")
		seen := map[uuid.UUID]bool{}
		for _, n := range g.notes {
			seen[*n.TicketID] = true
		}
		return fmt.Sprintf("%s assigned you %d tickets", who, len(seen))
	}
	var order []string
	count := map[string]int{}
	for i := len(g.notes) - 1; i >= 0; i-- { // oldest first reads as a story
		t := describe(g.notes[i]).title
		if count[t] == 0 {
			order = append(order, t)
		}
		count[t]++
	}
	parts := make([]string, len(order))
	for i, t := range order {
		parts[i] = t + times(count[t])
	}
	return strings.Join(parts, ", ")
}

// groupTone is the dot of the group's most pressing notification.
func groupTone(g group, urgent func(string) bool) string {
	for _, n := range g.notes {
		if urgent != nil && urgent(n.Type) {
			return toneOf(n)
		}
	}
	return toneOf(g.latest())
}

// summaryLabels name notification counts in the summary line.
var summaryLabels = map[string][2]string{
	"assigned":         {"assigned", "assigned"},
	"new_ticket":       {"new ticket", "new tickets"},
	"mentioned":        {"mention", "mentions"},
	"customer_replied": {"reply", "replies"},
	"thread_replied":   {"side conversation answer", "side conversation answers"},
	"sla_breached":     {"missed target", "missed targets"},
	"ai_handoff":       {"handoff", "handoffs"},
	"team_joined":      {"team change", "team changes"},
	"collab_assigned":  {"conversation assigned", "conversations assigned"},
	"collab_mention":   {"comment mention", "comment mentions"},
	"import_finished":  {"import", "imports"},
	"ai_billing":       {"AI or billing notice", "AI or billing notices"},
}

// summary is "Since 14:05: 2 assigned, 1 mention, 3 replies".
func summary(notes []Note, now time.Time, loc *time.Location, clock string) string {
	if len(notes) < 2 {
		return ""
	}
	oldest := notes[0].CreatedAt
	count := map[string]int{}
	for _, n := range notes {
		if n.CreatedAt.Before(oldest) {
			oldest = n.CreatedAt
		}
		k := n.Type
		if strings.HasPrefix(k, "ai_") && k != "ai_handoff" || k == "auto_recharge_failed" {
			k = "ai_billing"
		}
		if _, ok := summaryLabels[k]; !ok {
			k = "other"
		}
		count[k]++
	}
	var parts []string
	for _, k := range []string{"assigned", "new_ticket", "mentioned", "customer_replied", "thread_replied", "sla_breached", "ai_handoff", "collab_assigned", "collab_mention", "team_joined", "import_finished", "ai_billing", "other"} {
		n := count[k]
		if n == 0 {
			continue
		}
		l, ok := summaryLabels[k]
		if !ok {
			l = [2]string{"other", "other"}
		}
		label := l[1]
		if n == 1 {
			label = l[0]
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, label))
	}
	return "Since " + when(oldest, now, loc, clock) + ": " + strings.Join(parts, ", ")
}

func list(s Site, b Batch) mailer.Email {
	count := len(b.Notes) + b.More
	noun := "notifications"
	if count == 1 {
		noun = "notification"
	}
	groups := groupNotes(b.Notes, b.Urgent)
	// The subject leads with the most pressing line: the first urgent group, else the newest.
	lead := groups[0]
	for _, g := range groups {
		if g.urgent {
			lead = g
			break
		}
	}
	// The subject names one notification, the most pressing of that line, and counts the rest.
	leadTitle, rest := groupTitle(lead), count-len(lead.notes)
	if !lead.bulk {
		n := lead.latest()
		for _, x := range lead.notes {
			if b.Urgent != nil && b.Urgent(x.Type) {
				n = x
				break
			}
		}
		leadTitle, rest = describe(n).title, count-1
		if n.TicketID != nil {
			leadTitle += fmt.Sprintf(" #%d", n.TicketNumber)
		}
	}
	e := mailer.Email{}
	switch b.Cadence {
	case "hourly", "daily":
		e.Heading = "Your hourly digest"
		if b.Cadence == "daily" {
			e.Heading = "Your daily digest"
		}
		e.Subject = fmt.Sprintf("%s: %d %s in %s", e.Heading, count, noun, b.Workspace)
		e.Intro = []string{fmt.Sprintf("%d unread %s in %s since your last digest.", count, noun, b.Workspace)}
	default:
		e.Subject = leadTitle
		if rest > 0 {
			e.Subject += fmt.Sprintf(" and %d more", rest)
		}
		e.Heading = fmt.Sprintf("%d new %s", count, noun)
		e.Intro = []string{fmt.Sprintf("Waiting for you in %s, unread in the app.", b.Workspace)}
	}
	e.Summary = summary(b.Notes, b.Now, b.Location, b.Clock)

	compact := len(groups) > compactAfter
	var now, tickets, other []mailer.Item
	var titles []string
	for _, g := range groups {
		it := s.groupItem(b, g, compact)
		titles = append(titles, it.Title)
		switch {
		case g.urgent:
			now = append(now, it)
		case g.bulk || g.latest().TicketID != nil:
			tickets = append(tickets, it)
		default:
			other = append(other, it)
		}
	}
	titled := len(now) > 0 || (len(tickets) > 0 && len(other) > 0)
	add := func(title string, urgent bool, items []mailer.Item) {
		if len(items) == 0 {
			return
		}
		if !titled {
			title = ""
		}
		e.Sections = append(e.Sections, mailer.Section{Title: title, Urgent: urgent, Items: items})
	}
	add("Needs you now", true, now)
	add("Tickets", false, tickets)
	add("Workspace", false, other)

	e.Preheader = clip(strings.Join(titles, ", "), 140)
	if b.More > 0 {
		e.More = fmt.Sprintf("And %d more in opendesk.", b.More)
	}
	e.Button = &mailer.Button{Label: "Open opendesk", URL: s.App("/", "ws", b.WorkspaceID.String())}
	return e
}

// groupItem is one list line: what happened, the ticket, the newest excerpt and a link.
func (s Site) groupItem(b Batch, g group, compact bool) mailer.Item {
	n := g.latest()
	it := mailer.Item{Title: groupTitle(g), Time: when(n.CreatedAt, b.Now, b.Location, b.Clock), Tone: groupTone(g, b.Urgent)}
	if g.bulk {
		refs := make([]string, 0, 3)
		for i, x := range g.notes {
			if i == 3 {
				break
			}
			refs = append(refs, fmt.Sprintf("#%d", x.TicketNumber))
		}
		it.Subject = strings.Join(refs, ", ")
		if len(g.notes) > 3 {
			it.Subject += fmt.Sprintf(" and %d more", len(g.notes)-3)
		}
		it.URL = s.App("/inbox/mine", "ws", b.WorkspaceID.String())
		return it
	}
	it.URL = noteURL(s, b.WorkspaceID, n)
	if n.TicketID != nil {
		it.Ref = fmt.Sprintf("#%d", n.TicketNumber)
		it.Subject = n.TicketSubject
	}
	if !compact {
		for _, x := range g.notes { // the newest excerpt in the group
			if sn := snippet(x); sn != "" {
				it.Snippet = sn
				break
			}
		}
	}
	return it
}

// copyOf is everything an email says about one notification.
type copyOf struct {
	title   string // list line: "Maya Chen assigned you"
	subject string // single, before the ticket: "Maya Chen assigned you"
	heading string
	intro   []string
	meta    string
	facts   []mailer.Fact
	cta     string
	// subjectTail adds " in <workspace>" to a single email's subject without a ticket.
	subjectTail bool
}

func describe(n Note) copyOf {
	actor := nameIn(n.Data, "actor")
	contact := nameIn(n.Data, "contact")
	switch n.Type {
	case "assigned":
		if actor == "" {
			return copyOf{title: "Assigned to you", subject: "You were assigned", heading: "A ticket was assigned to you",
				intro: []string{"It now shows in your inbox under Mine."}, meta: "Assigned to you"}
		}
		return copyOf{title: actor + " assigned you", subject: actor + " assigned you", heading: actor + " assigned you a ticket",
			intro: []string{"It now shows in your inbox under Mine."}, meta: "Assigned to you"}
	case "new_ticket":
		meta := "Nobody is assigned yet"
		switch str(n.Data, "channel") {
		case "email":
			meta = "By email, nobody assigned yet"
		case "chat":
			meta = "In chat, nobody assigned yet"
		}
		if contact == "" {
			return copyOf{title: "New ticket", subject: "New ticket", heading: "A new ticket came in",
				intro: []string{"Nobody is assigned to it yet."}, meta: meta}
		}
		return copyOf{title: "New ticket from " + contact, subject: "New ticket from " + contact, heading: "New ticket from " + contact,
			intro: []string{"Nobody is assigned to it yet."}, meta: meta}
	case "mentioned":
		who := or(actor, "A teammate")
		return copyOf{title: who + " mentioned you", subject: who + " mentioned you on", heading: who + " mentioned you", meta: "In an internal note"}
	case "customer_replied":
		who := or(contact, "The customer")
		return copyOf{title: who + " replied", subject: who + " replied on", heading: who + " replied", meta: "On a ticket assigned to you"}
	case "thread_replied":
		who := nameIn(n.Data, "from")
		if f, ok := n.Data["from"].(map[string]any); ok && who == "" {
			who, _ = f["email"].(string)
		}
		who = or(clip(who, 60), "Someone outside the team")
		meta := "In a side conversation on a ticket assigned to you"
		if subj := str(n.Data, "subject"); subj != "" {
			meta = "In the side conversation " + subj
		}
		return copyOf{title: who + " answered your side conversation", subject: who + " answered your side conversation on", heading: who + " answered your side conversation", meta: meta}
	case "sla_breached":
		what := "The first response target"
		if str(n.Data, "kind") == "resolution" {
			what = "The resolution target"
		}
		short := strings.Replace(what, "The ", "", 1)
		return copyOf{title: capitalize(short) + " missed", subject: capitalize(short) + " missed on", heading: capitalize(short) + " missed",
			intro: []string{what + " of a ticket assigned to you has passed."}, meta: "Assigned to you"}
	case "ai_handoff":
		if str(n.Data, "via") == "contact" {
			who := or(contact, "A customer")
			return copyOf{title: who + " asked for a person", subject: who + " asked for a person on", heading: who + " asked for a person",
				intro: []string{"The customer asked Ollie to talk to someone from the team."}, meta: "Handed to the team"}
		}
		return copyOf{title: "Ollie handed a conversation to the team", subject: "Ollie handed over", heading: "Ollie handed a conversation to the team",
			intro: []string{"Ollie could not resolve this conversation on its own and passed it to a person."}, meta: or(contact, "Handed to the team")}
	case "team_joined":
		who := or(nameIn(n.Data, "user"), "Someone")
		team := teamName(n.Data)
		if team == "" {
			return copyOf{title: who + " joined a team you lead", heading: who + " joined a team you lead", intro: []string{who + " joined a team you lead."}, cta: "Open teams", subjectTail: true}
		}
		intro := who + " joined " + team + ", a team you lead."
		if b, _ := teamField(n.Data, "private").(bool); b {
			intro = who + " joined the private team " + team + ", which you lead, and can now see its tickets."
		}
		return copyOf{title: who + " joined " + team, heading: who + " joined " + team, intro: []string{intro}, cta: "Open team", subjectTail: true}
	case "ai_cap_reached":
		c := copyOf{title: "The chat agent reached its monthly cap", heading: "The chat agent reached its monthly cap",
			intro: []string{"Ollie stopped answering in the chat widget because this month's spending cap is reached. Visitors can still write to the team.",
				"It starts again next month, or when an admin raises the cap."},
			cta: "Open AI settings", subjectTail: true}
		if v, ok := num(n.Data, "cap_usd"); ok {
			c.facts = []mailer.Fact{{Label: "Monthly cap", Value: usd(v)}}
		}
		return c
	case "ai_key_failed":
		provider := providerName(str(n.Data, "provider"))
		if str(n.Data, "reason") == "no_funds" {
			return copyOf{title: "Your " + provider + " key is out of credit", heading: "Your " + provider + " key is out of credit",
				intro: []string{"AI requests in this workspace use your own " + provider + " key, and " + provider + " refused them for lack of credit. AI features are paused until the key works again."},
				cta:   "Open AI settings", subjectTail: true}
		}
		return copyOf{title: "Your " + provider + " key was refused", heading: "Your " + provider + " key was refused",
			intro: []string{"AI requests in this workspace use your own " + provider + " key, and " + provider + " refused it. Check the key or replace it to bring AI features back."},
			cta:   "Open AI settings", subjectTail: true}
	case "ai_balance_low":
		c := copyOf{title: "AI balance is running low", heading: "Your AI balance is running low",
			intro: []string{"Ollie keeps working until the balance runs out. Add AI usage or turn on auto-recharge so it does not stop."},
			cta:   "Open billing", subjectTail: true}
		if v, ok := num(n.Data, "remaining_usd"); ok {
			c.facts = []mailer.Fact{{Label: "Remaining", Value: usd(v)}}
		}
		return c
	case "ai_balance_out":
		c := copyOf{title: "AI balance is used up", heading: "Your AI balance is used up",
			intro: []string{"Ollie has paused: the chat widget agent stops answering and drafts, summaries and other AI features are unavailable until the balance is topped up or the monthly allowance resets.",
				"Add AI usage or turn on auto-recharge to keep Ollie working."},
			cta: "Add AI usage", subjectTail: true}
		if v, ok := num(n.Data, "remaining_usd"); ok {
			c.facts = []mailer.Fact{{Label: "Remaining", Value: usd(v)}}
		}
		return c
	case "auto_recharge_failed":
		return copyOf{title: "Auto-recharge failed", heading: "We could not recharge your AI balance",
			intro: []string{rechargeReason(str(n.Data, "reason")) + " Auto-recharge is now off, so nothing else will be charged.",
				"Update the payment method or add AI usage, then turn auto-recharge back on."},
			cta: "Open billing", subjectTail: true}
	case "collab_assigned", "collab_mention":
		return collabCopy(n, actor)
	case "import_finished":
		return importCopy(n.Data)
	}
	return copyOf{title: "New notification", heading: "You have a new notification", cta: "Open opendesk", subjectTail: true}
}

// TypeLabels name the notification types in plain words (also the settings page's labels).
var TypeLabels = map[string]string{
	"assigned":             "assignments",
	"new_ticket":           "new tickets nobody is assigned to",
	"mentioned":            "mentions",
	"customer_replied":     "customer replies",
	"thread_replied":       "answers in your side conversations",
	"sla_breached":         "SLA breaches",
	"ai_handoff":           "handoffs to a person",
	"team_joined":          "team changes",
	"collab_assigned":      "conversations assigned to you",
	"collab_mention":       "mentions in conversation comments",
	"ai_cap_reached":       "the chat agent's monthly cap",
	"ai_key_failed":        "AI key problems",
	"ai_balance_low":       "a low AI balance",
	"ai_balance_out":       "a used-up AI balance",
	"auto_recharge_failed": "auto-recharge problems",
	"import_finished":      "finished imports",
}

func typeLabel(t string) string {
	if l, ok := TypeLabels[t]; ok {
		return l
	}
	return "this kind of notification"
}

// WebPush is what a browser shows for one notification (contracts/native.md "Web
// Push"): the heading its email would have, the ticket's number and subject on one
// short line, and the link a click opens.
func WebPush(s Site, wid uuid.UUID, n Note) (title, body, link string) {
	subject := strings.Join(strings.Fields(n.TicketSubject), " ")
	switch {
	case n.TicketID != nil:
		body = clip(fmt.Sprintf("#%d %s", n.TicketNumber, subject), 120)
	case subject != "":
		body = clip(subject, 120) // a Collab conversation's subject
	}
	return describe(n).heading, body, noteURL(s, wid, n)
}

// noteURL is where a notification leads in the app, in its workspace.
func noteURL(s Site, wid uuid.UUID, n Note) string {
	ws := wid.String()
	switch n.Type {
	case "ai_cap_reached":
		return s.App("/settings/ai/behaviour", "ws", ws)
	case "ai_key_failed":
		return s.App("/settings/ai", "ws", ws)
	case "ai_balance_low", "ai_balance_out", "auto_recharge_failed":
		return s.App("/settings/billing", "ws", ws)
	case "team_joined":
		if id := teamID(n.Data); id != "" {
			return s.App("/settings/teams/"+url.PathEscape(id), "ws", ws)
		}
		return s.App("/settings/teams", "ws", ws)
	case "collab_assigned", "collab_mention":
		box := "all"
		if n.Type == "collab_assigned" {
			box = "assigned"
		}
		if id, err := uuid.Parse(str(n.Data, "thread_id")); err == nil {
			return s.App("/mail/"+box+"/"+id.String(), "ws", ws)
		}
		return s.App("/mail/"+box, "ws", ws)
	case "import_finished":
		id := ""
		if u, err := uuid.Parse(str(n.Data, "import_id")); err == nil {
			id = u.String()
		}
		return s.App("/settings/import", "ws", ws, "id", id)
	}
	if n.TicketID != nil {
		folder := "mine"
		if n.Type == "new_ticket" {
			folder = "unassigned" // nobody's yet: it is not in Mine
		}
		return s.App("/inbox/"+folder+"/"+n.TicketID.String(), "ws", ws)
	}
	return s.App("/", "ws", ws)
}

func toneOf(n Note) string {
	switch n.Type {
	case "assigned":
		return "#3B82F6"
	case "mentioned":
		return "#6366F1"
	case "customer_replied":
		return "#14B8A6"
	case "new_ticket":
		return "#22C55E"
	case "sla_breached", "ai_balance_out", "auto_recharge_failed", "ai_key_failed":
		return "#EF4444"
	case "ai_handoff", "ai_cap_reached":
		return "#8B5CF6"
	case "ai_balance_low":
		return "#F59E0B"
	case "team_joined", "thread_replied":
		return "#06B6D4"
	case "collab_assigned":
		return "#3B82F6"
	case "collab_mention":
		return "#6366F1"
	case "import_finished":
		switch str(n.Data, "status") {
		case "completed":
			return "#22C55E"
		case "needs_credentials":
			return "#F59E0B"
		}
		return "#EF4444"
	}
	return "#8A8A8A"
}

// collabCopy names who assigned or mentioned the member and the conversation's subject
// (contracts/collab.md "Notifications").
func collabCopy(n Note, actor string) copyOf {
	who, subject := or(actor, "A teammate"), clip(str(n.Data, "subject"), 80)
	on := ""
	if subject != "" {
		on = ": " + subject
	}
	if n.Type == "collab_assigned" {
		return copyOf{title: who + " assigned you a conversation", heading: who + " assigned you a conversation" + on,
			intro: []string{"It now shows in your mail under Assigned to me."}, cta: "Open conversation"}
	}
	intro := []string{who + " mentioned you in a comment on a conversation."}
	if sn := snippet(n); sn != "" {
		intro = append(intro, "“"+sn+"”")
	}
	return copyOf{title: who + " mentioned you in a comment", heading: who + " mentioned you" + on, intro: intro, cta: "Open conversation"}
}

// importCopy says how a history import ended (contracts/imports.md): done, waiting for new
// credentials, or stopped.
func importCopy(d map[string]any) copyOf {
	src := or(clip(str(d, "source_name"), 40), "history")
	from := or(clip(str(d, "account"), 60), or(clip(str(d, "source_name"), 40), "the old helpdesk"))
	count := func(k string) int {
		v, _ := num(d, k)
		return int(v)
	}
	tickets, updated, articles, failed := count("tickets"), count("updated"), count("articles"), count("failed")
	var facts []mailer.Fact
	add := func(label string, v int) {
		if v > 0 {
			facts = append(facts, mailer.Fact{Label: label, Value: fmt.Sprint(v)})
		}
	}
	add("Tickets", tickets)
	add("Updated", updated)
	add("Articles", articles)
	add("Could not import", failed)
	reason := clip(str(d, "error"), 300)
	switch str(d, "status") {
	case "completed":
		var came []string
		if tickets > 0 {
			came = append(came, plural(tickets, "ticket", "tickets"))
		}
		if articles > 0 {
			came = append(came, plural(articles, "article", "articles"))
		}
		intro := []string{"Nothing new came over from " + from + "."}
		if len(came) > 0 {
			intro = []string{strings.Join(came, " and ") + " came over from " + from + "."}
		}
		if updated > 0 {
			intro = append(intro, plural(updated, "ticket that was already here was", "tickets that were already here were")+" brought up to date.")
		}
		if failed > 0 {
			intro = append(intro, plural(failed, "item", "items")+" could not be imported. The import page lists them.")
		}
		return copyOf{title: "Your " + src + " import is done", heading: "Your " + src + " import is done", intro: intro, facts: facts, cta: "Open import", subjectTail: true}
	case "needs_credentials":
		return copyOf{title: "Your " + src + " import needs new credentials", heading: "Your " + src + " import needs new credentials",
			intro: []string{or(reason, src+" refused the credentials. Enter new ones to go on where it stopped."),
				"Everything that came over so far stays in opendesk."},
			facts: facts, cta: "Enter new credentials", subjectTail: true}
	}
	return copyOf{title: "Your " + src + " import stopped", heading: "Your " + src + " import stopped",
		intro: []string{or(reason, "Something went wrong. Resume the import to try again."),
			"Everything that came over so far stays in opendesk, and resuming goes on where it stopped."},
		facts: facts, cta: "Open import", subjectTail: true}
}

// plural is "1 ticket" or "3 tickets".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func rechargeReason(code string) string {
	switch code {
	case "card_declined":
		return "The card on file was declined."
	case "authentication_required":
		return "The bank asked to confirm the payment, which cannot happen while nobody is at the checkout."
	case "no_payment_method":
		return "There is no saved payment method."
	case "unavailable":
		return "AI top-ups are not available right now."
	}
	return "The payment did not go through."
}

func providerName(p string) string {
	switch p {
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "openrouter":
		return "OpenRouter"
	}
	return "AI provider"
}

// snippet is the notification's short plain-text excerpt, when it has one.
func snippet(n Note) string {
	switch n.Type {
	case "mentioned", "customer_replied", "collab_mention", "thread_replied":
		return clip(str(n.Data, "preview"), SnippetRunes)
	}
	return ""
}

func oneTicket(notes []Note) *uuid.UUID {
	var t *uuid.UUID
	for _, n := range notes {
		if n.TicketID == nil || (t != nil && *t != *n.TicketID) {
			return nil
		}
		t = n.TicketID
	}
	return t
}

func mailDomain(s Site) string {
	if u, err := url.Parse(s.AppURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "opendesk.local"
}

// when is a time in a list line: the time of day today, the weekday and time this week,
// else the date. clock "12h" reads 2:05 PM; anything else 14:05.
func when(t, now time.Time, loc *time.Location, clock string) string {
	t, now = t.In(loc), now.In(loc)
	hm := "15:04"
	if clock == "12h" {
		hm = "3:04 PM"
	}
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format(hm)
	}
	if now.Sub(t) < 6*24*time.Hour {
		return t.Format("Mon " + hm)
	}
	return t.Format("Jan 2")
}

// clip flattens whitespace and cuts s to n runes, on a word when it can.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	out := string([]rune(s)[:n])
	if i := strings.LastIndex(out, " "); i > n/2 {
		out = out[:i]
	}
	return strings.TrimRight(out, " ,.;:…") + "…"
}

func str(d map[string]any, k string) string {
	v, _ := d[k].(string)
	return v
}

func num(d map[string]any, k string) (float64, bool) {
	switch v := d[k].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func nameIn(d map[string]any, k string) string {
	if m, ok := d[k].(map[string]any); ok {
		if s, ok := m["name"].(string); ok {
			return clip(s, 60)
		}
	}
	return ""
}

func teamField(d map[string]any, k string) any {
	if m, ok := d["team"].(map[string]any); ok {
		return m[k]
	}
	return nil
}

func teamName(d map[string]any) string {
	s, _ := teamField(d, "name").(string)
	return clip(s, 60)
}

func teamID(d map[string]any) string {
	s, _ := teamField(d, "id").(string)
	if s == "" {
		s = str(d, "team_id")
	}
	if id, err := uuid.Parse(s); err == nil {
		return id.String()
	}
	return ""
}

func usd(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

func or(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func firstOr(list []string, fallback string) string {
	if len(list) > 0 {
		return list[0]
	}
	return fallback
}

// SampleNotes are made-up notifications for the test email and the previews.
func SampleNotes(now time.Time) []Note {
	t1, t2 := uuid.MustParse("0192f0c4-0000-7000-8000-000000001095"), uuid.MustParse("0192f0c4-0000-7000-8000-000000001102")
	return []Note{
		{ID: uuid.New(), Type: "customer_replied", TicketID: &t2, TicketNumber: 1102, TicketSubject: "Export to CSV skips archived tickets",
			Data:      map[string]any{"contact": map[string]any{"name": "Dana Lee"}, "preview": "Thanks, that worked for the open ones. The archived tickets from March are still missing from the file though."},
			CreatedAt: now.Add(-2 * time.Minute)},
		{ID: uuid.New(), Type: "mentioned", TicketID: &t1, TicketNumber: 1095, TicketSubject: "SAML with Okta",
			Data:      map[string]any{"actor": map[string]any{"name": "Maya Chen"}, "preview": "Can you check the Okta metadata URL? The customer says the ACS URL changed after the domain move."},
			CreatedAt: now.Add(-6 * time.Minute)},
		{ID: uuid.New(), Type: "assigned", TicketID: &t1, TicketNumber: 1095, TicketSubject: "SAML with Okta",
			Data: map[string]any{"actor": map[string]any{"name": "Maya Chen"}}, CreatedAt: now.Add(-9 * time.Minute)},
	}
}
