package panels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

func TestMain(m *testing.M) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		os.Setenv("TEST_DATABASE_URL", "postgres://opendesk:opendesk@localhost:55432/opendesk_panels?sslmode=disable")
	}
	hdtest.Main(m)
}

func apiEnv(t *testing.T) (*hdtest.Env, hdtest.WS) {
	t.Helper()
	e := hdtest.New(t, core.Register, helpdesk.Register, Register)
	return e, e.NewWorkspace()
}

// newTicket opens a ticket from contact (no contact when email is empty) and answers its id.
func newTicket(t *testing.T, e *hdtest.Env, a hdtest.Auth, email string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"subject": "Where is my order?", "body_html": "Hi"}
	if email != "" {
		body["contact"] = map[string]any{"email": email, "name": "Ada Buyer"}
	}
	for k, v := range extra {
		body[k] = v
	}
	var tk struct {
		ID string `json:"id"`
	}
	e.Do("POST", "/v1/tickets", a, body).Want(t, 201).JSON(t, &tk)
	return tk.ID
}

// readPanel reads a panel for a ticket into a fresh value.
func readPanel(t *testing.T, e *hdtest.Env, path string, a hdtest.Auth) Data {
	t.Helper()
	var d Data
	e.Do("GET", path, a, nil).Want(t, 200).JSON(t, &d)
	return d
}

// fakeShopify answers a shop's token endpoint (the client credentials grant, for the app
// "app-id") and its Admin GraphQL API, which knows one customer.
type fakeShopify struct {
	mu      sync.Mutex
	valid   map[string]bool // the Admin API tokens the shop accepts
	secret  string          // the app's client secret
	scope   string          // what the app's version grants
	issued  atomic.Int32
	lookups atomic.Int32
	// noAddress answers as for an app without access to customer addresses: a null
	// shippingAddress with an access error for it.
	noAddress bool
	// query and vars are the last lookup's.
	query string
	vars  map[string]any
}

func newFakeShopify(tokens ...string) *fakeShopify {
	f := &fakeShopify{secret: "app-secret", scope: "read_customers,read_orders"}
	f.only(tokens...)
	return f
}

// only makes the shop accept these tokens alone, revoking the others.
func (f *fakeShopify) only(tokens ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.valid = map[string]bool{}
	for _, t := range tokens {
		f.valid[t] = true
	}
}

func (f *fakeShopify) withoutAddresses(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noAddress = on
}

func (f *fakeShopify) last() (string, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.query, f.vars
}

func (f *fakeShopify) set(secret, scope string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secret, f.scope = secret, scope
}

