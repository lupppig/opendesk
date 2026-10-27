package collab

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/email"
)

// fakeMailAI answers decisions with a category per sender address.
type fakeMailAI struct {
	bySender map[string]string
	prob     float64
	err      error
	calls    int
	asked    []string // senders asked about
}

func (f *fakeMailAI) Enabled() bool                             { return true }
func (f *fakeMailAI) CanDecide(context.Context, uuid.UUID) bool { return true }
func (f *fakeMailAI) DecideMail(_ context.Context, _ uuid.UUID, req decide.Request) (decide.Result, bool, error) {
	f.calls++
	if f.err != nil {
		return decide.Result{}, false, f.err
	}
	emails := req.State.(map[string]any)["emails"].(map[string]any)
	res := decide.Result{Answers: map[string]decide.Answer{}}
	for key := range req.Questions {
		from := emails[key].(map[string]any)["from"].(string)
		for addr, cat := range f.bySender {
			if strings.Contains(from, addr) {
				f.asked = append(f.asked, addr)
				res.Answers[key] = decide.Answer{Type: decide.TypeChoice, Choice: cat, Probabilities: map[string]float64{cat: f.prob}}
			}
		}
		if _, ok := res.Answers[key]; !ok {
			res.Answers[key] = decide.Answer{Type: decide.TypeChoice, Choice: categoryPrimary, Probabilities: map[string]float64{categoryPrimary: 0.9}}
		}
	}
	return res, true, nil
}
func (f *fakeMailAI) SuggestMailCategories(context.Context, uuid.UUID, uuid.UUID, []string, string) ([]SuggestedCategory, error) {
	return []SuggestedCategory{{Key: "primary", Name: "Important", Description: "People."},
		{Key: "Invoices!", Name: "Invoices", Description: "Bills from suppliers.", Color: "green"},
		{Key: "x", Name: "https://evil.example", Color: "red"},
		{Key: "alerts", Name: "Alerts", Color: "neon"}}, nil
}
func (f *fakeMailAI) Retryable(err error) bool { return errors.Is(err, errBusyTest) }
func (f *fakeMailAI) APIError(err error) error { return err }

var errBusyTest = errors.New("busy")

// at joins an address.
func at(local, domain string) string { return local + "@" + domain }

