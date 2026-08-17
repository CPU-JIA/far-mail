package store

import (
	"context"

	"farmail/model"
)

func (s *Store) GetAnalyticsSummary(ctx context.Context, windowDays int) (model.AnalyticsSummary, []model.AnalyticsDay, error) {
	if windowDays != 14 && windowDays != 30 {
		windowDays = 7
	}
	var summary model.AnalyticsSummary
	err := s.pool.QueryRow(ctx, `
		WITH bounds AS (
			SELECT
				NOW() - INTERVAL '24 hours' AS last_24h,
				NOW() - INTERVAL '7 days' AS last_7d,
				(NOW() AT TIME ZONE 'Asia/Shanghai')::date AS local_day
		), mailbox_stats AS (
			SELECT
				COUNT(*) AS mailbox_total,
				COUNT(*) FILTER (WHERE keep_forever = TRUE) AS permanent_mailbox_total
			FROM mailboxes
		), email_stats AS (
			SELECT
				COUNT(*) AS email_total,
				COUNT(*) FILTER (WHERE e.received_at >= bounds.last_24h) AS email_last_24h,
				COUNT(*) FILTER (WHERE e.received_at >= bounds.last_7d) AS email_last_7d,
				COUNT(*) FILTER (WHERE e.parsed_code <> '') AS code_email_total,
				COUNT(*) FILTER (WHERE e.parsed_link <> '') AS link_email_total,
				COALESCE(SUM(e.size_bytes), 0) AS storage_bytes,
				COUNT(*) FILTER (WHERE e.raw_path <> '') AS raw_file_references
			FROM emails AS e
			CROSS JOIN bounds
		), domain_stats AS (
			SELECT
				COUNT(*) AS domain_total,
				COUNT(*) FILTER (WHERE is_active = TRUE) AS active_domain_total,
				COUNT(*) FILTER (WHERE status = 'pending') AS pending_domain_total
			FROM domains
		), token_stats AS (
			SELECT COUNT(*) AS active_token_total
			FROM account_tokens
			WHERE revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > NOW())
		), request_stats AS (
			SELECT COALESCE(SUM(daily.count), 0) AS token_calls_today
			FROM api_request_daily AS daily
			CROSS JOIN bounds
			WHERE daily.day = bounds.local_day
		)
		SELECT
			mailbox_stats.mailbox_total,
			mailbox_stats.permanent_mailbox_total,
			email_stats.email_total,
			email_stats.email_last_24h,
			email_stats.email_last_7d,
			email_stats.code_email_total,
			email_stats.link_email_total,
			email_stats.storage_bytes,
			email_stats.raw_file_references,
			domain_stats.domain_total,
			domain_stats.active_domain_total,
			domain_stats.pending_domain_total,
			token_stats.active_token_total,
			request_stats.token_calls_today
		FROM mailbox_stats
		CROSS JOIN email_stats
		CROSS JOIN domain_stats
		CROSS JOIN token_stats
		CROSS JOIN request_stats
	`).Scan(
		&summary.MailboxTotal,
		&summary.PermanentMailboxTotal,
		&summary.EmailTotal,
		&summary.EmailLast24Hours,
		&summary.EmailLast7Days,
		&summary.CodeEmailTotal,
		&summary.LinkEmailTotal,
		&summary.StorageBytes,
		&summary.RawFileReferences,
		&summary.DomainTotal,
		&summary.ActiveDomainTotal,
		&summary.PendingDomainTotal,
		&summary.ActiveTokenTotal,
		&summary.TokenCallsToday,
	)
	if err != nil {
		return summary, nil, err
	}

	rows, err := s.pool.Query(ctx, `
		WITH bounds AS (
			SELECT
				(NOW() AT TIME ZONE 'Asia/Shanghai')::date AS today,
				(((NOW() AT TIME ZONE 'Asia/Shanghai')::date - ($1::int - 1))::timestamp AT TIME ZONE 'Asia/Shanghai') AS start_at
		), days AS (
			SELECT generate_series(
				bounds.today - make_interval(days => $1::int - 1),
				bounds.today,
				INTERVAL '1 day'
			)::date AS day
			FROM bounds
		), mailbox_daily AS (
			SELECT
				(m.created_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
				COUNT(*) AS count
			FROM mailboxes AS m
			CROSS JOIN bounds
			WHERE m.created_at >= bounds.start_at
			GROUP BY 1
		), email_daily AS (
			SELECT
				(e.received_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
				COUNT(*) AS count,
				COUNT(*) FILTER (WHERE e.parsed_code <> '') AS code_count
			FROM emails AS e
			CROSS JOIN bounds
			WHERE e.received_at >= bounds.start_at
			GROUP BY 1
		)
		SELECT
			to_char(days.day, 'YYYY-MM-DD'),
			COALESCE(mailbox_daily.count, 0),
			COALESCE(email_daily.count, 0),
			COALESCE(email_daily.code_count, 0)
		FROM days
		LEFT JOIN mailbox_daily ON mailbox_daily.day = days.day
		LEFT JOIN email_daily ON email_daily.day = days.day
		ORDER BY days.day
	`, windowDays)
	if err != nil {
		return summary, nil, err
	}
	defer rows.Close()
	days := make([]model.AnalyticsDay, 0, windowDays)
	for rows.Next() {
		var item model.AnalyticsDay
		if err := rows.Scan(&item.Day, &item.Mailboxes, &item.Emails, &item.Codes); err != nil {
			return summary, nil, err
		}
		days = append(days, item)
	}
	if err := rows.Err(); err != nil {
		return summary, nil, err
	}
	return summary, days, nil
}
