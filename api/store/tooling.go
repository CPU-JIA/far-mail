package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"farmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SearchMailboxesForAccount(ctx context.Context, accountID uuid.UUID, creatorTokenID *uuid.UUID, page, size int, q, domain string, keepForeverOnly bool, expiringWithinHours int) ([]model.Mailbox, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	q = strings.TrimSpace(q)
	domain = strings.ToLower(strings.TrimSpace(domain))

	where, args := buildMailboxSearchWhere(accountID, creatorTokenID, q, domain, keepForeverOnly, expiringWithinHours)

	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	args = append(args, size, (page-1)*size)
	// Keep filtered listings on one connection/round trip as well. The count
	// branch preserves exact totals for an empty/out-of-range page while the
	// page branch remains index-friendly for the common account/time ordering.
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT page.id, page.account_id, page.address, page.domain_id,
		       page.full_address, page.created_at, page.expires_at,
		       page.keep_forever, totals.total
		FROM LATERAL (
			SELECT COUNT(*)::bigint AS total
			FROM mailboxes m %s
		) AS totals
		LEFT JOIN LATERAL (
			SELECT m.id, m.account_id, m.address, m.domain_id, m.full_address,
			       m.created_at, m.expires_at, m.keep_forever
			FROM mailboxes m %s
			ORDER BY m.created_at DESC, m.id DESC
			LIMIT $%d OFFSET $%d
		) AS page ON TRUE
		ORDER BY page.created_at DESC NULLS LAST, page.id DESC NULLS LAST
	`, where, where, limitArg, offsetArg), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.Mailbox, 0, size)
	var total int64
	for rows.Next() {
		var item model.Mailbox
		var id, itemAccountID *uuid.UUID
		var address, fullAddress *string
		var domainID *int
		var createdAt, expiresAt *time.Time
		var keepForever *bool
		if err := rows.Scan(
			&id, &itemAccountID, &address, &domainID,
			&fullAddress, &createdAt, &expiresAt, &keepForever, &total,
		); err != nil {
			return nil, 0, err
		}
		if id == nil {
			continue
		}
		item.ID = *id
		item.AccountID = *itemAccountID
		item.Address = *address
		item.DomainID = *domainID
		item.FullAddress = *fullAddress
		item.CreatedAt = *createdAt
		item.ExpiresAt = expiresAt
		item.KeepForever = *keepForever
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, int(total), nil
}

// buildMailboxSearchWhere keeps the common mailbox scope predicates sargable.
// The previous implementation represented every optional filter as an OR
// expression. That forced PostgreSQL to consider a broad scan even when only
// the account predicate was relevant. Building only the requested predicates
// lets the account+created_at index support the normal listing path while
// preserving the exact address/domain matching semantics of the API.
func buildMailboxSearchWhere(accountID uuid.UUID, creatorTokenID *uuid.UUID, q, domain string, keepForeverOnly bool, expiringWithinHours int) (string, []any) {
	clauses := []string{"WHERE m.account_id = $1"}
	args := []any{accountID}
	add := func(clause string, value any) int {
		args = append(args, value)
		placeholder := len(args)
		clauses = append(clauses, fmt.Sprintf(clause, placeholder))
		return placeholder
	}

	if creatorTokenID != nil {
		add("AND m.creator_token_id = $%d", *creatorTokenID)
	}
	if q != "" {
		// Keep the historical substring search behaviour. The same parameter is
		// reused for both columns so this adds no duplicate bind value.
		pattern := "%" + q + "%"
		args = append(args, pattern)
		placeholder := len(args)
		clauses = append(clauses, fmt.Sprintf("AND (m.full_address ILIKE $%d OR m.address ILIKE $%d)", placeholder, placeholder))
	}
	if domain != "" {
		add("AND split_part(m.full_address, '@', 2) = $%d", domain)
	}
	if keepForeverOnly {
		clauses = append(clauses, "AND m.keep_forever = TRUE")
	}
	if expiringWithinHours > 0 {
		add("AND m.keep_forever = FALSE AND m.expires_at > NOW() AND m.expires_at <= NOW() + make_interval(hours => $%d)", expiringWithinHours)
		// The value is used in make_interval; the predicate itself is complete.
	}
	return strings.Join(clauses, "\n  "), args
}

func (s *Store) LookupMailboxScoped(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, fullAddress string) (*model.Mailbox, error) {
	fullAddress = strings.ToLower(strings.TrimSpace(fullAddress))
	sql := `SELECT id, account_id, address, domain_id, full_address, created_at, expires_at, keep_forever
		FROM mailboxes
		WHERE full_address = $1`
	args := []any{fullAddress}
	if !isAdmin {
		sql += ` AND account_id = $2`
		args = append(args, accountID)
	}
	if creatorTokenID != nil {
		sql += fmt.Sprintf(` AND creator_token_id = $%d`, len(args)+1)
		args = append(args, *creatorTokenID)
	}
	sql += ` ORDER BY created_at DESC LIMIT 1`

	var m model.Mailbox
	if err := s.pool.QueryRow(ctx, sql, args...).Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt, &m.KeepForever); err != nil {
		return nil, err
	}
	return &m, nil
}

// LookupMailboxProjectionScoped resolves mailbox ownership and its valid latest
// projection in one database round trip. A projection is returned only while
// latest_email_id still references an email in the same mailbox. This keeps the
// read path correct if an older deployment left a stale projection behind; the
// caller can fall back to the indexed emails(mailbox_id, received_at) lookup.
func (s *Store) LookupMailboxProjectionScoped(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, fullAddress string) (*model.Mailbox, *model.MailboxState, error) {
	fullAddress = strings.ToLower(strings.TrimSpace(fullAddress))
	query := `
		SELECT m.id, m.account_id, m.address, m.domain_id, m.full_address,
		       m.created_at, m.expires_at, m.keep_forever,
		       projected.id IS NOT NULL,
		       COALESCE(state.mailbox_id, m.id),
		       COALESCE(state.account_id, m.account_id),
		       COALESCE(state.domain_id, m.domain_id),
		       COALESCE(NULLIF(state.domain_name, ''), split_part(m.full_address, '@', 2)),
		       COALESCE(state.full_address, m.full_address),
		       state.latest_email_id,
		       COALESCE(state.latest_sender, ''),
		       COALESCE(state.latest_subject, ''),
		       COALESCE(state.latest_code, ''),
		       COALESCE(state.latest_code_source, ''),
		       COALESCE(state.latest_link, ''),
		       COALESCE(state.latest_link_source, ''),
		       state.latest_received_at,
		       COALESCE(state.email_count, 0),
		       COALESCE(state.expires_at, m.expires_at),
		       COALESCE(state.keep_forever, m.keep_forever),
		       COALESCE(state.created_at, m.created_at),
		       COALESCE(state.updated_at, m.created_at)
		FROM mailboxes AS m
		LEFT JOIN mailbox_state AS state ON state.mailbox_id = m.id
		LEFT JOIN emails AS projected
		  ON projected.id = state.latest_email_id
		 AND projected.mailbox_id = m.id
		WHERE m.full_address = $1`
	args := []any{fullAddress}
	if !isAdmin {
		args = append(args, accountID)
		query += fmt.Sprintf(` AND m.account_id = $%d`, len(args))
	}
	if creatorTokenID != nil {
		args = append(args, *creatorTokenID)
		query += fmt.Sprintf(` AND m.creator_token_id = $%d`, len(args))
	}
	query += ` LIMIT 1`

	var mailbox model.Mailbox
	var state model.MailboxState
	var validProjection bool
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&mailbox.ID, &mailbox.AccountID, &mailbox.Address, &mailbox.DomainID,
		&mailbox.FullAddress, &mailbox.CreatedAt, &mailbox.ExpiresAt, &mailbox.KeepForever,
		&validProjection,
		&state.MailboxID, &state.AccountID, &state.DomainID, &state.DomainName,
		&state.FullAddress, &state.LatestEmailID, &state.LatestSender, &state.LatestSubject,
		&state.LatestCode, &state.LatestCodeSource, &state.LatestLink, &state.LatestLinkSource,
		&state.LatestReceivedAt, &state.EmailCount, &state.ExpiresAt, &state.KeepForever,
		&state.CreatedAt, &state.UpdatedAt,
	)
	if err != nil {
		return nil, nil, err
	}
	if !validProjection {
		return &mailbox, nil, nil
	}
	return &mailbox, &state, nil
}

func (s *Store) SetManyMailboxesKeepForever(ctx context.Context, accountID uuid.UUID, creatorTokenID *uuid.UUID, ids []uuid.UUID, keepForever bool, ttlMinutes int) ([]model.Mailbox, error) {
	if len(ids) == 0 {
		return []model.Mailbox{}, nil
	}

	// Update the base row and its mailbox_state projection in one statement.
	// This replaces the previous 2*N round trips and makes a batch atomic from
	// the caller's point of view. WITH ORDINALITY keeps the response order
	// stable without relying on UUID ordering.
	rows, err := s.pool.Query(ctx, `
		WITH requested AS (
			SELECT id, ord
			FROM unnest($1::uuid[]) WITH ORDINALITY AS input(id, ord)
		), updated AS (
			UPDATE mailboxes AS m
			SET keep_forever = $4,
				expires_at = CASE
					WHEN $4 THEN NULL
					WHEN $5 <= 0 THEN NULL
					ELSE NOW() + make_interval(mins => $5)
				END
			FROM requested AS r
			WHERE m.id = r.id
			  AND m.account_id = $2
			  AND ($3::uuid IS NULL OR m.creator_token_id = $3)
			RETURNING m.id, m.account_id, m.address, m.domain_id, m.full_address,
			          m.created_at, m.expires_at, m.keep_forever
		), state_updated AS (
			UPDATE mailbox_state AS state
			SET expires_at = updated.expires_at,
				keep_forever = updated.keep_forever,
				updated_at = NOW()
			FROM updated
			WHERE state.mailbox_id = updated.id
		)
		SELECT updated.id, updated.account_id, updated.address, updated.domain_id,
		       updated.full_address, updated.created_at, updated.expires_at,
		       updated.keep_forever
		FROM updated
		JOIN requested ON requested.id = updated.id
		ORDER BY requested.ord
	`, ids, accountID, creatorTokenID, keepForever, ttlMinutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[model.Mailbox])
	if err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (s *Store) GetLatestEmailForMailbox(ctx context.Context, mailboxID uuid.UUID) (*model.Email, error) {
	var e model.Email
	if err := s.pool.QueryRow(ctx,
		`SELECT id, mailbox_id, sender, subject, body_text, body_html, raw_message, message_id, headers_json::text, has_attachments, raw_path, raw_retention_until, processed_at, size_bytes, received_at
		 FROM emails
		 WHERE mailbox_id = $1
		 ORDER BY received_at DESC
		 LIMIT 1`,
		mailboxID,
	).Scan(&e.ID, &e.MailboxID, &e.Sender, &e.Subject, &e.BodyText, &e.BodyHTML, &e.RawMessage, &e.MessageID, &e.HeadersJSON, &e.HasAttachments, &e.RawPath, &e.RawRetentionUntil, &e.ProcessedAt, &e.SizeBytes, &e.ReceivedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// LookupLatestEmailScoped resolves ownership and the latest full email in one
// round trip. LEFT JOIN LATERAL preserves the mailbox row when the inbox is
// empty, allowing callers to distinguish "mailbox not found" from "no email".
func (s *Store) LookupLatestEmailScoped(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, fullAddress string) (*model.Mailbox, *model.Email, error) {
	fullAddress = strings.ToLower(strings.TrimSpace(fullAddress))
	query := `
		SELECT m.id, m.account_id, m.address, m.domain_id, m.full_address,
		       m.created_at, m.expires_at, m.keep_forever,
		       latest.id,
		       COALESCE(latest.mailbox_id, m.id),
		       COALESCE(latest.sender, ''),
		       COALESCE(latest.subject, ''),
		       COALESCE(latest.body_text, ''),
		       COALESCE(latest.body_html, ''),
		       COALESCE(latest.raw_message, ''),
		       COALESCE(latest.message_id, ''),
		       COALESCE(latest.headers_json::text, '{}'),
		       COALESCE(latest.has_attachments, FALSE),
		       COALESCE(latest.raw_path, ''),
		       latest.raw_retention_until,
		       latest.processed_at,
		       COALESCE(latest.parsed_code, ''),
		       COALESCE(latest.parsed_code_source, ''),
		       COALESCE(latest.parsed_link, ''),
		       COALESCE(latest.parsed_link_source, ''),
		       COALESCE(latest.size_bytes, 0),
		       latest.received_at
		FROM mailboxes AS m
		LEFT JOIN LATERAL (
			SELECT e.id, e.mailbox_id, e.sender, e.subject, e.body_text, e.body_html,
			       e.raw_message, e.message_id, e.headers_json, e.has_attachments,
			       e.raw_path, e.raw_retention_until, e.processed_at,
			       e.parsed_code, e.parsed_code_source, e.parsed_link,
			       e.parsed_link_source, e.size_bytes, e.received_at
			FROM emails AS e
			WHERE e.mailbox_id = m.id
			ORDER BY e.received_at DESC, e.id DESC
			LIMIT 1
		) AS latest ON TRUE
		WHERE m.full_address = $1`
	args := []any{fullAddress}
	if !isAdmin {
		args = append(args, accountID)
		query += fmt.Sprintf(` AND m.account_id = $%d`, len(args))
	}
	if creatorTokenID != nil {
		args = append(args, *creatorTokenID)
		query += fmt.Sprintf(` AND m.creator_token_id = $%d`, len(args))
	}
	query += ` LIMIT 1`

	var mailbox model.Mailbox
	var email model.Email
	var emailID *uuid.UUID
	var receivedAt *time.Time
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&mailbox.ID, &mailbox.AccountID, &mailbox.Address, &mailbox.DomainID,
		&mailbox.FullAddress, &mailbox.CreatedAt, &mailbox.ExpiresAt, &mailbox.KeepForever,
		&emailID, &email.MailboxID, &email.Sender, &email.Subject, &email.BodyText,
		&email.BodyHTML, &email.RawMessage, &email.MessageID, &email.HeadersJSON,
		&email.HasAttachments, &email.RawPath, &email.RawRetentionUntil, &email.ProcessedAt,
		&email.ParsedCode, &email.ParsedCodeSource, &email.ParsedLink, &email.ParsedLinkSource,
		&email.SizeBytes, &receivedAt,
	)
	if err != nil {
		return nil, nil, err
	}
	if emailID == nil || receivedAt == nil {
		return &mailbox, nil, nil
	}
	email.ID = *emailID
	email.ReceivedAt = *receivedAt
	return &mailbox, &email, nil
}

func (s *Store) PurgeMailboxes(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, q, domain string, onlyEmpty, onlyExpired bool) (int64, error) {
	q = strings.TrimSpace(q)
	domain = strings.ToLower(strings.TrimSpace(domain))
	where, args := buildMailboxCleanupWhere(accountID, isAdmin, creatorTokenID, q, domain)
	where = append(where, "m.keep_forever = FALSE")
	if onlyEmpty {
		where = append(where, "NOT EXISTS (SELECT 1 FROM emails e WHERE e.mailbox_id = m.id)")
	}
	if onlyExpired {
		where = append(where, "m.expires_at < NOW()")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM mailboxes m WHERE `+strings.Join(where, "\n  AND "), args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) PreviewPurgeMailboxes(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, q, domain string, onlyEmpty, onlyExpired bool) (int64, error) {
	q = strings.TrimSpace(q)
	domain = strings.ToLower(strings.TrimSpace(domain))
	where, args := buildMailboxCleanupWhere(accountID, isAdmin, creatorTokenID, q, domain)
	where = append(where, "m.keep_forever = FALSE")
	if onlyEmpty {
		where = append(where, "NOT EXISTS (SELECT 1 FROM emails e WHERE e.mailbox_id = m.id)")
	}
	if onlyExpired {
		where = append(where, "m.expires_at < NOW()")
	}
	var count int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM mailboxes m WHERE `+strings.Join(where, "\n  AND "), args...).Scan(&count)
	return count, err
}

func (s *Store) PurgeEmails(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, q, domain string, olderThanMinutes int) (int64, error) {
	q = strings.TrimSpace(q)
	domain = strings.ToLower(strings.TrimSpace(domain))
	where, args := buildMailboxCleanupWhere(accountID, isAdmin, creatorTokenID, q, domain)
	if olderThanMinutes > 0 {
		args = append(args, olderThanMinutes)
		where = append(where, fmt.Sprintf("e.received_at < NOW() - make_interval(mins => $%d)", len(args)))
	}
	where = append([]string{"e.mailbox_id = m.id"}, where...)
	return s.deleteEmailsAndRefreshProjection(ctx,
		`DELETE FROM emails AS e USING mailboxes AS m WHERE `+strings.Join(where, "\n  AND ")+`
		 RETURNING e.id, e.mailbox_id`,
		args...,
	)
}

func (s *Store) PreviewPurgeEmails(ctx context.Context, accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, q, domain string, olderThanMinutes int) (int64, int64, error) {
	q = strings.TrimSpace(q)
	domain = strings.ToLower(strings.TrimSpace(domain))
	where, args := buildMailboxCleanupWhere(accountID, isAdmin, creatorTokenID, q, domain)
	if olderThanMinutes > 0 {
		args = append(args, olderThanMinutes)
		where = append(where, fmt.Sprintf("e.received_at < NOW() - make_interval(mins => $%d)", len(args)))
	}
	where = append([]string{"e.mailbox_id = m.id"}, where...)
	var count, bytes int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(e.size_bytes), 0) FROM emails e JOIN mailboxes m ON m.id = e.mailbox_id WHERE `+strings.Join(where, "\n  AND "), args...).Scan(&count, &bytes)
	return count, bytes, err
}

