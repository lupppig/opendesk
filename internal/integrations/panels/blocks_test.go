package panels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/netsafe"
)

func rawOf(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(s)) {
		t.Fatalf("not JSON: %s", s)
	}
	return json.RawMessage(s)
}

func TestCleanValue(t *testing.T) {
	for in, want := range map[string]*PanelValue{
		`"Flos S.p.A."`:                        {Type: "text", Text: "Flos S.p.A."},
		`"a\u0000b\nc"`:                        {Type: "text", Text: "a b c"},
		`"   "`:                                nil,
		`3`:                                    {Type: "text", Text: "3"},
		`1234.50`:                              {Type: "text", Text: "1234.50"},
		`true`:                                 {Type: "text", Text: "true"},
		`null`:                                 nil,
		`[1,2]`:                                nil,
		`{"type":"text","text":"Paid"}`:        {Type: "text", Text: "Paid"},
		`{"type":"date","value":"2026-11-04"}`: {Type: "date", Value: "2026-11-04"},
		`{"type":"date","value":"2026-11-04","relative":true}`:  {Type: "date", Value: "2026-11-04", Relative: true},
		`{"type":"date","value":"2026-11-04T10:30:00+02:00"}`:   {Type: "date", Value: "2026-11-04T08:30:00Z"},
		`{"type":"date","value":"2026-11-04T10:30:00.123Z"}`:    {Type: "date", Value: "2026-11-04T10:30:00Z"},
		`{"type":"date","value":"2026-11-04 10:30:00"}`:         {Type: "date", Value: "2026-11-04T10:30:00Z"},
		`{"type":"date","value":"2026-11-04T10:30"}`:            {Type: "date", Value: "2026-11-04T10:30:00Z"},
		`{"type":"date","value":"next week"}`:                   nil,
		`{"type":"date","value":"2026-13-01"}`:                  nil,
		`{"type":"money","amount":"129.00","currency":"EUR"}`:   {Type: "money", Amount: "129.00", Currency: "EUR"},
		`{"type":"money","amount":-14.5,"currency":"usd"}`:      {Type: "money", Amount: "-14.5", Currency: "USD"},
		`{"type":"money","amount":"1,200.00","currency":"EUR"}`: nil,
		`{"type":"money","amount":"12","currency":"EURO"}`:      nil,
		`{"type":"money","amount":"12"}`:                        nil,
		`{"type":"money","amount":1e3,"currency":"EUR"}`:        nil,
		`{"type":"badge","text":"Late","tone":"WARNING"}`:       {Type: "badge", Text: "Late", Tone: "warning"},
		`{"type":"badge","text":"Odd","tone":"purple"}`:         {Type: "badge", Text: "Odd", Tone: "neutral"},
		`{"type":"badge","text":""}`:                            nil,
		`{"type":"html","value":"<b>x</b>"}`:                    nil,
		`{"value":"no type"}`:                                   nil,
	} {
		got := cleanValue(rawOf(t, in))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("cleanValue(%s) = %+v, want %+v", in, got, want)
		}
	}
	if v := cleanValue(rawOf(t, `"`+strings.Repeat("é", 600)+`"`)); len([]rune(v.Text)) != maxValue {
		t.Errorf("a long value is %d runes", len([]rune(v.Text)))
	}
	if v := cleanValue(rawOf(t, `{"type":"badge","text":"`+strings.Repeat("x", 60)+`","tone":"info"}`)); len([]rune(v.Text)) != maxBadgeText {
		t.Errorf("a long badge is %d runes", len([]rune(v.Text)))
	}
}

