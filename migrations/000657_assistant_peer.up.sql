-- Asking a teammate's Ollie (contracts/ai.md "Asking a teammate"): each member's own consent
-- to let teammates' Ollie ask theirs about their Collab mail, who may ask, what each of their
-- accounts shares, and the log of asks made to them.

CREATE TABLE assistant_peer_settings (
    workspace_id uuid NOT NULL,
    user_id      uuid NOT NULL,
    enabled      boolean NOT NULL DEFAULT false,
    share_with   text NOT NULL DEFAULT 'everyone' CHECK (share_with IN ('everyone', 'chosen')),
    all_accounts boolean NOT NULL DEFAULT true,
    default_mode text NOT NULL DEFAULT 'summary' CHECK (default_mode IN ('verbatim', 'summary', 'none')),
    rules        text NOT NULL DEFAULT '' CHECK (char_length(rules) <= 2000),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES members(workspace_id, user_id) ON DELETE CASCADE
);

-- Who may ask when share_with is 'chosen': people and teams.
CREATE TABLE assistant_peer_audience (
    workspace_id uuid NOT NULL,
    owner_id     uuid NOT NULL,
    user_id      uuid REFERENCES users(id) ON DELETE CASCADE,
    team_id      uuid REFERENCES teams(id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, owner_id) REFERENCES assistant_peer_settings(workspace_id, user_id) ON DELETE CASCADE,
    CHECK (num_nonnulls(user_id, team_id) = 1)
);
CREATE UNIQUE INDEX assistant_peer_audience_idx ON assistant_peer_audience(
    workspace_id, owner_id, COALESCE(user_id, team_id));

-- What each of the owner's own accounts shares.
CREATE TABLE assistant_peer_sources (
    workspace_id uuid NOT NULL,
    owner_id     uuid NOT NULL,
    account_id   uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    mode         text NOT NULL CHECK (mode IN ('verbatim', 'summary', 'none')),
    PRIMARY KEY (owner_id, account_id),
    FOREIGN KEY (workspace_id, owner_id) REFERENCES assistant_peer_settings(workspace_id, user_id) ON DELETE CASCADE
);
CREATE INDEX assistant_peer_sources_account_idx ON assistant_peer_sources(account_id);

-- Asks made to a member's Ollie, for the member's own log; removed after 90 days.
CREATE TABLE assistant_peer_asks (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    target_id    uuid NOT NULL,
    asker_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question     text NOT NULL,
    answer       text NOT NULL DEFAULT '',
    result       text NOT NULL CHECK (result IN ('answered', 'declined', 'not_shared', 'failed')),
    sources      integer NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, target_id) REFERENCES members(workspace_id, user_id) ON DELETE CASCADE
);
CREATE INDEX assistant_peer_asks_target_idx ON assistant_peer_asks(workspace_id, target_id, created_at DESC, id DESC);
CREATE INDEX assistant_peer_asks_created_idx ON assistant_peer_asks(created_at);
CREATE INDEX assistant_peer_asks_asker_idx ON assistant_peer_asks(asker_id);
