package billing_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/useopendesk/opendesk/internal/crypto"
)

// fakeStripe is the slice of the Stripe API billing uses, held in memory.
type fakeStripe struct {
	srv *httptest.Server

	mu        sync.Mutex
	n         int
	customers map[string]url.Values
	sessions  []url.Values // checkout session create forms, in order
	subs      map[string]*fakeSub
	itemForms []url.Values // subscription item update forms
	canceled  []string
	calls     []string
	prices    map[string]map[string]any

	// Payment methods and the invoices billing charges them with (top-ups, auto-recharge).
	pms        map[string][]string // customer -> saved payment methods, newest first
	defaultPM  map[string]string   // customer -> invoice_settings.default_payment_method
	setupPMs   map[string]string   // setup intent -> payment method
	invoices   map[string]map[string]any
	invOrder   []string       // invoice ids, oldest first
	idem       map[string]any // idempotency key -> the response first given
	invForms   []url.Values   // invoice create forms, in order
	itemForms2 []url.Values   // invoice item create forms, in order
	payForms   []url.Values   // invoice pay forms, in order
	payMode    string         // "" (paid), "decline", "soft" (insufficient funds), "auth", "open" (still processing)
}

type fakeSub struct {
	ID, Customer, Status, ItemID, PriceID, Interval string
	Quantity                                        int64
	Metadata                                        map[string]string
	PeriodEnd                                       int64
	CancelAtPeriodEnd                               bool
}

func newFakeStripe() *fakeStripe {
	f := &fakeStripe{customers: map[string]url.Values{}, subs: map[string]*fakeSub{}, pms: map[string][]string{}, defaultPM: map[string]string{},
		setupPMs: map[string]string{}, invoices: map[string]map[string]any{}, idem: map[string]any{}, prices: map[string]map[string]any{
			"price_pro":      {"id": "price_pro", "object": "price", "unit_amount": 1900, "currency": "usd", "recurring": map[string]any{"interval": "month"}},
			"price_pro_y":    {"id": "price_pro_y", "object": "price", "unit_amount": 19000, "currency": "usd", "recurring": map[string]any{"interval": "year"}},
			"price_collab":   {"id": "price_collab", "object": "price", "unit_amount": 3800, "currency": "usd", "recurring": map[string]any{"interval": "month"}},
			"price_collab_y": {"id": "price_collab_y", "object": "price", "unit_amount": 38000, "currency": "usd", "recurring": map[string]any{"interval": "year"}},
			"price_ai_topup": {"id": "price_ai_topup", "object": "price", "unit_amount": 1000, "currency": "usd", "recurring": nil, "product": "prod_ai_topup"},
		}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeStripe) id(prefix string) string {
	f.n++
	return prefix + "_" + strconv.Itoa(f.n) + crypto.RandomString(10)
}

// addSub creates a subscription as Stripe would after a completed Checkout.
func (f *fakeStripe) addSub(customer, wid string, qty int64) *fakeSub {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeSub{ID: f.id("sub"), Customer: customer, Status: "active", ItemID: f.id("si"), PriceID: "price_pro", Interval: "month",
		Quantity: qty, Metadata: map[string]string{"workspace_id": wid}, PeriodEnd: time.Now().Add(30 * 24 * time.Hour).Unix()}
	f.subs[s.ID] = s
	return s
}

// setPrice moves a subscription to another price, as a plan change in the portal does.
func (f *fakeStripe) setPrice(id, price, interval string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id].PriceID, f.subs[id].Interval = price, interval
}

func (f *fakeStripe) sub(id string) fakeSub {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.subs[id]
}

func (f *fakeStripe) setStatus(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id].Status = status
}

// addPM saves a payment method on a customer, as a Checkout with setup_future_usage does.
func (f *fakeStripe) addPM(customer string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("pm")
	f.pms[customer] = append([]string{id}, f.pms[customer]...)
	return id
}

func (f *fakeStripe) setPayMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payMode = mode
}

// forms returns a copy of create forms recorded so far: "invoice", "item" or "pay".
func (f *fakeStripe) forms(kind string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch kind {
	case "invoice":
		return append([]url.Values(nil), f.invForms...)
	case "item":
		return append([]url.Values(nil), f.itemForms2...)
	}
	return append([]url.Values(nil), f.payForms...)
}

// invoice returns a copy of an invoice, as a webhook payload.
func (f *fakeStripe) invoice(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{}
	for k, v := range f.invoices[id] {
		out[k] = v
	}
	return out
}

// lastInvoice is the id of the newest invoice.
func (f *fakeStripe) lastInvoice() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.invOrder) == 0 {
		return ""
	}
	return f.invOrder[len(f.invOrder)-1]
}

func formMeta(form url.Values, prefix string) map[string]any {
	meta := map[string]any{}
	for k, v := range form {
		if strings.HasPrefix(k, prefix+"[") {
			meta[strings.TrimSuffix(strings.TrimPrefix(k, prefix+"["), "]")] = v[0]
		}
	}
	return meta
}

func (f *fakeStripe) lastSession() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sessions) == 0 {
		return nil
	}
	return f.sessions[len(f.sessions)-1]
}

func (s *fakeSub) json() map[string]any {
	return map[string]any{"id": s.ID, "object": "subscription", "status": s.Status, "customer": s.Customer, "metadata": s.Metadata,
		"cancel_at_period_end": s.CancelAtPeriodEnd,
		"items": map[string]any{"object": "list", "data": []any{map[string]any{"id": s.ItemID, "object": "subscription_item",
			"quantity": s.Quantity, "current_period_end": s.PeriodEnd, "subscription": s.ID,
			"price": map[string]any{"id": s.PriceID, "object": "price", "recurring": map[string]any{"interval": s.Interval}}}}}}
}

func (f *fakeStripe) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = r.ParseForm()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk_test_") {
		writeStripe(w, 401, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "bad key"}})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	missing := func() {
		writeStripe(w, 404, map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "resource_missing", "message": "No such object"}})
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/customers":
		id := f.id("cus")
		f.customers[id] = r.PostForm
		writeStripe(w, 200, map[string]any{"id": id, "object": "customer"})
	case len(parts) == 3 && parts[1] == "customers":
		if _, ok := f.customers[parts[2]]; !ok {
			missing()
			return
		}
		if r.Method == "POST" {
			if pm := r.PostForm.Get("invoice_settings[default_payment_method]"); pm != "" {
				f.defaultPM[parts[2]] = pm
			}
		}
		var def any
		if pm := f.defaultPM[parts[2]]; pm != "" {
			def = pm
		}
		writeStripe(w, 200, map[string]any{"id": parts[2], "object": "customer", "invoice_settings": map[string]any{"default_payment_method": def}})
	case r.Method == "GET" && len(parts) == 4 && parts[1] == "customers" && parts[3] == "payment_methods":
		data := []any{}
		for _, id := range f.pms[parts[2]] {
			data = append(data, map[string]any{"id": id, "object": "payment_method", "type": "card", "customer": parts[2]})
		}
		if len(data) > 1 && r.Form.Get("limit") == "1" {
			data = data[:1]
		}
		writeStripe(w, 200, map[string]any{"object": "list", "data": data, "has_more": false, "url": r.URL.Path})
	case r.Method == "GET" && len(parts) == 3 && parts[1] == "setup_intents":
		var pm any
		if id := f.setupPMs[parts[2]]; id != "" {
			pm = id
		}
		writeStripe(w, 200, map[string]any{"id": parts[2], "object": "setup_intent", "status": "succeeded", "payment_method": pm})
	case r.Method == "GET" && len(parts) == 3 && parts[1] == "payment_methods":
		writeStripe(w, 200, map[string]any{"id": parts[2], "object": "payment_method", "type": "card",
			"card": map[string]any{"brand": "visa", "last4": "4242", "exp_month": 8, "exp_year": 2030}})
	case r.Method == "POST" && r.Header.Get("Idempotency-Key") != "" && f.idem[r.Header.Get("Idempotency-Key")] != nil:
		writeStripe(w, 200, f.idem[r.Header.Get("Idempotency-Key")])
	case r.Method == "POST" && r.URL.Path == "/v1/invoices":
		f.invForms = append(f.invForms, r.PostForm)
		id := f.id("in")
		inv := map[string]any{"id": id, "object": "invoice", "status": "draft", "customer": r.PostForm.Get("customer"), "currency": r.PostForm.Get("currency"),
			"description": r.PostForm.Get("description"), "metadata": formMeta(r.PostForm, "metadata"), "total": int64(0), "created": time.Now().Unix(),
			"number": "", "hosted_invoice_url": "https://invoice.stripe.test/" + id, "invoice_pdf": "https://invoice.stripe.test/" + id + ".pdf"}
		f.invoices[id] = inv
		f.invOrder = append(f.invOrder, id)
		f.remember(r, inv)
		writeStripe(w, 200, inv)
	case r.Method == "GET" && r.URL.Path == "/v1/invoices":
		data := []any{}
		for i := len(f.invOrder) - 1; i >= 0; i-- {
			if inv := f.invoices[f.invOrder[i]]; inv["customer"] == r.Form.Get("customer") {
				data = append(data, inv)
			}
		}
		writeStripe(w, 200, map[string]any{"object": "list", "data": data, "has_more": false, "url": "/v1/invoices"})
	case r.Method == "POST" && r.URL.Path == "/v1/invoiceitems":
		f.itemForms2 = append(f.itemForms2, r.PostForm)
		inv, ok := f.invoices[r.PostForm.Get("invoice")]
		if !ok {
			missing()
			return
		}
		amount, _ := strconv.ParseInt(r.PostForm.Get("price_data[unit_amount]"), 10, 64)
		if amount == 0 {
			amount, _ = strconv.ParseInt(r.PostForm.Get("amount"), 10, 64)
		}
		inv["total"] = inv["total"].(int64) + amount
		item := map[string]any{"id": f.id("ii"), "object": "invoiceitem", "amount": amount}
		f.remember(r, item)
		writeStripe(w, 200, item)
	case len(parts) >= 3 && parts[1] == "invoices":
		inv, ok := f.invoices[parts[2]]
		if !ok {
			missing()
			return
		}
		action := ""
		if len(parts) == 4 {
			action = parts[3]
		}
		switch {
		case r.Method == "DELETE":
			inv["status"] = "deleted"
		case action == "finalize":
			inv["status"], inv["number"] = "open", "OD-"+strconv.Itoa(len(f.invOrder))
		case action == "void":
			inv["status"] = "void"
		case action == "pay":
			f.payForms = append(f.payForms, r.PostForm)
			switch f.payMode {
			case "open":
			case "decline", "soft", "auth":
				code, decline := "card_declined", "generic_decline"
				if f.payMode == "soft" {
					decline = "insufficient_funds"
				}
				if f.payMode == "auth" {
					code, decline = "authentication_required", "authentication_required"
				}
				writeStripe(w, 402, map[string]any{"error": map[string]any{"type": "card_error", "code": code, "decline_code": decline,
					"message": "Your card was declined."}})
				return
			default:
				inv["status"] = "paid"
			}
		}
		f.remember(r, inv)
		writeStripe(w, 200, inv)
	case r.Method == "POST" && r.URL.Path == "/v1/checkout/sessions":
		f.sessions = append(f.sessions, r.PostForm)
		id := f.id("cs")
		writeStripe(w, 200, map[string]any{"id": id, "object": "checkout.session", "url": "https://checkout.stripe.test/" + id})
	case r.Method == "POST" && r.URL.Path == "/v1/billing_portal/sessions":
		writeStripe(w, 200, map[string]any{"id": f.id("bps"), "object": "billing_portal.session", "url": "https://billing.stripe.test/session"})
	case r.Method == "GET" && len(parts) == 3 && parts[1] == "prices":
		p, ok := f.prices[parts[2]]
		if !ok {
			missing()
			return
		}
		writeStripe(w, 200, p)
	case r.Method == "GET" && r.URL.Path == "/v1/subscriptions":
		data := []any{}
		for _, s := range f.subs {
			if s.Customer == r.Form.Get("customer") && s.Status != "canceled" {
				data = append(data, s.json())
			}
		}
		writeStripe(w, 200, map[string]any{"object": "list", "data": data, "has_more": false, "url": "/v1/subscriptions"})
	case len(parts) == 3 && parts[1] == "subscriptions":
		s, ok := f.subs[parts[2]]
		if !ok {
			missing()
			return
		}
		if r.Method == "DELETE" {
			s.Status = "canceled"
			f.canceled = append(f.canceled, s.ID)
		}
		writeStripe(w, 200, s.json())
	case r.Method == "POST" && len(parts) == 3 && parts[1] == "subscription_items":
		for _, s := range f.subs {
			if s.ItemID == parts[2] {
				f.itemForms = append(f.itemForms, r.PostForm)
				q, _ := strconv.ParseInt(r.PostForm.Get("quantity"), 10, 64)
				s.Quantity = q
				if price := r.PostForm.Get("price"); price != "" {
					p, ok := f.prices[price]
					if !ok {
						missing()
						return
					}
					s.PriceID = price
					s.Interval = p["recurring"].(map[string]any)["interval"].(string)
				}
				item := map[string]any{"id": s.ItemID, "object": "subscription_item", "quantity": q}
				f.remember(r, item)
				writeStripe(w, 200, item)
				return
			}
		}
		missing()
	default:
		missing()
	}
}

// remember keeps the first answer to a request with an idempotency key, as Stripe does.
func (f *fakeStripe) remember(r *http.Request, v map[string]any) {
	if k := r.Header.Get("Idempotency-Key"); k != "" && r.Method == "POST" {
		c := map[string]any{}
		for key, val := range v {
			c[key] = val
		}
		f.idem[k] = c
	}
}

func writeStripe(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(fmt.Sprint(err))
	}
}
