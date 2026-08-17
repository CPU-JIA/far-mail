-- v9: tune mailbox_state for frequent latest-mail updates.
ALTER TABLE mailbox_state SET (fillfactor = 80);

-- Run after deployment outside a transaction:
-- VACUUM ANALYZE mailbox_state, mailboxes, emails;
