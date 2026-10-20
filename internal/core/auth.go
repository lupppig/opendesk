package core

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/appattest"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Rate limits on the anonymous auth routes.
const (
	loginPerIP      = 30
	loginPerAccount = 10
	loginWindow     = 15 * time.Minute
	mfaPerUser      = 10
	// mfaPerUserDay caps second-factor attempts on one account per day.
	mfaPerUserDay  = 30
	registerPerIP  = 10
	forgotPerIP    = 10
	forgotPerEmail = 3
	resetPerIP     = 20
)

func errInvalidCredentials() *httpx.Error {
	return httpx.E(401, "invalid_credentials", "The email or password is incorrect.")
}

// errEmailTaken answers a sign-up with an address that has an account.
var errEmailTaken = httpx.E(409, "email_taken", "An account with this email already exists. Sign in instead.")

func errInvalidToken() *httpx.Error {
	return httpx.E(400, "invalid_token", "This link is invalid or has expired.")
}

type coreAuthProvider struct {
	ID   string `json:"id" enum:"google,microsoft,github,oidc"`
	Name string `json:"name"`
}

type coreAuthConfig struct {
	Providers        []coreAuthProvider `json:"providers"`
	PasswordLogin    bool               `json:"password_login"`
	SignupEnabled    bool               `json:"signup_enabled"`
	Passkeys         bool               `json:"passkeys" doc:"Passkey sign-in is available"`
	TurnstileSiteKey *string            `json:"turnstile_site_key"`
	Native           coreNativeConfig   `json:"native" doc:"What the native apps need to sign in and receive pushes"`
	WebPushKey       *string            `json:"web_push_key" doc:"The VAPID public key browsers subscribe to web pushes with; null when this server sends none"`
}

type coreNativeConfig struct {
	AppAttest bool    `json:"app_attest" doc:"App Attest proofs are accepted in place of Turnstile"`
	Push      string  `json:"push" enum:"direct,relay,off" doc:"How this instance reaches iPhones"`
	RelayURL  *string `json:"relay_url" doc:"The push relay origin when push is relay"`
}

type coreLoginBody struct {
	Email          string          `json:"email" maxLength:"320"`
	Password       string          `json:"password" maxLength:"1024"`
	TurnstileToken string          `json:"turnstile_token,omitempty" maxLength:"4096"`
	AppAttest      *appAttestProof `json:"app_attest,omitempty" doc:"Native requests: an App Attest proof in place of turnstile_token"`
}

type coreRegisterBody struct {
	Email          string          `json:"email" maxLength:"320"`
	Password       string          `json:"password" maxLength:"1024"`
	Name           string          `json:"name" maxLength:"200"`
	TurnstileToken string          `json:"turnstile_token,omitempty" maxLength:"4096"`
	AppAttest      *appAttestProof `json:"app_attest,omitempty" doc:"Native requests: an App Attest proof in place of turnstile_token"`
	InviteToken    string          `json:"invite_token,omitempty" maxLength:"200" doc:"Invitation token; lets an invited person register when public signup is off"`
}

// refreshBody names the account to refresh (browsers, which send its cookie) or carries a
// native client's refresh token.
type refreshBody struct {
	RefreshToken string `json:"refresh_token,omitempty" maxLength:"200" doc:"Native requests, which have no refresh cookie"`
	UserID       string `json:"user_id,omitempty" maxLength:"36" doc:"The account to refresh when the browser is signed in to several; defaults to the one used most recently"`
}

func (s *svc) registerAuth(api huma.API) {
	tags := []string{"Auth"}

	register(s, api, huma.Operation{OperationID: "auth-config", Method: http.MethodGet, Path: "/v1/auth/config",
		Summary: "Sign-in options", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body coreAuthConfig }, error) {
			c := coreAuthConfig{Providers: []coreAuthProvider{}, PasswordLogin: true, SignupEnabled: s.d.Cfg.SignupEnabled,
				Passkeys: s.webauthn != nil}
			for _, p := range s.oauth.list {
				c.Providers = append(c.Providers, coreAuthProvider{ID: p.id, Name: p.name})
			}
			if k := s.d.Cfg.TurnstileSiteKey; k != "" {
				c.TurnstileSiteKey = &k
			}
			c.Native = coreNativeConfig{AppAttest: len(s.d.Cfg.AppAttestAppIDs) > 0, Push: s.d.Cfg.PushMode()}
			if s.push.web != nil {
				k := s.push.web.Keys.Public
				c.WebPushKey = &k
			}
			if c.Native.Push == "relay" {
				u := s.d.Cfg.PushRelayURL
				c.Native.RelayURL = &u
			}
			return &struct{ Body coreAuthConfig }{c}, nil
		})

	register(s, api, huma.Operation{OperationID: "auth-register", Method: http.MethodPost, Path: "/v1/auth/register",
		Summary: "Create an account", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct{ Body coreRegisterBody }) (*authOut, error) {
			return s.handleRegister(ctx, &in.Body)
		})

	register(s, api, huma.Operation{OperationID: "auth-login", Method: http.MethodPost, Path: "/v1/auth/login",
		Summary: "Sign in with email and password", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct{ Body coreLoginBody }) (*authOut, error) {
			return s.handleLogin(ctx, &in.Body)
		})

	register(s, api, huma.Operation{OperationID: "auth-mfa", Method: http.MethodPost, Path: "/v1/auth/mfa",
		Summary: "Complete sign-in with a second factor", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				MFAToken string `json:"mfa_token" maxLength:"2048"`
				Code     string `json:"code" maxLength:"64" doc:"6-digit code or a recovery code"`
			}
		}) (*authOut, error) {
			return s.handleMFA(ctx, in.Body.MFAToken, in.Body.Code)
		})

	register(s, api, huma.Operation{OperationID: "auth-refresh", Method: http.MethodPost, Path: "/v1/auth/refresh",
		Summary: "Rotate the refresh token for a new access token", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct{ Body *refreshBody }) (*authOut, error) {
			return s.handleRefresh(ctx, in.Body)
		})

	register(s, api, huma.Operation{OperationID: "auth-logout", Method: http.MethodPost, Path: "/v1/auth/logout",
		Summary: "Sign out of this account, or of every account in this browser", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct{ Body *logoutBody }) (*struct {
			SetCookie []string `header:"Set-Cookie"`
		}, error) {
			ri := requestInfo(ctx)
			body := in.Body
			if body == nil {
				body = &logoutBody{}
			}
			var p sessionRef
			if pr := auth.FromContext(ctx); pr != nil && pr.Kind == auth.KindUser {
				p = sessionRef{pr.UserID, pr.SessionID}
			}
			var clear []string
			var ended []sessionRef
			err := s.tx(ctx, func(tx pgx.Tx) error {
				targets, c, err := s.logoutTargets(ctx, tx, p, body, ri)
				if err != nil {
					return err
				}
				clear = c
				for _, t := range targets {
					ok, err := endSession(ctx, tx, t.uid, t.sid)
					if err != nil {
						return err
					}
					if !ok {
						continue
					}
					ended = append(ended, t)
					if _, err := auditUser(ctx, tx, userPrincipal(t.uid, ri), t.uid, "logout", map[string]any{"session_id": t.sid}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			for _, t := range ended {
				s.disconnect(t.uid, t.sid)
			}
			if p.uid != uuid.Nil && len(ended) == 0 {
				// The session had already ended: still close what is left of it.
				s.disconnect(p.uid, p.sid)
			}
			out := &struct {
				SetCookie []string `header:"Set-Cookie"`
			}{}
			if !ri.Native {
				out.SetCookie = clear
			}
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "auth-password-forgot", Method: http.MethodPost, Path: "/v1/auth/password/forgot",
		Summary: "Email a password reset link", Tags: tags, DefaultStatus: http.StatusNoContent, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Email          string          `json:"email" maxLength:"320"`
				TurnstileToken string          `json:"turnstile_token,omitempty" maxLength:"4096"`
				AppAttest      *appAttestProof `json:"app_attest,omitempty" doc:"Native requests: an App Attest proof in place of turnstile_token"`
			}
		}) (*struct{}, error) {
			ri := requestInfo(ctx)
			if err := s.limitShared(ctx, "forgot:ip:"+ri.IP, forgotPerIP, time.Hour); err != nil {
				return nil, err
			}
			if err := s.humanCheck(ctx, ri, appattest.ActionPasswordForgot, in.Body.TurnstileToken, in.Body.AppAttest); err != nil {
				return nil, httpx.Wrap(err)
			}
			email := normEmail(in.Body.Email)
			if email == "" || !s.d.Limiter.Shared(ctx, "forgot:email:"+email, forgotPerEmail, time.Hour) {
				return nil, nil
			}
			if _, err := s.d.Jobs.Insert(ctx, forgotArgs{Email: email}, &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 5}); err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "auth-password-reset", Method: http.MethodPost, Path: "/v1/auth/password/reset",
		Summary: "Set a new password from a reset link", Tags: tags, DefaultStatus: http.StatusNoContent, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Token     string          `json:"token" maxLength:"200"`
				Password  string          `json:"password" maxLength:"1024"`
				AppAttest *appAttestProof `json:"app_attest,omitempty" doc:"Native requests: an App Attest proof; verified when present"`
			}
		}) (*struct{}, error) {
			ri := requestInfo(ctx)
			if err := s.limitShared(ctx, "reset:ip:"+ri.IP, resetPerIP, time.Hour); err != nil {
				return nil, err
			}
			// The emailed token is this route's gate; a native request's proof, when sent,
			// must still hold.
			if in.Body.AppAttest != nil && ri.Native && s.attestEnabled() {
				if err := s.verifyAppAttest(ctx, appattest.ActionPasswordReset, in.Body.AppAttest); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			if err := auth.CheckPassword(in.Body.Password); err != nil {
				return nil, httpx.Wrap(err)
			}
			hash, err := auth.HashPassword(in.Body.Password)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var uid uuid.UUID
			err = s.tx(ctx, func(tx pgx.Tx) error {
				o, err := consumeOneTime(ctx, tx, ottReset, in.Body.Token)
				if db.IsNotFound(err) {
					return errInvalidToken()
				}
				if err != nil {
					return err
				}
				if o.UserID == nil {
					return errInvalidToken()
				}
				uid = *o.UserID
				tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, password_expires_at = NULL, updated_at = now(),
					email_verified_at = CASE WHEN email = $3 THEN COALESCE(email_verified_at, now()) ELSE email_verified_at END
					WHERE id = $1`, uid, hash, o.Email)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return errInvalidToken()
				}
				if _, err := tx.Exec(ctx, `UPDATE one_time_tokens SET used_at = now() WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, uid, ottReset); err != nil {
					return err
				}
				if err := revokeOtherSessions(ctx, tx, uid, uuid.Nil); err != nil {
					return err
				}
				if _, err := oauthgrant.Revoke(ctx, tx, userPrincipal(uid, ri), uid, uuid.Nil, uuid.Nil, oauthgrant.ReasonSignedOut); err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, userPrincipal(uid, ri), uid, "password.reset", nil); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, uid, emails.PasswordReset, "")
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.disconnect(uid, uuid.Nil)
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "auth-email-verify", Method: http.MethodPost, Path: "/v1/auth/email/verify",
		Summary: "Confirm an email address", Tags: tags, DefaultStatus: http.StatusNoContent, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token" maxLength:"200"`
			}
		}) (*struct{}, error) {
			ri := requestInfo(ctx)
			if err := s.limit("verify:ip:"+ri.IP, 30, time.Hour); err != nil {
				return nil, err
			}
			err := s.tx(ctx, func(tx pgx.Tx) error {
				o, err := consumeOneTime(ctx, tx, ottVerifyEmail, in.Body.Token)
				if db.IsNotFound(err) || (err == nil && o.UserID == nil) {
					return errInvalidToken()
				}
				if err != nil {
					return err
				}
				tag, err := tx.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now()
					WHERE id = $1 AND email = $2`, *o.UserID, o.Email)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return errInvalidToken()
				}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "auth-email-resend", Method: http.MethodPost, Path: "/v1/auth/email/resend",
		Summary: "Send the confirmation email again", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("resend:"+p.UserID.String(), 3, time.Hour); err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				u, err := loadUser(ctx, tx, p.UserID)
				if err != nil || u.EmailVerified {
					return err
				}
				return s.sendVerifyEmail(ctx, tx, u.ID, u.Email)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

func (s *svc) handleRegister(ctx context.Context, in *coreRegisterBody) (*authOut, error) {
	ri := requestInfo(ctx)
	if err := s.limitShared(ctx, "register:ip:"+ri.IP, registerPerIP, time.Hour); err != nil {
		return nil, err
	}
	if err := s.humanCheck(ctx, ri, appattest.ActionRegister, in.TurnstileToken, in.AppAttest); err != nil {
		return nil, httpx.Wrap(err)
	}
	email := normEmail(in.Email)
	if email == "" {
		return nil, httpx.E(422, "invalid_email", "Enter a valid email address.")
	}
	name, err := displayname.Validate("Name", in.Name, displayname.Person, false)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if err := auth.CheckPassword(in.Password); err != nil {
		return nil, httpx.Wrap(err)
	}
	// An invitation opens sign-up when it is closed. Its link proves the address only when
	// it went by the invitation email: a link an admin copied and sent another way does
	// not, so that account confirms its address like any other.
	invited, proven := false, false
	if in.InviteToken != "" {
		var invEmail string
		var copied bool
		err := s.d.DB.Pool.QueryRow(ctx, `SELECT email, link_copied_at IS NOT NULL FROM invitations
			WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now()`,
			crypto.HashToken(in.InviteToken)).Scan(&invEmail, &copied)
		if err != nil && !db.IsNotFound(err) {
			return nil, httpx.Wrap(err)
		}
		invited = err == nil && invEmail == email
		proven = invited && !copied
	}
	if !s.d.Cfg.SignupEnabled && !invited {
		return nil, httpx.E(403, "signup_disabled", "Sign up is by invitation only on this instance.")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	var out *authOut
	uid := ids.New()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var verified *time.Time
		if proven {
			verified = ptr(time.Now())
		}
		_, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, password_hash, email_verified_at) VALUES ($1, $2, $3, $4, $5)`,
			uid, email, name, hash, verified)
		if db.IsUniqueViolation(err, "") {
			return errEmailTaken
		}
		if err != nil {
			return err
		}
		if !proven {
			if err := s.sendVerifyEmail(ctx, tx, uid, email); err != nil {
				return err
			}
		}
		out, err = s.issueSession(ctx, tx, uid, false, ri)
		return err
	})
	if errors.Is(err, errEmailTaken) {
		// The address's owner hears about the attempt (at most a few times a day), so a
		// probe for who has an account here does not go unnoticed.
		if s.d.Limiter.Shared(ctx, "register:taken:"+email, 3, 24*time.Hour) {
			m := emails.SignupAttempt(s.site())
			if _, err := s.d.Jobs.Insert(ctx, jobs.SystemEmailArgs{To: email, Subject: m.Subject, HTML: m.HTML, Text: m.Text, Headers: m.Headers},
				&river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 8}); err != nil {
				s.d.Log.Error("queue sign-up attempt email", "err", err)
			}
		}
		return nil, errEmailTaken
	}
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	s.d.Analytics.Capture(uid.String(), "", "user_signed_up", map[string]any{"method": "password", "invited": invited})
	return out, nil
}

