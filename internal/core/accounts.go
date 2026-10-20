package core

import (
	"context"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// A browser can be signed in to several accounts at once (contracts/tokens.md). Each
// account keeps its own refresh cookie, named after its user id, so signing in, refreshing
// or signing out one account never touches another's.
const (
	// RefreshCookiePrefix starts every account's refresh cookie: od_rt_<user id hex>.
	RefreshCookiePrefix = "od_rt_"
	// LegacyRefreshCookie is the single-account cookie of earlier versions. It is still
	// read, and moved to the account's own cookie the first time it is used.
	LegacyRefreshCookie = "od_refresh"
	// MaxAccounts is how many accounts one browser may hold sessions for.
	MaxAccounts = 5
	// maxPresentedCookies bounds the refresh cookies read from one request.
	maxPresentedCookies = 2*MaxAccounts + 1
)

// RefreshCookieName is the refresh cookie of one account, before its prefix.
func RefreshCookieName(uid uuid.UUID) string {
	return RefreshCookiePrefix + hex.EncodeToString(uid[:])
}

// Cookie name prefixes the browser enforces: a __Host- cookie was set by this host alone,
// over https, for Path=/, so no sibling subdomain can plant or overwrite it (ASVS 3.4.4).
// __Secure- is the most an instance with COOKIE_DOMAIN can use.
const (
	hostCookiePrefix   = "__Host-"
	secureCookiePrefix = "__Secure-"
)

// refreshCookieName is the name an account's refresh cookie is set under on this
// instance: __Host- prefixed on https, __Secure- with COOKIE_DOMAIN, bare on plain http.
// Cookies set under the bare name by earlier versions are still read, and replaced the
// next time the account's cookie is set.
func (s *svc) refreshCookieName(uid uuid.UUID) string {
	switch {
	case !s.d.Cfg.CookieSecure:
		return RefreshCookieName(uid)
	case s.d.Cfg.CookieDomain == "":
		return hostCookiePrefix + RefreshCookieName(uid)
	default:
		return secureCookiePrefix + RefreshCookieName(uid)
	}
}

// presentedCookie is a refresh cookie sent by the browser. UserID is the account its
// name claims (uuid.Nil for the legacy cookie); the session behind the value must agree.
type presentedCookie struct {
	Name   string
	UserID uuid.UUID
	Value  string
}

func (c presentedCookie) legacy() bool { return c.UserID == uuid.Nil }

// readRefreshCookies collects the refresh cookies of a request, bounded in number and size.
func readRefreshCookies(r *http.Request) []presentedCookie {
	var out []presentedCookie
	for _, c := range r.Cookies() {
		if len(out) >= maxPresentedCookies {
			break
		}
		if c.Value == "" || len(c.Value) > 200 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(c.Name, hostCookiePrefix), secureCookiePrefix)
		switch {
		case c.Name == LegacyRefreshCookie:
			out = append(out, presentedCookie{Name: c.Name, Value: c.Value})
		case strings.HasPrefix(name, RefreshCookiePrefix):
			raw, err := hex.DecodeString(strings.TrimPrefix(name, RefreshCookiePrefix))
			if err != nil || len(raw) != 16 {
				continue
			}
			uid, _ := uuid.FromBytes(raw)
			if uid == uuid.Nil {
				continue
			}
			out = append(out, presentedCookie{Name: c.Name, UserID: uid, Value: c.Value})
		}
	}
	return out
}

func (ri reqInfo) cookieFor(uid uuid.UUID) *presentedCookie {
	for i := range ri.Cookies {
		if ri.Cookies[i].UserID == uid && uid != uuid.Nil {
			return &ri.Cookies[i]
		}
	}
	return nil
}

func (ri reqInfo) legacyCookie() *presentedCookie {
	for i := range ri.Cookies {
		if ri.Cookies[i].legacy() {
			return &ri.Cookies[i]
		}
	}
	return nil
}

