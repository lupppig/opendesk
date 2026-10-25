ALTER TABLE support_channel_destinations
    ADD COLUMN message_limit integer NOT NULL DEFAULT 3500
    CHECK (message_limit BETWEEN 500 AND 3500);

CREATE TABLE support_channel_messages (
    destination_id uuid NOT NULL REFERENCES support_channel_destinations(id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('mirror', 'source')),
    external_message_id text NOT NULL,
    external_user_id text NOT NULL DEFAULT '',
    source_text text NOT NULL DEFAULT '',
    removed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (destination_id, message_id, kind),
    UNIQUE (destination_id, external_message_id)
);

CREATE INDEX support_channel_messages_message_idx
    ON support_channel_messages(message_id);

-- Existing Telegram webhooks need edited_message added to allowed_updates.
UPDATE support_channel_connections
SET reconfigure = true, updated_at = now()
WHERE provider = 'telegram';
