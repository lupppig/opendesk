-- Ticket replies sent from a member's own Collab address (contracts/collab.md "Answering tickets").

-- The account a reply went out from, and the Reply-To it asked for: "address", "mailbox" or an address.
ALTER TABLE messages ADD COLUMN collab_account_id uuid REFERENCES collab_accounts(id) ON DELETE SET NULL;
ALTER TABLE messages ADD COLUMN collab_reply_to text NOT NULL DEFAULT '';
CREATE INDEX messages_collab_account_idx ON messages(collab_account_id) WHERE collab_account_id IS NOT NULL;

-- Each address's own rules for them: allowed (null follows the workspace), the default
-- Reply-To (null follows the workspace) and which addresses: any, shared or domain.
ALTER TABLE mailboxes ADD COLUMN personal_replies jsonb NOT NULL DEFAULT '{}';

-- The ticket a conversation answered, or was answered from.
ALTER TABLE collab_threads ADD COLUMN ticket_id uuid REFERENCES tickets(id) ON DELETE SET NULL;
CREATE INDEX collab_threads_ticket_idx ON collab_threads(ticket_id) WHERE ticket_id IS NOT NULL;
