package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type APIRequestEvent struct {
	TokenID         uuid.UUID
	Method          string
	Route           string
	Status          int
	LatencyMS       int
	RequestID       string
	CreatedAt       time.Time
	CountTokenUsage bool
}

type APIUsageSummary struct {
	TotalRequests int64   `json:"total_requests"`
	ErrorRequests int64   `json:"error_requests"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
	P95LatencyMS  float64 `json:"p95_latency_ms"`
}

type APIUsageBucket struct {
	Hour          time.Time `json:"hour"`
	TotalRequests int64     `json:"total_requests"`
	ErrorRequests int64     `json:"error_requests"`
	AvgLatencyMS  float64   `json:"avg_latency_ms"`
}

type APIUsageRoute struct {
	Method        string  `json:"method"`
	Route         string  `json:"route"`
	TotalRequests int64   `json:"total_requests"`
	ErrorRequests int64   `json:"error_requests"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
}

type APIUsageError struct {
	CreatedAt time.Time `json:"created_at"`
	TokenName string    `json:"token_name"`
	Method    string    `json:"method"`
	Route     string    `json:"route"`
	Status    int       `json:"status_code"`
	LatencyMS int       `json:"latency_ms"`
	RequestID string    `json:"request_id"`
}

type APIUsageReport struct {
	Hours        int              `json:"hours"`
	Summary      APIUsageSummary  `json:"summary"`
	Buckets      []APIUsageBucket `json:"buckets"`
	Routes       []APIUsageRoute  `json:"routes"`
	RecentErrors []APIUsageError  `json:"recent_errors"`
}

type MaintenancePreview struct {
	ExpiredMailboxes int64 `json:"expired_mailboxes"`
	EmptyMailboxes   int64 `json:"empty_mailboxes"`
	OldEmails        int64 `json:"old_emails"`
	OldEmailBytes    int64 `json:"old_email_bytes"`
	OlderThanMinutes int   `json:"older_than_minutes"`
}

