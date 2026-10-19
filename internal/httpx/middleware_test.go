package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
