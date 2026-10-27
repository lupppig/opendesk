-- Online time for the agents report (contracts/api.md "Agents report"): stretches of time a
-- member had the app open with their availability set to online. A heartbeat from the app
-- extends the member's open stretch, or starts one after a gap; a change of availability
-- ends it. Only online time is kept, and for 400 days (core.cleanup).
CREATE TABLE agent_presence (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    user_id      uuid NOT NULL,
    started_at   timestamptz NOT NULL DEFAULT now(),
    ended_at     timestamptz NOT NULL DEFAULT now(),
    open         boolean NOT NULL DEFAULT true,
    FOREIGN KEY (workspace_id, user_id) REFERENCES members(workspace_id, user_id) ON DELETE CASCADE,
    CHECK (ended_at >= started_at)
);
-- One open stretch per member: the one heartbeats extend.
CREATE UNIQUE INDEX agent_presence_open_uq ON agent_presence(workspace_id, user_id) WHERE open;
-- The member foreign key.
CREATE INDEX agent_presence_member_idx ON agent_presence(workspace_id, user_id, started_at);
-- The report reads a workspace's stretches that end after the start of its range.
CREATE INDEX agent_presence_ws_ended_idx ON agent_presence(workspace_id, ended_at);
-- Retention.
CREATE INDEX agent_presence_ended_idx ON agent_presence(ended_at);

-- The agents report counts each member's replies and notes in a range from this index.
CREATE INDEX IF NOT EXISTS messages_ws_user_created_idx ON messages(workspace_id, created_at)
    INCLUDE (author_user_id, ticket_id, kind, direction, thread_id)
    WHERE author_type = 'user' AND author_user_id IS NOT NULL AND deleted_at IS NULL;
