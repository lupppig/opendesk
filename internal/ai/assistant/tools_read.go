package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

func readTools() []*tool {
	return []*tool{
		{
			Name: "search_tickets",
			Description: "Find tickets by text, status, priority, assignee, team, label, channel, creation time or SLA state. " +
				"Returns up to `limit` tickets (newest activity first, or newest created when a time filter is set) and how many matched.",
			Schema:  obj(merge(ticketFilterProps(), map[string]any{"limit": integer("How many tickets to return (default 20).", 1, 50)})),
			MinRole: auth.RoleViewer, Active: "Searching tickets", Done: "Searched tickets",
			Read: searchTickets,
		},
		{
			Name:        "get_ticket",
			Description: "Read one ticket: its fields, team, brand, the address the customer wrote to and the one replies go out from, SLA, labels and the latest messages and notes of its thread.",
			Schema:      obj(map[string]any{"ticket": str("The ticket number like #123, or its id.")}, "ticket"),
			MinRole:     auth.RoleViewer, Active: "Reading the ticket", Done: "Read the ticket",
			Read: getTicket,
		},
		{
			Name:        "search_contacts",
			Description: "Find customers (contacts) by name, email, phone or custom field value.",
			Schema:      obj(map[string]any{"query": str("What to search for.")}, "query"),
			MinRole:     auth.RoleViewer, Active: "Searching contacts", Done: "Searched contacts",
			Read: searchContacts,
		},
		{
			Name:        "search_kb",
			Description: "Search knowledge base articles (published and drafts) by text.",
			Schema:      obj(map[string]any{"query": str("What to search for."), "status": enum("Only drafts or only published.", "draft", "published")}, "query"),
			MinRole:     auth.RoleViewer, Active: "Searching the knowledge base", Done: "Searched the knowledge base",
			Read: searchKB,
		},
		{
			Name: "search_wiki",
			Description: "Search the team's internal wiki (runbooks, policies, escalation steps, how-tos) by text. " +
				"Returns matching pages with where they sit and a passage; read the best one with get_wiki_page before answering from it.",
			Schema:  obj(map[string]any{"query": str("What to search for.")}, "query"),
			MinRole: auth.RoleViewer, Active: "Searching the wiki", Done: "Searched the wiki",
			Read: searchWiki,
		},
		{
			Name:        "get_wiki_page",
			Description: "Read one wiki page: its text, where it sits and its subpages.",
			Schema:      obj(map[string]any{"page": str("The page id from search_wiki.")}, "page"),
			MinRole:     auth.RoleViewer, Active: "Reading the wiki", Done: "Read a wiki page",
			Read: getWikiPage,
		},
		{
			Name:        "list_views",
			Description: "List the saved views the user can see with their ticket counts, the inbox folder counts (unassigned, mine, by status) and the teams (queues).",
			Schema:      obj(map[string]any{}),
			MinRole:     auth.RoleViewer, Active: "Reading views and queues", Done: "Read views and queues",
			Read: listViews,
		},
		{
			Name: "get_report",
			Description: "Helpdesk stats for a date range: tickets created and solved, open now, median first response and resolution times, " +
				"SLA breaches, CSAT, by channel and by agent; plus the open tickets breaching or due on SLA right now.",
			Schema: obj(map[string]any{
				"from": str("Start day YYYY-MM-DD (default 30 days ago)."),
				"to":   str("End day YYYY-MM-DD, inclusive (default today)."),
			}),
			MinRole: auth.RoleViewer, Active: "Reading reports", Done: "Read reports",
			Read: getReport,
		},
		{
			Name:        "list_labels_and_fields",
			Description: "List the workspace's ticket labels and custom fields (keys, types and options), to use exact names in changes.",
			Schema:      obj(map[string]any{}),
			MinRole:     auth.RoleViewer, Active: "Reading labels and fields", Done: "Read labels and fields",
			Read: listLabelsAndFields,
		},
		{
			Name:        "list_team",
			Description: "List teammates with their role and current workload (open, pending and urgent tickets assigned), and the teams.",
			Schema:      obj(map[string]any{}),
			MinRole:     auth.RoleViewer, Active: "Checking the team", Done: "Checked the team",
			Read: listTeam,
		},
	}
}

func merge(a, b map[string]any) map[string]any {
	for k, v := range b {
		a[k] = v
	}
	return a
}

