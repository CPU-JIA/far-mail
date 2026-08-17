package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	AccountKey            = "account"
	TokenKey              = "token"
	AuthModeKey           = "auth_mode"
	DeferredTokenUsageKey = "deferred_token_usage"
)

var shanghaiTZ = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

type tokenQuotaState struct {
	MinuteRemaining int64
	MinuteReset     time.Time
	DailyRemaining  int64
	DailyReset      time.Time
	TotalRemaining  int64
}

func setTokenQuotaHeaders(c *gin.Context, token *model.AccountToken, state tokenQuotaState, now time.Time) {
	policy := make([]string, 0, 2)
	if token.RateLimitPerMinute > 0 {
		resetSeconds := max64(1, int64(state.MinuteReset.Sub(now).Seconds()+0.999))
		c.Header("RateLimit-Limit", fmt.Sprintf("%d", token.RateLimitPerMinute))
		c.Header("RateLimit-Remaining", fmt.Sprintf("%d", max64(0, state.MinuteRemaining)))
		c.Header("RateLimit-Reset", fmt.Sprintf("%d", resetSeconds))
		c.Header("X-RateLimit-Minute-Limit", fmt.Sprintf("%d", token.RateLimitPerMinute))
		c.Header("X-RateLimit-Minute-Remaining", fmt.Sprintf("%d", max64(0, state.MinuteRemaining)))
		c.Header("X-RateLimit-Minute-Reset", fmt.Sprintf("%d", state.MinuteReset.Unix()))
		// Retain the existing minute-window fields for current clients.
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", token.RateLimitPerMinute))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", max64(0, state.MinuteRemaining)))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", state.MinuteReset.Unix()))
		policy = append(policy, fmt.Sprintf("%d;w=60", token.RateLimitPerMinute))
	}
	if token.DailyRequestLimit > 0 {
		c.Header("X-RateLimit-Daily-Limit", fmt.Sprintf("%d", token.DailyRequestLimit))
		c.Header("X-RateLimit-Daily-Remaining", fmt.Sprintf("%d", max64(0, state.DailyRemaining)))
		c.Header("X-RateLimit-Daily-Reset", fmt.Sprintf("%d", state.DailyReset.Unix()))
		c.Header("X-Token-Daily-Remaining", fmt.Sprintf("%d", max64(0, state.DailyRemaining)))
		window := max64(1, int64(state.DailyReset.Sub(now).Seconds()+0.999))
		policy = append(policy, fmt.Sprintf("%d;w=%d", token.DailyRequestLimit, window))
	}
	if token.TotalRequestLimit > 0 {
		c.Header("X-RateLimit-Total-Limit", fmt.Sprintf("%d", token.TotalRequestLimit))
		c.Header("X-RateLimit-Total-Remaining", fmt.Sprintf("%d", max64(0, state.TotalRemaining)))
		c.Header("X-Token-Total-Remaining", fmt.Sprintf("%d", max64(0, state.TotalRemaining)))
	}
	if len(policy) > 0 {
		c.Header("RateLimit-Policy", strings.Join(policy, ", "))
	}
}

// AdminAuth authenticates the owner console only. It intentionally reads a
// dedicated header and never falls back to API access tokens.
func AdminAuth(s *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("X-Admin-Key"))
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing admin console key"})
			return
		}

		account, token, err := s.GetAccountByAdminAuthKey(c.Request.Context(), raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid admin console key"})
			return
		}

		c.Header("X-Auth-Mode", "admin_console")
		c.Set(AccountKey, account)
		c.Set(TokenKey, token)
		c.Set(AuthModeKey, "admin_console")
		c.Next()
	}
}

