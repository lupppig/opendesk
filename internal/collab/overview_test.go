package collab

import (
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/plan"
)

// The overview's mail numbers are GET /v1/collab/counts for that workspace, and nothing
// for a workspace without Collab.
func TestOverviewCounts(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	w.seedThread(a, "Hello", w.folder(a, RoleInbox))

	got, err := w.s.overviewCounts(w.ctx, w.agent)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := got.(CollabCounts)
	if !ok || c.Boxes.Inbox != 1 {
		t.Fatalf("counts: %#v", got)
	}
	// Someone else's private account counts nothing for them.
	if got, _ := w.s.overviewCounts(w.ctx, w.agent2); got.(CollabCounts).Boxes.Inbox != 0 {
		t.Fatalf("leaked another's private mail: %#v", got)
	}

	// Billing on and the workspace on the free plan: no Collab, no numbers.
	billed := *w.d
	billed.Plan = plan.New(true, 1)
	s := newService(&billed)
	if got, err := s.overviewCounts(w.ctx, w.agent); err != nil || got != nil {
		t.Fatalf("without Collab: %#v %v", got, err)
	}
	// Only a person's own request has mail numbers.
	key := &auth.Principal{Kind: auth.KindAPIKey, WorkspaceID: w.wid}
	if got, err := w.s.overviewCounts(w.ctx, key); err != nil || got != nil {
		t.Fatalf("api key: %#v %v", got, err)
	}
}