// heldSession is a presented refresh cookie resolved against the sessions table.
type heldSession struct {
	presentedCookie
	// Valid: the cookie names a live session of the account its name claims.
	Valid bool
	// Stale: the value is the session's previous token, rotated moments ago by another
	// tab; the browser already has (or is receiving) the new one, so it is left alone.
	Stale     bool
	Account   uuid.UUID
	SessionID uuid.UUID
	Created   time.Time
	LastUsed  time.Time
	Expires   time.Time
}

// heldSessions resolves every refresh cookie of the request in one query.
func (s *svc) heldSessions(ctx context.Context, q db.Querier, ri reqInfo) ([]heldSession, error) {
	if len(ri.Cookies) == 0 {
		return nil, nil
	}
	hashes := make([][]byte, len(ri.Cookies))
	for i, c := range ri.Cookies {
		hashes[i] = crypto.HashToken(c.Value)
	}
	rows, err := q.Query(ctx, `SELECT id, user_id, refresh_hash, COALESCE(prev_refresh_hash, ''::bytea),
			COALESCE(prev_refresh_hash = ANY($1) AND rotated_at > now() - make_interval(secs => $2), false) AS stale,
			created_at, last_used_at, expires_at
		FROM sessions
		WHERE revoked_at IS NULL AND expires_at > now() AND created_at > now() - make_interval(secs => $3)
			AND (refresh_hash = ANY($1) OR (prev_refresh_hash = ANY($1) AND rotated_at > now() - make_interval(secs => $2)))`,
		hashes, refreshReuseGrace.Seconds(), SessionMaxAge.Seconds())
	if err != nil {
		return nil, err
	}
	type row struct {
		sid, uid             uuid.UUID
		hash, prev           []byte
		stale                bool
		created, used, until time.Time
	}
	byHash := map[string]row{}
	err = forEachRow(rows, func(r pgx.CollectableRow) error {
		var x row
		if err := r.Scan(&x.sid, &x.uid, &x.hash, &x.prev, &x.stale, &x.created, &x.used, &x.until); err != nil {
			return err
		}
		byHash[string(x.hash)] = x
		if x.stale {
			byHash[string(x.prev)] = x
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]heldSession, len(ri.Cookies))
	for i, c := range ri.Cookies {
		h := heldSession{presentedCookie: c}
		if x, ok := byHash[string(hashes[i])]; ok && (c.legacy() || x.uid == c.UserID) {
			h.Valid, h.Stale = true, string(x.hash) != string(hashes[i])
			h.Account, h.SessionID, h.Created, h.LastUsed, h.Expires = x.uid, x.sid, x.created, x.used, x.until
		}
		out[i] = h
	}
	return out, nil
}

func forEachRow(rows pgx.Rows, fn func(pgx.CollectableRow) error) error {
	_, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) {
		return struct{}{}, fn(r)
	})
	return err
}

func errTooManyAccounts() *httpx.Error {
	return httpx.E(409, "too_many_accounts", "You're signed in to the most accounts this browser can hold. Log out of one, then try again.")
}

// cookieMaxAge is the seconds left until t, at least one.
func cookieMaxAge(t time.Time) int {
	if d := int(time.Until(t).Seconds()); d > 0 {
		return d
	}
	return 1
}

// adoptCookies runs while a browser session is issued for uid. It refuses a sixth
// account, ends the session this browser already held for uid (its cookie is about to be
// replaced), and returns the Set-Cookie values that tidy the others: invalid cookies are
// cleared and a legacy cookie moves to its account's own cookie.
func (s *svc) adoptCookies(ctx context.Context, q db.Querier, uid uuid.UUID, ri reqInfo) ([]string, error) {
	held, err := s.heldSessions(ctx, q, ri)
	if err != nil {
		return nil, err
	}
	others := map[uuid.UUID]bool{}
	for _, h := range held {
		if h.Valid && h.Account != uid {
			others[h.Account] = true
		}
	}
	if len(others) >= MaxAccounts {
		return nil, errTooManyAccounts()
	}
	var set []string
	for _, h := range held {
		switch {
		case !h.Valid:
			set = append(set, s.clearCookie(h.Name))
		case h.Account == uid:
			if _, err := endSession(ctx, q, uid, h.SessionID); err != nil {
				return nil, err
			}
			s.disconnect(uid, h.SessionID)
			if h.legacy() {
				set = append(set, s.clearCookie(h.Name))
			}
		case h.legacy() && !h.Stale:
			set = append(set, s.migrateLegacy(h, ri)...)
		}
	}
	return set, nil
}

