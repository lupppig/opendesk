-- Webhook endpoint ownership (CASA 7.1.2): a webhook receives events only once its URL
-- has answered a verification challenge, by echoing it or by an admin entering the code
-- it received. Webhooks made before verification existed already receive events and
-- count as verified.
ALTER TABLE webhooks
    ADD COLUMN verified_at      timestamptz,
    ADD COLUMN verify_code_hash bytea,
    ADD COLUMN verify_sent_at   timestamptz;
UPDATE webhooks SET verified_at = created_at;
