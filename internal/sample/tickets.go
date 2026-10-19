package sample

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/ids"
)

// The tickets step plans every ticket and chat first (content, people, timing, status),
// then writes them oldest first so ticket numbers follow time. A recent open ticket is
// written live: domain.CreateTicket and domain.AddMessage publish their events, with
// automations off (Trigger "none") and email replies stored as sent. The rest is history,
// written through the same functions with Imported set: dated, announced to no one, and
// never sent; its status, CSAT and timeline are settled afterwards, as an import does.

// pmsg is one planned message.
type pmsg struct {
	note    bool
	inbound bool
	ai      bool
	user    *member
	html    string // unsanitized; written content only
	at      time.Time
}

// plan is one planned ticket.
type plan struct {
	kind        Kind
	channel     string
	status      string
	priority    string
	severity    *string
	subject     string
	contact     *person
	spamFrom    *spamDef
	assignee    *member
	team        *team
	created     time.Time
	msgs        []pmsg
	labels      []string
	csat        int
	csatComment string
	solvedAt    *time.Time
	closedAt    *time.Time
	pageURL     string
	live        bool
	// session: a live chat a teammate answers; its visitor is on the page.
	session bool
}

func openish(status string) bool {
	return status == domain.StatusOpen || status == domain.StatusPending || status == domain.StatusOnHold
}

// seedTickets plans and writes the tickets and chats of the chosen kinds.
func (g *gen) seedTickets(ctx context.Context) error {
	var plans []*plan
	if g.kinds[KindTickets] || g.kinds[KindHistory] {
		plans = append(plans, g.planTickets()...)
	}
	if g.kinds[KindChat] {
		plans = append(plans, g.planChats()...)
	}
	plans = slices.DeleteFunc(plans, func(p *plan) bool { return !g.kinds[p.kind] })
	// The most recent open tickets are live, up to the size's cap; waiting and ongoing
	// chats always are.
	slices.SortStableFunc(plans, func(a, b *plan) int { return b.created.Compare(a.created) })
	live := 0
	for _, p := range plans {
		if p.live {
			continue
		}
		if p.channel != domain.ChannelChat && openish(p.status) && p.spamFrom == nil && g.now.Sub(p.created) < 72*time.Hour && live < g.vol.liveCap {
			p.live = true
			live++
		}
	}
	slices.SortStableFunc(plans, func(a, b *plan) int { return a.created.Compare(b.created) })
	return g.writePlans(ctx, plans)
}

// statusFor is a ticket's status by its age: the older, the likelier it is done.
func (g *gen) statusFor(age time.Duration) string {
	var w []float64 // open, pending, on_hold, solved, closed
	switch {
	case age < day:
		w = []float64{0.55, 0.22, 0.05, 0.18, 0}
	case age < 3*day:
		w = []float64{0.3, 0.25, 0.06, 0.39, 0}
	case age < 14*day:
		w = []float64{0.06, 0.09, 0.04, 0.71, 0.1}
	default:
		w = []float64{0.004, 0.008, 0.006, 0.38, 0.602}
	}
	return domain.Statuses[g.weighted(w...)]
}

// next is a moment after prev by delay, kept before now; ok is false when it would not be.
func (g *gen) next(prev time.Time, delay time.Duration) (time.Time, bool) {
	t := prev.Add(delay)
	limit := g.now.Add(-time.Minute)
	if t.After(limit) {
		if prev.Add(time.Minute).After(limit) {
			return prev, false
		}
		t = prev.Add(limit.Sub(prev) / 2)
	}
	return t, true
}

func (g *gen) priority() string {
	return domain.Priorities[g.weighted(0.15, 0.55, 0.22, 0.08)]
}

// severityFor is the severity of a problem by its priority; questions mostly have none.
func (g *gen) severityFor(priority, typeLabel string) *string {
	if typeLabel != labelBug && typeLabel != labelProblem {
		if g.chance(0.15) {
			s := "sev4"
			return &s
		}
		return nil
	}
	var s string
	switch priority {
	case "urgent":
		s = pick(g, []string{"sev1", "sev1", "sev2"})
	case "high":
		s = pick(g, []string{"sev2", "sev3"})
	case "normal":
		s = pick(g, []string{"sev3", "sev3", "sev4"})
	default:
		s = "sev4"
	}
	return &s
}