func (s *Store) RecordAPIRequestEvents(ctx context.Context, events []APIRequestEvent) error {
	if len(events) == 0 {
		return nil
	}
	query, args := buildAPIRequestEventInsert(events)
	hasDeferredUsage := false
	for _, event := range events {
		if event.CountTokenUsage {
			hasDeferredUsage = true
			break
		}
	}
	if !hasDeferredUsage {
		_, err := s.pool.Exec(ctx, query, args...)
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return err
	}
	if err := recordDeferredTokenUsage(ctx, tx, events); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func buildAPIRequestEventInsert(events []APIRequestEvent) (string, []any) {
	var query strings.Builder
	query.WriteString(`INSERT INTO api_request_events (token_id, method, route, status_code, latency_ms, request_id, created_at)
	SELECT event.token_id, event.method, event.route, event.status_code, event.latency_ms, event.request_id, event.created_at
	FROM (VALUES `)
	args := make([]any, 0, len(events)*7)
	for index, event := range events {
		if index > 0 {
			query.WriteByte(',')
		}
		base := index * 7
		fmt.Fprintf(&query, "($%d::uuid,$%d::text,$%d::text,$%d::integer,$%d::integer,$%d::text,$%d::timestamptz)", base+1, base+2, base+3, base+4, base+5, base+6, base+7)
		args = append(args, event.TokenID, event.Method, event.Route, event.Status, event.LatencyMS, event.RequestID, event.CreatedAt)
	}
	query.WriteString(`) AS event(token_id, method, route, status_code, latency_ms, request_id, created_at)
	JOIN account_tokens AS token ON token.id = event.token_id`)
	return query.String(), args
}

type deferredTokenTotal struct {
	TokenID    uuid.UUID
	Count      int64
	LastUsedAt time.Time
}

type deferredTokenDay struct {
	TokenID uuid.UUID
	Day     string
	Count   int64
}

func recordDeferredTokenUsage(ctx context.Context, tx pgx.Tx, events []APIRequestEvent) error {
	totals := make(map[uuid.UUID]deferredTokenTotal)
	days := make(map[string]deferredTokenDay)
	shanghai := time.FixedZone("CST", 8*60*60)
	for _, event := range events {
		if !event.CountTokenUsage {
			continue
		}
		total := totals[event.TokenID]
		total.TokenID = event.TokenID
		total.Count++
		if event.CreatedAt.After(total.LastUsedAt) {
			total.LastUsedAt = event.CreatedAt
		}
		totals[event.TokenID] = total

		day := event.CreatedAt.In(shanghai).Format("2006-01-02")
		key := event.TokenID.String() + ":" + day
		daily := days[key]
		daily.TokenID = event.TokenID
		daily.Day = day
		daily.Count++
		days[key] = daily
	}
	if len(totals) == 0 {
		return nil
	}

	totalItems := make([]deferredTokenTotal, 0, len(totals))
	for _, item := range totals {
		totalItems = append(totalItems, item)
	}
	sort.Slice(totalItems, func(i, j int) bool { return totalItems[i].TokenID.String() < totalItems[j].TokenID.String() })
	var totalQuery strings.Builder
	totalQuery.WriteString(`UPDATE account_tokens AS token SET
		request_count_total = token.request_count_total + usage.count,
		last_used_at = CASE WHEN token.last_used_at IS NULL OR token.last_used_at < usage.last_used_at THEN usage.last_used_at ELSE token.last_used_at END,
		updated_at = NOW()
	FROM (VALUES `)
	totalArgs := make([]any, 0, len(totalItems)*3)
	for index, item := range totalItems {
		if index > 0 {
			totalQuery.WriteByte(',')
		}
		base := index * 3
		fmt.Fprintf(&totalQuery, "($%d::uuid,$%d::bigint,$%d::timestamptz)", base+1, base+2, base+3)
		totalArgs = append(totalArgs, item.TokenID, item.Count, item.LastUsedAt)
	}
	totalQuery.WriteString(`) AS usage(token_id, count, last_used_at)
	WHERE token.id = usage.token_id AND token.revoked_at IS NULL`)
	if _, err := tx.Exec(ctx, totalQuery.String(), totalArgs...); err != nil {
		return err
	}

	dayItems := make([]deferredTokenDay, 0, len(days))
	for _, item := range days {
		dayItems = append(dayItems, item)
	}
	sort.Slice(dayItems, func(i, j int) bool {
		if dayItems[i].TokenID == dayItems[j].TokenID {
			return dayItems[i].Day < dayItems[j].Day
		}
		return dayItems[i].TokenID.String() < dayItems[j].TokenID.String()
	})
	var dailyQuery strings.Builder
	dailyQuery.WriteString(`INSERT INTO api_request_daily (day, token_id, account_id, count)
	SELECT usage.day, token.id, token.account_id, usage.count
	FROM (VALUES `)
	dailyArgs := make([]any, 0, len(dayItems)*3)
	for index, item := range dayItems {
		if index > 0 {
			dailyQuery.WriteByte(',')
		}
		base := index * 3
		fmt.Fprintf(&dailyQuery, "($%d::uuid,$%d::date,$%d::bigint)", base+1, base+2, base+3)
		dailyArgs = append(dailyArgs, item.TokenID, item.Day, item.Count)
	}
	dailyQuery.WriteString(`) AS usage(token_id, day, count)
	JOIN account_tokens AS token ON token.id = usage.token_id AND token.revoked_at IS NULL
	ON CONFLICT (day, token_id) DO UPDATE SET count = api_request_daily.count + EXCLUDED.count`)
	_, err := tx.Exec(ctx, dailyQuery.String(), dailyArgs...)
	return err
}

func (s *Store) DeleteOldAPIRequestEvents(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM api_request_events WHERE created_at < NOW() - INTERVAL '14 days'`)
	return err
}

func (s *Store) GetAPIUsageReport(ctx context.Context, hours int) (APIUsageReport, error) {
	if hours < 1 {
		hours = 24
	}
	if hours > 24*14 {
		hours = 24 * 14
	}
	report := APIUsageReport{Hours: hours, Buckets: []APIUsageBucket{}, Routes: []APIUsageRoute{}, RecentErrors: []APIUsageError{}}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400),
		       COALESCE(AVG(latency_ms), 0), COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)
		FROM api_request_events WHERE created_at >= NOW() - make_interval(hours => $1)
	`, hours).Scan(&report.Summary.TotalRequests, &report.Summary.ErrorRequests, &report.Summary.AvgLatencyMS, &report.Summary.P95LatencyMS); err != nil {
		return report, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('hour', created_at), COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400), COALESCE(AVG(latency_ms), 0)
		FROM api_request_events WHERE created_at >= NOW() - make_interval(hours => $1)
		GROUP BY 1 ORDER BY 1
	`, hours)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var item APIUsageBucket
		if err := rows.Scan(&item.Hour, &item.TotalRequests, &item.ErrorRequests, &item.AvgLatencyMS); err != nil {
			rows.Close()
			return report, err
		}
		report.Buckets = append(report.Buckets, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return report, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT method, route, COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400), COALESCE(AVG(latency_ms), 0)
		FROM api_request_events WHERE created_at >= NOW() - make_interval(hours => $1)
		GROUP BY method, route ORDER BY COUNT(*) DESC LIMIT 12
	`, hours)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var item APIUsageRoute
		if err := rows.Scan(&item.Method, &item.Route, &item.TotalRequests, &item.ErrorRequests, &item.AvgLatencyMS); err != nil {
			rows.Close()
			return report, err
		}
		report.Routes = append(report.Routes, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return report, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT e.created_at, COALESCE(NULLIF(t.name, ''), t.token_prefix), e.method, e.route, e.status_code, e.latency_ms, e.request_id
		FROM api_request_events e JOIN account_tokens t ON t.id = e.token_id
		WHERE e.created_at >= NOW() - make_interval(hours => $1) AND e.status_code >= 400
		ORDER BY e.created_at DESC LIMIT 20
	`, hours)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var item APIUsageError
		if err := rows.Scan(&item.CreatedAt, &item.TokenName, &item.Method, &item.Route, &item.Status, &item.LatencyMS, &item.RequestID); err != nil {
			return report, err
		}
		report.RecentErrors = append(report.RecentErrors, item)
	}
	return report, rows.Err()
}

func (s *Store) PreviewMaintenance(ctx context.Context, olderThanMinutes int) (MaintenancePreview, error) {
	if olderThanMinutes < 1 {
		olderThanMinutes = 240
	}
	if olderThanMinutes > 525600 {
		olderThanMinutes = 525600
	}
	preview := MaintenancePreview{OlderThanMinutes: olderThanMinutes}
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM mailboxes WHERE keep_forever = FALSE AND expires_at < NOW()),
			(SELECT COUNT(*) FROM mailboxes m WHERE keep_forever = FALSE AND NOT EXISTS (SELECT 1 FROM emails e WHERE e.mailbox_id = m.id)),
			(SELECT COUNT(*) FROM emails WHERE received_at < NOW() - make_interval(mins => $1)),
			(SELECT COALESCE(SUM(size_bytes), 0) FROM emails WHERE received_at < NOW() - make_interval(mins => $1))
	`, olderThanMinutes).Scan(&preview.ExpiredMailboxes, &preview.EmptyMailboxes, &preview.OldEmails, &preview.OldEmailBytes)
	return preview, err
}