func TestHeaderCategory(t *testing.T) {
	keys := []string{"primary", "updates", "newsletters", "promotions", "social"}
	cases := []struct {
		name string
		it   sortItem
		want string
	}{
		{"person", sortItem{From: Address{Email: at("ann", "people.example")}, Subject: "Lunch?"}, "primary"},
		{"newsletter", sortItem{From: Address{Email: at("digest", "news.example")}, Subject: "This week in Go", Signals: []string{signalList}}, "newsletters"},
		{"promotion", sortItem{From: Address{Email: at("deals", "shop.example")}, Subject: "40% off everything", Signals: []string{signalList}}, "promotions"},
		{"receipt", sortItem{From: Address{Email: at("noreply", "store.example")}, Subject: "Your receipt", Signals: []string{signalNoReply}}, "updates"},
		{"social", sortItem{From: Address{Email: at("messages-noreply", "linkedin.com")}, Subject: "You appeared in 4 searches", Signals: []string{signalList}}, "social"},
	}
	for _, c := range cases {
		if got := headerCategory(keys, &c.it); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Without the built-in keys, everything stays in Primary.
	it := sortItem{From: Address{Email: at("x", "list.example")}, Signals: []string{signalList}}
	if got := headerCategory([]string{"primary", "invoices"}, &it); got != "primary" {
		t.Errorf("custom keys: got %q", got)
	}
}

// TestGmailCategory: Gmail's own tab decides where its built-in category is the account's,
// and leaves the rest to Ollie and the headers.
func TestGmailCategory(t *testing.T) {
	gmailLike := []string{"primary", "updates", "newsletters", "promotions"}
	all := []string{"primary", "updates", "newsletters", "promotions", "social"}
	own := []string{"primary", "invoices", "promotions"}
	tab := func(sig ...string) *sortItem { return &sortItem{Signals: sig} }
	cases := []struct {
		name string
		keys []string
		it   *sortItem
		want string
	}{
		{"primary", gmailLike, tab(signalGmailPrimary), "primary"},
		{"primary with a category of the account's own", own, tab(signalGmailPrimary), ""},
		{"promotions", own, tab(signalGmailPromotions), "promotions"},
		{"social without Social", gmailLike, tab(signalGmailSocial), ""},
		{"social", all, tab(signalGmailSocial), "social"},
		{"forums are Social's", all, tab(signalGmailForums), "social"},
		{"updates", gmailLike, tab(signalGmailUpdates), "updates"},
		{"a list Gmail calls an update, with Newsletters", gmailLike, tab(signalGmailUpdates, signalList), ""},
		{"a list Gmail calls an update, without Newsletters", []string{"primary", "updates"}, tab(signalGmailUpdates, signalList), "updates"},
		{"no tab", all, tab(signalList), ""},
	}
	for _, c := range cases {
		if got := gmailCategory(c.keys, c.it); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := gmailSignals([]string{"INBOX", "UNREAD", "CATEGORY_PROMOTIONS"}); !slices.Equal(got, []string{signalGmailPromotions}) {
		t.Errorf("signals of the labels: %v", got)
	}
	if got := gmailSignals([]string{"INBOX"}); got != nil {
		t.Errorf("no tab: %v", got)
	}
}

func TestMailSignals(t *testing.T) {
	raw := "From: Shop <" + at("noreply", "shop.example") + ">\r\nTo: " + at("me", "acme.test") + "\r\nSubject: Hi\r\nList-Unsubscribe: <https://shop.example/u>\r\n" +
		"Precedence: bulk\r\nAuto-Submitted: auto-generated\r\nMessage-ID: <" + at("1", "shop.example") + ">\r\n\r\nHello\r\n"
	pm, err := email.ParseMail([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got := mailSignals(pm)
	for _, want := range []string{signalList, signalBulk, signalAuto, signalNoReply} {
		if !slices.Contains(got, want) {
			t.Errorf("signals %v lack %q", got, want)
		}
	}
}

func TestRuleFor(t *testing.T) {
	rules := []categoryRule{{Sender: "@shop.example", Category: "promotions"}, {Sender: at("boss", "shop.example"), Category: "primary"},
		{Sender: "@gone.example", Category: "removed"}}
	keys := []string{"primary", "promotions"}
	for addr, want := range map[string]string{at("deals", "shop.example"): "promotions", at("a", "mail.shop.example"): "promotions",
		at("boss", "shop.example"): "primary", at("someone", "shopping.example"): "", at("x", "gone.example"): ""} {
		if got := ruleFor(rules, addr, keys); got != want {
			t.Errorf("%s: got %q, want %q", addr, got, want)
		}
	}
}

func TestCleanCategories(t *testing.T) {
	got, err := cleanCategories([]MailCategory{{Name: "Important"}, {Name: "Job applications", Color: "green"}, {Name: "job applications"}})
	if err == nil {
		t.Fatalf("a repeated name passed: %v", got)
	}
	got, err = cleanCategories([]MailCategory{{Name: "Important"}, {Name: "Job applications", Color: "green"}, {Name: "Receipts"}})
	if err != nil {
		t.Fatal(err)
	}
	if keys := categoryKeysOf(got); !slices.Equal(keys, []string{"primary", "job-applications", "receipts"}) {
		t.Fatalf("keys %v", keys)
	}
	if got[2].Color != "gray" {
		t.Fatalf("default colour %q", got[2].Color)
	}
	for _, bad := range [][]MailCategory{
		{{Name: "Only one"}},
		{{Key: "updates", Name: "Updates"}, {Key: "primary", Name: "Primary"}},
		{{Name: "Primary"}, {Name: "Visit https://x.example"}},
		{{Name: "Primary"}, {Name: "Updates", Color: "neon"}},
	} {
		if _, err := cleanCategories(bad); err == nil {
			t.Errorf("passed: %+v", bad)
		}
	}
	cats := suggestedCategories((&fakeMailAI{}).mustSuggest())
	if keys := categoryKeysOf(cats); !slices.Equal(keys, []string{"primary", "invoices", "alerts"}) || cats[0].Name != "Important" || cats[2].Color != "gray" {
		t.Fatalf("suggested %+v", cats)
	}
	if _, err := cleanCategories(cats); err != nil {
		t.Fatalf("a cleaned suggestion does not pass: %v", err)
	}
}

func (f *fakeMailAI) mustSuggest() []SuggestedCategory {
	out, _ := f.SuggestMailCategories(context.Background(), uuid.Nil, uuid.Nil, nil, "")
	return out
}

func TestCategoriesResolve(t *testing.T) {
	w := newWorld(t)
	sales := w.team("Sales", w.agent)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	srv := w.api(w.owner)
	resolve := func() MailAccountCategories {
		t.Helper()
		row, err := loadAccount(w.ctx, w.d.DB.Pool, w.wid, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		cs, err := loadAccountCategorySets(w.ctx, w.d.DB.Pool, row)
		if err != nil {
			t.Fatal(err)
		}
		return cs.resolve(row)
	}
	if r := resolve(); r.Enabled || r.Source != sourceNone {
		t.Fatalf("without any: %+v", r)
	}
	var st MailCategorySettings
	if code := doJSON(t, http.MethodPut, srv.URL+"/v1/collab/categories/workspace", map[string]any{"enabled": true}, nil); code != 200 {
		var prob map[string]any
		doJSON(t, http.MethodPut, srv.URL+"/v1/collab/categories/workspace", map[string]any{"enabled": true}, &prob)
		t.Fatalf("workspace: %d %v", code, prob)
	}
	doJSON(t, http.MethodGet, srv.URL+"/v1/collab/categories", nil, &st)
	if st.Workspace == nil || len(st.Workspace.Categories) != 4 {
		t.Fatalf("default set: %+v", st.Workspace)
	}
	if r := resolve(); !r.Enabled || r.Source != sourceWorkspace || r.Categories[1].Key != "updates" {
		t.Fatalf("workspace: %+v", r)
	}
	// The owner's team decides over the workspace.
	if code := doJSON(t, http.MethodPut, srv.URL+"/v1/collab/categories/teams/"+sales.String(), map[string]any{"enabled": true,
		"categories": []map[string]string{{"name": "Primary"}, {"name": "Leads", "description": "Inbound leads."}}}, &st); code != 200 {
		t.Fatalf("team: %d", code)
	}
	if r := resolve(); r.Source != sourceTeam || r.Team == nil || r.Team.Name != "Sales" || r.Categories[1].Key != "leads" {
		t.Fatalf("team: %+v", r)
	}
	// Adam turns his own off; then the workspace locks, and his choice gives way.
	agent := w.api(w.agent)
	var acc MailAccount
	if code := doJSON(t, http.MethodPut, agent.URL+"/v1/collab/accounts/"+a.ID.String()+"/categories", map[string]any{"mode": "off"}, &acc); code != 200 {
		t.Fatalf("own: %d", code)
	}
	if acc.Categories.Enabled || acc.Categories.Mode != modeOff || acc.Categories.Source != sourceAccount {
		t.Fatalf("own off: %+v", acc.Categories)
	}
	if code := doJSON(t, http.MethodPut, srv.URL+"/v1/collab/categories/workspace", map[string]any{"enabled": true, "locked": true}, &st); code != 200 {
		t.Fatalf("lock: %d", code)
	}
	if r := resolve(); r.Source != sourceTeam {
		t.Fatalf("locked: %+v", r)
	}
	var prob map[string]any
	if code := doJSON(t, http.MethodPut, agent.URL+"/v1/collab/accounts/"+a.ID.String()+"/categories", map[string]any{"mode": "custom"}, &prob); code != 422 || prob["code"] != "categories_locked" {
		t.Fatalf("locked change: %d %v", code, prob)
	}
	// Turning Ollie off is always his.
	if code := doJSON(t, http.MethodPut, agent.URL+"/v1/collab/accounts/"+a.ID.String()+"/categories", map[string]any{"mode": "off", "ai": false}, &acc); code != 200 || acc.Categories.AI || !acc.Categories.Locked {
		t.Fatalf("ai off: %d %+v", code, acc.Categories)
	}
	// A member sees their own team's set, not others'.
	w.team("Support")
	var mine MailCategorySettings
	if code := doJSON(t, http.MethodGet, agent.URL+"/v1/collab/categories", nil, &mine); code != 200 || len(mine.Teams) != 1 || !mine.Workspace.Locked {
		t.Fatalf("member view: %d %+v", code, mine)
	}
	if code := doJSON(t, http.MethodPut, agent.URL+"/v1/collab/categories/workspace", map[string]any{"enabled": false}, nil); code != 403 {
		t.Fatalf("an agent set the default: %d", code)
	}
}

func TestCategorizeSorts(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	inbox, sent := w.folder(a, RoleInbox), w.folder(a, RoleSent)
	fake := &fakeMailAI{bySender: map[string]string{at("digest", "news.example"): "newsletters", at("deals", "shop.example"): "promotions"}, prob: 0.9}
	w.s.mailAI = fake
	owner := w.api(w.owner)
	if code := doJSON(t, http.MethodPut, owner.URL+"/v1/collab/categories/workspace", map[string]any{"enabled": true}, nil); code != 200 {
		t.Fatalf("workspace: %d", code)
	}
	thread := func(subject, from string, signals []string) uuid.UUID {
		tid, mid := w.seedThread(a, subject, inbox)
		mustExec(t, w.d, `UPDATE collab_messages SET from_addr = jsonb_build_object('name', '', 'email', $2::text), signals = COALESCE($3::text[], '{}') WHERE id = $1`, mid, from, signals)
		mustExec(t, w.d, `UPDATE collab_threads SET addresses = ARRAY[$2::text] WHERE id = $1`, tid, from)
		return tid
	}
	news := thread("This week", at("digest", "news.example"), []string{signalList})
	promo := thread("Big sale", at("deals", "shop.example"), []string{signalList})
	unsure := thread("Maybe", at("hello", "unknown.example"), nil)
	friend := thread("Dinner", at("pal", "friends.example"), []string{signalList})
	// Mail Gmail filed under Updates goes there without asking Ollie.
	shipped := thread("Your order shipped", at("orders", "store.example"), []string{signalGmailUpdates})
	// Someone the account writes to stays in Primary without asking Ollie.
	sentTo, _ := w.seedThread(a, "Re: earlier", sent)
	mustExec(t, w.d, `UPDATE collab_threads SET addresses = ARRAY[$2::text] WHERE id = $1`, sentTo, at("pal", "friends.example"))

	// The first run takes the new keys and sorts the recent inbox.
	if err := w.s.categorize(w.ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	cat := func(id uuid.UUID) string {
		var c *string
		if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT category FROM collab_threads WHERE id = $1`, id).Scan(&c); err != nil || c == nil {
			t.Fatalf("category of %s: %v", id, err)
		}
		return *c
	}
	for id, want := range map[uuid.UUID]string{news: "newsletters", promo: "promotions", unsure: "primary", friend: "primary", shipped: "updates"} {
		if got := cat(id); got != want {
			t.Errorf("%s: got %q, want %q", id, got, want)
		}
	}
	if slices.Contains(fake.asked, at("pal", "friends.example")) {
		t.Errorf("Ollie was asked about someone the account writes to")
	}
	if slices.Contains(fake.asked, at("orders", "store.example")) {
		t.Errorf("Ollie was asked about mail Gmail had sorted")
	}
	// Ollie unsure (a low probability) leaves it in Primary.
	fake.prob = 0.3
	low := thread("Offer", at("maybe", "offers.example"), nil)
	mustExec(t, w.d, `UPDATE collab_threads SET category_pending = true WHERE id = $1`, low)
	if err := w.s.categorize(w.ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := cat(low); got != "primary" {
		t.Errorf("unsure: %q", got)
	}

	agent := w.api(w.agent)
	var page struct {
		Data []MailThread `json:"data"`
	}
	if code := doJSON(t, http.MethodGet, agent.URL+"/v1/collab/threads?box=inbox&category=newsletters", nil, &page); code != 200 ||
		len(page.Data) != 1 || page.Data[0].ID != news || page.Data[0].Category == nil || *page.Data[0].Category != "newsletters" {
		t.Fatalf("newsletters tab: %d %+v", code, page.Data)
	}
	if code := doJSON(t, http.MethodGet, agent.URL+"/v1/collab/threads?box=inbox&category=primary", nil, &page); code != 200 || len(page.Data) != 3 {
		t.Fatalf("primary tab: %d %d", code, len(page.Data))
	}
	if code := doJSON(t, http.MethodGet, agent.URL+"/v1/collab/threads?box=sent&category=primary", nil, nil); code != 422 {
		t.Fatalf("a tab outside the inbox: %d", code)
	}
	var counts CollabCounts
	if code := doJSON(t, http.MethodGet, agent.URL+"/v1/collab/counts", nil, &counts); code != 200 ||
		counts.Categories[a.ID]["newsletters"] != 1 || counts.Categories[a.ID]["primary"] != 3 {
		t.Fatalf("counts: %+v", counts.Categories)
	}

	// Moving one is the person's choice, which sorting keeps.
	if code := doJSON(t, http.MethodPost, agent.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{promo},
		"action": "categorize", "category": "updates"}, nil); code != 200 {
		t.Fatalf("categorize: %d", code)
	}
	if code := doJSON(t, http.MethodPost, agent.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{promo},
		"action": "categorize", "category": "nope"}, nil); code != 422 {
		t.Fatalf("unknown category: %d", code)
	}
	if got := cat(promo); got != "updates" {
		t.Fatalf("moved: %q", got)
	}
	// A sender rule moves that sender's inbox mail now, and its new mail later.
	var rule categoryRule
	if code := doJSON(t, http.MethodPost, agent.URL+"/v1/collab/accounts/"+a.ID.String()+"/category-rules",
		map[string]any{"sender": "@news.example", "category": "updates"}, &rule); code != 200 || rule.Sender != "@news.example" {
		t.Fatalf("rule: %d %+v", code, rule)
	}
	if got := cat(news); got != "updates" {
		t.Fatalf("rule moved: %q", got)
	}
	later := thread("Next week", at("digest", "news.example"), []string{signalList})
	mustExec(t, w.d, `UPDATE collab_threads SET category_pending = true WHERE id = $1`, later)
	before := fake.calls
	if err := w.s.categorize(w.ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := cat(later); got != "updates" || fake.calls != before {
		t.Fatalf("rule on new mail: %q, Ollie asked %d times", got, fake.calls-before)
	}
	// Bea cannot see the private account: no rules for her.
	if code := doJSON(t, http.MethodGet, w.api(w.agent2).URL+"/v1/collab/accounts/"+a.ID.String()+"/category-rules", nil, nil); code != 404 {
		t.Fatalf("someone else's rules: %d", code)
	}

	// Without Ollie (a failing engine past its retries) the headers decide.
	fake.err = errBusyTest
	hdr := thread("Weekly digest", at("weekly", "digest.example"), []string{signalList})
	mustExec(t, w.d, `UPDATE collab_threads SET category_pending = true WHERE id = $1`, hdr)
	if err := w.s.categorize(w.ctx, a.ID, 1); err == nil {
		t.Fatal("a busy engine on the first attempt did not wait")
	}
	if err := w.s.categorize(w.ctx, a.ID, sortRetryUntil); err != nil {
		t.Fatal(err)
	}
	if got := cat(hdr); got != "newsletters" {
		t.Fatalf("headers: %q", got)
	}

	// Turning categories off clears them; MailThread.category is null.
	if code := doJSON(t, http.MethodPut, owner.URL+"/v1/collab/categories/workspace", map[string]any{"enabled": false}, nil); code != 200 {
		t.Fatal("off")
	}
	if err := w.s.categorize(w.ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE account_id = $1 AND category IS NOT NULL`, a.ID); n != 0 {
		t.Fatalf("%d still sorted", n)
	}
	if code := doJSON(t, http.MethodGet, agent.URL+"/v1/collab/threads?box=inbox", nil, &page); code != 200 || page.Data[0].Category != nil {
		t.Fatalf("off: %+v", page.Data[0].Category)
	}
}

func TestSuggestCategories(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	w.seedThread(a, "Invoice 42", w.folder(a, RoleInbox))
	w.s.mailAI = &fakeMailAI{}
	var out struct {
		Categories []MailCategory `json:"categories"`
	}
	if code := doJSON(t, http.MethodPost, w.api(w.agent).URL+"/v1/collab/categories/suggest", map[string]any{"account_id": a.ID, "about": "A bakery"}, &out); code != 200 || len(out.Categories) != 3 {
		t.Fatalf("suggest: %d %+v", code, out)
	}
	if code := doJSON(t, http.MethodPost, w.api(w.agent2).URL+"/v1/collab/categories/suggest", map[string]any{"account_id": a.ID}, nil); code != 404 {
		t.Fatalf("someone else's account: %d", code)
	}
	if code := doJSON(t, http.MethodPost, w.api(w.viewer).URL+"/v1/collab/categories/suggest", map[string]any{}, nil); code != 403 {
		t.Fatalf("a viewer: %d", code)
	}
}
