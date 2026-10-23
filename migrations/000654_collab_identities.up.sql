-- The addresses a Collab account sends from besides its own (contracts/collab.md
-- "Addresses"): Gmail's send-as addresses, read from Gmail, and aliases a person adds by
-- hand for Microsoft and IMAP accounts. The account's own address stays on collab_accounts
-- (display_name, signature_html).
CREATE TABLE collab_identities (
    id             uuid PRIMARY KEY,
    workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    -- Lower case.
    email          text NOT NULL,
    display_name   text NOT NULL DEFAULT '',
    signature_html text NOT NULL DEFAULT '',
    reply_to       text NOT NULL DEFAULT '',
    -- provider: listed by the provider (Gmail sendAs) and removed when it no longer is;
    -- manual: added by a person, lives in opendesk only.
    source         text NOT NULL CHECK (source IN ('provider', 'manual')),
    -- pending: Gmail is waiting for the address's owner to confirm it; it cannot send yet.
    verification   text NOT NULL DEFAULT 'accepted' CHECK (verification IN ('accepted', 'pending')),
    -- The provider's order of its addresses.
    position       integer NOT NULL DEFAULT 0,
    -- The hashes of the provider's signature and From name as last seen (settings.go).
    sync_state     jsonb NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, email)
);
CREATE INDEX collab_identities_workspace_idx ON collab_identities(workspace_id);

-- Where a message was delivered (Delivered-To, X-Original-To, X-Forwarded-To), for the
-- address it was written to when its To and Cc do not say.
ALTER TABLE collab_messages ADD COLUMN delivered_to text[] NOT NULL DEFAULT '{}';

-- Every address on a conversation's messages (From, To, Cc, delivered to), lower case,
-- newest first: the thread list's filter by address and the alias it was written to.
ALTER TABLE collab_threads ADD COLUMN addresses text[] NOT NULL DEFAULT '{}';
CREATE INDEX collab_threads_addresses_idx ON collab_threads USING gin (addresses);

UPDATE collab_threads t SET addresses = x.list
FROM (
    SELECT m.thread_id, array_agg(DISTINCT lower(e->>'email')) FILTER (WHERE coalesce(e->>'email', '') <> '') AS list
    FROM collab_messages m,
        jsonb_array_elements(m.to_addrs || m.cc_addrs || jsonb_build_array(m.from_addr)) e
    GROUP BY m.thread_id
) x
WHERE x.thread_id = t.id AND x.list IS NOT NULL;