func TestCleanBlocks(t *testing.T) {
	s := &Service{guard: netsafe.Guard{}}
	var a answer
	if err := json.Unmarshal([]byte(`{"version":2,"cache_seconds":120,"blocks":[
		{"type":"card","title":"Sales order S00042","url":"https://erp.example/so/42","subtitle":"Confirmed\ton Oct 2",
		 "badges":[{"text":"Late","tone":"warning"},{"text":"Paid","tone":"success"},{"text":""},{"text":"c"},{"text":"d"},{"text":"e"}],
		 "fields":[{"label":"Supplier date","value":{"type":"date","value":"2026-11-12","relative":true}},
		           {"label":"Nothing"},{"label":"Odd","value":{"type":"video"}},{"label":"Link","value":"x","url":"javascript:alert(1)"}],
		 "lines":[{"quantity":2,"title":"Arco floor lamp","subtitle":"Marble","value":{"type":"money","amount":"1890.00","currency":"EUR"},"url":"https://erp.example/p/1"},
		          {"quantity":"1.5","title":"Cable, m"},{"quantity":"lots","title":"Bulbs"},{"title":""},"not an object"],
		 "footer":[{"label":"Total","value":{"type":"money","amount":3780,"currency":"EUR"}}]},
		{"type":"card","subtitle":"no title"},
		{"type":"chart","title":"Unknown"},
		{"type":"fields","title":"Account","items":[{"label":"Plan","value":"Pro"},{"label":"Seats","value":12}]},
		{"type":"fields","items":[]},
		{"type":"list","title":"Invoices","items":[{"title":"INV/2026/0012","url":"https://erp.example/inv/12","subtitle":"Due Nov 1",
		 "value":{"type":"money","amount":"120","currency":"EUR"},"badge":{"text":"Overdue","tone":"danger"}},{"subtitle":"no title"}]},
		{"type":"note","text":"No subscription.","tone":"info"},
		{"type":"note","text":"  "},
		{"type":"NOTE","title":"dropped","text":"Toned down","tone":"loud"},
		"a string",
		{"type":"card","title":12}
	]}`), &a); err != nil {
		t.Fatal(err)
	}
	if !a.blocksAnswer() || cacheFor(a.CacheSeconds) != 2*time.Minute {
		t.Fatalf("answer = %+v", a)
	}
	got := s.cleanBlocks(a.Blocks)
	q := func(f float64) *float64 { return &f }
	want := []PanelBlock{
		{Type: "card", Title: "Sales order S00042", URL: "https://erp.example/so/42", Subtitle: "Confirmed on Oct 2",
			Badges: []PanelBadge{{"Late", "warning"}, {"Paid", "success"}, {"c", "neutral"}, {"d", "neutral"}},
			Fields: []PanelItem{{Label: "Supplier date", Value: &PanelValue{Type: "date", Value: "2026-11-12", Relative: true}},
				{Label: "Link", Value: &PanelValue{Type: "text", Text: "x"}}},
			Lines: []PanelItem{{Quantity: q(2), Title: "Arco floor lamp", Subtitle: "Marble", Value: &PanelValue{Type: "money", Amount: "1890.00", Currency: "EUR"}, URL: "https://erp.example/p/1"},
				{Quantity: q(1.5), Title: "Cable, m"}, {Title: "Bulbs"}},
			Footer: []PanelItem{{Label: "Total", Value: &PanelValue{Type: "money", Amount: "3780", Currency: "EUR"}}}},
		{Type: "fields", Title: "Account", Items: []PanelItem{{Label: "Plan", Value: &PanelValue{Type: "text", Text: "Pro"}}, {Label: "Seats", Value: &PanelValue{Type: "text", Text: "12"}}}},
		{Type: "list", Title: "Invoices", Items: []PanelItem{{Title: "INV/2026/0012", URL: "https://erp.example/inv/12", Subtitle: "Due Nov 1",
			Value: &PanelValue{Type: "money", Amount: "120", Currency: "EUR"}, Badge: &PanelBadge{"Overdue", "danger"}}}},
		{Type: "note", Text: "No subscription.", Tone: "info"},
		{Type: "note", Text: "Toned down", Tone: "neutral"},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("blocks =\n%s\nwant\n%s", g, w)
	}

	// The limits: 10 blocks, 30 fields, 20 lines, 6 footer fields, 20 list rows.
	many := func(n int, item string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(item, i)
		}
		return strings.Join(parts, ",")
	}
	body := `[` + many(12, `{"type":"note","text":"n%d"}`) + `]`
	if b := s.cleanBlocks(decodeList(t, body)); len(b) != maxBlocks || b[9].Text != "n9" {
		t.Fatalf("%d blocks", len(b))
	}
	card := `[{"type":"card","title":"Big","fields":[` + many(35, `{"label":"f%d","value":"v"}`) + `],"lines":[` + many(25, `{"title":"l%d"}`) +
		`],"footer":[` + many(9, `{"label":"t%d","value":"v"}`) + `]},{"type":"fields","items":[` + many(40, `{"label":"f%d","value":"v"}`) +
		`]},{"type":"list","items":[` + many(30, `{"title":"r%d"}`) + `]}]`
	b := s.cleanBlocks(decodeList(t, card))
	if len(b[0].Fields) != 30 || len(b[0].Lines) != 20 || len(b[0].Footer) != 6 || len(b[1].Items) != 30 || len(b[2].Items) != 20 {
		t.Fatalf("limits: %d fields, %d lines, %d footer, %d items, %d rows", len(b[0].Fields), len(b[0].Lines), len(b[0].Footer), len(b[1].Items), len(b[2].Items))
	}

	// A version 1 answer stays one; version 2 without blocks is empty blocks.
	for in, want := range map[string]bool{`{"sections":[]}`: false, `{"version":1,"sections":[]}`: false, `{"version":2}`: true, `{"version":"2"}`: false,
		`{"blocks":[]}`: true, `{"version":3,"blocks":[]}`: true} {
		var a answer
		if err := json.Unmarshal([]byte(in), &a); err != nil || a.blocksAnswer() != want {
			t.Errorf("blocksAnswer(%s) = %v, %v", in, a.blocksAnswer(), err)
		}
	}
}

