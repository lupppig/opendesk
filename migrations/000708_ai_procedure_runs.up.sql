-- Procedures an automation had Ollie run on a ticket (contracts/ai.md "Automations"): one row per
-- automation, ticket and the message or call the trigger was about, so a retried or replayed
-- job never runs twice, and the workspace's hourly runs are counted.
CREATE TABLE ai_procedure_runs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    ticket_id      uuid NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    automation_id  uuid NOT NULL,
    procedure_id   uuid NOT NULL,
    -- The trigger's message or call id, '' when it was about neither.
    trigger_key    text NOT NULL DEFAULT '',
    outcome        text NOT NULL DEFAULT 'running' CHECK (outcome IN ('running', 'done', 'nothing_to_do', 'failed', 'skipped')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, automation_id, ticket_id, trigger_key)
);
CREATE INDEX ai_procedure_runs_recent_idx ON ai_procedure_runs (workspace_id, created_at);
