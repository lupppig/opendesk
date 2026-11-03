DROP INDEX IF EXISTS collab_threads_snoozed_until_idx;
ALTER TABLE collab_threads DROP COLUMN IF EXISTS snoozed_until;
