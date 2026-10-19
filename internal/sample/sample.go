// Package sample fills a workspace with realistic demo data: teammates and teams,
// customers and companies, tickets and chats with believable conversations spread over
// weeks or months, a knowledge base, saved replies and views, and Collab mail. Instance
// admins use it for test workspaces (contracts/admin.md "Test workspaces").
//
// Nothing it writes reaches anyone. Generated people have addresses on the reserved
// .invalid top-level domain; generated teammates have no password; outbound replies are
// stored as already sent, nothing is queued to send, no automation, auto-reply, CSAT
// email, chat follow-up or model call runs; Collab accounts are demo accounts that never
// connect to a provider (internal/collab demo.go).
//
// Recent open tickets are written like live ones, through domain.CreateTicket and
// domain.AddMessage with their events. Older history goes through the imported-history
// path of those same functions (NewTicket.Imported, NewMessage.Imported;
// contracts/imports.md), which publishes no per-record events, like an import. Other
// bulk records (contacts, companies, teammates, articles) are not announced one by one
// either: lists show them when they load.
package sample

import (
	"context"
	"fmt"
	"html"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
)

// Size is how much data one batch adds.
type Size string

// Kind is a part of the data.
type Kind string

const (
	SizeSmall     Size = "small"
	SizeRealistic Size = "realistic"
	SizeLarge     Size = "large"

	// KindTickets: open, pending and on-hold email, API and portal tickets, with the
	// customers, companies, labels, contact fields, views and saved replies they need.
	KindTickets Kind = "tickets"
	// KindChat: widget conversations, some waiting for a teammate right now.
	KindChat Kind = "chat"
	// KindKB: knowledge base collections and articles.
	KindKB Kind = "kb"
	// KindTeam: teammates (users without a password) and teams.
	KindTeam Kind = "team"
	// KindCollab: demo Collab mail accounts with conversations.
	KindCollab Kind = "collab"
	// KindHistory: solved and closed tickets across the whole window, for reports.
	KindHistory Kind = "history"
)

// Sizes and Kinds list the valid values.
var (
	Sizes = []Size{SizeSmall, SizeRealistic, SizeLarge}
	Kinds = []Kind{KindTickets, KindChat, KindKB, KindTeam, KindCollab, KindHistory}
)

type Options struct {
	Size  Size
	Kinds []Kind // empty = all
	// Seed makes the content deterministic: the same seed and options give the same data
	// (ids and the moment it is relative to aside).
	Seed int64
	// OwnerID is the workspace owner; replies, assignments and Collab accounts may use them
	// besides the generated teammates.
	OwnerID uuid.UUID
}

// Counts is what one batch added. Tickets excludes chats (Chats); Messages counts both.
type Counts struct {
	Members, Teams, Contacts, Companies, Tickets, Messages, Chats, Articles, Collections,
	MailAccounts, MailThreads, Views, SavedReplies int
}

// volume is what a size adds.
type volume struct {
	members, teams, contacts, companies, tickets, chats, articles, collections, mailAccounts, mailThreads int
	days                                                                                                  int  // the window tickets spread over
	extras                                                                                                bool // views, saved replies, spam
	liveCap                                                                                               int  // most tickets written as live ones
}

var volumes = map[Size]volume{
	SizeSmall: {members: 3, teams: 2, contacts: 30, companies: 6, tickets: 40, chats: 5, articles: 6, collections: 2,
		mailAccounts: 1, mailThreads: 20, days: 30, liveCap: 20},
	SizeRealistic: {members: 8, teams: 4, contacts: 300, companies: 30, tickets: 400, chats: 40, articles: 25, collections: 5,
		mailAccounts: 2, mailThreads: 120, days: 90, extras: true, liveCap: 40},
	SizeLarge: {members: 25, teams: 8, contacts: 3000, companies: 120, tickets: 5000, chats: 300, articles: 80, collections: 8,
		mailAccounts: 4, mailThreads: 600, days: 365, extras: true, liveCap: 60},
}