func (f *fakeShopify) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/admin/oauth/access_token" {
		_ = r.ParseForm()
		switch {
		case r.PostForm.Get("grant_type") != "client_credentials" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded"):
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		case r.PostForm.Get("client_id") == "other-org":
			http.Error(w, `{"error":"shop_not_permitted","error_description":"Client credentials cannot be performed on this shop."}`, http.StatusBadRequest)
		case r.PostForm.Get("client_id") != "app-id" || r.PostForm.Get("client_secret") != f.secret:
			http.Error(w, `{"error":"invalid_client"}`, http.StatusBadRequest)
		default:
			tok := fmt.Sprintf("shpat_app_%d", f.issued.Add(1))
			f.valid[tok] = true
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "scope": f.scope, "expires_in": 86399})
		}
		return
	}
	if r.URL.Path != "/admin/api/"+shopifyVersion+"/graphql.json" {
		http.NotFound(w, r)
		return
	}
	if !f.valid[r.Header.Get("X-Shopify-Access-Token")] {
		http.Error(w, `{"errors":"[API] Invalid API key or access token"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.Contains(req.Query, "shop {") {
		_, _ = io.WriteString(w, `{"data":{"shop":{"name":"Lumen Store"}}}`)
		return
	}
	f.lookups.Add(1)
	f.query, f.vars = req.Query, req.Variables
	if req.Variables["q"] != `email:"ada@example.com"` {
		// A phrase search can still return a near match: it must not be taken.
		_, _ = io.WriteString(w, `{"data":{"customers":{"nodes":[{"id":"gid://shopify/Customer/9","displayName":"Ada Other",
			"defaultEmailAddress":{"emailAddress":"ada.other@example.com"},"numberOfOrders":"1","amountSpent":{"amount":"5.00","currencyCode":"EUR"},
			"tags":[],"orders":{"nodes":[]}}]}}}`)
		return
	}
	// Shopify leaves out what the query does not ask for: the tags here. The rest of the
	// answer is whole whatever the query, so that only what the panel shows is told.
	tags := ""
	if strings.Contains(req.Query, "tags") {
		tags = `"tags":["VIP","newsletter"],`
	}
	address, errs := `{"city":" Brussels ","countryCodeV2":"be"}`, ""
	if f.noAddress {
		address = "null"
		errs = `,"errors":[{"message":"Access denied for shippingAddress field.","extensions":{"code":"ACCESS_DENIED"},
			"path":["customers","nodes",0,"orders","nodes",0,"shippingAddress"]}]`
	}
	orders := []string{`{"id":"gid://shopify/Order/1001","name":"#1042","createdAt":"2026-10-20T09:00:00Z","cancelledAt":null,
		"displayFinancialStatus":"PARTIALLY_REFUNDED","displayFulfillmentStatus":"FULFILLED",
		"currentTotalPriceSet":{"shopMoney":{"amount":"119.32","currencyCode":"EUR"}},
		"totalRefundedSet":{"shopMoney":{"amount":"14.44","currencyCode":"EUR"}},
		"discountCodes":["WELCOME10"," "],"shippingLine":{"title":"DHL Express"},"shippingAddress":` + address + `,
		"lineItems":{"nodes":[{"title":"Arco lamp","variantTitle":"Black","quantity":1,"originalUnitPriceSet":{"shopMoney":{"amount":"129.00","currencyCode":"EUR"}}},
			{"title":"Lucide Leanne Wall Lamp","variantTitle":null,"quantity":3,"originalUnitPriceSet":{"shopMoney":{"amount":"14.44","currencyCode":"EUR"}}}]},
		"fulfillments":[{"trackingInfo":[{"company":"DHL","number":"JD0123","url":"https://dhl.example/track/JD0123"},{"company":null,"number":null,"url":"javascript:alert(1)"}]}]}`,
		`{"id":"gid://shopify/Order/998","name":"#1039","createdAt":"2026-09-02T14:00:00Z","cancelledAt":"2026-09-03T08:30:00Z",
		"displayFinancialStatus":"REFUNDED","displayFulfillmentStatus":"UNFULFILLED",
		"currentTotalPriceSet":{"shopMoney":{"amount":"0.00","currencyCode":"EUR"}},
		"totalRefundedSet":{"shopMoney":{"amount":"0.00","currencyCode":"EUR"}},
		"discountCodes":[],"shippingLine":null,"shippingAddress":null,
		"lineItems":{"nodes":[]},"fulfillments":[]}`}
	if n, ok := req.Variables["orders"].(float64); ok && int(n) < len(orders) {
		orders = orders[:int(n)]
	}
	_, _ = io.WriteString(w, `{"data":{"customers":{"nodes":[{"id":"gid://shopify/Customer/42","displayName":"Ada Buyer",
		"defaultEmailAddress":{"emailAddress":"Ada@Example.com"},"numberOfOrders":"3","amountSpent":{"amount":"412.50","currencyCode":"EUR"},
		`+tags+`"orders":{"nodes":[`+strings.Join(orders, ",")+`]}}]}}`+errs+`}`)
}

func TestShopifyPanel(t *testing.T) {
	e, ws := apiEnv(t)
	shop := newFakeShopify("shpat_good")
	srv := httptest.NewServer(shop)
	defer srv.Close()
	s := For(e.D)
	s.shopifyBase = func(string) string { return srv.URL }
	admin, agent, viewer := ws.Admin.As(ws), ws.Agent.As(ws), ws.Viewer.As(ws)

	body := map[string]any{"kind": "shopify", "shop": "https://Lumen-Store.myshopify.com/admin", "token": "shpat_good"}
	e.Do("POST", "/v1/ticket-panels", agent, body).Want(t, 403)
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "shopify", "shop": "evil.example.com", "token": "x"}).Want(t, 422)
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "shopify", "shop": "lumen-store", "token": "wrong"}).Want(t, 422)
	_, seq := e.Events(ws.ID, 0)
	var created struct {
		Panel  Panel  `json:"panel"`
		Secret string `json:"secret"`
	}
	e.Do("POST", "/v1/ticket-panels", admin, body).Want(t, 201).JSON(t, &created)
	p := created.Panel
	if p.Kind != "shopify" || p.Name != "Lumen Store" || p.Host != "lumen-store.myshopify.com" || p.Status != "connected" || created.Secret != "" ||
		p.Auth != "access_token" || p.ClientID != "" {
		t.Fatalf("panel = %+v, secret %q", p, created.Secret)
	}
	if types, _ := e.Events(ws.ID, seq); !slices.Contains(types, "ticket_panel.created") {
		t.Fatalf("events = %v", types)
	}
	var sealed string
	e.Scalar(`SELECT encode(secret_enc, 'escape') FROM ticket_panels WHERE id = $1`, &sealed, p.ID)
	if strings.Contains(sealed, "shpat_good") {
		t.Fatal("token stored in the clear")
	}
	var auditN int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ticket_panel.connected' AND NOT (data::text LIKE '%shpat%')`, &auditN, ws.ID)
	if auditN != 1 {
		t.Fatalf("audit entries = %d", auditN)
	}
	list := e.Do("GET", "/v1/ticket-panels", viewer, nil).Want(t, 200)
	if strings.Contains(string(list.Body), "shpat") {
		t.Fatalf("list leaks: %s", list.Body)
	}

	// The ticket's contact: the customer, the last order, tracking, links into the admin.
	tid := newTicket(t, e, agent, "ada@example.com", nil)
	path := "/v1/tickets/" + tid + "/panels/" + p.ID.String()
	var d Data
	var status string
	d = readPanel(t, e, path, viewer)
	if d.State != "ok" || d.Customer == nil || d.Customer.Name != "Ada Buyer" || d.Customer.OrdersCount == nil || *d.Customer.OrdersCount != 3 ||
		d.Customer.TotalSpent != "412.50" || d.Customer.Email != "" ||
		d.Customer.URL != "https://admin.shopify.com/store/lumen-store/customers/42" || !slices.Equal(d.Customer.Tags, []string{"VIP", "newsletter"}) {
		t.Fatalf("customer = %+v (%s %s)", d.Customer, d.State, d.Message)
	}
	// Until an admin chooses, a panel shows what it always has: everything but the email, 5 orders.
	if query, vars := shop.last(); !slices.Equal(p.Fields, defaultFields) || p.OrdersShown != 5 || vars["orders"] != 5.0 || query != customerQuery(newFieldSet(defaultFields)) {
		t.Fatalf("panel %v %d, asked %v", p.Fields, p.OrdersShown, vars)
	}
	if len(d.Orders) != 2 {
		t.Fatalf("orders = %+v", d.Orders)
	}
	// Each line with its unit price; the order links to the shop's admin by its id.
	o := d.Orders[0]
	if o.Number != "#1042" || o.Total != "119.32" || o.Currency != "EUR" || o.FinancialStatus != "partially_refunded" || o.FulfillmentStatus != "fulfilled" ||
		o.URL != "https://admin.shopify.com/store/lumen-store/orders/1001" || len(o.Items) != 2 || o.Cancelled || o.CancelledAt != nil {
		t.Fatalf("order = %+v", o)
	}
	if it := o.Items; it[0] != (OrderItem{Title: "Arco lamp", Variant: "Black", Quantity: 1, Price: "129.00"}) ||
		it[1] != (OrderItem{Title: "Lucide Leanne Wall Lamp", Quantity: 3, Price: "14.44"}) {
		t.Fatalf("items = %+v", it)
	}
	// Where it goes (city and country, never the street) and how; codes, and what was refunded.
	if o.Shipping == nil || *o.Shipping != (Shipping{City: "Brussels", Country: "BE", Method: "DHL Express"}) {
		t.Fatalf("shipping = %+v", o.Shipping)
	}
	if !slices.Equal(o.DiscountCodes, []string{"WELCOME10"}) || o.Refunded != "14.44" {
		t.Fatalf("discounts %v, refunded %q", o.DiscountCodes, o.Refunded)
	}
	// A cancelled order says when; nothing refunded, no address and no method leave those out.
	c := d.Orders[1]
	if !c.Cancelled || c.CancelledAt == nil || !c.CancelledAt.Equal(time.Date(2026, 9, 3, 8, 30, 0, 0, time.UTC)) || c.Refunded != "" ||
		c.Shipping != nil || c.DiscountCodes == nil || len(c.DiscountCodes) != 0 || c.Items == nil || c.Tracking == nil ||
		c.URL != "https://admin.shopify.com/store/lumen-store/orders/998" {
		t.Fatalf("cancelled order = %+v", c)
	}
	raw := string(e.Do("GET", path, viewer, nil).Want(t, 200).Body)
	if !strings.Contains(raw, `"discount_codes":[]`) || strings.Contains(raw, "street") {
		t.Fatalf("answer = %s", raw)
	}
	// A parcel whose only detail is a link that isn't https shows nothing, so it is left out.
	if len(o.Tracking) != 1 || o.Tracking[0].URL != "https://dhl.example/track/JD0123" || o.Tracking[0].Company != "DHL" {
		t.Fatalf("tracking = %+v", o.Tracking)
	}
	// The next open is answered from the cache; refresh asks again.
	n := shop.lookups.Load()
	e.Do("GET", path, viewer, nil).Want(t, 200)
	if shop.lookups.Load() != n {
		t.Fatal("second open was not cached")
	}
	e.Do("GET", path+"?refresh=true", viewer, nil).Want(t, 200)
	if shop.lookups.Load() != n+1 {
		t.Fatal("refresh did not ask again")
	}

	// An admin chooses what shows: what is left out is not asked for, nor told, and the changed
	// panel never answers from before.
	id := "/v1/ticket-panels/" + p.ID.String()
	few := map[string]any{"fields": []string{"line_items", "orders_count", "line_items"}, "orders_shown": 1}
	e.Do("PATCH", id, agent, few).Want(t, 403)
	e.Do("PATCH", id, admin, map[string]any{"fields": []string{"tags", "notes"}}).Want(t, 422)
	if r := e.Do("PATCH", id, admin, map[string]any{"fields": []string{"line_prices"}}).Want(t, 422); !strings.Contains(string(r.Body), "line_items") {
		t.Fatalf("prices without lines: %s", r.Body)
	}
	e.Do("PATCH", id, admin, map[string]any{"orders_shown": 0}).Want(t, 422)
	e.Do("PATCH", id, admin, map[string]any{"orders_shown": 6}).Want(t, 422)
	_, seq = e.Events(ws.ID, 0)
	var chosen Panel
	e.Do("PATCH", id, admin, few).Want(t, 200).JSON(t, &chosen)
	if !slices.Equal(chosen.Fields, []string{"orders_count", "line_items"}) || chosen.OrdersShown != 1 {
		t.Fatalf("chosen = %v %d", chosen.Fields, chosen.OrdersShown)
	}
	if types, _ := e.Events(ws.ID, seq); !slices.Contains(types, "ticket_panel.updated") {
		t.Fatalf("events = %v", types)
	}
	d = readPanel(t, e, path, viewer)
	query, vars := shop.last()
	for _, gone := range []string{"tags", "amountSpent", "displayFinancialStatus", "displayFulfillmentStatus", "totalRefundedSet", "discountCodes",
		"shippingLine", "shippingAddress", "originalUnitPriceSet", "fulfillments"} {
		if strings.Contains(query, gone) {
			t.Errorf("asked for %s:\n%s", gone, query)
		}
	}
	if !strings.Contains(query, "numberOfOrders") || !strings.Contains(query, "lineItems(first: $items)") || vars["orders"] != 1.0 {
		t.Fatalf("query %v:\n%s", vars, query)
	}
	if d.State != "ok" || d.Customer == nil || d.Customer.OrdersCount == nil || *d.Customer.OrdersCount != 3 || d.Customer.TotalSpent != "" ||
		d.Customer.Currency != "" || d.Customer.Tags == nil || len(d.Customer.Tags) != 0 || len(d.Orders) != 1 {
		t.Fatalf("few = %+v %+v (%s)", d.Customer, d.Orders, d.Message)
	}
	if o := d.Orders[0]; o.Number != "#1042" || o.Total != "119.32" || o.FinancialStatus != "" || o.FulfillmentStatus != "" || o.Refunded != "" ||
		o.Shipping != nil || len(o.DiscountCodes) != 0 || len(o.Tracking) != 0 || len(o.Items) != 2 || o.Items[0] != (OrderItem{Title: "Arco lamp", Variant: "Black", Quantity: 1}) {
		t.Fatalf("few order = %+v", o)
	}
	raw = string(e.Do("GET", path, viewer, nil).Want(t, 200).Body)
	for _, gone := range []string{`"price"`, `"total_spent"`, `"financial_status"`, `"refunded"`, `"shipping"`, `"email"`} {
		if strings.Contains(raw, gone) {
			t.Errorf("answer tells %s: %s", gone, raw)
		}
	}
	var stored string
	var ordersShown int
	e.Scalar(`SELECT array_to_string(fields, ',') FROM ticket_panels WHERE id = $1`, &stored, p.ID)
	e.Scalar(`SELECT orders_shown FROM ticket_panels WHERE id = $1`, &ordersShown, p.ID)
	if stored != "orders_count,line_items" || ordersShown != 1 {
		t.Fatalf("stored %q %d", stored, ordersShown)
	}
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ticket_panel.updated'
		AND data->'changes' @> '["fields","orders_shown"]'`, &auditN, ws.ID)
	if auditN != 1 {
		t.Fatalf("fields audit = %d", auditN)
	}
	// The store's spelling of the email, when chosen; nothing at all, when nothing is.
	e.Do("PATCH", id, admin, map[string]any{"fields": []string{"email"}}).Want(t, 200)
	if d = readPanel(t, e, path, viewer); d.Customer == nil || d.Customer.Email != "Ada@Example.com" || d.Customer.OrdersCount != nil {
		t.Fatalf("email = %+v", d.Customer)
	}
	if e.Do("PATCH", id, admin, map[string]any{"fields": []string{}}).Want(t, 200).JSON(t, &chosen); chosen.Fields == nil || len(chosen.Fields) != 0 {
		t.Fatalf("none = %v", chosen.Fields)
	}
	if d = readPanel(t, e, path, viewer); d.State != "ok" || len(d.Orders) != 1 || d.Orders[0].Items == nil || len(d.Orders[0].Items) != 0 {
		t.Fatalf("none = %+v", d)
	}
	e.Do("PATCH", id, admin, map[string]any{"fields": defaultFields, "orders_shown": 5}).Want(t, 200)

	// An app without access to addresses: the orders still show, with their shipping method.
	shop.withoutAddresses(true)
	d = readPanel(t, e, path+"?refresh=true", viewer)
	if d.State != "ok" || len(d.Orders) != 2 || d.Orders[0].Shipping == nil || *d.Orders[0].Shipping != (Shipping{Method: "DHL Express"}) {
		t.Fatalf("without addresses = %+v (%s)", d.Orders, d.Message)
	}
	e.Scalar(`SELECT status FROM ticket_panels WHERE id = $1`, &status, p.ID)
	if status != "connected" {
		t.Fatalf("an address refusal marked the panel %q", status)
	}
	shop.withoutAddresses(false)

	// Another contact the store doesn't know (a near match aside), and a ticket without one.
	d = readPanel(t, e, "/v1/tickets/"+newTicket(t, e, agent, "ada.o@example.com", nil)+"/panels/"+p.ID.String(), viewer)
	if d.State != "empty" || d.Customer != nil {
		t.Fatalf("unknown contact = %+v", d)
	}
	d = readPanel(t, e, "/v1/tickets/"+newTicket(t, e, agent, "", nil)+"/panels/"+p.ID.String(), viewer)
	if d.State != "no_contact" {
		t.Fatalf("no contact = %+v", d)
	}

	// A revoked token: a quiet state on the ticket, and the panel needs attention in settings.
	shop.only("shpat_new")
	d = readPanel(t, e, path+"?refresh=true", viewer)
	if d.State != "error" || !strings.Contains(d.Message, "access token") || d.Code != "refused" {
		t.Fatalf("revoked = %+v", d)
	}
	var lastErr string
	e.Scalar(`SELECT status || '|' || last_error FROM ticket_panels WHERE id = $1`, &status, p.ID)
	if status, lastErr, _ = strings.Cut(status, "|"); status != "error" || lastErr == "" {
		t.Fatalf("status = %q %q", status, lastErr)
	}
	// A new token is checked, then the panel is connected again.
	e.Do("PATCH", "/v1/ticket-panels/"+p.ID.String(), admin, map[string]any{"token": "still-wrong"}).Want(t, 422)
	upd := e.Do("PATCH", "/v1/ticket-panels/"+p.ID.String(), admin, map[string]any{"token": "shpat_new", "name": "Shop"}).Want(t, 200).Map(t)
	if upd["status"] != "connected" || upd["name"] != "Shop" {
		t.Fatalf("updated = %v", upd)
	}
	d = readPanel(t, e, path, viewer)
	if d.State != "ok" {
		t.Fatalf("after the new token = %+v", d)
	}

	// Turned off, it is not read; removed, it is gone.
	e.Do("PATCH", "/v1/ticket-panels/"+p.ID.String(), admin, map[string]any{"enabled": false}).Want(t, 200)
	e.Do("GET", path, viewer, nil).Want(t, 404)
	e.Do("DELETE", "/v1/ticket-panels/"+p.ID.String(), agent, nil).Want(t, 403)
	e.Do("DELETE", "/v1/ticket-panels/"+p.ID.String(), admin, nil).Want(t, 204)
	e.Do("GET", path, viewer, nil).Want(t, 404)
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ticket_panel.disconnected'`, &auditN, ws.ID)
	if auditN != 1 {
		t.Fatalf("disconnect audit = %d", auditN)
	}
}

