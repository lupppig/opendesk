-- Existing destinations keep their behaviour: automatic summaries spend AI usage,
-- so only destinations created from now on start with them on.
ALTER TABLE support_channel_destinations
    ADD COLUMN ollie_summary boolean NOT NULL DEFAULT false,
    ADD COLUMN ollie_warnings boolean NOT NULL DEFAULT true,
    ADD COLUMN ollie_personality text NOT NULL DEFAULT '' CHECK (char_length(ollie_personality) <= 500),
    ADD COLUMN ollie_length text NOT NULL DEFAULT 'standard' CHECK (ollie_length IN ('brief', 'standard', 'detailed'));

ALTER TABLE support_channel_destinations ALTER COLUMN ollie_summary SET DEFAULT true;

ALTER TABLE support_channel_threads
    ADD COLUMN summary_posted_at timestamptz,
    ADD COLUMN summary_processing_expires_at timestamptz,
    ADD COLUMN sla_warning text NOT NULL DEFAULT '',
    ADD COLUMN sentiment_warning text NOT NULL DEFAULT '';

CREATE TABLE support_channel_ollie_settings (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    actions text[] NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