// Seed adds demo data to an existing workspace (it must already exist with its default
// brand etc., as core creates it). Safe to call more than once: each call adds a new batch.
func Seed(ctx context.Context, d *deps.Deps, wid uuid.UUID, o Options) (Counts, error) {
	if o.Size == "" {
		o.Size = SizeSmall
	}
	vol, ok := volumes[o.Size]
	if !ok {
		return Counts{}, fmt.Errorf("unknown sample size %q", o.Size)
	}
	kinds := map[Kind]bool{}
	for _, k := range o.Kinds {
		if !slices.Contains(Kinds, k) {
			return Counts{}, fmt.Errorf("unknown sample kind %q", k)
		}
		kinds[k] = true
	}
	if len(kinds) == 0 {
		for _, k := range Kinds {
			kinds[k] = true
		}
	}
	g := &gen{d: d, wid: wid, o: o, vol: vol, kinds: kinds, now: time.Now().UTC(),
		rng: rand.New(rand.NewPCG(uint64(o.Seed), uint64(o.Seed)^0x9e3779b97f4a7c15))}
	if err := g.load(ctx); err != nil {
		return g.counts, err
	}
	steps := []struct {
		on bool
		fn func(context.Context) error
	}{
		{kinds[KindTeam], g.seedTeam},
		{true, g.loadRoster},
		{kinds[KindTickets] || kinds[KindChat] || kinds[KindHistory], g.seedPeople},
		{kinds[KindTickets] && vol.extras, g.seedExtras},
		{kinds[KindTickets] || kinds[KindChat] || kinds[KindHistory], g.seedTickets},
		{kinds[KindKB], g.seedKB},
		{kinds[KindCollab], g.seedCollab},
	}
	for _, s := range steps {
		if !s.on {
			continue
		}
		if err := ctx.Err(); err != nil {
			return g.counts, err
		}
		if err := s.fn(ctx); err != nil {
			return g.counts, err
		}
	}
	return g.counts, nil
}

// gen is one batch being generated.
type gen struct {
	d      *deps.Deps
	wid    uuid.UUID
	o      Options
	vol    volume
	kinds  map[Kind]bool
	now    time.Time
	rng    *rand.Rand
	counts Counts

	// The workspace.
	name    string // its name, the product the demo's customers write about
	slug    string
	site    string // <slug>.example.invalid
	tz      *time.Location
	batch   int // 1 for the first batch, then 2, 3, ...
	widget  *uuid.UUID
	widgetT *uuid.UUID // the widget's team

	// Who works here (loadRoster).
	owner   *member
	members []*member // everyone who can reply: owner, admins, agents
	teams   []*team

	// Customers (seedPeople).
	companies []*company
	contacts  []*person
	vips      map[uuid.UUID]bool
	labels    map[string]uuid.UUID
	sla       []slaPolicy
}

type member struct {
	id          uuid.UUID
	name, email string
	role        string
}

func (m *member) first() string { return strings.Fields(m.name + " x")[0] }

type team struct {
	id      uuid.UUID
	key     string // its teamDefs key when it has one
	name    string
	members []*member
}

type company struct {
	id           uuid.UUID
	name, domain string
}

type person struct {
	id          uuid.UUID
	name, email string
	company     *company
}

func (p *person) first() string { return strings.Fields(p.name + " x")[0] }

// load reads the workspace and works out the batch number.
func (g *gen) load(ctx context.Context) error {
	var tz *string
	err := g.d.DB.Pool.QueryRow(ctx, `SELECT name, slug, settings->>'timezone' FROM workspaces WHERE id = $1`, g.wid).Scan(&g.name, &g.slug, &tz)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	g.name = strings.TrimSpace(g.name)
	if g.name == "" {
		g.name = "Margin"
	}
	g.site = g.slug + ".example.invalid"
	g.tz = time.UTC
	if tz != nil {
		if loc, err := time.LoadLocation(*tz); err == nil {
			g.tz = loc
		}
	}
	// The batch is one more than the highest batch number in the generated addresses: the
	// first batch's have no suffix, later ones "+<batch>" before the @.
	err = g.d.DB.Pool.QueryRow(ctx, `SELECT COALESCE(max(COALESCE(substring(e FROM '\+([0-9]+)@')::int, 1)), 0) + 1 FROM (
		SELECT u.email::text AS e FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1 AND u.email LIKE '%.example.invalid'
		UNION ALL SELECT email::text FROM contacts WHERE workspace_id = $1 AND email LIKE '%.example.invalid'
		UNION ALL SELECT email FROM collab_accounts WHERE workspace_id = $1 AND email LIKE '%.example.invalid') x`, g.wid).Scan(&g.batch)
	if err != nil {
		return fmt.Errorf("sample batch: %w", err)
	}
	var widget, widgetTeam *uuid.UUID
	err = g.d.DB.Pool.QueryRow(ctx, `SELECT id, team_id FROM widgets WHERE workspace_id = $1 ORDER BY position, created_at LIMIT 1`, g.wid).
		Scan(&widget, &widgetTeam)
	if err != nil && !isNoRows(err) {
		return err
	}
	g.widget, g.widgetT = widget, widgetTeam
	return nil
}

