package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/netsafe"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
	"github.com/useopendesk/opendesk/internal/push"
)

// Push to the iPhone app (contracts/native.md "Push"): hd.Notify enqueues a
// PushNotificationArgs in its transaction, reading notifications enqueues a badge
// update, and these workers deliver them directly over APNs or through the relay.

const (
	relayRegisterPerIP   = 30   // per hour
	relaySendPerIP       = 1200 // per minute
	relaySendPerInstance = 600  // per minute
	relayKeyFetches      = 10   // per instance per 10 minutes
	relayKeyTTL          = time.Hour
)

// pushSender holds what this instance needs to push: the relay client in relay mode,
// and, on a relay, the instance keys it has fetched.
type pushSender struct {
	relay *push.RelayClient
	keys  *instanceKeys
}

func (s *svc) newPushSender() *pushSender {
	ps := &pushSender{keys: &instanceKeys{m: map[string]cachedKey{}}}
	if s.d.Cfg.PushMode() == "relay" {
		ps.relay = &push.RelayClient{URL: s.d.Cfg.PushRelayURL, Instance: s.d.Cfg.APIURL, Key: s.instanceKey(),
			HTTP: &http.Client{Timeout: 15 * time.Second}}
	}
	return ps
}

// instanceKey is this instance's relay signing key, derived from AUTH_SECRET.
func (s *svc) instanceKey() ed25519.PrivateKey {
	k, err := push.InstanceKey(s.d.Cfg.AuthSecret)
	if err != nil {
		panic(err) // HKDF over SHA-256 never fails for a 32-byte output
	}
	return k
}

// enqueueBadge queues a badge update for a user with a phone, in tx.
func (s *svc) enqueueBadge(ctx context.Context, tx pgx.Tx, uid uuid.UUID) error {
	if s.d.Cfg.PushMode() == "off" {
		return nil
	}
	var phone bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_devices WHERE user_id = $1)`, uid).Scan(&phone); err != nil || !phone {
		return err
	}
	_, err := s.d.Jobs.InsertTx(ctx, tx, jobs.PushBadgeArgs{UserID: uid}, &river.InsertOpts{MaxAttempts: 2})
	return err
}

type pushDevice struct {
	id     uuid.UUID
	mode   string
	env    string
	sealed []byte
	muted  []string
}

// liveDevices are the user's phones whose sessions are still signed in.
func liveDevices(ctx context.Context, q db.Querier, uid uuid.UUID) ([]pushDevice, error) {
	rows, err := q.Query(ctx, `SELECT d.id, d.mode, d.environment, d.token_sealed, d.muted_types FROM `+liveDevice+`
		WHERE d.user_id = $1 LIMIT 50`, uid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (pushDevice, error) {
		var x pushDevice
		err := r.Scan(&x.id, &x.mode, &x.env, &x.sealed, &x.muted)
		return x, err
	})
}

// unreadBadge is the badge: unread notifications across the user's workspaces.
func unreadBadge(ctx context.Context, q db.Querier, uid uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM notifications n JOIN members m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
		WHERE n.user_id = $1 AND n.read_at IS NULL`, uid).Scan(&n)
	return n, err
}

// deliver sends p to one device. A token APNs dropped deletes the device. subtitle (the
// ticket subject) goes only into a push this instance sends to APNs itself.
func (s *svc) deliver(ctx context.Context, dev pushDevice, p push.Push, subtitle string) error {
	raw, err := s.d.Box.Open(dev.sealed)
	if err != nil {
		return err
	}
	switch {
	case dev.mode == "direct" && s.d.APNs != nil:
		payload, perr := push.Payload(p, s.d.Cfg.APIURL, subtitle)
		if perr != nil {
			return perr
		}
		err = s.d.APNs.Send(ctx, string(raw), dev.env, p, payload)
	case dev.mode == "relay" && s.push.relay != nil:
		err = s.push.relay.Send(ctx, string(raw), p)
	default:
		return nil // this instance no longer pushes that way
	}
	if errors.Is(err, push.ErrUnregistered) {
		_, err = s.d.DB.Pool.Exec(ctx, `DELETE FROM push_devices WHERE id = $1`, dev.id)
		return err
	}
	if err != nil {
		return err
	}
	_, err = s.d.DB.Pool.Exec(ctx, `UPDATE push_devices SET last_push_at = now() WHERE id = $1`, dev.id)
	return err
}

