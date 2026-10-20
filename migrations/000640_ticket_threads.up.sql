-- Third-party threads (contracts/api.md "Threads"): an email conversation on a ticket with
-- someone outside the team, such as a supplier or a payment provider. Its messages are
-- internal notes with thread_id set, so they never reach the customer and never count as
-- the conversation with them.
CREATE TABLE ticket_threads (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    ticket_id       uuid NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    subject         text NOT NULL,
    to_emails       text[] NOT NULL DEFAULT '{}',
    cc_emails       text[] NOT NULL DEFAULT '{}',
    -- The address the thread is sent from; its replies come back to it.
    mailbox_id      uuid REFERENCES mailboxes(id) ON DELETE SET NULL,
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    -- The first message quotes the ticket's conversation with the customer.
    include_history boolean NOT NULL DEFAULT false,
    last_message_at timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ticket_threads_ticket_idx ON ticket_threads(workspace_id, ticket_id, created_at);

ALTER TABLE messages ADD COLUMN thread_id uuid REFERENCES ticket_threads(id) ON DELETE CASCADE;
ALTER TABLE messages ADD CONSTRAINT messages_thread_note CHECK (thread_id IS NULL OR kind = 'note');
CREATE INDEX messages_thread_idx ON messages(thread_id, created_at) WHERE thread_id IS NOT NULL;