func (s *svc) handleLogin(ctx context.Context, in *coreLoginBody) (*authOut, error) {
	ri := requestInfo(ctx)
	if err := s.limitShared(ctx, "login:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	// The per-account limit counts only requests that passed the human check, so
	// nobody can lock an account out without solving one per attempt.
	if err := s.humanCheck(ctx, ri, appattest.ActionLogin, in.TurnstileToken, in.AppAttest); err != nil {
		return nil, httpx.Wrap(err)
	}
	email := normEmail(in.Email)
	if email != "" {
		if err := s.limitShared(ctx, "login:email:"+email, loginPerAccount, loginWindow); err != nil {
			return nil, err
		}
	}
	var uid uuid.UUID
	var hash *string
	var totp, generated bool
	found := false
	if email != "" {
		err := s.d.DB.Pool.QueryRow(ctx, `SELECT id, `+auth.PasswordHashSQL+`, totp_enabled, password_expires_at IS NOT NULL FROM users WHERE email = $1`,
			email).Scan(&uid, &hash, &totp, &generated)
		if err != nil && !db.IsNotFound(err) {
			return nil, httpx.Wrap(err)
		}
		found = err == nil
	}
	if !found || hash == nil {
		auth.DummyVerify(in.Password)
		s.loginFailed(ctx, uid, ri, "password")
		return nil, errInvalidCredentials()
	}
	if !auth.VerifyPassword(in.Password, *hash) {
		s.loginFailed(ctx, uid, ri, "password")
		return nil, errInvalidCredentials()
	}
	// A password that no longer meets the rules (breached, or shorter than they now ask)
	// still signs in, and the answer asks for a new one (ASVS 2.1.7); so does a password
	// an admin generated for a test account, which is never meant to last (2.3.1).
	weak := auth.CheckPassword(in.Password) != nil || generated
	return s.completeFirstFactor(ctx, uid, totp, "password", "", weak, ri)
}

// completeFirstFactor answers a verified first factor with tokens, or with an MFA
// challenge when the user has TOTP on. weakPassword asks for a new password once signed in.
func (s *svc) completeFirstFactor(ctx context.Context, uid uuid.UUID, totp bool, method, redirectTo string, weakPassword bool, ri reqInfo) (*authOut, error) {
	if totp {
		data := map[string]string{"method": method, "rt": redirectTo}
		if weakPassword {
			data["cp"] = "1"
		}
		tok, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeMFA, Data: data},
			uid.String(), auth.MFATTL)
		if err != nil {
			return nil, httpx.Wrap(err)
		}
		return &authOut{CacheControl: "no-store", Body: CoreAuthResult{MFARequired: true, MFAToken: tok, RedirectTo: redirectTo}}, nil
	}
	var out *authOut
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if out, err = s.issueSession(ctx, tx, uid, false, ri); err != nil {
			return err
		}
		_, err = auditUser(ctx, tx, userPrincipal(uid, ri), uid, "login", map[string]any{"method": method, "mfa": false})
		return err
	})
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	out.Body.RedirectTo, out.Body.ChangePassword = redirectTo, weakPassword
	s.d.Analytics.Capture(uid.String(), "", "user_logged_in", map[string]any{"method": method, "mfa": false})
	return out, nil
}

