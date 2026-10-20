package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
)

// TestCORSAdminRoutes: the admin routes allow only the panel's origin, and the panel's
// origin is not allowed anywhere else.
func TestCORSAdminRoutes(t *testing.T) {
	h := CORS([]string{"https://app.example.com"}, "https://admin.example.com")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	allowed := func(path, origin string) string {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Header().Get("Access-Control-Allow-Origin")
	}
	cases := []struct{ path, origin, want string }{
		{"/v1/admin/me", "https://admin.example.com", "https://admin.example.com"},
		{"/v1/admin/me", "https://app.example.com", ""},
		{"/v1/tickets", "https://app.example.com", "https://app.example.com"},
		{"/v1/tickets", "https://admin.example.com", ""},
		{"/v1/widget/config", "https://admin.example.com", "*"},
	}
	for _, c := range cases {
		if got := allowed(c.path, c.origin); got != c.want {
			t.Errorf("%s from %s: %q, want %q", c.path, c.origin, got, c.want)
		}
	}
}

// TestClientIP: hops reads X-Forwarded-For from the right, so what a client puts on the
// left never becomes its address; trustProxy alone believes the headers as set.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name  string
		trust bool
		hops  int
		xff   []string
		cf    string
		want  string
	}{
		{"no trust ignores headers", false, 0, []string{"198.51.100.1"}, "198.51.100.2", "10.0.0.9"},
		{"trust takes CF-Connecting-IP", true, 0, []string{"198.51.100.1"}, "198.51.100.2", "198.51.100.2"},
		{"trust takes the first entry", true, 0, []string{"198.51.100.1, 10.0.0.5"}, "", "198.51.100.1"},
		{"one hop takes the last entry", false, 1, []string{"203.0.113.66, 198.51.100.1"}, "", "198.51.100.1"},
		{"hops ignore CF-Connecting-IP", true, 1, []string{"198.51.100.1"}, "203.0.113.66", "198.51.100.1"},
		{"two hops", false, 2, []string{"203.0.113.66, 198.51.100.1, 10.0.0.5"}, "", "198.51.100.1"},
		{"hops count across header lines", false, 2, []string{"203.0.113.66, 198.51.100.1", "10.0.0.5"}, "", "198.51.100.1"},
		{"entries that are not addresses do not count", false, 1, []string{"198.51.100.1, unknown"}, "", "198.51.100.1"},
		{"fewer entries than hops is the peer", false, 2, []string{"198.51.100.1"}, "", "10.0.0.9"},
		{"no header is the peer", false, 1, nil, "", "10.0.0.9"},
		{"IPv6", false, 1, []string{"2001:db8::1"}, "", "2001:db8::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.9:51234"
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if got := ClientIP(c.trust, c.hops)(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAPIResponseHeaders: API responses are not stored and JSON ones download, unless the
// route sets its own; other paths are left alone.
func TestAPIResponseHeaders(t *testing.T) {
	serve := func(path, ct, cache, disposition string) http.Header {
		h := SecurityHeaders(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", ct)
			if cache != "" {
				w.Header().Set("Cache-Control", cache)
			}
			if disposition != "" {
				w.Header().Set("Content-Disposition", disposition)
			}
			_, _ = w.Write([]byte("{}"))
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Header()
	}
	if h := serve("/v1/tickets", "application/json", "", ""); h.Get("Cache-Control") != "no-store" || h.Get("Content-Disposition") != `attachment; filename="api.json"` {
		t.Fatalf("json: %v", h)
	}
	if h := serve("/v1/images/x", "image/png", "public, max-age=3600", ""); h.Get("Cache-Control") != "public, max-age=3600" || h.Get("Content-Disposition") != "" {
		t.Fatalf("image: %v", h)
	}
	if h := serve("/v1/attachments/x", "application/json", "", `attachment; filename="data.json"`); h.Get("Content-Disposition") != `attachment; filename="data.json"` {
		t.Fatalf("route disposition: %v", h)
	}
	if h := serve("/inbox", "text/html", "", ""); h.Get("Cache-Control") != "" || h.Get("Content-Disposition") != "" {
		t.Fatalf("app page: %v", h)
	}
}

// TestCredentialLimit: each credential gets its own window, writes count twice, and the
// headers say where it stands.
func TestCredentialLimit(t *testing.T) {
	l := NewLimiter()
	h := CredentialLimit(l, func(*http.Request) string { return "192.0.2.1" })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	call := func(method string, p *auth.Principal) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/tickets", nil)
		if p != nil {
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	key := &auth.Principal{Kind: auth.KindAPIKey, APIKeyID: uuid.New()}
	if w := call("GET", key); w.Code != 204 || w.Header().Get("RateLimit-Limit") != "1200" || w.Header().Get("RateLimit-Remaining") != "1199" {
		t.Fatalf("first: %d %v", w.Code, w.Header())
	}
	for range credentialWritesPerMinute {
		call("POST", key)
	}
	if w := call("POST", key); w.Code != 429 || w.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("past the write limit: %d %v", w.Code, w.Header())
	}
	if w := call("GET", key); w.Code != 204 {
		t.Fatalf("reads stop with writes: %d", w.Code)
	}
	if w := call("POST", &auth.Principal{Kind: auth.KindAPIKey, APIKeyID: uuid.New()}); w.Code != 204 {
		t.Fatalf("another key shares the window: %d", w.Code)
	}
	if w := call("POST", nil); w.Code != 204 || w.Header().Get("RateLimit-Limit") != "" {
		t.Fatalf("anonymous: %d %v", w.Code, w.Header())
	}
	// Credentials that do not resolve are limited per address.
	bad := func() int {
		r := httptest.NewRequest("GET", "/v1/tickets", nil)
		r.Header.Set("Authorization", "Bearer od_live_wrong")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for range badCredentialsPerMinute {
		bad()
	}
	if code := bad(); code != 429 {
		t.Fatalf("guessing keys: %d", code)
	}
}