// planTickets plans the email, API and portal tickets: the stories, then generated ones
// across the window (a few of them from the last hours), then a little spam.
func (g *gen) planTickets() []*plan {
	n := g.vol.tickets
	out := make([]*plan, 0, n+len(spam))
	ns := min(len(stories), n/3)
	// A few written conversations happen today; the rest moved back up to two weeks, each
	// keeping its own timing, so today looks busy without dwarfing every other day.
	for i, s := range stories[:ns] {
		var shift time.Duration
		if i >= storiesToday {
			shift = time.Duration(1+g.rng.IntN(min(14, g.vol.days))) * day
		}
		out = append(out, g.planStory(s, shift))
	}
	fresh := min(12, max(3, n/40))
	window := time.Duration(g.vol.days) * day
	for i := range n - ns {
		var created time.Time
		switch {
		case i < fresh/3:
			created = g.when(g.ago(6*time.Hour), g.ago(10*time.Minute))
		case i < fresh:
			created = g.when(g.ago(3*day), g.ago(6*time.Hour))
		default:
			created = g.when(g.ago(window), g.ago(20*time.Minute))
		}
		out = append(out, g.planGenerated(created))
	}
	if g.vol.extras {
		for i := range spam {
			s := &spam[i]
			created := g.when(g.ago(10*day), g.ago(time.Hour))
			out = append(out, &plan{kind: KindHistory, channel: domain.ChannelEmail, status: domain.StatusOpen, priority: "low",
				subject: s.subject, spamFrom: s, created: created, msgs: []pmsg{{inbound: true, html: textHTML(s.body), at: created}}})
		}
	}
	return out
}

func (g *gen) kindOf(status string) Kind {
	if openish(status) {
		return KindTickets
	}
	return KindHistory
}

// customer picks a contact, mostly one at a company when company is set.
func (g *gen) customer(company bool) *person {
	for range 8 {
		c := pick(g, g.contacts)
		if !company || c.company != nil {
			return c
		}
	}
	return pick(g, g.contacts)
}

func (g *gen) vars(c *person, agent *member, other *member) map[string]string {
	v := map[string]string{"first": c.first(), "name": c.name, "company": "our team"}
	if c.company != nil {
		v["company"] = c.company.name
	}
	if agent != nil {
		v["agent"] = agent.first()
	}
	if other != nil {
		v["mention"] = mention(other)
	}
	return v
}

// storiesToday is how many written conversations stay on today.
const storiesToday = 4

// planStory plans a written conversation, moved back by shift.
func (g *gen) planStory(s story, shift time.Duration) *plan {
	created := g.ago(time.Duration(s.thread[0].min)*time.Minute + shift)
	t := g.teamFor(s.team)
	if s.team == "" {
		t = nil
	}
	p := &plan{kind: g.kindOf(s.status), channel: s.channel, status: s.status, priority: s.priority, subject: s.subject,
		contact: g.customer(true), team: t, created: created, labels: s.labels, csat: s.csat, csatComment: s.comment}
	if s.severity != "" {
		sev := s.severity
		p.severity = &sev
	}
	writer := g.assigneeIn(t)
	if s.assigned {
		p.assignee = writer
	}
	other := g.otherThan(writer)
	v := g.vars(p.contact, writer, other)
	for _, m := range s.thread {
		at := g.ago(time.Duration(m.min)*time.Minute + shift)
		pm := pmsg{note: m.note, html: g.fill(m.html, true, v), at: at}
		switch m.from {
		case "c":
			pm.inbound = true
		case "b":
			pm.user = other
		default:
			pm.user = writer
		}
		p.msgs = append(p.msgs, pm)
	}
	if !openish(s.status) {
		last := p.msgs[len(p.msgs)-1].at
		solved, _ := g.next(last, g.minutes(20, 0.6, 2, 240))
		p.solvedAt = &solved
	}
	if g.vips[p.contact.id] {
		p.labels = append(slices.Clone(p.labels), labelVIP)
	}
	return p
}