// migrateLegacy moves a valid legacy cookie to its account's own cookie, unless the
// browser already holds one for that account.
func (s *svc) migrateLegacy(h heldSession, ri reqInfo) []string {
	out := []string{s.clearCookie(LegacyRefreshCookie)}
	if ri.cookieFor(h.Account) == nil {
		out = append(out, s.refreshCookie(s.refreshCookieName(h.Account), h.Value, cookieMaxAge(h.Expires)))
	}
	return out
}

// CoreAccount is one account this browser is signed in to (POST /v1/auth/accounts).
type CoreAccount struct {
	User        CoreAccountUser         `json:"user"`
	Memberships []CoreAccountMembership `json:"memberships"`
}

type CoreAccountUser struct {
	ID        uuid.UUID     `json:"id"`
	Name      string        `json:"name"`
	Email     string        `json:"email"`
	AvatarURL domain.Avatar `json:"avatar_url"`
}

type CoreAccountMembership struct {
	Workspace CoreAccountWorkspace `json:"workspace"`
	Role      string               `json:"role" enum:"owner,admin,agent,viewer"`
}

type CoreAccountWorkspace struct {
	ID        uuid.UUID     `json:"id"`
	Name      string        `json:"name"`
	Slug      string        `json:"slug"`
	AvatarURL domain.Avatar `json:"avatar_url"`
}

type accountsOut struct {
	SetCookie    []string `header:"Set-Cookie"`
	CacheControl string   `header:"Cache-Control"`
	Body         struct {
		Accounts []CoreAccount `json:"accounts"`
	}
}

func loadAccount(ctx context.Context, q db.Querier, uid uuid.UUID) (CoreAccount, error) {
	a := CoreAccount{Memberships: []CoreAccountMembership{}}
	err := q.QueryRow(ctx, `SELECT id, name, email, avatar_url FROM users WHERE id = $1`, uid).
		Scan(&a.User.ID, &a.User.Name, &a.User.Email, &a.User.AvatarURL)
	if err != nil {
		return a, err
	}
	rows, err := q.Query(ctx, `SELECT w.id, w.name, w.slug, w.avatar_url, m.role FROM members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY m.created_at LIMIT 200`, uid)
	if err != nil {
		return a, err
	}
	err = forEachRow(rows, func(r pgx.CollectableRow) error {
		var m CoreAccountMembership
		if err := r.Scan(&m.Workspace.ID, &m.Workspace.Name, &m.Workspace.Slug, &m.Workspace.AvatarURL, &m.Role); err != nil {
			return err
		}
		a.Memberships = append(a.Memberships, m)
		return nil
	})
	return a, err
}

