package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/version"
)

// RefreshCookie holds the admin panel's refresh token (contracts/admin.md). On https it
// is set as __Host-od_admin (HostRefreshCookie): host-only and Path=/, so no sibling
// subdomain can plant or overwrite it (ASVS 3.4.4).
const (
	RefreshCookie     = "od_admin"
	HostRefreshCookie = "__Host-" + RefreshCookie
)

const (
	// SessionIdle ends a session this long after its last refresh.
	SessionIdle = time.Hour
	// SessionMaxAge ends a session this long after sign-in, whatever its activity.
	SessionMaxAge = 12 * time.Hour
	// refreshReuseGrace tolerates two tabs refreshing with the same cookie at once.
	refreshReuseGrace = 30 * time.Second

	loginPerIP      = 30
	loginPerAccount = 10
	mfaPerUser      = 10
	mfaPerUserDay   = 30
	loginWindow     = 15 * time.Minute
)

func errInvalidCredentials() *httpx.Error {
	return httpx.E(401, "invalid_credentials", "The email or password is incorrect.")
}

func errSessionEnded() *httpx.Error {
	return httpx.E(401, "unauthenticated", "Your admin session has ended. Sign in again.")
}

func errMFAToken() *httpx.Error {
	return httpx.E(401, "invalid_mfa_token", "Your sign-in attempt expired. Start again.")
}

// AdminInstance describes the instance to the panel.
type AdminInstance struct {
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	BillingEnabled bool   `json:"billing_enabled"`
	AIEnabled      bool   `json:"ai_enabled"`
	AppURL         string `json:"app_url"`
	APIURL         string `json:"api_url"`
	SelfHosted     bool   `json:"self_hosted"`
}

// AdminMe is the signed-in admin.
type AdminMe struct {
	ID          uuid.UUID     `json:"id"`
	Email       string        `json:"email"`
	Name        string        `json:"name"`
	Role        Role          `json:"role" enum:"viewer,support,super"`
	Permissions []string      `json:"permissions"`
	Instance    AdminInstance `json:"instance"`
}

// AdminAuthResult answers a completed sign-in and every refresh.
type AdminAuthResult struct {
	AccessToken string  `json:"access_token"`
	ExpiresIn   int     `json:"expires_in"`
	Admin       AdminMe `json:"admin"`
}

type authOut struct {
	SetCookie    []string `header:"Set-Cookie"`
	CacheControl string   `header:"Cache-Control"`
	Body         AdminAuthResult
}

func (s *svc) me(a *Admin) AdminMe {
	cfg := s.d.Cfg
	return AdminMe{ID: a.UserID, Email: a.Email, Name: a.Name, Role: a.Role, Permissions: a.Role.Permissions(),
		Instance: AdminInstance{Version: version.Version, Commit: version.Commit, BillingEnabled: cfg.BillingEnabled(),
			AIEnabled: cfg.AIEnabled(), AppURL: cfg.AppURL, APIURL: cfg.APIURL, SelfHosted: !cfg.BillingEnabled()}}
}

