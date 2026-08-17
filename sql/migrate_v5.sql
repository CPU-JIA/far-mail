-- v5: mailbox_ttl_minutes = 0 表示永不过期
-- 允许 mailboxes.expires_at 为空，空值表示不自动过期

ALTER TABLE mailboxes
    ALTER COLUMN expires_at DROP NOT NULL;
