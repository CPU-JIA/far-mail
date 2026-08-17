-- v11: split admin console auth key from API access tokens.
--
-- accounts.api_key is now the Web/admin-console key:
--   sk-<custom>-<16 or 32 hex>
-- API tokens remain in account_tokens and keep:
--   sk-<custom>-<32 hex>

UPDATE accounts
SET api_key = 'sk-mail-' || encode(gen_random_bytes(16), 'hex'),
    updated_at = NOW()
WHERE api_key IS NULL
   OR api_key = ''
   OR api_key !~ '^sk-[a-z0-9_-]{1,24}-([0-9a-f]{16}|[0-9a-f]{32})$';
