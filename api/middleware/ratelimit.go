package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimit 基于 Redis 的滑动窗口速率限制
// limit: 每个窗口允许的请求数
// window: 窗口大小（秒）
func RateLimit(rdb *redis.Client, limit int, window int) gin.HandlerFunc {
	windowDur := time.Duration(window) * time.Second

	return func(c *gin.Context) {
		// Use the bearer credential when available; query-string credentials are
		// intentionally unsupported so they cannot leak through URLs and logs.
		identity := c.GetHeader("Authorization")
		if identity == "" {
			identity = c.ClientIP()
		}
		// Never place bearer credentials directly in Redis keys. Include the
		// endpoint path so public submit/status limits cannot consume each other.
		redisKey := fixedWindowKey(c.Request.URL.Path, identity, time.Now(), window)
		ctx := c.Request.Context()
		count, err := incrementWithTTL(ctx, rdb, redisKey, windowDur*2)

		if err != nil {
			// A failed limiter must not silently remove abuse protection.
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable", "reason": "redis_unavailable"})
			return
		}

		remaining := int64(limit) - count
		if remaining < 0 {
			remaining = 0
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(windowDur).Unix()))

		if count > int64(limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"limit":       limit,
				"retry_after": window,
			})
			return
		}

		c.Next()
	}
}

func fixedWindowKey(path, identity string, now time.Time, windowSeconds int) string {
	digest := sha256.Sum256([]byte(identity))
	bucket := now.Unix() / int64(windowSeconds)
	return fmt.Sprintf("rl:%s:%s:%d", path, hex.EncodeToString(digest[:]), bucket)
}
