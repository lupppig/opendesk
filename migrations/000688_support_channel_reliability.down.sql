DROP TABLE support_channel_metric_buckets;

DROP INDEX support_channel_connections_health_alert_idx;

ALTER TABLE support_channel_connections
    DROP COLUMN health_alerted_at,
    DROP COLUMN health_unhealthy_since;
