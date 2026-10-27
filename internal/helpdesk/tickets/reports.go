package tickets

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/kb"
	"github.com/useopendesk/opendesk/internal/helpdesk/wiki"
	"github.com/useopendesk/opendesk/internal/httpx"
)

type ReportDay struct {
	Date    string `json:"date"`
	Created int    `json:"created"`
	Solved  int    `json:"solved"`
}

type ReportAgent struct {
	User                    domain.UserRef `json:"user"`
	Solved                  int            `json:"solved"`
	FirstResponseP50Minutes *float64       `json:"first_response_p50_minutes"`
}

// ReportTeam is one team's row of the overview; TeamID nil counts tickets without a team.
type ReportTeam struct {
	TeamID                  *uuid.UUID `json:"team_id"`
	Created                 int        `json:"created"`
	Solved                  int        `json:"solved"`
	OpenNow                 int        `json:"open_now"`
	FirstResponseP50Minutes *float64   `json:"first_response_p50_minutes"`
}

// ReportBrand is one brand's row of the overview.
type ReportBrand struct {
	BrandID                 uuid.UUID `json:"brand_id"`
	Created                 int       `json:"created"`
	Solved                  int       `json:"solved"`
	OpenNow                 int       `json:"open_now"`
	FirstResponseP50Minutes *float64  `json:"first_response_p50_minutes"`
}

// reportFilter narrows the overview to a team ("none": no team) and a brand.
type reportFilter struct {
	Team  string
	Brand string
}

// sql binds the filter as $n and $n+1.
func (f reportFilter) sql(n int) string {
	return fmt.Sprintf(` AND ($%[1]d = '' OR ($%[1]d = 'none' AND t.team_id IS NULL) OR t.team_id::text = $%[1]d)
		AND ($%[2]d = '' OR t.brand_id::text = $%[2]d)`, n, n+1)
}

func (f reportFilter) validate() (reportFilter, error) {
	f.Team, f.Brand = strings.TrimSpace(f.Team), strings.TrimSpace(f.Brand)
	if f.Team != "" && f.Team != hd.TeamNone {
		id, err := hd.ParseRef(f.Team, "team_id")
		if err != nil {
			return f, err
		}
		f.Team = id.String()
	}
	if f.Brand != "" {
		id, err := hd.ParseRef(f.Brand, "brand_id")
		if err != nil {
			return f, err
		}
		f.Brand = id.String()
	}
	return f, nil
}

// Overview is GET /v1/reports/overview.
type ReportOverview struct {
	Created                 int            `json:"created"`
	Solved                  int            `json:"solved"`
	OpenNow                 int            `json:"open_now"`
	FirstResponseP50Minutes *float64       `json:"first_response_p50_minutes"`
	ResolutionP50Minutes    *float64       `json:"resolution_p50_minutes"`
	CSATAvg                 *float64       `json:"csat_avg"`
	CSATCount               int            `json:"csat_count"`
	SLABreached             int            `json:"sla_breached"`
	ByDay                   []ReportDay    `json:"by_day"`
	ByChannel               map[string]int `json:"by_channel"`
	BySeverity              map[string]int `json:"by_severity" doc:"Tickets created in the range by severity; none counts tickets without one"`
	ByAgent                 []ReportAgent  `json:"by_agent"`
	ByTeam                  []ReportTeam   `json:"by_team" doc:"Every team with tickets in the range or open now; team_id null counts tickets without a team"`
	ByBrand                 []ReportBrand  `json:"by_brand"`
}

// parseBound reads YYYY-MM-DD (a day in loc) or RFC 3339. endOfDay moves a date to the
// start of the next day so "to" is inclusive.
func parseBound(s string, loc *time.Location, endOfDay bool) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		if endOfDay {
			t = t.AddDate(0, 0, 1)
		}
		return t, true, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true, nil
	}
	return time.Time{}, false, httpx.Validation("from and to must be YYYY-MM-DD or RFC 3339 timestamps")
}

func round1(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := math.Round(*v*10) / 10
	return &r
}

// reportPeriod reads a report's from and to in the workspace's timezone: the last 30 days
// by default, at most 366 days.
func reportPeriod(ctx context.Context, q db.Querier, wid uuid.UUID, fromS, toS string) (from, to time.Time, loc *time.Location, err error) {
	s, err := hd.LoadSettings(ctx, q, wid)
	if err != nil {
		return from, to, nil, err
	}
	loc = s.Location()
	to, ok, err := parseBound(toS, loc, true)
	if err != nil {
		return from, to, loc, err
	}
	if !ok {
		to = time.Now()
	}
	from, ok, err = parseBound(fromS, loc, false)
	if err != nil {
		return from, to, loc, err
	}
	if !ok {
		from = to.AddDate(0, 0, -30)
	}
	if !from.Before(to) {
		return from, to, loc, httpx.Validation("from must be before to")
	}
	if to.Sub(from) > 366*24*time.Hour {
		return from, to, loc, httpx.Validation("the range can be at most 366 days")
	}
	return from, to, loc, nil
}

