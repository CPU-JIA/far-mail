-- ============================================================
-- FAR Mail - 数据库初始化
-- 针对高并发优化：索引、分区就绪、UUID主键
-- ============================================================

-- 启用扩展
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================
-- 1. 账号表 (accounts)
-- ============================================================
CREATE TABLE accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username    VARCHAR(64)  NOT NULL UNIQUE,
    api_key     VARCHAR(64)  NOT NULL UNIQUE,
    is_admin    BOOLEAN      NOT NULL DEFAULT FALSE,
    is_active   BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- API Key 查询走 B-tree 索引（认证热路径）
CREATE INDEX idx_accounts_api_key ON accounts (api_key);

-- ============================================================
-- 2. 域名池表 (domains)
-- ============================================================
CREATE TABLE domains (
    id            SERIAL PRIMARY KEY,
    domain        VARCHAR(255) NOT NULL UNIQUE,
    is_active     BOOLEAN      NOT NULL DEFAULT TRUE,
    status        VARCHAR(16)  NOT NULL DEFAULT 'active',  -- active / pending / disabled
    visibility    VARCHAR(16)  NOT NULL DEFAULT 'public',
    source_type   VARCHAR(16)  NOT NULL DEFAULT 'manual',
    verified_at   TIMESTAMPTZ,
    mx_checked_at TIMESTAMPTZ,                             -- 最近一次 MX 检测时间
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_domains_active ON domains (is_active) WHERE is_active = TRUE;
CREATE INDEX idx_domains_status ON domains (status) WHERE status = 'pending';

-- ============================================================
-- 3. 邮箱表 (mailboxes)
-- ============================================================
CREATE TABLE mailboxes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    address      VARCHAR(128) NOT NULL,  -- 本地部分，如 "abc123"
    domain_id    INT          NOT NULL REFERENCES domains(id),
    full_address VARCHAR(320) NOT NULL,  -- 完整地址 "abc123@mail.xxx.xyz"
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ  DEFAULT NOW() + INTERVAL '30 minutes',
    keep_forever BOOLEAN      NOT NULL DEFAULT FALSE
);

-- 完整地址唯一索引（收件匹配热路径）
CREATE UNIQUE INDEX idx_mailboxes_full_address ON mailboxes (full_address);

-- 按账号查邮箱列表，并直接提供倒序分页所需顺序
CREATE INDEX idx_mailboxes_account_created ON mailboxes (account_id, created_at DESC);

-- 精确域名筛选与账号范围、倒序分页共用表达式索引
CREATE INDEX idx_mailboxes_account_domain_created ON mailboxes (account_id, (split_part(full_address, '@', 2)), created_at DESC);

-- 过期自动清理索引
CREATE INDEX idx_mailboxes_expires_at ON mailboxes (expires_at);

