ALTER TABLE webhooks
    DROP COLUMN IF EXISTS verified_at,
    DROP COLUMN IF EXISTS verify_code_hash,
    DROP COLUMN IF EXISTS verify_sent_at;
