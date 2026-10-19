package sample

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const day = 24 * time.Hour

// seedTeam adds teammates (real users without a password, so they cannot sign in) and
// teams. A later batch takes the next names; a team that exists by name is reused.
func (g *gen) seedTeam(ctx context.Context) error {
	return g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		n := g.vol.members
		offset := ((g.batch - 1) * n) % len(agentNames)
		admins := max(1, n/8)
		var added []*member
		var viewers []*member
		for i := range n {
			def := agentNames[(offset+i)%len(agentNames)]
			role := "agent"
			switch {
			case i < admins:
				role = "admin"
			case n >= 3 && i == n-1:
				role = "viewer"
			}
			m := &member{id: ids.New(), name: def.name, email: localPart(def.name) + g.suffix() + "@" + g.site, role: role}
			joined := g.ago(time.Duration(g.between(g.vol.days+20, g.vol.days+420)) * day)
			availability := []string{"online", "away", "offline"}[g.weighted(0.6, 0.25, 0.15)]
			seen := g.ago(g.minutes(8, 0.8, 1, 30))
			switch availability {
			case "away":
				seen = g.ago(g.minutes(120, 0.5, 40, 300))
			case "offline":
				seen = g.ago(time.Duration(g.between(20, 120)) * time.Hour)
			}
			var id uuid.UUID
			err := tx.QueryRow(ctx, `INSERT INTO users (id, email, name, email_verified_at, created_at, updated_at, last_seen_at)
				VALUES ($1, $2, $3, $4, $4, $4, $5) ON CONFLICT (email) DO NOTHING RETURNING id`, m.id, m.email, m.name, joined, seen).Scan(&id)
			if isNoRows(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("add teammate: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role, availability, title, created_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, g.wid, m.id, role, availability, def.title, joined); err != nil {
				return fmt.Errorf("add member: %w", err)
			}
			g.counts.Members++
			if role == "viewer" {
				viewers = append(viewers, m)
			} else {
				added = append(added, m)
			}
		}

		k := min(g.vol.teams, len(teamDefs))
		for i := range k {
			def := teamDefs[i]
			id := ids.New()
			created := g.ago(time.Duration(g.between(g.vol.days, g.vol.days+300)) * day)
			err := tx.QueryRow(ctx, `INSERT INTO teams (id, workspace_id, name, description, color, private, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $7) ON CONFLICT (workspace_id, name) DO NOTHING RETURNING id`,
				id, g.wid, def.name, def.desc, def.color, def.private, created).Scan(&id)
			fresh := err == nil
			if isNoRows(err) {
				err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE workspace_id = $1 AND name = $2`, g.wid, def.name).Scan(&id)
			}
			if err != nil {
				return fmt.Errorf("add team: %w", err)
			}
			if fresh {
				g.counts.Teams++
				// The owner is on every new team, so private ones stay visible to them.
				if g.o.OwnerID != uuid.Nil {
					role := "member"
					if i == 0 {
						role = "lead"
					}
					if _, err := tx.Exec(ctx, `INSERT INTO team_members (team_id, user_id, role)
						SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM members WHERE workspace_id = $4 AND user_id = $2) ON CONFLICT DO NOTHING`,
						id, g.o.OwnerID, role, g.wid); err != nil {
						return err
					}
				}
			}
			lead := true
			for j, m := range added {
				if j%k != i && !(k > 1 && (j+1)%k == i && g.chance(0.35)) {
					continue
				}
				role := "member"
				if lead && i != 0 {
					role = "lead"
				}
				lead = false
				if _, err := tx.Exec(ctx, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
					id, m.id, role); err != nil {
					return err
				}
			}
			if i == 0 {
				for _, v := range viewers {
					if _, err := tx.Exec(ctx, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'observer') ON CONFLICT DO NOTHING`,
						id, v.id); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// loadRoster reads who can reply (the owner, admins and agents) and the teams with their
// members, generated or not.
func (g *gen) loadRoster(ctx context.Context) error {
	rows, err := g.d.DB.Pool.Query(ctx, `SELECT u.id, u.name, u.email::text, m.role FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND m.role <> 'viewer' AND u.suspended_at IS NULL ORDER BY m.created_at, u.id`, g.wid)
	if err != nil {
		return err
	}
	byID := map[uuid.UUID]*member{}
	for rows.Next() {
		m := &member{}
		if err := rows.Scan(&m.id, &m.name, &m.email, &m.role); err != nil {
			rows.Close()
			return err
		}
		if strings.TrimSpace(m.name) == "" {
			m.name = strings.Split(m.email, "@")[0]
		}
		g.members = append(g.members, m)
		byID[m.id] = m
		if m.role == "owner" && (g.owner == nil || m.id == g.o.OwnerID) {
			g.owner = m
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(g.members) == 0 {
		return errors.New("the workspace has no members to write sample data as")
	}
	if g.owner == nil {
		g.owner = g.members[0]
	}
	rows, err = g.d.DB.Pool.Query(ctx, `SELECT t.id, t.name, COALESCE(array_agg(tm.user_id) FILTER (WHERE tm.user_id IS NOT NULL AND tm.role <> 'observer'), '{}')
		FROM teams t LEFT JOIN team_members tm ON tm.team_id = t.id WHERE t.workspace_id = $1 GROUP BY t.id ORDER BY min(t.created_at), t.id`, g.wid)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		t := &team{}
		var people []uuid.UUID
		if err := rows.Scan(&t.id, &t.name, &people); err != nil {
			return err
		}
		for _, def := range teamDefs {
			if strings.EqualFold(def.name, t.name) {
				t.key = def.key
			}
		}
		for _, id := range people {
			if m := byID[id]; m != nil {
				t.members = append(t.members, m)
			}
		}
		g.teams = append(g.teams, t)
	}
	return rows.Err()
}

// teamFor is the team for a topic key: its own when the workspace has it, else Support,
// else the first team, else none.
func (g *gen) teamFor(key string) *team {
	var support *team
	for _, t := range g.teams {
		if t.key == key && key != "" {
			return t
		}
		if t.key == "support" {
			support = t
		}
	}
	if support != nil {
		return support
	}
	if len(g.teams) > 0 {
		return g.teams[0]
	}
	return nil
}

// assigneeIn picks who handles a ticket of team t.
func (g *gen) assigneeIn(t *team) *member {
	if t != nil && len(t.members) > 0 && g.chance(0.85) {
		return pick(g, t.members)
	}
	return pick(g, g.members)
}

// otherThan picks a teammate other than m, for mentions; m itself when alone.
func (g *gen) otherThan(m *member) *member {
	if len(g.members) < 2 {
		return m
	}
	for {
		if o := pick(g, g.members); o.id != m.id {
			return o
		}
	}
}

type slaPolicy struct {
	id       uuid.UUID
	priority *string
	team     *uuid.UUID
	fr, res  int
}

// policyFor is the SLA policy a ticket gets, as applySLA picks it (calendar time).
func (g *gen) policyFor(priority string, teamID *uuid.UUID) *slaPolicy {
	for i := range g.sla {
		p := &g.sla[i]
		if (p.priority == nil || *p.priority == priority) && (p.team == nil || (teamID != nil && *p.team == *teamID)) {
			return p
		}
	}
	return nil
}

// Contact fields sample contacts fill in, created when missing.
var fieldDefs = []struct {
	key, name string
	options   []domain.FieldOption
	weights   []float64
}{
	{"plan", "Plan", []domain.FieldOption{{ID: "free", Label: "Free", Color: "gray"}, {ID: "team", Label: "Team", Color: "blue"},
		{ID: "business", Label: "Business", Color: "violet"}, {ID: "enterprise", Label: "Enterprise", Color: "amber"}}, []float64{0.2, 0.4, 0.3, 0.1}},
	{"region", "Region", []domain.FieldOption{{ID: "europe", Label: "Europe", Color: "blue"}, {ID: "north_america", Label: "North America", Color: "green"},
		{ID: "asia_pacific", Label: "Asia Pacific", Color: "orange"}, {ID: "latin_america", Label: "Latin America", Color: "pink"}}, []float64{0.45, 0.35, 0.12, 0.08}},
}

// seedPeople adds labels, contact fields, companies and contacts, and reads the SLA
// policies tickets will follow.
func (g *gen) seedPeople(ctx context.Context) error {
	g.labels, g.vips = map[string]uuid.UUID{}, map[uuid.UUID]bool{}
	fieldOptions := map[string][]string{}
	err := g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		for _, name := range allLabels {
			ids, err := domain.EnsureTags(ctx, tx, g.wid, []string{name})
			var he *httpx.Error
			if errors.As(err, &he) {
				continue // the workspace has as many labels as it may
			}
			if err != nil {
				return err
			}
			g.labels[name] = ids[0]
		}
		for _, f := range fieldDefs {
			opts, err := g.ensureField(ctx, tx, f.key, f.name, f.options)
			if err != nil {
				return err
			}
			fieldOptions[f.key] = opts
		}
		return nil
	})
	if err != nil {
		return err
	}
	rows, err := g.d.DB.Pool.Query(ctx, `SELECT id, priority, team_id, first_response_minutes, resolution_minutes FROM sla_policies
		WHERE workspace_id = $1 AND enabled ORDER BY position, (priority IS NULL), (team_id IS NULL), created_at`, g.wid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p slaPolicy
		if err := rows.Scan(&p.id, &p.priority, &p.team, &p.fr, &p.res); err != nil {
			rows.Close()
			return err
		}
		g.sla = append(g.sla, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := g.seedCompanies(ctx); err != nil {
		return err
	}
	return g.seedContacts(ctx, fieldOptions)
}

// ensureField creates a select contact field unless one with the key exists, and returns
// the option ids sample contacts may use (none when the existing field is not a select).
func (g *gen) ensureField(ctx context.Context, tx pgx.Tx, key, name string, options []domain.FieldOption) ([]string, error) {
	id := ids.New()
	err := tx.QueryRow(ctx, `INSERT INTO custom_fields (id, workspace_id, entity, key, name, type, options, position)
		VALUES ($1, $2, 'contact', $3, $4, 'select', $5, (SELECT COALESCE(max(position) + 1, 0) FROM custom_fields WHERE workspace_id = $2 AND entity = 'contact'))
		ON CONFLICT (workspace_id, entity, key) DO NOTHING RETURNING id`, id, g.wid, key, name, options).Scan(&id)
	if err == nil {
		f, err := domain.ScanCustomField(tx.QueryRow(ctx, domain.CustomFieldSelect+` WHERE workspace_id = $1 AND id = $2`, g.wid, id))
		if err != nil {
			return nil, err
		}
		if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: g.wid, Type: "custom_field.created", Data: map[string]any{"custom_field": f}}); err != nil {
			return nil, err
		}
	} else if !isNoRows(err) {
		return nil, err
	}
	f, err := domain.ScanCustomField(tx.QueryRow(ctx, domain.CustomFieldSelect+` WHERE workspace_id = $1 AND entity = 'contact' AND key = $2`, g.wid, key))
	if err != nil {
		return nil, err
	}
	if f.Type != domain.FieldSelect || f.Archived {
		return nil, nil
	}
	out := []string{}
	for _, o := range f.Options {
		out = append(out, o.ID)
	}
	return out, nil
}

// seedCompanies adds companies on .invalid domains; one whose domain exists is reused.
func (g *gen) seedCompanies(ctx context.Context) error {
	names := make([]companyDef, 0, g.vol.companies)
	names = append(names, companyDefs[:min(len(companyDefs), g.vol.companies)]...)
	for i := 0; len(names) < g.vol.companies && i < len(companyPrefixes)*len(companySuffixes); i++ {
		// A fixed walk through the combinations, so every batch names them alike.
		p, s := companyPrefixes[i%len(companyPrefixes)], companySuffixes[(i*7+i/len(companyPrefixes))%len(companySuffixes)]
		names = append(names, companyDef{name: p + " " + s})
	}
	var idList []uuid.UUID
	var nameList, domainList, noteList []string
	var created []time.Time
	seen := map[string]bool{}
	for _, c := range names {
		dom := domainLabel(c.name) + ".example.invalid"
		if seen[dom] {
			continue
		}
		seen[dom] = true
		idList = append(idList, ids.New())
		nameList = append(nameList, c.name)
		domainList = append(domainList, dom)
		noteList = append(noteList, c.notes)
		created = append(created, g.ago(time.Duration(g.between(g.vol.days+10, g.vol.days+500))*day))
	}
	return g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		// No logo lookup is ever due: the domains are not real.
		rows, err := tx.Query(ctx, `INSERT INTO companies (id, workspace_id, name, domain, notes, created_at, updated_at, avatar_retry_at, avatar_checked_at)
			SELECT u.id, $1, u.name, u.domain, u.notes, u.created_at, u.created_at, NULL, now()
			FROM unnest($2::uuid[], $3::text[], $4::text[], $5::text[], $6::timestamptz[]) AS u(id, name, domain, notes, created_at)
			ON CONFLICT DO NOTHING RETURNING id`, g.wid, idList, nameList, domainList, noteList, created)
		if err != nil {
			return fmt.Errorf("add companies: %w", err)
		}
		inserted, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		g.counts.Companies += len(inserted)
		rows, err = tx.Query(ctx, `SELECT id, name, domain::text FROM companies WHERE workspace_id = $1 AND domain = ANY($2::text[])`, g.wid, domainList)
		if err != nil {
			return err
		}
		byDomain := map[string]*company{}
		for rows.Next() {
			c := &company{}
			if err := rows.Scan(&c.id, &c.name, &c.domain); err != nil {
				rows.Close()
				return err
			}
			byDomain[c.domain] = c
		}
		rows.Close()
		for _, dom := range domainList {
			if c := byDomain[dom]; c != nil {
				g.companies = append(g.companies, c)
			}
		}
		return rows.Err()
	})
}

