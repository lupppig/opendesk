-- The clock a person reads times in (contracts/api.md User.time_format): "auto" follows
-- the browser's language and region in the app and is 24-hour in emails; "12h" and "24h" set both.
ALTER TABLE users
    ADD COLUMN time_format text NOT NULL DEFAULT 'auto' CHECK (time_format IN ('auto', '12h', '24h'));