// credentialFailed records a wrong password or code given to confirm an action of a
// signed-in person (check: current_password, totp_enable, totp_disable).
func (s *svc) credentialFailed(ctx context.Context, p *auth.Principal, check string) {
	err := s.tx(context.WithoutCancel(ctx), func(tx pgx.Tx) error {
		_, err := auditUser(ctx, tx, p, p.UserID, "credential.failed", map[string]any{"check": check})
		return err
	})
	if err != nil {
		s.d.Log.Error("audit failed credential check", "err", err)
	}
}

// loginFailed records a failed sign-in: an audit row in the user's workspaces when
// the account is known and has any, otherwise a log line without the address.
func (s *svc) loginFailed(ctx context.Context, uid uuid.UUID, ri reqInfo, method string) {
	wrote := false
	if uid != uuid.Nil {
		err := s.tx(context.WithoutCancel(ctx), func(tx pgx.Tx) error {
			var err error
			wrote, err = auditUser(ctx, tx, userPrincipal(uid, ri), uid, "login.failed", map[string]any{"method": method})
			return err
		})
		if err != nil {
			s.d.Log.Error("audit login failure", "err", err)
		}
	}
	if !wrote {
		attrs := []any{"method", method, "ip", ri.IP}
		if uid != uuid.Nil {
			attrs = append(attrs, "user_id", uid)
		}
		s.d.Log.Info("login failed", attrs...)
	}
}

