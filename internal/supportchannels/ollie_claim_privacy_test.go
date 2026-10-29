package supportchannels

import (
	"context"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

type auditPrivateModel struct {
	*fakeModel
	sawPrivate bool
}

func (m *auditPrivateModel) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if strings.Contains(b.Text, "confidential-after-private-transfer") {
				m.sawPrivate = true
			}
		}
	}
	return ai.Response{Text: `{"summary":"confidential-after-private-transfer","tried":[],"sentiment":"neutral","urgency":"normal","urgency_reason":""}`}, nil
}
func TestAuditSummaryReadsMessageAfterPrivateTransfer(t *testing.T) {
	w := newWorkflow(t)
	team := ids.New()
	w.e.Exec(`INSERT INTO teams(id,workspace_id,name,private) VALUES($1,$2,'Restricted',true)`, team, w.ws.ID)
	// Test-only trigger chooses a deterministic legal interleaving: the move and new
	// message commit after the public-team check and before ListMessages.
	w.e.Exec(`CREATE FUNCTION audit_private_summary_claim() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE tickets SET team_id=TG_ARGV[0]::uuid WHERE id=NEW.ticket_id;
 INSERT INTO messages(workspace_id,ticket_id,kind,direction,author_type,body_text)
 VALUES(NEW.workspace_id,NEW.ticket_id,'reply','inbound','system','confidential-after-private-transfer');
 RETURN NEW; END $$`)
	t.Cleanup(func() {
		w.e.Exec(`DROP TRIGGER IF EXISTS audit_private_summary_claim ON support_channel_threads`)
		w.e.Exec(`DROP FUNCTION audit_private_summary_claim()`)
	})
	w.e.Exec(`CREATE TRIGGER audit_private_summary_claim AFTER UPDATE OF summary_processing_expires_at ON support_channel_threads FOR EACH ROW WHEN (NEW.summary_processing_expires_at IS NOT NULL AND NEW.workspace_id='` + w.ws.ID.String() + `'::uuid) EXECUTE FUNCTION audit_private_summary_claim('` + team.String() + `')`)
	model := &auditPrivateModel{fakeModel: &fakeModel{}}
	w.s.ai = ai.For(w.e.D).WithProvider(model, nil, "fake-model", "fake-model")
	if err := w.s.postSummary(w.e.Ctx, jobs.SupportChannelSummaryArgs{DestinationID: threadDestination(t, w), TicketID: w.ticket.ID}); err != nil {
		t.Fatal(err)
	}
	t.Logf("model received private message=%v; external summary posts=%+v", model.sawPrivate, w.fake.interactive)
	if model.sawPrivate || len(w.fake.interactive) > 0 {
		t.Fatal("private-team message reached model and external channel after transfer")
	}
}
