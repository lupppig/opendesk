package phone

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/phone/provider"
)

const (
	maxWebhookBody = 1 << 20
	// webhookPerMinute bounds what one connection's URL takes in once verified: a call is a
	// handful of events, so this is far above a busy line and keeps a flood from filling
	// the queue.
	webhookPerMinute = 600
	// badHooksPerMinute bounds the requests one address sends that name no connection or
	// fail verification, which a provider never does: past it, that address is refused
	// before any work. They count apart from the connection's limit, so they cannot use it
	// up and keep the provider's own events out.
	badHooksPerMinute = 30
	// eventRetention is how long an applied event's id is kept to recognize the provider's
	// retries (the prune job), so an event older than that is dropped: once its id is gone
	// a replay could not be told from a new event.
	eventRetention = 7 * 24 * time.Hour
)

// webhook receives a provider's events. The connection is found by its opaque id and
// the event is verified with the webhook's own secret before anything is read from it:
// in the body as the provider signs it, or, for a webhook an admin added by hand
// (Descriptor.ManualWebhook), as the URL's last segment. Nothing in the payload decides
// which workspace or ticket it reaches. A verified event
// is stored (normalized) and queued in one transaction, then the provider gets its 200
// at once: Aircall waits five seconds and disables a webhook that keeps failing.
func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	// Limits are keyed by address and by known connection only, so requests naming random
	// ids add no keys of their own.
	bad := "phone-hook-bad:" + httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r)
	// A webhook an admin added by hand (its secret in the path) keeps posting from the
	// provider's shared addresses after a rotation or a disconnect, until the admin removes
	// it there: its address's failures never hold back another connection's verified events,
	// so such a request is always checked, and refused with 429 only once it fails.
	pathSecret := chi.URLParam(r, "secret")
	if pathSecret == "" && s.d.Limiter.Over(bad, badHooksPerMinute, time.Minute) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	refuse := func(status int) {
		if !s.d.Limiter.Allow(bad, badHooksPerMinute, time.Minute) {
			status = http.StatusTooManyRequests
		}
		w.WriteHeader(status)
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		refuse(http.StatusNotFound)
		return
	}
	c, err := scanConnection(s.d.DB.Pool.QueryRow(r.Context(), connectionSelect+` WHERE id = $1`, id))
	if err != nil && !db.IsNotFound(err) {
		s.d.Log.Error("phone webhook connection", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	a, ok := s.adapters[c.Provider]
	if err != nil || !ok || c.Provider != chi.URLParam(r, "provider") || len(c.webhookSecretEnc) == 0 {
		refuse(http.StatusNotFound)
		return
	}
	manual := a.Describe().ManualWebhook
	if pathSecret != "" && !manual {
		refuse(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		refuse(http.StatusRequestEntityTooLarge)
		return
	}
	secret, err := s.d.Box.Open(c.webhookSecretEnc)
	if err != nil {
		s.d.Log.Error("phone webhook secret", "connection_id", c.ID, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if manual && subtle.ConstantTimeCompare([]byte(pathSecret), secret) != 1 || !manual && a.Verify(body, string(secret)) != nil {
		refuse(http.StatusUnauthorized)
		return
	}
	if !s.d.Limiter.Allow("phone-hook:"+c.ID.String(), webhookPerMinute, time.Minute) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	ev, err := a.Parse(body)
	if errors.Is(err, provider.ErrIgnored) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		// Verified but not understood: acknowledged, so the provider keeps the webhook on.
		s.d.Log.Warn("phone event not understood", "provider", c.Provider, "connection_id", c.ID, "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	if time.Since(ev.At) > eventRetention {
		// Acknowledged like an ignored event, and dropped.
		s.d.Log.Info("phone event too old", "provider", c.Provider, "connection_id", c.ID, "event", ev.Type)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.persist(r, c, ev); err != nil {
		s.d.Log.Error("phone event not stored", "connection_id", c.ID, "event", ev.Type, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) persist(r *http.Request, c PhoneConnection, ev provider.Event) error {
	ctx := r.Context()
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO phone_inbound_events (connection_id, external_event_id, workspace_id, payload)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, c.ID, ev.ID, c.WorkspaceID, raw)
		if err != nil || tag.RowsAffected() == 0 {
			return err // a retry of an event already stored
		}
		if _, err := tx.Exec(ctx, `UPDATE phone_connections SET last_event_at = now() WHERE id = $1`, c.ID); err != nil {
			return err
		}
		// A webhook an admin added by hand works once its first event came in.
		if c.HealthStatus == "action_required" && s.describe(c.Provider).ManualWebhook {
			now := time.Now().UTC()
			c.LastEventAt = &now
			if _, err := s.storeHealth(ctx, tx, c, "healthy", HealthReceiving); err != nil {
				return err
			}
		}
		_, err = s.d.Jobs.InsertTx(ctx, tx, jobs.PhoneInboundArgs{WorkspaceID: c.WorkspaceID, ConnectionID: c.ID, ExternalEventID: ev.ID},
			&river.InsertOpts{MaxAttempts: 10})
		return err
	})
}