func (s *svc) handleMFA(ctx context.Context, mfaToken, code string) (*authOut, error) {
	ri := requestInfo(ctx)
	if err := s.limitShared(ctx, "mfa:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	c, err := s.d.Tokens.Verify(mfaToken, auth.PurposeMFA)
	if err != nil {
		return nil, httpx.E(401, "invalid_mfa_token", "Your sign-in attempt expired. Start again.")
	}
	uid, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, httpx.E(401, "invalid_mfa_token", "Your sign-in attempt expired. Start again.")
	}
	if err := s.limitShared(ctx, "mfa:user:"+uid.String(), mfaPerUser, loginWindow); err != nil {
		return nil, err
	}
	if err := s.limitShared(ctx, "mfa:user:day:"+uid.String(), mfaPerUserDay, 24*time.Hour); err != nil {
		return nil, err
	}
	method := c.Data["method"]
	var out *authOut
	var factor string
	err = s.tx(ctx, func(tx pgx.Tx) error {
		f, err := s.checkSecondFactor(ctx, tx, uid, code)
		if err != nil {
			return err
		}
		factor = f
		if out, err = s.issueSession(ctx, tx, uid, true, ri); err != nil {
			return err
		}
		_, err = auditUser(ctx, tx, userPrincipal(uid, ri), uid, "login", map[string]any{"method": method, "mfa": true, "factor": factor})
		return err
	})
	if err != nil {
		if he, ok := err.(*httpx.Error); ok && he.Code == "invalid_code" {
			s.loginFailed(ctx, uid, ri, "mfa")
		}
		return nil, httpx.Wrap(err)
	}
	out.Body.RedirectTo, out.Body.ChangePassword = c.Data["rt"], c.Data["cp"] == "1"
	s.d.Analytics.Capture(uid.String(), "", "user_logged_in", map[string]any{"method": method, "mfa": true})
	return out, nil
}

