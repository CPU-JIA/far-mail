package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// IntegrationAuditEvent is deliberately limited to operational metadata.
// Credentials, request bodies and provider responses never enter this table.
type IntegrationAuditEvent struct {
	ID          int64     `json:"id"`
	Integration string    `json:"integration"`
	Action      string    `json:"action"`
	Domain      string    `json:"domain"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) RecordIntegrationAudit(ctx context.Context, integration, action, domain, status, detail string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO integration_audit_events (integration, action, domain, status, detail)
		VALUES ($1, $2, $3, $4, $5)
	`, strings.TrimSpace(integration), strings.TrimSpace(action), strings.TrimSpace(domain), strings.TrimSpace(status), strings.TrimSpace(detail))
	return err
}

func (s *Store) ListIntegrationAudit(ctx context.Context, limit int) ([]IntegrationAuditEvent, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, integration, action, domain, status, detail, created_at
		FROM integration_audit_events
		ORDER BY created_at DESC, id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[IntegrationAuditEvent])
}
