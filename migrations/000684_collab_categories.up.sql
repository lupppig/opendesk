-- Inbox categories (contracts/collab.md "Categories"): tabs that split a Collab inbox into
-- Primary and categories such as Updates and Newsletters, set for the whole workspace, for a
-- team, or by a member for an account they manage.
CREATE TABLE collab_category_sets (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    scope        text NOT NULL CHECK (scope IN ('workspace', 'team', 'account')),
    team_id      uuid REFERENCES teams(id) ON DELETE CASCADE,
    account_id   uuid REFERENCES collab_accounts(id) ON DELETE CASCADE,
    -- on: these categories; off: the inbox is not split; follow (accounts only): the team's
    -- or the workspace's, keeping its own categories for later.
    mode         text NOT NULL CHECK (mode IN ('on', 'off', 'follow')),
    -- [{key, name, description, color}], "primary" first.
    categories   jsonb NOT NULL DEFAULT '[]',
    -- workspace: members who are not admins cannot choose their own.
    locked       boolean NOT NULL DEFAULT false,
    -- account: Ollie may sort its new mail; by_admin: an admin made its choice, which a
    -- locked workspace keeps.
    ai           boolean NOT NULL DEFAULT true,
    by_admin     boolean NOT NULL DEFAULT false,
    -- team: the workspace's order of team sets; the owner's first team with one decides.
    position     integer NOT NULL DEFAULT 0,
    updated_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'workspace' AND team_id IS NULL AND account_id IS NULL)
        OR (scope = 'team' AND team_id IS NOT NULL AND account_id IS NULL)
        OR (scope = 'account' AND account_id IS NOT NULL AND team_id IS NULL)),
    CHECK (mode <> 'follow' OR scope = 'account')
);
CREATE UNIQUE INDEX collab_category_sets_workspace_idx ON collab_category_sets(workspace_id) WHERE scope = 'workspace';
CREATE UNIQUE INDEX collab_category_sets_team_idx ON collab_category_sets(team_id) WHERE scope = 'team';
CREATE UNIQUE INDEX collab_category_sets_account_idx ON collab_category_sets(account_id) WHERE scope = 'account';

-- An account's sender rules: mail from an address, or from "@domain", goes to a category.
CREATE TABLE collab_category_rules (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    -- Lower case: an address, or "@" and a domain.
    sender       text NOT NULL,
    category     text NOT NULL,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, sender)
);
CREATE INDEX collab_category_rules_workspace_idx ON collab_category_rules(workspace_id);

-- A conversation's category key (NULL: Primary, or not sorted), how it got it (rule, sender,
-- reply, ai, headers, person) and whether it waits for collab.categorize.
ALTER TABLE collab_threads ADD COLUMN category text;
ALTER TABLE collab_threads ADD COLUMN category_source text;
ALTER TABLE collab_threads ADD COLUMN category_pending boolean NOT NULL DEFAULT false;
CREATE INDEX collab_threads_category_pending_idx ON collab_threads(account_id, last_message_at DESC) WHERE category_pending;
CREATE INDEX collab_threads_category_idx ON collab_threads(account_id, category, last_message_at DESC) WHERE category IS NOT NULL;

-- What the headers say about a message, for sorting: list, bulk, auto, noreply, direct.
ALTER TABLE collab_messages ADD COLUMN signals text[] NOT NULL DEFAULT '{}';

-- The category keys the account's inbox was last sorted with ('{}': not split) and a hash
-- of the categories, descriptions and choices behind them.
ALTER TABLE collab_accounts ADD COLUMN category_keys text[] NOT NULL DEFAULT '{}';
ALTER TABLE collab_accounts ADD COLUMN category_sig text NOT NULL DEFAULT '';
