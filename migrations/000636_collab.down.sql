DROP TABLE IF EXISTS collab_storage_trash;
DROP TABLE IF EXISTS collab_sidebar_prefs;
DROP TABLE IF EXISTS collab_comments;
DROP TABLE IF EXISTS collab_assignees;
DROP TABLE IF EXISTS collab_shares;
DROP TABLE IF EXISTS collab_message_folders;
DROP TABLE IF EXISTS collab_messages;
DROP TABLE IF EXISTS collab_threads;
DROP TABLE IF EXISTS collab_folders;
DROP TABLE IF EXISTS collab_accounts;

ALTER TABLE events DROP COLUMN IF EXISTS user_ids;

ALTER TABLE subscriptions
    DROP COLUMN IF EXISTS collab_note,
    DROP COLUMN IF EXISTS collab_until,
    DROP COLUMN IF EXISTS collab_access;
UPDATE subscriptions SET plan = 'pro' WHERE plan = 'collab';
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_plan_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_plan_check CHECK (plan IN ('free', 'pro'));
