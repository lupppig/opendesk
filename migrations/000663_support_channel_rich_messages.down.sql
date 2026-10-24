DROP TABLE IF EXISTS support_channel_messages;

ALTER TABLE support_channel_destinations
    DROP COLUMN IF EXISTS message_limit;