func TestCleanLinks(t *testing.T) {
	s := &Service{guard: netsafe.Guard{}}
	b := s.cleanBlocks(decodeList(t, `[
		{"type":"card","title":"Order #1042","url":"https://shop.example/orders/1042","links":[
			{"label":"Open in the shop","url":"https://admin.shopify.com/store/x/orders/1","icon":"Shopify"},
			{"label":" Open in\tthe ERP ","url":"https://erp.example/so/42","icon":" receipt "},
			{"label":"Carrier","url":"https://carrier.example/t/1","icon":"https://evil.example/favicon.ico"},
			{"label":"Plain","url":"https://crm.example/a/7"},
			{"label":"No url"},
			{"label":"Script","url":"javascript:alert(1)","icon":"truck"},
			{"label":"Insecure","url":"http://erp.example/so/42"},
			{"label":"","url":"https://erp.example/blank"},
			"not an object",
			{"label":"`+strings.Repeat("x", 60)+`","url":"https://erp.example/long","icon":"package"},
			{"label":"Sixth","url":"https://erp.example/6"}]},
		{"type":"list","items":[{"title":"INV/1","links":[{"label":"PDF","url":"https://billing.example/inv/1.pdf","icon":"file-text"}]}]},
		{"type":"card","title":"Lines","lines":[{"title":"Lamp","links":[{"label":"Shop","url":"https://shop.example/p/1"}]}]},
		{"type":"fields","items":[{"label":"Plan","value":"Pro","links":[{"label":"Billing","url":"https://billing.example"}]}]}
	]`))
	want := []PanelLink{
		{Label: "Open in the shop", URL: "https://admin.shopify.com/store/x/orders/1", Icon: "shopify"},
		{Label: "Open in the ERP", URL: "https://erp.example/so/42", Icon: "receipt"},
		{Label: "Carrier", URL: "https://carrier.example/t/1"},
		{Label: "Plain", URL: "https://crm.example/a/7"},
		{Label: clip(strings.Repeat("x", 60), maxLinkLabel), URL: "https://erp.example/long", Icon: "package"},
	}
	if !reflect.DeepEqual(b[0].Links, want) || len([]rune(b[0].Links[4].Label)) != maxLinkLabel {
		t.Fatalf("card links = %+v", b[0].Links)
	}
	if got := b[1].Items[0].Links; !reflect.DeepEqual(got, []PanelLink{{Label: "PDF", URL: "https://billing.example/inv/1.pdf", Icon: "file-text"}}) {
		t.Fatalf("row links = %+v", got)
	}
	// Lines and fields have no links.
	if b[2].Lines[0].Links != nil || b[3].Items[0].Links != nil {
		t.Fatalf("lines %+v, fields %+v", b[2].Lines[0].Links, b[3].Items[0].Links)
	}
	// A product's logo by its name, whatever its case: Odoo's.
	odoo := `[{"type":"card","title":"S00042","links":[{"label":"Open in Odoo","url":"https://acme.odoo.com/odoo/sales/42","icon":" Odoo "}]}]`
	if c := s.cleanBlocks(decodeList(t, odoo)); !reflect.DeepEqual(c[0].Links, []PanelLink{{Label: "Open in Odoo", URL: "https://acme.odoo.com/odoo/sales/42", Icon: "odoo"}}) {
		t.Fatalf("odoo = %+v", c[0].Links)
	}
	// A card without links answers none, and the icon of a cleaned link is always one the app draws.
	if c := s.cleanBlocks(decodeList(t, `[{"type":"card","title":"Bare","links":[]}]`)); c[0].Links != nil {
		t.Fatalf("bare = %+v", c[0].Links)
	}
	for _, l := range b[0].Links {
		if l.Icon != "" && !linkIcons[l.Icon] {
			t.Fatalf("icon %q", l.Icon)
		}
	}
	if raw, _ := json.Marshal(want[2]); string(raw) != `{"label":"Carrier","url":"https://carrier.example/t/1"}` {
		t.Fatalf("json = %s", raw)
	}
}

// TestLinkIconsHaveLogos: every product a link may name has its logo shipped with the app, so
// an icon the server lets through is never a broken picture.
func TestLinkIconsHaveLogos(t *testing.T) {
	generic := map[string]bool{"external-link": true, "store": true, "shopping-cart": true, "receipt": true, "file-text": true, "truck": true,
		"package": true, "user": true, "building": true, "phone": true, "mail": true, "credit-card": true, "map-pin": true}
	for icon := range linkIcons {
		if generic[icon] {
			continue
		}
		if _, err := os.Stat(filepath.Join("..", "..", "..", "web", "public", "logos", "apps", icon+".svg")); err != nil {
			t.Errorf("link icon %q has no logo: %v", icon, err)
		}
	}
	if !linkIcons["odoo"] {
		t.Fatal("odoo is not a link icon")
	}
}

func TestCleanCollapsed(t *testing.T) {
	s := &Service{guard: netsafe.Guard{}}
	b := s.cleanBlocks(decodeList(t, `[
		{"type":"card","title":"Order #1042","collapsed":false,"fields":[{"label":"Status","value":"Late"}]},
		{"type":"card","title":"Order #1041","collapsed":true,"fields":[{"label":"Status","value":"Delivered"}]},
		{"type":"card","title":"Order #1040"},
		{"type":"card","title":"Order #1039","collapsed":"yes"},
		{"type":"card","title":"Order #1038","collapsed":1},
		{"type":"note","text":"Not a card.","collapsed":true}
	]`))
	got := make([]bool, len(b))
	for i, x := range b {
		got[i] = x.Collapsed
	}
	if !reflect.DeepEqual(got, []bool{false, true, false, false, false, false}) || len(b[1].Fields) != 1 {
		t.Fatalf("collapsed = %v", got)
	}
	if raw, _ := json.Marshal(b[1]); !strings.Contains(string(raw), `"collapsed":true`) {
		t.Fatalf("json = %s", raw)
	}
	if raw, _ := json.Marshal(b[0]); strings.Contains(string(raw), `collapsed`) {
		t.Fatalf("json = %s", raw)
	}
}

func TestCleanCardID(t *testing.T) {
	s := &Service{guard: netsafe.Guard{}}
	long := strings.Repeat("x", maxCardID)
	b := s.cleanBlocks(decodeList(t, `[
		{"type":"card","id":"  so-42 ","title":"Order #1042"},
		{"type":"card","id":4471,"title":"Order #1041"},
		{"type":"card","title":"Order #1040"},
		{"type":"card","id":{"odoo":39},"title":"Order #1039"},
		{"type":"card","id":"`+long+`","title":"Order #1038"},
		{"type":"card","id":"`+long+`y","title":"Order #1037"},
		{"type":"note","id":"n1","text":"Not a card."}
	]`))
	got := make([]string, len(b))
	for i, x := range b {
		got[i] = x.ID
	}
	// A string or a number; anything else, or one too long to keep whole, is no id: the card still shows.
	if !reflect.DeepEqual(got, []string{"so-42", "4471", "", "", long, "", ""}) {
		t.Fatalf("ids = %q", got)
	}
	if raw, _ := json.Marshal(b[0]); !strings.Contains(string(raw), `"id":"so-42"`) {
		t.Fatalf("json = %s", raw)
	}
	if raw, _ := json.Marshal(b[2]); strings.Contains(string(raw), `"id"`) {
		t.Fatalf("json = %s", raw)
	}
}