func overview(ctx context.Context, q db.Querier, wid uuid.UUID, fromS, toS string, rf reportFilter) (ReportOverview, error) {
	o := ReportOverview{ByDay: []ReportDay{}, ByAgent: []ReportAgent{}, ByChannel: map[string]int{}, BySeverity: map[string]int{},
		ByTeam: []ReportTeam{}, ByBrand: []ReportBrand{}}
	rf, err := rf.validate()
	if err != nil {
		return o, err
	}
	from, to, loc, err := reportPeriod(ctx, q, wid, fromS, toS)
	if err != nil {
		return o, err
	}
	tz := loc.String()

	// Reports count every ticket, private teams' included, without subjects or contacts.
	base := `FROM tickets t WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam` + rf.sql(4)
	var frP50, resP50, csatAvg *float64
	err = q.QueryRow(ctx, `SELECT
		(SELECT count(*) `+base+` AND t.created_at >= $2 AND t.created_at < $3),
		(SELECT count(*) `+base+` AND t.solved_at >= $2 AND t.solved_at < $3),
		(SELECT count(*) `+base+` AND t.status IN ('open', 'pending', 'on_hold')),
		(SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM t.first_responded_at - t.created_at) / 60) `+base+`
			AND t.created_at >= $2 AND t.created_at < $3 AND t.first_responded_at IS NOT NULL),
		(SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM t.solved_at - t.created_at) / 60) `+base+`
			AND t.solved_at >= $2 AND t.solved_at < $3),
		(SELECT avg(t.csat_score)::float8 `+base+` AND t.csat_score IS NOT NULL AND t.solved_at >= $2 AND t.solved_at < $3),
		(SELECT count(*) `+base+` AND t.csat_score IS NOT NULL AND t.solved_at >= $2 AND t.solved_at < $3),
		(SELECT count(DISTINCT a.ticket_id) FROM activities a JOIN tickets t ON t.id = a.ticket_id AND t.deleted_at IS NULL AND NOT t.spam
			WHERE a.workspace_id = $1 AND a.type = 'sla_breached' AND a.created_at >= $2 AND a.created_at < $3`+rf.sql(4)+`)`,
		wid, from, to, rf.Team, rf.Brand).Scan(&o.Created, &o.Solved, &o.OpenNow, &frP50, &resP50, &csatAvg, &o.CSATCount, &o.SLABreached)
	if err != nil {
		return o, err
	}
	o.FirstResponseP50Minutes, o.ResolutionP50Minutes = round1(frP50), round1(resP50)
	if csatAvg != nil {
		v := math.Round(*csatAvg*100) / 100
		o.CSATAvg = &v
	}

	base5 := `FROM tickets t WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam` + rf.sql(5)
	rows, err := q.Query(ctx, `WITH days AS (
			SELECT generate_series(($2::timestamptz AT TIME ZONE $4)::date, (($3::timestamptz - interval '1 microsecond') AT TIME ZONE $4)::date,
				interval '1 day')::date AS day),
		c AS (SELECT (t.created_at AT TIME ZONE $4)::date AS day, count(*) AS n `+base5+` AND t.created_at >= $2 AND t.created_at < $3 GROUP BY 1),
		s AS (SELECT (t.solved_at AT TIME ZONE $4)::date AS day, count(*) AS n `+base5+` AND t.solved_at >= $2 AND t.solved_at < $3 GROUP BY 1)
		SELECT to_char(days.day, 'YYYY-MM-DD'), COALESCE(c.n, 0), COALESCE(s.n, 0)
		FROM days LEFT JOIN c USING (day) LEFT JOIN s USING (day) ORDER BY days.day`, wid, from, to, tz, rf.Team, rf.Brand)
	if err != nil {
		return o, err
	}
	o.ByDay, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReportDay, error) {
		var dc ReportDay
		err := r.Scan(&dc.Date, &dc.Created, &dc.Solved)
		return dc, err
	})
	if err != nil {
		return o, err
	}

	for _, c := range domain.Channels {
		o.ByChannel[c] = 0
	}
	rows, err = q.Query(ctx, `SELECT t.channel, count(*) `+base+` AND t.created_at >= $2 AND t.created_at < $3 GROUP BY t.channel`, wid, from, to, rf.Team, rf.Brand)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var ch string
		var n int
		if err := rows.Scan(&ch, &n); err != nil {
			rows.Close()
			return o, err
		}
		o.ByChannel[ch] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}

	for _, s := range hd.SeverityFilterValues {
		o.BySeverity[s] = 0
	}
	rows, err = q.Query(ctx, `SELECT COALESCE(t.severity, 'none'), count(*) `+base+` AND t.created_at >= $2 AND t.created_at < $3 GROUP BY 1`, wid, from, to, rf.Team, rf.Brand)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			rows.Close()
			return o, err
		}
		o.BySeverity[sev] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}

	rows, err = q.Query(ctx, `SELECT u.id, u.name, u.email, u.avatar_url,
			count(*) FILTER (WHERE t.solved_at >= $2 AND t.solved_at < $3),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM t.first_responded_at - t.created_at) / 60)
				FILTER (WHERE t.first_responded_at IS NOT NULL AND t.created_at >= $2 AND t.created_at < $3)
		FROM tickets t JOIN users u ON u.id = t.assignee_id
		JOIN members m ON m.workspace_id = t.workspace_id AND m.user_id = u.id
		WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam`+rf.sql(4)+`
			AND ((t.solved_at >= $2 AND t.solved_at < $3) OR (t.created_at >= $2 AND t.created_at < $3))
		GROUP BY u.id, u.name, u.email, u.avatar_url
		ORDER BY 5 DESC, u.name LIMIT 100`, wid, from, to, rf.Team, rf.Brand)
	if err != nil {
		return o, err
	}
	o.ByAgent, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReportAgent, error) {
		var a ReportAgent
		var p50 *float64
		err := r.Scan(&a.User.ID, &a.User.Name, &a.User.Email, &a.User.AvatarURL, &a.Solved, &p50)
		a.FirstResponseP50Minutes = round1(p50)
		return a, err
	})
	if err != nil {
		return o, err
	}
	if o.ByAgent == nil {
		o.ByAgent = []ReportAgent{}
	}

	// By team and by brand: created, solved, open now and first response, per group.
	group := func(col string) string {
		return `SELECT ` + col + `,
			count(*) FILTER (WHERE t.created_at >= $2 AND t.created_at < $3),
			count(*) FILTER (WHERE t.solved_at >= $2 AND t.solved_at < $3),
			count(*) FILTER (WHERE t.status IN ('open', 'pending', 'on_hold')),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM t.first_responded_at - t.created_at) / 60)
				FILTER (WHERE t.first_responded_at IS NOT NULL AND t.created_at >= $2 AND t.created_at < $3)
			` + base + ` AND ((t.created_at >= $2 AND t.created_at < $3) OR (t.solved_at >= $2 AND t.solved_at < $3)
				OR t.status IN ('open', 'pending', 'on_hold'))
			GROUP BY 1 ORDER BY 2 DESC, 4 DESC, 1 LIMIT 500`
	}
	rows, err = q.Query(ctx, group("t.team_id"), wid, from, to, rf.Team, rf.Brand)
	if err != nil {
		return o, err
	}
	o.ByTeam, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReportTeam, error) {
		var x ReportTeam
		var p50 *float64
		err := r.Scan(&x.TeamID, &x.Created, &x.Solved, &x.OpenNow, &p50)
		x.FirstResponseP50Minutes = round1(p50)
		return x, err
	})
	if err != nil {
		return o, err
	}
	rows, err = q.Query(ctx, group("t.brand_id"), wid, from, to, rf.Team, rf.Brand)
	if err != nil {
		return o, err
	}
	o.ByBrand, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReportBrand, error) {
		var x ReportBrand
		var p50 *float64
		err := r.Scan(&x.BrandID, &x.Created, &x.Solved, &x.OpenNow, &p50)
		x.FirstResponseP50Minutes = round1(p50)
		return x, err
	})
	if o.ByTeam == nil {
		o.ByTeam = []ReportTeam{}
	}
	if o.ByBrand == nil {
		o.ByBrand = []ReportBrand{}
	}
	return o, err
}