// TestShopifyAppPanel: an app made in Shopify's Dev Dashboard connects with its client ID
// and secret; OpenDesk asks the shop for a token, keeps it until shortly before its end
// and asks again when the shop revokes it.
func TestShopifyAppPanel(t *testing.T) {
	e, ws := apiEnv(t)
	shop := newFakeShopify()
	srv := httptest.NewServer(shop)
	defer srv.Close()
	s := For(e.D)
	s.shopifyBase = func(string) string { return srv.URL }
	admin, viewer := ws.Admin.As(ws), ws.Viewer.As(ws)
	app := func(extra map[string]any) map[string]any {
		b := map[string]any{"kind": "shopify", "shop": "lumen-store", "client_id": "app-id", "client_secret": "app-secret"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	refused := func(body map[string]any, want string) {
		t.Helper()
		if r := e.Do("POST", "/v1/ticket-panels", admin, body).Want(t, 422); !strings.Contains(string(r.Body), want) {
			t.Fatalf("refusal %s: %s", want, r.Body)
		}
	}

	// What the shop would refuse is caught before saving.
	refused(app(map[string]any{"client_secret": "wrong"}), "client ID or secret")
	refused(app(map[string]any{"client_id": "other-org"}), "organization")
	refused(app(map[string]any{"client_secret": ""}), "client ID and client secret")
	refused(app(map[string]any{"token": "shpat_x"}), "not both")
	shop.set("app-secret", "read_customers")
	refused(app(nil), "read_orders")
	shop.set("app-secret", "read_customers,write_orders") // a write scope includes its read one
	var created struct {
		Panel Panel `json:"panel"`
	}
	e.Do("POST", "/v1/ticket-panels", admin, app(nil)).Want(t, 201).JSON(t, &created)
	p := created.Panel
	if p.Auth != "client_credentials" || p.ClientID != "app-id" || p.Status != "connected" || p.Name != "Lumen Store" {
		t.Fatalf("panel = %+v", p)
	}
	var sealed string
	e.Scalar(`SELECT encode(secret_enc, 'escape') FROM ticket_panels WHERE id = $1`, &sealed, p.ID)
	if strings.Contains(sealed, "app-secret") {
		t.Fatal("client secret stored in the clear")
	}
	if list := string(e.Do("GET", "/v1/ticket-panels", viewer, nil).Want(t, 200).Body); strings.Contains(list, "app-secret") || strings.Contains(list, "shpat_") {
		t.Fatalf("list leaks: %s", list)
	}

	// One token serves the lookups until shortly before its end.
	path := "/v1/tickets/" + newTicket(t, e, ws.Agent.As(ws), "ada@example.com", nil) + "/panels/" + p.ID.String()
	issued := shop.issued.Load()
	if d := readPanel(t, e, path, viewer); d.State != "ok" || d.Customer == nil || d.Customer.Name != "Ada Buyer" {
		t.Fatalf("data = %+v", d)
	}
	readPanel(t, e, path+"?refresh=true", viewer)
	if n := shop.issued.Load(); n != issued+1 {
		t.Fatalf("tokens asked for: %d", n-issued)
	}
	s.tokens.now = func() time.Time { return time.Now().Add(24*time.Hour - time.Minute) }
	if d := readPanel(t, e, path+"?refresh=true", viewer); d.State != "ok" || shop.issued.Load() != issued+2 {
		t.Fatalf("near its end: %+v, %d tokens", d, shop.issued.Load()-issued)
	}

	// A token revoked before its end (the app reinstalled): once more with a new one.
	shop.only()
	if d := readPanel(t, e, path+"?refresh=true", viewer); d.State != "ok" || shop.issued.Load() != issued+3 {
		t.Fatalf("revoked: %+v, %d tokens", d, shop.issued.Load()-issued)
	}

	// A rotated secret: the panel needs attention until it is given the new one, which keeps
	// the client ID.
	shop.set("app-secret-2", "read_customers,read_orders")
	shop.only()
	if d := readPanel(t, e, path+"?refresh=true", viewer); d.State != "error" || !strings.Contains(d.Message, "client ID or secret") {
		t.Fatalf("rotated = %+v", d)
	}
	var status string
	e.Scalar(`SELECT status FROM ticket_panels WHERE id = $1`, &status, p.ID)
	if status != "error" {
		t.Fatalf("status = %q", status)
	}
	id := "/v1/ticket-panels/" + p.ID.String()
	e.Do("PATCH", id, admin, map[string]any{"client_secret": "app-secret"}).Want(t, 422)
	upd := e.Do("PATCH", id, admin, map[string]any{"client_secret": "app-secret-2"}).Want(t, 200).Map(t)
	if upd["status"] != "connected" || upd["client_id"] != "app-id" {
		t.Fatalf("new secret = %v", upd)
	}
	if d := readPanel(t, e, path, viewer); d.State != "ok" {
		t.Fatalf("after the new secret = %+v", d)
	}

	// A pasted token in place of the app, and back: a secret alone needs a client ID.
	shop.only("shpat_old")
	if upd = e.Do("PATCH", id, admin, map[string]any{"token": "shpat_old"}).Want(t, 200).Map(t); upd["auth"] != "access_token" || upd["client_id"] != nil {
		t.Fatalf("token = %v", upd)
	}
	if d := readPanel(t, e, path, viewer); d.State != "ok" {
		t.Fatalf("with the token = %+v", d)
	}
	e.Do("PATCH", id, admin, map[string]any{"client_secret": "app-secret-2"}).Want(t, 422)
	e.Do("PATCH", id, admin, map[string]any{"client_id": "app-id", "client_secret": "app-secret-2"}).Want(t, 200)
	var leaks int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND (data::text LIKE '%app-secret%' OR data::text LIKE '%shpat%')`, &leaks, ws.ID)
	if leaks != 0 {
		t.Fatalf("audit leaks credentials: %d", leaks)
	}
}

func TestPanelFollowsTicketVisibility(t *testing.T) {
	e, ws := apiEnv(t)
	shop := newFakeShopify("shpat_good")
	srv := httptest.NewServer(shop)
	defer srv.Close()
	For(e.D).shopifyBase = func(string) string { return srv.URL }
	var created struct {
		Panel Panel `json:"panel"`
	}
	add := func(extra map[string]any) map[string]any {
		b := map[string]any{"kind": "shopify", "shop": "lumen-store", "token": "shpat_good"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	e.Do("POST", "/v1/ticket-panels", ws.Admin.As(ws), add(map[string]any{"fields": []string{"tags", "street"}})).Want(t, 422)
	e.Do("POST", "/v1/ticket-panels", ws.Admin.As(ws), add(map[string]any{"orders_shown": 9})).Want(t, 422)
	e.Do("POST", "/v1/ticket-panels", ws.Admin.As(ws), add(map[string]any{"fields": []string{"tracking", "total_spent"}, "orders_shown": 2})).Want(t, 201).JSON(t, &created)
	if !slices.Equal(created.Panel.Fields, []string{"total_spent", "tracking"}) || created.Panel.OrdersShown != 2 {
		t.Fatalf("added with %v %d", created.Panel.Fields, created.Panel.OrdersShown)
	}
	lead := e.AddUser(ws, "agent", "Lena Lead")
	var team string
	e.Scalar(`INSERT INTO teams (id, workspace_id, name, private) VALUES (gen_random_uuid(), $1, 'Security', true) RETURNING id::text`, &team, ws.ID)
	e.Exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1::uuid, $2, 'lead')`, team, lead.ID)
	tid := newTicket(t, e, lead.As(ws), "ada@example.com", map[string]any{"team_id": team})
	path := "/v1/tickets/" + tid + "/panels/" + created.Panel.ID.String()
	n := shop.lookups.Load()
	e.Do("GET", path, ws.Agent.As(ws), nil).Want(t, 404)
	if shop.lookups.Load() != n {
		t.Fatal("a hidden ticket's contact was looked up")
	}
	e.Do("GET", path, lead.As(ws), nil).Want(t, 200)
	// Another workspace's panel is not found.
	other := e.NewWorkspace()
	e.Do("GET", "/v1/tickets/"+newTicket(t, e, other.Agent.As(other), "ada@example.com", nil)+"/panels/"+created.Panel.ID.String(),
		other.Agent.As(other), nil).Want(t, 404)
}

func TestHTTPPanel(t *testing.T) {
	e, ws := apiEnv(t)
	var secret atomic.Value
	secret.Store("")
	var got atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/panel", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sig := r.Header.Get("OD-Signature")
		ts, mac, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",v1=")
		if !crypto.EqualHex(mac, crypto.HMACHex([]byte(secret.Load().(string)), ts+"."+string(body))) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		got.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sections":[{"title":"Odoo\norder","items":[
			{"label":"Confirmed delivery","value":"2026-11-04","url":"https://erp.example/so/42"},
			{"label":"Lines","value":3},{"label":"Paid","value":true},
			{"label":"Script","value":"x","url":"javascript:alert(1)"},{"label":"","value":""}]},
			{"title":"Empty","items":[]}]}`)
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("/huge", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"sections":"`+strings.Repeat("x", 70<<10)+`"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	admin, viewer := ws.Admin.As(ws), ws.Viewer.As(ws)

	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": "ftp://erp.example/panel"}).Want(t, 422)
	var created struct {
		Panel  Panel  `json:"panel"`
		Secret string `json:"secret"`
	}
	resp := e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/panel?key=s3cret", "name": "Odoo"}).Want(t, 201)
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("the secret's answer is cacheable")
	}
	resp.JSON(t, &created)
	if !strings.HasPrefix(created.Secret, "pnsec_") || created.Panel.Name != "Odoo" || strings.Contains(created.Panel.Host, "s3cret") {
		t.Fatalf("created = %+v", created)
	}
	if created.Panel.Fields == nil || len(created.Panel.Fields) != 0 || created.Panel.OrdersShown != 0 {
		t.Fatalf("an http panel has fields: %+v", created.Panel)
	}
	e.Do("PATCH", "/v1/ticket-panels/"+created.Panel.ID.String(), admin, map[string]any{"fields": []string{"tags"}}).Want(t, 422)
	e.Do("PATCH", "/v1/ticket-panels/"+created.Panel.ID.String(), admin, map[string]any{"orders_shown": 2}).Want(t, 422)
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/panel", "fields": []string{}}).Want(t, 422)
	if strings.Contains(string(e.Do("GET", "/v1/ticket-panels", viewer, nil).Want(t, 200).Body), "s3cret") {
		t.Fatal("the URL's query is listed")
	}
	secret.Store(created.Secret)

	// Renamed: the name is trimmed, audited as the one change and published; a blank, long or
	// link-like name is refused and the panel keeps its own.
	pid := "/v1/ticket-panels/" + created.Panel.ID.String()
	for _, bad := range []string{"   ", strings.Repeat("x", 65), "erp.example.com"} {
		e.Do("PATCH", pid, admin, map[string]any{"name": bad}).Want(t, 422)
	}
	_, seq := e.Events(ws.ID, 0)
	var renamed Panel
	if e.Do("PATCH", pid, admin, map[string]any{"name": "  Odoo   ERP "}).Want(t, 200).JSON(t, &renamed); renamed.Name != "Odoo ERP" {
		t.Fatalf("renamed = %q", renamed.Name)
	}
	if types, _ := e.Events(ws.ID, seq); !slices.Equal(types, []string{"ticket_panel.updated"}) {
		t.Fatalf("rename events = %v", types)
	}
	var published, stored string
	var renames int
	e.Scalar(`SELECT data->'panel'->>'name' FROM events WHERE workspace_id = $1 AND seq > $2 AND type = 'ticket_panel.updated'`, &published, ws.ID, seq)
	e.Scalar(`SELECT name FROM ticket_panels WHERE id = $1`, &stored, created.Panel.ID)
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ticket_panel.updated' AND data->'changes' = '["name"]'::jsonb`, &renames, ws.ID)
	if published != "Odoo ERP" || stored != "Odoo ERP" || renames != 1 {
		t.Fatalf("rename: published %q, stored %q, audited %d", published, stored, renames)
	}

	tid := newTicket(t, e, ws.Agent.As(ws), "ada@example.com", nil)
	path := "/v1/tickets/" + tid + "/panels/" + created.Panel.ID.String()
	var d Data
	d = readPanel(t, e, path, viewer)
	if d.State != "ok" || len(d.Sections) != 1 || d.Sections[0].Title != "Odoo order" || d.Blocks != nil {
		t.Fatalf("data = %+v", d)
	}
	items := d.Sections[0].Items
	if len(items) != 4 || items[0].URL != "https://erp.example/so/42" || items[1].Value != "3" || items[2].Value != "true" || items[3].URL != "" {
		t.Fatalf("items = %+v", items)
	}
	var sent lookupRequest
	_ = json.Unmarshal([]byte(got.Load().(string)), &sent)
	if sent.Contact.Email != "ada@example.com" || sent.Ticket.ID.String() != tid || sent.WorkspaceID != ws.ID || sent.PanelID != created.Panel.ID {
		t.Fatalf("request = %+v", sent)
	}

	// A new secret: the old one is refused until the URL knows the new one.
	var rotated struct {
		Secret string `json:"secret"`
	}
	e.Do("POST", "/v1/ticket-panels/"+created.Panel.ID.String()+"/rotate-secret", admin, nil).Want(t, 200).JSON(t, &rotated)
	if rotated.Secret == "" || rotated.Secret == created.Secret {
		t.Fatalf("rotated = %q", rotated.Secret)
	}
	d = readPanel(t, e, path, viewer)
	if d.State != "error" || !strings.Contains(d.Message, "signing secret") || d.Code != "refused" {
		t.Fatalf("old secret = %+v", d)
	}
	secret.Store(rotated.Secret)
	d = readPanel(t, e, path+"?refresh=1", viewer)
	if d.State != "ok" {
		t.Fatalf("new secret = %+v", d)
	}

	// Nothing for this contact, and an answer too large.
	e.Do("PATCH", "/v1/ticket-panels/"+created.Panel.ID.String(), admin, map[string]any{"url": srv.URL + "/gone"}).Want(t, 200)
	d = readPanel(t, e, path, viewer)
	if d.State != "empty" {
		t.Fatalf("404 = %+v", d)
	}
	e.Do("PATCH", "/v1/ticket-panels/"+created.Panel.ID.String(), admin, map[string]any{"url": srv.URL + "/huge"}).Want(t, 200)
	d = readPanel(t, e, path, viewer)
	if d.State != "error" || !strings.Contains(d.Message, "64 KB") || d.Code != "answer" {
		t.Fatalf("huge = %+v", d)
	}
}

