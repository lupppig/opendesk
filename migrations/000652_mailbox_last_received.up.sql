-- When mail last arrived at each address, whatever its route (forwarding, IMAP, the
-- Gmail and Microsoft APIs): the mailbox page shows it and warns when mail stops coming.
ALTER TABLE mailboxes ADD COLUMN last_received_at timestamptz;

UPDATE mailboxes m SET last_received_at = x.at
FROM (SELECT mailbox_id, max(received_at) AS at FROM inbound_emails GROUP BY mailbox_id) x
WHERE x.mailbox_id = m.id;