func decodeList(t *testing.T, s string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCacheFor(t *testing.T) {
	for in, want := range map[string]time.Duration{``: cacheTTL, `null`: cacheTTL, `0`: 0, `5`: 5 * time.Second, `12.9`: 12 * time.Second,
		`600`: 10 * time.Minute, `86400`: 10 * time.Minute, `-1`: cacheTTL, `"30"`: cacheTTL} {
		if got := cacheFor(json.RawMessage(in)); got != want {
			t.Errorf("cacheFor(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestReferences(t *testing.T) {
	re := regexp.MustCompile(DefaultReferencePattern)
	got := findReferences(re,
		"Order #1042 late, and SO-2026-0042?",
		"Hi, about S00042 and INV/2026/00012 (paid 129.00 EUR on 2026-10-02). PO4471 too. Call +32 470 12 34 56, 1000 Brussels. #1042 again. E27 GU10 LED3000K ISO 2026",
	)
	want := []string{"#1042", "SO-2026-0042", "S00042", "INV/2026/00012", "PO4471"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("references = %q, want %q", got, want)
	}
	// A group picks what is sent; at most 10, each clipped.
	re = regexp.MustCompile(`(?i)order (\d+)`)
	if got := findReferences(re, "order 5 order 77 ORDER 77"); !reflect.DeepEqual(got, []string{"5", "77"}) {
		t.Fatalf("group = %q", got)
	}
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "#%d ", 1000+i)
	}
	if got := findReferences(regexp.MustCompile(DefaultReferencePattern), b.String()); len(got) != maxReferences || got[9] != "#1009" {
		t.Fatalf("cap = %q", got)
	}
	if got := findReferences(regexp.MustCompile(`#\w+`), "#"+strings.Repeat("a", 100)); len([]rune(got[0])) != maxReferenceLen {
		t.Fatalf("clip = %q", got)
	}
	if got := findReferences(regexp.MustCompile(DefaultReferencePattern), "nothing here"); got == nil || len(got) != 0 {
		t.Fatalf("none = %#v", got)
	}
	// Each text is read to 20,000 characters, all of them to 100,000, and only the first 20
	// matches count, so a pattern that matches everywhere stops early.
	long := strings.Repeat("é", maxScannedText)
	if got := findReferences(regexp.MustCompile(DefaultReferencePattern), long+" #1001", "#1002"); !reflect.DeepEqual(got, []string{"#1002"}) {
		t.Fatalf("one text = %q", got)
	}
	texts := []string{"Order #1001"}
	for range 5 {
		texts = append(texts, long)
	}
	if got := findReferences(regexp.MustCompile(DefaultReferencePattern), append(texts, "#1002")...); !reflect.DeepEqual(got, []string{"#1001"}) {
		t.Fatalf("in all = %q", got)
	}
	if got := findReferences(regexp.MustCompile(DefaultReferencePattern), strings.Repeat("#1001 ", maxMatches)+"#1002"); !reflect.DeepEqual(got, []string{"#1001"}) {
		t.Fatalf("matches = %q", got)
	}
	if got := findReferences(regexp.MustCompile(`[^x]*x|a`), strings.Repeat("a", maxScannedText)); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("everywhere = %q", got)
	}

	// What an admin may save.
	for p, ok := range map[string]bool{DefaultReferencePattern: true, `\bSO\d{5}\b`: true, `(?i)order (\d+)`: true,
		`(unclosed`: false, `\d*`: false, `x?`: false, strings.Repeat("a", 201): false, `(a+)+$`: true, /* RE2: linear, no backtracking */
		`\b(?:SO|INV|PO)[-/]?\d{4,10}\b`: true, `(?i)\border\s*#?\s*([A-Z0-9-]{4,20})`: true,
		`\w{100}x`: false, `\w{1000}x`: false, `[\pL\pN]{1000}#`: false, `(?:\d{1,9}-){9}\d+`: false} {
		if _, err := compilePattern(p); (err == nil) != ok {
			t.Errorf("compilePattern(%q) = %v", p, err)
		}
	}
	if p, err := normalizePattern(" " + DefaultReferencePattern + " "); p != nil || err != nil {
		t.Errorf("the default is stored as NULL: %v %v", p, err)
	}
	if p, err := normalizePattern(""); p == nil || *p != "" || err != nil {
		t.Errorf("off = %v %v", p, err)
	}
}

// TestHTTPPanelBlocks is a version 2 answer end to end: what the request carries (references,
// the contact's addresses and phone, refresh), the cleaned blocks, and the answer's cache time.
func TestHTTPPanelBlocks(t *testing.T) {
	e, ws := apiEnv(t)
	var mu sync.Mutex
	var sent []lookupRequest
	var calls atomic.Int32
	cacheSeconds := atomic.Value{}
	cacheSeconds.Store(`"cache_seconds": 0,`)
	mux := http.NewServeMux()
	mux.HandleFunc("/blocks", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req lookupRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		sent = append(sent, req)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"version":2,`+cacheSeconds.Load().(string)+`"blocks":[
			{"type":"card","title":"Sales order S00042","badges":[{"text":"Supplier late","tone":"warning"}],
			 "fields":[{"label":"Supplier date","value":{"type":"date","value":"2026-11-12","relative":true}}],
			 "lines":[{"quantity":2,"title":"Arco","value":{"type":"money","amount":"1890.00","currency":"eur"}}]},
			{"type":"unknown"},{"type":"note","text":"No open invoice."}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	admin, viewer, agent := ws.Admin.As(ws), ws.Viewer.As(ws), ws.Agent.As(ws)

	var created struct {
		Panel  Panel  `json:"panel"`
		Secret string `json:"secret"`
	}
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/blocks", "reference_pattern": `(unclosed`}).Want(t, 422)
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/blocks", "reference_pattern": `\w{1000}x`}).Want(t, 422)
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/blocks"}).Want(t, 201).JSON(t, &created)
	if created.Panel.ReferencePattern == nil || *created.Panel.ReferencePattern != DefaultReferencePattern {
		t.Fatalf("default pattern = %v", created.Panel.ReferencePattern)
	}
	id := created.Panel.ID.String()

	var contact struct {
		ID string `json:"id"`
	}
	e.Do("POST", "/v1/contacts", agent, map[string]any{"email": "Ada@Example.com", "name": "Ada Buyer", "phone": "+32 470 12 34 56"}).Want(t, 201).JSON(t, &contact)
	var tk struct {
		ID string `json:"id"`
	}
	e.Do("POST", "/v1/tickets", agent, map[string]any{"subject": "Order SO-2026-0042 is late", "contact_id": contact.ID,
		"body_html": "<p>Hi, it is about S00042 and invoice INV/2026/00012. Paid 129.00 EUR in 2026.</p>"}).Want(t, 201).JSON(t, &tk)
	path := "/v1/tickets/" + tk.ID + "/panels/" + id

	d := readPanel(t, e, path, viewer)
	if d.State != "ok" || len(d.Blocks) != 2 || d.Sections != nil {
		t.Fatalf("data = %+v", d)
	}
	card := d.Blocks[0]
	if card.Type != "card" || card.Badges[0].Tone != "warning" || card.Fields[0].Value.Type != "date" || card.Fields[0].Value.Value != "2026-11-12" ||
		!card.Fields[0].Value.Relative || *card.Lines[0].Quantity != 2 || card.Lines[0].Value.Currency != "EUR" || d.Blocks[1].Text != "No open invoice." {
		t.Fatalf("blocks = %+v", d.Blocks)
	}
	mu.Lock()
	req := sent[len(sent)-1]
	mu.Unlock()
	if !reflect.DeepEqual(req.Ticket.References, []string{"SO-2026-0042", "S00042", "INV/2026/00012"}) {
		t.Fatalf("references = %q", req.Ticket.References)
	}
	if !reflect.DeepEqual(req.Contact.Emails, []string{"ada@example.com"}) || req.Contact.Phone != "+32 470 12 34 56" || req.Refresh {
		t.Fatalf("contact = %+v, refresh %v", req.Contact, req.Refresh)
	}

	// cache_seconds 0: asked on every open. Refresh says so.
	readPanel(t, e, path, viewer)
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 (not kept)", calls.Load())
	}
	readPanel(t, e, path+"?refresh=true", viewer)
	mu.Lock()
	refreshed := sent[len(sent)-1].Refresh
	mu.Unlock()
	if !refreshed {
		t.Fatal("refresh not sent")
	}

	// The default minute: the next open answers from the copy, until a new reference shows.
	cacheSeconds.Store("")
	readPanel(t, e, path+"?refresh=true", viewer)
	n := calls.Load()
	readPanel(t, e, path, viewer)
	if calls.Load() != n {
		t.Fatal("not kept for a minute")
	}
	e.Do("POST", "/v1/tickets/"+tk.ID+"/messages", agent, map[string]any{"kind": "note", "body_html": "<p>Also see #10442</p>"}).Want(t, 201)
	readPanel(t, e, path, viewer)
	mu.Lock()
	req = sent[len(sent)-1]
	mu.Unlock()
	if calls.Load() != n+1 || !slices.Contains(req.Ticket.References, "#10442") {
		t.Fatalf("a new reference: calls %d, refs %q", calls.Load()-n, req.Ticket.References)
	}

	// The workspace's own pattern, then none; Shopify panels have none.
	// Deleted messages and notes sent as replies are read by admins alone: none of their
	// references is sent.
	var note struct {
		ID string `json:"id"`
	}
	e.Do("POST", "/v1/tickets/"+tk.ID+"/messages", agent, map[string]any{"kind": "note", "body_html": "<p>Wrong customer: #30001</p>"}).Want(t, 201).JSON(t, &note)
	e.Do("DELETE", "/v1/tickets/"+tk.ID+"/messages/"+note.ID, agent, nil).Want(t, 204)
	e.Do("POST", "/v1/tickets/"+tk.ID+"/messages", agent, map[string]any{"kind": "note", "body_html": "<p>Draft about #30002</p>"}).Want(t, 201).JSON(t, &note)
	e.Do("POST", "/v1/tickets/"+tk.ID+"/messages", agent, map[string]any{"kind": "reply", "body_html": "<p>It is on its way.</p>", "from_note_id": note.ID}).Want(t, 201)
	readPanel(t, e, path+"?refresh=true", viewer)
	mu.Lock()
	req = sent[len(sent)-1]
	mu.Unlock()
	if slices.Contains(req.Ticket.References, "#30001") || slices.Contains(req.Ticket.References, "#30002") || !slices.Contains(req.Ticket.References, "#10442") {
		t.Fatalf("hidden messages = %q", req.Ticket.References)
	}

	e.Do("PATCH", "/v1/ticket-panels/"+id, admin, map[string]any{"reference_pattern": `\d*`}).Want(t, 422)
	e.Do("PATCH", "/v1/ticket-panels/"+id, admin, map[string]any{"reference_pattern": `\w{1000}x`}).Want(t, 422)
	var p Panel
	e.Do("PATCH", "/v1/ticket-panels/"+id, admin, map[string]any{"reference_pattern": `\bINV/(\d{4}/\d+)`}).Want(t, 200).JSON(t, &p)
	if *p.ReferencePattern != `\bINV/(\d{4}/\d+)` {
		t.Fatalf("pattern = %q", *p.ReferencePattern)
	}
	readPanel(t, e, path, viewer)
	mu.Lock()
	req = sent[len(sent)-1]
	mu.Unlock()
	if !reflect.DeepEqual(req.Ticket.References, []string{"2026/00012"}) {
		t.Fatalf("own pattern = %q", req.Ticket.References)
	}
	e.Do("PATCH", "/v1/ticket-panels/"+id, admin, map[string]any{"reference_pattern": ""}).Want(t, 200).JSON(t, &p)
	readPanel(t, e, path, viewer)
	mu.Lock()
	req = sent[len(sent)-1]
	mu.Unlock()
	if *p.ReferencePattern != "" || req.Ticket.References == nil || len(req.Ticket.References) != 0 {
		t.Fatalf("none = %q, %#v", *p.ReferencePattern, req.Ticket.References)
	}
	e.Do("PATCH", "/v1/ticket-panels/"+id, admin, map[string]any{"reference_pattern": DefaultReferencePattern}).Want(t, 200).JSON(t, &p)
	if *p.ReferencePattern != DefaultReferencePattern {
		t.Fatalf("default again = %q", *p.ReferencePattern)
	}
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "shopify", "shop": "x", "token": "t", "reference_pattern": "x"}).Want(t, 422)

	// A ticket without a contact: no addresses, no phone, still the references.
	e.Do("POST", "/v1/tickets", agent, map[string]any{"subject": "About #20001", "body_html": "Hi"}).Want(t, 201).JSON(t, &tk)
	readPanel(t, e, "/v1/tickets/"+tk.ID+"/panels/"+id, viewer)
	mu.Lock()
	req = sent[len(sent)-1]
	mu.Unlock()
	if req.Contact.ID != nil || req.Contact.Emails == nil || len(req.Contact.Emails) != 0 || req.Contact.Phone != "" || !slices.Contains(req.Ticket.References, "#20001") {
		t.Fatalf("no contact = %+v %q", req.Contact, req.Ticket.References)
	}
}

