package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"farmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrDonationConflict   = errors.New("domain already has a donation claim")
	ErrDonationTokenLimit = errors.New("donation token reached its domain limit")
)

type DonationPolicy struct {
	RateLimitPerMinute int
	DailyRequestLimit  int
	TotalRequestLimit  int64
	MaxDomainsPerToken int
}

type DonationVerification struct {
	Valid     bool
	Transient bool
	Status    string
}

type rowScanner interface {
	Scan(dest ...any) error
}

const donationSelect = `
	SELECT d.id, d.domain_id, dom.domain, d.token_id, t.token_prefix,
	       d.include_subdomains, d.challenge_token, d.status, d.reward_active,
	       d.reward_rate_limit_per_minute, d.reward_daily_request_limit, d.reward_total_request_limit,
	       d.failure_count, d.last_error, d.last_checked_at, d.activated_at, d.reward_revoked_at,
	       d.created_at, d.updated_at,
	       t.rate_limit_per_minute, t.daily_request_limit, t.total_request_limit, t.request_count_total
	FROM domain_donations d
	JOIN domains dom ON dom.id = d.domain_id
	JOIN account_tokens t ON t.id = d.token_id
`

func scanDonation(row rowScanner) (*model.DomainDonation, error) {
	var item model.DomainDonation
	err := row.Scan(
		&item.ID, &item.DomainID, &item.Domain, &item.TokenID, &item.TokenPrefix,
		&item.IncludeSubdomains, &item.ChallengeToken, &item.Status, &item.RewardActive,
		&item.RewardRPM, &item.RewardDaily, &item.RewardTotal,
		&item.FailureCount, &item.LastError, &item.LastCheckedAt, &item.ActivatedAt, &item.RewardRevokedAt,
		&item.CreatedAt, &item.UpdatedAt,
		&item.EffectiveRPM, &item.EffectiveDaily, &item.EffectiveTotal, &item.RequestCountTotal,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func randomHex(byteLength int) string {
	b := make([]byte, byteLength)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) DonationPolicy(ctx context.Context) DonationPolicy {
	policy := DonationPolicy{
		RateLimitPerMinute: s.intSetting(ctx, "donation_reward_rate_limit_per_minute", 30),
		DailyRequestLimit:  s.intSetting(ctx, "donation_reward_daily_request_limit", 5000),
		TotalRequestLimit:  s.int64Setting(ctx, "donation_reward_total_request_limit", 100000),
		MaxDomainsPerToken: s.intSetting(ctx, "donation_max_domains_per_token", 10),
	}
	if policy.RateLimitPerMinute < 1 {
		policy.RateLimitPerMinute = 1
	}
	if policy.DailyRequestLimit < 0 {
		policy.DailyRequestLimit = 0
	}
	if policy.TotalRequestLimit < 1 {
		policy.TotalRequestLimit = 1
	}
	if policy.MaxDomainsPerToken < 1 {
		policy.MaxDomainsPerToken = 1
	}
	return policy
}

// CreateDonationRequest creates the claim, reward token and domain row in one
// transaction. Existing reward tokens can collect multiple domain grants.
func (s *Store) CreateDonationRequest(ctx context.Context, domain string, includeSubdomains bool, existingToken string) (*model.DomainDonation, string, string, error) {
	policy := s.DonationPolicy(ctx)
	domain = strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	if domain == "" {
		return nil, "", "", fmt.Errorf("domain is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE is_admin = TRUE AND is_active = TRUE ORDER BY created_at, id LIMIT 1`).Scan(&accountID); err != nil {
		return nil, "", "", err
	}

	var tokenID uuid.UUID
	var rawToken, prefix string
	existingToken = strings.TrimSpace(existingToken)
	if existingToken != "" {
		if !isAccessTokenFormat(existingToken) {
			return nil, "", "", pgx.ErrNoRows
		}
		if err := tx.QueryRow(ctx, `
			SELECT id, token_prefix
			FROM account_tokens
			WHERE account_id = $1 AND token_hash = $2 AND token_kind = 'donation'
			FOR UPDATE
		`, accountID, hashToken(existingToken)).Scan(&tokenID, &prefix); err != nil {
			return nil, "", "", err
		}
	} else {
		rawToken = generateAccessToken()
		prefix = tokenPrefix(rawToken)
		if err := tx.QueryRow(ctx, `
			INSERT INTO account_tokens (
				account_id, name, token_hash, token_prefix, scope, is_primary, token_kind,
				rate_limit_per_minute, daily_request_limit, total_request_limit
			) VALUES ($1, 'Donation rewards', $2, $3, 'cleanup', FALSE, 'donation', 0, 0, 0)
			RETURNING id
		`, accountID, hashToken(rawToken), prefix).Scan(&tokenID); err != nil {
			return nil, "", "", err
		}
	}

	var domainCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM domain_donations WHERE token_id = $1 AND status <> 'revoked'`, tokenID).Scan(&domainCount); err != nil {
		return nil, "", "", err
	}
	if domainCount >= policy.MaxDomainsPerToken {
		return nil, "", "", ErrDonationTokenLimit
	}

	var domainID int
	err = tx.QueryRow(ctx, `
		INSERT INTO domains (domain, is_active, status, visibility, source_type)
		VALUES ($1, FALSE, 'pending', 'public', 'donated')
		ON CONFLICT (domain) DO NOTHING
		RETURNING id
	`, domain).Scan(&domainID)
	if errors.Is(err, pgx.ErrNoRows) {
		var sourceType string
		if err := tx.QueryRow(ctx, `SELECT id, source_type FROM domains WHERE domain = $1`, domain).Scan(&domainID, &sourceType); err != nil {
			return nil, "", "", err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM domain_donations WHERE domain_id = $1)`, domainID).Scan(&exists); err != nil {
			return nil, "", "", err
		}
		if exists || sourceType != "donated" {
			return nil, "", "", ErrDonationConflict
		}
	} else if err != nil {
		return nil, "", "", err
	}

	claimSecret := randomHex(24)
	challenge := randomHex(16)
	var donationID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO domain_donations (
			domain_id, token_id, claim_secret_hash, challenge_token, include_subdomains,
			reward_rate_limit_per_minute, reward_daily_request_limit, reward_total_request_limit
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id
	`, domainID, tokenID, hashToken(claimSecret), challenge, includeSubdomains,
		policy.RateLimitPerMinute, policy.DailyRequestLimit, policy.TotalRequestLimit).Scan(&donationID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, "", "", ErrDonationConflict
		}
		return nil, "", "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE domains SET source_type = 'donated', visibility = 'public' WHERE id = $1`, domainID); err != nil {
		return nil, "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", "", err
	}

	item, err := s.GetDonation(ctx, donationID)
	if err != nil {
		return nil, "", "", err
	}
	return item, claimSecret, rawToken, nil
}

func (s *Store) GetDonation(ctx context.Context, id uuid.UUID) (*model.DomainDonation, error) {
	return scanDonation(s.pool.QueryRow(ctx, donationSelect+` WHERE d.id = $1`, id))
}

func (s *Store) GetDonationByClaim(ctx context.Context, id uuid.UUID, claimSecret string) (*model.DomainDonation, error) {
	return scanDonation(s.pool.QueryRow(ctx, donationSelect+` WHERE d.id = $1 AND d.claim_secret_hash = $2`, id, hashToken(claimSecret)))
}

func (s *Store) ListDonations(ctx context.Context) ([]model.DomainDonation, error) {
	rows, err := s.pool.Query(ctx, donationSelect+` ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.DomainDonation, 0, 32)
	for rows.Next() {
		item, err := scanDonation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) ListDonationRewardTokens(ctx context.Context) ([]model.DonationRewardToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.token_prefix,
		       COUNT(d.id),
		       COUNT(d.id) FILTER (WHERE d.status = 'active' AND d.reward_active),
		       COUNT(d.id) FILTER (WHERE d.status = 'pending'),
		       COUNT(d.id) FILTER (WHERE d.status IN ('inactive', 'revoked')),
		       t.rate_limit_per_minute, t.daily_request_limit, t.total_request_limit,
		       t.request_count_total, GREATEST(t.total_request_limit - t.request_count_total, 0),
		       CASE
		         WHEN t.revoked_at IS NOT NULL THEN 'revoked'
		         WHEN t.expires_at IS NOT NULL AND t.expires_at <= NOW() THEN 'expired'
		         WHEN COUNT(d.id) FILTER (WHERE d.status = 'active' AND d.reward_active) > 0
		              AND t.total_request_limit > t.request_count_total THEN 'active'
		         ELSE 'inactive'
		       END,
		       t.last_used_at, t.created_at
		FROM account_tokens t
		LEFT JOIN domain_donations d ON d.token_id = t.id
		WHERE t.token_kind = 'donation'
		GROUP BY t.id
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.DonationRewardToken, 0, 16)
	for rows.Next() {
		var item model.DonationRewardToken
		if err := rows.Scan(
			&item.ID, &item.TokenPrefix,
			&item.DomainCount, &item.ActiveDomainCount, &item.PendingDomainCount, &item.InactiveDomainCount,
			&item.RateLimitPerMinute, &item.DailyRequestLimit, &item.TotalRequestLimit,
			&item.RequestCountTotal, &item.RemainingTotal, &item.Status,
			&item.LastUsedAt, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListDonationRewardEvents(ctx context.Context, limit int) ([]model.DonationRewardEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.token_id, t.token_prefix, e.donation_id,
		       COALESCE(dom.domain, ''), e.event_type,
		       e.total_delta, e.daily_delta, e.rpm_delta, e.note, e.created_at
		FROM donation_reward_events e
		JOIN account_tokens t ON t.id = e.token_id AND t.token_kind = 'donation'
		LEFT JOIN domain_donations d ON d.id = e.donation_id
		LEFT JOIN domains dom ON dom.id = d.domain_id
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.DonationRewardEvent, 0, limit)
	for rows.Next() {
		var item model.DonationRewardEvent
		if err := rows.Scan(
			&item.ID, &item.TokenID, &item.TokenPrefix, &item.DonationID,
			&item.Domain, &item.EventType, &item.TotalDelta, &item.DailyDelta,
			&item.RPMDelta, &item.Note, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListDueDonations(ctx context.Context) ([]model.DomainDonation, error) {
	minutes := s.intSetting(ctx, "donation_recheck_minutes", 30)
	if minutes < 1 {
		minutes = 1
	}
	rows, err := s.pool.Query(ctx, donationSelect+`
		WHERE d.status <> 'revoked'
		  AND (
			(d.status = 'pending' AND (d.last_checked_at IS NULL OR d.last_checked_at < NOW() - INTERVAL '30 seconds'))
			OR (d.status IN ('active', 'inactive') AND (d.last_checked_at IS NULL OR d.last_checked_at < NOW() - make_interval(mins => $1)))
		  )
		ORDER BY d.last_checked_at NULLS FIRST
		LIMIT 100
	`, minutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.DomainDonation, 0, 32)
	for rows.Next() {
		item, err := scanDonation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) DonationSummary(ctx context.Context) (model.DonationSummary, error) {
	var summary model.DonationSummary
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'active' AND reward_active),
		       COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status IN ('inactive', 'revoked')),
		       COUNT(DISTINCT token_id),
	       COALESCE((SELECT SUM(total_request_limit) FROM account_tokens WHERE token_kind = 'donation'), 0),
	       COALESCE((SELECT SUM(request_count_total) FROM account_tokens WHERE token_kind = 'donation'), 0)
		FROM domain_donations
	`).Scan(
		&summary.TotalDonations, &summary.ActiveDonations, &summary.PendingDonations,
		&summary.InactiveDonations, &summary.RewardTokenTotal, &summary.EffectiveQuota, &summary.ConsumedQuota,
	)
	return summary, err
}

func (s *Store) ApplyDonationVerification(ctx context.Context, id uuid.UUID, result DonationVerification) (*model.DomainDonation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tokenID uuid.UUID
	var domainID int
	var status string
	var rewardActive bool
	var failureCount int
	var rpm, daily int
	var total int64
	err = tx.QueryRow(ctx, `
		SELECT token_id, domain_id, status, reward_active, failure_count,
		       reward_rate_limit_per_minute, reward_daily_request_limit, reward_total_request_limit
		FROM domain_donations WHERE id = $1 FOR UPDATE
	`, id).Scan(&tokenID, &domainID, &status, &rewardActive, &failureCount, &rpm, &daily, &total)
	if err != nil {
		return nil, err
	}
	if status == "revoked" {
		return nil, fmt.Errorf("donation reward is revoked")
	}
	// Reward events reference the shared Token and therefore acquire a key-share
	// lock. Take the stronger Token lock first so parallel domain checks cannot
	// deadlock while upgrading that foreign-key lock during recalculation.
	if err := lockDonationTokenTx(ctx, tx, tokenID); err != nil {
		return nil, err
	}

	if result.Valid {
		_, err = tx.Exec(ctx, `
			UPDATE domain_donations
			SET status = 'active', reward_active = TRUE, failure_count = 0, last_error = '',
			    last_checked_at = NOW(), activated_at = COALESCE(activated_at, NOW()),
			    reward_revoked_at = NULL, updated_at = NOW()
			WHERE id = $1
		`, id)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE domains SET is_active = TRUE, status = 'active', verified_at = COALESCE(verified_at, NOW()), mx_checked_at = NOW() WHERE id = $1`, domainID)
		}
		if err == nil && !rewardActive {
			_, err = tx.Exec(ctx, `
				INSERT INTO donation_reward_events (token_id, donation_id, event_type, total_delta, daily_delta, rpm_delta, note)
				VALUES ($1,$2,'grant',$3,$4,$5,$6)
			`, tokenID, id, total, daily, rpm, result.Status)
		}
	} else if rewardActive {
		failureCount++
		tolerance := s.intSetting(ctx, "donation_dns_failure_tolerance", 3)
		if tolerance < 1 {
			tolerance = 1
		}
		deactivate := !result.Transient || failureCount >= tolerance
		if deactivate {
			_, err = tx.Exec(ctx, `
				UPDATE domain_donations
				SET status = 'inactive', reward_active = FALSE, failure_count = $2, last_error = $3,
				    last_checked_at = NOW(), reward_revoked_at = NOW(), updated_at = NOW()
				WHERE id = $1
			`, id, failureCount, result.Status)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE domains SET is_active = FALSE, status = 'disabled', mx_checked_at = NOW() WHERE id = $1`, domainID)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `
					INSERT INTO donation_reward_events (token_id, donation_id, event_type, total_delta, daily_delta, rpm_delta, note)
					VALUES ($1,$2,'revoke',$3,$4,$5,$6)
				`, tokenID, id, -total, -daily, -rpm, result.Status)
			}
		} else {
			_, err = tx.Exec(ctx, `UPDATE domain_donations SET failure_count = $2, last_error = $3, last_checked_at = NOW(), updated_at = NOW() WHERE id = $1`, id, failureCount, result.Status)
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE domain_donations SET failure_count = failure_count + 1, last_error = $2, last_checked_at = NOW(), updated_at = NOW() WHERE id = $1`, id, result.Status)
	}
	if err != nil {
		return nil, err
	}
	if err := s.recalculateDonationTokenLockedTx(ctx, tx, tokenID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.invalidateTokenAuthCache(tokenID)
	s.invalidateActiveDomainsCache()
	return s.GetDonation(ctx, id)
}

func (s *Store) recalculateDonationTokenTx(ctx context.Context, tx pgx.Tx, tokenID uuid.UUID) error {
	if err := lockDonationTokenTx(ctx, tx, tokenID); err != nil {
		return err
	}
	return s.recalculateDonationTokenLockedTx(ctx, tx, tokenID)
}

func lockDonationTokenTx(ctx context.Context, tx pgx.Tx, tokenID uuid.UUID) error {
	var lockedTokenID uuid.UUID
	return tx.QueryRow(ctx, `
		SELECT id FROM account_tokens
		WHERE id = $1 AND token_kind = 'donation'
		FOR UPDATE
	`, tokenID).Scan(&lockedTokenID)
}

func (s *Store) recalculateDonationTokenLockedTx(ctx context.Context, tx pgx.Tx, tokenID uuid.UUID) error {
	// The caller holds the Token row lock. READ COMMITTED gives each statement a
	// fresh snapshot, so a waiter sees grants committed by the preceding checker.
	var rpm, daily int64
	var total int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(reward_rate_limit_per_minute) FILTER (WHERE reward_active), 0),
		       COALESCE(SUM(reward_daily_request_limit) FILTER (WHERE reward_active), 0),
		       COALESCE(SUM(reward_total_request_limit) FILTER (WHERE reward_active), 0)
		FROM domain_donations WHERE token_id = $1
	`, tokenID).Scan(&rpm, &daily, &total); err != nil {
		return err
	}
	var adjRPM, adjDaily, adjTotal int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(rpm_delta), 0), COALESCE(SUM(daily_delta), 0), COALESCE(SUM(total_delta), 0)
		FROM donation_reward_events WHERE token_id = $1 AND event_type = 'manual_adjust'
	`, tokenID).Scan(&adjRPM, &adjDaily, &adjTotal); err != nil {
		return err
	}
	rpm += adjRPM
	daily += adjDaily
	total += adjTotal
	if rpm < 0 {
		rpm = 0
	}
	if daily < 0 {
		daily = 0
	}
	if total < 0 {
		total = 0
	}
	capRPM := int64(s.intSetting(ctx, "donation_token_rate_limit_cap", 180))
	if capRPM > 0 && rpm > capRPM {
		rpm = capRPM
	}
	_, err := tx.Exec(ctx, `
		UPDATE account_tokens
		SET rate_limit_per_minute = $2, daily_request_limit = $3, total_request_limit = $4, updated_at = NOW()
		WHERE id = $1 AND token_kind = 'donation'
	`, tokenID, rpm, daily, total)
	return err
}

func (s *Store) SetDonationRevoked(ctx context.Context, id uuid.UUID, revoked bool, note string) error {
	if !revoked {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var tokenID uuid.UUID
		var domainID int
		if err := tx.QueryRow(ctx, `
			SELECT token_id, domain_id
			FROM domain_donations
			WHERE id = $1
			FOR UPDATE
		`, id).Scan(&tokenID, &domainID); err != nil {
			return err
		}
		if err := lockDonationTokenTx(ctx, tx, tokenID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE domain_donations
			SET status = 'inactive', reward_active = FALSE, failure_count = 0,
			    last_error = '', last_checked_at = NULL, updated_at = NOW()
			WHERE id = $1
		`, id); err != nil {
			return err
		}
		// Keep the domain out of the active pool until the next MX+TXT check
		// proves ownership again.
		if _, err := tx.Exec(ctx, `
			UPDATE domains
			SET is_active = FALSE, status = 'disabled', mx_checked_at = NOW()
			WHERE id = $1
		`, domainID); err != nil {
			return err
		}
		if err := s.recalculateDonationTokenLockedTx(ctx, tx, tokenID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		s.invalidateTokenAuthCache(tokenID)
		s.invalidateActiveDomainsCache()
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tokenID uuid.UUID
	var active bool
	var rpm, daily int
	var total int64
	if err := tx.QueryRow(ctx, `SELECT token_id, reward_active, reward_rate_limit_per_minute, reward_daily_request_limit, reward_total_request_limit FROM domain_donations WHERE id = $1 FOR UPDATE`, id).Scan(&tokenID, &active, &rpm, &daily, &total); err != nil {
		return err
	}
	if err := lockDonationTokenTx(ctx, tx, tokenID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE domain_donations SET status = 'revoked', reward_active = FALSE, reward_revoked_at = NOW(), last_error = $2, updated_at = NOW() WHERE id = $1`, id, strings.TrimSpace(note)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domains SET is_active = FALSE, status = 'disabled', mx_checked_at = NOW()
		WHERE id = (SELECT domain_id FROM domain_donations WHERE id = $1)
	`, id); err != nil {
		return err
	}
	if active {
		if _, err := tx.Exec(ctx, `INSERT INTO donation_reward_events (token_id, donation_id, event_type, total_delta, daily_delta, rpm_delta, note) VALUES ($1,$2,'revoke',$3,$4,$5,$6)`, tokenID, id, -total, -daily, -rpm, note); err != nil {
			return err
		}
	}
	if err := s.recalculateDonationTokenLockedTx(ctx, tx, tokenID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidateTokenAuthCache(tokenID)
	s.invalidateActiveDomainsCache()
	return nil
}

func (s *Store) AdjustDonationToken(ctx context.Context, tokenID uuid.UUID, totalDelta int64, dailyDelta, rpmDelta int, note string) error {
	const maxAdjustment int64 = 1_000_000_000
	if totalDelta < -maxAdjustment || totalDelta > maxAdjustment || int64(dailyDelta) < -maxAdjustment || int64(dailyDelta) > maxAdjustment || int64(rpmDelta) < -maxAdjustment || int64(rpmDelta) > maxAdjustment {
		return fmt.Errorf("donation adjustment exceeds allowed range")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockDonationTokenTx(ctx, tx, tokenID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO donation_reward_events (token_id, event_type, total_delta, daily_delta, rpm_delta, note) VALUES ($1,'manual_adjust',$2,$3,$4,$5)`, tokenID, totalDelta, dailyDelta, rpmDelta, strings.TrimSpace(note)); err != nil {
		return err
	}
	if err := s.recalculateDonationTokenLockedTx(ctx, tx, tokenID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidateTokenAuthCache(tokenID)
	return nil
}

func (s *Store) ApplyDonationPolicyToExisting(ctx context.Context) error {
	policy := s.DonationPolicy(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT id, token_id, reward_active, reward_rate_limit_per_minute, reward_daily_request_limit, reward_total_request_limit
		FROM domain_donations WHERE status <> 'revoked' FOR UPDATE
	`)
	if err != nil {
		return err
	}
	type existingGrant struct {
		id      uuid.UUID
		tokenID uuid.UUID
		active  bool
		rpm     int
		daily   int
		total   int64
	}
	grants := make([]existingGrant, 0, 32)
	for rows.Next() {
		var grant existingGrant
		if err := rows.Scan(&grant.id, &grant.tokenID, &grant.active, &grant.rpm, &grant.daily, &grant.total); err != nil {
			rows.Close()
			return err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	tokens := make(map[uuid.UUID]struct{})
	for _, grant := range grants {
		tokens[grant.tokenID] = struct{}{}
	}
	// Lock shared Tokens in a stable order before writing reward events. This
	// avoids lock-upgrade and cross-Token ordering deadlocks during bulk policy
	// application.
	tokenIDs := make([]uuid.UUID, 0, len(tokens))
	for tokenID := range tokens {
		tokenIDs = append(tokenIDs, tokenID)
	}
	sort.Slice(tokenIDs, func(i, j int) bool { return tokenIDs[i].String() < tokenIDs[j].String() })
	for _, tokenID := range tokenIDs {
		if err := lockDonationTokenTx(ctx, tx, tokenID); err != nil {
			return err
		}
	}
	for _, grant := range grants {
		if _, err := tx.Exec(ctx, `
			UPDATE domain_donations
			SET reward_rate_limit_per_minute = $2, reward_daily_request_limit = $3,
			    reward_total_request_limit = $4, updated_at = NOW()
			WHERE id = $1
		`, grant.id, policy.RateLimitPerMinute, policy.DailyRequestLimit, policy.TotalRequestLimit); err != nil {
			return err
		}
		rpmDelta, dailyDelta, totalDelta := 0, 0, int64(0)
		if grant.active {
			rpmDelta = policy.RateLimitPerMinute - grant.rpm
			dailyDelta = policy.DailyRequestLimit - grant.daily
			totalDelta = policy.TotalRequestLimit - grant.total
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO donation_reward_events (token_id, donation_id, event_type, total_delta, daily_delta, rpm_delta, note)
			VALUES ($1,$2,'policy_update',$3,$4,$5,'管理员应用最新奖励规则')
		`, grant.tokenID, grant.id, totalDelta, dailyDelta, rpmDelta); err != nil {
			return err
		}
	}
	for _, tokenID := range tokenIDs {
		if err := s.recalculateDonationTokenLockedTx(ctx, tx, tokenID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidateTokenAuthCaches(tokenIDs)
	return nil
}

func (s *Store) DonationEnabled(ctx context.Context) bool {
	value := strings.ToLower(strings.TrimSpace(s.settingOr(ctx, "donation_enabled", "true")))
	return value != "false" && value != "0" && value != "off"
}

func (s *Store) DonationTXTValue(item *model.DomainDonation) string {
	return DonationTXTValuePrefix + item.ChallengeToken
}