// deliverAll sends to every device. Sent pushes are not repeated on retry: a run fails
// only when no device could be reached.
func (s *svc) deliverAll(ctx context.Context, devs []pushDevice, p push.Push, subtitle string) error {
	var firstErr error
	failed := 0
	for _, dev := range devs {
		if err := s.deliver(ctx, dev, p, subtitle); err != nil {
			s.d.Log.Warn("push delivery failed", "device_id", dev.id, "mode", dev.mode, "err", err)
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if failed == len(devs) {
		return firstErr
	}
	return nil
}

type pushNotificationWorker struct {
	river.WorkerDefaults[jobs.PushNotificationArgs]
	s *svc
}

func (w *pushNotificationWorker) Timeout(*river.Job[jobs.PushNotificationArgs]) time.Duration {
	return time.Minute
}

func (w *pushNotificationWorker) Work(ctx context.Context, job *river.Job[jobs.PushNotificationArgs]) error {
	a := job.Args
	q := w.s.d.DB.Pool
	var typ string
	var ticketID *uuid.UUID
	var readAt *time.Time
	var number *int64
	var subject *string
	// A notification without a ticket may name its own subject (a Collab conversation's).
	err := q.QueryRow(ctx, `SELECT n.type, n.read_at, t.id, t.number,
			COALESCE(t.subject, CASE WHEN n.ticket_id IS NULL THEN n.data->>'subject' END)
		FROM notifications n JOIN members m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
		LEFT JOIN tickets t ON t.id = n.ticket_id AND t.workspace_id = n.workspace_id AND t.deleted_at IS NULL
			AND `+domain.VisibleToUserSQL("t", 3)+`
		WHERE n.id = $1 AND n.workspace_id = $2 AND n.user_id = $3`, a.NotificationID, a.WorkspaceID, a.UserID).
		Scan(&typ, &readAt, &ticketID, &number, &subject)
	if db.IsNotFound(err) || (err == nil && readAt != nil) {
		return nil // gone, the member left, or already read
	}
	if err != nil {
		return err
	}
	// The phone channel of the member's preferences, then each phone's own mutes.
	ch, err := notifyprefs.Channel(ctx, q, a.WorkspaceID, a.UserID, typ)
	if err != nil || !ch.Push {
		return err
	}
	set, err := notifyprefs.LoadSettings(ctx, q, a.UserID)
	if err != nil || set.Paused(time.Now()) {
		return err
	}
	devs, err := liveDevices(ctx, q, a.UserID)
	if err != nil {
		return err
	}
	devs = slices.DeleteFunc(devs, func(d pushDevice) bool { return slices.Contains(d.muted, typ) })
	if len(devs) == 0 {
		return nil
	}
	badge, err := unreadBadge(ctx, q, a.UserID)
	if err != nil {
		return err
	}
	nid, wid := a.NotificationID, a.WorkspaceID
	p := push.Push{Kind: push.KindAlert, Type: typ, NotificationID: &nid, WorkspaceID: &wid, TicketID: ticketID,
		TicketNumber: number, Badge: badge}
	sub := ""
	if subject != nil {
		sub = *subject
	}
	return w.s.deliverAll(ctx, devs, p, sub)
}

type pushBadgeWorker struct {
	river.WorkerDefaults[jobs.PushBadgeArgs]
	s *svc
}

func (w *pushBadgeWorker) Timeout(*river.Job[jobs.PushBadgeArgs]) time.Duration { return time.Minute }

func (w *pushBadgeWorker) Work(ctx context.Context, job *river.Job[jobs.PushBadgeArgs]) error {
	q := w.s.d.DB.Pool
	devs, err := liveDevices(ctx, q, job.Args.UserID)
	if err != nil || len(devs) == 0 {
		return err
	}
	badge, err := unreadBadge(ctx, q, job.Args.UserID)
	if err != nil {
		return err
	}
	return w.s.deliverAll(ctx, devs, push.Push{Kind: push.KindBadge, Badge: badge}, "")
}

// instanceKeys caches instance public keys on the relay for an hour.
type instanceKeys struct {
	mu sync.Mutex
	m  map[string]cachedKey
}

type cachedKey struct {
	key ed25519.PublicKey
	at  time.Time
}

// relayInstanceKey returns an instance's public key, fetched over HTTPS from public
// addresses only (netsafe) and cached for an hour.
func (s *svc) relayInstanceKey(ctx context.Context, instance string) (ed25519.PublicKey, error) {
	c := s.push.keys
	c.mu.Lock()
	k, ok := c.m[instance]
	c.mu.Unlock()
	if ok && time.Since(k.at) < relayKeyTTL {
		return k.key, nil
	}
	if !s.d.Limiter.Allow("pushrelay:fetch:"+instance, relayKeyFetches, 10*time.Minute) {
		return nil, httpx.RateLimited()
	}
	g := netsafe.Guard{Dev: s.devURL}
	u, err := g.ValidateURL(ctx, instance+"/v1/push/instance-key")
	if err != nil {
		return nil, errInstanceKey()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errInstanceKey()
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.hooks.Do(req)
	if err != nil {
		return nil, errInstanceKey()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errInstanceKey()
	}
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil {
		return nil, errInstanceKey()
	}
	pub, err := push.DecodeKey(body.PublicKey)
	if err != nil {
		return nil, errInstanceKey()
	}
	c.mu.Lock()
	if len(c.m) > 10000 {
		c.m = map[string]cachedKey{}
	}
	c.m[instance] = cachedKey{key: pub, at: time.Now()}
	c.mu.Unlock()
	return pub, nil
}

func errInstanceKey() *httpx.Error {
	return httpx.E(401, "invalid_signature", "The instance key could not be fetched from the instance.")
}

func errRelaySignature() *httpx.Error {
	return httpx.E(401, "invalid_signature", "The request is not signed by the instance this relay token belongs to.")
}

func (s *svc) relayServing() bool { return s.d.Cfg.PushRelayServe && s.d.APNs != nil }

func (s *svc) registerPush(api huma.API) {
	tags := []string{"Push"}
	relayOff := func() *httpx.Error {
		return httpx.E(404, "relay_off", "This server is not a push relay.")
	}

	register(s, api, huma.Operation{OperationID: "push-instance-key", Method: http.MethodGet, Path: "/v1/push/instance-key",
		Summary: "This instance's push relay signing key", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, _ *struct{}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         struct {
				PublicKey string `json:"public_key" doc:"Ed25519 public key, base64url"`
			}
		}, error) {
			out := &struct {
				CacheControl string `header:"Cache-Control"`
				Body         struct {
					PublicKey string `json:"public_key" doc:"Ed25519 public key, base64url"`
				}
			}{CacheControl: "public, max-age=3600"}
			out.Body.PublicKey = push.EncodeKey(s.instanceKey().Public().(ed25519.PublicKey))
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "push-relay-register", Method: http.MethodPost, Path: "/v1/push/relay/register",
		Summary: "Seal a phone's APNs token for one instance (push relay)", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Token       string `json:"token" maxLength:"200" doc:"APNs device token, hex"`
				Environment string `json:"environment" enum:"production,sandbox"`
				Instance    string `json:"instance" maxLength:"500" doc:"The instance's API URL"`
			}
		}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         struct {
				RelayToken string `json:"relay_token"`
			}
		}, error) {
			if !s.relayServing() {
				return nil, relayOff()
			}
			ri := requestInfo(ctx)
			if err := s.limit("pushrelay:register:"+ri.IP, relayRegisterPerIP, time.Hour); err != nil {
				return nil, err
			}
			tok, ok := push.NormalizeDeviceToken(in.Body.Token)
			if !ok {
				return nil, httpx.Validation("token must be an APNs device token in hex.")
			}
			instance, err := push.NormalizeInstance(in.Body.Instance, s.devURL)
			if err != nil {
				return nil, httpx.Validation("instance must be the server's https API URL.")
			}
			rt, err := push.SealRelayToken(s.d.Box, push.RelayToken{Token: tok, Environment: in.Body.Environment, Instance: instance})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				CacheControl string `header:"Cache-Control"`
				Body         struct {
					RelayToken string `json:"relay_token"`
				}
			}{CacheControl: "no-store"}
			out.Body.RelayToken = rt
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "push-relay-send", Method: http.MethodPost, Path: "/v1/push/relay/send",
		Summary: "Send a push for an instance (push relay)", Tags: tags, Security: []map[string][]string{},
		DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Instance  string `header:"OD-Push-Instance" maxLength:"500"`
			Timestamp string `header:"OD-Push-Timestamp" maxLength:"20"`
			Signature string `header:"OD-Push-Signature" maxLength:"200"`
			Body      push.RelaySend
			RawBody   []byte
		}) (*struct{}, error) {
			if !s.relayServing() {
				return nil, relayOff()
			}
			ri := requestInfo(ctx)
			if err := s.limit("pushrelay:send:"+ri.IP, relaySendPerIP, time.Minute); err != nil {
				return nil, err
			}
			instance, err := push.NormalizeInstance(in.Instance, s.devURL)
			if err != nil {
				return nil, errRelaySignature()
			}
			rt, err := push.OpenRelayToken(s.d.Box, in.Body.RelayToken)
			if err != nil || rt.Instance != instance {
				return nil, errRelaySignature()
			}
			pub, err := s.relayInstanceKey(ctx, instance)
			if err != nil {
				return nil, err
			}
			if err := push.Verify(pub, in.Timestamp, in.RawBody, in.Signature, time.Now()); err != nil {
				return nil, errRelaySignature()
			}
			if err := s.limit("pushrelay:instance:"+instance, relaySendPerInstance, time.Minute); err != nil {
				return nil, err
			}
			p := in.Body.Push
			if p.Kind == push.KindAlert && p.Type == "" {
				return nil, httpx.Validation("An alert needs its notification type.")
			}
			// The relay builds the payload itself, from the whitelisted fields, with no subtitle.
			payload, err := push.Payload(p, instance, "")
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.APNs.Send(ctx, rt.Token, rt.Environment, p, payload)
			if errors.Is(err, push.ErrUnregistered) {
				return nil, httpx.E(410, "gone", "APNs no longer accepts this device token.")
			}
			if err != nil {
				s.d.Log.Warn("push relay delivery failed", "err", err)
				return nil, httpx.E(502, "push_failed", "APNs did not accept the push. Try again later.")
			}
			return nil, nil
		})
}
