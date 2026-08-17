package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type DomainHandler struct {
	store       *store.Store
	cfgIP       string // SMTP_SERVER_IP env
	cfgHostname string // SMTP_HOSTNAME env
}

func NewDomainHandler(s *store.Store, smtpIP, smtpHostname string) *DomainHandler {
	return &DomainHandler{store: s, cfgIP: smtpIP, cfgHostname: smtpHostname}
}

// getServerIP 优先读 DB 设置，其次环境变量传入的值
func (h *DomainHandler) getServerIP(ctx context.Context) string {
	if ip, err := h.store.GetSetting(ctx, "smtp_server_ip"); err == nil && ip != "" {
		return ip
	}
	return h.cfgIP
}

// getServerHostname 返回 MX 记录应指向的邮件服务器 hostname
// 优先: DB 设置 smtp_hostname → 环境变量 → 空串（傻用 mail.提交域名 方式）
func (h *DomainHandler) getServerHostname(ctx context.Context) string {
	if hn, err := h.store.GetSetting(ctx, "smtp_hostname"); err == nil && hn != "" {
		return hn
	}
	return h.cfgHostname
}

func (h *DomainHandler) buildDonationDNS(domain string, includeWildcard bool, ctx context.Context) []gin.H {
	serverIP := h.getServerIP(ctx)
	hostname := h.getServerHostname(ctx)

	records := []gin.H{
		{"type": "MX", "host": "@", "value": hostname, "priority": 10, "description": "根域收件 MX"},
	}
	if includeWildcard {
		records = append(records, gin.H{
			"type": "MX", "host": "*", "value": hostname, "priority": 10, "description": "子域通配 MX（支持无限子域收件）",
		})
	}
	if serverIP != "" {
		records = append(records, gin.H{
			"type": "TXT", "host": "@", "value": fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP), "description": "SPF（可选，收件不是必须）",
		})
	}
	if hostname == "" {
		mailSub := fmt.Sprintf("mail.%s", domain)
		records = []gin.H{
			{"type": "MX", "host": "@", "value": mailSub, "priority": 10, "description": "根域收件 MX"},
		}
		if includeWildcard {
			records = append(records, gin.H{
				"type": "MX", "host": "*", "value": mailSub, "priority": 10, "description": "子域通配 MX（支持无限子域收件）",
			})
		}
		records = append(records, gin.H{
			"type": "A", "host": mailSub, "value": serverIP, "description": "邮件服务器 A 记录",
		})
	}
	return records
}

func (h *DomainHandler) buildDonationClaimDNS(domain, challenge string, includeWildcard bool, ctx context.Context) []gin.H {
	records := h.buildDonationDNS(domain, includeWildcard, ctx)
	records = append(records, gin.H{
		"type": "TXT", "host": store.DonationTXTLabel, "value": store.DonationTXTValuePrefix + challenge,
		"description": "域名控制权验证",
	})
	return records
}

func normalizePublicDomain(input string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(input))
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	if strings.HasPrefix(domain, "*.") || strings.HasPrefix(domain, ".") || strings.Contains(domain, "@") {
		return "", fmt.Errorf("enter a root domain without wildcard, @, or email address")
	}
	if strings.ContainsAny(domain, " /\\") || !strings.Contains(domain, ".") {
		return "", fmt.Errorf("invalid domain format")
	}
	if len(domain) > 253 {
		return "", fmt.Errorf("domain is too long")
	}
	return domain, nil
}

// GET /console/v1/domains - 列出站点域名池。
func (h *DomainHandler) List(c *gin.Context) {
	domains, err := h.store.ListDomains(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

// ListActive exposes only the active root-domain pool to authenticated API clients.
func (h *DomainHandler) ListActive(c *gin.Context) {
	domains, err := h.store.GetActiveDomains(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "active domains could not be listed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

// DELETE /console/v1/domains/:id - 删除域名。
func (h *DomainHandler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid domain id"})
		return
	}

	domain, err := h.store.GetDomainByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return
	}
	if domain.SourceType == "donated" {
		c.JSON(http.StatusConflict, gin.H{"error": "manage this domain from the donation plan"})
		return
	}

	if err := h.store.DeleteDomain(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "domain deleted"})
}

// PUT /console/v1/domains/:id/toggle - 启用或停用域名。
func (h *DomainHandler) Toggle(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid domain id"})
		return
	}

	domain, err := h.store.GetDomainByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain not found"})
		return
	}
	if domain.SourceType == "donated" {
		c.JSON(http.StatusConflict, gin.H{"error": "manage this domain from the donation plan"})
		return
	}

	var req struct {
		Active bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.ToggleDomain(c.Request.Context(), id, req.Active); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "domain updated"})
}