// TestHTTPPanelLocale: the request carries the reader's language as the app said it, and an
// answer is kept per language, so a reader never gets a copy made in another language.
func TestHTTPPanelLocale(t *testing.T) {
	e, ws := apiEnv(t)
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req lookupRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		text, ok := map[string]string{"fr": "Commande en route.", "de": "Bestellung unterwegs."}[req.Locale]
		if !ok {
			text = "Order on its way."
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 2, "blocks": []any{map[string]any{"type": "note", "text": text}}})
	}))
	defer srv.Close()
	admin, viewer := ws.Admin.As(ws), ws.Viewer.As(ws)
	var created struct {
		Panel Panel `json:"panel"`
	}
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/panel"}).Want(t, 201).JSON(t, &created)
	path := "/v1/tickets/" + newTicket(t, e, ws.Agent.As(ws), "ada@example.com", nil) + "/panels/" + created.Panel.ID.String()
	note := func(query string) string {
		t.Helper()
		d := readPanel(t, e, path+query, viewer)
		if d.State != "ok" || len(d.Blocks) != 1 {
			t.Fatalf("%s: data = %+v", query, d)
		}
		return d.Blocks[0].Text
	}
	asked := func() (int, string) {
		mu.Lock()
		defer mu.Unlock()
		return len(bodies), bodies[len(bodies)-1]
	}

	// No language said: the field is left out.
	if got := note(""); got != "Order on its way." {
		t.Fatalf("no locale = %q", got)
	}
	if _, raw := asked(); strings.Contains(raw, `"locale"`) {
		t.Fatalf("request = %s", raw)
	}
	// French, then German: each asks the URL, which answers in it.
	if got := note("?locale=fr"); got != "Commande en route." {
		t.Fatalf("fr = %q", got)
	}
	if _, raw := asked(); !strings.Contains(raw, `"locale":"fr"`) {
		t.Fatalf("request = %s", raw)
	}
	if got := note("?locale=de"); got != "Bestellung unterwegs." {
		t.Fatalf("de = %q", got)
	}
	n, _ := asked()
	if n != 3 {
		t.Fatalf("asked %d times, want 3: one per language", n)
	}
	// Each language's copy answers its own readers for the minute.
	for query, want := range map[string]string{"?locale=fr": "Commande en route.", "?locale=de": "Bestellung unterwegs.", "": "Order on its way."} {
		if got := note(query); got != want {
			t.Fatalf("%q kept = %q, want %q", query, got, want)
		}
	}
	// Anything but a lowercase base language tag is ignored: the copy made without a language.
	for _, bad := range []string{"FR", "fr-BE", "french", "f", "fr%0A", "%2E%2E"} {
		if got := note("?locale=" + bad); got != "Order on its way." {
			t.Fatalf("%q = %q", bad, got)
		}
	}
	if m, _ := asked(); m != n {
		t.Fatalf("asked %d more times, want none", m-n)
	}
	// Refresh asks again in the reader's language.
	note("?locale=fr&refresh=true")
	if m, raw := asked(); m != n+1 || !strings.Contains(raw, `"locale":"fr"`) || !strings.Contains(raw, `"refresh":true`) {
		t.Fatalf("refresh: asked %d, request %s", m-n, raw)
	}
}