// cookie is the refresh cookie: host-only, HttpOnly, SameSite=Lax; __Host- prefixed on
// https, otherwise scoped to the admin auth routes. It is read only by POSTs that carry
// the panel's Origin, so Lax refuses the same cross-site requests Strict would, and the
// browser sends it on every load of the panel (Safari withholds a Strict cookie from a
// page restored at launch or reached from another site).
func (s *svc) cookie(value string, maxAge int) string {
	c := http.Cookie{Name: RefreshCookie, Value: value, Path: "/v1/admin/auth", HttpOnly: true,
		Secure: s.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
	if s.d.Cfg.CookieSecure {
		c.Name, c.Path = HostRefreshCookie, "/"
	}
	if maxAge < 0 {
		c.Expires = time.Unix(0, 0)
	}
	return c.String()
}

// checkOrigin refuses an admin auth request that does not come from the panel's origin.
func (s *svc) checkOrigin(ri reqInfo) error {
	if want := s.d.Cfg.AdminOrigin(); want == "" || ri.Origin != want {
		return httpx.E(403, "origin_refused", "Sign in from the admin panel.")
	}
	return nil
}

func (s *svc) registerAuth(api huma.API) {
	op(s, api, huma.Operation{OperationID: "admin-login", Method: http.MethodPost, Path: "/v1/admin/auth/login",
		Summary: "Admin sign-in: email and password", Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Email          string `json:"email" maxLength:"320"`
				Password       string `json:"password" maxLength:"1024"`
				TurnstileToken string `json:"turnstile_token,omitempty" maxLength:"4096"`
			}
		}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         struct {
				MFAToken string `json:"mfa_token"`
			}
		}, error) {
			tok, err := s.login(ctx, in.Body.Email, in.Body.Password, in.Body.TurnstileToken)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				CacheControl string `header:"Cache-Control"`
				Body         struct {
					MFAToken string `json:"mfa_token"`
				}
			}{CacheControl: "no-store"}
			out.Body.MFAToken = tok
			return out, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-mfa", Method: http.MethodPost, Path: "/v1/admin/auth/mfa",
		Summary: "Admin sign-in: the authenticator code", Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				MFAToken string `json:"mfa_token" maxLength:"2048"`
				Code     string `json:"code" maxLength:"64" doc:"6-digit code or a recovery code"`
			}
		}) (*authOut, error) {
			out, err := s.mfa(ctx, in.Body.MFAToken, in.Body.Code)
			return out, httpx.Wrap(err)
		})

	op(s, api, huma.Operation{OperationID: "admin-refresh", Method: http.MethodPost, Path: "/v1/admin/auth/refresh",
		Summary: "Rotate the admin refresh cookie for a new access token", Security: []map[string][]string{}},
		func(ctx context.Context, _ *struct{}) (*authOut, error) {
			out, err := s.refresh(ctx)
			if err != nil {
				// A refused cookie is cleared; a request that carried none leaves the
				// browser's alone.
				if he, ok := err.(*httpx.Error); ok && he.Status == 401 && info(ctx).Cookie != "" {
					return nil, huma.ErrorWithHeaders(he, http.Header{"Set-Cookie": {s.cookie("", -1)}})
				}
				return nil, httpx.Wrap(err)
			}
			return out, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-logout", Method: http.MethodPost, Path: "/v1/admin/auth/logout",
		Summary: "Sign out of the admin panel", Security: []map[string][]string{}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct {
			SetCookie string `header:"Set-Cookie"`
		}, error) {
			if err := s.logout(ctx); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct {
				SetCookie string `header:"Set-Cookie"`
			}{s.cookie("", -1)}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-me", Method: http.MethodGet, Path: "/v1/admin/me", Summary: "The signed-in admin"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body AdminMe }, error) {
			a, err := require(ctx, PermRead)
			if err != nil {
				return nil, err
			}
			return &struct{ Body AdminMe }{s.me(a)}, nil
		})
}

func (s *svc) login(ctx context.Context, email, password, turnstile string) (string, error) {
	ri := info(ctx)
	if err := s.checkOrigin(ri); err != nil {
		return "", err
	}
	if err := s.limitShared(ctx, "admin:login:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
		return "", err
	}
	// The per-account limit counts only requests that passed the captcha, so nobody
	// can lock an admin out without solving one per attempt.
	if err := s.d.Captcha.Verify(ctx, turnstile, ri.IP); err != nil {
		return "", err
	}
	email = normEmail(email)
	if email != "" {
		if err := s.limitShared(ctx, "login:email:"+email, loginPerAccount, loginWindow); err != nil {
			return "", err
		}
	}
	var uid uuid.UUID
	var hash *string
	var totp, suspended bool
	var role *Role
	found := false
	if email != "" {
		err := s.d.DB.Pool.QueryRow(ctx, `SELECT u.id, `+auth.PasswordHashSQL+`, u.totp_enabled, u.suspended_at IS NOT NULL, a.role
			FROM users u LEFT JOIN instance_admins a ON a.user_id = u.id WHERE u.email = $1`, email).Scan(&uid, &hash, &totp, &suspended, &role)
		if err != nil && !db.IsNotFound(err) {
			return "", err
		}
		found = err == nil
	}
	if !found || hash == nil {
		auth.DummyVerify(password)
		s.loginFailed(ctx, uid, role != nil, ri, "password")
		return "", errInvalidCredentials()
	}
	if !auth.VerifyPassword(password, *hash) {
		s.loginFailed(ctx, uid, role != nil, ri, "password")
		return "", errInvalidCredentials()
	}
	switch {
	case role == nil:
		// Answered like a wrong password: the panel never tells whether a password was
		// right for an account that cannot sign in to it.
		s.loginFailed(ctx, uid, false, ri, "password")
		return "", errInvalidCredentials()
	case suspended:
		return "", httpx.E(403, "account_suspended", "This account is suspended.")
	case !totp:
		return "", httpx.E(403, "admin_mfa_setup_required", "Turn on two-factor authentication in your account's security settings, then sign in again.")
	}
	tok, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAdminMFA}, uid.String(), auth.MFATTL)
	return tok, err
}