// TestStatusFollowsTheSavedPanel checks a lookup's status is recorded only for the panel as it
// was saved when the lookup began: a refusal of the old URL or secret does not mark the new one.
func TestStatusFollowsTheSavedPanel(t *testing.T) {
	e, ws := apiEnv(t)
	admin := ws.Admin.As(ws)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	var created struct {
		Panel Panel `json:"panel"`
	}
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/panel"}).Want(t, 201).JSON(t, &created)
	s := For(e.D)
	old, err := loadRow(e.Ctx, e.D.DB.Pool, ws.ID, created.Panel.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.Do("PATCH", "/v1/ticket-panels/"+created.Panel.ID.String(), admin, map[string]any{"url": srv.URL + "/v2"}).Want(t, 200)
	refused := &authError{"The URL refused the signing secret."}
	s.markStatus(e.Ctx, old, refused)
	var status string
	e.Scalar(`SELECT status FROM ticket_panels WHERE id = $1`, &status, created.Panel.ID)
	if status != "connected" {
		t.Fatalf("a lookup of the old URL marked the new one %q", status)
	}
	cur, err := loadRow(e.Ctx, e.D.DB.Pool, ws.ID, created.Panel.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.markStatus(e.Ctx, cur, refused)
	e.Scalar(`SELECT status FROM ticket_panels WHERE id = $1`, &status, created.Panel.ID)
	if status != "error" {
		t.Fatalf("a refusal of the saved panel left it %q", status)
	}
}

// TestLookupCode checks a failure the app says in the reader's language carries its code, and
// one only the product's or the network's words describe carries none.
func TestLookupCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("lookup: %w", context.DeadlineExceeded), "timeout"},
		{&authError{"Shopify refused the access token."}, "refused"},
		{&answerError{"The panel's URL answered 500.", "status"}, "status"},
		{fmt.Errorf("read: %w", &answerError{"The panel's answer is not the expected JSON.", "answer"}), "answer"},
		{errors.New("dial tcp: connection refused"), ""},
	}
	for _, c := range cases {
		if got := lookupCode(c.err); got != c.want {
			t.Errorf("lookupCode(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestPanelLimitAndKinds(t *testing.T) {
	e, ws := apiEnv(t)
	admin := ws.Admin.As(ws)
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "zendesk"}).Want(t, 422)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	for i := range maxPerWorkspace {
		e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/p" + string(rune('a'+i))}).Want(t, 201)
	}
	e.Do("POST", "/v1/ticket-panels", admin, map[string]any{"kind": "http", "url": srv.URL + "/more"}).Want(t, 409)
	key := e.APIKey(ws, "settings:write", "tickets:read")
	e.Do("POST", "/v1/ticket-panels", hdtest.Auth{Bearer: key}, map[string]any{"kind": "http", "url": srv.URL + "/key"}).Want(t, 403)
	e.Do("GET", "/v1/ticket-panels", hdtest.Auth{Bearer: key}, nil).Want(t, 200)
}

