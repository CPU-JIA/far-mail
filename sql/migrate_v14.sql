-- v14: measured hot-path indexes.
--
-- Run each statement outside an explicit transaction. CONCURRENTLY keeps
-- mailbox creation and LMTP delivery available while an existing large table
-- is indexed.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mailboxes_account_created
    ON mailboxes (account_id, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_emails_received_at
    ON emails (received_at DESC);

-- The composite index above has the same account_id leading key and also
-- satisfies the normal created_at DESC listing, so the old single-column
-- index only adds write amplification.
DROP INDEX CONCURRENTLY IF EXISTS idx_mailboxes_account_id;

ANALYZE mailboxes;
ANALYZE emails;
