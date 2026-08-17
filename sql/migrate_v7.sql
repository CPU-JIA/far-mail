ALTER TABLE mailbox_state
    ADD COLUMN IF NOT EXISTS domain_name VARCHAR(255) NOT NULL DEFAULT '';

UPDATE mailbox_state ms
SET domain_name = LOWER(COALESCE(NULLIF(split_part(ms.full_address, '@', 2), ''), d.domain, ''))
FROM domains d
WHERE d.id = ms.domain_id
  AND (ms.domain_name = '' OR ms.domain_name IS NULL);

CREATE INDEX IF NOT EXISTS idx_mailbox_state_account_domain_recent
    ON mailbox_state (account_id, domain_name, latest_received_at DESC);
