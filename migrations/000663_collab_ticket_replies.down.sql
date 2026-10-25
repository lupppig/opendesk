DROP INDEX collab_threads_ticket_idx;
ALTER TABLE collab_threads DROP COLUMN ticket_id;
ALTER TABLE mailboxes DROP COLUMN personal_replies;
DROP INDEX messages_collab_account_idx;
ALTER TABLE messages DROP COLUMN collab_reply_to;
ALTER TABLE messages DROP COLUMN collab_account_id;
