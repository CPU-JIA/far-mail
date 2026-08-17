package store

import (
	"context"
	"strings"
)

func (s *Store) UpdateDomainMeta(ctx context.Context, id int, visibility, sourceType string) error {
	visibility = strings.ToLower(strings.TrimSpace(visibility))
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	if visibility == "" {
		visibility = "public"
	}
	if sourceType == "" {
		sourceType = "manual"
	}
	_, err := s.pool.Exec(ctx, `
        UPDATE domains
        SET visibility = $2,
            source_type = $3,
            verified_at = CASE WHEN is_active THEN COALESCE(verified_at, NOW()) ELSE verified_at END
        WHERE id = $1
    `, id, visibility, sourceType)
	if err == nil {
		s.invalidateActiveDomainsCache()
	}
	return err
}
