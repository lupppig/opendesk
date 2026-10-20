package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// An access token works only while its session does: logging out ends it at once.
func TestAccessTokenEndsWithSession(t *testing.T) {
	e := newEnv(t)
	u, _ := e.signup("sessionend")
	token := u.token
	if r := u.do("GET", "/v1/me", nil); r.Status != 200 {
		t.Fatalf("me: %d %s", r.Status, r.Raw)
	}
	if r := u.do("POST", "/v1/auth/logout", nil); r.Status != 204 {
		t.Fatalf("logout: %d %s", r.Status, r.Raw)
	}
	u.token = token
	if r := u.do("GET", "/v1/me", nil); r.Status != 401 {
		t.Fatalf("access token outlived its session: %d", r.Status)
	}
	if r := u.do("POST", "/v1/realtime/token", nil); r.Status == 200 {
		t.Fatal("a revoked session minted a socket token")
	}
}

// A suspended user's access token stops at once.
func TestAccessTokenEndsWithSuspension(t *testing.T) {
	e := newEnv(t)
	u, email := e.signup("suspendnow")
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE users SET suspended_at = now() WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	if r := u.do("GET", "/v1/me", nil); r.Status != 401 {
		t.Fatalf("suspended user still in: %d", r.Status)
	}
}

// Refresh cookies count only from the app's own pages.
func TestRefreshCookieRefusedFromForeignOrigin(t *testing.T) {
	e := newEnv(t)
	e.d.Cfg.CORSOrigins = []string{"http://app.test"}
	u, _ := e.signup("foreignorigin")
	if r := u.do("POST", "/v1/auth/refresh", map[string]any{}, "Origin", "https://evil.example"); r.Status != 401 {
		t.Fatalf("refresh from another origin: %d %s", r.Status, r.Raw)
	}
	if r := u.do("POST", "/v1/auth/refresh", map[string]any{}, "Origin", "http://app.test"); r.Status != 200 {
		t.Fatalf("refresh from the app: %d %s", r.Status, r.Raw)
	}
	if r := u.do("POST", "/v1/auth/refresh", map[string]any{}); r.Status != 200 {
		t.Fatalf("refresh without Origin: %d %s", r.Status, r.Raw)
	}
}

// On https the refresh cookie is __Host- prefixed: host-only and Path=/.
func TestRefreshCookiePrefix(t *testing.T) {
	uid := ids.New()
	for _, c := range []struct {
		secure       bool
		domain       string
		name, prefix string
	}{
		{true, "", "__Host-" + RefreshCookieName(uid), "Path=/;"},
		{true, "example.com", "__Secure-" + RefreshCookieName(uid), "Path=/v1/auth;"},
		{false, "", RefreshCookieName(uid), "Path=/v1/auth;"},
	} {
		s := &svc{d: &deps.Deps{Cfg: &config.Config{CookieSecure: c.secure, CookieDomain: c.domain}}}
		name := s.refreshCookieName(uid)
		if name != c.name {
			t.Fatalf("name %q want %q", name, c.name)
		}
		set := s.refreshCookie(name, "v", 60) + ";"
		if !strings.Contains(set, c.prefix) || !strings.Contains(set, "HttpOnly") {
			t.Fatalf("cookie %q", set)
		}
		if strings.HasPrefix(name, "__Host-") && strings.Contains(set, "Domain=") {
			t.Fatalf("__Host- cookie with a domain: %q", set)
		}
	}
	// A prefixed cookie is read as the account's.
	r, _ := http.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "__Host-" + RefreshCookieName(uid), Value: "x"})
	got := readRefreshCookies(r)
	if len(got) != 1 || got[0].UserID != uid {
		t.Fatalf("read %+v", got)
	}
}

// Per-account limits count across every process: two limiters on one database share them.
func TestSharedLimiter(t *testing.T) {
	e := newEnv(t)
	a, b := httpx.NewLimiter(), httpx.NewLimiter()
	a.UseDB(e.d.DB.Pool, e.d.Log)
	b.UseDB(e.d.DB.Pool, e.d.Log)
	key := "test:" + crypto.RandomString(8)
	ctx := context.Background()
	for i := range 4 {
		l := a
		if i%2 == 1 {
			l = b
		}
		if !l.Shared(ctx, key, 4, time.Minute) {
			t.Fatalf("hit %d refused", i)
		}
	}
	if a.Shared(ctx, key, 4, time.Minute) || b.Shared(ctx, key, 4, time.Minute) {
		t.Fatal("limit not shared")
	}
}

// Changes to how someone signs in are emailed to them.
func TestSecurityNotices(t *testing.T) {
	e := newEnv(t)
	u, email := e.signup("notices")
	newPassword := goodPassword + "-changed"
	if r := u.do("POST", "/v1/me/password", map[string]any{"current_password": goodPassword, "new_password": newPassword}); r.Status/100 != 2 {
		t.Fatalf("change: %d %s", r.Status, r.Raw)
	}
	if subject, _ := e.lastMail(email); subject != "Your password was changed" {
		t.Fatalf("after password change: %q", subject)
	}
	r := u.do("POST", "/v1/auth/totp/setup", nil)
	code, _ := totp.GenerateCode(r.str("secret"), time.Now())
	if r := u.do("POST", "/v1/auth/totp/enable", map[string]any{"code": code}); r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Raw)
	}
	if subject, text := e.lastMail(email); subject != "Two-factor authentication was turned on" || !strings.Contains(text, "/settings/security") {
		t.Fatalf("after 2FA on: %q", subject)
	}
}