func TestNormalizeShop(t *testing.T) {
	for in, want := range map[string]string{
		"lumen-store":               "lumen-store.myshopify.com",
		"Lumen-Store.myshopify.com": "lumen-store.myshopify.com",
		"https://lumen-store.myshopify.com/admin/orders": "lumen-store.myshopify.com",
	} {
		if got, err := normalizeShop(in); err != nil || got != want {
			t.Errorf("normalizeShop(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "evil.example.com", "a.myshopify.com.evil.example", "lumen store"} {
		if _, err := normalizeShop(in); err == nil {
			t.Errorf("normalizeShop(%q) accepted", in)
		}
	}
	if q := searchPhrase(`a"b\c@x.com`); q != `email:"a\"b\\c@x.com"` {
		t.Errorf("searchPhrase = %s", q)
	}
}

func TestShopifyHelpers(t *testing.T) {
	if got := adminURL("lumen-store.myshopify.com") + "/orders/" + gidNumber("gid://shopify/Order/5551234"); got != "https://admin.shopify.com/store/lumen-store/orders/5551234" {
		t.Errorf("order URL = %s", got)
	}
	for in, want := range map[string]bool{"14.44": true, "0.00": false, "0": false, "": false, "x": false, " 0.01 ": true} {
		if positive(in) != want {
			t.Errorf("positive(%q) = %v", in, !want)
		}
	}
	data := json.RawMessage(`{"customers":{"nodes":[]}}`)
	addr := gqlError{Message: "Access denied", Path: []any{"customers", "nodes", 0.0, "orders", "nodes", 1.0, "shippingAddress"}}
	other := gqlError{Message: "Access denied", Path: []any{"customers", "nodes", 0.0, "orders"}}
	if errs := blocking([]gqlError{addr}, data); len(errs) != 0 {
		t.Errorf("an address refusal blocks: %v", errs)
	}
	if errs := blocking([]gqlError{addr, other}, data); len(errs) != 1 || errs[0].Path[len(errs[0].Path)-1] != "orders" {
		t.Errorf("another refusal passes: %v", errs)
	}
	if errs := blocking([]gqlError{addr}, json.RawMessage("null")); len(errs) != 1 {
		t.Errorf("no data, yet no error: %v", errs)
	}
}

func TestShopifyFields(t *testing.T) {
	if got, err := normalizeFields([]string{"tracking", " tags ", "line_prices", "line_items", "tracking"}); err != nil ||
		!slices.Equal(got, []string{"tags", "line_items", "line_prices", "tracking"}) {
		t.Errorf("normalize = %v, %v", got, err)
	}
	if got, err := normalizeFields(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("none = %#v, %v", got, err)
	}
	for _, in := range [][]string{{"notes"}, {"Tags"}, {""}, {"line_prices"}} {
		if _, err := normalizeFields(in); err == nil {
			t.Errorf("normalizeFields(%q) accepted", in)
		}
	}
	if slices.Contains(defaultFields, FieldEmail) || len(defaultFields) != len(shopifyFields)-1 {
		t.Errorf("defaults = %v", defaultFields)
	}
	if got := (&row{Kind: KindShopify}).shownFields(); !slices.Equal(got, defaultFields) {
		t.Errorf("unchosen = %v", got)
	}
	if got := (&row{Kind: KindHTTP}).shownFields(); got == nil || len(got) != 0 {
		t.Errorf("http = %v", got)
	}

	// Each field asks for its part of the answer, and only for it.
	parts := map[string][]string{
		FieldTags: {"tags"}, FieldOrdersCount: {"numberOfOrders"}, FieldTotalSpent: {"amountSpent"},
		FieldFinancialStatus: {"displayFinancialStatus"}, FieldFulfillmentStatus: {"displayFulfillmentStatus"},
		FieldLineItems: {"lineItems(first: $items)"}, FieldLinePrices: {"originalUnitPriceSet"}, FieldShipping: {"shippingLine", "shippingAddress"},
		FieldDiscounts: {"discountCodes"}, FieldRefunds: {"totalRefundedSet"}, FieldTracking: {"fulfillments", "trackingInfo"},
	}
	all := customerQuery(newFieldSet(shopifyFields))
	bare := customerQuery(fieldSet{})
	for field, want := range parts {
		for _, w := range want {
			if !strings.Contains(all, w) || strings.Contains(bare, w) {
				t.Errorf("%s: %s in all %v, in none %v", field, w, strings.Contains(all, w), strings.Contains(bare, w))
			}
		}
	}
	if q := customerQuery(newFieldSet([]string{FieldLineItems})); !strings.Contains(q, "nodes { title variantTitle quantity }") {
		t.Errorf("lines without prices:\n%s", q)
	}
	if customerQuery(newFieldSet([]string{FieldEmail})) != bare {
		t.Error("the email asks for more: it is read to match the contact anyway")
	}
	for _, q := range []string{all, bare} {
		if strings.Count(q, "{") != strings.Count(q, "}") || !strings.Contains(q, "defaultEmailAddress { emailAddress }") ||
			!strings.Contains(q, "orders(first: $orders, sortKey: CREATED_AT, reverse: true)") || !strings.Contains(q, "currentTotalPriceSet") {
			t.Errorf("query:\n%s", q)
		}
	}
}

func TestShopifyCredentialsAndScopes(t *testing.T) {
	if c, err := shopifyCredentials(" app-id ", "secret\n", ""); err != nil || c.clientID != "app-id" || c.secret != "secret" {
		t.Errorf("app = %+v, %v", c, err)
	}
	if c, err := shopifyCredentials("", "", " shpat_x "); err != nil || c.clientID != "" || c.secret != "shpat_x" {
		t.Errorf("token = %+v, %v", c, err)
	}
	for _, in := range [][3]string{{"", "", ""}, {"app-id", "", ""}, {"", "secret", ""}, {"app id", "secret", ""}, {"app-id", "s", "shpat_x"}, {"", "", "shpat x"}} {
		if _, err := shopifyCredentials(in[0], in[1], in[2]); err == nil {
			t.Errorf("shopifyCredentials(%q) accepted", in)
		}
	}
	for scope, want := range map[string]string{
		"read_customers,read_orders":                   "",
		"write_customers, read_all_orders,read_orders": "",
		"read_customers":                               "read_orders",
		"":                                             "read_customers read_orders",
	} {
		if got := strings.Join(missingScopes(scope), " "); got != want {
			t.Errorf("missingScopes(%q) = %q", scope, got)
		}
	}
	if err := tokenRefusal(400, []byte(`<p>Oauth error shop_not_permitted: Client credentials cannot be performed on this shop.</p>`)); !strings.Contains(err.Error(), "organization") {
		t.Errorf("shop_not_permitted = %v", err)
	}
	if err := tokenRefusal(503, nil); err.Error() != "Shopify answered 503." {
		t.Errorf("503 = %v", err)
	}
}
