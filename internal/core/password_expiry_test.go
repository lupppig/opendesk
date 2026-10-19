package core

import (
	"context"
	"testing"
)

// TestPasswordExpiry: a password past users.password_expires_at reads as no password:
// sign-in answers exactly like an unknown account, and a new password clears the expiry.
func TestPasswordExpiry(t *testing.T) {
	e := newEnv(t)
	u, email := e.signup("expiry")
	ctx := context.Background()
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE users SET password_expires_at = now() + interval '1 day' WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	if r := c.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": goodPassword}); r.Status != 200 {
		t.Fatalf("login before expiry: %d %s", r.Status, r.Raw)
	}
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE users SET password_expires_at = now() - interval '1 minute' WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	expired := c.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": goodPassword})
	unknown := c.do("POST", "/v1/auth/login", map[string]any{"email": uniqueEmail(t, "nobody"), "password": goodPassword})
	if expired.Status != 401 || expired.str("code") != "invalid_credentials" || expired.str("detail") != unknown.str("detail") || unknown.Status != 401 {
		t.Fatalf("expired password: %d %s / unknown %d %s", expired.Status, expired.Raw, unknown.Status, unknown.Raw)
	}

	// The signed-in session sets a new password (as a first one, after a recent sign-in);
	// the expiry goes with the old one.
	if r := u.do("POST", "/v1/me/password", map[string]any{"new_password": "a brand new long passphrase 77"}); r.Status != 204 {
		t.Fatalf("set password: %d %s", r.Status, r.Raw)
	}
	var cleared bool
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT password_expires_at IS NULL FROM users WHERE email = $1`, email).Scan(&cleared); err != nil || !cleared {
		t.Fatalf("expiry not cleared: %v %v", cleared, err)
	}
	if r := c.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": "a brand new long passphrase 77"}); r.Status != 200 {
		t.Fatalf("login with the new password: %d %s", r.Status, r.Raw)
	}
}