// A password that no longer meets the rules signs in and asks for a new one.
func TestWeakPasswordAsksForChange(t *testing.T) {
	e := newEnv(t)
	c, email := e.signup("weakpw")
	if r := c.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": goodPassword}); r.Status != 200 || r.Body["change_password"] != nil {
		t.Fatalf("good password: %d %s", r.Status, r.Raw)
	}
	hash, err := auth.HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE users SET password_hash = $2 WHERE email = $1`, email, hash); err != nil {
		t.Fatal(err)
	}
	if r := e.client().do("POST", "/v1/auth/login", map[string]any{"email": email, "password": "password"}); r.Status != 200 || r.Body["change_password"] != true {
		t.Fatalf("weak password: %d %s", r.Status, r.Raw)
	}
	// A password an admin generated for a test account is never meant to last.
	_, other := e.signup("generatedpw")
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE users SET password_expires_at = now() + interval '7 days' WHERE email = $1`, other); err != nil {
		t.Fatal(err)
	}
	if r := e.client().do("POST", "/v1/auth/login", map[string]any{"email": other, "password": goodPassword}); r.Status != 200 || r.Body["change_password"] != true {
		t.Fatalf("generated password: %d %s", r.Status, r.Raw)
	}
}

// Signing up with a taken address tells the address's owner.
func TestSignupAttemptNotice(t *testing.T) {
	e := newEnv(t)
	_, email := e.signup("taken")
	r := e.client().do("POST", "/v1/auth/register", map[string]any{"email": email, "password": goodPassword, "name": "Someone"})
	if r.Status != 409 || r.str("code") != "email_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Raw)
	}
	if subject, _ := e.lastMail(email); subject != "Someone tried to sign up with your email" {
		t.Fatalf("no notice: %q", subject)
	}
}

// Making an API key needs a recent sign-in.
func TestAPIKeyNeedsRecentSignIn(t *testing.T) {
	e := newEnv(t)
	c, email := e.signup("keystepup")
	c.workspace = c.newWorkspace("Keys Co")
	if r := c.do("POST", "/v1/api-keys", map[string]any{"name": "Fresh", "scopes": []string{"tickets:read"}}); r.Status != 201 {
		t.Fatalf("recent sign-in: %d %s", r.Status, r.Raw)
	}
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE sessions SET created_at = now() - interval '13 hours'
		WHERE user_id = (SELECT id FROM users WHERE email = $1)`, email); err != nil {
		t.Fatal(err)
	}
	if r := c.do("POST", "/v1/api-keys", map[string]any{"name": "Stale", "scopes": []string{"tickets:read"}}); r.Status != 403 || r.str("code") != "reauth_required" {
		t.Fatalf("old sign-in: %d %s", r.Status, r.Raw)
	}
}

// A webhook's address proves it wants the events before any is sent (CASA 7.1.2): by
// echoing the challenge, or by an admin entering the code it received.
func TestWebhookVerification(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("hookverify")
	c.workspace = c.newWorkspace("Hooks Verify Co")

	// An address that echoes the challenge is verified at once.
	echo := httptest.NewServer(echoChallenges(nil))
	defer echo.Close()
	if r := c.do("POST", "/v1/webhooks", map[string]any{"url": echo.URL}); r.Status != 201 || r.Body["verified"] != true {
		t.Fatalf("echoing address: %d %s", r.Status, r.Raw)
	}

	// One that only receives (a no-code tool) waits for the code to be entered.
	var mu sync.Mutex
	var challenge string
	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Challenge string `json:"challenge"`
			} `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&ev)
		mu.Lock()
		if ev.Type == "webhook.verification" {
			challenge = ev.Data.Challenge
		}
		mu.Unlock()
	}))
	defer quiet.Close()
	r := c.do("POST", "/v1/webhooks", map[string]any{"url": quiet.URL})
	if r.Status != 201 || r.Body["verified"] != false {
		t.Fatalf("quiet address: %d %s", r.Status, r.Raw)
	}
	path := "/v1/webhooks/" + r.str("id")
	if r := c.do("POST", path+"/test", nil); r.Status != 409 {
		t.Fatalf("test before verification: %d", r.Status)
	}
	if r := c.do("POST", path+"/verify", map[string]any{"code": "whv_wrong"}); r.Status != 422 {
		t.Fatalf("wrong code: %d %s", r.Status, r.Raw)
	}
	mu.Lock()
	code := challenge
	mu.Unlock()
	if r := c.do("POST", path+"/verify", map[string]any{"code": code}); r.Status != 200 || r.Body["verified"] != true {
		t.Fatalf("right code: %d %s", r.Status, r.Raw)
	}
	if r := c.do("POST", path+"/test", nil); r.Status != 202 {
		t.Fatalf("test once verified: %d %s", r.Status, r.Raw)
	}

	// A new address must prove itself again.
	if r := c.do("PATCH", path, map[string]any{"url": quiet.URL + "/elsewhere"}); r.Status != 200 || r.Body["verified"] != false {
		t.Fatalf("moved: %d %s", r.Status, r.Raw)
	}
}
