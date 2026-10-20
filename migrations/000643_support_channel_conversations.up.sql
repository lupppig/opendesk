CREATE TABLE support_channel_conversations (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    external_thread_id text NOT NULL,
    external_user_id text NOT NULL,
    turns jsonb NOT NULL DEFAULT '[]',
    pending_action jsonb,
    pending_expires_at timestamptz,
    processing_id uuid,
    processing_expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, external_thread_id, external_user_id)
);

CREATE INDEX support_channel_conversations_workspace_idx
    ON support_channel_conversations(workspace_id, updated_at);

CREATE TABLE support_channel_subscriber_leases (
    connection_id uuid PRIMARY KEY REFERENCES support_channel_connections(id) ON DELETE CASCADE,
    holder uuid NOT NULL,
    expires_at timestamptz NOT NULL
);