// SearchResult is GET /v1/search.
type SearchResults struct {
	Tickets  []domain.Ticket  `json:"tickets"`
	Contacts []domain.Contact `json:"contacts"`
	Articles []kb.KbArticle   `json:"articles"`
	// WikiPages are the team's internal pages (contracts/wiki.md).
	WikiPages []wiki.WikiSearchHit `json:"wiki_pages"`
}

// search finds the caller's tickets, contacts, articles and wiki pages. brand narrows the articles to
// what that brand shows (its own and shared ones).
func search(ctx context.Context, q db.Querier, p *auth.Principal, query string, brand *uuid.UUID) (SearchResults, error) {
	res := SearchResults{Tickets: []domain.Ticket{}, Contacts: []domain.Contact{}, Articles: []kb.KbArticle{}, WikiPages: []wiki.WikiSearchHit{}}
	// A person signed in directly sees what their role allows; an API key or a person
	// acting through an OAuth grant only what its scopes allow.
	allowed := func(scope string) bool {
		return (p.Kind == auth.KindUser && p.GrantID == uuid.Nil) || p.HasScope(scope)
	}
	if allowed(auth.ScopeTicketsRead) {
		lq, err := forCaller(ctx, q, p, listQuery{TicketFilters: hd.TicketFilters{Q: query}})
		if err != nil {
			return res, err
		}
		page, err := listTickets(ctx, q, p.WorkspaceID, p.ActorUserID(), lq, "", "", 5, "")
		if err != nil {
			return res, err
		}
		res.Tickets = page.Data
	}
	if allowed(auth.ScopeContactsRead) {
		args := []any{p.WorkspaceID, query, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(query)) + "%"}
		cond := `ct.search @@ websearch_to_tsquery('simple', $2) OR ct.fields_search @@ websearch_to_tsquery('simple', $2)
			OR lower(ct.email::text) LIKE $3 OR lower(ct.name) LIKE $3`
		if pq := prefixQuery(query); pq != "" {
			args = append(args, pq)
			cond += ` OR ct.search @@ to_tsquery('simple', $4) OR ct.fields_search @@ to_tsquery('simple', $4)`
		}
		rows, err := q.Query(ctx, domain.ContactSelect+` WHERE ct.workspace_id = $1 AND (`+cond+`) ORDER BY ct.updated_at DESC, ct.id LIMIT 5`, args...)
		if err != nil {
			return res, err
		}
		res.Contacts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Contact, error) { return domain.ScanContact(r) })
		if err != nil {
			return res, err
		}
		if res.Contacts == nil {
			res.Contacts = []domain.Contact{}
		}
	}
	if allowed(auth.ScopeKBRead) {
		arts, err := kb.Search(ctx, q, p.WorkspaceID, query, 5, brand)
		if err != nil {
			return res, err
		}
		res.Articles = arts
	}
	if allowed(auth.ScopeWikiRead) {
		pages, err := wiki.Search(ctx, q, p.WorkspaceID, query, 5)
		if err != nil {
			return res, err
		}
		res.WikiPages = pages
	}
	return res, nil
}