func searchTickets(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	type args struct {
		ticketFilter
		Limit int `json:"limit"`
	}
	a, err := decode[args](in)
	if err != nil {
		return output{}, err
	}
	if err := validEnum(a.Status, statuses, "status"); err != nil {
		return output{}, err
	}
	if err := validEnum(a.Priority, priorities, "priority"); err != nil {
		return output{}, err
	}
	limit := a.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	ts, matched, err := rc.findTickets(ctx, a.ticketFilter, limit, 500)
	if err != nil {
		return output{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d tickets matched; showing %d.\n", matched, len(ts))
	out := output{Summary: "Found " + plural(matched, "ticket", "tickets")}
	for _, t := range ts {
		b.WriteString(t.line() + "\n")
		out.Refs = append(out.Refs, t.ref())
	}
	out.Content = block("search_tickets", b.String())
	return out, nil
}

func getTicket(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		Ticket string `json:"ticket"`
	}](in)
	if err != nil {
		return output{}, err
	}
	t, err := rc.getTicket(ctx, a.Ticket)
	if err != nil {
		return output{}, err
	}
	rc.replyAddress(ctx, &t)
	var msgs struct {
		Data []domain.Message `json:"data"`
	}
	if err := rc.read.get(ctx, "/v1/tickets/"+t.ID+"/messages", nil, &msgs); err != nil {
		return output{}, err
	}
	var b strings.Builder
	b.WriteString(t.line() + "\n")
	if len(t.Fields) > 0 {
		raw, _ := json.Marshal(t.Fields)
		b.WriteString("custom fields: " + string(raw) + "\n")
	}
	if s, ok := t.Custom["ai_summary"].(map[string]any); ok {
		if text, ok := s["text"].(string); ok && text != "" {
			b.WriteString("stored summary: " + clip(text, 600) + "\n")
		}
	}
	// Deleted messages and notes sent as replies stay out, whoever asks (admins read them
	// in the app, never through Ollie).
	list := slices.DeleteFunc(msgs.Data, func(m domain.Message) bool { return m.DeletedAt != nil || m.SentAsReplyID != nil })
	if len(list) > 12 {
		fmt.Fprintf(&b, "(%d earlier messages not shown)\n", len(list)-12)
		list = list[len(list)-12:]
	}
	budget := 9000
	for _, m := range list {
		who := "agent"
		switch {
		case m.Author.Type == "contact":
			who = "customer"
			if m.Author.Contact != nil {
				who = "customer " + m.Author.Contact.Name
			}
		case m.Author.User != nil:
			who = "agent " + m.Author.User.Name
		case m.Author.Type == "system":
			who = "system"
		}
		kind := "message"
		if m.Kind == "note" {
			kind = "internal note"
		}
		text := clip(m.BodyText, min(budget, 2500))
		budget -= len(text)
		fmt.Fprintf(&b, "\n--- %s from %s at %s\n%s\n", kind, who, m.CreatedAt.UTC().Format(time.RFC3339), text)
		if budget <= 0 {
			break
		}
	}
	return output{Content: block("get_ticket", b.String()), Summary: fmt.Sprintf("Read #%d", t.Number), Refs: []Ref{t.ref()}}, nil
}

func searchContacts(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		Query string `json:"query"`
	}](in)
	if err != nil {
		return output{}, err
	}
	var pg page[struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Email       *string `json:"email"`
		Phone       string  `json:"phone"`
		TicketCount int     `json:"ticket_count"`
		Company     *struct {
			Name string `json:"name"`
		} `json:"company"`
	}]
	q := url.Values{"q": {clip(a.Query, 200)}, "limit": {"10"}}
	if err := rc.read.get(ctx, "/v1/contacts", q, &pg); err != nil {
		return output{}, err
	}
	var b strings.Builder
	out := output{Summary: "Found " + plural(len(pg.Data), "contact", "contacts")}
	for _, c := range pg.Data {
		email := ""
		if c.Email != nil {
			email = *c.Email
		}
		company := ""
		if c.Company != nil {
			company = "; company " + c.Company.Name
		}
		fmt.Fprintf(&b, "%s <%s>%s; %d tickets; id %s\n", c.Name, email, company, c.TicketCount, c.ID)
		label := c.Name
		if label == "" {
			label = email
		}
		out.Refs = append(out.Refs, Ref{Kind: "contact", ID: c.ID, Label: label})
	}
	if len(pg.Data) == 0 {
		b.WriteString("No contacts matched.")
	}
	out.Content = block("search_contacts", b.String())
	return out, nil
}