// POST /console/v1/domains/mx-register - 提交域名并等待自动 MX 验证。
// body: {"domain":"example.com"}
func (h *DomainHandler) MXRegister(c *gin.Context) {
	var req struct {
		Domain string `json:"domain" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))

	serverIP := h.getServerIP(c.Request.Context())
	hostname := h.getServerHostname(c.Request.Context())

	// MX 目标: 优先用服务器自己的 hostname，否则用用户域名的 mail 子域
	mxTarget := fmt.Sprintf("mail.%s", req.Domain)
	dnsRequired := []gin.H{
		{"type": "MX", "host": "@", "value": mxTarget, "priority": 10},
		{"type": "A", "host": mxTarget, "value": serverIP},
		{"type": "TXT", "host": "@", "value": fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP)},
	}
	if hostname != "" {
		mxTarget = hostname
		dnsRequired = []gin.H{
			{"type": "MX", "host": "@", "value": hostname, "priority": 10},
			{"type": "TXT", "host": "@", "value": fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP)},
		}
	}

	// 先尝试立即检测；通过则直接激活
	matched, _, mxStatus := store.CheckDomainMX(req.Domain, serverIP)
	if matched {
		domain, err := h.store.AddDomain(c.Request.Context(), req.Domain)
		if err != nil {
			if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
				// 已存在则直接返回
				domains, _ := h.store.ListDomains(c.Request.Context())
				for _, d := range domains {
					if d.Domain == req.Domain {
						c.JSON(http.StatusOK, gin.H{
							"domain":    d,
							"status":    d.Status,
							"mx_status": mxStatus,
							"message":   "domain already exists and is active",
						})
						return
					}
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"domain":  domain,
			"status":  "active",
			"message": "MX verification passed; domain added to the pool",
		})
		return
	}

	// MX未通过 → 加入 pending，等待后台自动轮询
	domain, err := h.store.AddDomainPending(c.Request.Context(), req.Domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"domain":       domain,
		"status":       domain.Status,
		"server_ip":    serverIP,
		"mx_status":    mxStatus,
		"message":      fmt.Sprintf("domain %s queued for MX verification", req.Domain),
		"dns_required": dnsRequired,
	})
}

// POST /public/v1/domains/submit - first donation, no prior token required.
func (h *DomainHandler) PublicSubmit(c *gin.Context) {
	h.submitDonation(c, "")
}

// POST /api/v1/donations - add a domain to an existing donation token.
func (h *DomainHandler) AuthenticatedDonationSubmit(c *gin.Context) {
	raw := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(raw) >= len("Bearer ") && strings.EqualFold(raw[:len("Bearer ")], "Bearer ") {
		raw = strings.TrimSpace(raw[len("Bearer "):])
	}
	h.submitDonation(c, raw)
}

func (h *DomainHandler) submitDonation(c *gin.Context, existingToken string) {
	if !h.store.DonationEnabled(c.Request.Context()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "domain donations are currently closed"})
		return
	}
	var req struct {
		Domain           string `json:"domain" binding:"required"`
		EnableSubdomains bool   `json:"enable_subdomains"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	normalized, err := normalizePublicDomain(req.Domain)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Domain = normalized

	donation, claimSecret, accessToken, err := h.store.CreateDonationRequest(c.Request.Context(), req.Domain, req.EnableSubdomains, existingToken)
	if err != nil {
		status := http.StatusInternalServerError
		message := "domain donation could not be created"
		switch {
		case errors.Is(err, store.ErrDonationConflict):
			status, message = http.StatusConflict, "domain already submitted or owner-managed"
		case errors.Is(err, store.ErrDonationTokenLimit):
			status, message = http.StatusConflict, "reward API Token domain limit reached"
		case errors.Is(err, pgx.ErrNoRows):
			status, message = http.StatusUnauthorized, "invalid reward API Token"
		}
		c.JSON(status, gin.H{"error": message})
		return
	}
	domain, err := h.store.GetDomainByID(c.Request.Context(), donation.DomainID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"domain":            domain,
		"donation":          donation,
		"donation_id":       donation.ID,
		"claim_secret":      claimSecret,
		"access_token":      accessToken,
		"token_prefix":      donation.TokenPrefix,
		"status":            donation.Status,
		"smtp_hostname":     h.getServerHostname(c.Request.Context()),
		"server_ip":         h.getServerIP(c.Request.Context()),
		"enable_subdomains": req.EnableSubdomains,
		"dns_required":      h.buildDonationClaimDNS(req.Domain, donation.ChallengeToken, req.EnableSubdomains, c.Request.Context()),
		"message":           "domain submitted",
	})
}

