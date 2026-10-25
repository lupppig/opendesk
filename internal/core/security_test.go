package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func TestPasswordGate(t *testing.T) {
	for _, pw := range []string{"short", strings.Repeat("a", 129), "password1234", "Password1234"} {
		if auth.CheckPassword(pw) == nil {
			t.Errorf("CheckPassword(%q) accepted", pw)
		}
	}
	if err := auth.CheckPassword(goodPassword); err != nil {
		t.Fatalf("good password refused: %v", err)
	}

	e := newEnv(t)
	c := e.client()
	r := c.do("POST", "/v1/auth/register", map[string]any{"email": uniqueEmail(t, "gate"), "password": "password1234", "name": "Gate"})
	if r.Status != 422 || r.str("code") != "weak_password" {
		t.Fatalf("breached password at registration: %d %s", r.Status, r.Raw)
	}
	u, _ := e.signup("gate")
	r = u.do("POST", "/v1/me/password", map[string]any{"current_password": goodPassword, "new_password": "short"})
	if r.Status != 422 || r.str("code") != "weak_password" {
		t.Fatalf("weak password at change: %d %s", r.Status, r.Raw)
	}
}

func TestLoginSameAnswer(t *testing.T) {
	e := newEnv(t)
	_, email := e.signup("login")
	c := e.client()
	wrong := c.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": "not the password at all"})
	unknown := c.do("POST", "/v1/auth/login", map[string]any{"email": uniqueEmail(t, "nobody"), "password": "not the password at all"})
	if wrong.Status != 401 || unknown.Status != 401 || wrong.str("code") != "invalid_credentials" || wrong.str("detail") != unknown.str("detail") {
		t.Fatalf("login answers differ: %d %s / %d %s", wrong.Status, wrong.Raw, unknown.Status, unknown.Raw)
	}
	ok := c.do("POST", "/v1/auth/login", map[string]any{"email": strings.ToUpper(email), "password": goodPassword})
	if ok.Status != 200 || ok.str("access_token") == "" {
		t.Fatalf("login: %d %s", ok.Status, ok.Raw)
	}
}

func TestTokenPurposes(t *testing.T) {
	tok := auth.NewTokens([]byte("test-auth-secret-test-auth-secret-0000"))
	for _, p := range []string{auth.PurposeMFA, auth.PurposeWS, auth.PurposeOAuthState, auth.PurposeWidgetWS} {
		s, _, err := tok.Sign(auth.Claims{Purpose: p}, uuid.NewString(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tok.Verify(s, auth.PurposeAccess); err == nil {
			t.Errorf("%s token accepted as access", p)
		}
	}

	e := newEnv(t)
	u, email := e.signup("purpose")
	// An mfa token is not a bearer credential.
	var uid uuid.UUID
	if err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	mfa, _, _ := e.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeMFA}, uid.String(), time.Minute)
	c := e.client()
	c.token = mfa
	if r := c.do("GET", "/v1/me", nil); r.Status != 401 {
		t.Fatalf("mfa token used as bearer: %d", r.Status)
	}
	// An access token is not an mfa token.
	if r := c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": u.token, "code": "000000"}); r.Status != 401 || r.str("code") != "invalid_mfa_token" {
		t.Fatalf("access token accepted as mfa token: %d %s", r.Status, r.Raw)
	}
	// A ws token minted by the API is purpose ws.
	u.newWorkspace("Purpose Co")
	r := u.do("POST", "/v1/realtime/token", nil)
	if r.Status != 200 {
		t.Fatalf("realtime token: %d %s", r.Status, r.Raw)
	}
	cl, err := e.d.Tokens.Verify(r.str("token"), auth.PurposeWS)
	if err != nil || cl.Kind != "user" || cl.Workspace != u.workspace || cl.Role != "owner" {
		t.Fatalf("ws claims: %+v %v", cl, err)
	}
	// It is bound to the session that asked for it, with an issue time in milliseconds
	// no later than its whole-second iat allows.
	access, err := e.d.Tokens.Verify(u.token, auth.PurposeAccess)
	if err != nil || access.SessionID == "" || cl.SessionID != access.SessionID {
		t.Fatalf("ws sid %q, session %+v %v", cl.SessionID, access, err)
	}
	if cl.IssuedAtMs <= 0 || cl.IssuedAtMs/1000 > cl.IssuedAt.Unix() {
		t.Fatalf("ws iat_ms %d, iat %v", cl.IssuedAtMs, cl.IssuedAt)
	}
	if _, err := e.d.Tokens.Verify(r.str("token"), auth.PurposeAccess); err == nil {
		t.Fatal("ws token accepted as access")
	}
}