func searchKB(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		Query  string `json:"query"`
		Status string `json:"status"`
	}](in)
	if err != nil {
		return output{}, err
	}
	q := url.Values{"q": {clip(a.Query, 200)}, "limit": {"10"}}
	if a.Status == "draft" || a.Status == "published" {
		q.Set("status", a.Status)
	}
	var pg page[struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Status  string `json:"status"`
		Excerpt string `json:"excerpt"`
	}]
	if err := rc.read.get(ctx, "/v1/kb/articles", q, &pg); err != nil {
		return output{}, err
	}
	var b strings.Builder
	out := output{Summary: "Found " + plural(len(pg.Data), "article", "articles")}
	for _, ar := range pg.Data {
		fmt.Fprintf(&b, "%q [%s]: %s; id %s\n", ar.Title, ar.Status, clip(ar.Excerpt, 300), ar.ID)
		out.Refs = append(out.Refs, Ref{Kind: "article", ID: ar.ID, Label: ar.Title})
	}
	if len(pg.Data) == 0 {
		b.WriteString("No articles matched.")
	}
	out.Content = block("search_kb", b.String())
	return out, nil
}

type wikiRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func wikiTitle(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Untitled"
	}
	return s
}

func wikiPath(refs []wikiRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = wikiTitle(r.Title)
	}
	return strings.Join(parts, " / ")
}

func searchWiki(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		Query string `json:"query"`
	}](in)
	if err != nil {
		return output{}, err
	}
	var pg page[struct {
		ID      string    `json:"id"`
		Title   string    `json:"title"`
		Snippet string    `json:"snippet"`
		Path    []wikiRef `json:"path"`
	}]
	if err := rc.read.get(ctx, "/v1/wiki/search", url.Values{"q": {clip(a.Query, 200)}, "limit": {"8"}}, &pg); err != nil {
		return output{}, err
	}
	var b strings.Builder
	out := output{Summary: "Found " + plural(len(pg.Data), "page", "pages")}
	for _, h := range pg.Data {
		title := wikiTitle(h.Title)
		where := ""
		if len(h.Path) > 0 {
			where = " in " + wikiPath(h.Path)
		}
		fmt.Fprintf(&b, "%q%s: %s; id %s\n", title, where, clip(h.Snippet, 300), h.ID)
		out.Refs = append(out.Refs, Ref{Kind: "wiki_page", ID: h.ID, Label: title})
	}
	if len(pg.Data) == 0 {
		b.WriteString("No wiki pages matched.")
	}
	out.Content = block("search_wiki", b.String())
	return out, nil
}

// wikiPageChars is the most of a page's text Ollie reads.
const wikiPageChars = 12000

func getWikiPage(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		Page string `json:"page"`
	}](in)
	if err != nil {
		return output{}, err
	}
	id, err := uuid.Parse(strings.TrimSpace(a.Page))
	if err != nil {
		return output{}, errors.New("page must be a page id from search_wiki")
	}
	var p struct {
		ID          string    `json:"id"`
		Title       string    `json:"title"`
		BodyHTML    string    `json:"body_html"`
		UpdatedAt   time.Time `json:"updated_at"`
		Breadcrumbs []wikiRef `json:"breadcrumbs"`
		Children    []wikiRef `json:"children"`
		UpdatedBy   *struct {
			Name string `json:"name"`
		} `json:"updated_by"`
	}
	if err := rc.read.get(ctx, "/v1/wiki/pages/"+id.String(), nil, &p); err != nil {
		return output{}, err
	}
	title := wikiTitle(p.Title)
	var b strings.Builder
	fmt.Fprintf(&b, "Wiki page %q; id %s\n", title, p.ID)
	if len(p.Breadcrumbs) > 0 {
		fmt.Fprintf(&b, "In: %s\n", wikiPath(p.Breadcrumbs))
	}
	fmt.Fprintf(&b, "Last edited %s", p.UpdatedAt.UTC().Format("2006-01-02"))
	if p.UpdatedBy != nil {
		fmt.Fprintf(&b, " by %s", p.UpdatedBy.Name)
	}
	b.WriteString("\n\n")
	if text := clip(hd.HTMLToText(p.BodyHTML), wikiPageChars); text != "" {
		b.WriteString(text)
	} else {
		b.WriteString("(The page has no text.)")
	}
	out := output{Summary: "Read " + title, Refs: []Ref{{Kind: "wiki_page", ID: p.ID, Label: title}}}
	if len(p.Children) > 0 {
		b.WriteString("\n\nSubpages:\n")
		for _, c := range p.Children {
			fmt.Fprintf(&b, "- %q; id %s\n", wikiTitle(c.Title), c.ID)
		}
	}
	out.Content = block("get_wiki_page", b.String())
	return out, nil
}

