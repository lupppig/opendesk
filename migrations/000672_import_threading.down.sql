-- An import in its threading phase has its tickets in: it ends there.
DROP INDEX import_records_unthreaded_idx;
ALTER TABLE import_records DROP COLUMN threaded_at;
UPDATE imports SET phase = 'done' WHERE phase = 'threading';
ALTER TABLE imports DROP CONSTRAINT imports_phase_check;
ALTER TABLE imports ADD CONSTRAINT imports_phase_check
    CHECK (phase IN ('articles', 'tickets', 'done'));
