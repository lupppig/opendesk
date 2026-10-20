package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const (
	passkeyDefaultName  = "Passkey"
	maxPasskeysPerUser  = 25
	passkeyRegisterRate = 20 // ceremonies per user per hour
)

// newWebAuthn builds the relying party from APP_URL: the RP ID is its host and the
// only accepted origin is its scheme and host. It returns nil when APP_URL has no host.
func newWebAuthn(cfg *config.Config) *webauthn.WebAuthn {
	u, err := url.Parse(cfg.AppURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: auth.PasskeyTTL, TimeoutUVD: auth.PasskeyTTL}
	w, err := webauthn.New(&webauthn.Config{
		RPID:                  u.Hostname(),
		RPDisplayName:         "opendesk",
		RPOrigins:             []string{u.Scheme + "://" + u.Host},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil
	}
	return w
}

// CorePasskey is one of the signed-in user's passkeys.
type CorePasskey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	BackedUp   bool       `json:"backed_up" doc:"The credential is synced to a passkey provider"`
}

const passkeyCols = `id, name, created_at, last_used_at, backed_up`

func scanPasskey(row pgx.Row) (CorePasskey, error) {
	var k CorePasskey
	err := row.Scan(&k.ID, &k.Name, &k.CreatedAt, &k.LastUsedAt, &k.BackedUp)
	return k, err
}

// passkeyUser is a user and their stored credentials, as the WebAuthn engine sees them.
// The user handle is the 16 bytes of the user id: stable and free of personal data.
type passkeyUser struct {
	id          uuid.UUID
	email, name string
	creds       []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte { return u.id[:] }
func (u *passkeyUser) WebAuthnName() string {
	return u.email
}
func (u *passkeyUser) WebAuthnDisplayName() string {
	if u.name != "" {
		return u.name
	}
	return u.email
}
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func loadPasskeyUser(ctx context.Context, q db.Querier, uid uuid.UUID) (*passkeyUser, error) {
	u := &passkeyUser{id: uid}
	if err := q.QueryRow(ctx, `SELECT email, name FROM users WHERE id = $1`, uid).Scan(&u.email, &u.name); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT credential_id, public_key, attestation_type, attestation_format, aaguid, sign_count,
		transports, backup_eligible, backed_up FROM passkeys WHERE user_id = $1 ORDER BY created_at`, uid)
	if err != nil {
		return nil, err
	}
	u.creds, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (webauthn.Credential, error) {
		var c webauthn.Credential
		var count int64
		var transports []string
		err := r.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &c.AttestationFormat, &c.Authenticator.AAGUID, &count,
			&transports, &c.Flags.BackupEligible, &c.Flags.BackupState)
		c.Authenticator.SignCount = uint32(count)
		for _, t := range transports {
			c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
		}
		return c, err
	})
	return u, err
}

func errPasskeyLogin() *httpx.Error {
	return httpx.E(401, "invalid_credentials", "That passkey could not be verified. Try again.")
}

func errPasskeyRegister() *httpx.Error {
	return httpx.E(400, "invalid_passkey", "The passkey could not be verified. Start again.")
}

func errPasskeysOff() *httpx.Error {
	return httpx.E(404, "passkeys_unavailable", "Passkeys are not available on this instance.")
}

// startCeremony stores the ceremony's session data under a fresh jti and signs the
// passkey_token that references it.
func (s *svc) startCeremony(ctx context.Context, purpose string, uid, sid uuid.UUID, data *webauthn.SessionData) (string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	jti := ids.New()
	var userID, sessionID *uuid.UUID
	if uid != uuid.Nil {
		userID, sessionID = &uid, &sid
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `INSERT INTO passkey_challenges (id, purpose, user_id, session_id, session_data, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, jti, purpose, userID, sessionID, raw, time.Now().Add(auth.PasskeyTTL)); err != nil {
		return "", err
	}
	c := auth.Claims{Purpose: purpose}
	c.ID = jti.String()
	subject := ""
	if uid != uuid.Nil {
		subject, c.SessionID = uid.String(), sid.String()
	}
	tok, _, err := s.d.Tokens.Sign(c, subject, auth.PasskeyTTL)
	return tok, err
}

// takeCeremony verifies a passkey_token and consumes the ceremony it references. The
// row is deleted outside any later transaction, so a failed attempt spends it too.
// ok is false for a token of another purpose, owner or session, or a spent or expired
// ceremony.
func (s *svc) takeCeremony(ctx context.Context, token, purpose string, uid, sid uuid.UUID) (*webauthn.SessionData, bool, error) {
	c, err := s.d.Tokens.Verify(token, purpose)
	if err != nil {
		return nil, false, nil
	}
	jti, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, false, nil
	}
	var raw []byte
	if uid == uuid.Nil {
		if c.Subject != "" {
			return nil, false, nil
		}
		err = s.d.DB.Pool.QueryRow(ctx, `DELETE FROM passkey_challenges WHERE id = $1 AND purpose = $2 AND user_id IS NULL
			AND expires_at > now() RETURNING session_data`, jti, purpose).Scan(&raw)
	} else {
		if c.Subject != uid.String() || c.SessionID != sid.String() {
			return nil, false, nil
		}
		err = s.d.DB.Pool.QueryRow(ctx, `DELETE FROM passkey_challenges WHERE id = $1 AND purpose = $2 AND user_id = $3
			AND session_id = $4 AND expires_at > now() RETURNING session_data`, jti, purpose, uid, sid).Scan(&raw)
	}
	if db.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var data webauthn.SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, false, err
	}
	return &data, true, nil
}