func listViews(ctx context.Context, rc *runCtx, _ json.RawMessage) (output, error) {
	var views page[struct {
		ID      string         `json:"id"`
		Name    string         `json:"name"`
		Shared  bool           `json:"shared"`
		Filters map[string]any `json:"filters"`
	}]
	if err := rc.read.get(ctx, "/v1/views", nil, &views); err != nil {
		return output{}, err
	}
	var counts struct {
		Unassigned int            `json:"unassigned"`
		Mine       int            `json:"mine"`
		Open       int            `json:"open"`
		Pending    int            `json:"pending"`
		OnHold     int            `json:"on_hold"`
		Solved     int            `json:"solved"`
		Spam       int            `json:"spam"`
		Views      map[string]int `json:"views"`
	}
	if err := rc.read.get(ctx, "/v1/tickets/counts", nil, &counts); err != nil {
		return output{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "folders: unassigned %d, mine %d, open %d, pending %d, on hold %d, solved %d, spam %d\n",
		counts.Unassigned, counts.Mine, counts.Open, counts.Pending, counts.OnHold, counts.Solved, counts.Spam)
	out := output{Summary: plural(len(views.Data), "view", "views")}
	for _, v := range views.Data {
		raw, _ := json.Marshal(v.Filters)
		scope := "personal"
		if v.Shared {
			scope = "shared"
		}
		fmt.Fprintf(&b, "view %q (%s, %d tickets): filters %s; id %s\n", v.Name, scope, counts.Views[v.ID], raw, v.ID)
		out.Refs = append(out.Refs, Ref{Kind: "view", ID: v.ID, Label: v.Name})
	}
	var teams page[struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		MemberIDs []string `json:"member_ids"`
	}]
	if rc.read.get(ctx, "/v1/teams", nil, &teams) == nil {
		for _, t := range teams.Data {
			fmt.Fprintf(&b, "team %q (%d members); id %s\n", t.Name, len(t.MemberIDs), t.ID)
		}
	}
	out.Content = block("list_views", b.String())
	return out, nil
}

func getReport(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		From string `json:"from"`
		To   string `json:"to"`
	}](in)
	if err != nil {
		return output{}, err
	}
	q := url.Values{}
	for k, v := range map[string]string{"from": a.From, "to": a.To} {
		if v = strings.TrimSpace(v); v != "" {
			if _, err := parseTime(v, false); err != nil {
				return output{}, err
			}
			q.Set(k, v)
		}
	}
	var rep map[string]any
	if err := rc.read.get(ctx, "/v1/reports/overview", q, &rep); err != nil {
		return output{}, err
	}
	// Daily rows are long; the totals carry the answer.
	if days, ok := rep["by_day"].([]any); ok && len(days) > 14 {
		rep["by_day"] = days[len(days)-14:]
		rep["by_day_note"] = "last 14 days of the range"
	}
	raw, _ := json.Marshal(rep)
	var b strings.Builder
	b.WriteString("overview: " + string(raw) + "\n")
	risk, _, err := rc.findTickets(ctx, ticketFilter{Status: []string{"open", "pending", "on_hold"}, SLA: "breached"}, 10, 300)
	if err == nil {
		fmt.Fprintf(&b, "open tickets breaching SLA now (up to 10): %d\n", len(risk))
		for _, t := range risk {
			b.WriteString(t.line() + "\n")
		}
	}
	soon, _, err := rc.findTickets(ctx, ticketFilter{Status: []string{"open", "pending", "on_hold"}, SLA: "due_soon"}, 10, 300)
	if err == nil {
		fmt.Fprintf(&b, "open tickets due on SLA in the next hour (up to 10): %d\n", len(soon))
		for _, t := range soon {
			b.WriteString(t.line() + "\n")
		}
	}
	return output{Content: block("get_report", b.String()), Summary: "Read the overview"}, nil
}

