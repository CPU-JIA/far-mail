-- Integration operation audit metadata. Credentials and provider responses are never stored.
CREATE TABLE IF NOT EXISTS integration_audit_events (
    id BIGSERIAL PRIMARY KEY,
    integration VARCHAR(32) NOT NULL,
    action VARCHAR(32) NOT NULL,
    domain VARCHAR(255) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_integration_audit_created
    ON integration_audit_events (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_integration_audit_domain
    ON integration_audit_events (integration, domain, created_at DESC);
