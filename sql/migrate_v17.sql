-- FAR Mail v17: make exact domain-filtered mailbox searches use the account
-- scope and newest-first ordering index without a split_part() table scan.
-- Run outside an explicit transaction on a live deployment.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_mailboxes_account_domain_created
    ON mailboxes (account_id, (split_part(full_address, '@', 2)), created_at DESC);