func listLabelsAndFields(ctx context.Context, rc *runCtx, _ json.RawMessage) (output, error) {
	var tags page[struct {
		Name      string   `json:"name"`
		AppliesTo []string `json:"applies_to"`
		Usage     struct {
			Tickets int `json:"tickets"`
		} `json:"usage"`
	}]
	if err := rc.read.get(ctx, "/v1/tags", url.Values{"archived": {"false"}, "applies_to": {"tickets"}}, &tags); err != nil {
		return output{}, err
	}
	var b strings.Builder
	b.WriteString("ticket labels:\n")
	n := 0
	for _, t := range tags.Data {
		if len(t.AppliesTo) > 0 && !contains(t.AppliesTo, "tickets") {
			continue
		}
		n++
		fmt.Fprintf(&b, "- %s (%d tickets)\n", t.Name, t.Usage.Tickets)
	}
	var fields page[struct {
		Key      string `json:"key"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Entity   string `json:"entity"`
		Archived bool   `json:"archived"`
		Options  []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"options"`
	}]
	if err := rc.read.get(ctx, "/v1/custom-fields", url.Values{"entity": {"ticket"}}, &fields); err == nil {
		b.WriteString("ticket custom fields:\n")
		for _, f := range fields.Data {
			if f.Archived || (f.Entity != "" && f.Entity != "ticket") {
				continue
			}
			fmt.Fprintf(&b, "- key %s, %q, type %s", f.Key, f.Name, f.Type)
			if len(f.Options) > 0 {
				var opts []string
				for _, o := range f.Options {
					opts = append(opts, o.ID+"="+o.Label)
				}
				b.WriteString(", options (id=label): " + strings.Join(opts, ", "))
			}
			b.WriteString("\n")
		}
	}
	return output{Content: block("list_labels_and_fields", b.String()), Summary: plural(n, "label", "labels")}, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func listTeam(ctx context.Context, rc *runCtx, _ json.RawMessage) (output, error) {
	ms, err := rc.loadMembers(ctx)
	if err != nil {
		return output{}, err
	}
	// Workload: counted directly, scoped to the caller's workspace and to the tickets the
	// caller may see (contracts/teams.md "Visibility").
	hidden, err := domain.HiddenTeams(ctx, rc.svc.d.DB.Pool, rc.p)
	if err != nil {
		return output{}, err
	}
	type load struct{ open, pending, urgent int }
	loads := map[string]*load{}
	rows, err := rc.svc.d.DB.Pool.Query(ctx, `SELECT t.assignee_id::text, t.status, t.priority, count(*) FROM tickets t
		WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam AND t.assignee_id IS NOT NULL AND t.status IN ('open', 'pending', 'on_hold')
			AND `+domain.VisibleSQLUser("t", 2, 3)+`
		GROUP BY 1, 2, 3`, rc.p.WorkspaceID, hidden, rc.p.UserID)
	if err != nil {
		return output{}, err
	}
	for rows.Next() {
		var uid, status, prio string
		var n int
		if err := rows.Scan(&uid, &status, &prio, &n); err != nil {
			rows.Close()
			return output{}, err
		}
		l := loads[uid]
		if l == nil {
			l = &load{}
			loads[uid] = l
		}
		if status == "pending" || status == "on_hold" {
			l.pending += n
		} else {
			l.open += n
		}
		if prio == "urgent" {
			l.urgent += n
		}
	}
	rows.Close()
	sort.SliceStable(ms, func(i, j int) bool { return strings.ToLower(ms[i].User.Name) < strings.ToLower(ms[j].User.Name) })
	var b strings.Builder
	out := output{Summary: plural(len(ms), "teammate", "teammates")}
	for _, m := range ms {
		l := loads[m.User.ID]
		if l == nil {
			l = &load{}
		}
		me := ""
		if m.User.ID == rc.p.UserID.String() {
			me = " (the user)"
		}
		fmt.Fprintf(&b, "%s <%s>%s, %s: %d open, %d pending or on hold, %d urgent; id %s\n",
			m.User.Name, m.User.Email, me, m.Role, l.open, l.pending, l.urgent, m.User.ID)
		out.Refs = append(out.Refs, Ref{Kind: "member", ID: m.User.ID, Label: m.User.Name})
	}
	var teams page[struct {
		Name      string   `json:"name"`
		MemberIDs []string `json:"member_ids"`
	}]
	if rc.read.get(ctx, "/v1/teams", nil, &teams) == nil {
		for _, t := range teams.Data {
			fmt.Fprintf(&b, "team %q: %s members\n", t.Name, strconv.Itoa(len(t.MemberIDs)))
		}
	}
	out.Content = block("list_team", b.String())
	return out, nil
}
