ALTER TABLE support_channel_connections
    ADD COLUMN health_unhealthy_since timestamptz,
    ADD COLUMN health_alerted_at timestamptz;

UPDATE support_channel_connections
SET health_unhealthy_since = COALESCE(health_checked_at, updated_at)
WHERE health_status IN ('action_required', 'error');

CREATE INDEX support_channel_connections_health_alert_idx
    ON support_channel_connections(health_unhealthy_since)
    WHERE health_status IN ('action_required', 'error') AND health_alerted_at IS NULL;

CREATE TABLE support_channel_metric_buckets (
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    bucket_at timestamptz NOT NULL,
    webhook_rejections bigint NOT NULL DEFAULT 0 CHECK (webhook_rejections >= 0),
    duplicate_events bigint NOT NULL DEFAULT 0 CHECK (duplicate_events >= 0),
    PRIMARY KEY (connection_id, bucket_at)
);
