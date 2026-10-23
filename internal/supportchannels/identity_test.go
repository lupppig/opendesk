package supportchannels

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

// Only a linked, active agent or above of the connection's own workspace acts from a
// channel: runs commands, or asks Ollie to create a ticket.
func TestOnlyALinkedActiveAgentActsFromAChannel(t *testing.T) {
	e := hdtest.New(t)
	ws, other := e.NewWorkspace(), e.NewWorkspace()
	s := For(e.D)
	connect := func(wid uuid.UUID, account string) connection {
		c := connection{ID: ids.New(), WorkspaceID: wid, Provider: "slack"}
		e.Exec(`INSERT INTO support_channel_connections (id, workspace_id, provider, name, external_account_id, credentials_enc)
			VALUES ($1, $2, 'slack', 'Acme', $3, $4)`, c.ID, wid, account, []byte{0})
		return c
	}
	link := func(c connection, external string, uid uuid.UUID) {
		e.Exec(`INSERT INTO support_channel_identities (id, workspace_id, connection_id, external_user_id, user_id) VALUES ($1, $2, $3, $4, $5)`,
			ids.New(), c.WorkspaceID, c.ID, external, uid)
	}
	c, c2 := connect(ws.ID, "T1"), connect(other.ID, "T2")
	link(c, "U-agent", ws.Agent.ID)
	link(c, "U-admin", ws.Admin.ID)
	link(c, "U-viewer", ws.Viewer.ID)
	// Linked on another workspace's connection, without being a member there.
	link(c2, "U-agent", ws.Agent.ID)

	for _, x := range []struct {
		external string
		user     uuid.UUID
		role     auth.Role
	}{{"U-agent", ws.Agent.ID, auth.RoleAgent}, {"U-admin", ws.Admin.ID, auth.RoleAdmin}} {
		p, err := s.externalPrincipal(e.Ctx, c, x.external)
		if err != nil || p.UserID != x.user || p.WorkspaceID != ws.ID || p.Role != x.role || p.Kind != auth.KindUser {
			t.Fatalf("%s: %+v %v", x.external, p, err)
		}
	}
	if _, err := s.externalPrincipal(e.Ctx, c, "U-viewer"); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("viewer: %v", err)
	}
	if _, err := s.externalPrincipal(e.Ctx, c, "U-nobody"); !db.IsNotFound(err) {
		t.Errorf("unlinked: %v", err)
	}
	if _, err := s.externalPrincipal(e.Ctx, c2, "U-agent"); !db.IsNotFound(err) {
		t.Errorf("not a member of the connection's workspace: %v", err)
	}
	e.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, ws.Agent.ID)
	if _, err := s.externalPrincipal(e.Ctx, c, "U-agent"); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("suspended: %v", err)
	}
	e.Exec(`UPDATE workspaces SET suspended_at = now() WHERE id = $1`, ws.ID)
	if _, err := s.externalPrincipal(e.Ctx, c, "U-admin"); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("suspended workspace: %v", err)
	}
	e.Exec(`DELETE FROM members WHERE workspace_id = $1 AND user_id = $2`, ws.ID, ws.Viewer.ID)
	if _, err := s.externalPrincipal(e.Ctx, c, "U-viewer"); !db.IsNotFound(err) {
		t.Errorf("removed member: %v", err)
	}
}

// A connection's health is written in its own workspace and announced to it; a signed
// webhook announces healthy once.
func TestHealthChangesAreAnnounced(t *testing.T) {
	e := hdtest.New(t)
	ws := e.NewWorkspace()
	s := For(e.D)
	c := connection{ID: ids.New(), WorkspaceID: ws.ID, Provider: "slack", HealthStatus: "unknown"}
	e.Exec(`INSERT INTO support_channel_connections (id, workspace_id, provider, name, external_account_id, credentials_enc)
		VALUES ($1, $2, 'slack', 'Acme', 'T1', $3)`, c.ID, ws.ID, []byte{0})
	updates := func() int {
		var n int
		e.Scalar(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'support_channel_connection.updated'`, &n, ws.ID)
		return n
	}
	c = s.storeHealth(e.Ctx, c, channel.Health{Status: "action_required", Detail: "Point the webhook here."})
	var status string
	e.Scalar(`SELECT health_status FROM support_channel_connections WHERE id = $1`, &status, c.ID)
	if status != "action_required" || c.HealthStatus != "action_required" || updates() != 1 {
		t.Fatalf("stored %q, connection %+v, updates %d", status, c, updates())
	}
	s.markWebhookHealthy(e.Ctx, c, false)
	s.markWebhookHealthy(e.Ctx, c, false) // a second webhook changes nothing
	e.Scalar(`SELECT health_status FROM support_channel_connections WHERE id = $1`, &status, c.ID)
	if status != "healthy" || updates() != 2 {
		t.Fatalf("after webhooks %q, updates %d", status, updates())
	}

	// Another workspace's id writes nothing.
	stranger := c
	stranger.WorkspaceID = e.NewWorkspace().ID
	s.storeHealth(e.Ctx, stranger, channel.Health{Status: "error", Detail: "x"})
	e.Scalar(`SELECT health_status FROM support_channel_connections WHERE id = $1`, &status, c.ID)
	if status != "healthy" {
		t.Errorf("another workspace changed the health to %q", status)
	}
}
