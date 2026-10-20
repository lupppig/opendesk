CREATE TABLE support_channel_connections (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider text NOT NULL,
    name text NOT NULL,
    external_account_id text NOT NULL,
    external_account_name text NOT NULL DEFAULT '',
    credentials_enc bytea NOT NULL,
    status text NOT NULL DEFAULT 'connected' CHECK (status IN ('connected', 'error')),
    last_error text NOT NULL DEFAULT '',
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, provider, external_account_id)
);

CREATE TABLE support_channel_destinations (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    team_id uuid REFERENCES teams(id) ON DELETE CASCADE,
    external_channel_id text NOT NULL,
    external_channel_name text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (connection_id, external_channel_id),
    UNIQUE (connection_id, team_id)
);

CREATE TABLE support_channel_threads (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    destination_id uuid NOT NULL REFERENCES support_channel_destinations(id) ON DELETE CASCADE,
    ticket_id uuid NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    -- Where the provider delivers messages written in the thread: the destination's
    -- channel, or the thread's own channel id on providers whose threads are channels.
    external_channel_id text,
    external_thread_id text,
    external_root_message_id text,
    last_event_seq bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (destination_id, ticket_id),
    -- Thread ids are only unique within a channel on some providers (Telegram topics).
    UNIQUE (connection_id, external_channel_id, external_thread_id)
);

CREATE TABLE support_channel_deliveries (
    destination_id uuid NOT NULL REFERENCES support_channel_destinations(id) ON DELETE CASCADE,
    event_seq bigint NOT NULL,
    attempts integer NOT NULL DEFAULT 1,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (destination_id, event_seq)
);

CREATE TABLE support_channel_identities (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    external_user_id text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (connection_id, external_user_id),
    UNIQUE (connection_id, user_id)
);

CREATE TABLE support_channel_link_codes (
    code_hash bytea PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE support_channel_inbound_events (
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    external_event_id text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    PRIMARY KEY (connection_id, external_event_id)
);

CREATE INDEX support_channel_destinations_workspace_idx ON support_channel_destinations(workspace_id);
CREATE UNIQUE INDEX support_channel_destinations_fallback_idx ON support_channel_destinations(connection_id) WHERE team_id IS NULL;
CREATE INDEX support_channel_threads_ticket_idx ON support_channel_threads(workspace_id, ticket_id);
CREATE INDEX support_channel_link_codes_expiry_idx ON support_channel_link_codes(expires_at);
