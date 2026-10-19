-- Collab (contracts/collab.md): members' own mail accounts, private or shared, read and
-- handled like a mailbox, and the collab plan (contracts/billing.md "Collab").

-- The collab plan, and an instance admin's Collab access for a workspace.
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_plan_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_plan_check CHECK (plan IN ('free', 'pro', 'collab'));
ALTER TABLE subscriptions
    ADD COLUMN collab_access text NOT NULL DEFAULT 'none' CHECK (collab_access IN ('none', 'offered', 'complimentary')),
    ADD COLUMN collab_until timestamptz,
    ADD COLUMN collab_note text NOT NULL DEFAULT '';

-- Events scoped to people (contracts/events.md): only these users see them.
ALTER TABLE events ADD COLUMN user_ids uuid[];

CREATE TABLE collab_accounts (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    owner_id        uuid REFERENCES users(id) ON DELETE SET NULL,
    provider        text NOT NULL CHECK (provider IN ('google', 'microsoft', 'imap')),
    email           text NOT NULL,
    name            text NOT NULL DEFAULT '',
    display_name    text NOT NULL DEFAULT '',
    color           text NOT NULL DEFAULT 'blue',
    visibility      text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'shared')),
    signature_html  text NOT NULL DEFAULT '',
    config          jsonb NOT NULL DEFAULT '{}',
    credentials_enc bytea,
    -- Provider cursors: Gmail history id, Microsoft delta links, IMAP per-folder state.
    sync_state      jsonb NOT NULL DEFAULT '{}',
    status          text NOT NULL DEFAULT 'syncing' CHECK (status IN ('syncing', 'active', 'error')),
    reconnect       boolean NOT NULL DEFAULT false,
    last_error      text NOT NULL DEFAULT '',
    last_synced_at  timestamptz,
    sync_failures   integer NOT NULL DEFAULT 0,
    next_sync_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, email)
);
CREATE INDEX collab_accounts_owner_idx ON collab_accounts(owner_id);
CREATE INDEX collab_accounts_due_idx ON collab_accounts(next_sync_at);

CREATE TABLE collab_folders (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    remote_id    text NOT NULL,
    name         text NOT NULL,
    path         text NOT NULL,
    role         text CHECK (role IN ('inbox', 'sent', 'drafts', 'archive', 'trash', 'spam', 'starred', 'important', 'all')),
    kind         text NOT NULL DEFAULT 'folder' CHECK (kind IN ('folder', 'label')),
    color        text,
    -- Per-folder provider cursor (IMAP UIDVALIDITY, last UID, MODSEQ; Microsoft delta link).
    sync_state   jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, remote_id)
);

CREATE TABLE collab_threads (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id      uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    remote_id       text NOT NULL,
    subject         text NOT NULL DEFAULT '',
    snippet         text NOT NULL DEFAULT '',
    participants    jsonb NOT NULL DEFAULT '[]',
    last_message_at timestamptz NOT NULL DEFAULT now(),
    message_count   integer NOT NULL DEFAULT 0,
    unread          boolean NOT NULL DEFAULT false,
    starred         boolean NOT NULL DEFAULT false,
    has_attachments boolean NOT NULL DEFAULT false,
    -- Folders any of its messages are in (kept by the store with its messages).
    folder_ids      uuid[] NOT NULL DEFAULT '{}',
    search          tsvector,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, remote_id)
);
CREATE INDEX collab_threads_list_idx ON collab_threads(account_id, last_message_at DESC, id DESC);
CREATE INDEX collab_threads_folders_idx ON collab_threads USING gin (folder_ids);
CREATE INDEX collab_threads_search_idx ON collab_threads USING gin (search);
CREATE INDEX collab_threads_workspace_idx ON collab_threads(workspace_id, last_message_at DESC);

