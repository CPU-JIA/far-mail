-- v16: 重建 mailbox_state 的最新邮件、验证码/链接投影与计数
--
-- 适用于已运行 v6+ 的数据库。该迁移只在升级时执行一次；新部署的
-- sql/init.sql 已包含完整结构。窗口聚合让 emails 只做一次逻辑扫描，
-- 并以 received_at + id 作为确定性的最新顺序。

BEGIN;

WITH ranked_emails AS (
    SELECT
        e.mailbox_id,
        e.id,
        e.sender,
        e.subject,
        e.parsed_code,
        e.parsed_code_source,
        e.parsed_link,
        e.parsed_link_source,
        e.received_at,
        COUNT(*) OVER (PARTITION BY e.mailbox_id) AS email_count,
        ROW_NUMBER() OVER (
            PARTITION BY e.mailbox_id
            ORDER BY e.received_at DESC, e.id DESC
        ) AS row_number
    FROM emails e
), latest_emails AS (
    SELECT *
    FROM ranked_emails
    WHERE row_number = 1
), rebuilt AS (
    INSERT INTO mailbox_state (
        mailbox_id,
        account_id,
        domain_id,
        domain_name,
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
        COALESCE(d.domain, split_part(m.full_address, '@', 2), ''),
        m.full_address,
        latest.id,
        COALESCE(latest.sender, ''),
        COALESCE(latest.subject, ''),
        COALESCE(latest.parsed_code, ''),
        COALESCE(latest.parsed_code_source, ''),
        COALESCE(latest.parsed_link, ''),
        COALESCE(latest.parsed_link_source, ''),
        latest.received_at,
        COALESCE(latest.email_count, 0),
        m.expires_at,
        m.keep_forever,
        m.created_at,
        NOW()
    FROM mailboxes m
    LEFT JOIN domains d ON d.id = m.domain_id
    LEFT JOIN latest_emails latest ON latest.mailbox_id = m.id
    ON CONFLICT (mailbox_id) DO UPDATE SET
        account_id = EXCLUDED.account_id,
        domain_id = EXCLUDED.domain_id,
        domain_name = EXCLUDED.domain_name,
        full_address = EXCLUDED.full_address,
        latest_email_id = EXCLUDED.latest_email_id,
        latest_sender = EXCLUDED.latest_sender,
        latest_subject = EXCLUDED.latest_subject,
        latest_code = EXCLUDED.latest_code,
        latest_code_source = EXCLUDED.latest_code_source,
        latest_link = EXCLUDED.latest_link,
        latest_link_source = EXCLUDED.latest_link_source,
        latest_received_at = EXCLUDED.latest_received_at,
        email_count = EXCLUDED.email_count,
        expires_at = EXCLUDED.expires_at,
        keep_forever = EXCLUDED.keep_forever,
        updated_at = NOW()
    RETURNING mailbox_id
)
SELECT COUNT(*) AS rebuilt_mailboxes FROM rebuilt;

COMMIT;
