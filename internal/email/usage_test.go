package email

import (
	"net/http"
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
)

// TestMailboxUsage: before a mailbox is removed, admins learn how many tickets reply from
// it and how many are still open; other mailboxes' tickets and deleted ones don't count.
func TestMailboxUsage(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	other := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) { m.Email = "other@acme.test" })
	open, _ := firstTicket(t, w, m, customer())
	closed, _ := firstTicket(t, w, m, customer())
	gone, _ := firstTicket(t, w, m, customer())
	firstTicket(t, w, other, customer())
	if _, err := w.d.DB.Pool.Exec(w.ctx, `UPDATE tickets SET status = 'closed' WHERE id = $1`, closed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.DB.Pool.Exec(w.ctx, `UPDATE tickets SET deleted_at = now() WHERE id = $1`, gone.ID); err != nil {
		t.Fatal(err)
	}
	var u mailboxUsage
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+m.ID.String()+"/usage", nil, &u); code != 200 || u.Tickets != 2 || u.Open != 1 {
		t.Fatalf("usage %d %+v (open ticket %s)", code, u, open.ID)
	}

	agent := *w.admin
	agent.Role = auth.RoleAgent
	resp, err := http.Get(w.api(&agent).URL + "/v1/mailboxes/" + m.ID.String() + "/usage")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("agent: %d", resp.StatusCode)
	}
}
