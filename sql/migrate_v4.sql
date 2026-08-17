-- ============================================================
-- FAR Mail v4 迁移 — 邮箱永久保留 keep_forever
-- ============================================================

ALTER TABLE mailboxes
  ADD COLUMN IF NOT EXISTS keep_forever BOOLEAN NOT NULL DEFAULT FALSE;
