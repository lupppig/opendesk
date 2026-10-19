package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/plan"
)

func (e *env) setCollab(wid uuid.UUID, access string, until *time.Time) {
	e.t.Helper()
	if err := plan.SetCollabAccess(context.Background(), e.d.DB.Pool, wid, plan.CollabAccess{Access: access, Until: until}); err != nil {
		e.t.Fatal(err)
	}
}

func (r resp) obj(path ...string) map[string]any {
	var v any = r.Body
	for _, p := range path {
		m, _ := v.(map[string]any)
		v = m[p]
	}
	m, _ := v.(map[string]any)
	return m
}

func TestCollabCheckoutNeedsAccess(t *testing.T) {
	e := newEnv(t, true)
	owner := e.signup("owner")
	wid := owner.newWorkspace("Acme")

	// Hidden: no collab price, not available, and checkout refused.
	b := owner.do("GET", "/v1/billing", nil)
	expect(t, b, 200, "")
	prices := b.obj("prices")
	if _, ok := prices["collab_month"]; ok {
		t.Fatalf("collab price shown without access: %s", b.Raw)
	}
	if _, ok := prices["collab_year"]; ok {
		t.Fatalf("collab yearly price shown without access: %s", b.Raw)
	}
	if b.str("collab", "access") != "none" || b.obj("collab")["available"] != false || b.obj("collab")["enabled"] != false {
		t.Fatalf("collab without access: %s", b.Raw)
	}
	expect(t, owner.do("POST", "/v1/billing/checkout", map[string]any{"plan": "collab"}), 403, "plan_unavailable")
	if e.fake.lastSession() != nil {
		t.Fatal("a checkout session was created without access")
	}

	// Offered: the price shows and checkout opens on the collab price, with the plan recorded.
	e.setCollab(wid, plan.CollabOffered, nil)
	b = owner.do("GET", "/v1/billing", nil)
	if b.str("plan") != "free" || b.obj("collab")["available"] != true || b.obj("collab")["enabled"] != false ||
		b.num("prices", "collab_month", "unit_amount") != 3800 || b.num("prices", "collab_year", "unit_amount") != 38000 {
		t.Fatalf("collab offered: %s", b.Raw)
	}
	expect(t, owner.do("POST", "/v1/billing/checkout", map[string]any{"plan": "collab", "interval": "year"}), 200, "")
	if f := e.fake.lastSession(); f.Get("line_items[0][price]") != "price_collab_y" || f.Get("metadata[plan]") != "collab" {
		t.Fatalf("yearly collab checkout: %v", f)
	}
	expect(t, owner.do("POST", "/v1/billing/checkout", map[string]any{"plan": "collab"}), 200, "")
	f := e.fake.lastSession()
	if f.Get("line_items[0][price]") != "price_collab" || f.Get("metadata[plan]") != "collab" ||
		f.Get("subscription_data[metadata][plan]") != "collab" || f.Get("line_items[0][quantity]") != "1" {
		t.Fatalf("collab checkout: %v", f)
	}
	// Pro still opens on the pro price.
	expect(t, owner.do("POST", "/v1/billing/checkout", nil), 200, "")
	if f := e.fake.lastSession(); f.Get("line_items[0][price]") != "price_pro" || f.Get("metadata[plan]") != "pro" {
		t.Fatalf("pro checkout: %v", f)
	}

	// The webhook maps the collab price to the collab plan.
	cus := e.scalar(`SELECT stripe_customer_id FROM subscriptions WHERE workspace_id = $1`, wid).(string)
	sub := e.fake.addSub(cus, wid.String(), 1)
	e.fake.setPrice(sub.ID, "price_collab", "month")
	expect(t, e.event("evt_collab_sub", "customer.subscription.created", e.fake.subJSON(sub.ID)), 200, "")
	b = owner.do("GET", "/v1/billing", nil)
	if b.str("plan") != "collab" || b.str("status") != "active" || b.obj("collab")["enabled"] != true ||
		b.num("ai", "monthly_usd") != 20 || b.num("limits", "max_mailboxes") != 0 || b.num("limits", "max_members") != 0 {
		t.Fatalf("collab plan: %s", b.Raw)
	}
	ent, err := e.d.Plan.Entitlements(context.Background(), e.d.DB.Pool, wid)
	if err != nil || ent.Plan != plan.Collab || !ent.Collab {
		t.Fatalf("entitlements: %+v %v", ent, err)
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'billing.subscribed'`, wid); n != 1 {
		t.Fatalf("billing.subscribed audit rows: %d", n)
	}
	expect(t, owner.do("POST", "/v1/billing/checkout", map[string]any{"plan": "collab"}), 409, "already_subscribed")
	expect(t, owner.do("POST", "/v1/billing/checkout", nil), 409, "already_subscribed")

	// Seats sync on the collab plan like on pro.
	agent := e.signup("agent")
	expect(t, owner.invite(agent.email, "agent"), 201, "")
	expect(t, agent.do("POST", "/v1/invitations/accept", map[string]any{"token": e.inviteToken(agent.email)}), 200, "")
	if err := billing.RunSyncSeats(context.Background(), e.d, wid); err != nil {
		t.Fatal(err)
	}
	if s := e.fake.sub(sub.ID); s.Quantity != 2 || s.PriceID != "price_collab" {
		t.Fatalf("collab seat sync: %+v", s)
	}
	if b := owner.do("GET", "/v1/billing", nil); b.str("plan") != "collab" || b.num("seats") != 2 || b.num("ai", "monthly_usd") != 40 {
		t.Fatalf("collab after seat sync: %s", b.Raw)
	}

	// Canceled: back to free.
	e.fake.setStatus(sub.ID, "canceled")
	expect(t, e.event("evt_collab_del", "customer.subscription.deleted", e.fake.subJSON(sub.ID)), 200, "")
	if b := owner.do("GET", "/v1/billing", nil); b.str("plan") != "free" || b.obj("collab")["enabled"] != false {
		t.Fatalf("collab canceled: %s", b.Raw)
	}
}

func TestChangePlan(t *testing.T) {
	e := newEnv(t, true)
	owner, wid, sub := proWorkspace(t, e)

	free := e.signup("free")
	free.newWorkspace("Free")
	expect(t, free.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "pro"}), 409, "no_subscription")

	expect(t, owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "pro"}), 409, "already_on_plan")
	expect(t, owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "collab"}), 403, "plan_unavailable")
	expect(t, owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "enterprise"}), 422, "")

	e.setCollab(wid, plan.CollabOffered, nil)
	r := owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "collab"})
	expect(t, r, 200, "")
	if r.str("plan") != "collab" || r.obj("collab")["enabled"] != true || r.num("ai", "monthly_usd") != 20 {
		t.Fatalf("after change to collab: %s", r.Raw)
	}
	if s := e.fake.sub(sub.ID); s.PriceID != "price_collab" || s.Quantity != 1 {
		t.Fatalf("stripe item: %+v", s)
	}
	f := e.fake.itemForms[len(e.fake.itemForms)-1]
	if f.Get("price") != "price_collab" || f.Get("proration_behavior") != "create_prorations" || f.Get("quantity") != "1" {
		t.Fatalf("item update: %v", f)
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'billing.plan_changed'`, wid); n != 1 {
		t.Fatalf("billing.plan_changed audit rows: %d", n)
	}
	// The webhook that follows finds the change applied.
	expect(t, e.event("evt_plan_changed", "customer.subscription.updated", e.fake.subJSON(sub.ID)), 200, "")
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action IN ('billing.plan_changed', 'billing.updated')`, wid); n != 1 {
		t.Fatalf("audit rows after the webhook: %d", n)
	}
	expect(t, owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "collab"}), 409, "already_on_plan")

	// And back to pro.
	r = owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "pro"})
	expect(t, r, 200, "")
	if r.str("plan") != "pro" || r.obj("collab")["enabled"] != false || r.num("ai", "monthly_usd") != 10 || e.fake.sub(sub.ID).PriceID != "price_pro" {
		t.Fatalf("after change to pro: %s", r.Raw)
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'billing.plan_changed'`, wid); n != 2 {
		t.Fatalf("billing.plan_changed audit rows: %d", n)
	}

	// A plan change made in the portal arrives by webhook.
	e.fake.setPrice(sub.ID, "price_collab", "month")
	expect(t, e.event("evt_portal_change", "customer.subscription.updated", e.fake.subJSON(sub.ID)), 200, "")
	if b := owner.do("GET", "/v1/billing", nil); b.str("plan") != "collab" {
		t.Fatalf("portal change: %s", b.Raw)
	}
}