func (s *svc) registerAccounts(api huma.API) {
	register(s, api, huma.Operation{OperationID: "auth-accounts", Method: http.MethodPost, Path: "/v1/auth/accounts",
		Summary: "List the accounts this browser is signed in to", Tags: []string{"Auth"}, Security: []map[string][]string{},
		Description: "Reads the browser's refresh cookies without rotating them. Accounts whose session ended are left out and their cookies cleared."},
		func(ctx context.Context, _ *struct{}) (*accountsOut, error) {
			ri := requestInfo(ctx)
			out := &accountsOut{CacheControl: "no-store"}
			out.Body.Accounts = []CoreAccount{}
			held, err := s.heldSessions(ctx, s.d.DB.Pool, ri)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// In the order the accounts were added to this browser.
			sort.SliceStable(held, func(i, j int) bool {
				if !held[i].Created.Equal(held[j].Created) {
					return held[i].Created.Before(held[j].Created)
				}
				return held[i].Name < held[j].Name
			})
			seen := map[uuid.UUID]bool{}
			for _, h := range held {
				if !h.Valid {
					out.SetCookie = append(out.SetCookie, s.clearCookie(h.Name))
					continue
				}
				if seen[h.Account] {
					if h.legacy() {
						out.SetCookie = append(out.SetCookie, s.clearCookie(h.Name))
					}
					continue
				}
				a, err := loadAccount(ctx, s.d.DB.Pool, h.Account)
				if db.IsNotFound(err) {
					continue
				}
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				seen[h.Account] = true
				if h.legacy() && !h.Stale {
					out.SetCookie = append(out.SetCookie, s.migrateLegacy(h, ri)...)
				}
				out.Body.Accounts = append(out.Body.Accounts, a)
			}
			return out, nil
		})
}

// logoutBody names what to sign out. Browsers send user_id (the account) or all; native
// requests send their refresh token.
type logoutBody struct {
	RefreshToken string `json:"refresh_token,omitempty" maxLength:"200" doc:"Native requests: the refresh token of the session to end"`
	UserID       string `json:"user_id,omitempty" maxLength:"36" doc:"The account to sign out of in this browser; defaults to the access token's"`
	All          bool   `json:"all,omitempty" doc:"Sign out of every account in this browser"`
}

type sessionRef struct{ uid, sid uuid.UUID }

// logoutTargets works out which sessions a logout ends and which cookies it clears.
func (s *svc) logoutTargets(ctx context.Context, q db.Querier, p sessionRef, in *logoutBody, ri reqInfo) ([]sessionRef, []string, error) {
	var targets []sessionRef
	var clear []string
	add := func(r sessionRef) {
		for _, t := range targets {
			if t == r {
				return
			}
		}
		targets = append(targets, r)
	}
	if p.sid != uuid.Nil {
		add(p)
	}
	held, err := s.heldSessions(ctx, q, ri)
	if err != nil {
		return nil, nil, err
	}
	if in.All {
		for _, h := range held {
			if h.Valid {
				add(sessionRef{h.Account, h.SessionID})
			}
			clear = append(clear, s.clearCookie(h.Name))
		}
	} else {
		want := p.uid
		if want == uuid.Nil {
			if id, err := uuid.Parse(in.UserID); err == nil {
				want = id
			}
		}
		if want == uuid.Nil && len(held) == 1 {
			want = held[0].Account
			if !held[0].Valid {
				clear = append(clear, s.clearCookie(held[0].Name))
			}
		}
		if want != uuid.Nil {
			for _, h := range held {
				if h.Valid && h.Account == want {
					add(sessionRef{h.Account, h.SessionID})
					clear = append(clear, s.clearCookie(h.Name))
				}
			}
			if !ri.Native {
				if c := ri.cookieFor(want); c != nil && !containsCookie(clear, c.Name) {
					clear = append(clear, s.clearCookie(c.Name))
				}
			}
		}
	}
	if in.RefreshToken != "" && len(targets) == 0 {
		var r sessionRef
		err := q.QueryRow(ctx, `SELECT user_id, id FROM sessions WHERE refresh_hash = $1 AND revoked_at IS NULL`,
			crypto.HashToken(in.RefreshToken)).Scan(&r.uid, &r.sid)
		if err != nil && !db.IsNotFound(err) {
			return nil, nil, err
		}
		if err == nil {
			add(r)
		}
	}
	return targets, clear, nil
}

func containsCookie(set []string, name string) bool {
	for _, c := range set {
		if strings.HasPrefix(c, name+"=") {
			return true
		}
	}
	return false
}
