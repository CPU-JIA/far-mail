package store

import "context"

func (s *Store) ensureAuxSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS account_tokens (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			name VARCHAR(128) NOT NULL DEFAULT '',
			token_hash VARCHAR(64) NOT NULL UNIQUE,
			token_prefix VARCHAR(32) NOT NULL,
			scope VARCHAR(16) NOT NULL DEFAULT 'read',
			is_primary BOOLEAN NOT NULL DEFAULT FALSE,
			token_kind VARCHAR(16) NOT NULL DEFAULT 'standard',
			rate_limit_per_minute INT NOT NULL DEFAULT 120,
			daily_request_limit INT NOT NULL DEFAULT 5000,
			total_request_limit BIGINT NOT NULL DEFAULT 100000,
			request_count_total BIGINT NOT NULL DEFAULT 0,
			last_used_at TIMESTAMPTZ,
			expires_at TIMESTAMPTZ,
			revoked_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_account_tokens_account ON account_tokens (account_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_account_tokens_active ON account_tokens (account_id, revoked_at, expires_at);

		CREATE TABLE IF NOT EXISTS domain_donations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			domain_id INT NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
			token_id UUID NOT NULL REFERENCES account_tokens(id) ON DELETE RESTRICT,
			claim_secret_hash VARCHAR(64) NOT NULL UNIQUE,
			challenge_token VARCHAR(64) NOT NULL,
			include_subdomains BOOLEAN NOT NULL DEFAULT TRUE,
			status VARCHAR(16) NOT NULL DEFAULT 'pending',
			reward_active BOOLEAN NOT NULL DEFAULT FALSE,
			reward_rate_limit_per_minute INT NOT NULL,
			reward_daily_request_limit INT NOT NULL,
			reward_total_request_limit BIGINT NOT NULL,
			failure_count INT NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			last_checked_at TIMESTAMPTZ,
			activated_at TIMESTAMPTZ,
			reward_revoked_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_domain_donations_due ON domain_donations (status, last_checked_at);
		CREATE INDEX IF NOT EXISTS idx_domain_donations_token ON domain_donations (token_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS donation_reward_events (
			id BIGSERIAL PRIMARY KEY,
			token_id UUID NOT NULL REFERENCES account_tokens(id) ON DELETE CASCADE,
			donation_id UUID REFERENCES domain_donations(id) ON DELETE SET NULL,
			event_type VARCHAR(24) NOT NULL,
			total_delta BIGINT NOT NULL DEFAULT 0,
			daily_delta INT NOT NULL DEFAULT 0,
			rpm_delta INT NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_donation_reward_events_token ON donation_reward_events (token_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_donation_reward_events_manual ON donation_reward_events (token_id) WHERE event_type = 'manual_adjust';

		CREATE TABLE IF NOT EXISTS domain_health_snapshots (
			domain VARCHAR(255) PRIMARY KEY,
			root_mx_ok BOOLEAN NOT NULL DEFAULT FALSE,
			wildcard_mx_ok BOOLEAN NOT NULL DEFAULT FALSE,
			root_mx_status TEXT NOT NULL DEFAULT '',
			wildcard_mx_status TEXT NOT NULL DEFAULT '',
			root_mx_hosts_json JSONB NOT NULL DEFAULT '[]'::jsonb,
			wildcard_mx_hosts_json JSONB NOT NULL DEFAULT '[]'::jsonb,
			checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS api_request_daily (
			day DATE NOT NULL,
			token_id UUID NOT NULL REFERENCES account_tokens(id) ON DELETE CASCADE,
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			count BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (day, token_id)
		);
		CREATE INDEX IF NOT EXISTS idx_api_request_daily_day ON api_request_daily (day DESC);

		CREATE TABLE IF NOT EXISTS api_request_events (
			id BIGSERIAL PRIMARY KEY,
			token_id UUID NOT NULL REFERENCES account_tokens(id) ON DELETE CASCADE,
			method VARCHAR(8) NOT NULL,
			route VARCHAR(160) NOT NULL,
			status_code INT NOT NULL,
			latency_ms INT NOT NULL,
			request_id VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_api_request_events_created ON api_request_events (created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_api_request_events_token_created ON api_request_events (token_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS integration_audit_events (
			id BIGSERIAL PRIMARY KEY,
			integration VARCHAR(32) NOT NULL,
			action VARCHAR(32) NOT NULL,
			domain VARCHAR(255) NOT NULL DEFAULT '',
			status VARCHAR(16) NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_integration_audit_created ON integration_audit_events (created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_integration_audit_domain ON integration_audit_events (integration, domain, created_at DESC);

		CREATE TABLE IF NOT EXISTS mailbox_state (
			mailbox_id UUID PRIMARY KEY REFERENCES mailboxes(id) ON DELETE CASCADE,
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
			domain_id INT NOT NULL REFERENCES domains(id),
			domain_name VARCHAR(255) NOT NULL DEFAULT '',
			full_address VARCHAR(320) NOT NULL UNIQUE,
			latest_email_id UUID,
			latest_sender VARCHAR(320) NOT NULL DEFAULT '',
			latest_subject VARCHAR(998) NOT NULL DEFAULT '',
			latest_code TEXT NOT NULL DEFAULT '',
			latest_code_source VARCHAR(32) NOT NULL DEFAULT '',
			latest_link TEXT NOT NULL DEFAULT '',
			latest_link_source VARCHAR(32) NOT NULL DEFAULT '',
			latest_received_at TIMESTAMPTZ,
			email_count BIGINT NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ,
			keep_forever BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		ALTER TABLE mailbox_state SET (fillfactor = 80);
	`)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		ALTER TABLE mailboxes ALTER COLUMN expires_at DROP NOT NULL;
		ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS keep_forever BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS creator_token_id UUID REFERENCES account_tokens(id) ON DELETE SET NULL;
		CREATE INDEX IF NOT EXISTS idx_mailboxes_creator_token ON mailboxes (creator_token_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_mailboxes_account_created ON mailboxes (account_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_mailboxes_account_domain_created ON mailboxes (account_id, (split_part(full_address, '@', 2)), created_at DESC);
		DROP INDEX IF EXISTS idx_mailboxes_account_id;

		ALTER TABLE account_tokens ADD COLUMN IF NOT EXISTS token_kind VARCHAR(16) NOT NULL DEFAULT 'standard';

		ALTER TABLE domains ADD COLUMN IF NOT EXISTS visibility VARCHAR(16) NOT NULL DEFAULT 'public';
		ALTER TABLE domains ADD COLUMN IF NOT EXISTS source_type VARCHAR(16) NOT NULL DEFAULT 'manual';
		ALTER TABLE domains ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ;

		ALTER TABLE emails ADD COLUMN IF NOT EXISTS message_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS headers_json JSONB NOT NULL DEFAULT '{}'::jsonb;
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS has_attachments BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS raw_path TEXT NOT NULL DEFAULT '';
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS raw_retention_until TIMESTAMPTZ;
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS processed_at TIMESTAMPTZ;
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS processed_error TEXT NOT NULL DEFAULT '';
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS parsed_code TEXT NOT NULL DEFAULT '';
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS parsed_code_source VARCHAR(32) NOT NULL DEFAULT '';
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS parsed_link TEXT NOT NULL DEFAULT '';
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS parsed_link_source VARCHAR(32) NOT NULL DEFAULT '';
		CREATE INDEX IF NOT EXISTS idx_emails_received_at ON emails (received_at DESC);

		ALTER TABLE mailbox_state ADD COLUMN IF NOT EXISTS domain_name VARCHAR(255) NOT NULL DEFAULT '';
		ALTER TABLE mailbox_state ADD COLUMN IF NOT EXISTS latest_link TEXT NOT NULL DEFAULT '';
		ALTER TABLE mailbox_state ADD COLUMN IF NOT EXISTS latest_link_source VARCHAR(32) NOT NULL DEFAULT '';
	`)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value) VALUES
			('site_title', 'FAR Mail'),
			('site_logo_url', ''),
			('mailbox_ttl_minutes', '30'),
			('email_retention_minutes', '1440'),
			('inbox_refresh_seconds', '3'),
			('token_default_scope', 'read'),
			('token_default_expires_days', '30'),
			('token_default_rate_limit_per_minute', '0'),
			('token_default_daily_request_limit', '0'),
			('token_default_total_request_limit', '0'),
			('admin_key_prefix', 'mail'),
			('admin_key_hex_length', '32'),
			('donation_enabled', 'true'),
			('donation_reward_rate_limit_per_minute', '30'),
			('donation_reward_daily_request_limit', '5000'),
			('donation_reward_total_request_limit', '100000'),
			('donation_token_rate_limit_cap', '180'),
			('donation_max_domains_per_token', '10'),
			('donation_dns_failure_tolerance', '3'),
			('donation_recheck_minutes', '30')
		ON CONFLICT (key) DO NOTHING
	`)
	if err != nil {
		return err
	}

	// Replace only known legacy defaults. Owner-defined site titles remain intact.
	_, err = s.pool.Exec(ctx, `
		UPDATE app_settings
		SET value = 'FAR Mail', updated_at = NOW()
		WHERE key = 'site_title'
		  AND (
			LOWER(BTRIM(value)) IN ('tempmail', 'temp mail', 'temporary mail')
			OR BTRIM(value) IN ('临时邮箱', '临时邮箱平台')
		  )
	`)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		DELETE FROM app_settings
		WHERE key IN (
			'registration_open', 'max_mailboxes_per_user', 'rate_limit_enabled', 'default_domain',
			'primary_token_rate_limit_per_minute', 'primary_token_daily_request_limit', 'primary_token_total_request_limit',
			'token_secret_prefix'
		);

		UPDATE accounts
		SET api_key = 'sk-mail-' || encode(gen_random_bytes(16), 'hex'),
		    updated_at = NOW()
		WHERE is_admin = TRUE
		  AND (api_key IS NULL OR api_key !~ '^sk-[a-z0-9_-]{1,24}-([0-9a-f]{16}|[0-9a-f]{32})$');
	`)
	if err != nil {
		return err
	}

	// API tokens are owner-issued credentials; boot must not create a hidden default token.
	_, err = s.pool.Exec(ctx, `DELETE FROM account_tokens WHERE is_primary = TRUE`)
	return err
}
