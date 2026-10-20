package core

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const (
	// SessionMaxAge caps a session regardless of refresh activity: everyone signs in
	// again at least every 30 days (ASVS 3.3.2).
	SessionMaxAge = 30 * 24 * time.Hour
	// refreshReuseGrace tolerates two tabs refreshing with the same cookie at once.
	refreshReuseGrace = 30 * time.Second
)

// CoreAuthResult is every sign-in step's answer: tokens, a second-factor challenge, or a
// request to link an existing password account.
type CoreAuthResult struct {
	AccessToken  string    `json:"access_token,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	User         *CoreUser `json:"user,omitempty"`
	MFARequired  bool      `json:"mfa_required,omitempty"`
	MFAToken     string    `json:"mfa_token,omitempty"`
	LinkRequired bool      `json:"link_required,omitempty"`
	LinkToken    string    `json:"link_token,omitempty"`
	Email        string    `json:"email,omitempty"`
	RedirectTo   string    `json:"redirect_to,omitempty" doc:"App path to continue to (OAuth sign-in)"`
	// ChangePassword: the password signed in with no longer meets the rules (it is in the
	// breached list or shorter than they ask); the app asks for a new one.
	ChangePassword bool   `json:"change_password,omitempty" doc:"The password used is breached or too short for the current rules: ask for a new one"`
	RefreshToken   string `json:"refresh_token,omitempty" doc:"Native requests only (contracts/native.md): the refresh token, which browsers get as the account's od_rt_ cookie instead"`
}

type authOut struct {
	SetCookie    []string `header:"Set-Cookie"`
	CacheControl string   `header:"Cache-Control"`
	Body         CoreAuthResult
}

// refreshCookie is a refresh cookie (one per account, contracts/tokens.md). A __Host-
// cookie is host-only with Path=/, as the prefix requires; any other is scoped to the
// auth routes and COOKIE_DOMAIN.
func (s *svc) refreshCookie(name, value string, maxAge int) string {
	path, domain := "/v1/auth", s.d.Cfg.CookieDomain
	if strings.HasPrefix(name, hostCookiePrefix) {
		path, domain = "/", ""
	}
	c := http.Cookie{Name: name, Value: value, Path: path, Domain: domain,
		HttpOnly: true, Secure: s.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
	if maxAge < 0 {
		c.Expires = time.Unix(0, 0)
	}
	return c.String()
}

func (s *svc) clearCookie(name string) string {
	return s.refreshCookie(name, "", -1)
}

func (s *svc) clearCookieHeader(names ...string) http.Header {
	h := http.Header{}
	for _, n := range names {
		h.Add("Set-Cookie", s.clearCookie(n))
	}
	return h
}

// issueSession creates a session and answers with an access token and the refresh token:
// in the account's refresh cookie, or in the body for a native request. A browser keeps
// its other accounts; the session it held for this account, if any, is replaced.
func (s *svc) issueSession(ctx context.Context, q db.Querier, uid uuid.UUID, mfa bool, ri reqInfo) (*authOut, error) {
	var suspended bool
	if err := q.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM users WHERE id = $1`, uid).Scan(&suspended); err != nil {
		return nil, err
	}
	if suspended {
		return nil, ErrAccountSuspended()
	}
	var tidy []string
	if !ri.Native {
		var err error
		if tidy, err = s.adoptCookies(ctx, q, uid, ri); err != nil {
			return nil, err
		}
	}
	refresh := crypto.RandomToken(32)
	sid := ids.New()
	client := ri.Client
	if client == "" {
		client = "web"
	}
	if _, err := q.Exec(ctx, `INSERT INTO sessions (id, user_id, refresh_hash, user_agent, ip, mfa_verified, expires_at, client)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, sid, uid, crypto.HashToken(refresh), ri.UserAgent, ri.IP, mfa,
		time.Now().Add(auth.RefreshTTL), client); err != nil {
		return nil, err
	}
	out, err := s.tokenResponse(ctx, q, uid, sid, mfa, refresh, ri)
	if err != nil {
		return nil, err
	}
	out.SetCookie = append(out.SetCookie, tidy...)
	return out, nil
}

func (s *svc) tokenResponse(ctx context.Context, q db.Querier, uid, sid uuid.UUID, mfa bool, refresh string, ri reqInfo) (*authOut, error) {
	access, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAccess, SessionID: sid.String(), MFA: mfa},
		uid.String(), auth.AccessTTL)
	if err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE users SET last_seen_at = now() WHERE id = $1`, uid); err != nil {
		return nil, err
	}
	u, err := loadUser(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	out := &authOut{CacheControl: "no-store", Body: CoreAuthResult{AccessToken: access, ExpiresIn: int(auth.AccessTTL.Seconds()), User: &u}}
	if ri.Native {
		out.Body.RefreshToken = refresh
	} else {
		name := s.refreshCookieName(uid)
		out.SetCookie = []string{s.refreshCookie(name, refresh, int(auth.RefreshTTL.Seconds()))}
		if old := ri.cookieFor(uid); old != nil && old.Name != name {
			// The account's cookie moves to this instance's prefixed name.
			out.SetCookie = append(out.SetCookie, s.clearCookie(old.Name))
		}
	}
	return out, nil
}

