-- v6: 新增 mailbox_state 投影表，收口 latest-code / latest-link 热路径

CREATE TABLE IF NOT EXISTS mailbox_state (
    mailbox_id          UUID PRIMARY KEY REFERENCES mailboxes(id) ON DELETE CASCADE,
    account_id          UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    domain_id           INT          NOT NULL REFERENCES domains(id),
    domain_name         VARCHAR(255) NOT NULL DEFAULT '',
    full_address        VARCHAR(320) NOT NULL UNIQUE,
    latest_email_id     UUID,
    latest_sender       VARCHAR(320) NOT NULL DEFAULT '',
    latest_subject      VARCHAR(998) NOT NULL DEFAULT '',
    latest_code         TEXT         NOT NULL DEFAULT '',
    latest_code_source  VARCHAR(32)  NOT NULL DEFAULT '',
    latest_link         TEXT         NOT NULL DEFAULT '',
    latest_link_source  VARCHAR(32)  NOT NULL DEFAULT '',
    latest_received_at  TIMESTAMPTZ,
    email_count         BIGINT       NOT NULL DEFAULT 0,
    expires_at          TIMESTAMPTZ,
    keep_forever        BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mailbox_state_account_recent
    ON mailbox_state (account_id, latest_received_at DESC);

INSERT INTO mailbox_state (
    mailbox_id,
    account_id,
    domain_id,
    full_address,
    latest_email_id,
    latest_sender,
    latest_subject,
    latest_code,
    latest_code_source,
    latest_link,
    latest_link_source,
    latest_received_at,
    email_count,
    expires_at,
    keep_forever,
    created_at,
    updated_at
)
SELECT
    m.id,
    m.account_id,
    m.domain_id,
    m.full_address,
    le.id,
    COALESCE(le.sender, ''),
    COALESCE(le.subject, ''),
    COALESCE(le.parsed_code, ''),
    CASE WHEN COALESCE(le.parsed_code, '') <> '' THEN 'projection' ELSE '' END,
    '',
    '',
    le.received_at,
    COALESCE(ec.email_count, 0),
    m.expires_at,
    m.keep_forever,
    NOW(),
    NOW()
FROM mailboxes m
LEFT JOIN LATERAL (
    SELECT e.id, e.sender, e.subject, e.parsed_code, e.received_at
    FROM emails e
    WHERE e.mailbox_id = m.id
    ORDER BY e.received_at DESC
    LIMIT 1
) le ON TRUE
LEFT JOIN (
    SELECT mailbox_id, COUNT(*) AS email_count
    FROM emails
    GROUP BY mailbox_id
) ec ON ec.mailbox_id = m.id
ON CONFLICT (mailbox_id) DO UPDATE
SET
    account_id = EXCLUDED.account_id,
    domain_id = EXCLUDED.domain_id,
    full_address = EXCLUDED.full_address,
    latest_email_id = EXCLUDED.latest_email_id,
    latest_sender = EXCLUDED.latest_sender,
    latest_subject = EXCLUDED.latest_subject,
    latest_code = EXCLUDED.latest_code,
    latest_code_source = EXCLUDED.latest_code_source,
    latest_received_at = EXCLUDED.latest_received_at,
    email_count = EXCLUDED.email_count,
    expires_at = EXCLUDED.expires_at,
    keep_forever = EXCLUDED.keep_forever,
    updated_at = NOW();

ALTER TABLE mailbox_state
    ADD COLUMN IF NOT EXISTS domain_name VARCHAR(255) NOT NULL DEFAULT '';

UPDATE mailbox_state ms
SET domain_name = LOWER(COALESCE(NULLIF(split_part(ms.full_address, '@', 2), ''), d.domain, ''))
FROM domains d
WHERE d.id = ms.domain_id
  AND (ms.domain_name = '' OR ms.domain_name IS NULL);

CREATE INDEX IF NOT EXISTS idx_mailbox_state_account_domain_recent
    ON mailbox_state (account_id, domain_name, latest_received_at DESC);
