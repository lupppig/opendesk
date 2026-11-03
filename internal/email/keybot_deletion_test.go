package email

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
)

func TestAPIKeyBotDeletionPreservesQueuedEmail(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	mustExec(t, w.d, `UPDATE mailboxes SET from_name_format='{agent} - Support' WHERE id=$1`, m.ID)
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", "", "{{ID}}", "del"+crypto.RandomString(6)))
	bot, key := uuid.New(), uuid.New()
	mustExec(t, w.d, `INSERT INTO bots (id,workspace_id,name,look) VALUES ($1,$2,'Julia','{}')`, bot, w.wid)
	mustExec(t, w.d, `INSERT INTO api_keys (id,workspace_id,name,prefix,key_hash,scopes,bot_id) VALUES ($1,$2,'Replai','test','test','{}',$3)`, key, w.wid, bot)
	var mid uuid.UUID
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: w.messageFor(in).TicketID, Kind: "reply", Direction: "outbound", AuthorType: "api", BodyHTML: "<p>It shipped.</p>", BodyText: "It shipped.", FromName: "Julia", BotID: &bot, APIKeyID: &key, DeliveryStatus: "pending", Trigger: "none"})
		mid = msg.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, w.d, `UPDATE api_keys SET revoked_at=now() WHERE id=$1`, key)
	r := chi.NewRouter()
	bots.Register(w.d, humachi.New(r, huma.DefaultConfig("test", "0")), r)
	remove := func() int {
		req := httptest.NewRequest(http.MethodDelete, "/v1/bots/"+bot.String(), nil).WithContext(auth.WithPrincipal(w.ctx, w.admin))
		result := httptest.NewRecorder()
		r.ServeHTTP(result, req)
		return result.Code
	}
	if status := remove(); status != http.StatusConflict {
		t.Fatalf("delete before delivery: %d, want 409", status)
	}
	var raw []byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, b []byte) error { raw = b; return nil }
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	if !strings.Contains(head, "Julia - Support") || !strings.Contains(head, "Auto-Submitted: auto-generated") {
		t.Fatal("bot sender or automatic header missing")
	}
	if got := w.message(mid); got.Delivery != "sent" {
		t.Fatalf("delivery: %s", got.Delivery)
	}
	if status := remove(); status != http.StatusNoContent {
		t.Fatalf("delete after delivery: %d, want 204", status)
	}
}
