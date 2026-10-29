package supportchannels

import (
	"context"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func TestAuditSLAWarningsRespectSuspension(t *testing.T) {
	w := newWorkflow(t)
	policy := ids.New()
	w.e.Exec(`INSERT INTO sla_policies(id,workspace_id,name,first_response_minutes,resolution_minutes) VALUES($1,$2,'Audit',60,480)`, policy, w.ws.ID)
	w.e.Exec(`UPDATE tickets SET sla_policy_id=$2,first_response_due_at=now()+interval '10 minutes' WHERE id=$1`, w.ticket.ID, policy)
	w.e.Exec(`UPDATE workspaces SET suspended_at=now() WHERE id=$1`, w.ws.ID)
	if err := w.s.warnSLAs(w.e.Ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("posts on suspended workspace: %q", w.fake.posts)
	if len(w.fake.posts) != 0 {
		t.Fatalf("sent %d SLA warnings for suspended workspace, want none", len(w.fake.posts))
	}
}

type auditSummaryModel struct {
	*fakeModel
	beforeReturn func()
}

func (m *auditSummaryModel) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	m.beforeReturn()
	return m.fakeModel.Complete(ctx, req)
}
func TestAuditSummaryRechecksPrivateTeamAfterModel(t *testing.T) {
	w := newWorkflow(t)
	team := ids.New()
	w.e.Exec(`INSERT INTO teams(id,workspace_id,name,private) VALUES($1,$2,'Restricted',true)`, team, w.ws.ID)
	model := &auditSummaryModel{fakeModel: &fakeModel{complete: `{"summary":"Sensitive new message","tried":[],"sentiment":"neutral","urgency":"normal","urgency_reason":""}`}, beforeReturn: func() {
		w.e.Exec(`UPDATE tickets SET team_id=$2 WHERE id=$1`, w.ticket.ID, team)
	}}
	w.s.ai = ai.For(w.e.D).WithProvider(model, nil, "fake-model", "fake-model")
	if err := w.s.postSummary(w.e.Ctx, jobs.SupportChannelSummaryArgs{DestinationID: threadDestination(t, w), TicketID: w.ticket.ID}); err != nil {
		t.Fatal(err)
	}
	t.Logf("external posts after ticket moved private during model call: %+v", w.fake.interactive)
	for _, post := range w.fake.interactive {
		if strings.Contains(post.Text, "Sensitive new message") {
			t.Fatal("summary posted into external channel after ticket moved to private team")
		}
	}
}
