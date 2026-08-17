package store

import (
	"context"
	"encoding/json"
	"time"

	"farmail/model"

	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertDomainHealthSnapshot(ctx context.Context, item model.DomainHealth) error {
	rootHosts, _ := json.Marshal(item.RootMXHosts)
	wildHosts, _ := json.Marshal(item.WildcardMXHosts)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO domain_health_snapshots (
			domain, root_mx_ok, wildcard_mx_ok, root_mx_status, wildcard_mx_status,
			root_mx_hosts_json, wildcard_mx_hosts_json, checked_at
		) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,NOW())
		ON CONFLICT (domain) DO UPDATE SET
			root_mx_ok = EXCLUDED.root_mx_ok,
			wildcard_mx_ok = EXCLUDED.wildcard_mx_ok,
			root_mx_status = EXCLUDED.root_mx_status,
			wildcard_mx_status = EXCLUDED.wildcard_mx_status,
			root_mx_hosts_json = EXCLUDED.root_mx_hosts_json,
			wildcard_mx_hosts_json = EXCLUDED.wildcard_mx_hosts_json,
			checked_at = NOW()
	`, item.Domain, item.RootMXOK, item.WildcardMXOK, item.RootMXStatus, item.WildcardMXStatus, string(rootHosts), string(wildHosts))
	return err
}

func (s *Store) ListDomainHealthSnapshots(ctx context.Context) ([]model.DomainHealth, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.domain, d.is_active, d.status, d.mx_checked_at,
		       COALESCE(h.root_mx_ok, FALSE),
		       COALESCE(h.wildcard_mx_ok, FALSE),
		       COALESCE(h.root_mx_status, ''),
		       COALESCE(h.wildcard_mx_status, ''),
		       COALESCE(h.root_mx_hosts_json::text, '[]'),
		       COALESCE(h.wildcard_mx_hosts_json::text, '[]'),
		       h.checked_at
		FROM domains d
		LEFT JOIN domain_health_snapshots h ON h.domain = d.domain
		ORDER BY d.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.DomainHealth
	for rows.Next() {
		var (
			item     model.DomainHealth
			rootJSON string
			wildJSON string
		)
		if err := rows.Scan(
			&item.Domain, &item.IsActive, &item.Status, &item.MxCheckedAt,
			&item.RootMXOK, &item.WildcardMXOK, &item.RootMXStatus, &item.WildcardMXStatus,
			&rootJSON, &wildJSON, &item.CheckedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(rootJSON), &item.RootMXHosts)
		_ = json.Unmarshal([]byte(wildJSON), &item.WildcardMXHosts)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetDomainHealthSummary(ctx context.Context) (healthyRoot int, healthyWildcard int, unhealthy int, latest *time.Time, err error) {
	var (
		lr, lw, lu int
		ts         *time.Time
	)
	err = s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN s.root_mx_ok THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN s.wildcard_mx_ok THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN NOT s.root_mx_ok OR NOT s.wildcard_mx_ok THEN 1 ELSE 0 END), 0),
			MAX(s.checked_at)
		FROM domain_health_snapshots s
		JOIN domains d ON d.domain = s.domain
		WHERE d.is_active = TRUE
	`).Scan(&lr, &lw, &lu, &ts)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, 0, 0, nil, nil
		}
		return 0, 0, 0, nil, err
	}
	return lr, lw, lu, ts, nil
}
