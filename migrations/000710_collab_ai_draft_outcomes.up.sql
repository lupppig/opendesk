-- What happens to each AI draft (contracts/collab.md "AI draft outcomes", GH#261). The AI's
-- last saved text is kept here when a person takes the draft over, sends it or discards
-- it, so the key that wrote it can learn how it did. The server sets the outcome; a person
-- may add feedback. seq moves on with every outcome or feedback: the key's feed reads past
-- its cursor, so a draft comes back when something new is known about it.
CREATE SEQUENCE collab_ai_drafts_seq;

CREATE TABLE collab_ai_drafts (
    id                 uuid PRIMARY KEY,
    workspace_id       uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id         uuid NOT NULL REFERENCES collab_accounts(id) ON DELETE CASCADE,
    thread_id          uuid NOT NULL REFERENCES collab_threads(id) ON DELETE CASCADE,
    api_key_id         uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    -- The member the key acts as.
    member_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client             text NOT NULL,
    -- The draft's message id. No foreign key: a draft saved again is stored again under
    -- the same id, and the draft row goes when it is sent or discarded.
    draft_message_id   uuid NOT NULL,
    client_ref         text NOT NULL DEFAULT '',
    ai_body_html       text NOT NULL,
    ai_saved_at        timestamptz NOT NULL DEFAULT now(),
    taken_over_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    taken_over_at      timestamptz,
    outcome            text CHECK (outcome IN ('sent_unchanged', 'sent_edited', 'discarded', 'replaced', 'abandoned')),
    outcome_at         timestamptz,
    outcome_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    sent_message_id    uuid REFERENCES collab_messages(id) ON DELETE SET NULL,
    feedback_reason    text CHECK (feedback_reason IN ('wrong_facts', 'wrong_tone', 'missing_info', 'should_not_answer', 'other')),
    feedback_note      text NOT NULL DEFAULT '',
    better_answer_html text NOT NULL DEFAULT '',
    feedback_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    feedback_at        timestamptz,
    seq                bigint NOT NULL DEFAULT nextval('collab_ai_drafts_seq'),
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX collab_ai_drafts_message_idx ON collab_ai_drafts(draft_message_id);
CREATE INDEX collab_ai_drafts_thread_open_idx ON collab_ai_drafts(thread_id) WHERE outcome IS NULL;
CREATE INDEX collab_ai_drafts_feed_idx ON collab_ai_drafts(api_key_id, seq)
    WHERE outcome IS NOT NULL OR feedback_at IS NOT NULL;
CREATE INDEX collab_ai_drafts_sent_idx ON collab_ai_drafts(sent_message_id) WHERE sent_message_id IS NOT NULL;
