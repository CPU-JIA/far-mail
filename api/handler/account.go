package handler

import (
	"net/http"
	"os"
	"path/filepath"

	"farmail/middleware"
	"farmail/store"

	"github.com/gin-gonic/gin"
)

type AccountHandler struct {
	store *store.Store
}

func NewAccountHandler(s *store.Store) *AccountHandler {
	return &AccountHandler{store: s}
}

func credentialKindFromMode(mode string) string {
	if mode == "admin_console" {
		return "admin_auth_key"
	}
	return "api_access_token"
}

func (h *AccountHandler) RotateAdminAuthKey(c *gin.Context) {
	account := middleware.GetAccount(c)
	if account == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		AdminAuthKey string `json:"admin_auth_key"`
	}
	_ = c.ShouldBindJSON(&req)
	adminKey := req.AdminAuthKey
	var err error
	if adminKey != "" {
		err = h.store.SetAdminAuthKey(c.Request.Context(), account.ID, adminKey)
	} else {
		adminKey, err = h.store.RotateAdminAuthKey(c.Request.Context(), account.ID)
	}
	if err != nil {
		status := http.StatusInternalServerError
		if req.AdminAuthKey != "" {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": "update admin auth key failed: " + err.Error()})
		return
	}

	keyFile, fileErr := writeAdminAuthKeyFile(adminKey)
	resp := gin.H{
		"message":        "admin auth key rotated",
		"admin_auth_key": adminKey,
		"key_kind":       "admin_auth_key",
		"key_file":       keyFile,
		"file_written":   fileErr == nil,
	}
	if fileErr != nil {
		resp["warning"] = "database key updated, but writing admin.key failed: " + fileErr.Error()
	}
	c.JSON(http.StatusOK, resp)
}

func writeAdminAuthKeyFile(adminKey string) (string, error) {
	keyFile := os.Getenv("ADMIN_KEY_FILE")
	if keyFile == "" {
		keyFile = "/data/admin.key"
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0700); err != nil {
		return keyFile, err
	}
	content := "# FAR Mail Admin Console Auth Key\n" +
		"# Format: sk-<custom>-<16 or 32 hex>; default prefix: sk-mail-.\n" +
		"# Send only through X-Admin-Key to /console/v1. API tokens are stored and authenticated separately for /api/v1.\n\n" +
		"ADMIN_AUTH_KEY=" + adminKey + "\n"
	return keyFile, os.WriteFile(keyFile, []byte(content), 0600)
}

func (h *AccountHandler) Session(c *gin.Context) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	if account == nil || token == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	remainingToday := int64(-1)
	if token.DailyRequestLimit > 0 {
		// best-effort from header injected by auth middleware
	}
	remainingTotal := int64(-1)
	if token.TotalRequestLimit > 0 {
		remainingTotal = token.TotalRequestLimit - token.RequestCountTotal
		if remainingTotal < 0 {
			remainingTotal = 0
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"credential_kind": credentialKindFromMode(middleware.GetAuthMode(c)),
		"account": gin.H{
			"id":         account.ID,
			"username":   account.Username,
			"is_admin":   account.IsAdmin,
			"is_active":  account.IsActive,
			"created_at": account.CreatedAt,
		},
		"token": gin.H{
			"id":                    token.ID,
			"name":                  token.Name,
			"token_prefix":          token.TokenPrefix,
			"scope":                 token.Scope,
			"is_primary":            token.IsPrimary,
			"token_kind":            token.TokenKind,
			"rate_limit_per_minute": token.RateLimitPerMinute,
			"daily_request_limit":   token.DailyRequestLimit,
			"total_request_limit":   token.TotalRequestLimit,
			"request_count_total":   token.RequestCountTotal,
			"remaining_today":       remainingToday,
			"remaining_total":       remainingTotal,
			"last_used_at":          token.LastUsedAt,
			"expires_at":            token.ExpiresAt,
			"created_at":            token.CreatedAt,
			"auth_mode":             middleware.GetAuthMode(c),
			"credential_kind":       credentialKindFromMode(middleware.GetAuthMode(c)),
		},
		"auth_mode": middleware.GetAuthMode(c),
	})
}