// handleRefresh rotates one account's refresh token. A browser names the account with
// user_id and only that account's cookie changes; without user_id it refreshes the
// account it used most recently. Native requests send the token in the body.
func (s *svc) handleRefresh(ctx context.Context, in *refreshBody) (*authOut, error) {
	ri := requestInfo(ctx)
	if err := s.limit("refresh:ip:"+ri.IP, 120, time.Minute); err != nil {
		return nil, err
	}
	if in == nil {
		in = &refreshBody{}
	}
	var want uuid.UUID
	if in.UserID != "" {
		id, err := uuid.Parse(in.UserID)
		if err != nil {
			return nil, httpx.Wrap(errRefreshInvalid())
		}
		want = id
	}
	pick, err := s.pickRefreshCookie(ctx, want, ri)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	token := in.RefreshToken
	if pick != nil {
		token = pick.Value
	}
	expect := want
	if pick != nil && !pick.legacy() {
		expect = pick.UserID
	}
	out, keep, err := s.rotateRefresh(ctx, token, expect, ri)
	if err != nil {
		if he, ok := err.(*httpx.Error); ok && he.Status == 401 && pick != nil && !keep {
			return nil, huma.ErrorWithHeaders(he, s.clearCookieHeader(pick.Name))
		}
		return nil, httpx.Wrap(err)
	}
	if pick != nil && pick.legacy() {
		// The legacy cookie has moved to the account's own cookie.
		out.SetCookie = append(out.SetCookie, s.clearCookie(LegacyRefreshCookie))
	}
	return out, nil
}

// pickRefreshCookie chooses the cookie a refresh uses: the named account's (or the legacy
// cookie, which may belong to it), or without a name the account used most recently.
func (s *svc) pickRefreshCookie(ctx context.Context, want uuid.UUID, ri reqInfo) (*presentedCookie, error) {
	if len(ri.Cookies) == 0 {
		return nil, nil
	}
	if want != uuid.Nil {
		if c := ri.cookieFor(want); c != nil {
			return c, nil
		}
		return ri.legacyCookie(), nil
	}
	if len(ri.Cookies) == 1 {
		return &ri.Cookies[0], nil
	}
	held, err := s.heldSessions(ctx, s.d.DB.Pool, ri)
	if err != nil {
		return nil, err
	}
	var best *heldSession
	for i := range held {
		h := &held[i]
		if h.Valid && !h.Stale && (best == nil || h.LastUsed.After(best.LastUsed)) {
			best = h
		}
	}
	if best == nil {
		return &ri.Cookies[0], nil
	}
	c := best.presentedCookie
	return &c, nil
}
