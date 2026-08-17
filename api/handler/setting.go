package handler

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"farmail/store"

	"github.com/gin-gonic/gin"
)

type SettingHandler struct {
	store        *store.Store
	smtpServerIP string
	smtpHostname string
}

var editableSettingKeys = map[string]struct{}{
	"site_title":                            {},
	"site_logo_url":                         {},
	"announcement":                          {},
	"smtp_server_ip":                        {},
	"smtp_hostname":                         {},
	"mailbox_ttl_minutes":                   {},
	"email_retention_minutes":               {},
	"inbox_refresh_seconds":                 {},
	"token_default_expires_days":            {},
	"token_default_rate_limit_per_minute":   {},
	"token_default_daily_request_limit":     {},
	"token_default_total_request_limit":     {},
	"admin_key_prefix":                      {},
	"admin_key_hex_length":                  {},
	"donation_enabled":                      {},
	"donation_reward_rate_limit_per_minute": {},
	"donation_reward_daily_request_limit":   {},
	"donation_reward_total_request_limit":   {},
	"donation_token_rate_limit_cap":         {},
	"donation_max_domains_per_token":        {},
	"donation_dns_failure_tolerance":        {},
	"donation_recheck_minutes":              {},
}

var credentialPrefixPattern = regexp.MustCompile(`^[a-z0-9_-]{1,24}$`)
var hostnamePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func NewSettingHandler(s *store.Store, smtpServerIP, smtpHostname string) *SettingHandler {
	return &SettingHandler{store: s, smtpServerIP: strings.TrimSpace(smtpServerIP), smtpHostname: strings.TrimSpace(smtpHostname)}
}

func lightweightLogoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		return raw
	}
	sum := sha1.Sum([]byte(raw))
	return "/public/v1/logo?v=" + hex.EncodeToString(sum[:6])
}

// GET /public/v1/settings → 返回公开站点配置。
func (h *SettingHandler) GetPublic(c *gin.Context) {
	settings, err := h.store.GetAllSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	siteTitle := settings["site_title"]
	siteLogoURL := settings["site_logo_url"]
	smtpIP := strings.TrimSpace(settings["smtp_server_ip"])
	if smtpIP == "" {
		smtpIP = h.smtpServerIP
	}
	smtpHostname := strings.TrimSpace(settings["smtp_hostname"])
	if smtpHostname == "" {
		smtpHostname = h.smtpHostname
	}
	inboxRefreshSeconds := settings["inbox_refresh_seconds"]
	if inboxRefreshSeconds == "" {
		inboxRefreshSeconds = "3"
	}
	announce := settings["announcement"]
	c.Header("Cache-Control", "public, max-age=5, stale-while-revalidate=30")
	c.JSON(http.StatusOK, gin.H{
		"site_title":                            siteTitle,
		"site_logo_url":                         lightweightLogoURL(siteLogoURL),
		"smtp_server_ip":                        smtpIP,
		"smtp_hostname":                         smtpHostname,
		"inbox_refresh_seconds":                 inboxRefreshSeconds,
		"announcement":                          announce,
		"donation_enabled":                      settings["donation_enabled"],
		"donation_reward_rate_limit_per_minute": settings["donation_reward_rate_limit_per_minute"],
		"donation_reward_daily_request_limit":   settings["donation_reward_daily_request_limit"],
		"donation_reward_total_request_limit":   settings["donation_reward_total_request_limit"],
	})
}

// GET /public/v1/logo → 独立输出站点图标，避免 settings 反复携带大 base64。
func (h *SettingHandler) GetLogo(c *gin.Context) {
	raw, err := h.store.GetSetting(c.Request.Context(), "site_logo_url")
	if err != nil || strings.TrimSpace(raw) == "" {
		c.Status(http.StatusNotFound)
		return
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		c.Redirect(http.StatusFound, raw)
		return
	}

	head, body, ok := strings.Cut(raw, ",")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid logo data url"})
		return
	}
	meta := strings.TrimPrefix(head, "data:")
	contentType := strings.TrimSpace(strings.Split(meta, ";")[0])
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var payload []byte
	if strings.Contains(strings.ToLower(meta), ";base64") {
		payload, err = base64.StdEncoding.DecodeString(body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid logo base64"})
			return
		}
	} else {
		decoded, err := url.QueryUnescape(body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid logo payload"})
			return
		}
		payload = []byte(decoded)
	}

	c.Header("Cache-Control", "public, max-age=86400, immutable")
	c.Data(http.StatusOK, contentType, payload)
}

