DROP INDEX IF EXISTS collab_threads_addresses_idx;
ALTER TABLE collab_threads DROP COLUMN IF EXISTS addresses;
ALTER TABLE collab_messages DROP COLUMN IF EXISTS delivered_to;
DROP TABLE IF EXISTS collab_identities;
