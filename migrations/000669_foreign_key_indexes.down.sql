DROP INDEX IF EXISTS attachments_uploaded_by_contact_idx;
DROP INDEX IF EXISTS attachments_uploaded_by_idx;

DROP INDEX IF EXISTS tickets_widget_idx;
DROP INDEX IF EXISTS tickets_assignee_idx;
DROP INDEX IF EXISTS tickets_team_idx;
DROP INDEX IF EXISTS tickets_merged_into_idx;
DROP INDEX IF EXISTS tickets_sla_policy_idx;
DROP INDEX IF EXISTS tickets_brand_idx;
DROP INDEX IF EXISTS tickets_contact_idx;
DROP INDEX IF EXISTS tickets_origin_mailbox_idx;
DROP INDEX IF EXISTS tickets_mailbox_idx;

DROP INDEX IF EXISTS messages_author_user_idx;
DROP INDEX IF EXISTS messages_author_contact_idx;
DROP INDEX IF EXISTS messages_mailbox_idx;
DROP INDEX IF EXISTS messages_inbound_email_idx;