// buildMailboxCleanupWhere builds the shared ownership and text filters for
// destructive maintenance operations. Omitting disabled filters keeps the
// account/creator predicates visible to PostgreSQL's planner and avoids the
// broad OR expressions used by the old implementation.
func buildMailboxCleanupWhere(accountID uuid.UUID, isAdmin bool, creatorTokenID *uuid.UUID, q, domain string) ([]string, []any) {
	where := make([]string, 0, 6)
	args := make([]any, 0, 5)
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if !isAdmin {
		add("m.account_id = $%d", accountID)
	}
	if creatorTokenID != nil {
		add("m.creator_token_id = $%d", *creatorTokenID)
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		placeholder := len(args)
		where = append(where, fmt.Sprintf("(m.full_address ILIKE $%d OR m.address ILIKE $%d)", placeholder, placeholder))
	}
	if domain != "" {
		add("split_part(m.full_address, '@', 2) = $%d", domain)
	}
	return where, args
}

func (s *Store) ListRecentCodeActivity(ctx context.Context, accountID uuid.UUID, limit int) ([]model.RecentCodeActivity, error) {
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.mailbox_id, e.id, m.full_address, e.sender, e.subject, e.body_text, e.body_html, e.received_at
		FROM emails e
		JOIN mailboxes m ON m.id = e.mailbox_id
		WHERE m.account_id = $1
		ORDER BY e.received_at DESC
		LIMIT $2
	`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.RecentCodeActivity
	for rows.Next() {
		var item model.RecentCodeActivity
		if err := rows.Scan(&item.MailboxID, &item.EmailID, &item.FullAddress, &item.Sender, &item.Subject, &item.BodyText, &item.BodyHTML, &item.ReceivedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
