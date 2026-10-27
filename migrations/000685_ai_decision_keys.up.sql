-- A workspace's own TypeSafe key for Jev decisions (contracts/ai.md, "Provider per
-- workspace"). One row per workspace; no row means decisions run on the workspace's own
-- OpenRouter key when it has one, else on the instance's engine.
CREATE TABLE ai_decision_keys (
    workspace_id  uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    -- The API key, sealed with d.Box. Only its last 4 characters are ever shown.
    key_enc       bytea NOT NULL,
    key_last4     text NOT NULL,
    -- status is what TypeSafe last said about the key; notified_at dedupes the admin
    -- notification when it fails.
    status        text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'rejected', 'no_funds')),
    status_at     timestamptz,
    notified_at   timestamptz,
    created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_decision_keys_created_by_idx ON ai_decision_keys (created_by);
