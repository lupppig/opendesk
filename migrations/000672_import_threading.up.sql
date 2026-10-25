-- The threading phase reads, after the tickets, the headers of emails a source lists
-- without them (contracts/imports.md), and marks each email it has read.
ALTER TABLE imports DROP CONSTRAINT imports_phase_check;
ALTER TABLE imports ADD CONSTRAINT imports_phase_check
    CHECK (phase IN ('articles', 'tickets', 'threading', 'done'));
ALTER TABLE import_records ADD COLUMN threaded_at timestamptz;
CREATE INDEX import_records_unthreaded_idx ON import_records(workspace_id, source, account_key)
    WHERE kind = 'message' AND threaded_at IS NULL;