// APIAuth authenticates automation clients only. It never accepts or checks
// the admin console key store.
func APIAuth(s *store.Store, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if len(raw) < len("Bearer ") || !strings.EqualFold(raw[:len("Bearer ")], "Bearer ") {
			raw = ""
		} else {
			raw = strings.TrimSpace(raw[len("Bearer "):])
		}
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing API access token"})
			return
		}

		account, token, err := s.GetAccountByToken(c.Request.Context(), raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API access token"})
			return
		}
		if token.RevokedAt != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API access token revoked"})
			return
		}
		if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API access token expired"})
			return
		}
		if token.TokenKind == "donation" && (token.TotalRequestLimit <= 0 || token.RateLimitPerMinute <= 0) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":  "donation reward is not active",
				"reason": "donation_reward_inactive",
			})
			return
		}

		now := time.Now()
		nextMinute := now.Truncate(time.Minute).Add(time.Minute)
		localNow := now.In(shanghaiTZ)
		nextMidnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, 0, 0, 0, 0, shanghaiTZ)
		quota := tokenQuotaState{
			MinuteRemaining: int64(token.RateLimitPerMinute),
			MinuteReset:     nextMinute,
			DailyRemaining:  int64(token.DailyRequestLimit),
			DailyReset:      nextMidnight,
			TotalRemaining:  max64(0, token.TotalRequestLimit-token.RequestCountTotal),
		}
		setTokenQuotaHeaders(c, token, quota, now)

		minuteKey := ""
		if token.RateLimitPerMinute > 0 {
			minuteKey = fmt.Sprintf("tok:min:%s:%d", token.ID.String(), now.Unix()/60)
		}
		dailyKey := ""
		if token.DailyRequestLimit > 0 {
			dailyKey = fmt.Sprintf("tok:day:%s:%04d%02d%02d", token.ID.String(), localNow.Year(), localNow.Month(), localNow.Day())
		}
		counters, err := incrementQuotaCounters(
			c.Request.Context(), rdb,
			minuteKey, 2*time.Minute, token.RateLimitPerMinute,
			dailyKey, time.Until(nextMidnight.Add(5*time.Minute)), token.DailyRequestLimit,
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable", "reason": "redis_unavailable"})
			return
		}
		if counters.MinuteCount >= 0 {
			quota.MinuteRemaining = int64(token.RateLimitPerMinute) - counters.MinuteCount
		}
		if counters.DailyCount >= 0 {
			quota.DailyRemaining = int64(token.DailyRequestLimit) - counters.DailyCount
		}
		setTokenQuotaHeaders(c, token, quota, now)
		if counters.RejectedBy == 1 {
			c.Header("Retry-After", fmt.Sprintf("%d", max64(1, int64(time.Until(nextMinute).Seconds()+0.999))))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded", "reason": "rate_limit"})
			return
		}
		if counters.RejectedBy == 2 {
			c.Header("Retry-After", fmt.Sprintf("%d", max64(1, int64(time.Until(nextMidnight).Seconds()+0.999))))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":           "daily request limit exceeded",
				"reason":          "daily_limit",
				"reset_at":        nextMidnight.Format(time.RFC3339),
				"remaining_today": 0,
			})
			return
		}

		totalUsed := token.RequestCountTotal
		totalLimit := token.TotalRequestLimit
		if token.TotalRequestLimit > 0 {
			var err error
			totalUsed, totalLimit, err = s.IncrementTokenUsageIfAllowed(c.Request.Context(), token.ID)
			if err != nil {
				if err == store.ErrTokenLimitReached {
					quota.TotalRemaining = 0
					setTokenQuotaHeaders(c, token, quota, now)
					c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
						"error":           "total request limit exceeded",
						"reason":          "total_limit",
						"remaining_total": 0,
					})
					return
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			token.RequestCountTotal = totalUsed
			usedAt := time.Now()
			token.LastUsedAt = &usedAt
		} else {
			// Unlimited-total tokens do not need a synchronous quota write. The API
			// usage recorder batches this telemetry after the response, removing a
			// contended account_tokens UPDATE from the request path.
			c.Set(DeferredTokenUsageKey, true)
			usedAt := time.Now()
			token.LastUsedAt = &usedAt
			token.RequestCountTotal++
		}
		quota.TotalRemaining = max64(0, totalLimit-totalUsed)
		setTokenQuotaHeaders(c, token, quota, now)

		c.Header("X-Token-Scope", token.Scope)
		c.Header("X-Auth-Mode", "api_token")

		c.Set(AccountKey, account)
		c.Set(TokenKey, token)
		c.Set(AuthModeKey, "api_token")
		c.Next()
	}
}

// DonationAuth accepts only donation reward tokens. It intentionally does not
// consume quota so an exhausted token can still add another verified domain.
func DonationAuth(s *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if len(raw) < len("Bearer ") || !strings.EqualFold(raw[:len("Bearer ")], "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing donation API token"})
			return
		}
		raw = strings.TrimSpace(raw[len("Bearer "):])
		account, token, err := s.GetAccountByToken(c.Request.Context(), raw)
		if err != nil || token.TokenKind != "donation" || token.RevokedAt != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid donation API token"})
			return
		}
		if token.ExpiresAt != nil && !token.ExpiresAt.After(time.Now()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "donation API token expired"})
			return
		}
		c.Set(AccountKey, account)
		c.Set(TokenKey, token)
		c.Set(AuthModeKey, "donation_token")
		c.Next()
	}
}

func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		account := GetAccount(c)
		token := GetToken(c)
		mode, _ := c.Get(AuthModeKey)
		if account == nil || token == nil || !account.IsAdmin || token.Scope != "owner" || mode != "admin_console" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin console auth key required"})
			return
		}
		c.Next()
	}
}

func RequireAnyScope(scopes ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		allowed[strings.ToLower(strings.TrimSpace(scope))] = struct{}{}
	}
	return func(c *gin.Context) {
		token := GetToken(c)
		if token == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token context"})
			return
		}
		if _, ok := allowed[token.Scope]; !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient token scope"})
			return
		}
		c.Next()
	}
}

func GetAccount(c *gin.Context) *model.Account {
	val, exists := c.Get(AccountKey)
	if !exists {
		return nil
	}
	a, ok := val.(*model.Account)
	if !ok {
		return nil
	}
	return a
}

func GetToken(c *gin.Context) *model.AccountToken {
	val, exists := c.Get(TokenKey)
	if !exists {
		return nil
	}
	t, ok := val.(*model.AccountToken)
	if !ok {
		return nil
	}
	return t
}

func GetAuthMode(c *gin.Context) string {
	val, exists := c.Get(AuthModeKey)
	if !exists {
		return ""
	}
	mode, _ := val.(string)
	return mode
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