-- ============================================================
-- 4. 邮件表 (emails)
-- ============================================================
CREATE TABLE emails (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id   UUID         NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    sender       VARCHAR(320) NOT NULL DEFAULT '',
    subject      VARCHAR(998) NOT NULL DEFAULT '',
    body_text    TEXT         NOT NULL DEFAULT '',
    body_html    TEXT         NOT NULL DEFAULT '',
    raw_message  TEXT         NOT NULL DEFAULT '',
    message_id   TEXT         NOT NULL DEFAULT '',
    headers_json JSONB        NOT NULL DEFAULT '{}'::jsonb,
    has_attachments BOOLEAN   NOT NULL DEFAULT FALSE,
    raw_path     TEXT         NOT NULL DEFAULT '',
    raw_retention_until TIMESTAMPTZ,
    processed_at TIMESTAMPTZ,
    processed_error TEXT      NOT NULL DEFAULT '',
    parsed_code  TEXT         NOT NULL DEFAULT '',
    parsed_code_source VARCHAR(32) NOT NULL DEFAULT '',
    parsed_link  TEXT         NOT NULL DEFAULT '',
    parsed_link_source VARCHAR(32) NOT NULL DEFAULT '',
    size_bytes   INT          NOT NULL DEFAULT 0,
    received_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 按邮箱查邮件（分页查询热路径）
CREATE INDEX idx_emails_mailbox_received ON emails (mailbox_id, received_at DESC);

-- 全局接收时间索引：历史清理、容量裁剪、最近活动与短期统计
CREATE INDEX idx_emails_received_at ON emails (received_at DESC);

CREATE TABLE mailbox_state (
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

ALTER TABLE mailbox_state SET (fillfactor = 80);

-- ============================================================
-- 5. 初始管理员账号
-- ============================================================
INSERT INTO accounts (username, api_key, is_admin)
VALUES ('admin', 'sk-mail-' || encode(gen_random_bytes(16), 'hex'), TRUE);

-- 5.1 token 表（新认证模型）
CREATE TABLE IF NOT EXISTS account_tokens (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id            UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name                  VARCHAR(128) NOT NULL DEFAULT '',
    token_hash            VARCHAR(64)  NOT NULL UNIQUE,
    token_prefix          VARCHAR(32)  NOT NULL,
    scope                 VARCHAR(16)  NOT NULL DEFAULT 'read',
    is_primary            BOOLEAN      NOT NULL DEFAULT FALSE,
    rate_limit_per_minute INT          NOT NULL DEFAULT 120,
    daily_request_limit   INT          NOT NULL DEFAULT 5000,
    total_request_limit   BIGINT       NOT NULL DEFAULT 100000,
    request_count_total   BIGINT       NOT NULL DEFAULT 0,
    last_used_at          TIMESTAMPTZ,
    expires_at            TIMESTAMPTZ,
    revoked_at            TIMESTAMPTZ,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_account_tokens_account ON account_tokens (account_id, created_at DESC);

-- ============================================================
-- 6. 初始域名（请在启动后通过管理后台或 API 添加实际域名）
-- ============================================================
-- INSERT INTO domains (domain) VALUES ('mail.yourdomain.com');

-- ============================================================
-- 7. 应用设置表 (app_settings)
-- ============================================================
CREATE TABLE IF NOT EXISTS app_settings (
    key        VARCHAR(64) PRIMARY KEY,
    value      TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO app_settings (key, value) VALUES ('smtp_server_ip', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('smtp_hostname', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('site_title', 'FAR Mail') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('site_logo_url', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('mailbox_ttl_minutes', '30') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('inbox_refresh_seconds', '3') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('token_default_scope', 'read') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('token_default_expires_days', '30') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('token_default_rate_limit_per_minute', '0') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('token_default_daily_request_limit', '0') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('token_default_total_request_limit', '0') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('admin_key_prefix', 'mail') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('admin_key_hex_length', '32') ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS domain_health_snapshots (
    domain               VARCHAR(255) PRIMARY KEY,
    root_mx_ok           BOOLEAN      NOT NULL DEFAULT FALSE,
    wildcard_mx_ok       BOOLEAN      NOT NULL DEFAULT FALSE,
    root_mx_status       TEXT         NOT NULL DEFAULT '',
    wildcard_mx_status   TEXT         NOT NULL DEFAULT '',
    root_mx_hosts_json   JSONB        NOT NULL DEFAULT '[]'::jsonb,
    wildcard_mx_hosts_json JSONB      NOT NULL DEFAULT '[]'::jsonb,
    checked_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS api_request_daily (
    day        DATE NOT NULL,
    token_id   UUID NOT NULL REFERENCES account_tokens(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    count      BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, token_id)
);

-- ============================================================
-- 7. 数据库性能参数（在 postgresql.conf 或 docker 环境变量中设置更佳）
-- ============================================================
-- 以下通过 ALTER SYSTEM 设置，重启后生效
-- ALTER SYSTEM SET shared_buffers = '256MB';
-- ALTER SYSTEM SET effective_cache_size = '512MB';
-- ALTER SYSTEM SET work_mem = '4MB';
-- ALTER SYSTEM SET maintenance_work_mem = '64MB';
-- ALTER SYSTEM SET max_connections = 200;