type passkeyLoginBegin struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		Options      protocol.PublicKeyCredentialRequestOptions `json:"options" doc:"PublicKeyCredentialRequestOptionsJSON for navigator.credentials.get"`
		PasskeyToken string                                     `json:"passkey_token"`
	}
}

type passkeyRegisterBegin struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		Options      protocol.PublicKeyCredentialCreationOptions `json:"options" doc:"PublicKeyCredentialCreationOptionsJSON for navigator.credentials.create"`
		PasskeyToken string                                      `json:"passkey_token"`
	}
}

func (s *svc) registerPasskeys(api huma.API) {
	authTags := []string{"Auth"}
	tags := []string{"Account"}

	register(s, api, huma.Operation{OperationID: "passkey-login-begin", Method: http.MethodPost, Path: "/v1/auth/passkey/login/begin",
		Summary: "Start signing in with a passkey", Tags: authTags, Security: []map[string][]string{}},
		func(ctx context.Context, _ *struct{}) (*passkeyLoginBegin, error) {
			if s.webauthn == nil {
				return nil, errPasskeysOff()
			}
			ri := requestInfo(ctx)
			if err := s.limit("passkey:begin:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
				return nil, err
			}
			opts, data, err := s.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			tok, err := s.startCeremony(ctx, auth.PurposePasskeyLogin, uuid.Nil, uuid.Nil, data)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &passkeyLoginBegin{CacheControl: "no-store"}
			out.Body.Options, out.Body.PasskeyToken = opts.Response, tok
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "passkey-login-finish", Method: http.MethodPost, Path: "/v1/auth/passkey/login/finish",
		Summary: "Finish signing in with a passkey", Tags: authTags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				PasskeyToken string         `json:"passkey_token" maxLength:"2048"`
				Credential   map[string]any `json:"credential" doc:"AuthenticationResponseJSON from navigator.credentials.get"`
			}
		}) (*authOut, error) {
			return s.handlePasskeyLogin(ctx, in.Body.PasskeyToken, in.Body.Credential)
		})

	register(s, api, huma.Operation{OperationID: "list-passkeys", Method: http.MethodGet, Path: "/v1/me/passkeys",
		Summary: "List your passkeys", Tags: tags},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[CorePasskey] }, error) {
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
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+passkeyCols+` FROM passkeys
				WHERE user_id = $1 AND (created_at, id) < ($2, $3)
				ORDER BY created_at DESC, id DESC LIMIT $4`, p.UserID, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CorePasskey, error) { return scanPasskey(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(list, in.Limit, func(x CorePasskey) any {
				return map[string]any{"t": x.CreatedAt, "id": x.ID}
			})
			return &struct{ Body httpx.Page[CorePasskey] }{page}, nil
		})

	register(s, api, huma.Operation{OperationID: "passkey-register-begin", Method: http.MethodPost, Path: "/v1/me/passkeys/register/begin",
		Summary: "Start adding a passkey", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*passkeyRegisterBegin, error) {
			p, err := s.requirePasskeyManager(ctx)
			if err != nil {
				return nil, err
			}
			if err := s.limit("passkey:register:"+p.UserID.String(), passkeyRegisterRate, time.Hour); err != nil {
				return nil, err
			}
			u, err := loadPasskeyUser(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(u.creds) >= maxPasskeysPerUser {
				return nil, httpx.E(409, "passkey_limit", "You have the most passkeys an account can hold. Remove one first.")
			}
			opts, data, err := s.webauthn.BeginRegistration(u,
				webauthn.WithExclusions(webauthn.Credentials(u.creds).CredentialDescriptors()),
				webauthn.WithConveyancePreference(protocol.PreferNoAttestation))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			tok, err := s.startCeremony(ctx, auth.PurposePasskeyRegister, p.UserID, p.SessionID, data)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &passkeyRegisterBegin{CacheControl: "no-store"}
			out.Body.Options, out.Body.PasskeyToken = opts.Response, tok
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "passkey-register-finish", Method: http.MethodPost, Path: "/v1/me/passkeys/register/finish",
		Summary: "Finish adding a passkey", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				PasskeyToken string         `json:"passkey_token" maxLength:"2048"`
				Credential   map[string]any `json:"credential" doc:"RegistrationResponseJSON from navigator.credentials.create"`
				Name         string         `json:"name,omitempty" maxLength:"200" doc:"Defaults to \"Passkey\""`
			}
		}) (*struct{ Body CorePasskey }, error) {
			p, err := s.requirePasskeyManager(ctx)
			if err != nil {
				return nil, err
			}
			name, err := displayname.Validate("Name", in.Body.Name, displayname.Workspace, true)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if name == "" {
				name = passkeyDefaultName
			}
			data, ok, err := s.takeCeremony(ctx, in.Body.PasskeyToken, auth.PurposePasskeyRegister, p.UserID, p.SessionID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !ok {
				return nil, errPasskeyRegister()
			}
			raw, err := json.Marshal(in.Body.Credential)
			if err != nil {
				return nil, errPasskeyRegister()
			}
			parsed, err := protocol.ParseCredentialCreationResponseBytes(raw)
			if err != nil {
				return nil, errPasskeyRegister()
			}
			var k CorePasskey
			err = s.tx(ctx, func(tx pgx.Tx) error {
				u, err := loadPasskeyUser(ctx, tx, p.UserID)
				if err != nil {
					return err
				}
				if len(u.creds) >= maxPasskeysPerUser {
					return httpx.E(409, "passkey_limit", "You have the most passkeys an account can hold. Remove one first.")
				}
				cred, err := s.webauthn.CreateCredential(u, *data, parsed)
				if err != nil {
					return errPasskeyRegister()
				}
				transports := make([]string, 0, len(cred.Transport))
				for _, t := range cred.Transport {
					transports = append(transports, string(t))
				}
				aaguid := cred.Authenticator.AAGUID
				if aaguid == nil {
					aaguid = []byte{}
				}
				k, err = scanPasskey(tx.QueryRow(ctx, `INSERT INTO passkeys (id, user_id, credential_id, public_key, attestation_type,
					attestation_format, aaguid, sign_count, transports, backup_eligible, backed_up, name)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING `+passkeyCols,
					ids.New(), p.UserID, cred.ID, cred.PublicKey, cred.AttestationType, cred.AttestationFormat, aaguid,
					int64(cred.Authenticator.SignCount), transports, cred.Flags.BackupEligible, cred.Flags.BackupState, name))
				if db.IsUniqueViolation(err, "") {
					return httpx.E(409, "passkey_exists", "This passkey is already registered.")
				}
				if err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, p, p.UserID, "passkey.added", map[string]any{"passkey_id": k.ID, "name": k.Name}); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, p.UserID, emails.PasskeyAdded, passkeyDetail(k.Name))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CorePasskey }{k}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-passkey", Method: http.MethodPatch, Path: "/v1/me/passkeys/{id}",
		Summary: "Rename a passkey", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name string `json:"name" maxLength:"200"`
			}
		}) (*struct{ Body CorePasskey }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Passkey")
			if err != nil {
				return nil, err
			}
			name, err := displayname.Validate("Name", in.Body.Name, displayname.Workspace, false)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			k, err := scanPasskey(s.d.DB.Pool.QueryRow(ctx, `UPDATE passkeys SET name = $3 WHERE id = $1 AND user_id = $2
				RETURNING `+passkeyCols, id, p.UserID, name))
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Passkey")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CorePasskey }{k}, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-passkey", Method: http.MethodDelete, Path: "/v1/me/passkeys/{id}",
		Summary: "Remove a passkey", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			// Removing a way to sign in asks what adding one does: the second factor and a
			// recent sign-in. It works with passkeys turned off, to clean up.
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.requireMFA(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Passkey")
			if err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var name string
				err := tx.QueryRow(ctx, `DELETE FROM passkeys WHERE id = $1 AND user_id = $2 RETURNING name`, id, p.UserID).Scan(&name)
				if db.IsNotFound(err) {
					return httpx.NotFound("Passkey")
				}
				if err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, p, p.UserID, "passkey.removed", map[string]any{"passkey_id": id, "name": name}); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, p.UserID, emails.PasskeyRemoved, passkeyDetail(name))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