// TestTicketReadingIsKept: an http panel reads a ticket once per version of the panel and the
// ticket. A kept reading answers without the database or a lookup of the panel's; a new one
// takes the panel's lookup before it reads, and the lookup it leads to asks with that one. A
// cancelled context shows when the database is read: the reading answers "Couldn't read the
// ticket."
func TestTicketReadingIsKept(t *testing.T) {
	e, ws := apiEnv(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req lookupRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		text := strings.Join(req.Ticket.References, " ") + " | " + strings.Join(req.Contact.Emails, " ")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 2, "blocks": []any{map[string]any{"type": "note", "text": text}}})
	}))
	defer srv.Close()
	admin, agent := ws.Admin.As(ws), ws.Agent.As(ws)
	newPanel := func() *row {
		var created struct {
			Panel Panel `json:"panel"`
		}
		e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/panel"}).Want(t, 201).JSON(t, &created)
		x, err := loadRow(e.Ctx, e.D.DB.Pool, ws.ID, created.Panel.ID)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	spend := func(x *row, n int) {
		for range n {
			e.D.Limiter.Allow("ticket-panel:lookup:"+x.ID.String(), lookupsPerMinute, time.Minute)
		}
	}
	const contactEmail = "ada@example.com"
	var contact, tk struct {
		ID uuid.UUID `json:"id"`
	}
	e.Do("POST", "/v1/contacts", agent, map[string]any{"email": contactEmail, "name": "Ada Buyer"}).Want(t, 201).JSON(t, &contact)
	e.Do("POST", "/v1/tickets", agent, map[string]any{"subject": "Order #1001", "contact_id": contact.ID, "body_html": "<p>Hi</p>"}).Want(t, 201).JSON(t, &tk)
	ticket := func() *domain.Ticket {
		tt, err := domain.LoadTicket(e.Ctx, e.D.DB.Pool, ws.ID, tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		return &tt
	}
	shown := func(d Data) string {
		if d.State != "ok" || len(d.Blocks) != 1 {
			return d.State + ": " + d.Message
		}
		return d.Blocks[0].Text
	}
	s := For(e.D)
	cancelled, cancel := context.WithCancel(e.Ctx)
	cancel()
	const unread, refused = "error: Couldn't read the ticket.", "error: Too many lookups. Try again in a minute."

	if got := shown(s.read(cancelled, newPanel(), ticket(), false, "")); got != unread {
		t.Fatalf("a new reading without the database = %q", got)
	}

	// One lookup left: the new reading takes it, and the lookup asks with it.
	x := newPanel()
	spend(x, lookupsPerMinute-1)
	want := "#1001 | " + contactEmail
	if got := shown(s.read(e.Ctx, x, ticket(), false, "")); got != want || calls.Load() != 1 {
		t.Fatalf("the last lookup = %q, %d calls", got, calls.Load())
	}
	// None left: the kept reading and answer still answer, without the database.
	for range 3 {
		if got := shown(s.read(cancelled, x, ticket(), false, "")); got != want || calls.Load() != 1 {
			t.Fatalf("a repeated open = %q, %d calls", got, calls.Load())
		}
	}
	// A new version of the ticket is read again: refused before the database is read.
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", agent, map[string]any{"kind": "note", "body_html": "<p>See #1002</p>"}).Want(t, 201)
	for _, ctx := range []context.Context{cancelled, e.Ctx} {
		if got := shown(s.read(ctx, x, ticket(), false, "")); got != refused || calls.Load() != 1 {
			t.Fatalf("a new version with no lookup left = %q, %d calls", got, calls.Load())
		}
	}

	// A deleted note and a note sent as a reply move the ticket: their references leave at once.
	x = newPanel()
	var note struct {
		ID string `json:"id"`
	}
	notes := "/v1/tickets/" + tk.ID.String() + "/messages"
	e.Do("POST", notes, agent, map[string]any{"kind": "note", "body_html": "<p>Wrong customer: #3001</p>"}).Want(t, 201).JSON(t, &note)
	if got := shown(s.read(e.Ctx, x, ticket(), false, "")); got != "#1001 #3001 #1002 | "+contactEmail {
		t.Fatalf("a new note = %q", got)
	}
	before := ticket()
	e.Do("DELETE", notes+"/"+note.ID, agent, nil).Want(t, 204)
	after := ticket()
	if got := shown(s.read(e.Ctx, x, after, false, "")); got != "#1001 #1002 | "+contactEmail || !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("a deleted note = %q, updated_at %v then %v", got, before.UpdatedAt, after.UpdatedAt)
	}
	e.Do("POST", notes, agent, map[string]any{"kind": "note", "body_html": "<p>Draft about #3002</p>"}).Want(t, 201).JSON(t, &note)
	if got := shown(s.read(e.Ctx, x, ticket(), false, "")); got != "#1001 #3002 #1002 | "+contactEmail {
		t.Fatalf("a new note = %q", got)
	}
	before = ticket()
	e.Do("POST", notes, agent, map[string]any{"kind": "reply", "body_html": "<p>It is on its way.</p>", "from_note_id": note.ID}).Want(t, 201)
	after = ticket()
	if got := shown(s.read(e.Ctx, x, after, false, "")); got != "#1001 #1002 | "+contactEmail || !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("a note sent as a reply = %q, updated_at %v then %v", got, before.UpdatedAt, after.UpdatedAt)
	}

	// An incoming message moves the ticket: its reference and address are read at once.
	before = ticket()
	if got := shown(s.read(e.Ctx, x, before, false, "")); got != "#1001 #1002 | "+contactEmail {
		t.Fatalf("before = %q", got)
	}
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		_, err := domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tk.ID, Kind: "reply", Direction: "inbound",
			AuthorType: "contact", AuthorContactID: &contact.ID, FromEmail: "ada.work@example.com", BodyHTML: "<p>And #1003</p>", BodyText: "And #1003"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after = ticket()
	if got := shown(s.read(e.Ctx, x, after, false, "")); got != "#1001 #1003 #1002 | "+contactEmail+" ada.work@example.com" || !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("after an incoming message = %q, updated_at %v then %v", got, before.UpdatedAt, after.UpdatedAt)
	}
}