func (g *gen) planGenerated(created time.Time) *plan {
	gt := pick(g, generatedTickets)
	status := g.statusFor(g.now.Sub(created))
	c := g.customer(false)
	p := &plan{kind: g.kindOf(status), status: status, priority: g.priority(), subject: g.fill(gt.subject, false, nil),
		contact: c, created: created}
	p.channel = []string{domain.ChannelEmail, domain.ChannelAPI, domain.ChannelPortal}[g.weighted(0.85, 0.09, 0.06)]

	typeLabel := labelQuestion
	teamKey := "support"
	for _, tag := range gt.tags {
		switch tag {
		case "bug":
			typeLabel = labelBug
		case "feature-request":
			if typeLabel != labelBug {
				typeLabel = labelFeature
			}
		case "billing", "refund":
			if typeLabel == labelQuestion {
				typeLabel = labelBilling
			}
			teamKey = "billing"
		case "sso", "api":
			if teamKey == "support" {
				teamKey = "technical"
			}
		case "onboarding":
			if teamKey == "support" {
				teamKey = "onboarding"
			}
		}
		if l, ok := topicLabels[tag]; ok {
			p.labels = append(p.labels, l)
		}
	}
	if typeLabel == labelQuestion && g.chance(0.3) {
		typeLabel = labelProblem
	}
	p.labels = append(p.labels, typeLabel)
	if g.vips[c.id] {
		p.labels = append(p.labels, labelVIP)
	}
	p.severity = g.severityFor(p.priority, typeLabel)
	if c.company != nil && g.chance(0.15) {
		if t := g.teamFor("enterprise"); t != nil && t.key == "enterprise" {
			teamKey = "enterprise"
		}
	}
	if !g.chance(0.1) {
		p.team = g.teamFor(teamKey)
	}
	if status != domain.StatusOpen || !g.chance(0.4) {
		p.assignee = g.assigneeIn(p.team)
	}
	writer := p.assignee
	if writer == nil {
		writer = g.assigneeIn(p.team)
	}
	v := g.vars(c, writer, g.otherThan(writer))

	at := created
	p.msgs = append(p.msgs, pmsg{inbound: true, at: at, html: textHTML(g.fill("Hi,\n\n"+gt.body+"\n\nThanks,\n{first}", false, v))})
	if g.chance(0.25) {
		if t, ok := g.next(at, g.minutes(15, 0.8, 1, 120)); ok {
			p.msgs = append(p.msgs, pmsg{note: true, user: writer, at: t, html: g.fill(pick(g, notes), true, v)})
			at = t
		}
	}
	reply := func(text string) bool {
		t, ok := g.next(at, g.minutes(55, 1.0, 3, 2880))
		if !ok {
			return false
		}
		at = t
		p.msgs = append(p.msgs, pmsg{user: writer, at: t, html: textHTML(g.fill("Hi {first},\n\n"+text+"\n\n{agent}", false, v))})
		return true
	}
	customer := func(text string, median float64) bool {
		t, ok := g.next(at, g.minutes(median, 1.0, 5, 4320))
		if !ok {
			return false
		}
		at = t
		p.msgs = append(p.msgs, pmsg{inbound: true, at: t, html: textHTML(text)})
		return true
	}
	switch status {
	case domain.StatusOpen:
		if g.chance(0.5) && reply(pick(g, agentReplies)) {
			customer(pick(g, followUps), 180)
		}
	case domain.StatusPending:
		reply(pick(g, agentReplies))
		if g.chance(0.3) && customer(pick(g, followUps), 240) {
			reply(pick(g, secondReplies))
		}
	case domain.StatusOnHold:
		if reply(pick(g, agentReplies)) {
			if t, ok := g.next(at, g.minutes(10, 0.5, 1, 60)); ok {
				p.msgs = append(p.msgs, pmsg{note: true, user: writer, at: t, html: onHoldNote})
				at = t
			}
		}
	default:
		reply(pick(g, agentReplies))
		if g.chance(0.25) && customer(pick(g, followUps), 300) {
			reply(pick(g, secondReplies))
		}
		if g.chance(0.6) {
			customer(pick(g, thanks), 120)
		}
		solved, _ := g.next(at, g.minutes(30, 0.8, 2, 1440))
		p.solvedAt = &solved
		if status == domain.StatusClosed {
			closed := solved.Add(4 * day)
			if closed.Before(g.now) {
				p.closedAt = &closed
			} else {
				p.status = domain.StatusSolved
			}
		}
		if g.chance(0.45) {
			p.csat = pick(g, csatScores)
			if p.csat >= 4 {
				p.csatComment = pick(g, csatComments)
			} else {
				p.csatComment = pick(g, csatComplaints)
			}
		}
	}
	return p
}

