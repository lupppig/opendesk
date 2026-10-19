package core

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Ops is what the instance admin area (internal/admin, contracts/admin.md) does through
// core: the same checks, removals and session endings the app's own routes use.
type Ops struct{ s *svc }

func NewOps(d *deps.Deps) *Ops { return &Ops{s: newSvc(d)} }

// CheckSecondFactor verifies a TOTP code (its step is consumed, so it cannot be replayed)
// or consumes a recovery code of uid. A wrong code is 401 invalid_code.
func (o *Ops) CheckSecondFactor(ctx context.Context, tx pgx.Tx, uid uuid.UUID, code string) (string, error) {
	return o.s.checkSecondFactor(ctx, tx, uid, code)
}

// DeleteWorkspace removes a workspace and everything it owns, like its owner's "Delete
// workspace". The caller commits tx, then calls Finish with the result.
func (o *Ops) DeleteWorkspace(ctx context.Context, tx pgx.Tx, wid uuid.UUID) (Removal, error) {
	members, keys, err := o.s.deleteWorkspace(ctx, tx, wid)
	return Removal{disconnect: members, files: keys}, err
}

// CreateWorkspace creates a workspace owned by owner with the defaults of the app's own
// "Create workspace" (default brand and widget, views, labels, SLA policy). actor is who
// created it, for the workspace's audit log.
func (o *Ops) CreateWorkspace(ctx context.Context, tx pgx.Tx, actor *auth.Principal, owner uuid.UUID, name string) (uuid.UUID, error) {
	w, err := o.s.createWorkspace(ctx, tx, actor, owner, name)
	return w.ID, err
}

// CreateTestAccount creates the owner account of a test workspace (contracts/admin.md
// "Test workspaces"): the operator hands over its password, which stops working at
// expires; its address is a reserved .invalid one that receives nothing, so it is marked
// confirmed and no email is sent. 409 email_taken when the address has an account.
func (o *Ops) CreateTestAccount(ctx context.Context, tx pgx.Tx, email, name, passwordHash string, expires time.Time) (uuid.UUID, error) {
	uid := ids.New()
	_, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, email_verified_at, test_account, password_expires_at)
		VALUES ($1, $2, $3, $4, now(), true, $5)`, uid, email, name, passwordHash, expires)
	if db.IsUniqueViolation(err, "") {
		return uuid.Nil, httpx.E(409, "email_taken", "An account with this email already exists.")
	}
	return uid, err
}

// SetTestPassword gives a test account a new password and expiry, and ends its sessions
// and connected apps like a password reset. Call Disconnect after the commit. 409
// not_test_account for any other account.
func (o *Ops) SetTestPassword(ctx context.Context, tx pgx.Tx, actor *auth.Principal, uid uuid.UUID, passwordHash string, expires time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, password_expires_at = $3, updated_at = now() WHERE id = $1 AND test_account`,
		uid, passwordHash, expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.E(409, "not_test_account", "Only a test account's password is set by an admin.")
	}
	if err := revokeOtherSessions(ctx, tx, uid, uuid.Nil); err != nil {
		return err
	}
	if _, err := oauthgrant.Revoke(ctx, tx, actor, uid, uuid.Nil, uuid.Nil, oauthgrant.ReasonSignedOut); err != nil {
		return err
	}
	_, err = auditUser(ctx, tx, actor, uid, "password.reset", InstanceAdminData(nil))
	return err
}

// DeleteAccount removes a user like their own "Delete account": the workspaces they own
// alone go with them. 409 sole_owner while they own a workspace with other members. The
// workspaces they leave record member.account_deleted with actor (attribution).
func (o *Ops) DeleteAccount(ctx context.Context, tx pgx.Tx, actor *auth.Principal, uid uuid.UUID) (Removal, error) {
	disconnect, keys, err := o.s.deleteAccount(ctx, tx, actor, uid)
	return Removal{disconnect: disconnect, files: keys}, err
}

// Removal is what is left to do once a deletion committed.
type Removal struct {
	disconnect []uuid.UUID
	files      []string
}

// Finish closes the removed people's sockets and purges the stored files.
func (o *Ops) Finish(r Removal) {
	o.s.purgeFiles(r.files)
	for _, uid := range r.disconnect {
		o.s.disconnect(uid, uuid.Nil)
	}
}

// EndSessions ends every app session of uid (and their push devices). Call Disconnect
// after the commit.
func (o *Ops) EndSessions(ctx context.Context, tx pgx.Tx, uid uuid.UUID) error {
	return revokeOtherSessions(ctx, tx, uid, uuid.Nil)
}

// EndSession ends one app session of uid; false when it was not live.
func (o *Ops) EndSession(ctx context.Context, tx pgx.Tx, uid, sid uuid.UUID) (bool, error) {
	return endSession(ctx, tx, uid, sid)
}

// Disconnect asks the gateway to close the user's sockets: of one session, or all with
// uuid.Nil.
func (o *Ops) Disconnect(uid, sid uuid.UUID) { o.s.disconnect(uid, sid) }

// SendVerifyEmail sends a new email confirmation link.
func (o *Ops) SendVerifyEmail(ctx context.Context, tx pgx.Tx, uid uuid.UUID, email string) error {
	return o.s.sendVerifyEmail(ctx, tx, uid, email)
}

// SendTwoFactorDisabled tells the user an administrator turned off their two-factor
// authentication.
func (o *Ops) SendTwoFactorDisabled(ctx context.Context, tx pgx.Tx, email string) error {
	return o.s.enqueueMail(ctx, tx, email, emails.TwoFactorDisabled(o.s.site()))
}

// AuditUser records an operator's action on a user's account in each of their
// workspaces' audit logs, with no actor user and data.actor = "instance_admin".
func (o *Ops) AuditUser(ctx context.Context, tx pgx.Tx, actor *auth.Principal, uid uuid.UUID, action string, data map[string]any) error {
	_, err := auditUser(ctx, tx, actor, uid, action, InstanceAdminData(data))
	return err
}

// AuditWorkspace records an operator's action in the workspace's audit log.
func (o *Ops) AuditWorkspace(ctx context.Context, tx pgx.Tx, actor *auth.Principal, wid uuid.UUID, action string, data map[string]any) error {
	return audit.Log(ctx, tx, actor, wid, action, "workspace", &wid, InstanceAdminData(data))
}

// InstanceAdminData tags audit data as the instance operator's.
func InstanceAdminData(data map[string]any) map[string]any {
	out := make(map[string]any, len(data)+1)
	for k, v := range data {
		out[k] = v
	}
	out["actor"] = "instance_admin"
	return out
}
