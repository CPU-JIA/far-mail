package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"farmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrTokenLimitReached = errors.New("token total request limit reached")

const (
	tokenAuthCacheTTL        = 5 * time.Second
	tokenAuthCacheMaxEntries = 4096
)

type tokenAuthCacheEntry struct {
	account   model.Account
	token     model.AccountToken
	expiresAt time.Time
}

type tokenAuthLoadResult struct {
	account model.Account
	token   model.AccountToken
	epoch   uint64
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func tokenPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= 16 {
		return raw
	}
	return raw[:16]
}

func sanitizeTokenSecretPrefix(raw string) string {
	prefix := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	b.Grow(len(prefix))
	lastDash := false
	for _, r := range prefix {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !allowed {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		if r == '-' {
			if b.Len() == 0 || lastDash {
				continue
			}
			lastDash = true
		} else {
			lastDash = false
		}
		b.WriteRune(r)
		if b.Len() >= 24 {
			break
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "mail"
	}
	return out
}

func randomAccessTokenSecret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateAdminAuthKey(prefix string, hexLength int) string {
	prefix = sanitizeTokenSecretPrefix(prefix)
	byteLength := 16
	if hexLength == 16 {
		byteLength = 8
	}
	b := make([]byte, byteLength)
	_, _ = rand.Read(b)
	return "sk-" + prefix + "-" + hex.EncodeToString(b)
}

func isAdminAuthKeyFormat(raw string) bool {
	return isNamedSecretFormat(raw, true)
}

func isAccessTokenFormat(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != 32 {
		return false
	}
	for _, r := range raw {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func isNamedSecretFormat(raw string, allowShort bool) bool {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "sk-") {
		return false
	}
	lastDash := strings.LastIndex(raw, "-")
	secretLength := len(raw) - lastDash - 1
	if lastDash <= 3 || (secretLength != 32 && !(allowShort && secretLength == 16)) {
		return false
	}
	prefix := raw[3:lastDash]
	if len(prefix) == 0 || len(prefix) > 24 {
		return false
	}
	for _, r := range prefix {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	for _, r := range raw[lastDash+1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func generateAccessToken() string {
	return randomAccessTokenSecret()
}

func normalizeScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "cleanup", "owner":
		return scope
	case "read":
		return scope
	default:
		return "read"
	}
}

func (s *Store) intSetting(ctx context.Context, key string, fallback int) int {
	v, err := s.GetSetting(ctx, key)
	if err != nil {
		return fallback
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}

func (s *Store) int64Setting(ctx context.Context, key string, fallback int64) int64 {
	v, err := s.GetSetting(ctx, key)
	if err != nil {
		return fallback
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}

func (s *Store) GetAccountByToken(ctx context.Context, rawToken string) (*model.Account, *model.AccountToken, error) {
	if !isAccessTokenFormat(rawToken) {
		return nil, nil, pgx.ErrNoRows
	}
	tokenHash := hashToken(rawToken)
	if account, token, ok := s.getCachedAccountByToken(tokenHash); ok {
		return account, token, nil
	}
	for {
		value, err, _ := s.tokenAuthLoadGroup.Do(tokenHash, func() (any, error) {
			if account, token, ok := s.getCachedAccountByToken(tokenHash); ok {
				return tokenAuthLoadResult{account: *account, token: *token, epoch: s.tokenAuthEpoch.Load()}, nil
			}
			epoch := s.tokenAuthEpoch.Load()
			account, token, err := s.loadAccountByTokenHash(ctx, tokenHash)
			if err != nil {
				return nil, err
			}
			result := tokenAuthLoadResult{account: *account, token: *token, epoch: epoch}
			if s.tokenAuthEpoch.Load() == epoch {
				s.cacheAccountToken(tokenHash, result.account, result.token)
			}
			return result, nil
		})
		if err != nil {
			return nil, nil, err
		}
		result := value.(tokenAuthLoadResult)
		if result.epoch != s.tokenAuthEpoch.Load() {
			s.tokenAuthLoadGroup.Forget(tokenHash)
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			continue
		}
		account, token := result.account, cloneAccountToken(result.token)
		return &account, &token, nil
	}
}

func (s *Store) loadAccountByTokenHash(ctx context.Context, tokenHash string) (*model.Account, *model.AccountToken, error) {
	var (
		a model.Account
		t model.AccountToken
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			a.id, a.username, a.api_key, a.is_admin, a.is_active, a.created_at, a.updated_at,
			t.id, t.account_id, t.name, t.token_prefix, t.scope, t.is_primary, t.token_kind,
			t.rate_limit_per_minute, t.daily_request_limit, t.total_request_limit, t.request_count_total,
			t.last_used_at, t.expires_at, t.revoked_at, t.created_at, t.updated_at
		FROM account_tokens t
		JOIN accounts a ON a.id = t.account_id
		WHERE t.token_hash = $1
		  AND a.is_active = TRUE
		  AND a.is_admin = TRUE
		  AND a.id = (
			SELECT id FROM accounts
			WHERE is_admin = TRUE AND is_active = TRUE
			ORDER BY created_at, id
			LIMIT 1
		  )
		LIMIT 1
	`, tokenHash).Scan(
		&a.ID, &a.Username, &a.APIKey, &a.IsAdmin, &a.IsActive, &a.CreatedAt, &a.UpdatedAt,
		&t.ID, &t.AccountID, &t.Name, &t.TokenPrefix, &t.Scope, &t.IsPrimary, &t.TokenKind,
		&t.RateLimitPerMinute, &t.DailyRequestLimit, &t.TotalRequestLimit, &t.RequestCountTotal,
		&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, nil, err
	}
	return &a, &t, nil
}

func (s *Store) getCachedAccountByToken(tokenHash string) (*model.Account, *model.AccountToken, bool) {
	now := time.Now()
	s.tokenAuthMu.RLock()
	entry, ok := s.tokenAuthCache[tokenHash]
	s.tokenAuthMu.RUnlock()
	if !ok {
		s.tokenCacheMisses.Add(1)
		return nil, nil, false
	}
	if !now.Before(entry.expiresAt) {
		s.tokenCacheMisses.Add(1)
		s.tokenAuthMu.Lock()
		if current, exists := s.tokenAuthCache[tokenHash]; exists && !now.Before(current.expiresAt) {
			delete(s.tokenAuthCache, tokenHash)
		}
		s.tokenAuthMu.Unlock()
		return nil, nil, false
	}
	account, token := entry.account, cloneAccountToken(entry.token)
	s.tokenCacheHits.Add(1)
	return &account, &token, true
}

func (s *Store) cacheAccountToken(tokenHash string, account model.Account, token model.AccountToken) {
	now := time.Now()
	expiresAt := now.Add(tokenAuthCacheTTL)
	if token.ExpiresAt != nil && token.ExpiresAt.Before(expiresAt) {
		expiresAt = *token.ExpiresAt
	}
	s.tokenAuthMu.Lock()
	if _, exists := s.tokenAuthCache[tokenHash]; !exists && len(s.tokenAuthCache) >= tokenAuthCacheMaxEntries {
		for cachedHash, entry := range s.tokenAuthCache {
			if !now.Before(entry.expiresAt) {
				delete(s.tokenAuthCache, cachedHash)
			}
		}
		if len(s.tokenAuthCache) >= tokenAuthCacheMaxEntries {
			var earliestHash string
			var earliestExpiry time.Time
			for cachedHash, entry := range s.tokenAuthCache {
				if earliestHash == "" || entry.expiresAt.Before(earliestExpiry) {
					earliestHash = cachedHash
					earliestExpiry = entry.expiresAt
				}
			}
			delete(s.tokenAuthCache, earliestHash)
		}
	}
	s.tokenAuthCache[tokenHash] = tokenAuthCacheEntry{account: account, token: cloneAccountToken(token), expiresAt: expiresAt}
	s.tokenAuthMu.Unlock()
}

func cloneAccountToken(token model.AccountToken) model.AccountToken {
	cloned := token
	if token.LastUsedAt != nil {
		lastUsedAt := *token.LastUsedAt
		cloned.LastUsedAt = &lastUsedAt
	}
	if token.ExpiresAt != nil {
		expiresAt := *token.ExpiresAt
		cloned.ExpiresAt = &expiresAt
	}
	if token.RevokedAt != nil {
		revokedAt := *token.RevokedAt
		cloned.RevokedAt = &revokedAt
	}
	return cloned
}

func (s *Store) invalidateTokenAuthCache(tokenID uuid.UUID) {
	s.invalidateTokenAuthCaches([]uuid.UUID{tokenID})
}

func (s *Store) invalidateTokenAuthCaches(tokenIDs []uuid.UUID) {
	if len(tokenIDs) == 0 {
		return
	}
	targets := make(map[uuid.UUID]struct{}, len(tokenIDs))
	for _, tokenID := range tokenIDs {
		targets[tokenID] = struct{}{}
	}
	s.tokenAuthEpoch.Add(1)
	s.tokenAuthMu.Lock()
	for tokenHash, entry := range s.tokenAuthCache {
		if _, ok := targets[entry.token.ID]; ok {
			delete(s.tokenAuthCache, tokenHash)
			s.tokenAuthLoadGroup.Forget(tokenHash)
		}
	}
	s.tokenAuthMu.Unlock()
}

func (s *Store) GetAccountByAdminAuthKey(ctx context.Context, rawKey string) (*model.Account, *model.AccountToken, error) {
	if !isAdminAuthKeyFormat(rawKey) {
		return nil, nil, pgx.ErrNoRows
	}
	var a model.Account
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.username, a.api_key, a.is_admin, a.is_active, a.created_at, a.updated_at
		FROM accounts a
		WHERE a.api_key = $1
		  AND a.is_admin = TRUE
		  AND a.is_active = TRUE
		  AND a.id = (
			SELECT id FROM accounts
			WHERE is_admin = TRUE AND is_active = TRUE
			ORDER BY created_at, id
			LIMIT 1
		  )
		LIMIT 1
	`, strings.TrimSpace(rawKey)).Scan(&a.ID, &a.Username, &a.APIKey, &a.IsAdmin, &a.IsActive, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, nil, err
	}
	t := &model.AccountToken{
		Name:               "Admin console key",
		TokenPrefix:        tokenPrefix(rawKey),
		Scope:              "owner",
		IsPrimary:          true,
		TokenKind:          "admin",
		RateLimitPerMinute: 0,
		DailyRequestLimit:  0,
		TotalRequestLimit:  0,
		CreatedAt:          a.CreatedAt,
		UpdatedAt:          a.UpdatedAt,
	}
	t.AccountID = a.ID
	return &a, t, nil
}

func (s *Store) RotateAdminAuthKey(ctx context.Context, accountID uuid.UUID) (string, error) {
	prefix := s.settingOr(ctx, "admin_key_prefix", "mail")
	hexLength := s.intSetting(ctx, "admin_key_hex_length", 32)
	if hexLength != 16 {
		hexLength = 32
	}
	next := generateAdminAuthKey(prefix, hexLength)
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts
		SET api_key = $1,
		    updated_at = NOW()
		WHERE id = $2
		  AND is_admin = TRUE
		  AND is_active = TRUE
	`, next, accountID)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", pgx.ErrNoRows
	}
	return next, nil
}

func (s *Store) SetAdminAuthKey(ctx context.Context, accountID uuid.UUID, next string) error {
	if !isAdminAuthKeyFormat(next) {
		return fmt.Errorf("admin auth key must match sk-<custom>-<16 or 32 hex>")
	}
	var conflicts bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_tokens WHERE token_hash = $1)`, hashToken(next)).Scan(&conflicts); err != nil {
		return err
	}
	if conflicts {
		return fmt.Errorf("admin auth key must not reuse an API token")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE accounts SET api_key = $1, updated_at = NOW() WHERE id = $2 AND is_admin = TRUE AND is_active = TRUE`, strings.TrimSpace(next), accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) IncrementTokenUsageIfAllowed(ctx context.Context, tokenID uuid.UUID) (int64, int64, error) {
	var totalUsed, totalLimit int64
	err := s.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE account_tokens
			SET request_count_total = request_count_total + 1,
			    last_used_at = NOW(),
			    updated_at = NOW()
			WHERE id = $1
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > NOW())
			  AND (total_request_limit <= 0 OR request_count_total < total_request_limit)
			RETURNING id, account_id, request_count_total, total_request_limit
		), daily AS (
			INSERT INTO api_request_daily (day, token_id, account_id, count)
			SELECT (NOW() AT TIME ZONE 'Asia/Shanghai')::date, id, account_id, 1 FROM updated
			ON CONFLICT (day, token_id)
			DO UPDATE SET count = api_request_daily.count + 1
		)
		SELECT request_count_total, total_request_limit FROM updated
	`, tokenID).Scan(&totalUsed, &totalLimit)
	if err == nil {
		return totalUsed, totalLimit, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrTokenLimitReached
	}
	return 0, 0, err
}

func (s *Store) RecordTokenUsage(ctx context.Context, tokenID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		WITH updated AS (
			UPDATE account_tokens
			SET request_count_total = request_count_total + 1,
			    last_used_at = NOW(),
			    updated_at = NOW()
			WHERE id = $1 AND revoked_at IS NULL
			RETURNING id, account_id
		)
		INSERT INTO api_request_daily (day, token_id, account_id, count)
		SELECT (NOW() AT TIME ZONE 'Asia/Shanghai')::date, id, account_id, 1 FROM updated
		ON CONFLICT (day, token_id)
		DO UPDATE SET count = api_request_daily.count + 1
	`, tokenID)
	return err
}

func (s *Store) ListTokensByAccount(ctx context.Context, accountID uuid.UUID) ([]model.AccountToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, name, token_prefix, scope, is_primary, token_kind,
		       rate_limit_per_minute, daily_request_limit, total_request_limit, request_count_total,
		       last_used_at, expires_at, revoked_at, created_at, updated_at
		FROM account_tokens
		WHERE account_id = $1 AND token_kind = 'standard'
		ORDER BY revoked_at IS NULL DESC, created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.AccountToken])
}

func (s *Store) CreateToken(ctx context.Context, accountID uuid.UUID, name, scope string, rpm, daily int, total int64, expiresAt *time.Time, useExpiryDefault bool) (*model.AccountToken, string, error) {
	if strings.TrimSpace(scope) == "" {
		scope = strings.TrimSpace(strings.ToLower(s.settingOr(ctx, "token_default_scope", "read")))
	}
	if rpm == 0 {
		rpm = s.intSetting(ctx, "token_default_rate_limit_per_minute", 0)
	}
	if daily == 0 {
		daily = s.intSetting(ctx, "token_default_daily_request_limit", 0)
	}
	if total == 0 {
		total = s.int64Setting(ctx, "token_default_total_request_limit", 0)
	}
	if expiresAt == nil && useExpiryDefault {
		if days := s.intSetting(ctx, "token_default_expires_days", 30); days > 0 {
			exp := time.Now().Add(time.Duration(days) * 24 * time.Hour)
			expiresAt = &exp
		}
	}
	if rpm < 0 {
		rpm = 0
	}
	if daily < 0 {
		daily = 0
	}
	if total < 0 {
		total = 0
	}
	scope = normalizeScope(scope)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled token"
	}

	raw := generateAccessToken()
	var item model.AccountToken
	err := s.pool.QueryRow(ctx, `
		INSERT INTO account_tokens (
			account_id, name, token_hash, token_prefix, scope, is_primary, token_kind,
			rate_limit_per_minute, daily_request_limit, total_request_limit, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,'standard',$7,$8,$9,$10)
		RETURNING id, account_id, name, token_prefix, scope, is_primary, token_kind,
		          rate_limit_per_minute, daily_request_limit, total_request_limit, request_count_total,
		          last_used_at, expires_at, revoked_at, created_at, updated_at
	`, accountID, name, hashToken(raw), tokenPrefix(raw), scope, false, rpm, daily, total, expiresAt).Scan(
		&item.ID, &item.AccountID, &item.Name, &item.TokenPrefix, &item.Scope, &item.IsPrimary, &item.TokenKind,
		&item.RateLimitPerMinute, &item.DailyRequestLimit, &item.TotalRequestLimit, &item.RequestCountTotal,
		&item.LastUsedAt, &item.ExpiresAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, "", err
	}
	return &item, raw, nil
}

func (s *Store) UpdateToken(ctx context.Context, tokenID, accountID uuid.UUID, name, scope string, rpm, daily int, total int64, expiresAt *time.Time) (*model.AccountToken, error) {
	item, err := s.GetTokenByID(ctx, tokenID, accountID)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = item.Name
	}
	scope = normalizeScope(scope)
	if rpm < 0 {
		rpm = 0
	}
	if daily < 0 {
		daily = 0
	}
	if total < 0 {
		total = 0
	}
	var updated model.AccountToken
	err = s.pool.QueryRow(ctx, `
		UPDATE account_tokens
		SET name = $3,
		    scope = $4,
		    rate_limit_per_minute = $5,
		    daily_request_limit = $6,
		    total_request_limit = $7,
		    expires_at = $8,
		    updated_at = NOW()
		WHERE id = $1 AND account_id = $2
		RETURNING id, account_id, name, token_prefix, scope, is_primary, token_kind,
		          rate_limit_per_minute, daily_request_limit, total_request_limit, request_count_total,
		          last_used_at, expires_at, revoked_at, created_at, updated_at
	`, tokenID, accountID, name, scope, rpm, daily, total, expiresAt).Scan(
		&updated.ID, &updated.AccountID, &updated.Name, &updated.TokenPrefix, &updated.Scope, &updated.IsPrimary, &updated.TokenKind,
		&updated.RateLimitPerMinute, &updated.DailyRequestLimit, &updated.TotalRequestLimit, &updated.RequestCountTotal,
		&updated.LastUsedAt, &updated.ExpiresAt, &updated.RevokedAt, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.invalidateTokenAuthCache(tokenID)
	return &updated, nil
}

func (s *Store) settingOr(ctx context.Context, key, fallback string) string {
	v, err := s.GetSetting(ctx, key)
	if err != nil || strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func (s *Store) GetTokenByID(ctx context.Context, tokenID, accountID uuid.UUID) (*model.AccountToken, error) {
	var item model.AccountToken
	err := s.pool.QueryRow(ctx, `
		SELECT id, account_id, name, token_prefix, scope, is_primary, token_kind,
		       rate_limit_per_minute, daily_request_limit, total_request_limit, request_count_total,
		       last_used_at, expires_at, revoked_at, created_at, updated_at
		FROM account_tokens
		WHERE id = $1 AND account_id = $2 AND token_kind = 'standard'
	`, tokenID, accountID).Scan(
		&item.ID, &item.AccountID, &item.Name, &item.TokenPrefix, &item.Scope, &item.IsPrimary, &item.TokenKind,
		&item.RateLimitPerMinute, &item.DailyRequestLimit, &item.TotalRequestLimit, &item.RequestCountTotal,
		&item.LastUsedAt, &item.ExpiresAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) RotateToken(ctx context.Context, tokenID, accountID uuid.UUID) (*model.AccountToken, string, error) {
	raw := generateAccessToken()
	var rotated model.AccountToken
	err := s.pool.QueryRow(ctx, `
		UPDATE account_tokens
			SET token_hash = $3,
			    token_prefix = $4,
			    updated_at = NOW(),
			    revoked_at = NULL
			WHERE id = $1 AND account_id = $2 AND token_kind = 'standard'
			RETURNING id, account_id, name, token_prefix, scope, is_primary, token_kind,
			          rate_limit_per_minute, daily_request_limit, total_request_limit, request_count_total,
			          last_used_at, expires_at, revoked_at, created_at, updated_at
		`, tokenID, accountID, hashToken(raw), tokenPrefix(raw)).Scan(
		&rotated.ID, &rotated.AccountID, &rotated.Name, &rotated.TokenPrefix, &rotated.Scope, &rotated.IsPrimary, &rotated.TokenKind,
		&rotated.RateLimitPerMinute, &rotated.DailyRequestLimit, &rotated.TotalRequestLimit, &rotated.RequestCountTotal,
		&rotated.LastUsedAt, &rotated.ExpiresAt, &rotated.RevokedAt, &rotated.CreatedAt, &rotated.UpdatedAt,
	)
	if err != nil {
		return nil, "", err
	}
	s.invalidateTokenAuthCache(tokenID)
	return &rotated, raw, nil
}

func (s *Store) DisableToken(ctx context.Context, tokenID, accountID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE account_tokens
		SET revoked_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1 AND account_id = $2 AND token_kind = 'standard' AND revoked_at IS NULL
	`, tokenID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.invalidateTokenAuthCache(tokenID)
	return nil
}

func (s *Store) EnableToken(ctx context.Context, tokenID, accountID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE account_tokens
		SET revoked_at = NULL,
		    updated_at = NOW()
		WHERE id = $1 AND account_id = $2 AND token_kind = 'standard'
	`, tokenID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.invalidateTokenAuthCache(tokenID)
	return nil
}

func (s *Store) DeleteToken(ctx context.Context, tokenID, accountID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM account_tokens
		WHERE id = $1 AND account_id = $2 AND token_kind = 'standard'
	`, tokenID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.invalidateTokenAuthCache(tokenID)
	return nil
}
