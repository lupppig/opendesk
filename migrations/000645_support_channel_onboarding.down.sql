DROP TABLE support_channel_discovered_destinations;

ALTER TABLE support_channel_connections
    DROP COLUMN external_account_handle,
    DROP COLUMN credentials_updated_at,
    DROP COLUMN health_checked_at,
    DROP COLUMN health_detail,
    DROP COLUMN health_status;