// GET /console/v1/settings → 读取站长后台可编辑设置。
func (h *SettingHandler) AdminGetAll(c *gin.Context) {
	allSettings, err := h.store.GetAllSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	settings := make(map[string]string, len(editableSettingKeys))
	for key := range editableSettingKeys {
		if value, ok := allSettings[key]; ok {
			settings[key] = value
		}
	}
	if strings.TrimSpace(settings["smtp_server_ip"]) == "" {
		settings["smtp_server_ip"] = h.smtpServerIP
	}
	if strings.TrimSpace(settings["smtp_hostname"]) == "" {
		settings["smtp_hostname"] = h.smtpHostname
	}
	if raw := settings["site_logo_url"]; raw != "" {
		settings["site_logo_url"] = lightweightLogoURL(raw)
	}
	c.JSON(http.StatusOK, settings)
}

// PUT /console/v1/settings → 更新站长后台设置。
func (h *SettingHandler) AdminUpdate(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	normalized := make(map[string]string, len(req))
	for k, v := range req {
		if _, ok := editableSettingKeys[k]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown setting key: " + k})
			return
		}
		if k == "admin_key_prefix" {
			v = strings.ToLower(strings.TrimSpace(v))
			if !credentialPrefixPattern.MatchString(v) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "admin_key_prefix must be 1-24 lowercase letters, numbers, dashes, or underscores"})
				return
			}
		}
		if k == "admin_key_hex_length" && v != "16" && v != "32" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "admin_key_hex_length must be 16 or 32"})
			return
		}
		if err := validateSettingValue(k, v); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		normalized[k] = v
	}
	if err := h.store.SetSettings(c.Request.Context(), normalized); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}

func validateSettingValue(key, raw string) error {
	v := strings.TrimSpace(raw)
	switch key {
	case "smtp_server_ip":
		if v != "" && net.ParseIP(v) == nil {
			return fmt.Errorf("smtp_server_ip must be a valid IPv4 or IPv6 address")
		}
	case "smtp_hostname":
		if v != "" && (len(v) > 253 || !hostnamePattern.MatchString(strings.ToLower(v))) {
			return fmt.Errorf("smtp_hostname must be a valid hostname")
		}
	case "mailbox_ttl_minutes", "email_retention_minutes":
		return validateIntegerRange(key, v, 0, 525600)
	case "inbox_refresh_seconds":
		return validateIntegerRange(key, v, 2, 3600)
	case "token_default_expires_days":
		return validateIntegerRange(key, v, 0, 3650)
	case "token_default_rate_limit_per_minute", "token_default_daily_request_limit", "token_default_total_request_limit":
		return validateIntegerRange(key, v, 0, 1000000000)
	case "donation_reward_rate_limit_per_minute", "donation_reward_total_request_limit":
		return validateIntegerRange(key, v, 1, 1000000000)
	case "donation_reward_daily_request_limit", "donation_token_rate_limit_cap":
		return validateIntegerRange(key, v, 0, 1000000000)
	case "donation_max_domains_per_token":
		return validateIntegerRange(key, v, 1, 1000)
	case "donation_dns_failure_tolerance":
		return validateIntegerRange(key, v, 1, 20)
	case "donation_recheck_minutes":
		return validateIntegerRange(key, v, 1, 1440)
	case "donation_enabled":
		if v != "true" && v != "false" {
			return fmt.Errorf("donation_enabled must be true or false")
		}
	case "site_title":
		if len([]rune(v)) > 80 {
			return fmt.Errorf("site_title must not exceed 80 characters")
		}
	case "announcement":
		if len([]rune(v)) > 500 {
			return fmt.Errorf("announcement must not exceed 500 characters")
		}
	case "site_logo_url":
		if len(v) > 750000 {
			return fmt.Errorf("site_logo_url payload is too large")
		}
	}
	return nil
}

func validateIntegerRange(key, value string, min, max int64) error {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < min || n > max {
		return fmt.Errorf("%s must be an integer between %d and %d", key, min, max)
	}
	return nil
}
