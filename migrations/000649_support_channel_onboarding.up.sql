ALTER TABLE support_channel_connections
    ADD COLUMN health_status text NOT NULL DEFAULT 'unknown'
        CHECK (health_status IN ('unknown', 'healthy', 'action_required', 'error')),
    ADD COLUMN health_detail text NOT NULL DEFAULT '',
    ADD COLUMN health_checked_at timestamptz,
    ADD COLUMN credentials_updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN external_account_handle text NOT NULL DEFAULT '';

CREATE TABLE support_channel_discovered_destinations (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    external_channel_id text NOT NULL,
    external_channel_name text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT '',
    available boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, external_channel_id)
);

CREATE INDEX support_channel_discovered_destinations_workspace_idx
    ON support_channel_discovered_destinations(workspace_id, connection_id);
