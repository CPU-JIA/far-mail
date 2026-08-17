-- v12: remove legacy account/registration settings and hard-separate credentials.

DELETE FROM app_settings
WHERE key IN ('registration_open', 'max_mailboxes_per_user', 'rate_limit_enabled', 'default_domain');

INSERT INTO app_settings (key, value) VALUES
  ('admin_key_prefix', 'mail'),
  ('admin_key_hex_length', '32')
ON CONFLICT (key) DO NOTHING;

UPDATE accounts
SET api_key = 'sk-mail-' || encode(gen_random_bytes(16), 'hex'),
    updated_at = NOW()
WHERE is_admin = TRUE
  AND (api_key IS NULL OR api_key !~ '^sk-[a-z0-9_-]{1,24}-([0-9a-f]{16}|[0-9a-f]{32})$');
