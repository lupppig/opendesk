DROP INDEX IF EXISTS messages_thread_idx;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_thread_note;
ALTER TABLE messages DROP COLUMN IF EXISTS thread_id;
DROP TABLE IF EXISTS ticket_threads;
