package collab

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// PeerExcerpts reads only the owner's own accounts that share something.
func TestPeerExcerptsScope(t *testing.T) {
	w := newWorld(t)
	own := w.account(w.owner, ProviderGoogle, VisibilityPrivate, Audience{})
	quiet := w.account(w.owner, ProviderGoogle, VisibilityPrivate, Audience{})
	// Bea's accounts: one private, one shared with everyone, which the owner sees too.
	beas := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	shared := w.account(w.agent2, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	for _, a := range []*accountRow{own, quiet, beas, shared} {
		w.seedThread(a, "Acme renewal", w.folder(a, RoleInbox))
	}
	w.seedThread(own, "Acme renewal spam", w.folder(own, RoleSpam))

	modes := map[uuid.UUID]string{own.ID: PeerSummary, quiet.ID: PeerNone, beas.ID: PeerVerbatim, shared.ID: PeerVerbatim}
	got, err := PeerExcerpts(w.ctx, w.d.DB.Pool, w.wid, w.owner.UserID, modes, "When is the Acme renewal?", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subject != "Acme renewal" || got[0].Mode != PeerSummary || got[0].From != "Frank Sender" {
		t.Fatalf("excerpts %+v", got)
	}
	if strings.Contains(got[0].Text, "@") || !strings.Contains(got[0].Text, "Hello there") {
		t.Fatalf("excerpt text %q", got[0].Text)
	}
	// Another workspace's id for the owner finds nothing.
	if got, _ := PeerExcerpts(w.ctx, w.d.DB.Pool, uuid.New(), w.owner.UserID, modes, "acme", 8); len(got) != 0 {
		t.Fatal("found mail across workspaces")
	}
	// No words to search, or nothing shared: nothing is read.
	if got, _ := PeerExcerpts(w.ctx, w.d.DB.Pool, w.wid, w.owner.UserID, modes, "what is it?", 8); len(got) != 0 {
		t.Fatal("stop words matched")
	}
	if got, _ := PeerExcerpts(w.ctx, w.d.DB.Pool, w.wid, w.owner.UserID, map[uuid.UUID]string{own.ID: PeerNone}, "acme", 8); len(got) != 0 {
		t.Fatal("an account sharing nothing was read")
	}
	accts, err := OwnAccounts(w.ctx, w.d.DB.Pool, w.wid, w.agent2.UserID)
	if err != nil || len(accts) != 2 {
		t.Fatalf("own accounts %v %v", accts, err)
	}
}

// Ollie's requests carry ViaAssistant, and Collab refuses them, reads included.
func TestCollabRefusesTheAssistant(t *testing.T) {
	w := newWorld(t)
	w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	p := *w.agent
	if st := doJSON(t, http.MethodGet, w.api(&p).URL+"/v1/collab/threads", nil, nil); st != http.StatusOK {
		t.Fatalf("member: %d", st)
	}
	p.ViaAssistant = true
	for _, path := range []string{"/v1/collab/threads", "/v1/collab/accounts", "/v1/collab"} {
		if st := doJSON(t, http.MethodGet, w.api(&p).URL+path, nil, nil); st != http.StatusForbidden {
			t.Fatalf("assistant read %s: %d", path, st)
		}
	}
}
