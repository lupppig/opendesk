-- Test workspaces (contracts/admin.md "Test workspaces"): workspaces an instance admin
-- creates for demos and testing, optionally filled with sample data, and deleted by the
-- hourly cleanup once test_expires_at passes (NULL keeps them).
ALTER TABLE workspaces
    ADD COLUMN test            boolean NOT NULL DEFAULT false,
    ADD COLUMN test_expires_at timestamptz,
    ADD COLUMN test_reason     text,
    ADD COLUMN test_created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    -- The plan asked for at creation: free, pro or collab (applied through limits and
    -- Collab access, never Stripe).
    ADD COLUMN test_plan       text,
    -- {status: none|running|done|failed, size, kinds, counts, batches, started_at, seeded_at, error}
    ADD COLUMN sample_data     jsonb NOT NULL DEFAULT '{}';
CREATE INDEX workspaces_test_idx ON workspaces(created_at, id) WHERE test;
CREATE INDEX workspaces_test_expiry_idx ON workspaces(test_expires_at) WHERE test AND test_expires_at IS NOT NULL;

-- A test account's generated password stops working at password_expires_at; an expired
-- password reads as no password at sign-in.
ALTER TABLE users
    ADD COLUMN password_expires_at timestamptz,
    ADD COLUMN test_account        boolean NOT NULL DEFAULT false;

-- The cleanup of expired test workspaces writes the admin audit log as the system.
ALTER TABLE admin_audit_logs DROP CONSTRAINT IF EXISTS admin_audit_logs_actor_type_check;
ALTER TABLE admin_audit_logs ADD CONSTRAINT admin_audit_logs_actor_type_check CHECK (actor_type IN ('admin', 'cli', 'system'));