// ErrAccountSuspended answers a session request of a suspended account (contracts/admin.md).
func ErrAccountSuspended() *httpx.Error {
	return httpx.E(403, "account_suspended", "This account is suspended. Contact support to restore it.")
}

func errRefreshInvalid() *httpx.Error {
	return httpx.E(401, "unauthenticated", "Your session has ended. Sign in again.")
}

// rotateRefresh trades a refresh token for a new one. A token that was already rotated
// (outside a short grace window) is treated as stolen and ends its session. With expect
// set, the token must belong to that account. keep reports a refusal that must leave the
// browser's cookie alone: a replay inside the grace window (another tab already holds the
// new token) or a token of another account.
func (s *svc) rotateRefresh(ctx context.Context, token string, expect uuid.UUID, ri reqInfo) (out *authOut, keep bool, err error) {
	if token == "" {
		return nil, false, errRefreshInvalid()
	}
	h := crypto.HashToken(token)
	var reusedUser, reusedSession uuid.UUID
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var sid, uid uuid.UUID
		var mfa, suspended bool
		var created, expires time.Time
		err := tx.QueryRow(ctx, `SELECT s.id, s.user_id, s.mfa_verified, s.created_at, s.expires_at, u.suspended_at IS NOT NULL
			FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.refresh_hash = $1 AND s.revoked_at IS NULL FOR UPDATE OF s`, h).Scan(&sid, &uid, &mfa, &created, &expires, &suspended)
		if db.IsNotFound(err) {
			var rotated *time.Time
			err = tx.QueryRow(ctx, `SELECT id, user_id, rotated_at FROM sessions
				WHERE prev_refresh_hash = $1 AND revoked_at IS NULL FOR UPDATE`, h).Scan(&sid, &uid, &rotated)
			if db.IsNotFound(err) {
				return errRefreshInvalid()
			}
			if err != nil {
				return err
			}
			if expect != uuid.Nil && uid != expect {
				keep = true
				return errRefreshInvalid()
			}
			if rotated != nil && time.Since(*rotated) < refreshReuseGrace {
				keep = true
				return errRefreshInvalid()
			}
			if _, err := endSession(ctx, tx, uid, sid); err != nil {
				return err
			}
			if _, err := auditUser(ctx, tx, userPrincipal(uid, ri), uid, "session.refresh_reused", map[string]any{"session_id": sid}); err != nil {
				return err
			}
			reusedUser, reusedSession = uid, sid
			return nil
		}
		if err != nil {
			return err
		}
		if expect != uuid.Nil && uid != expect {
			keep = true
			return errRefreshInvalid()
		}
		now := time.Now()
		if suspended || now.After(expires) || now.Sub(created) > SessionMaxAge {
			_, _ = tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, sid)
			return errRefreshInvalid()
		}
		next := crypto.RandomToken(32)
		newExp := now.Add(auth.RefreshTTL)
		if cap := created.Add(SessionMaxAge); newExp.After(cap) {
			newExp = cap
		}
		if _, err := tx.Exec(ctx, `UPDATE sessions SET prev_refresh_hash = refresh_hash, refresh_hash = $2, rotated_at = now(),
			last_used_at = now(), expires_at = $3, ip = $4, user_agent = $5 WHERE id = $1`,
			sid, crypto.HashToken(next), newExp, ri.IP, ri.UserAgent); err != nil {
			return err
		}
		out, err = s.tokenResponse(ctx, tx, uid, sid, mfa, next, ri)
		return err
	})
	if reusedUser != uuid.Nil {
		s.d.Log.Warn("refresh token reuse; session revoked", "user_id", reusedUser, "session_id", reusedSession)
		s.disconnect(reusedUser, reusedSession)
		return nil, false, errRefreshInvalid()
	}
	if err != nil {
		return nil, keep, err
	}
	return out, false, nil
}

