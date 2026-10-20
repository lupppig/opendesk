package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const (
	totpPeriod        = 30
	totpSkew          = 1 // one step either side for clock drift
	totpIssuer        = "opendesk"
	recoveryCodeCount = 10
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// totpMatch returns the time step a 6-digit code is valid for at now. Candidates are
// tried oldest first, so a code in the overlap retires the earliest step it fits.
func totpMatch(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / totpPeriod
	for i := -totpSkew; i <= totpSkew; i++ {
		s := step + int64(i)
		want, err := totp.GenerateCodeCustom(secret, time.Unix(s*totpPeriod, 0), totpOpts)
		if err != nil {
			return 0, false
		}
		if constantTimeEq(want, code) {
			return s, true
		}
	}
	return 0, false
}

// consumeTOTP accepts a code only for a step after the last one consumed, and records it.
func consumeTOTP(ctx context.Context, q db.Querier, uid uuid.UUID, secret, code string, now time.Time) (bool, error) {
	step, ok := totpMatch(secret, code, now)
	if !ok {
		return false, nil
	}
	tag, err := q.Exec(ctx, `UPDATE users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2`, uid, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// recoveryKey is the HMAC key recovery codes are hashed with; a database copy alone
// cannot be brute-forced.
func (s *svc) recoveryHash(code string) []byte {
	m := hmac.New(sha256.New, append([]byte("totp-recovery:"), s.d.Cfg.AuthSecret...))
	m.Write([]byte(normalizeRecovery(code)))
	return m.Sum(nil)
}

func normalizeRecovery(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func newRecoveryCode() string {
	r := crypto.RandomString(16) // 80 bits
	return r[0:4] + "-" + r[4:8] + "-" + r[8:12] + "-" + r[12:16]
}

func errInvalidCode() *httpx.Error {
	return httpx.E(401, "invalid_code", "That code is not valid. Try the current code from your app.")
}

// checkSecondFactor verifies a TOTP code (replay-protected) or consumes a recovery code.
func (s *svc) checkSecondFactor(ctx context.Context, tx pgx.Tx, uid uuid.UUID, code string) (string, error) {
	var sealed []byte
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT totp_secret_enc, totp_enabled FROM users WHERE id = $1 FOR UPDATE`, uid).Scan(&sealed, &enabled)
	if err != nil {
		if db.IsNotFound(err) {
			return "", errInvalidCode()
		}
		return "", err
	}
	if !enabled || len(sealed) == 0 {
		return "", errInvalidCode()
	}
	if n := normalizeRecovery(code); len(n) == 16 {
		tag, err := tx.Exec(ctx, `UPDATE totp_recovery_codes SET used_at = now() WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
			uid, s.recoveryHash(n))
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() != 1 {
			return "", errInvalidCode()
		}
		return "recovery_code", nil
	}
	secret, err := s.d.Box.Open(sealed)
	if err != nil {
		return "", err
	}
	ok, err := consumeTOTP(ctx, tx, uid, string(secret), code, time.Now())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errInvalidCode()
	}
	return "totp", nil
}

// requireMFA is auth.RequireMFA that also honors a second factor verified during this
// session after its access token was minted (for example, just after enabling TOTP).
func (s *svc) requireMFA(ctx context.Context, p *auth.Principal) error {
	if p.Kind == auth.KindUser && !p.MFAVerified {
		var verified bool
		err := s.d.DB.Pool.QueryRow(ctx, `SELECT mfa_verified FROM sessions WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			p.SessionID, p.UserID).Scan(&verified)
		if err != nil && !db.IsNotFound(err) {
			return err
		}
		if verified {
			return nil
		}
	}
	return auth.RequireMFA(ctx, s.d.DB.Pool, p)
}

type coreCodeBody struct {
	Code string `json:"code" maxLength:"64"`
}

func (s *svc) registerTOTP(api huma.API) {
	tags := []string{"Auth"}

	register(s, api, huma.Operation{OperationID: "totp-setup", Method: http.MethodPost, Path: "/v1/auth/totp/setup",
		Summary: "Start two-factor setup", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         struct {
				Secret     string `json:"secret"`
				OtpauthURL string `json:"otpauth_url"`
			}
		}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// Setting up a second factor needs a recent sign-in, so a long-held session
			// cannot lock its owner out with a factor of its own.
			if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("totp:setup:"+p.UserID.String(), 10, time.Hour); err != nil {
				return nil, err
			}
			u, err := loadUser(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if u.TOTPEnabled {
				return nil, httpx.Conflict("Two-factor authentication is already on.")
			}
			key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: u.Email, Period: totpPeriod,
				Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1, SecretSize: 20})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			sealed, err := s.d.Box.Seal([]byte(key.Secret()))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE users SET totp_secret_enc = $2, updated_at = now() WHERE id = $1 AND NOT totp_enabled`,
				p.UserID, sealed); err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				CacheControl string `header:"Cache-Control"`
				Body         struct {
					Secret     string `json:"secret"`
					OtpauthURL string `json:"otpauth_url"`
				}
			}{CacheControl: "no-store"}
			out.Body.Secret, out.Body.OtpauthURL = key.Secret(), key.URL()
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "totp-enable", Method: http.MethodPost, Path: "/v1/auth/totp/enable",
		Summary: "Turn on two-factor authentication", Tags: tags},
		func(ctx context.Context, in *struct{ Body coreCodeBody }) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         struct {
				RecoveryCodes []string `json:"recovery_codes"`
			}
		}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limitShared(ctx, "totp:verify:"+p.UserID.String(), mfaPerUser, loginWindow); err != nil {
				return nil, err
			}
			codes := make([]string, recoveryCodeCount)
			for i := range codes {
				codes[i] = newRecoveryCode()
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var sealed []byte
				var enabled bool
				if err := tx.QueryRow(ctx, `SELECT totp_secret_enc, totp_enabled FROM users WHERE id = $1 FOR UPDATE`, p.UserID).Scan(&sealed, &enabled); err != nil {
					return err
				}
				if enabled {
					return httpx.Conflict("Two-factor authentication is already on.")
				}
				if len(sealed) == 0 {
					return httpx.E(409, "totp_not_started", "Start two-factor setup first.")
				}
				secret, err := s.d.Box.Open(sealed)
				if err != nil {
					return err
				}
				ok, err := consumeTOTP(ctx, tx, p.UserID, string(secret), in.Body.Code, time.Now())
				if err != nil {
					return err
				}
				if !ok {
					return errInvalidCode()
				}
				if _, err := tx.Exec(ctx, `UPDATE users SET totp_enabled = true, updated_at = now() WHERE id = $1`, p.UserID); err != nil {
					return err
				}
				if err := s.replaceRecoveryCodes(ctx, tx, p.UserID, codes); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE sessions SET mfa_verified = true WHERE id = $1 AND user_id = $2`, p.SessionID, p.UserID); err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, p, p.UserID, "2fa.enabled", nil); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, p.UserID, emails.TwoFactorEnabled, "")
			})
			if he, ok := err.(*httpx.Error); ok && he.Code == "invalid_code" {
				s.credentialFailed(ctx, p, "totp_enable")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				CacheControl string `header:"Cache-Control"`
				Body         struct {
					RecoveryCodes []string `json:"recovery_codes"`
				}
			}{CacheControl: "no-store"}
			out.Body.RecoveryCodes = codes
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "totp-disable", Method: http.MethodPost, Path: "/v1/auth/totp/disable",
		Summary: "Turn off two-factor authentication", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct{ Body coreCodeBody }) (*struct{}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limitShared(ctx, "totp:verify:"+p.UserID.String(), mfaPerUser, loginWindow); err != nil {
				return nil, err
			}
			if err := s.requireMFA(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if _, err := s.checkSecondFactor(ctx, tx, p.UserID, in.Body.Code); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE users SET totp_enabled = false, totp_secret_enc = NULL, updated_at = now() WHERE id = $1`, p.UserID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM totp_recovery_codes WHERE user_id = $1`, p.UserID); err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, p, p.UserID, "2fa.disabled", nil); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, p.UserID, emails.TwoFactorTurnedOff, "")
			})
			if he, ok := err.(*httpx.Error); ok && he.Code == "invalid_code" {
				s.credentialFailed(ctx, p, "totp_disable")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

func (s *svc) replaceRecoveryCodes(ctx context.Context, tx pgx.Tx, uid uuid.UUID, codes []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM totp_recovery_codes WHERE user_id = $1`, uid); err != nil {
		return err
	}
	for _, c := range codes {
		if _, err := tx.Exec(ctx, `INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
			ids.New(), uid, s.recoveryHash(c)); err != nil {
			return err
		}
	}
	return nil
}
