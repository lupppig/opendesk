package supportchannels

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

const subscriberRefresh = 15 * time.Second

// runSubscribers owns optional long-lived provider event streams. A renewable
// database lease gives each connection one owner when more than one worker is
// running; webhook-only adapters never enter this path.
func (s *Service) runSubscribers(ctx context.Context) {
	running := map[uuid.UUID]context.CancelFunc{}
	ticker := time.NewTicker(subscriberRefresh)
	defer ticker.Stop()
	defer func() {
		for id, cancel := range running {
			cancel()
			s.releaseSubscriber(id)
		}
	}()
	for {
		active, err := s.subscriberConnections(ctx)
		if err != nil {
			s.d.Log.Warn("support channel subscribers", "err", err)
		} else {
			for id, cancel := range running {
				if _, ok := active[id]; !ok {
					cancel()
					s.releaseSubscriber(id)
					delete(running, id)
				}
			}
			for id, c := range active {
				claimed, claimErr := s.claimSubscriber(ctx, id)
				if claimErr != nil {
					s.d.Log.Warn("support channel subscriber lease", "connection_id", id, "err", claimErr)
					continue
				}
				if !claimed {
					if cancel, ok := running[id]; ok {
						cancel()
						delete(running, id)
					}
					continue
				}
				if _, ok := running[id]; ok {
					continue
				}
				runCtx, cancel := context.WithCancel(ctx)
				running[id] = cancel
				go s.runSubscriber(runCtx, c)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) subscriberConnections(ctx context.Context) (map[uuid.UUID]connection, error) {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT id,workspace_id,provider,name,external_account_id,external_account_name,credentials_enc,status,last_error,created_at
		FROM support_channel_connections`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (connection, error) {
		var c connection
		err := r.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.Name, &c.ExternalAccountID, &c.ExternalAccountName, &c.CredentialsEnc, &c.Status, &c.LastError, &c.CreatedAt)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]connection{}
	for _, c := range list {
		if a, ok := s.registry.Get(c.Provider); ok {
			if _, ok := a.(channel.Subscriber); ok {
				out[c.ID] = c
			}
		}
	}
	return out, nil
}

func (s *Service) runSubscriber(ctx context.Context, c connection) {
	a, ok := s.registry.Get(c.Provider)
	if !ok {
		return
	}
	subscriber, ok := a.(channel.Subscriber)
	if !ok {
		return
	}
	credentials, err := s.credentials(c)
	if err != nil {
		return
	}
	failures := 0
	for ctx.Err() == nil {
		var connected atomic.Bool
		err = subscriber.Subscribe(ctx, s.http, credentials, channel.Stream{
			Emit: func(eventCtx context.Context, events []channel.Inbound) error {
				return s.persistInbound(eventCtx, c, events)
			},
			Connected: func() {
				connected.Store(true)
				s.subscriberConnected(ctx, c.ID)
			},
		})
		if ctx.Err() != nil {
			return
		}
		var fatal *channel.FatalError
		if errors.As(err, &fatal) {
			// The provider refused the credentials or settings: stop until an admin
			// connects it again, instead of retrying forever.
			s.d.Log.Warn("support channel subscriber stopped", "provider", c.Provider, "connection_id", c.ID, "reason", fatal.Reason)
			_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET status='error',last_error=$2,updated_at=now() WHERE id=$1`, c.ID, fatal.Reason)
			return
		}
		if connected.Load() {
			failures = 0
		}
		delay := subscriberBackoff(failures)
		failures++
		s.d.Log.Warn("support channel subscriber disconnected", "provider", c.Provider, "connection_id", c.ID, "err", err, "retry_in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

const (
	subscriberRetryBase = 5 * time.Second
	subscriberRetryMax  = 5 * time.Minute
)

// subscriberBackoff is the wait before reconnect attempt n (0-based): doubling
// from five seconds up to five minutes, with jitter over its upper half so
// workers that lost the provider together don't reconnect together.
func subscriberBackoff(n int) time.Duration {
	d := subscriberRetryMax
	if n < 7 {
		d = min(subscriberRetryBase<<n, subscriberRetryMax)
	}
	return d/2 + rand.N(d/2+1)
}

// subscriberConnected marks a connection connected once its provider accepts the
// session. A delivery error stays until a delivery succeeds.
func (s *Service) subscriberConnected(ctx context.Context, id uuid.UUID) {
	_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE support_channel_connections SET status='connected',last_error='',updated_at=now() WHERE id=$1 AND (status<>'connected' OR last_error<>'') AND last_error<>$2`, id, deliveryError)
}

func (s *Service) claimSubscriber(ctx context.Context, id uuid.UUID) (bool, error) {
	var claimed bool
	err := s.d.DB.Pool.QueryRow(ctx, `INSERT INTO support_channel_subscriber_leases(connection_id,holder,expires_at)
		VALUES($1,$2,$3) ON CONFLICT(connection_id) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at
		WHERE support_channel_subscriber_leases.holder=EXCLUDED.holder OR support_channel_subscriber_leases.expires_at<now()
		RETURNING true`, id, s.subscriberID, time.Now().Add(2*subscriberRefresh+5*time.Second)).Scan(&claimed)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return claimed, err
}

func (s *Service) releaseSubscriber(id uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.d.DB.Pool.Exec(ctx, `DELETE FROM support_channel_subscriber_leases WHERE connection_id=$1 AND holder=$2`, id, s.subscriberID)
}