// planChats plans the widget conversations: some waiting for a teammate right now, some
// going on, the rest finished across the window.
func (g *gen) planChats() []*plan {
	n := g.vol.chats
	waiting, active := 2, 1
	switch g.o.Size {
	case SizeRealistic:
		waiting, active = 4, 4
	case SizeLarge:
		waiting, active = 6, 8
	}
	waiting = min(waiting, n)
	active = min(active, n-waiting)
	out := make([]*plan, 0, n)
	team := g.teamFor("support")
	if g.widgetT != nil {
		for _, t := range g.teams {
			if t.id == *g.widgetT {
				team = t
			}
		}
	}
	window := time.Duration(g.vol.days) * day
	for i := range n {
		var script chatScript
		var created time.Time
		switch {
		case i < waiting:
			script = waitingOpeners[i%len(waitingOpeners)]
			created = g.ago(time.Duration(g.between(2, 25)) * time.Minute)
		case i < waiting+active:
			script = chatScripts[(i-waiting)%len(chatScripts)]
			created = g.ago(time.Duration(g.between(15, 180)) * time.Minute)
		default:
			script = pick(g, chatScripts)
			created = g.when(g.ago(window), g.ago(4*time.Hour))
		}
		c := g.customer(false)
		p := &plan{kind: KindChat, channel: domain.ChannelChat, status: domain.StatusOpen, priority: "normal", contact: c,
			team: team, created: created, subject: g.fill(script.subject, false, nil), pageURL: "https://" + g.site + script.page,
			live: i < waiting+active}
		if g.chance(0.12) {
			p.priority = "high"
		}
		human := slices.ContainsFunc(script.lines, func(l chatLine) bool { return l.who == "a" })
		writer := g.assigneeIn(team)
		if i >= waiting && human {
			p.assignee = writer
		}
		v := g.vars(c, writer, nil)
		at := created
		for k, l := range script.lines {
			if k > 0 {
				t, ok := g.next(at, g.minutes(1.5, 0.6, 1, 8))
				if !ok {
					break
				}
				at = t
			}
			pm := pmsg{at: at, html: hd.TextToHTML(g.fill(l.text, false, v))}
			switch l.who {
			case "v":
				pm.inbound = true
			case "o":
				pm.ai = true
			default:
				pm.user = writer
				p.session = p.live
			}
			p.msgs = append(p.msgs, pm)
		}
		switch {
		case i < waiting:
		case i < waiting+active:
			if !p.msgs[len(p.msgs)-1].inbound && g.chance(0.4) {
				p.status = domain.StatusPending
			}
		default:
			p.status = domain.StatusSolved
			solved, _ := g.next(at, g.minutes(8, 0.6, 1, 120))
			p.solvedAt = &solved
			if closed := solved.Add(4 * day); closed.Before(g.now) && g.chance(0.7) {
				p.status, p.closedAt = domain.StatusClosed, &closed
			}
			if g.chance(0.3) {
				p.csat = pick(g, csatScores)
				if p.csat >= 4 {
					p.csatComment = pick(g, csatComments)
				} else {
					p.csatComment = pick(g, csatComplaints)
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// activity is a timeline entry of imported history, written in bulk without events.
type activity struct {
	ticket    uuid.UUID
	actorType string
	actor     *uuid.UUID
	typ       string
	data      map[string]any
	at        time.Time
}

// writePlans writes the plans, a chunk per transaction.
func (g *gen) writePlans(ctx context.Context, plans []*plan) error {
	const chunk = 40
	for start := 0; start < len(plans); start += chunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		part := plans[start:min(start+chunk, len(plans))]
		before := g.counts
		err := g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			var acts []activity
			for _, p := range part {
				if err := g.writePlan(ctx, tx, p, &acts); err != nil {
					return err
				}
			}
			return insertActivities(ctx, tx, g.wid, acts)
		})
		if err != nil {
			g.counts = before
			return err
		}
	}
	return nil
}

func (g *gen) writePlan(ctx context.Context, tx pgx.Tx, p *plan, acts *[]activity) error {
	wid := g.wid
	contactID, contactEmail, contactName := uuid.Nil, "", ""
	if p.spamFrom != nil {
		id, _, err := domain.ImportContactByEmail(ctx, tx, wid, p.spamFrom.from, p.spamFrom.name, "")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE contacts SET avatar_retry_at = NULL, avatar_checked_at = now()
			WHERE workspace_id = $1 AND id = $2 AND avatar_source IS NULL`, wid, id); err != nil {
			return err
		}
		contactID, contactEmail, contactName = id, p.spamFrom.from, p.spamFrom.name
	} else {
		contactID, contactEmail, contactName = p.contact.id, p.contact.email, p.contact.name
	}
	nt := domain.NewTicket{WorkspaceID: wid, Subject: p.subject, Channel: p.channel, Status: p.status, Priority: p.priority,
		Severity: p.severity, ContactID: &contactID, PageURL: p.pageURL, Imported: !p.live, CreatedAt: p.created}
	if p.live {
		nt.Status = domain.StatusOpen // the messages arrive as they did; the status is set after them
	}
	if p.assignee != nil {
		nt.AssigneeID = &p.assignee.id
	}
	var teamID *uuid.UUID
	if p.team != nil {
		teamID = &p.team.id
		nt.TeamID = teamID
	}
	if p.channel == domain.ChannelChat {
		nt.WidgetID = g.widget
		if teamID != nil && g.widgetT != nil && *teamID == *g.widgetT {
			nt.Routed = &domain.TicketRouted{Via: "widget"}
		}
	}
	t, err := domain.CreateTicket(ctx, tx, nt)
	if err != nil {
		return err
	}
	if p.live {
		// CreateTicket dates a live ticket now; it began with its first message.
		if _, err := tx.Exec(ctx, `UPDATE tickets SET created_at = $3, updated_at = $3, last_message_at = $3 WHERE workspace_id = $1 AND id = $2`,
			wid, t.ID, p.created); err != nil {
			return err
		}
	}
	var tagIDs []uuid.UUID
	for _, name := range p.labels {
		if id, ok := g.labels[name]; ok && !slices.Contains(tagIDs, id) {
			tagIDs = append(tagIDs, id)
		}
	}
	if len(tagIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_tags (ticket_id, tag_id) SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, t.ID, tagIDs); err != nil {
			return err
		}
	}
	var policy *slaPolicy
	var frDue, resDue time.Time
	if p.spamFrom == nil {
		if policy = g.policyFor(p.priority, teamID); policy != nil {
			frDue = p.created.Add(time.Duration(policy.fr) * time.Minute)
			resDue = p.created.Add(time.Duration(policy.res) * time.Minute)
			if _, err := tx.Exec(ctx, `UPDATE tickets SET sla_policy_id = $3, first_response_due_at = $4, resolution_due_at = $5
				WHERE workspace_id = $1 AND id = $2`, wid, t.ID, policy.id, frDue, resDue); err != nil {
				return err
			}
		}
	}
	if p.assignee != nil {
		data := map[string]any{"from": nil, "to": p.assignee.id.String()}
		if p.live {
			if _, err := domain.LogActivity(ctx, tx, wid, t.ID, "system", nil, "assigned", data); err != nil {
				return err
			}
		} else {
			*acts = append(*acts, activity{t.ID, "system", nil, "assigned", data, p.created.Add(time.Duration(g.between(1, 5)) * time.Minute)})
		}
	}
	if p.session {
		// The visitor is on the page, as a widget session says (domain.ChatFollowUpDue).
		if _, err := tx.Exec(ctx, `INSERT INTO widget_sessions (id, workspace_id, contact_id, token_hash, user_agent, widget_id, created_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now())`, ids.New(), wid, contactID, crypto.HashToken(crypto.RandomToken(32)),
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_6) AppleWebKit/605.1.15 Safari/605.1.15", g.widget, p.created); err != nil {
			return err
		}
	}

	var firstResponse *time.Time
	for i, m := range p.msgs {
		nm := domain.NewMessage{WorkspaceID: wid, TicketID: t.ID, Kind: "reply", CreatedAt: m.at, Imported: !p.live, Trigger: "none"}
		if p.channel == domain.ChannelEmail {
			nm.Subject = p.subject
			if i > 0 {
				nm.Subject = "Re: " + p.subject
			}
		}
		var body string
		switch {
		case m.inbound:
			nm.Direction, nm.AuthorType, nm.AuthorContactID = "inbound", "contact", &contactID
			nm.FromEmail, nm.FromName = contactEmail, contactName
			if p.channel == domain.ChannelEmail {
				body = hd.SanitizeEmailHTML(m.html)
			} else {
				body = hd.SanitizeRich(m.html)
			}
		case m.ai:
			nm.Direction, nm.AuthorType, nm.AI, nm.FromName = "outbound", "system", true, "Ollie"
			body = hd.SanitizeRich(m.html)
		default:
			nm.Direction, nm.AuthorType, nm.AuthorUserID = "outbound", "user", &m.user.id
			nm.FromName, nm.FromEmail = m.user.name, m.user.email
			if m.note {
				nm.Kind = "note"
			} else {
				if firstResponse == nil {
					at := m.at
					firstResponse = &at
				}
				if p.channel == domain.ChannelEmail {
					nm.To = []string{contactEmail}
					nm.DeliveryStatus = "sent" // stored as sent: nothing is queued
				}
			}
			body = hd.SanitizeRich(m.html)
		}
		nm.BodyHTML, nm.BodyText = body, hd.HTMLToText(body)
		if _, err := domain.AddMessage(ctx, tx, g.d.Jobs, nm); err != nil {
			return err
		}
		g.counts.Messages++
	}

	// SLA breaches, as the scan would have recorded them.
	var frBreach, resBreach *time.Time
	if policy != nil {
		switch {
		case firstResponse != nil && firstResponse.After(frDue):
			frBreach = &frDue
		case firstResponse == nil && openish(p.status) && frDue.Before(g.now):
			frBreach = &frDue
		}
		switch {
		case p.solvedAt != nil && p.solvedAt.After(resDue):
			resBreach = &resDue
		case p.solvedAt == nil && openish(p.status) && resDue.Before(g.now):
			resBreach = &resDue
		}
	}
	var actor *uuid.UUID
	actorType := "system"
	if p.assignee != nil {
		actor, actorType = &p.assignee.id, "user"
	}
	if p.live {
		changes := []string{}
		if p.status != domain.StatusOpen {
			if _, err := tx.Exec(ctx, `UPDATE tickets SET status = $3 WHERE workspace_id = $1 AND id = $2`, wid, t.ID, p.status); err != nil {
				return err
			}
			if _, err := domain.LogActivity(ctx, tx, wid, t.ID, actorType, actor, "status_changed",
				map[string]any{"from": domain.StatusOpen, "to": p.status}); err != nil {
				return err
			}
			changes = append(changes, "status")
		}
		if frBreach != nil || resBreach != nil {
			if _, err := tx.Exec(ctx, `UPDATE tickets SET sla_first_response_breached_at = $3, sla_resolution_breached_at = $4
				WHERE workspace_id = $1 AND id = $2`, wid, t.ID, frBreach, resBreach); err != nil {
				return err
			}
			for _, b := range []struct {
				at   *time.Time
				kind string
			}{{frBreach, "first_response"}, {resBreach, "resolution"}} {
				if b.at != nil {
					if _, err := domain.LogActivity(ctx, tx, wid, t.ID, "system", nil, "sla_breached", map[string]any{"kind": b.kind}); err != nil {
						return err
					}
				}
			}
			changes = append(changes, "sla")
		}
		if len(changes) > 0 {
			if err := domain.PublishTicketUpdated(ctx, tx, wid, t.ID, changes); err != nil {
				return err
			}
		}
	} else {
		var csat *int
		var comment string
		if p.csat > 0 && !openish(p.status) {
			c := p.csat
			csat, comment = &c, p.csatComment
		}
		last := p.created
		for _, ts := range []*time.Time{p.solvedAt, p.closedAt} {
			if ts != nil && ts.After(last) {
				last = *ts
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE tickets SET status = $3,
			solved_at = CASE WHEN $3 IN ('solved', 'closed') THEN $4::timestamptz END,
			closed_at = CASE WHEN $3 = 'closed' THEN $5::timestamptz END,
			waiting_since = CASE WHEN $3 IN ('solved', 'closed') THEN NULL ELSE waiting_since END,
			unanswered_count = CASE WHEN $3 IN ('solved', 'closed') THEN 0 ELSE unanswered_count END,
			csat_score = $6, csat_comment = $7, csat_requested_at = CASE WHEN $6::smallint IS NOT NULL THEN $4::timestamptz END,
			spam = $8, sla_first_response_breached_at = $9, sla_resolution_breached_at = $10,
			updated_at = GREATEST(updated_at, $11)
			WHERE workspace_id = $1 AND id = $2`, wid, t.ID, p.status, p.solvedAt, p.closedAt, csat, comment, p.spamFrom != nil,
			frBreach, resBreach, last); err != nil {
			return fmt.Errorf("settle ticket: %w", err)
		}
		if p.solvedAt != nil {
			*acts = append(*acts, activity{t.ID, actorType, actor, "status_changed", map[string]any{"from": domain.StatusOpen, "to": domain.StatusSolved}, *p.solvedAt})
		}
		if p.closedAt != nil {
			*acts = append(*acts, activity{t.ID, "system", nil, "status_changed", map[string]any{"from": domain.StatusSolved, "to": domain.StatusClosed}, *p.closedAt})
		}
		if frBreach != nil {
			*acts = append(*acts, activity{t.ID, "system", nil, "sla_breached", map[string]any{"kind": "first_response"}, *frBreach})
		}
		if resBreach != nil {
			*acts = append(*acts, activity{t.ID, "system", nil, "sla_breached", map[string]any{"kind": "resolution"}, *resBreach})
		}
	}
	if p.channel == domain.ChannelChat {
		g.counts.Chats++
	} else {
		g.counts.Tickets++
	}
	return nil
}

// insertActivities writes imported history's timeline entries in one statement.
func insertActivities(ctx context.Context, tx pgx.Tx, wid uuid.UUID, acts []activity) error {
	if len(acts) == 0 {
		return nil
	}
	n := len(acts)
	idList, tickets, types, actorTypes, data := make([]uuid.UUID, n), make([]uuid.UUID, n), make([]string, n), make([]string, n), make([]string, n)
	actors, at := make([]*uuid.UUID, n), make([]time.Time, n)
	for i, a := range acts {
		b, err := json.Marshal(a.data)
		if err != nil {
			return err
		}
		idList[i], tickets[i], types[i], actorTypes[i], data[i], actors[i], at[i] = ids.New(), a.ticket, a.typ, a.actorType, string(b), a.actor, a.at
	}
	_, err := tx.Exec(ctx, `INSERT INTO activities (id, workspace_id, ticket_id, actor_type, actor_user_id, type, data, created_at)
		SELECT u.id, $1, u.ticket_id, u.actor_type, u.actor_user_id, u.type, u.data::jsonb, u.created_at
		FROM unnest($2::uuid[], $3::uuid[], $4::text[], $5::uuid[], $6::text[], $7::text[], $8::timestamptz[])
			AS u(id, ticket_id, actor_type, actor_user_id, type, data, created_at)`,
		wid, idList, tickets, actorTypes, actors, types, data, at)
	if err != nil {
		return fmt.Errorf("add timeline: %w", err)
	}
	return nil
}