func registerInsights(d *deps.Deps, api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "search", Method: http.MethodGet, Path: "/v1/search",
		Summary: "Search tickets, contacts, articles and wiki pages", Tags: []string{"Search"},
		Description: "Top 5 of each. API keys see only the kinds their scopes allow."},
		func(ctx context.Context, in *struct {
			Q       string `query:"q" required:"true" minLength:"1" maxLength:"200"`
			BrandID string `query:"brand_id" doc:"Articles of this brand and of every brand only"`
		}) (*struct{ Body SearchResults }, error) {
			p, err := requireAnyScope(ctx, auth.ScopeTicketsRead, auth.ScopeContactsRead, auth.ScopeKBRead, auth.ScopeWikiRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			q := strings.TrimSpace(in.Q)
			if q == "" {
				return nil, httpx.Validation("q is required")
			}
			var brand *uuid.UUID
			if strings.TrimSpace(in.BrandID) != "" {
				id, err := hd.ParseRef(in.BrandID, "brand_id")
				if err != nil {
					return nil, err
				}
				brand = &id
			}
			res, err := search(ctx, d.DB.Pool, p, q, brand)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body SearchResults }{res}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "report-overview", Method: http.MethodGet, Path: "/v1/reports/overview",
		Summary: "Helpdesk overview report", Tags: []string{"Reports"},
		Description: "from and to are YYYY-MM-DD (days in the workspace timezone, to inclusive) or RFC 3339. Default: the last 30 days. Spam and deleted tickets are excluded. team_id (or none) and brand_id narrow every figure."},
		func(ctx context.Context, in *struct {
			From    string `query:"from"`
			To      string `query:"to"`
			TeamID  string `query:"team_id" doc:"A team id, or none for tickets without a team"`
			BrandID string `query:"brand_id"`
		}) (*struct{ Body ReportOverview }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			o, err := overview(ctx, d.DB.Pool, p.WorkspaceID, in.From, in.To, reportFilter{Team: in.TeamID, Brand: in.BrandID})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body ReportOverview }{o}, nil
		})
}

// requireAnyScope accepts users with a workspace, and API keys holding any of scopes.
func requireAnyScope(ctx context.Context, scopes ...string) (*auth.Principal, error) {
	var last error
	for _, s := range scopes {
		p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, s)
		if err == nil {
			return p, nil
		}
		last = err
	}
	return nil, last
}