// requirePasskeyManager gates adding a passkey: a signed-in user whose session passed
// 2FA when it is on, and started within RecentLoginWindow.
func (s *svc) requirePasskeyManager(ctx context.Context) (*auth.Principal, error) {
	p, err := auth.RequireUser(ctx)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if s.webauthn == nil {
		return nil, errPasskeysOff()
	}
	if err := s.requireMFA(ctx, p); err != nil {
		return nil, httpx.Wrap(err)
	}
	if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
		return nil, httpx.Wrap(err)
	}
	return p, nil
}

// handlePasskeyLogin verifies a discoverable assertion. A user-verified passkey is a
// complete multi-factor sign-in; one without user verification counts as a first
// factor, so an account with TOTP on still answers with an MFA challenge.
func (s *svc) handlePasskeyLogin(ctx context.Context, token string, credential map[string]any) (*authOut, error) {
	if s.webauthn == nil {
		return nil, errPasskeysOff()
	}
	ri := requestInfo(ctx)
	if err := s.limitShared(ctx, "passkey:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	data, ok, err := s.takeCeremony(ctx, token, auth.PurposePasskeyLogin, uuid.Nil, uuid.Nil)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if !ok {
		s.loginFailed(ctx, uuid.Nil, ri, "passkey")
		return nil, errPasskeyLogin()
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, errPasskeyLogin()
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		s.loginFailed(ctx, uuid.Nil, ri, "passkey")
		return nil, errPasskeyLogin()
	}
	var keyID, uid uuid.UUID
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT id, user_id FROM passkeys WHERE credential_id = $1`, parsed.RawID).Scan(&keyID, &uid)
	if db.IsNotFound(err) {
		s.loginFailed(ctx, uuid.Nil, ri, "passkey")
		return nil, errPasskeyLogin()
	}
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if err := s.limitShared(ctx, "passkey:user:"+uid.String(), loginPerAccount, loginWindow); err != nil {
		return nil, err
	}
	var totp bool
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		if !bytes.Equal(userHandle, uid[:]) {
			return nil, errors.New("user handle does not own the credential")
		}
		u, err := loadPasskeyUser(ctx, s.d.DB.Pool, uid)
		if err != nil {
			return nil, err
		}
		if err := s.d.DB.Pool.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id = $1`, uid).Scan(&totp); err != nil {
			return nil, err
		}
		return u, nil
	}
	_, cred, err := s.webauthn.ValidatePasskeyLogin(handler, *data, parsed)
	if err != nil || cred.Authenticator.CloneWarning {
		if err == nil {
			s.d.Log.Warn("passkey sign counter did not advance; sign-in refused", "user_id", uid, "passkey_id", keyID)
		}
		s.loginFailed(ctx, uid, ri, "passkey")
		return nil, errPasskeyLogin()
	}
	verified := parsed.Response.AuthenticatorData.Flags.HasUserVerified()
	count := int64(cred.Authenticator.SignCount)
	advance := func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE passkeys SET sign_count = $2, backed_up = $3, last_used_at = now()
			WHERE id = $1 AND (sign_count < $2 OR (sign_count = 0 AND $2 = 0))`, keyID, count, cred.Flags.BackupState)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errPasskeyLogin()
		}
		return nil
	}
	if !verified {
		if err := s.tx(ctx, advance); err != nil {
			if isCode(err, "invalid_credentials") {
				s.loginFailed(ctx, uid, ri, "passkey")
			}
			return nil, httpx.Wrap(err)
		}
		return s.completeFirstFactor(ctx, uid, totp, "passkey", "", false, ri)
	}
	var out *authOut
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := advance(tx); err != nil {
			return err
		}
		var err error
		if out, err = s.issueSession(ctx, tx, uid, true, ri); err != nil {
			return err
		}
		_, err = auditUser(ctx, tx, userPrincipal(uid, ri), uid, "login",
			map[string]any{"method": "passkey", "mfa": true, "factor": "passkey", "passkey_id": keyID})
		return err
	})
	if err != nil {
		if isCode(err, "invalid_credentials") {
			s.loginFailed(ctx, uid, ri, "passkey")
		}
		return nil, httpx.Wrap(err)
	}
	s.d.Analytics.Capture(uid.String(), "", "user_logged_in", map[string]any{"method": "passkey", "mfa": true})
	return out, nil
}

func isCode(err error, code string) bool {
	var he *httpx.Error
	return errors.As(err, &he) && he.Code == code
}