// TestTicketReadingsShareTheWorkspaceBudget: a workspace's http panels read tickets
// readingsPerMinute times a minute together, whichever panel reads and whether a refresh, a new
// ticket or a new version of one asks. Past them a reading is refused before the database is
// read; a kept reading still answers, and asks the URL again when its answer is not kept. Another
// workspace reads on.
func TestTicketReadingsShareTheWorkspaceBudget(t *testing.T) {
	e, ws := apiEnv(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req lookupRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		text := strings.Join(req.Ticket.References, " ")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 2, "cache_seconds": 0, "blocks": []any{map[string]any{"type": "note", "text": text}}})
	}))
	defer srv.Close()
	newPanel := func(ws hdtest.WS) *row {
		var created struct {
			Panel Panel `json:"panel"`
		}
		e.Do("POST", "/v1/ticket-panels", ws.Admin.As(ws), map[string]any{"kind": "http", "url": srv.URL + "/panel"}).Want(t, 201).JSON(t, &created)
		x, err := loadRow(e.Ctx, e.D.DB.Pool, ws.ID, created.Panel.ID)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	openTicket := func(ws hdtest.WS) func() *domain.Ticket {
		var tk struct {
			ID uuid.UUID `json:"id"`
		}
		e.Do("POST", "/v1/tickets", ws.Agent.As(ws), map[string]any{"subject": "Order #1001", "contact": map[string]any{"email": "ada@example.com"}, "body_html": "<p>Hi</p>"}).Want(t, 201).JSON(t, &tk)
		return func() *domain.Ticket {
			tt, err := domain.LoadTicket(e.Ctx, e.D.DB.Pool, ws.ID, tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			return &tt
		}
	}
	shown := func(d Data) string {
		if d.State != "ok" || len(d.Blocks) != 1 {
			return d.State + ": " + d.Message
		}
		return d.Blocks[0].Text
	}
	s := For(e.D)
	cancelled, cancel := context.WithCancel(e.Ctx)
	cancel()
	const refused = "error: Too many lookups. Try again in a minute."

	// Three panels, each far below its own lookups, spend the workspace's readings: a viewer's
	// refreshes of one ticket through the API, after each panel's first reading.
	panels := []*row{newPanel(ws), newPanel(ws), newPanel(ws)}
	ticket := openTicket(ws)
	viewer := ws.Viewer.As(ws)
	path := func(x *row) string { return "/v1/tickets/" + ticket().ID.String() + "/panels/" + x.ID.String() }
	for i := range readingsPerMinute {
		x, q := panels[i%len(panels)], "?refresh=true"
		if i < len(panels) {
			q = ""
		}
		if got := shown(readPanel(t, e, path(x)+q, viewer)); got != "#1001" {
			t.Fatalf("reading %d = %q", i+1, got)
		}
	}
	if calls.Load() != readingsPerMinute {
		t.Fatalf("%d calls for %d readings", calls.Load(), readingsPerMinute)
	}

	// None left: a refresh, another panel and a new ticket are refused before the database is
	// read (a cancelled context would answer "Couldn't read the ticket."), on any panel.
	if got := shown(readPanel(t, e, path(panels[0])+"?refresh=true", viewer)); got != refused {
		t.Fatalf("a refresh past the workspace's readings = %q", got)
	}
	other := openTicket(ws)
	for _, read := range []struct {
		name string
		x    *row
		t    *domain.Ticket
		r    bool
	}{{"a refresh", panels[1], ticket(), true}, {"a new panel", newPanel(ws), ticket(), false}, {"a new ticket", panels[2], other(), false}} {
		if got := shown(s.read(cancelled, read.x, read.t, read.r, "")); got != refused {
			t.Fatalf("%s past the workspace's readings = %q", read.name, got)
		}
	}
	// A kept reading still answers, and asks the URL again with the panel's lookup alone (the
	// answer is not kept): a new reading would be refused.
	before := calls.Load()
	for _, x := range panels {
		if got := shown(s.read(e.Ctx, x, ticket(), false, "")); got != "#1001" {
			t.Fatalf("a kept reading = %q", got)
		}
	}
	if calls.Load() != before+int32(len(panels)) {
		t.Fatalf("kept readings made %d calls", calls.Load()-before)
	}
	// A new version of the ticket is refused before the database too.
	e.Do("POST", "/v1/tickets/"+ticket().ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "note", "body_html": "<p>See #1002</p>"}).Want(t, 201)
	for _, ctx := range []context.Context{cancelled, e.Ctx} {
		if got := shown(s.read(ctx, panels[0], ticket(), false, "")); got != refused {
			t.Fatalf("a new version past the workspace's readings = %q", got)
		}
	}

	// Another workspace's panels read on.
	ws2 := e.NewWorkspace()
	if got := shown(s.read(e.Ctx, newPanel(ws2), openTicket(ws2)(), true, "")); got != "#1001" {
		t.Fatalf("another workspace = %q", got)
	}
}
