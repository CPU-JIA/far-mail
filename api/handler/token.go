package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"farmail/middleware"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type TokenHandler struct {
	store *store.Store
}

const maxTokenLimit int64 = 1_000_000_000

func NewTokenHandler(s *store.Store) *TokenHandler {
	return &TokenHandler{store: s}
}

func validateTokenInput(name, scope string, rpm, daily int, total int64, expiresInDays int) error {
	if len([]rune(name)) > 128 {
		return fmt.Errorf("token name must not exceed 128 characters")
	}
	switch scope {
	case "", "read", "cleanup", "owner":
	default:
		return fmt.Errorf("scope must be read, cleanup, or owner")
	}
	if rpm < 0 || daily < 0 || total < 0 {
		return fmt.Errorf("token limits must not be negative")
	}
	if int64(rpm) > maxTokenLimit || int64(daily) > maxTokenLimit || total > maxTokenLimit {
		return fmt.Errorf("token limits must not exceed %d", maxTokenLimit)
	}
	if expiresInDays > 3650 {
		return fmt.Errorf("expires_in_days must not exceed 3650")
	}
	return nil
}

func tokenStatus(revokedAt *time.Time, expiresAt *time.Time) string {
	now := time.Now()
	if revokedAt != nil {
		return "disabled"
	}
	if expiresAt != nil && expiresAt.Before(now) {
		return "expired"
	}
	return "active"
}

func (h *TokenHandler) List(c *gin.Context) {
	account := middleware.GetAccount(c)
	current := middleware.GetToken(c)
	items, err := h.store.ListTokensByAccount(c.Request.Context(), account.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	data := make([]gin.H, 0, len(items))
	for _, item := range items {
		remainingTotal := int64(-1)
		if item.TotalRequestLimit > 0 {
			remainingTotal = item.TotalRequestLimit - item.RequestCountTotal
			if remainingTotal < 0 {
				remainingTotal = 0
			}
		}
		data = append(data, gin.H{
			"id":                    item.ID,
			"name":                  item.Name,
			"token_prefix":          item.TokenPrefix,
			"scope":                 item.Scope,
			"is_primary":            item.IsPrimary,
			"token_kind":            item.TokenKind,
			"is_current":            current != nil && current.ID == item.ID,
			"rate_limit_per_minute": item.RateLimitPerMinute,
			"daily_request_limit":   item.DailyRequestLimit,
			"total_request_limit":   item.TotalRequestLimit,
			"request_count_total":   item.RequestCountTotal,
			"remaining_total":       remainingTotal,
			"last_used_at":          item.LastUsedAt,
			"expires_at":            item.ExpiresAt,
			"revoked_at":            item.RevokedAt,
			"status":                tokenStatus(item.RevokedAt, item.ExpiresAt),
			"created_at":            item.CreatedAt,
			"updated_at":            item.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *TokenHandler) Create(c *gin.Context) {
	account := middleware.GetAccount(c)
	var req struct {
		Name               string `json:"name"`
		Scope              string `json:"scope"`
		RateLimitPerMinute int    `json:"rate_limit_per_minute"`
		DailyRequestLimit  int    `json:"daily_request_limit"`
		TotalRequestLimit  int64  `json:"total_request_limit"`
		ExpiresInDays      int    `json:"expires_in_days"`
		Permanent          bool   `json:"permanent"`
		KeepExpiry         bool   `json:"keep_expiry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTokenInput(req.Name, req.Scope, req.RateLimitPerMinute, req.DailyRequestLimit, req.TotalRequestLimit, req.ExpiresInDays); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var expiresAt *time.Time
	useExpiryDefault := true
	if req.ExpiresInDays < 0 || req.Permanent {
		req.ExpiresInDays = 0
		useExpiryDefault = false
	}
	if req.ExpiresInDays > 0 {
		exp := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &exp
		useExpiryDefault = false
	}

	token, plain, err := h.store.CreateToken(
		c.Request.Context(),
		account.ID,
		req.Name,
		req.Scope,
		req.RateLimitPerMinute,
		req.DailyRequestLimit,
		req.TotalRequestLimit,
		expiresAt,
		useExpiryDefault,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"token":             token,
		"access_token":      plain,
		"access_token_kind": "api_access_token",
	})
}

func (h *TokenHandler) Update(c *gin.Context) {
	account := middleware.GetAccount(c)
	tokenID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}
	var req struct {
		Name               string `json:"name"`
		Scope              string `json:"scope"`
		RateLimitPerMinute int    `json:"rate_limit_per_minute"`
		DailyRequestLimit  int    `json:"daily_request_limit"`
		TotalRequestLimit  int64  `json:"total_request_limit"`
		ExpiresInDays      int    `json:"expires_in_days"`
		Permanent          bool   `json:"permanent"`
		KeepExpiry         bool   `json:"keep_expiry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTokenInput(req.Name, req.Scope, req.RateLimitPerMinute, req.DailyRequestLimit, req.TotalRequestLimit, req.ExpiresInDays); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var expiresAt *time.Time
	if req.KeepExpiry {
		current, err := h.store.GetTokenByID(c.Request.Context(), tokenID, account.ID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
			return
		}
		expiresAt = current.ExpiresAt
	} else if req.ExpiresInDays > 0 && !req.Permanent {
		exp := time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &exp
	}
	item, err := h.store.UpdateToken(c.Request.Context(), tokenID, account.ID, req.Name, req.Scope, req.RateLimitPerMinute, req.DailyRequestLimit, req.TotalRequestLimit, expiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": item})
}

func (h *TokenHandler) Rotate(c *gin.Context) {
	account := middleware.GetAccount(c)
	tokenID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}
	item, plain, err := h.store.RotateToken(c.Request.Context(), tokenID, account.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":             item,
		"access_token":      plain,
		"access_token_kind": "api_access_token",
	})
}

func (h *TokenHandler) Disable(c *gin.Context) {
	account := middleware.GetAccount(c)
	tokenID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}
	if err := h.store.DisableToken(c.Request.Context(), tokenID, account.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "token not found or already disabled"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token could not be disabled"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "token disabled"})
}

func (h *TokenHandler) Enable(c *gin.Context) {
	account := middleware.GetAccount(c)
	tokenID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}
	if err := h.store.EnableToken(c.Request.Context(), tokenID, account.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "token not found or already enabled"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token could not be enabled"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "token enabled"})
}

func (h *TokenHandler) Delete(c *gin.Context) {
	account := middleware.GetAccount(c)
	tokenID, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token id"})
		return
	}
	if err := h.store.DeleteToken(c.Request.Context(), tokenID, account.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token could not be deleted"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "token deleted"})
}
