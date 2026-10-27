package supportchannels

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/jobs"
)

const (
	NotifySupportChannelUnhealthy = "support_channel_unhealthy"
	healthAlertAfter              = 15 * time.Minute
	operationsWindow              = 24 * time.Hour
	metricsRetention              = 30 * 24 * time.Hour
)

type healthAlertWorker struct {
	river.WorkerDefaults[jobs.SupportChannelHealthAlertArgs]
	s *Service
}

func (w *healthAlertWorker) Work(ctx context.Context, _ *river.Job[jobs.SupportChannelHealthAlertArgs]) error {
	return w.s.alertUnhealthyConnections(ctx)
}

func (s *Service) alertUnhealthyConnections(ctx context.Context) error {
	if _, err := s.d.DB.Pool.Exec(ctx, `DELETE FROM support_channel_metric_buckets WHERE bucket_at < $1`, time.Now().Add(-metricsRetention)); err != nil {
		return err
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT id FROM support_channel_connections
		WHERE health_status IN ('action_required','error') AND health_alerted_at IS NULL
		AND health_unhealthy_since <= $1
		ORDER BY health_unhealthy_since LIMIT 100`, time.Now().Add(-healthAlertAfter))
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.alertUnhealthyConnection(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) alertUnhealthyConnection(ctx context.Context, id uuid.UUID) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var c connection
		err := tx.QueryRow(ctx, connectionSelect+` WHERE id=$1 FOR UPDATE`, id).Scan(connectionScan(&c)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if c.HealthStatus == "healthy" || c.HealthStatus == "unknown" || c.HealthUnhealthySince == nil || c.HealthAlertedAt != nil || c.HealthUnhealthySince.After(time.Now().Add(-healthAlertAfter)) {
			return nil
		}
		at := time.Now().UTC()
		if _, err := tx.Exec(ctx, `UPDATE support_channel_connections SET health_alerted_at=$2 WHERE id=$1`, c.ID, at); err != nil {
			return err
		}
		c.HealthAlertedAt = &at
		admins, err := tx.Query(ctx, `SELECT user_id FROM members WHERE workspace_id=$1 AND role IN ('owner','admin') ORDER BY user_id`, c.WorkspaceID)
		if err != nil {
			return err
		}
		userIDs, err := pgx.CollectRows(admins, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, userID := range userIDs {
			if err := hd.Notify(ctx, tx, c.WorkspaceID, userID, NotifySupportChannelUnhealthy, nil, map[string]any{
				"connection_id": c.ID,
				"name":          c.Name,
				"provider":      c.Provider,
				"status":        c.HealthStatus,
				"detail":        c.HealthDetail,
			}); err != nil {
				return err
			}
		}
		return s.publishConnection(ctx, tx, c)
	})
}

func (s *Service) recordWebhookRejection(ctx context.Context, connectionID uuid.UUID) {
	_, err := s.d.DB.Pool.Exec(ctx, `INSERT INTO support_channel_metric_buckets(connection_id,bucket_at,webhook_rejections)
		VALUES($1,date_trunc('hour',now()),1)
		ON CONFLICT(connection_id,bucket_at) DO UPDATE
		SET webhook_rejections=support_channel_metric_buckets.webhook_rejections+1`, connectionID)
	if err != nil {
		s.d.Log.Warn("support channel webhook metric", "connection_id", connectionID, "err", err)
	}
}

func (s *Service) recordDuplicateEvents(ctx context.Context, connectionID uuid.UUID, count int64) {
	if count == 0 {
		return
	}
	_, err := s.d.DB.Pool.Exec(ctx, `INSERT INTO support_channel_metric_buckets(connection_id,bucket_at,duplicate_events)
		VALUES($1,date_trunc('hour',now()),$2)
		ON CONFLICT(connection_id,bucket_at) DO UPDATE
		SET duplicate_events=support_channel_metric_buckets.duplicate_events+EXCLUDED.duplicate_events`, connectionID, count)
	if err != nil {
		s.d.Log.Warn("support channel duplicate metric", "connection_id", connectionID, "err", err)
	}
}

func (s *Service) loadOperations(ctx context.Context, wid uuid.UUID) (operations, error) {
	now := time.Now().UTC()
	out := operations{WindowStartedAt: now.Add(-operationsWindow), GeneratedAt: now}
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE health_status='healthy'),
		count(*) FILTER (WHERE health_status IN ('action_required','error')),
		count(*) FILTER (WHERE health_alerted_at IS NOT NULL)
		FROM support_channel_connections WHERE workspace_id=$1`, wid).
		Scan(&out.Connections.Total, &out.Connections.Healthy, &out.Connections.Unhealthy, &out.Connections.Alerted); err != nil {
		return out, err
	}
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE d.status='queued'),
		count(*) FILTER (WHERE d.status='retrying'),
		count(*) FILTER (WHERE d.status='delivered'),
		count(*) FILTER (WHERE d.status='failed'),
		COALESCE(round(avg(extract(epoch FROM (d.delivered_at-d.created_at))*1000) FILTER (WHERE d.status='delivered')),0)::bigint,
		COALESCE(round(max(extract(epoch FROM (d.delivered_at-d.created_at))*1000) FILTER (WHERE d.status='delivered')),0)::bigint
		FROM support_channel_dispatches d
		JOIN events e ON e.workspace_id=d.workspace_id AND e.seq=d.event_seq
		LEFT JOIN teams tm ON tm.id=e.team_id AND tm.workspace_id=e.workspace_id
		WHERE d.workspace_id=$1 AND d.created_at >= $2 AND NOT COALESCE(tm.private,false)`, wid, out.WindowStartedAt).
		Scan(&out.Deliveries.Queued, &out.Deliveries.Retrying, &out.Deliveries.Delivered, &out.Deliveries.Failed, &out.Deliveries.AverageLatencyMS, &out.Deliveries.MaximumLatencyMS); err != nil {
		return out, err
	}
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT min(d.created_at) FROM support_channel_dispatches d
		JOIN events e ON e.workspace_id=d.workspace_id AND e.seq=d.event_seq
		LEFT JOIN teams tm ON tm.id=e.team_id AND tm.workspace_id=e.workspace_id
		WHERE d.workspace_id=$1 AND d.status IN ('queued','retrying') AND NOT COALESCE(tm.private,false)`, wid).
		Scan(&out.Deliveries.OldestPendingAt); err != nil {
		return out, err
	}
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT COALESCE(sum(m.webhook_rejections),0),COALESCE(sum(m.duplicate_events),0)
		FROM support_channel_metric_buckets m JOIN support_channel_connections c ON c.id=m.connection_id
		WHERE c.workspace_id=$1 AND m.bucket_at >= $2`, wid, out.WindowStartedAt).
		Scan(&out.Webhooks.Rejected, &out.Webhooks.DuplicatesSuppressed); err != nil {
		return out, err
	}
	return out, nil
}
