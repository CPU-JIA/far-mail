-- v10: remove low-use mailbox_state recency indexes from the hot write path.
--
-- The lookup API uses mailbox_state_full_address_key, and LMTP delivery updates
-- mailbox_state on every delivered message. These two recency indexes add write
-- amplification but are not used by the current API routes.

DROP INDEX CONCURRENTLY IF EXISTS idx_mailbox_state_account_domain_recent;
DROP INDEX CONCURRENTLY IF EXISTS idx_mailbox_state_account_recent;

ALTER TABLE mailbox_state SET (fillfactor = 80);

-- Run after deployment outside a transaction:
-- VACUUM ANALYZE mailbox_state, mailboxes, emails;