// suffix is what makes a generated address unique to its batch.
func (g *gen) suffix() string {
	if g.batch <= 1 {
		return ""
	}
	return fmt.Sprintf("+%d", g.batch)
}

// Randomness: every choice goes through g.rng, so a seed gives the same content.

func (g *gen) chance(p float64) bool { return g.rng.Float64() < p }

// between is a whole number in [lo, hi].
func (g *gen) between(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + g.rng.IntN(hi-lo+1)
}

func pick[T any](g *gen, list []T) T { return list[g.rng.IntN(len(list))] }

// weighted picks an index by weight.
func (g *gen) weighted(weights ...float64) int {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	x := g.rng.Float64() * total
	for i, w := range weights {
		if x < w {
			return i
		}
		x -= w
	}
	return len(weights) - 1
}

// minutes draws a log-normal delay in minutes around median, kept within [lo, hi].
func (g *gen) minutes(median, spread float64, lo, hi int) time.Duration {
	m := math.Exp(math.Log(median) + g.rng.NormFloat64()*spread)
	m = math.Max(float64(lo), math.Min(float64(hi), m))
	return time.Duration(m * float64(time.Minute))
}

// Time: everything is relative to g.now, in the workspace's time zone.

var dayWeight = [7]float64{0.25, 1, 1, 1, 0.95, 0.85, 0.35} // Sunday first

var hourWeight = [24]float64{0.05, 0.04, 0.03, 0.03, 0.04, 0.08, 0.15, 0.3, 0.65, 0.95, 1, 1, 0.85, 0.95, 1, 0.95, 0.85, 0.65, 0.4, 0.3, 0.25, 0.2, 0.12, 0.08}

// when is a moment between from and to, weighted towards weekdays and business hours,
// and slightly towards the recent end so volume looks like it grows.
func (g *gen) when(from, to time.Time) time.Time {
	span := to.Sub(from)
	if span <= 0 {
		return from
	}
	var t time.Time
	for range 60 {
		t = from.Add(time.Duration(g.rng.Int64N(int64(span))))
		local := t.In(g.tz)
		w := dayWeight[local.Weekday()] * hourWeight[local.Hour()] * (0.7 + 0.3*float64(t.Sub(from))/float64(span))
		if g.rng.Float64() < w {
			return t
		}
	}
	return t
}

// ago is now minus d.
func (g *gen) ago(d time.Duration) time.Time { return g.now.Add(-d) }

// fill replaces the placeholders of written content. Values are escaped for HTML when
// asHTML is set.
func (g *gen) fill(s string, asHTML bool, vars map[string]string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	esc := func(v string) string {
		if asHTML {
			return html.EscapeString(v)
		}
		return v
	}
	pairs := []string{"{product}", esc(g.name), "{site}", esc(g.site)}
	for k, v := range vars {
		if k == "mention" {
			pairs = append(pairs, "{mention}", v) // already HTML
			continue
		}
		pairs = append(pairs, "{"+k+"}", esc(v))
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// textHTML turns plain text with blank-line paragraphs into HTML.
func textHTML(s string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.TrimSpace(s), "\n\n") {
		b.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(para), "\n", "<br>") + "</p>")
	}
	return b.String()
}

// mention is the editor's mark for a teammate.
func mention(m *member) string {
	return fmt.Sprintf(`<span class="mention" data-type="mention" data-id="%s">@%s</span>`, m.id, html.EscapeString(m.name))
}

// localPart makes an address's local part from a name: lowercase ASCII letters, digits and dots.
func localPart(name string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.Is(unicode.Mn, r):
		case r == ' ' || r == '-' || r == '.' || r == '\'':
			if s := b.String(); s != "" && !strings.HasSuffix(s, ".") {
				b.WriteByte('.')
			}
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		s = "person"
	}
	return s
}

// domainLabel makes a domain label from a company name.
func domainLabel(name string) string { return strings.ReplaceAll(localPart(name), ".", "-") }

func isNoRows(err error) bool { return db.IsNotFound(err) }
