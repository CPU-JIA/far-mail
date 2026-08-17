-- v13: adopt the FAR Mail brand without overwriting owner-defined site titles.

INSERT INTO app_settings (key, value)
VALUES ('site_title', 'FAR Mail')
ON CONFLICT (key) DO NOTHING;

UPDATE app_settings
SET value = 'FAR Mail', updated_at = NOW()
WHERE key = 'site_title'
  AND (
    LOWER(BTRIM(value)) IN ('tempmail', 'temp mail', 'temporary mail')
    OR BTRIM(value) IN ('临时邮箱', '临时邮箱平台')
  );