func TestTOTPReplay(t *testing.T) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "opendesk", AccountName: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, _ := totp.GenerateCode(key.Secret(), now)
	step, ok := totpMatch(key.Secret(), code, now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("totpMatch: %d %v", step, ok)
	}
	if _, ok := totpMatch(key.Secret(), code, now.Add(5*time.Minute)); ok {
		t.Fatal("stale code accepted")
	}

	e := newEnv(t)
	u, email := e.signup("totp")
	r := u.do("POST", "/v1/auth/totp/setup", nil)
	secret := r.str("secret")
	if r.Status != 200 || secret == "" {
		t.Fatalf("setup: %d %s", r.Status, r.Raw)
	}
	code, _ = totp.GenerateCode(secret, time.Now())
	r = u.do("POST", "/v1/auth/totp/enable", map[string]any{"code": code})
	if r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Raw)
	}
	var codes []string
	_ = json.Unmarshal(mustJSON(r.Body["recovery_codes"]), &codes)
	if len(codes) != 10 {
		t.Fatalf("recovery codes: %v", codes)
	}

	login := func() string {
		c := e.client()
		r := c.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": goodPassword})
		if r.Status != 200 || r.Body["mfa_required"] != true {
			t.Fatalf("login with totp: %d %s", r.Status, r.Raw)
		}
		return r.str("mfa_token")
	}
	c := e.client()
	// The code that enabled TOTP is spent.
	if r := c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": login(), "code": code}); r.Status != 401 {
		t.Fatalf("enable code replayed: %d %s", r.Status, r.Raw)
	}
	// A recovery code works once.
	if r := c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": login(), "code": strings.ToUpper(codes[0])}); r.Status != 200 {
		t.Fatalf("recovery code: %d %s", r.Status, r.Raw)
	}
	if r := c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": login(), "code": codes[0]}); r.Status != 401 {
		t.Fatalf("recovery code reused: %d %s", r.Status, r.Raw)
	}
	// A code for the next step works once; its replay is refused.
	next, _ := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	r = c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": login(), "code": next})
	if r.Status != 200 || r.str("access_token") == "" {
		t.Fatalf("mfa: %d %s", r.Status, r.Raw)
	}
	if cl, _ := e.d.Tokens.Verify(r.str("access_token"), auth.PurposeAccess); cl == nil || !cl.MFA {
		t.Fatal("access token after mfa lacks mfa claim")
	}
	if r := c.do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": login(), "code": next}); r.Status != 401 {
		t.Fatalf("totp replay accepted: %d %s", r.Status, r.Raw)
	}
}