// disconnect asks the gateway to close the user's sockets.
func (s *svc) disconnect(uid, sid uuid.UUID) {
	if s.d.NATS == nil || s.d.NATS.Conn == nil {
		return
	}
	session := ""
	if sid != uuid.Nil {
		session = sid.String()
	}
	events.Disconnect(s.d.NATS.Conn, uid.String(), session)
}

// Every path that ends a session goes through endSession or revokeOtherSessions, which
// delete the session's push devices and browser subscriptions in the same transaction: a
// signed-out phone or browser stops receiving pushes at once. (Deleting a user or a
// session row cascades to both.)

// endSession revokes one live session of uid and deletes its push devices. It reports
// whether a live session was ended.
func endSession(ctx context.Context, q db.Querier, uid, sid uuid.UUID) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, sid, uid)
	if err != nil {
		return false, err
	}
	if _, err := q.Exec(ctx, `DELETE FROM push_devices WHERE session_id = $1`, sid); err != nil {
		return false, err
	}
	if _, err := q.Exec(ctx, `DELETE FROM web_push_subscriptions WHERE session_id = $1`, sid); err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// revokeOtherSessions ends every session of uid except keep (uuid.Nil ends all) and
// deletes their push devices. The user's admin panel sessions (contracts/admin.md) end
// with them: a password change or "sign out everywhere" leaves none behind.
func revokeOtherSessions(ctx context.Context, q db.Querier, uid, keep uuid.UUID) error {
	if _, err := q.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`, uid, keep); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE admin_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, uid); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM push_devices WHERE user_id = $1 AND session_id <> $2`, uid, keep); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM web_push_subscriptions WHERE user_id = $1 AND session_id <> $2`, uid, keep)
	return err
}

// CoreSession is one row of GET /v1/me/sessions.
type CoreSession struct {
	ID         uuid.UUID `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Current    bool      `json:"current"`
	Client     string    `json:"client" enum:"web,desktop,ios" doc:"The app the session signed in from"`
}

func (s *svc) registerSessions(api huma.API) {
	register(s, api, huma.Operation{OperationID: "list-sessions", Method: http.MethodGet, Path: "/v1/me/sessions",
		Summary: "List your active sessions", Tags: []string{"Account"}},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[CoreSession] }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var cur struct {
				T  time.Time `json:"t"`
				ID uuid.UUID `json:"id"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			if cur.T.IsZero() {
				cur.T = time.Now().Add(24 * time.Hour)
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT id, user_agent, ip, created_at, last_used_at, client FROM sessions
				WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() AND (last_used_at, id) < ($2, $3)
				ORDER BY last_used_at DESC, id DESC LIMIT $4`, p.UserID, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreSession, error) {
				var x CoreSession
				err := r.Scan(&x.ID, &x.UserAgent, &x.IP, &x.CreatedAt, &x.LastUsedAt, &x.Client)
				x.Current = x.ID == p.SessionID
				return x, err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(list, in.Limit, func(x CoreSession) any {
				return map[string]any{"t": x.LastUsedAt, "id": x.ID}
			})
			return &struct{ Body httpx.Page[CoreSession] }{page}, nil
		})

	register(s, api, huma.Operation{OperationID: "revoke-session", Method: http.MethodDelete, Path: "/v1/me/sessions/{id}",
		Summary: "Sign out a session", Tags: []string{"Account"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			sid, err := parseID(in.ID, "Session")
			if err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				ended, err := endSession(ctx, tx, p.UserID, sid)
				if err != nil {
					return err
				}
				if !ended {
					return httpx.NotFound("Session")
				}
				_, err = auditUser(ctx, tx, p, p.UserID, "session.revoked", map[string]any{"session_id": sid})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.disconnect(p.UserID, sid)
			return nil, nil
		})
}

// constantTimeEq compares two strings without leaking where they differ.
func constantTimeEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
