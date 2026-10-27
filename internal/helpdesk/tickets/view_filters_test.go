package tickets_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Label lists (any of, none of, no label) and exclusions of teams, mailboxes and channels,
// in the list, in saved views and in their counts.
func TestListAnyOfAndExclusionFilters(t *testing.T) {
	e, ws := newFullEnv(t)
	agent := ws.Agent.As(ws)
	lead := e.AddUser(ws, auth.RoleAgent, "Lena Lead")
	returns := addTeam(t, e, ws, ws.Agent.ID)
	lgx := addTeam(t, e, ws)
	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{lead.ID: hd.TeamLead})
	mb := addMailbox(t, e, ws)

	a := createTicket(t, e, ws, map[string]any{"subject": "Cancel my order", "kind": "note", "team_id": returns.String(), "tags": []string{"cancel/refund"}})
	b := createTicket(t, e, ws, map[string]any{"subject": "Change my order", "kind": "note", "team_id": lgx.String(),
		"tags": []string{"ORDER-CHANGE/CANCEL", "Q&A Done"}})
	c := createTicket(t, e, ws, map[string]any{"subject": "Answered", "kind": "note", "tags": []string{"Q&A Done"}})
	d := createTicket(t, e, ws, map[string]any{"subject": "Plain email", "kind": "note", "mailbox_id": mb.String()})
	var p struct {
		ID uuid.UUID `json:"id"`
	}
	e.Do("POST", "/v1/tickets", lead.As(ws), map[string]any{"subject": "Private", "body_html": "<p>x</p>", "kind": "note",
		"team_id": priv.String()}).Want(t, 201).JSON(t, &p)

	qa := url.QueryEscape("Q&A Done")
	cases := []struct {
		q    string
		want []uuid.UUID
	}{
		{"tag=CANCEL/REFUND", []uuid.UUID{a.ID}}, // one name, as before
		{"tag=cancel/refund,order-change/cancel", []uuid.UUID{a.ID, b.ID}},
		{"tag=" + url.QueryEscape(" cancel/refund , ,nothing"), []uuid.UUID{a.ID}},
		{"tag_not=" + qa, []uuid.UUID{a.ID, d.ID}},
		{"tag_not=" + qa + ",cancel/refund", []uuid.UUID{d.ID}},
		{"untagged=true", []uuid.UUID{d.ID}},
		{"untagged=true&tag=cancel/refund", []uuid.UUID{}},
		{"team_not=" + returns.String() + "," + lgx.String(), []uuid.UUID{c.ID, d.ID}},
		{"team_not=" + returns.String() + "&tag_not=" + qa, []uuid.UUID{d.ID}},
		{"team_not=none", []uuid.UUID{a.ID, b.ID}},
		{"team_not=mine", []uuid.UUID{b.ID, c.ID, d.ID}},
		{"team_id=" + returns.String() + "," + lgx.String() + "&team_not=" + lgx.String(), []uuid.UUID{a.ID}},
		{"mailbox_not=" + mb.String(), []uuid.UUID{a.ID, b.ID, c.ID}},
		{"channel_not=api", []uuid.UUID{d.ID}},
		{"channel_not=email,chat", []uuid.UUID{a.ID, b.ID, c.ID}},
	}
	for _, cs := range cases {
		if got := idsOf(list(t, e, agent, cs.q)); !sameSet(got, cs.want) {
			t.Errorf("%q: got %v want %v", cs.q, got, cs.want)
		}
	}
	for _, bad := range []string{"channel_not=fax", "team_not=nope", "mailbox_not=nope", "untagged=maybe"} {
		if res := e.Do("GET", "/v1/tickets?"+bad, agent, nil); res.Status != 422 {
			t.Errorf("%s: %d %s", bad, res.Status, res.Body)
		}
	}
	// Exclusions never reveal a private team's tickets; its members see them as usual.
	for q, want := range map[string][]uuid.UUID{
		"untagged=true": {d.ID, p.ID},
		"team_not=" + returns.String() + "," + lgx.String(): {c.ID, d.ID, p.ID},
		"team_not=" + priv.String():                         {a.ID, b.ID, c.ID, d.ID},
	} {
		if got := idsOf(list(t, e, lead.As(ws), q)); !sameSet(got, want) {
			t.Errorf("lead %q: got %v want %v", q, got, want)
		}
	}

	// Saved views store the lists normalized and filter, and count, the same tickets.
	type view struct {
		ID      uuid.UUID        `json:"id"`
		Filters hd.TicketFilters `json:"filters"`
	}
	newView := func(name string, filters map[string]any) view {
		t.Helper()
		var v view
		e.Do("POST", "/v1/views", ws.Admin.As(ws), map[string]any{"name": name, "shared": true, "filters": filters}).Want(t, 201).JSON(t, &v)
		return v
	}
	anyOf := newView("Cancellations", map[string]any{"tag": " cancel/refund, ORDER-CHANGE/CANCEL ,Cancel/Refund"})
	if anyOf.Filters.Tag != "cancel/refund,ORDER-CHANGE/CANCEL" {
		t.Fatalf("stored tag %q", anyOf.Filters.Tag)
	}
	excl := newView("To handle", map[string]any{"tag_not": "Q&A Done", "team_not": lgx.String() + ", " + returns.String() + "," + lgx.String(),
		"channel_not": []string{"chat", "chat"}})
	if excl.Filters.TeamNot != lgx.String()+","+returns.String() || len(excl.Filters.ChannelNot) != 1 {
		t.Fatalf("stored exclusions %+v", excl.Filters)
	}
	empty := newView("Empty tags", map[string]any{"untagged": true})
	if !empty.Filters.Untagged {
		t.Fatalf("stored untagged %+v", empty.Filters)
	}
	notMailbox := newView("Not support", map[string]any{"mailbox_not": mb.String()})
	// A view saved before label lists existed keeps its meaning.
	legacy := ids.New()
	e.Exec(`INSERT INTO views (id, workspace_id, name, filters, position) VALUES ($1, $2, 'Legacy', '{"tag":"Cancel/Refund"}', 9)`, legacy, ws.ID)
	for _, bad := range []map[string]any{{"team_not": ids.New().String()}, {"mailbox_not": "nope"}, {"channel_not": []string{"fax"}},
		{"tag_not": "x,y,z,w,v,u,t,s,r,q,p,o,n,m,l,k,j,i,h,g,f,e,d,c,b,a,aa,bb,cc,dd,ee,ff,gg,hh,ii,jj,kk,ll,mm,nn,oo,pp,qq,rr,ss,tt,uu,vv,ww,xx,yy"}} {
		e.Do("POST", "/v1/views", ws.Admin.As(ws), map[string]any{"name": "Bad", "shared": true, "filters": bad}).Want(t, 422)
	}

	want := map[uuid.UUID][]uuid.UUID{
		anyOf.ID:      {a.ID, b.ID},
		excl.ID:       {d.ID},
		empty.ID:      {d.ID},
		notMailbox.ID: {a.ID, b.ID, c.ID},
		legacy:        {a.ID},
	}
	var counts struct {
		Views map[string]int `json:"views"`
		Stats map[string]struct {
			Urgent int `json:"urgent"`
		} `json:"stats"`
	}
	e.Do("GET", "/v1/tickets/counts", agent, nil).Want(t, 200).JSON(t, &counts)
	for id, w := range want {
		got := idsOf(list(t, e, agent, "view_id="+id.String()))
		if !sameSet(got, w) {
			t.Errorf("view %s: got %v want %v", id, got, w)
		}
		if counts.Views[id.String()] != len(got) {
			t.Errorf("view %s counts %d, lists %d", id, counts.Views[id.String()], len(got))
		}
		if _, ok := counts.Stats["view:"+id.String()]; !ok {
			t.Errorf("view %s has no stats", id)
		}
	}
	// The private team's ticket counts in a view for its members only.
	e.Do("GET", "/v1/tickets/counts", lead.As(ws), nil).Want(t, 200).JSON(t, &counts)
	if counts.Views[empty.ID.String()] != 2 || counts.Views[excl.ID.String()] != 2 {
		t.Fatalf("lead counts %v", counts.Views)
	}
	// Explicit filters replace the view's own for the same key.
	if got := idsOf(list(t, e, agent, "view_id="+anyOf.ID.String()+"&tag="+qa)); !sameSet(got, []uuid.UUID{b.ID, c.ID}) {
		t.Fatalf("override %v", got)
	}
}