// seedContacts adds contacts: most at a company (a few companies have many people), the
// rest on personal domains, some with a phone, an external id, notes, the contact fields
// and the VIP label.
func (g *gen) seedContacts(ctx context.Context, fieldOptions map[string][]string) error {
	type row struct {
		id                        uuid.UUID
		email, name, phone, notes string
		companyID                 *uuid.UUID
		externalID                *string
		fields                    string
		created                   time.Time
		company                   *company
	}
	plans := map[uuid.UUID]string{} // a company's people share its plan
	used := map[string]bool{}
	list := make([]row, 0, g.vol.contacts)
	for i := range g.vol.contacts {
		first, last := pick(g, firstNames), pick(g, lastNames)
		var c *company
		if len(g.companies) > 0 && g.chance(0.75) {
			// Skewed: the first companies have most of the people.
			c = g.companies[int(float64(len(g.companies))*math.Pow(g.rng.Float64(), 1.8))]
		}
		dom := pick(g, personalDomains)
		if c != nil {
			dom = c.domain
		}
		local := localPart(first) + "." + localPart(last)
		for n := 2; used[local+"@"+dom]; n++ {
			local = fmt.Sprintf("%s.%s%d", localPart(first), localPart(last), n)
		}
		used[local+"@"+dom] = true
		r := row{id: ids.New(), name: first + " " + last, email: local + g.suffix() + "@" + dom, company: c,
			created: g.ago(time.Duration(g.between(g.vol.days, g.vol.days+400)) * day)}
		if c != nil {
			r.companyID = &c.id
		}
		if g.chance(0.4) {
			if g.chance(0.5) {
				r.phone = fmt.Sprintf("+1 202 555 01%02d", g.between(0, 99))
			} else {
				r.phone = fmt.Sprintf("+44 20 7946 0%03d", g.between(0, 999))
			}
		}
		if g.chance(0.3) {
			ext := fmt.Sprintf("cus_%d%06d", g.batch, i)
			r.externalID = &ext
		}
		if g.chance(0.04) {
			r.notes = pick(g, contactNotes)
		}
		values := map[string]any{}
		if opts := fieldOptions["plan"]; len(opts) > 0 {
			plan := ""
			if c != nil {
				if plan = plans[c.id]; plan == "" {
					plan = pickOption(g, opts, fieldDefs[0].options, fieldDefs[0].weights)
					plans[c.id] = plan
				}
			} else {
				plan = pickOption(g, opts, fieldDefs[0].options, []float64{0.6, 0.4, 0, 0})
			}
			values["plan"] = plan
		}
		if opts := fieldOptions["region"]; len(opts) > 0 && g.chance(0.9) {
			values["region"] = pickOption(g, opts, fieldDefs[1].options, fieldDefs[1].weights)
		}
		b, _ := json.Marshal(values)
		r.fields = string(b)
		list = append(list, r)
	}
	const chunk = 500
	for start := 0; start < len(list); start += chunk {
		part := list[start:min(start+chunk, len(list))]
		err := g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			n := len(part)
			idList, emails, names, phones, notesList, fields := make([]uuid.UUID, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
			companies, externals, created := make([]*uuid.UUID, n), make([]*string, n), make([]time.Time, n)
			for i, r := range part {
				idList[i], emails[i], names[i], phones[i], notesList[i], fields[i] = r.id, r.email, r.name, r.phone, r.notes, r.fields
				companies[i], externals[i], created[i] = r.companyID, r.externalID, r.created
			}
			// No picture lookup is ever due: the addresses are not real.
			rows, err := tx.Query(ctx, `INSERT INTO contacts (id, workspace_id, email, name, phone, notes, fields, company_id, external_id, created_at, updated_at,
				avatar_retry_at, avatar_checked_at)
				SELECT u.id, $1, u.email, u.name, u.phone, u.notes, u.fields::jsonb, u.company_id, u.external_id, u.created_at, u.created_at, NULL, now()
				FROM unnest($2::uuid[], $3::text[], $4::text[], $5::text[], $6::text[], $7::text[], $8::uuid[], $9::text[], $10::timestamptz[])
					AS u(id, email, name, phone, notes, fields, company_id, external_id, created_at)
				ON CONFLICT DO NOTHING RETURNING id`, g.wid, idList, emails, names, phones, notesList, fields, companies, externals, created)
			if err != nil {
				return fmt.Errorf("add contacts: %w", err)
			}
			inserted, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			ok := map[uuid.UUID]bool{}
			for _, id := range inserted {
				ok[id] = true
			}
			var vips []uuid.UUID
			for _, r := range part {
				if !ok[r.id] {
					continue
				}
				g.contacts = append(g.contacts, &person{id: r.id, name: r.name, email: r.email, company: r.company})
				if g.chance(0.05) {
					vips = append(vips, r.id)
					g.vips[r.id] = true
				}
			}
			g.counts.Contacts += len(inserted)
			if vip, ok := g.labels[labelVIP]; ok && len(vips) > 0 {
				if _, err := tx.Exec(ctx, `INSERT INTO contact_tags (contact_id, tag_id) SELECT unnest($1::uuid[]), $2 ON CONFLICT DO NOTHING`,
					vips, vip); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(g.contacts) == 0 {
		return errors.New("no sample contacts could be added")
	}
	return nil
}

// pickOption picks one of the field's option ids by our weights when it has our options,
// else any of its options.
func pickOption(g *gen, have []string, ours []domain.FieldOption, weights []float64) string {
	i := g.weighted(weights...)
	for _, id := range have {
		if id == ours[i].ID {
			return id
		}
	}
	return pick(g, have)
}

// seedExtras adds shared views and saved replies; one with the same name or shortcut is
// left as it is.
func (g *gen) seedExtras(ctx context.Context) error {
	type view struct {
		name    string
		filters map[string]any
		team    *team
	}
	views := []view{
		{"Urgent and high", map[string]any{"status": []string{"open", "pending"}, "priority": []string{"urgent", "high"}}, nil},
		{"Waiting chats", map[string]any{"status": []string{"open", "pending"}, "channel": "chat", "assignee": "none"}, nil},
		{"Bugs in progress", map[string]any{"status": []string{"open", "pending", "on_hold"}, "tag": labelBug}, nil},
		{"Sev1 and sev2", map[string]any{"status": []string{"open", "pending", "on_hold"}, "severity": []string{"sev1", "sev2"}}, nil},
	}
	if t := g.teamFor("billing"); t != nil && t.key == "billing" {
		views = append(views, view{"Billing queue", map[string]any{"status": []string{"open", "pending"}, "team_id": t.id.String()}, t})
	}
	return g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		for _, v := range views {
			var teamID *uuid.UUID
			if v.team != nil {
				teamID = &v.team.id
			}
			tag, err := tx.Exec(ctx, `INSERT INTO views (id, workspace_id, user_id, name, filters, position, team_id)
				SELECT $1, $2, NULL, $3, $4, (SELECT COALESCE(max(position) + 1, 0) FROM views WHERE workspace_id = $2), $5
				WHERE NOT EXISTS (SELECT 1 FROM views WHERE workspace_id = $2 AND user_id IS NULL AND name = $3)`,
				ids.New(), g.wid, v.name, v.filters, teamID)
			if err != nil {
				return fmt.Errorf("add view: %w", err)
			}
			g.counts.Views += int(tag.RowsAffected())
		}
		n := len(savedReplies)
		if g.o.Size != SizeLarge {
			n = 6
		}
		for _, r := range savedReplies[:n] {
			tag, err := tx.Exec(ctx, `INSERT INTO saved_replies (id, workspace_id, title, shortcut, body_html, created_by)
				SELECT $1, $2, $3, $4, $5, $6 WHERE NOT EXISTS (SELECT 1 FROM saved_replies WHERE workspace_id = $2 AND (shortcut = $4 OR title = $3))`,
				ids.New(), g.wid, r.title, r.shortcut, hd.SanitizeRichTemplate(g.fill(r.body, true, nil)), g.owner.id)
			if err != nil {
				return fmt.Errorf("add saved reply: %w", err)
			}
			g.counts.SavedReplies += int(tag.RowsAffected())
		}
		return nil
	})
}