// POST /public/v1/domains/status - claim-secret protected status polling.
func (h *DomainHandler) PublicStatus(c *gin.Context) {
	var req struct {
		DonationID  string `json:"donation_id" binding:"required"`
		ClaimSecret string `json:"claim_secret" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "donation_id and claim_secret are required"})
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(req.DonationID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid donation_id"})
		return
	}
	donation, err := h.store.GetDonationByClaim(c.Request.Context(), id, req.ClaimSecret)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "donation not found"})
		return
	}
	if donation.LastCheckedAt == nil || time.Since(*donation.LastCheckedAt) > 10*time.Second {
		if checked, checkErr := h.verifyDonation(c.Request.Context(), donation); checkErr == nil {
			donation = checked
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"donation":      donation,
		"id":            donation.ID,
		"domain":        donation.Domain,
		"status":        donation.Status,
		"is_active":     donation.RewardActive,
		"mx_checked_at": donation.LastCheckedAt,
		"dns_required":  h.buildDonationClaimDNS(donation.Domain, donation.ChallengeToken, donation.IncludeSubdomains, c.Request.Context()),
	})
}

func (h *DomainHandler) verifyDonation(ctx context.Context, donation *model.DomainDonation) (*model.DomainDonation, error) {
	result := store.CheckDonationRecords(
		donation.Domain,
		h.getServerIP(ctx),
		h.getServerHostname(ctx),
		h.store.DonationTXTValue(donation),
	)
	return h.store.ApplyDonationVerification(ctx, donation.ID, result)
}

func (h *DomainHandler) ListDonations(c *gin.Context) {
	items, err := h.store.ListDonations(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	summary, err := h.store.DonationSummary(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tokens, err := h.store.ListDonationRewardTokens(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	events, err := h.store.ListDonationRewardEvents(c.Request.Context(), 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "summary": summary, "tokens": tokens, "events": events})
}

func (h *DomainHandler) RecheckDonation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid donation id"})
		return
	}
	item, err := h.store.GetDonation(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "donation not found"})
		return
	}
	if item.Status == "revoked" {
		if err := h.store.SetDonationRevoked(c.Request.Context(), id, false, ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		item, _ = h.store.GetDonation(c.Request.Context(), id)
	}
	item, err = h.verifyDonation(c.Request.Context(), item)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"donation": item})
}

func (h *DomainHandler) RevokeDonation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid donation id"})
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.store.SetDonationRevoked(c.Request.Context(), id, true, req.Note); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "donation reward revoked"})
}

func (h *DomainHandler) AdjustDonation(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid donation id"})
		return
	}
	item, err := h.store.GetDonation(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "donation not found"})
		return
	}
	var req struct {
		TotalDelta int64  `json:"total_delta"`
		DailyDelta int    `json:"daily_delta"`
		RPMDelta   int    `json:"rpm_delta"`
		Note       string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid adjustment"})
		return
	}
	if req.TotalDelta == 0 && req.DailyDelta == 0 && req.RPMDelta == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one adjustment is required"})
		return
	}
	if err := h.store.AdjustDonationToken(c.Request.Context(), item.TokenID, req.TotalDelta, req.DailyDelta, req.RPMDelta, req.Note); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated, _ := h.store.GetDonation(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"donation": updated})
}

func (h *DomainHandler) AdjustDonationToken(c *gin.Context) {
	tokenID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid donation token id"})
		return
	}
	var req struct {
		TotalDelta int64  `json:"total_delta"`
		DailyDelta int    `json:"daily_delta"`
		RPMDelta   int    `json:"rpm_delta"`
		Note       string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid adjustment"})
		return
	}
	if req.TotalDelta == 0 && req.DailyDelta == 0 && req.RPMDelta == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one adjustment is required"})
		return
	}
	if err := h.store.AdjustDonationToken(c.Request.Context(), tokenID, req.TotalDelta, req.DailyDelta, req.RPMDelta, req.Note); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "donation token quota adjusted"})
}

func (h *DomainHandler) ApplyDonationPolicy(c *gin.Context) {
	if err := h.store.ApplyDonationPolicyToExisting(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "donation policy applied"})
}