// Renaming or merging a label rewrites it in every view's label lists.
func TestLabelRenameRewritesViewLists(t *testing.T) {
	e, ws := newFullEnv(t)
	admin := ws.Admin.As(ws)
	label := func(name string) string {
		var l struct {
			ID string `json:"id"`
		}
		e.Do("POST", "/v1/tags", admin, map[string]any{"name": name}).Want(t, 201).JSON(t, &l)
		return l.ID
	}
	refund, cancel, done := label("Refund"), label("Cancel"), label("Done")
	label("Other")
	view := func(filters string) uuid.UUID {
		id := ids.New()
		e.Exec(`INSERT INTO views (id, workspace_id, name, filters, position) VALUES ($1, $2, 'v', $3, 0)`, id, ws.ID, filters)
		return id
	}
	anyOf := view(`{"tag":"refund,Cancel","status":["open"]}`)
	noneOf := view(`{"tag_not":"Done,Other"}`)
	single := view(`{"tag":"DONE"}`)
	untouched := view(`{"tag":"Other","tag_not":"Refunded"}`)
	filters := func(id uuid.UUID) map[string]any {
		var raw string
		e.Scalar(`SELECT filters::text FROM views WHERE id = $1`, &raw, id)
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	e.Do("PATCH", "/v1/tags/"+refund, admin, map[string]any{"name": "Refunds"}).Want(t, 200)
	if f := filters(anyOf); f["tag"] != "Refunds,Cancel" || f["status"] == nil {
		t.Fatalf("rename %v", f)
	}
	e.Do("POST", "/v1/tags/"+cancel+"/merge", admin, map[string]any{"into": refund}).Want(t, 200)
	if f := filters(anyOf); f["tag"] != "Refunds" {
		t.Fatalf("merge %v", f)
	}
	e.Do("PATCH", "/v1/tags/"+done, admin, map[string]any{"name": "QA Done"}).Want(t, 200)
	if f := filters(noneOf); f["tag_not"] != "QA Done,Other" {
		t.Fatalf("tag_not rename %v", f)
	}
	if f := filters(single); f["tag"] != "QA Done" {
		t.Fatalf("single rename %v", f)
	}
	if f := filters(untouched); f["tag"] != "Other" || f["tag_not"] != "Refunded" {
		t.Fatalf("untouched %v", f)
	}
}

// Leaving out contacts and companies, in the list, in saved views, their counts and bulk
// queries. A saved view names only the workspace's own contacts and companies.
func TestListLeavesOutContactsAndCompanies(t *testing.T) {
	e, ws := newFullEnv(t)
	agent := ws.Agent.As(ws)
	company := func(wid uuid.UUID, name string) uuid.UUID {
		id := ids.New()
		e.Exec(`INSERT INTO companies (id, workspace_id, name) VALUES ($1, $2, $3)`, id, wid, name)
		return id
	}
	contact := func(wid uuid.UUID, name string, companyID *uuid.UUID) uuid.UUID {
		id := ids.New()
		e.Exec(`INSERT INTO contacts (id, workspace_id, name, email, company_id) VALUES ($1, $2, $3, $4, $5)`,
			id, wid, name, strings.ToLower(name)+"-"+id.String()[24:]+"@customer.test", companyID)
		return id
	}
	acme, globex := company(ws.ID, "Acme"), company(ws.ID, "Globex")
	ann, bob, cat, dan := contact(ws.ID, "Ann", &acme), contact(ws.ID, "Bob", &acme), contact(ws.ID, "Cat", &globex), contact(ws.ID, "Dan", nil)
	from := func(c uuid.UUID) uuid.UUID {
		return createTicket(t, e, ws, map[string]any{"subject": "Hello", "kind": "note", "contact_id": c.String()}).ID
	}
	ta, tb, tc, td := from(ann), from(bob), from(cat), from(dan)
	tn := createTicket(t, e, ws, map[string]any{"subject": "No contact", "kind": "note"}).ID
	if getTicket(t, e, ws, tn).Contact != nil {
		t.Fatal("ticket has a contact")
	}

	cases := []struct {
		q    string
		want []uuid.UUID
	}{
		{"contact_not=" + ann.String(), []uuid.UUID{tb, tc, td, tn}},
		{"contact_not=" + url.QueryEscape(" "+ann.String()+" , ,"+cat.String()), []uuid.UUID{tb, td, tn}},
		{"company_not=" + acme.String(), []uuid.UUID{tc, td, tn}},
		{"company_not=" + acme.String() + "," + globex.String(), []uuid.UUID{td, tn}},
		{"company_not=" + acme.String() + "&contact_not=" + dan.String(), []uuid.UUID{tc, tn}},
		{"contact_id=" + ann.String() + "&company_not=" + acme.String(), []uuid.UUID{}},
		{"contact_not=" + ids.New().String(), []uuid.UUID{ta, tb, tc, td, tn}},
	}
	for _, cs := range cases {
		if got := idsOf(list(t, e, agent, cs.q)); !sameSet(got, cs.want) {
			t.Errorf("%q: got %v want %v", cs.q, got, cs.want)
		}
	}
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = ids.New().String()
	}
	for _, bad := range []string{"contact_not=nope", "company_not=" + acme.String() + ",nope", "contact_not=" + strings.Join(tooMany, ",")} {
		if res := e.Do("GET", "/v1/tickets?"+bad, agent, nil); res.Status != 422 {
			t.Errorf("%.40s: %d %s", bad, res.Status, res.Body)
		}
	}

	type view struct {
		ID      uuid.UUID        `json:"id"`
		Filters hd.TicketFilters `json:"filters"`
	}
	newView := func(name string, filters map[string]any) view {
		t.Helper()
		var v view
		e.Do("POST", "/v1/views", ws.Admin.As(ws), map[string]any{"name": name, "shared": true, "filters": filters}).Want(t, 201).JSON(t, &v)
		return v
	}
	notThem := newView("Not Ann or Cat", map[string]any{"contact_not": " " + ann.String() + ", " + cat.String() + "," + ann.String()})
	if notThem.Filters.ContactNot != ann.String()+","+cat.String() {
		t.Fatalf("stored contact_not %q", notThem.Filters.ContactNot)
	}
	notAcme := newView("Not Acme", map[string]any{"company_not": acme.String(), "status": []string{"open"}})
	if notAcme.Filters.CompanyNot != acme.String() {
		t.Fatalf("stored company_not %q", notAcme.Filters.CompanyNot)
	}
	// The ids a view names are the workspace's own contacts and companies.
	other := e.NewWorkspace()
	otherCompany := company(other.ID, "Initech")
	otherContact := contact(other.ID, "Eve", &otherCompany)
	for _, bad := range []map[string]any{{"contact_not": otherContact.String()}, {"company_not": otherCompany.String()},
		{"contact_not": acme.String()}, {"company_not": ann.String()}, {"contact_not": "nope"}, {"company_not": strings.Join(tooMany, ",")}} {
		e.Do("POST", "/v1/views", ws.Admin.As(ws), map[string]any{"name": "Bad", "shared": true, "filters": bad}).Want(t, 422)
	}
	e.Do("PATCH", "/v1/views/"+notAcme.ID.String(), ws.Admin.As(ws), map[string]any{"filters": map[string]any{"company_not": otherCompany.String()}}).Want(t, 422)

	want := map[uuid.UUID][]uuid.UUID{
		notThem.ID: {tb, td, tn},
		notAcme.ID: {tc, td, tn},
	}
	var counts struct {
		Views map[string]int `json:"views"`
	}
	e.Do("GET", "/v1/tickets/counts", agent, nil).Want(t, 200).JSON(t, &counts)
	for id, w := range want {
		got := idsOf(list(t, e, agent, "view_id="+id.String()))
		if !sameSet(got, w) {
			t.Errorf("view %s: got %v want %v", id, got, w)
		}
		if counts.Views[id.String()] != len(got) {
			t.Errorf("view %s counts %d, lists %d", id, counts.Views[id.String()], len(got))
		}
	}
	// Explicit filters replace the view's own for the same key.
	if got := idsOf(list(t, e, agent, "view_id="+notThem.ID.String()+"&contact_not="+bob.String())); !sameSet(got, []uuid.UUID{ta, tc, td, tn}) {
		t.Fatalf("override %v", got)
	}

	// A bulk query leaves out the same tickets.
	var out struct{ Updated int }
	e.Do("POST", "/v1/tickets/bulk", agent, map[string]any{"query": map[string]any{"company_not": acme.String(), "contact_not": dan.String()},
		"patch": map[string]any{"priority": "high"}}).Want(t, 200).JSON(t, &out)
	if out.Updated != 2 || getTicket(t, e, ws, tc).Priority != "high" || getTicket(t, e, ws, ta).Priority != "normal" || getTicket(t, e, ws, td).Priority != "normal" {
		t.Fatalf("bulk updated %d", out.Updated)
	}
}
