ALTER TABLE support_channel_connections DROP COLUMN IF EXISTS reconfigure;

ALTER TABLE support_channel_threads
    DROP COLUMN IF EXISTS history_posted_at,
    DROP COLUMN IF EXISTS history_processing_expires_at,
    DROP COLUMN IF EXISTS history_message_ids,
    DROP COLUMN IF EXISTS history_before,
    DROP COLUMN IF EXISTS history_event_seq,
    DROP COLUMN IF EXISTS card_processing_expires_at,
    DROP COLUMN IF EXISTS card_processing_seq,
    DROP COLUMN IF EXISTS card_event_seq;

DROP INDEX IF EXISTS support_channel_inbound_ticket_seq_uq;

ALTER TABLE support_channel_inbound_events
    DROP COLUMN IF EXISTS ticket_command_seq,
    DROP COLUMN IF EXISTS ticket_id;

DROP TABLE IF EXISTS support_channel_ticket_lanes;
