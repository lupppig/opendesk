package app

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/useopendesk/opendesk/internal/config"
)

// securityTxtTTL is how far ahead a served security.txt expires (RFC 9116 asks for less
// than a year); it is written at each request, so it never goes stale.
const securityTxtTTL = 180 * 24 * time.Hour

// mountSecurityTxt serves /.well-known/security.txt (RFC 9116) with the instance's
// security contact: opendesk's on the Cloud, SECURITY_CONTACT on a self-hosted instance.
// Without a contact there is none to give, and the path is not served.
func mountSecurityTxt(r chi.Router, cfg *config.Config) {
	if cfg.SecurityContact == "" {
		return
	}
	r.Get("/.well-known/security.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte(securityTxt(cfg, time.Now())))
	})
}

func securityTxt(cfg *config.Config, now time.Time) string {
	var b strings.Builder
	b.WriteString("Contact: " + cfg.SecurityContact + "\n")
	b.WriteString("Expires: " + now.Add(securityTxtTTL).UTC().Truncate(24*time.Hour).Format(time.RFC3339) + "\n")
	b.WriteString("Preferred-Languages: en\n")
	if cfg.SecurityPolicy != "" {
		b.WriteString("Policy: " + cfg.SecurityPolicy + "\n")
	}
	return b.String()
}