func TestComplimentaryCollab(t *testing.T) {
	e := newEnv(t, true)
	owner := e.signup("owner")
	wid := owner.newWorkspace("Acme")
	e.setCollab(wid, plan.CollabComplimentary, nil)

	b := owner.do("GET", "/v1/billing", nil)
	expect(t, b, 200, "")
	if b.str("plan") != "collab" || b.str("status") != "none" || b.num("limits", "max_mailboxes") != 0 || b.num("limits", "max_members") != 0 ||
		b.num("ai", "monthly_usd") != 20 || b.str("collab", "access") != "complimentary" || b.obj("collab")["until"] != nil ||
		b.obj("collab")["enabled"] != true || b.obj("collab")["available"] != true || b.num("prices", "collab_month", "unit_amount") != 3800 {
		t.Fatalf("complimentary: %s", b.Raw)
	}
	expect(t, owner.addMailbox("one@acme.test"), 201, "")
	expect(t, owner.addMailbox("two@acme.test"), 201, "")

	// The allowance is $20 per billed seat; viewers are free.
	agent := e.signup("agent")
	expect(t, owner.invite(agent.email, "agent"), 201, "")
	expect(t, agent.do("POST", "/v1/invitations/accept", map[string]any{"token": e.inviteToken(agent.email)}), 200, "")
	viewer := e.signup("viewer")
	expect(t, owner.invite(viewer.email, "viewer"), 201, "")
	expect(t, viewer.do("POST", "/v1/invitations/accept", map[string]any{"token": e.inviteToken(viewer.email)}), 200, "")
	if b := owner.do("GET", "/v1/billing", nil); b.num("ai", "monthly_usd") != 40 || b.num("billed_seats") != 2 {
		t.Fatalf("complimentary seats: %s", b.Raw)
	}
	// No subscription to change, but checkout stays open (to pay for it).
	expect(t, owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "collab"}), 409, "no_subscription")
	expect(t, owner.do("POST", "/v1/billing/checkout", map[string]any{"plan": "collab"}), 200, "")

	// With an end date in the future it stays on; once it ends the workspace is free again.
	until := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	e.setCollab(wid, plan.CollabComplimentary, &until)
	if b := owner.do("GET", "/v1/billing", nil); b.str("plan") != "collab" || b.str("collab", "until") == "" {
		t.Fatalf("complimentary until: %s", b.Raw)
	}
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE subscriptions SET collab_until = now() - interval '1 minute' WHERE workspace_id = $1`, wid); err != nil {
		t.Fatal(err)
	}
	b = owner.do("GET", "/v1/billing", nil)
	if b.str("plan") != "free" || b.obj("collab")["enabled"] != false || b.num("limits", "max_mailboxes") != 1 || b.num("ai", "monthly_usd") != 0.5 {
		t.Fatalf("complimentary ended: %s", b.Raw)
	}
}

func TestCollabSelfHosted(t *testing.T) {
	e := newEnv(t, false)
	owner := e.signup("owner")
	owner.newWorkspace("Self")
	b := owner.do("GET", "/v1/billing", nil)
	expect(t, b, 200, "")
	if b.str("plan") != "self_hosted" || b.str("collab", "access") != "none" || b.obj("collab")["enabled"] != true ||
		b.obj("collab")["available"] != false {
		t.Fatalf("self-hosted collab: %s", b.Raw)
	}
	if _, ok := b.obj("prices")["collab_month"]; ok {
		t.Fatalf("collab price on a self-hosted instance: %s", b.Raw)
	}
	expect(t, owner.do("POST", "/v1/billing/change-plan", map[string]any{"plan": "collab"}), 404, "billing_disabled")
}

// subJSON is a subscription as a webhook payload.
func (f *fakeStripe) subJSON(id string) map[string]any {
	s := f.sub(id)
	return s.json()
}
