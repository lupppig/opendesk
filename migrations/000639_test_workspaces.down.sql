DELETE FROM admin_audit_logs WHERE actor_type = 'system';
ALTER TABLE admin_audit_logs DROP CONSTRAINT IF EXISTS admin_audit_logs_actor_type_check;
ALTER TABLE admin_audit_logs ADD CONSTRAINT admin_audit_logs_actor_type_check CHECK (actor_type IN ('admin', 'cli'));

ALTER TABLE users
    DROP COLUMN IF EXISTS test_account,
    DROP COLUMN IF EXISTS password_expires_at;

DROP INDEX IF EXISTS workspaces_test_expiry_idx;
DROP INDEX IF EXISTS workspaces_test_idx;
ALTER TABLE workspaces
    DROP COLUMN IF EXISTS sample_data,
    DROP COLUMN IF EXISTS test_plan,
    DROP COLUMN IF EXISTS test_created_by,
    DROP COLUMN IF EXISTS test_reason,
    DROP COLUMN IF EXISTS test_expires_at,
    DROP COLUMN IF EXISTS test;
