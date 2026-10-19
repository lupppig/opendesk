package email

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// What a workspace's mailboxes may receive with (contracts/api.md "Mailboxes"):
//
//   - forwarding: always.
//   - generic IMAP (inbound_kind imap): while the instance has MAILBOX_IMAP on, or the
//     workspace has Collab (plan.Entitlements.Collab) and the instance offers Collab IMAP
//     (COLLAB_IMAP). The network guard and TLS rules are the same either way.
//   - Google or Microsoft over IMAP (a sync grant): only while MAILBOX_IMAP is on; it
//     needs the restricted scopes.
//   - Google or Microsoft through their APIs (an api grant): while the workspace has
//     Collab and the provider's Collab OAuth client is configured.

// access is what one workspace may use; read it once per request or tick (s.access).
type access struct {
	collab bool
}

// access reads the workspace's entitlements.
func (s *service) access(ctx context.Context, q db.Querier, wid uuid.UUID) (access, error) {
	e, err := s.d.Plan.Entitlements(ctx, q, wid)
	if err != nil {
		return access{}, err
	}
	return access{collab: e.Collab}, nil
}

// imapAllowed reports whether the workspace's mailboxes may receive over generic IMAP.
func (s *service) imapAllowed(a access) bool {
	return s.mailboxIMAP || (s.d.Cfg.CollabIMAP && a.collab)
}

// imapReceives reports whether a mailbox that reads over IMAP is polled: generic IMAP
// per workspace, a Google or Microsoft sync connection per instance.
func (s *service) imapReceives(m *mailboxRow, a access) bool {
	if m.InboundKind == InboundIMAP {
		return s.imapAllowed(a)
	}
	return s.mailboxIMAP
}

// imapReceivesNow is imapReceives with the workspace's entitlements read now.
func (s *service) imapReceivesNow(ctx context.Context, m *mailboxRow) (bool, error) {
	if s.mailboxIMAP {
		return true, nil
	}
	if m.InboundKind != InboundIMAP {
		return false, nil
	}
	a, err := s.access(ctx, s.d.DB.Pool, m.WorkspaceID)
	if err != nil {
		return false, err
	}
	return s.imapReceives(m, a), nil
}

// apiConfigured reports whether provider's Collab OAuth client is set up.
func (s *service) apiConfigured(provider string) bool {
	return s.apiClients[provider].configured()
}

// apiConnect reports whether a new mailbox may connect provider's Collab client to read
// its inbox directly: the client is configured and, when it is the mailbox client itself,
// that provider's mailbox sign-in is on (GOOGLE_MAILBOX_OAUTH, MICROSOFT_MAILBOX_OAUTH).
func (s *service) apiConnect(provider string) bool {
	c := s.apiClients[provider]
	if !c.configured() {
		return false
	}
	if mp := s.providers[provider]; mp.configured() && mp.clientID == c.clientID {
		return s.oauthConnect(provider)
	}
	return true
}

// apiSync reports whether the workspace may connect provider to read the inbox directly.
func (s *service) apiSync(provider string, a access) bool {
	return a.collab && s.apiConnect(provider)
}

// apiReceives reports whether a mailbox with an api grant is read: the workspace has
// Collab and the provider's Collab client is configured (a reconnect needs no more).
func (s *service) apiReceives(provider string, a access) bool {
	return a.collab && s.apiConfigured(provider)
}

// imapOff answers a request to receive over IMAP while that is off for the workspace.
func imapOff() error {
	return httpx.E(http.StatusUnprocessableEntity, "imap_off", "Receiving over IMAP is off on this instance. Forward the address to opendesk instead.")
}

// apiSyncUnavailable answers a request to read a Google or Microsoft inbox directly while
// the workspace or instance cannot.
func apiSyncUnavailable(provider string, a access) error {
	detail := "Reading the inbox directly needs Collab on this workspace's plan. Forward its mail to opendesk instead."
	if a.collab {
		detail = "Reading the inbox directly through " + providerLabel(provider) + " is not set up on this instance. Forward its mail to opendesk instead."
	}
	return httpx.E(http.StatusUnprocessableEntity, "api_sync_unavailable", detail)
}

// errAPINeedsCollab is what a mailbox that reads through the provider's API reports while
// its workspace does not have Collab: it is kept, and resumes when Collab is back.
var errAPINeedsCollab = &imapError{msg: "Reading this inbox directly needs Collab. Forward its mail or connect it again."}