// loginFailed records a failed sign-in of an admin in the admin audit log; anyone else
// gets a log line without the address.
func (s *svc) loginFailed(ctx context.Context, uid uuid.UUID, isAdmin bool, ri reqInfo, method string) {
	if uid == uuid.Nil || !isAdmin {
		attrs := []any{"method", method, "ip", ri.IP}
		if uid != uuid.Nil {
			attrs = append(attrs, "user_id", uid)
		}
		s.d.Log.Info("admin sign-in failed", attrs...)
		return
	}
	a := &Admin{UserID: uid, IP: ri.IP}
	_ = s.d.DB.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, uid).Scan(&a.Email)
	if err := s.audit(context.WithoutCancel(ctx), s.d.DB.Pool, a, "login.failed", "admin", uid.String(), map[string]any{"method": method}); err != nil {
		s.d.Log.Error("admin: audit failed sign-in", "err", err)
	}
}

func (s *svc) mfa(ctx context.Context, mfaToken, code string) (*authOut, error) {
	ri := info(ctx)
	if err := s.checkOrigin(ri); err != nil {
		return nil, err
	}
	if err := s.limitShared(ctx, "admin:mfa:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	c, err := s.d.Tokens.Verify(mfaToken, auth.PurposeAdminMFA)
	if err != nil {
		return nil, errMFAToken()
	}
	uid, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, errMFAToken()
	}
	if err := s.limitShared(ctx, "admin:mfa:user:"+uid.String(), mfaPerUser, loginWindow); err != nil {
		return nil, err
	}
	if err := s.limitShared(ctx, "admin:mfa:user:day:"+uid.String(), mfaPerUserDay, 24*time.Hour); err != nil {
		return nil, err
	}
	var out *authOut
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		a, err := s.loadAdmin(ctx, tx, uid, ri)
		if err != nil {
			return err
		}
		factor, err := s.ops.CheckSecondFactor(ctx, tx, uid, code)
		if err != nil {
			return err
		}
		refresh := crypto.RandomToken(32)
		a.SessionID = ids.New()
		now := time.Now()
		if _, err := tx.Exec(ctx, `INSERT INTO admin_sessions (id, user_id, refresh_hash, ip, user_agent, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, a.SessionID, uid, crypto.HashToken(refresh), ri.IP, ri.UserAgent, now.Add(SessionIdle)); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, a, "login", "admin", uid.String(), map[string]any{"factor": factor}); err != nil {
			return err
		}
		out, err = s.tokens(a, refresh, int(SessionIdle.Seconds()))
		return err
	})
	if he, ok := err.(*httpx.Error); ok && he.Code == "invalid_code" {
		s.loginFailed(ctx, uid, true, ri, "mfa")
	}
	return out, err
}

// loadAdmin reads an admin who may sign in: still an admin, not suspended, with
// two-factor authentication on.
func (s *svc) loadAdmin(ctx context.Context, q db.Querier, uid uuid.UUID, ri reqInfo) (*Admin, error) {
	a := &Admin{UserID: uid, IP: ri.IP, UserAgent: ri.UserAgent}
	var totp, suspended bool
	err := q.QueryRow(ctx, `SELECT u.email, u.name, a.role, u.totp_enabled, u.suspended_at IS NOT NULL
		FROM users u JOIN instance_admins a ON a.user_id = u.id WHERE u.id = $1`, uid).Scan(&a.Email, &a.Name, &a.Role, &totp, &suspended)
	if db.IsNotFound(err) {
		return nil, httpx.E(403, "not_admin", "This account is not an admin of this instance.")
	}
	if err != nil {
		return nil, err
	}
	if suspended {
		return nil, httpx.E(403, "account_suspended", "This account is suspended.")
	}
	if !totp {
		return nil, httpx.E(403, "admin_mfa_setup_required", "Turn on two-factor authentication in your account's security settings, then sign in again.")
	}
	return a, nil
}

func (s *svc) tokens(a *Admin, refresh string, maxAge int) (*authOut, error) {
	access, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAdmin, SessionID: a.SessionID.String()}, a.UserID.String(), auth.AdminTTL)
	if err != nil {
		return nil, err
	}
	return &authOut{SetCookie: []string{s.cookie(refresh, maxAge)}, CacheControl: "no-store",
		Body: AdminAuthResult{AccessToken: access, ExpiresIn: int(auth.AdminTTL.Seconds()), Admin: s.me(a)}}, nil
}

// refresh rotates the session's refresh token. A token that was already rotated, presented
// again outside the grace window, is treated as stolen and ends the session.
func (s *svc) refresh(ctx context.Context) (*authOut, error) {
	ri := info(ctx)
	if err := s.checkOrigin(ri); err != nil {
		return nil, err
	}
	if err := s.limit("admin:refresh:ip:"+ri.IP, 120, time.Minute); err != nil {
		return nil, err
	}
	if ri.Cookie == "" || len(ri.Cookie) > 200 {
		return nil, errSessionEnded()
	}
	h := crypto.HashToken(ri.Cookie)
	var out *authOut
	var reused *Admin
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var sid, uid uuid.UUID
		var created, expires time.Time
		err := tx.QueryRow(ctx, `SELECT id, user_id, created_at, expires_at FROM admin_sessions
			WHERE refresh_hash = $1 AND revoked_at IS NULL FOR UPDATE`, h).Scan(&sid, &uid, &created, &expires)
		if db.IsNotFound(err) {
			var rotated *time.Time
			err = tx.QueryRow(ctx, `SELECT id, user_id, rotated_at FROM admin_sessions
				WHERE prev_refresh_hash = $1 AND revoked_at IS NULL FOR UPDATE`, h).Scan(&sid, &uid, &rotated)
			if db.IsNotFound(err) {
				return errSessionEnded()
			}
			if err != nil {
				return err
			}
			if rotated != nil && time.Since(*rotated) < refreshReuseGrace {
				return httpx.E(409, "refresh_in_progress", "Another tab refreshed this session. Try again.")
			}
			if _, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = now() WHERE id = $1`, sid); err != nil {
				return err
			}
			reused = &Admin{UserID: uid, SessionID: sid, IP: ri.IP, UserAgent: ri.UserAgent}
			_ = tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, uid).Scan(&reused.Email)
			return s.audit(ctx, tx, reused, "logout", "admin", uid.String(), map[string]any{"reason": "refresh_reused"})
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if now.After(expires) || now.Sub(created) > SessionMaxAge {
			_, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = now() WHERE id = $1`, sid)
			if err != nil {
				return err
			}
			return errSessionEnded()
		}
		a, err := s.loadAdmin(ctx, tx, uid, ri)
		if err != nil {
			if _, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = now() WHERE id = $1`, sid); err != nil {
				return err
			}
			return errSessionEnded()
		}
		a.SessionID = sid
		next := crypto.RandomToken(32)
		newExp := now.Add(SessionIdle)
		if limit := created.Add(SessionMaxAge); newExp.After(limit) {
			newExp = limit
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_sessions SET prev_refresh_hash = refresh_hash, refresh_hash = $2, rotated_at = now(),
			last_used_at = now(), expires_at = $3, ip = $4, user_agent = $5 WHERE id = $1`,
			sid, crypto.HashToken(next), newExp, ri.IP, ri.UserAgent); err != nil {
			return err
		}
		out, err = s.tokens(a, next, int(time.Until(newExp).Seconds()))
		return err
	})
	if reused != nil && err == nil {
		s.d.Log.Warn("admin refresh token reuse; session revoked", "user_id", reused.UserID, "session_id", reused.SessionID)
		return nil, errSessionEnded()
	}
	return out, err
}

// logout ends the session behind the refresh cookie, and the one behind the access
// token when it is another.
func (s *svc) logout(ctx context.Context) error {
	ri := info(ctx)
	if err := s.checkOrigin(ri); err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var ended []uuid.UUID
		if ri.Cookie != "" && len(ri.Cookie) <= 200 {
			h := crypto.HashToken(ri.Cookie)
			rows, err := tx.Query(ctx, `UPDATE admin_sessions SET revoked_at = now()
				WHERE (refresh_hash = $1 OR prev_refresh_hash = $1) AND revoked_at IS NULL RETURNING user_id`, h)
			if err != nil {
				return err
			}
			if ended, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
				return err
			}
		}
		if a := ri.admin; a != nil {
			tag, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, a.SessionID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				ended = append(ended, a.UserID)
			}
		}
		for _, uid := range ended {
			a := &Admin{UserID: uid, IP: ri.IP, UserAgent: ri.UserAgent}
			_ = tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, uid).Scan(&a.Email)
			if err := s.audit(ctx, tx, a, "logout", "admin", uid.String(), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// stepUp checks the acting admin's current authenticator code (contracts/admin.md
// "Step-up"): missing is 403 mfa_required, wrong 401 invalid_code.
func (s *svc) stepUp(ctx context.Context, tx pgx.Tx, a *Admin, code string) error {
	if code == "" {
		return httpx.E(403, "mfa_required", "Enter a code from your authenticator app.")
	}
	if err := s.limitShared(ctx, "admin:stepup:"+a.UserID.String(), mfaPerUser, loginWindow); err != nil {
		return err
	}
	_, err := s.ops.CheckSecondFactor(ctx, tx, a.UserID, code)
	if he, ok := err.(*httpx.Error); ok && he.Code == "invalid_code" {
		// Written beside the transaction, which the refusal rolls back: it waits on the
		// user row tx holds, so it runs once the request has answered.
		actor := *a
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if aerr := s.audit(ctx, s.d.DB.Pool, &actor, "stepup.failed", "admin", actor.UserID.String(), nil); aerr != nil {
				s.d.Log.Error("admin: audit failed step-up", "err", aerr)
			}
		}()
	}
	return err
}
