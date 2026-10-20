package collab

import (
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/ids"
)

// inlineImageTypes are the images a browser may show inline from an attachment link.
// Anything else (SVG included, which can carry script) downloads.
var inlineImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp"}

// attachmentHandler serves GET /v1/collab/attachments/{message_id}/{index}: one file of a
// stored message, authenticated by the link's signature alone (it works in <img>). A wrong
// or expired signature, or anything missing, is 404.
func (s *service) attachmentHandler(w http.ResponseWriter, r *http.Request) {
	mid, ok := ids.Parse(chi.URLParam(r, "message_id"))
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	q := r.URL.Query()
	if !ok || err != nil || index < 0 || index > 9999 || !s.verifyAttachmentSig(mid, index, q.Get("exp"), q.Get("sig")) {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	m, err := scanMessage(s.d.DB.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.id = $1`, mid))
	if err != nil {
		if !db.IsNotFound(err) {
			s.log.Error("collab attachment message", "message_id", mid, "err", err)
		}
		http.NotFound(w, r)
		return
	}
	at := slices.IndexFunc(m.Attachments, func(a storedAttachment) bool { return a.Index == index })
	if at < 0 {
		http.NotFound(w, r)
		return
	}
	stored := m.Attachments[at]
	a, err := loadAccountByID(ctx, s.d.DB.Pool, m.AccountID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// A link stops working with the workspace's access to Collab, not only when it expires.
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, a.WorkspaceID); err != nil || suspended {
		http.NotFound(w, r)
		return
	}
	if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, a.WorkspaceID); err != nil {
		http.NotFound(w, r)
		return
	}
	var threadRemoteID string
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT remote_id FROM collab_threads WHERE id = $1`, m.ThreadID).Scan(&threadRemoteID); err != nil {
		http.NotFound(w, r)
		return
	}
	raw, err := s.loadRaw(ctx, a, m, threadRemoteID)
	if err != nil {
		s.log.Warn("collab attachment source", "message_id", mid, "err", err)
		http.Error(w, "The file could not be read from the mail provider. Try again.", http.StatusBadGateway)
		return
	}
	pm, err := email.ParseMail(raw)
	if err != nil || index >= len(pm.Parts) {
		http.NotFound(w, r)
		return
	}
	part := pm.Parts[index]

	ct := strings.ToLower(strings.TrimSpace(stored.ContentType))
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		ct = mt
	} else {
		ct = "application/octet-stream"
	}
	name := stored.Filename
	if name == "" {
		name = email.CleanFilename(part.FileName, ct)
	}
	disposition := "attachment"
	if slices.Contains(inlineImageTypes, ct) && q.Get("inline") == "1" {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	h.Set("Content-Length", strconv.Itoa(len(part.Content)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(part.Content)
	}
}