func TestRefreshRotation(t *testing.T) {
	e := newEnv(t)
	u, _ := e.signup("refresh")
	name, first := u.accountCookie()
	if first == "" {
		t.Fatal("no refresh cookie")
	}
	r := u.do("POST", "/v1/auth/refresh", nil)
	if r.Status != 200 || r.str("access_token") == "" {
		t.Fatalf("refresh: %d %s", r.Status, r.Raw)
	}
	second := u.cookie(name)
	if second == "" || second == first {
		t.Fatal("refresh token did not rotate")
	}
	sc := r.Header.Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "Path=/v1/auth", "SameSite=Lax"} {
		if !strings.Contains(sc, want) {
			t.Errorf("cookie lacks %s: %s", want, sc)
		}
	}
	// Replaying the rotated token inside the grace window is refused without ending the session.
	old := e.client()
	old.jar.SetCookies(mustURL(e.srv.URL+"/v1/auth"), []*http.Cookie{{Name: name, Value: first, Path: "/v1/auth"}})
	if r := old.do("POST", "/v1/auth/refresh", nil); r.Status != 401 {
		t.Fatalf("rotated token accepted: %d", r.Status)
	}
	if r := u.do("POST", "/v1/auth/refresh", nil); r.Status != 200 {
		t.Fatalf("session ended by in-grace replay: %d %s", r.Status, r.Raw)
	}
	// After the grace window, a replay of a rotated token ends the session.
	third := u.cookie(name)
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE sessions SET rotated_at = now() - interval '5 minutes' WHERE refresh_hash = $1`,
		crypto.HashToken(third)); err != nil {
		t.Fatal(err)
	}
	old2 := e.client()
	old2.jar.SetCookies(mustURL(e.srv.URL+"/v1/auth"), []*http.Cookie{{Name: name, Value: second, Path: "/v1/auth"}})
	if r := old2.do("POST", "/v1/auth/refresh", nil); r.Status != 401 {
		t.Fatalf("stale rotated token accepted: %d", r.Status)
	}
	if r := u.do("POST", "/v1/auth/refresh", nil); r.Status != 401 {
		t.Fatalf("session survived refresh token reuse: %d", r.Status)
	}
	// Logout revokes.
	v, _ := e.signup("logout")
	if r := v.do("POST", "/v1/auth/logout", nil); r.Status != 204 {
		t.Fatalf("logout: %d", r.Status)
	}
	if r := v.do("POST", "/v1/auth/refresh", nil); r.Status != 401 {
		t.Fatalf("refresh after logout: %d", r.Status)
	}
}

func TestWebhookSignature(t *testing.T) {
	sig := signWebhook([]byte("whsec_test"), 1700000000, []byte(`{"a":1}`))
	want := "t=1700000000,v1=" + crypto.HMACHex([]byte("whsec_test"), `1700000000.{"a":1}`)
	if sig != want {
		t.Fatalf("signature %s want %s", sig, want)
	}

	e := newEnv(t)
	u, _ := e.signup("hooks")
	u.newWorkspace("Hooks Co")
	type got struct {
		body []byte
		hdr  http.Header
	}
	ch := make(chan got, 4)
	recv := httptest.NewServer(echoChallenges(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- got{b, r.Header.Clone()}
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer recv.Close()
	r := u.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL + "/hook", "events": []string{"team.created"}})
	if r.Status != 201 || !strings.HasPrefix(r.str("secret"), "whsec_") || r.Body["verified"] != true {
		t.Fatalf("create webhook: %d %s", r.Status, r.Raw)
	}
	secret := r.str("secret")
	if r := u.do("POST", "/v1/teams", map[string]any{"name": "Support"}); r.Status != 201 {
		t.Fatalf("team: %d %s", r.Status, r.Raw)
	}
	ctx := context.Background()
	var seq int64
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT seq FROM events WHERE workspace_id = $1 AND type = 'team.created'`, u.workspace).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	wid := uuid.MustParse(u.workspace)
	dw := &dispatchWorker{s: e.s}
	if err := dw.Work(ctx, &river.Job[jobs.WebhookDispatchArgs]{Args: jobs.WebhookDispatchArgs{WorkspaceID: wid, EventSeq: seq}}); err != nil {
		t.Fatal(err)
	}
	var did uuid.UUID
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT id FROM webhook_deliveries WHERE workspace_id = $1 AND event_type = 'team.created'`, wid).Scan(&did); err != nil {
		t.Fatal(err)
	}
	job := &river.Job[jobs.WebhookDeliverArgs]{Args: jobs.WebhookDeliverArgs{DeliveryID: did}}
	job.JobRow = &rivertype.JobRow{Attempt: 1, MaxAttempts: webhookMaxAttempts}
	if err := (&deliverWorker{s: e.s}).Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	g := <-ch
	parts := strings.Split(g.hdr.Get("OD-Signature"), ",")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "t=") {
		t.Fatalf("signature header: %q", g.hdr.Get("OD-Signature"))
	}
	ts, _ := strconv.ParseInt(strings.TrimPrefix(parts[0], "t="), 10, 64)
	if !crypto.EqualHex(parts[1], "v1="+crypto.HMACHex([]byte(secret), strconv.FormatInt(ts, 10)+"."+string(g.body))) {
		t.Fatal("signature does not verify")
	}
	var env map[string]any
	_ = json.Unmarshal(g.body, &env)
	if env["type"] != "team.created" || g.hdr.Get("OD-Event-Type") != "team.created" || g.hdr.Get("OD-Event-Id") != env["id"] {
		t.Fatalf("envelope: %v %v", env, g.hdr)
	}
	if _, has := env["topics"]; has {
		t.Fatal("envelope carries topics")
	}
	var status, body string
	_ = e.d.DB.Pool.QueryRow(ctx, `SELECT status, response_body FROM webhook_deliveries WHERE id = $1`, did).Scan(&status, &body)
	if status != "succeeded" || len(body) != webhookResponseKeep {
		t.Fatalf("delivery recorded %s with %d bytes", status, len(body))
	}
}

func TestSSRFGuard(t *testing.T) {
	ctx := context.Background()
	prod := outboundGuard{dev: false}
	for _, u := range []string{
		"http://example.com/hook",
		"https://127.0.0.1/hook",
		"https://localhost/hook",
		"https://10.0.0.5/hook",
		"https://192.168.1.1/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/hook",
		"https://[fd00::1]/hook",
		"https://[::ffff:127.0.0.1]/hook",
		"https://100.64.0.1/hook",
		"https://0.0.0.0/hook",
		"https://metadata.google.internal/computeMetadata",
		"https://user:pass@example.com/hook",
		"ftp://example.com/hook",
		"//example.com/hook",
	} {
		if _, err := prod.validateURL(ctx, u); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !isPublicIP(netip.MustParseAddr(ip)) {
			t.Errorf("%s judged private", ip)
		}
	}
	dev := outboundGuard{dev: true}
	if _, err := dev.validateURL(ctx, "http://127.0.0.1:9000/hook"); err != nil {
		t.Errorf("dev loopback refused: %v", err)
	}
	if _, err := dev.validateURL(ctx, "http://10.0.0.5/hook"); err == nil {
		t.Error("dev accepted a private http target")
	}
	if _, err := dev.validateURL(ctx, "https://169.254.169.254/"); err == nil {
		t.Error("dev accepted metadata")
	}
	// The delivery client re-checks at dial time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := webhookClient(false).Get(srv.URL)
	if !errors.Is(err, errBlockedAddress) {
		t.Fatalf("production client reached loopback: %v", err)
	}
	if res, err := webhookClient(true).Get(srv.URL); err != nil {
		t.Fatalf("dev client: %v", err)
	} else {
		res.Body.Close()
	}
	// Redirects are not followed.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer redir.Close()
	res, err := webhookClient(true).Get(redir.URL)
	if err != nil || res.StatusCode != http.StatusFound {
		t.Fatalf("redirect followed: %v", err)
	}
	res.Body.Close()
}

func TestCrossWorkspaceRefused(t *testing.T) {
	e := newEnv(t)
	a, _ := e.signup("alice")
	wa := a.newWorkspace("Alpha")
	b, _ := e.signup("bob")
	wb := b.newWorkspace("Beta")

	r := a.do("POST", "/v1/teams", map[string]any{"name": "Alpha team"})
	if r.Status != 201 {
		t.Fatalf("team: %d %s", r.Status, r.Raw)
	}
	teamA := r.str("id")

	// Bob naming Alpha in the header is not a member there.
	b.workspace = wa
	if r := b.do("GET", "/v1/teams", nil); r.Status != 400 || r.str("code") != "workspace_required" {
		t.Fatalf("foreign workspace header: %d %s", r.Status, r.Raw)
	}
	b.workspace = wb
	if r := b.do("GET", "/v1/teams/"+teamA, nil); r.Status != 404 {
		t.Fatalf("foreign team readable: %d", r.Status)
	}
	if r := b.do("PATCH", "/v1/teams/"+teamA, map[string]any{"name": "Owned"}); r.Status != 404 {
		t.Fatalf("foreign team writable: %d", r.Status)
	}
	if r := b.do("DELETE", "/v1/teams/"+teamA, nil); r.Status != 404 {
		t.Fatalf("foreign team deletable: %d", r.Status)
	}
	// A team in Beta cannot seat a member of Alpha.
	var aliceID string
	ar := a.do("GET", "/v1/me", nil)
	aliceID = ar.str("user", "id")
	if r := b.do("POST", "/v1/teams", map[string]any{"name": "Beta team", "member_ids": []string{aliceID}}); r.Status != 422 {
		t.Fatalf("foreign member seated: %d %s", r.Status, r.Raw)
	}
	if r := b.do("PATCH", "/v1/members/"+aliceID, map[string]any{"role": "viewer"}); r.Status != 404 {
		t.Fatalf("foreign member role changed: %d", r.Status)
	}
	// An API key is bound to its workspace whatever the header says.
	r = a.do("POST", "/v1/api-keys", map[string]any{"name": "Alpha key", "scopes": []string{"settings:read", "realtime"}})
	if r.Status != 201 || !strings.HasPrefix(r.str("secret"), "od_live_") {
		t.Fatalf("api key: %d %s", r.Status, r.Raw)
	}
	key := e.client()
	key.token, key.workspace = r.str("secret"), wb
	tr := key.do("GET", "/v1/teams", nil)
	if tr.Status != 200 || !strings.Contains(string(tr.Raw), teamA) {
		t.Fatalf("key listing: %d %s", tr.Status, tr.Raw)
	}
	if r := key.do("GET", "/v1/api-keys", nil); r.Status != 403 {
		t.Fatalf("key managed keys: %d", r.Status)
	}
	if r := key.do("POST", "/v1/teams", map[string]any{"name": "Nope"}); r.Status != 403 {
		t.Fatalf("read key wrote: %d", r.Status)
	}
	// Viewers read only.
	inv := a.do("POST", "/v1/invitations", map[string]any{"email": "viewer-" + crypto.RandomString(6) + "@example.com", "role": "viewer"})
	if inv.Status != 201 {
		t.Fatalf("invite: %d %s", inv.Status, inv.Raw)
	}
	if r := b.do("GET", "/v1/invitations", nil); !strings.Contains(string(r.Raw), `"data":[]`) {
		t.Fatalf("foreign invitations visible: %s", r.Raw)
	}
	if r := b.do("DELETE", "/v1/invitations/"+inv.str("id"), nil); r.Status != 404 {
		t.Fatalf("foreign invitation revoked: %d", r.Status)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }
