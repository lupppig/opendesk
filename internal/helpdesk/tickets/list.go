package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// listQuery is the parsed GET /v1/tickets query.
type listQuery struct {
	hd.TicketFilters
	ContactID string
	Spam      string // "", "true", "false", "any"
	// Defs are the ticket field definitions (needed for Fields filters and field sorts).
	Defs domain.FieldDefs
	// Hidden are the private teams the caller may not see (domain.HiddenTeams); their
	// tickets are left out unless the caller is the assignee. nil applies no visibility
	// (internal callers only).
	Hidden []uuid.UUID
}

// forCaller returns q with the caller's visibility applied.
func forCaller(ctx context.Context, qr db.Querier, p *auth.Principal, q listQuery) (listQuery, error) {
	hidden, err := domain.HiddenTeams(ctx, qr, p)
	if err != nil {
		return q, err
	}
	q.Hidden = hidden
	return q, nil
}

// prepareListQuery loads and normalizes custom field filters before they reach SQL.
func prepareListQuery(ctx context.Context, q db.Querier, wid uuid.UUID, f listQuery, sort string) (listQuery, error) {
	if f.Defs == nil && (len(f.Fields) > 0 || strings.HasPrefix(sort, "field.")) {
		defs, err := domain.LoadFieldDefs(ctx, q, wid, domain.FieldEntityTicket)
		if err != nil {
			return f, err
		}
		f.Defs = defs
		if f.Fields, err = hd.NormalizeFieldFilters(defs, f.Fields); err != nil {
			return f, err
		}
	}
	return f, nil
}

// sqlBuilder accumulates WHERE clauses and positional args.
type sqlBuilder struct {
	where []string
	args  []any
}

func (b *sqlBuilder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *sqlBuilder) add(clause string) { b.where = append(b.where, clause) }

// argN binds v and returns its parameter number.
func (b *sqlBuilder) argN(v any) int {
	b.args = append(b.args, v)
	return len(b.args)
}

func (b *sqlBuilder) sql() string {
	if len(b.where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(b.where, " AND ")
}

var numberRef = regexp.MustCompile(`^#?(\d{1,18})$`)

// applyFilters adds the filter clauses for f. me is the caller's user id (nil for API keys).
func applyFilters(b *sqlBuilder, wid uuid.UUID, me *uuid.UUID, f listQuery) error {
	b.add("t.workspace_id = " + b.arg(wid))
	b.add("t.deleted_at IS NULL")
	if len(f.Hidden) > 0 {
		if me != nil {
			b.add(domain.VisibleSQLUser("t", b.argN(f.Hidden), b.argN(*me)))
		} else {
			b.add(domain.VisibleSQL("t", b.argN(f.Hidden)))
		}
	}
	switch f.Spam {
	case "true":
		b.add("t.spam")
	case "any":
	case "", "false":
		b.add("NOT t.spam")
	default:
		return httpx.Validation("spam must be true, false or any")
	}
	if len(f.Status) > 0 {
		b.add("t.status = ANY(" + b.arg(f.Status) + ")")
	}
	if len(f.Priority) > 0 {
		b.add("t.priority = ANY(" + b.arg(f.Priority) + ")")
	}
	if len(f.Severity) > 0 {
		clause := "t.severity = ANY(" + b.arg(f.Severity) + ")"
		if slices.Contains(f.Severity, "none") {
			clause = "(" + clause + " OR t.severity IS NULL)"
		}
		b.add(clause)
	}
	switch f.Assignee {
	case "":
	case "none":
		b.add("t.assignee_id IS NULL")
	case "me":
		if me == nil {
			b.add("false")
		} else {
			b.add("t.assignee_id = " + b.arg(*me))
		}
	default:
		id, err := hd.ParseRef(f.Assignee, "assignee")
		if err != nil {
			return err
		}
		b.add("t.assignee_id = " + b.arg(id))
	}
	if strings.TrimSpace(f.TeamID) != "" {
		clause, err := teamClause(b, me, f.TeamID, "team_id")
		if err != nil {
			return err
		}
		b.add(clause)
	}
	if strings.TrimSpace(f.TeamNot) != "" {
		clause, err := teamClause(b, me, f.TeamNot, "team_not")
		if err != nil {
			return err
		}
		// A ticket without a team is in none of the listed teams.
		b.add("NOT COALESCE(" + clause + ", false)")
	}
	for _, ref := range []struct{ v, col string }{{f.MailboxID, "t.mailbox_id"}, {f.ContactID, "t.contact_id"},
		{f.BrandID, "t.brand_id"}, {f.WidgetID, "t.widget_id"}} {
		if ref.v == "" {
			continue
		}
		id, err := hd.ParseRef(ref.v, strings.TrimPrefix(ref.col, "t."))
		if err != nil {
			return err
		}
		b.add(ref.col + " = " + b.arg(id))
	}
	boxes, err := refList(f.MailboxNot, "mailbox_not")
	if err != nil {
		return err
	}
	if len(boxes) > 0 {
		b.add("(t.mailbox_id IS NULL OR NOT t.mailbox_id = ANY(" + b.arg(boxes) + "::uuid[]))")
	}
	contacts, err := refList(f.ContactNot, "contact_not")
	if err != nil {
		return err
	}
	if len(contacts) > 0 {
		// A ticket without a contact is from none of them.
		b.add("(t.contact_id IS NULL OR NOT t.contact_id = ANY(" + b.arg(contacts) + "::uuid[]))")
	}
	companies, err := refList(f.CompanyNot, "company_not")
	if err != nil {
		return err
	}
	if len(companies) > 0 {
		// A ticket without a contact, or whose contact has no company, is from none of them.
		b.add(`NOT EXISTS (SELECT 1 FROM contacts fc WHERE fc.workspace_id = t.workspace_id AND fc.id = t.contact_id
			AND fc.company_id = ANY(` + b.arg(companies) + `::uuid[]))`)
	}
	if a := strings.ToLower(strings.TrimSpace(f.ReceivedAt)); a != "" {
		b.add("t.received_at = " + b.arg(a))
	}
	for _, l := range []struct {
		raw, field, op string
	}{{f.Tag, "tag", "EXISTS"}, {f.TagNot, "tag_not", "NOT EXISTS"}} {
		names, err := hd.SplitLabels(l.raw, l.field)
		if err != nil {
			return err
		}
		if len(names) > 0 {
			b.add(l.op + ` (SELECT 1 FROM ticket_tags ftt JOIN tags fg ON fg.id = ftt.tag_id
				WHERE ftt.ticket_id = t.id AND fg.workspace_id = t.workspace_id
				AND lower(fg.name) IN (SELECT lower(n) FROM unnest(` + b.arg(names) + `::text[]) n))`)
		}
	}
	if f.Untagged {
		b.add(`NOT EXISTS (SELECT 1 FROM ticket_tags ftt JOIN tags fg ON fg.id = ftt.tag_id
			WHERE ftt.ticket_id = t.id AND fg.workspace_id = t.workspace_id)`)
	}
	if f.Channel != "" {
		b.add("t.channel = " + b.arg(f.Channel))
	}
	if len(f.ChannelNot) > 0 {
		b.add("NOT t.channel = ANY(" + b.arg(f.ChannelNot) + "::text[])")
	}
	for _, ff := range f.Fields {
		def, ok := f.Defs[ff.Key]
		if !ok {
			return httpx.Validation("there is no custom field " + strconv.Quote(hd.Truncate(ff.Key, 40)))
		}
		b.add(hd.FieldClause(def, ff, "t.fields", b.arg, me))
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		var parts []string
		if m := numberRef.FindStringSubmatch(q); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			parts = append(parts, "t.number = "+b.arg(n))
		}
		tsq := "websearch_to_tsquery('simple', " + b.arg(q) + ")"
		// The customer's name, email and phone (contacts.search) match too.
		contact := `EXISTS (SELECT 1 FROM contacts fc WHERE fc.workspace_id = t.workspace_id AND fc.id = t.contact_id AND (fc.search @@ ` + tsq
		parts = append(parts, "t.search @@ "+tsq, "t.fields_search @@ "+tsq,
			`EXISTS (SELECT 1 FROM messages fm WHERE fm.workspace_id = t.workspace_id AND fm.ticket_id = t.id AND fm.deleted_at IS NULL AND fm.search @@ `+tsq+`)`)
		if pq := prefixQuery(q); pq != "" {
			ptsq := "to_tsquery('simple', " + b.arg(pq) + ")"
			parts = append(parts, "t.search @@ "+ptsq, "t.fields_search @@ "+ptsq)
			contact += " OR fc.search @@ " + ptsq
		}
		parts = append(parts, contact+"))")
		b.add("(" + strings.Join(parts, " OR ") + ")")
	}
	return nil
}

// refList parses a comma-separated list of ids, at most 50.
func refList(raw, field string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, v := range hd.SplitCSV(raw) {
		id, err := hd.ParseRef(v, field)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if len(out) > 50 {
		return nil, httpx.Validation(field + " takes at most 50 values")
	}
	return out, nil
}

// teamClause is the predicate for tickets of any of a comma-separated list of team ids,
// mine (the caller's teams) and none. It is NULL for a ticket without a team unless none
// is listed.
func teamClause(b *sqlBuilder, me *uuid.UUID, raw, field string) (string, error) {
	var ors []string
	var teams []uuid.UUID
	parts := strings.Split(raw, ",")
	if len(parts) > 50 {
		return "", httpx.Validation(field + " takes at most 50 values")
	}
	for _, part := range parts {
		switch v := strings.TrimSpace(part); v {
		case "":
		case hd.TeamNone:
			ors = append(ors, "t.team_id IS NULL")
		case hd.TeamMine:
			if me != nil {
				ors = append(ors, "t.team_id IN (SELECT mt.team_id FROM team_members mt WHERE mt.user_id = "+b.arg(*me)+")")
			}
		default:
			id, err := hd.ParseRef(v, field)
			if err != nil {
				return "", err
			}
			if !slices.Contains(teams, id) {
				teams = append(teams, id)
			}
		}
	}
	if len(teams) > 0 {
		ors = append(ors, "t.team_id = ANY("+b.arg(teams)+"::uuid[])")
	}
	if len(ors) == 0 {
		return "false", nil
	}
	return "(" + strings.Join(ors, " OR ") + ")", nil
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// prefixQuery turns "refu ord" into "refu:* & ord:*" so partial words match subjects.
func prefixQuery(q string) string {
	words := wordRe.FindAllString(strings.ToLower(q), 8)
	if len(words) == 0 {
		return ""
	}
	for i, w := range words {
		words[i] = w + ":*"
	}
	return strings.Join(words, " & ")
}

// Sort keys.
const dueExpr = `COALESCE(CASE WHEN t.sla_policy_id IS NULL THEN NULL
	WHEN t.first_responded_at IS NULL THEN LEAST(t.first_response_due_at, t.resolution_due_at)
	ELSE t.resolution_due_at END, 'infinity'::timestamptz)`

const priorityExpr = `(CASE t.priority WHEN 'urgent' THEN 4 WHEN 'high' THEN 3 WHEN 'normal' THEN 2 ELSE 1 END)`

var priorityRank = map[string]int{"urgent": 4, "high": 3, "normal": 2, "low": 1}

const severityExpr = `(CASE t.severity WHEN 'sev1' THEN 4 WHEN 'sev2' THEN 3 WHEN 'sev3' THEN 2 WHEN 'sev4' THEN 1 ELSE 0 END)`

type sortSpec struct {
	expr string
	cast string
	desc bool
	key  func(domain.Ticket) any
}

func parseSort(sort, order string, defs domain.FieldDefs, b *sqlBuilder) (sortSpec, error) {
	var s sortSpec
	if key, ok := strings.CutPrefix(sort, "field."); ok {
		f, ok := defs[key]
		if !ok || !hd.FieldSortable(f.Type) {
			return s, httpx.Validation("sort field." + hd.Truncate(key, 40) + " must be a text, number, date, checkbox or select custom field")
		}
		expr, cast := hd.FieldSortExpr(f, "t.fields", b.arg)
		s = sortSpec{expr: expr, cast: cast, desc: true, key: func(t domain.Ticket) any { return fieldSortKey(f, t.Fields[f.Key]) }}
		return s, applyOrder(&s, order)
	}
	switch sort {
	case "", "last_message_at":
		s = sortSpec{expr: "t.last_message_at", cast: "timestamptz", desc: true,
			key: func(t domain.Ticket) any { return t.LastMessageAt.UTC().Format(time.RFC3339Nano) }}
	case "created_at":
		s = sortSpec{expr: "t.created_at", cast: "timestamptz", desc: true,
			key: func(t domain.Ticket) any { return t.CreatedAt.UTC().Format(time.RFC3339Nano) }}
	case "priority":
		s = sortSpec{expr: priorityExpr, cast: "int", desc: true, key: func(t domain.Ticket) any { return priorityRank[t.Priority] }}
	case "severity":
		s = sortSpec{expr: severityExpr, cast: "int", desc: true, key: func(t domain.Ticket) any { return domain.SeverityRank(t.Severity) }}
	case "due":
		s = sortSpec{expr: dueExpr, cast: "timestamptz", desc: false, key: func(t domain.Ticket) any {
			if d := ticketDue(t); d != nil {
				return d.UTC().Format(time.RFC3339Nano)
			}
			return "infinity"
		}}
	default:
		return s, httpx.Validation("sort must be last_message_at, created_at, priority, severity, due or field.<key>")
	}
	return s, applyOrder(&s, order)
}

func applyOrder(s *sortSpec, order string) error {
	switch order {
	case "":
	case "asc":
		s.desc = false
	case "desc":
		s.desc = true
	default:
		return httpx.Validation("order must be asc or desc")
	}
	return nil
}

// fieldSortKey mirrors hd.FieldSortExpr for the cursor.
func fieldSortKey(f domain.CustomField, v any) any {
	switch f.Type {
	case domain.FieldNumber:
		if n, ok := v.(float64); ok {
			return strconv.FormatFloat(n, 'g', -1, 64)
		}
		return "-Infinity"
	case domain.FieldCheckbox:
		if b, _ := v.(bool); b {
			return 1
		}
		return 0
	case domain.FieldSelect:
		s, _ := v.(string)
		for i, o := range f.Options {
			if o.ID == s {
				return i + 1
			}
		}
		return 0
	case domain.FieldDate:
		s, _ := v.(string)
		return s
	}
	s, _ := v.(string)
	return strings.ToLower(s)
}

// ticketDue mirrors dueExpr: the next SLA deadline that still matters.
func ticketDue(t domain.Ticket) *time.Time {
	if t.SLA == nil {
		return nil
	}
	if t.FirstRespondedAt == nil {
		a, b := t.SLA.FirstResponseDueAt, t.SLA.ResolutionDueAt
		switch {
		case a == nil:
			return b
		case b == nil:
			return a
		case a.Before(*b):
			return a
		default:
			return b
		}
	}
	return t.SLA.ResolutionDueAt
}

type ticketCursor struct {
	K  json.RawMessage `json:"k"`
	ID uuid.UUID       `json:"id"`
}

// listTickets runs a filtered, sorted, keyset-paginated ticket query.
func listTickets(ctx context.Context, q db.Querier, wid uuid.UUID, me *uuid.UUID, f listQuery, sort, order string, limit int, cursor string) (httpx.Page[domain.Ticket], error) {
	var page httpx.Page[domain.Ticket]
	var err error
	if f, err = prepareListQuery(ctx, q, wid, f, sort); err != nil {
		return page, err
	}
	b := &sqlBuilder{}
	if err := applyFilters(b, wid, me, f); err != nil {
		return page, err
	}
	s, err := parseSort(sort, order, f.Defs, b)
	if err != nil {
		return page, err
	}
	cmp, dir := "<", "DESC"
	if !s.desc {
		cmp, dir = ">", "ASC"
	}
	if cursor != "" {
		var c ticketCursor
		if err := httpx.DecodeCursor(cursor, &c); err != nil {
			return page, err
		}
		var k any
		if err := json.Unmarshal(c.K, &k); err != nil || k == nil {
			return page, httpx.BadRequest("invalid cursor")
		}
		if s.cast == "int" {
			if _, ok := k.(float64); !ok {
				return page, httpx.BadRequest("invalid cursor")
			}
			k = int(k.(float64))
		} else if str, ok := k.(string); !ok {
			return page, httpx.BadRequest("invalid cursor")
		} else if s.cast == "float8" {
			if _, err := strconv.ParseFloat(str, 64); err != nil && str != "-Infinity" {
				return page, httpx.BadRequest("invalid cursor")
			}
		} else if s.cast == "text" {
		} else if str != "infinity" {
			if _, err := time.Parse(time.RFC3339Nano, str); err != nil {
				return page, httpx.BadRequest("invalid cursor")
			}
		}
		b.add(fmt.Sprintf("(%s, t.id) %s (%s::%s, %s::uuid)", s.expr, cmp, b.arg(k), s.cast, b.arg(c.ID)))
	}
	sql := domain.TicketSelect + b.sql() + fmt.Sprintf(" ORDER BY %s %s, t.id %s LIMIT %d", s.expr, dir, dir, limit+1)
	rows, err := q.Query(ctx, sql, b.args...)
	if err != nil {
		return page, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Ticket, error) { return domain.ScanTicket(r) })
	if err != nil {
		return page, err
	}
	return httpx.NewPage(list, limit, func(t domain.Ticket) any {
		raw, _ := json.Marshal(s.key(t))
		return ticketCursor{K: raw, ID: t.ID}
	}), nil
}

// filteredTicketIDs finds every ticket that matches f for this caller. It is used by
// bulk actions so the same filters and private-team visibility as the inbox apply.
func filteredTicketIDs(ctx context.Context, q db.Querier, wid uuid.UUID, me *uuid.UUID, f listQuery) ([]uuid.UUID, error) {
	var err error
	if f, err = prepareListQuery(ctx, q, wid, f, ""); err != nil {
		return nil, err
	}
	b := &sqlBuilder{}
	if err := applyFilters(b, wid, me, f); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, "SELECT t.id FROM tickets t"+b.sql()+" ORDER BY t.id", b.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// loadView returns the filters of a view the caller can see.
func loadView(ctx context.Context, q db.Querier, p *auth.Principal, id string) (hd.TicketFilters, error) {
	var f hd.TicketFilters
	vid, err := hd.ParseRef(id, "view_id")
	if err != nil {
		return f, err
	}
	var raw []byte
	err = q.QueryRow(ctx, `SELECT filters FROM views v WHERE v.workspace_id = $1 AND v.id = $2 AND `+hd.ViewVisibleSQL("v", 3),
		p.WorkspaceID, vid, p.ActorUserID()).Scan(&raw)
	if db.IsNotFound(err) {
		return f, httpx.Validation("view not found")
	}
	if err != nil {
		return f, err
	}
	_ = json.Unmarshal(raw, &f)
	return f, nil
}

// mergeFilters overlays explicitly requested filters on a view's filters.
func mergeFilters(view, req hd.TicketFilters) hd.TicketFilters {
	out := view
	if len(req.Status) > 0 {
		out.Status = req.Status
	}
	if len(req.Priority) > 0 {
		out.Priority = req.Priority
	}
	if len(req.Severity) > 0 {
		out.Severity = req.Severity
	}
	if len(req.ChannelNot) > 0 {
		out.ChannelNot = req.ChannelNot
	}
	for _, f := range []struct{ dst, src *string }{{&out.Assignee, &req.Assignee}, {&out.TeamID, &req.TeamID}, {&out.TeamNot, &req.TeamNot},
		{&out.MailboxID, &req.MailboxID}, {&out.MailboxNot, &req.MailboxNot}, {&out.ContactNot, &req.ContactNot}, {&out.CompanyNot, &req.CompanyNot},
		{&out.Tag, &req.Tag}, {&out.TagNot, &req.TagNot},
		{&out.Channel, &req.Channel}, {&out.Q, &req.Q}, {&out.BrandID, &req.BrandID}, {&out.WidgetID, &req.WidgetID},
		{&out.ReceivedAt, &req.ReceivedAt}} {
		if *f.src != "" {
			*f.dst = *f.src
		}
	}
	out.Untagged = view.Untagged || req.Untagged
	// Field filters add up: the view's and the request's all apply.
	out.Fields = append(slices.Clone(view.Fields), req.Fields...)
	return out
}

// resolveTicket finds a ticket by uuid, "#123" or "123" in the workspace.
func resolveTicket(ctx context.Context, q db.Querier, wid uuid.UUID, ref string) (uuid.UUID, error) {
	ref = strings.TrimSpace(ref)
	if id, err := uuid.Parse(ref); err == nil {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tickets WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL)`,
			wid, id).Scan(&exists); err != nil {
			return uuid.Nil, err
		}
		if !exists {
			return uuid.Nil, httpx.NotFound("Ticket")
		}
		return id, nil
	}
	m := numberRef.FindStringSubmatch(ref)
	if m == nil {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM tickets WHERE workspace_id = $1 AND number = $2 AND deleted_at IS NULL`, wid, n).Scan(&id)
	if db.IsNotFound(err) {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	return id, err
}

// resolveVisible is resolveTicket for a caller: a ticket of a private team the caller may
// not see is not found, like a missing one.
func resolveVisible(ctx context.Context, q db.Querier, p *auth.Principal, ref string) (uuid.UUID, error) {
	id, err := resolveTicket(ctx, q, p.WorkspaceID, ref)
	if err != nil {
		return id, err
	}
	var team, assignee *uuid.UUID
	if err := q.QueryRow(ctx, `SELECT team_id, assignee_id FROM tickets WHERE workspace_id = $1 AND id = $2`,
		p.WorkspaceID, id).Scan(&team, &assignee); err != nil {
		return id, err
	}
	ok, err := domain.CanSee(ctx, q, p, team, assignee)
	if err != nil {
		return id, err
	}
	if !ok {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	return id, nil
}

// Counts is the sidebar badge set.
type TicketCounts struct {
	Unassigned int            `json:"unassigned"`
	Mine       int            `json:"mine"`
	Open       int            `json:"open"`
	Pending    int            `json:"pending"`
	OnHold     int            `json:"on_hold"`
	Solved     int            `json:"solved"`
	Spam       int            `json:"spam"`
	Views      map[string]int `json:"views"`
	// Teams counts every team the caller can see; MyTeams the tickets of the caller's teams.
	Teams   map[string]TeamCount `json:"teams" doc:"Every team the caller can see"`
	MyTeams TeamCount            `json:"my_teams" doc:"Tickets of the caller's teams"`
	// Stats breaks each sidebar row's tickets down, keyed by row id: ticket_folder:<unassigned|mine|open|pending>,
	// team:<id>, team:mine and view:<id>, over the same tickets as the row's count.
	Stats map[string]TicketStats `json:"stats" doc:"Per sidebar row: awaiting a reply, urgent and overdue tickets"`
}

// TeamCount counts a team's open and pending tickets (spam excluded).
type TeamCount struct {
	Open       int `json:"open"`
	Unassigned int `json:"unassigned"`
}

// TicketStats are the numbers a sidebar row can show besides its count.
type TicketStats struct {
	Awaiting int `json:"awaiting" doc:"Open or pending, and the customer wrote last"`
	Urgent   int `json:"urgent" doc:"Urgent priority"`
	Overdue  int `json:"overdue" doc:"Not solved or closed, past its due time or with an SLA breach recorded"`
}

// statCols are the three TicketStats counts over the tickets matching cond, for columns of
// alias a ("t." or ""): awaiting (the customer wrote last, as the inbox list shows it),
// urgent, and overdue (the due time that applies has passed, or an SLA breach is recorded).
func statCols(a, cond string) string {
	return fmt.Sprintf(`count(*) FILTER (WHERE (%[2]s) AND %[1]sstatus IN ('open','pending')
			AND %[1]slast_customer_message_at >= %[1]slast_message_at - interval '1 second'),
		count(*) FILTER (WHERE (%[2]s) AND %[1]spriority = 'urgent'),
		count(*) FILTER (WHERE (%[2]s) AND %[1]sstatus NOT IN ('solved','closed') AND (
			CASE WHEN %[1]sfirst_responded_at IS NULL THEN %[1]sfirst_response_due_at ELSE %[1]sresolution_due_at END < now()
			OR %[1]ssla_first_response_breached_at IS NOT NULL OR %[1]ssla_resolution_breached_at IS NOT NULL))`, a, cond)
}

func (s *TicketStats) dest() []any { return []any{&s.Awaiting, &s.Urgent, &s.Overdue} }

func ticketCounts(ctx context.Context, q db.Querier, p *auth.Principal) (TicketCounts, error) {
	c := TicketCounts{Views: map[string]int{}, Teams: map[string]TeamCount{}, Stats: map[string]TicketStats{}}
	const (
		unassigned = `NOT spam AND status IN ('open','pending','on_hold') AND assignee_id IS NULL`
		mine       = `NOT spam AND status IN ('open','pending','on_hold') AND assignee_id = $2`
		open       = `NOT spam AND status = 'open'`
		pending    = `NOT spam AND status IN ('pending','on_hold')`
		myTeams    = `NOT spam AND status IN ('open','pending') AND team_id IN (SELECT mt.team_id FROM team_members mt WHERE mt.user_id = $2)`
	)
	var sUnassigned, sMine, sOpen, sPending, sMyTeams TicketStats
	dest := []any{&c.Unassigned, &c.Mine, &c.Open, &c.Pending, &c.OnHold, &c.Spam, &c.MyTeams.Open, &c.MyTeams.Unassigned}
	for _, s := range []*TicketStats{&sUnassigned, &sMine, &sOpen, &sPending, &sMyTeams} {
		dest = append(dest, s.dest()...)
	}
	// Folder totals count only tickets the caller may see: a private team's are left out
	// unless they are on it or the ticket is theirs, as in the lists.
	hidden, err := domain.HiddenTeams(ctx, q, p)
	if err != nil {
		return c, err
	}
	// Every folder but Solved and Spam holds open, pending and on-hold tickets, read here through
	// the status index. Spam counts spam that is not closed: these tickets' and the solved ones'.
	err = q.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE `+unassigned+`),
		count(*) FILTER (WHERE `+mine+`),
		count(*) FILTER (WHERE `+open+`),
		count(*) FILTER (WHERE NOT spam AND status = 'pending'),
		count(*) FILTER (WHERE NOT spam AND status = 'on_hold'),
		count(*) FILTER (WHERE spam),
		count(*) FILTER (WHERE `+myTeams+`),
		count(*) FILTER (WHERE `+myTeams+` AND assignee_id IS NULL),
		`+statCols("", unassigned)+`,
		`+statCols("", mine)+`,
		`+statCols("", open)+`,
		`+statCols("", pending)+`,
		`+statCols("", myTeams)+`
		FROM tickets WHERE workspace_id = $1 AND deleted_at IS NULL AND status IN ('open','pending','on_hold')
			AND `+domain.VisibleSQLUser("tickets", 3, 2), p.WorkspaceID, p.ActorUserID(), hidden).
		Scan(dest...)
	if err != nil {
		return c, err
	}
	// Solved tickets are most of a workspace's: their counts read tickets_ws_solved_count_idx alone.
	var solvedSpam int
	err = q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT spam), count(*) FILTER (WHERE spam)
		FROM tickets WHERE workspace_id = $1 AND deleted_at IS NULL AND status = 'solved'
			AND `+domain.VisibleSQLUser("tickets", 3, 2), p.WorkspaceID, p.ActorUserID(), hidden).
		Scan(&c.Solved, &solvedSpam)
	if err != nil {
		return c, err
	}
	c.Spam += solvedSpam
	c.Stats["ticket_folder:unassigned"] = sUnassigned
	c.Stats["ticket_folder:mine"] = sMine
	c.Stats["ticket_folder:open"] = sOpen
	c.Stats["ticket_folder:pending"] = sPending
	c.Stats["team:mine"] = sMyTeams
	rows, err := q.Query(ctx, `SELECT tm.id,
		count(t.id) FILTER (WHERE t.id IS NOT NULL), count(t.id) FILTER (WHERE t.assignee_id IS NULL),
		`+statCols("t.", "t.id IS NOT NULL")+`
		FROM teams tm LEFT JOIN tickets t ON t.workspace_id = tm.workspace_id AND t.team_id = tm.id AND t.deleted_at IS NULL
			AND NOT t.spam AND t.status IN ('open', 'pending')
		WHERE tm.workspace_id = $1 AND NOT tm.id = ANY($2::uuid[]) GROUP BY tm.id`, p.WorkspaceID, hidden)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var id uuid.UUID
		var tc TeamCount
		var st TicketStats
		if err := rows.Scan(append([]any{&id, &tc.Open, &tc.Unassigned}, st.dest()...)...); err != nil {
			rows.Close()
			return c, err
		}
		c.Teams[id.String()] = tc
		c.Stats["team:"+id.String()] = st
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	rows, err = q.Query(ctx, `SELECT v.id, v.filters FROM views v WHERE v.workspace_id = $1 AND `+hd.ViewVisibleSQL("v", 2)+`
		ORDER BY v.position, v.name LIMIT 100`, p.WorkspaceID, p.ActorUserID())
	if err != nil {
		return c, err
	}
	type view struct {
		id uuid.UUID
		f  hd.TicketFilters
	}
	var views []view
	for rows.Next() {
		var v view
		var raw []byte
		if err := rows.Scan(&v.id, &raw); err != nil {
			rows.Close()
			return c, err
		}
		_ = json.Unmarshal(raw, &v.f)
		views = append(views, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	var defs domain.FieldDefs
	for _, v := range views {
		if len(v.f.Fields) > 0 && defs == nil {
			if defs, err = domain.LoadFieldDefs(ctx, q, p.WorkspaceID, domain.FieldEntityTicket); err != nil {
				return c, err
			}
		}
		lq := listQuery{TicketFilters: v.f, Defs: defs, Hidden: hidden}
		if len(v.f.Fields) > 0 {
			if lq.Fields, err = hd.NormalizeFieldFilters(defs, v.f.Fields); err != nil {
				continue // a view whose filters no longer parse counts as zero
			}
		}
		b := &sqlBuilder{}
		if err := applyFilters(b, p.WorkspaceID, p.ActorUserID(), lq); err != nil {
			continue // a view whose filters no longer parse counts as zero
		}
		var n int
		var st TicketStats
		if err := q.QueryRow(ctx, `SELECT count(*), `+statCols("t.", "true")+` FROM tickets t`+b.sql(), b.args...).
			Scan(append([]any{&n}, st.dest()...)...); err != nil {
			return c, err
		}
		c.Views[v.id.String()] = n
		c.Stats["view:"+v.id.String()] = st
	}
	return c, nil
}

// summaryCap is the most tickets GET /v1/tickets/summary counts: past it the list is a
// workspace's history (Solved, a view of every status) and counting it would read it all.
var summaryCap = 10000

// TicketListSummary describes a whole ticket list, every page of it (GET /v1/tickets/summary).
type TicketListSummary struct {
	Complete bool `json:"complete" doc:"false when the list was not counted: more tickets match than the summary counts, or it is a full-text search (q). The counts are then 0"`
	Total    int  `json:"total" doc:"Tickets in the list"`
	Awaiting int  `json:"awaiting" doc:"Open or pending, and the customer wrote last"`
	Urgent   int  `json:"urgent" doc:"Urgent priority"`
	Overdue  int  `json:"overdue" doc:"Not solved or closed, past its due time or with an SLA breach recorded"`
	DueSoon  int  `json:"due_soon" doc:"Not solved, closed or overdue, and its due time is within the hour"`
}

// summaryCols are the columns statCols and summaryDueSoon read.
const summaryCols = `t.status, t.priority, t.last_message_at, t.last_customer_message_at, t.first_responded_at,
	t.first_response_due_at, t.resolution_due_at, t.sla_first_response_breached_at, t.sla_resolution_breached_at`

// summaryDueSoon is the due time that applies (as in statCols) falling within the next hour,
// with no breach recorded: never an overdue ticket.
const summaryDueSoon = `t.status NOT IN ('solved','closed')
	AND t.sla_first_response_breached_at IS NULL AND t.sla_resolution_breached_at IS NULL
	AND CASE WHEN t.first_responded_at IS NULL THEN t.first_response_due_at ELSE t.resolution_due_at END >= now()
	AND CASE WHEN t.first_responded_at IS NULL THEN t.first_response_due_at ELSE t.resolution_due_at END < now() + interval '1 hour'`

// ticketListSummary counts the tickets listTickets pages through for f, with the same filters
// and visibility. The tickets are read through the list's own WHERE, without its ORDER BY, and
// at most summaryCap+1 of them: one more means the list is too long to count, and the summary
// is left incomplete. A full-text search is never counted: its matches are read message by message.
func ticketListSummary(ctx context.Context, q db.Querier, wid uuid.UUID, me *uuid.UUID, f listQuery) (TicketListSummary, error) {
	var s TicketListSummary
	var err error
	if f, err = prepareListQuery(ctx, q, wid, f, ""); err != nil {
		return s, err
	}
	b := &sqlBuilder{}
	if err := applyFilters(b, wid, me, f); err != nil {
		return s, err
	}
	if strings.TrimSpace(f.Q) != "" {
		return s, nil
	}
	var st TicketStats
	dest := append(append([]any{&s.Total}, st.dest()...), &s.DueSoon)
	err = q.QueryRow(ctx, `SELECT count(*), `+statCols("t.", "true")+`, count(*) FILTER (WHERE `+summaryDueSoon+`)
		FROM (SELECT `+summaryCols+` FROM tickets t`+b.sql()+` LIMIT `+strconv.Itoa(summaryCap+1)+`) t`, b.args...).Scan(dest...)
	if err != nil {
		return s, err
	}
	if s.Total > summaryCap {
		return TicketListSummary{}, nil
	}
	s.Complete, s.Awaiting, s.Urgent, s.Overdue = true, st.Awaiting, st.Urgent, st.Overdue
	return s, nil
}
