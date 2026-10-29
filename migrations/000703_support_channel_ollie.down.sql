DROP TABLE support_channel_ollie_settings;

ALTER TABLE support_channel_threads
    DROP COLUMN sentiment_warning,
    DROP COLUMN sla_warning,
    DROP COLUMN summary_processing_expires_at,
    DROP COLUMN summary_posted_at;

ALTER TABLE support_channel_destinations
    DROP COLUMN ollie_length,
    DROP COLUMN ollie_personality,
    DROP COLUMN ollie_warnings,
    DROP COLUMN ollie_summary;
