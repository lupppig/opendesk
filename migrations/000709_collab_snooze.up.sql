ALTER TABLE collab_threads ADD COLUMN snoozed_until timestamptz;
CREATE INDEX collab_threads_snoozed_until_idx ON collab_threads(snoozed_until) WHERE snoozed_until IS NOT NULL;
