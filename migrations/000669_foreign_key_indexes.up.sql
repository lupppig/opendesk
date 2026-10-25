CREATE INDEX messages_inbound_email_idx ON messages(inbound_email_id) WHERE inbound_email_id IS NOT NULL;
CREATE INDEX messages_mailbox_idx ON messages(mailbox_id) WHERE mailbox_id IS NOT NULL;
CREATE INDEX messages_author_contact_idx ON messages(author_contact_id) WHERE author_contact_id IS NOT NULL;
CREATE INDEX messages_author_user_idx ON messages(author_user_id) WHERE author_user_id IS NOT NULL;

CREATE INDEX tickets_mailbox_idx ON tickets(mailbox_id) WHERE mailbox_id IS NOT NULL;
CREATE INDEX tickets_origin_mailbox_idx ON tickets(origin_mailbox_id) WHERE origin_mailbox_id IS NOT NULL;
CREATE INDEX tickets_contact_idx ON tickets(contact_id) WHERE contact_id IS NOT NULL;
CREATE INDEX tickets_brand_idx ON tickets(brand_id) WHERE brand_id IS NOT NULL;
CREATE INDEX tickets_sla_policy_idx ON tickets(sla_policy_id) WHERE sla_policy_id IS NOT NULL;
CREATE INDEX tickets_merged_into_idx ON tickets(merged_into_id) WHERE merged_into_id IS NOT NULL;
CREATE INDEX tickets_team_idx ON tickets(team_id) WHERE team_id IS NOT NULL;
CREATE INDEX tickets_assignee_idx ON tickets(assignee_id) WHERE assignee_id IS NOT NULL;
CREATE INDEX tickets_widget_idx ON tickets(widget_id) WHERE widget_id IS NOT NULL;

CREATE INDEX attachments_uploaded_by_idx ON attachments(uploaded_by) WHERE uploaded_by IS NOT NULL;
CREATE INDEX attachments_uploaded_by_contact_idx ON attachments(uploaded_by_contact_id) WHERE uploaded_by_contact_id IS NOT NULL;
