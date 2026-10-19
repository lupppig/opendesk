package tickets_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// TestSolveArchivesAtProvider: solving or closing a ticket whose email came in through a
// mailbox that reads Gmail or Outlook directly, with archiving on, queues the archive in
// the same write; nothing is queued otherwise.
func TestSolveArchivesAtProvider(t *testing.T) {
	e, ws := newEnv(t)
	path := func(id uuid.UUID) string { return "/v1/tickets/" + id.String() }
	mailbox := func(config string) uuid.UUID {
		id := ids.New()
		e.Exec(`INSERT INTO mailboxes (id, workspace_id, name, email, display_name, inbound_kind, forwarding_token, outbound_kind, brand_id, config)
			VALUES ($1, $2, 'Desk', $3, '', 'google', $4, 'google', $5, $6)`, id, ws.ID, "desk-"+id.String()[24:]+"@acme.test", "fw-"+id.String(), ws.BrandID, config)
		return id
	}
	ticketFrom := func(mb uuid.UUID) uuid.UUID {
		tk := createTicket(t, e, ws, map[string]any{})
		m := inbound(t, e, ws, tk.ID, "Hello")
		e.Exec(`UPDATE messages SET provider_ref = jsonb_build_object('mailbox_id', $2::text, 'account', 'desk@acme.test', 'provider', 'google', 'id', 'g1')
			WHERE id = $1`, m.ID, mb.String())
		return tk.ID
	}
	archives := func(tid uuid.UUID) int {
		n := 0
		for _, raw := range e.Jobs(jobs.ProviderTidyArgs{}.Kind(), ws.ID) {
			var a jobs.ProviderTidyArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				t.Fatal(err)
			}
			if a.TicketID == tid && a.Action == jobs.ProviderArchive {
				n++
			}
		}
		return n
	}
	agent := ws.Agent.As(ws)

	on := mailbox(`{"oauth_grant":"api","provider_archive_on_solve":true}`)
	tid := ticketFrom(on)
	e.Do("PATCH", path(tid), agent, map[string]any{"status": "solved"}).Want(t, 200)
	if n := archives(tid); n != 1 {
		t.Fatalf("solving queued %d archives", n)
	}
	// Solved to closed is not another archive; reopened and closed is.
	e.Do("PATCH", path(tid), agent, map[string]any{"status": "closed"}).Want(t, 200)
	if n := archives(tid); n != 0 {
		t.Fatalf("closing a solved ticket queued %d", n)
	}
	tid2 := ticketFrom(on)
	e.Do("PATCH", path(tid2), agent, map[string]any{"status": "pending"}).Want(t, 200)
	if n := archives(tid2); n != 0 {
		t.Fatalf("pending queued %d", n)
	}
	e.Do("PATCH", path(tid2), agent, map[string]any{"status": "closed"}).Want(t, 200)
	if n := archives(tid2); n != 1 {
		t.Fatalf("closing queued %d", n)
	}

	// Archiving off, or a mailbox that does not read through the API: nothing.
	for _, cfg := range []string{`{"oauth_grant":"api"}`, `{"oauth_grant":"sync","provider_archive_on_solve":true}`} {
		tid := ticketFrom(mailbox(cfg))
		e.Do("PATCH", path(tid), agent, map[string]any{"status": "solved"}).Want(t, 200)
		if n := archives(tid); n != 0 {
			t.Fatalf("%s queued %d", cfg, n)
		}
	}
	// A ticket without provider email.
	plain := createTicket(t, e, ws, map[string]any{})
	e.Do("PATCH", path(plain.ID), agent, map[string]any{"status": "solved"}).Want(t, 200)
	if n := archives(plain.ID); n != 0 {
		t.Fatalf("plain ticket queued %d", n)
	}
}