CREATE TABLE collab_messages (
    id              uuid PRIMARY KEY,
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id      uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    thread_id       uuid NOT NULL REFERENCES collab_threads(id) ON DELETE CASCADE,
    -- Gmail message id, Microsoft message id, or for IMAP the Message-ID header (else a hash).
    remote_id       text NOT NULL,
    message_id      text NOT NULL DEFAULT '',
    in_reply_to     text NOT NULL DEFAULT '',
    refs            text[] NOT NULL DEFAULT '{}',
    from_addr       jsonb NOT NULL DEFAULT '{}',
    to_addrs        jsonb NOT NULL DEFAULT '[]',
    cc_addrs        jsonb NOT NULL DEFAULT '[]',
    bcc_addrs       jsonb NOT NULL DEFAULT '[]',
    reply_to        jsonb NOT NULL DEFAULT '[]',
    subject         text NOT NULL DEFAULT '',
    snippet         text NOT NULL DEFAULT '',
    body_html       text NOT NULL DEFAULT '',
    body_text       text NOT NULL DEFAULT '',
    sent_at         timestamptz NOT NULL DEFAULT now(),
    unread          boolean NOT NULL DEFAULT false,
    starred         boolean NOT NULL DEFAULT false,
    draft           boolean NOT NULL DEFAULT false,
    attachments     jsonb NOT NULL DEFAULT '[]',
    raw_key         text NOT NULL DEFAULT '',
    -- Where the message is at the provider when that is not its id (IMAP: folder and UID).
    location        jsonb NOT NULL DEFAULT '{}',
    sent_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, remote_id)
);
CREATE INDEX collab_messages_thread_idx ON collab_messages(thread_id, sent_at);
CREATE INDEX collab_messages_msgid_idx ON collab_messages(account_id, message_id);

CREATE TABLE collab_message_folders (
    message_id uuid NOT NULL REFERENCES collab_messages(id) ON DELETE CASCADE,
    folder_id  uuid NOT NULL REFERENCES collab_folders(id) ON DELETE CASCADE,
    PRIMARY KEY (message_id, folder_id)
);
CREATE INDEX collab_message_folders_folder_idx ON collab_message_folders(folder_id);

-- Who an account or a conversation is shared with: everyone, a team, or a person.
CREATE TABLE collab_shares (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id   uuid REFERENCES collab_accounts(id) ON DELETE CASCADE,
    thread_id    uuid REFERENCES collab_threads(id) ON DELETE CASCADE,
    everyone     boolean NOT NULL DEFAULT false,
    team_id      uuid REFERENCES teams(id) ON DELETE CASCADE,
    user_id      uuid REFERENCES users(id) ON DELETE CASCADE,
    shared_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(account_id, thread_id) = 1),
    CHECK (num_nonnulls(team_id, user_id) + everyone::int = 1)
);
CREATE UNIQUE INDEX collab_shares_target_idx ON collab_shares(
    COALESCE(account_id, thread_id), everyone, COALESCE(team_id, user_id, '00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX collab_shares_account_idx ON collab_shares(account_id) WHERE account_id IS NOT NULL;
CREATE INDEX collab_shares_thread_idx ON collab_shares(thread_id) WHERE thread_id IS NOT NULL;
CREATE INDEX collab_shares_user_idx ON collab_shares(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX collab_shares_team_idx ON collab_shares(team_id) WHERE team_id IS NOT NULL;
CREATE INDEX collab_shares_workspace_idx ON collab_shares(workspace_id);

CREATE TABLE collab_assignees (
    thread_id   uuid NOT NULL REFERENCES collab_threads(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    assigned_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (thread_id, user_id)
);
CREATE INDEX collab_assignees_user_idx ON collab_assignees(user_id);

CREATE TABLE collab_comments (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    thread_id    uuid NOT NULL REFERENCES collab_threads(id) ON DELETE CASCADE,
    author_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    body_html    text NOT NULL,
    mentions     uuid[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX collab_comments_thread_idx ON collab_comments(thread_id, created_at);

CREATE TABLE collab_sidebar_prefs (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prefs        jsonb NOT NULL DEFAULT '{}',
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);

-- Raw messages to remove from storage after their rows are gone (collab.cleanup).
CREATE TABLE collab_storage_trash (
    key        text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);
