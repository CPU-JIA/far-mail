package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"farmail/middleware"
	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var mailboxDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
var mailboxLocalPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._+-]{0,62}[a-z0-9])?$`)

type MailboxHandler struct {
	store *store.Store
}

func NewMailboxHandler(s *store.Store) *MailboxHandler {
	return &MailboxHandler{store: s}
}

// POST /console/v1/mailboxes 或 /api/v1/mailboxes - 创建临时邮箱
// 请求体字段均为可选：
//
//	address — 本地部分（@ 前），为空则随机生成
//	domain  — 指定域名（须是已激活域名），为空则随机选取
func (h *MailboxHandler) Create(c *gin.Context) {
	account := middleware.GetAccount(c)

	var req struct {
		Address string `json:"address"`
		Domain  string `json:"domain"`
	}
	c.ShouldBindJSON(&req)

	address := strings.TrimSpace(req.Address)
	if address == "" {
		address = store.GenerateRandomAddress()
	}
	address = strings.ToLower(address)
	if !mailboxLocalPattern.MatchString(address) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "address must be 1-64 lowercase letters, numbers, dots, underscores, plus signs, or dashes"})
		return
	}

	ttlMinutes := h.mailboxTTLMinutes(c)

	// 确定域名：指定 or 随机
	var domainRecord *model.Domain
	resolvedDomain := ""
	if d := strings.TrimSpace(strings.ToLower(req.Domain)); d != "" {
		if !mailboxDomainPattern.MatchString(d) || !strings.Contains(d, ".") || strings.Contains(d, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid domain format"})
			return
		}
		found, actualDomain, err := h.store.ResolveActiveMailboxDomain(c.Request.Context(), d)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "domain not found under active root domains: " + d})
			return
		}
		domainRecord = found
		resolvedDomain = actualDomain
	} else {
		found, err := h.store.GetRandomActiveDomain(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no active domains available"})
			return
		}
		domainRecord = found
		resolvedDomain = found.Domain
	}

	fullAddress := fmt.Sprintf("%s@%s", address, resolvedDomain)

	mailbox, err := h.store.CreateMailbox(c.Request.Context(), account.ID, donationCreatorToken(c), address, domainRecord.ID, fullAddress, ttlMinutes)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "address already taken, try again"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"mailbox": mailbox})
}

// GET /console/v1/mailboxes 或 /api/v1/mailboxes - 列出邮箱
func (h *MailboxHandler) List(c *gin.Context) {
	account := middleware.GetAccount(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}

	q := strings.TrimSpace(c.Query("q"))
	domain := strings.TrimSpace(c.Query("domain"))
	keepForeverOnly := c.Query("keep_forever") == "true"
	expiringWithinHours, _ := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("expiring_within_hours", "0")))

	var (
		mailboxes []model.Mailbox
		total     int
		err       error
	)
	if q != "" || domain != "" || keepForeverOnly || expiringWithinHours > 0 {
		mailboxes, total, err = h.store.SearchMailboxesForAccount(c.Request.Context(), account.ID, donationCreatorToken(c), page, size, q, domain, keepForeverOnly, expiringWithinHours)
	} else {
		mailboxes, total, err = h.store.ListMailboxes(c.Request.Context(), account.ID, donationCreatorToken(c), page, size)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":                  mailboxes,
		"total":                 total,
		"page":                  page,
		"size":                  size,
		"q":                     q,
		"domain":                domain,
		"keep_forever":          keepForeverOnly,
		"expiring_within_hours": expiringWithinHours,
	})
}

// GET /console/v1/mailboxes/:id 或 /api/v1/mailboxes/:id - 获取邮箱详情
func (h *MailboxHandler) Get(c *gin.Context) {
	account := middleware.GetAccount(c)
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}

	mailbox, err := h.store.GetMailbox(c.Request.Context(), id, account.ID, donationCreatorToken(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"mailbox": mailbox})
}

// PUT /console/v1/mailboxes/:id/retention 或 /api/v1/... - 设置保留策略
func (h *MailboxHandler) UpdateRetention(c *gin.Context) {
	account := middleware.GetAccount(c)
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}

	var req struct {
		KeepForever bool `json:"keep_forever"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ttlMinutes := h.mailboxTTLMinutes(c)

	mailbox, err := h.store.SetMailboxKeepForever(c.Request.Context(), id, account.ID, donationCreatorToken(c), req.KeepForever, ttlMinutes)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"mailbox": mailbox})
}

// POST /console/v1/mailboxes/retention/batch 或 /api/v1/... - 批量设置保留策略
func (h *MailboxHandler) BatchUpdateRetention(c *gin.Context) {
	account := middleware.GetAccount(c)
	var req struct {
		IDs         []string `json:"ids"`
		KeepForever bool     `json:"keep_forever"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids is required"})
		return
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id, err := parseUUID(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id in ids"})
			return
		}
		ids = append(ids, id)
	}

	ttlMinutes := h.mailboxTTLMinutes(c)

	items, err := h.store.SetManyMailboxesKeepForever(c.Request.Context(), account.ID, donationCreatorToken(c), ids, req.KeepForever, ttlMinutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no mailboxes updated"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": items, "updated_count": len(items)})
}

// DELETE /console/v1/mailboxes/:id 或 /api/v1/mailboxes/:id - 删除邮箱
func (h *MailboxHandler) Delete(c *gin.Context) {
	account := middleware.GetAccount(c)
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mailbox id"})
		return
	}

	if err := h.store.DeleteMailbox(c.Request.Context(), id, account.ID, donationCreatorToken(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "mailbox deleted"})
}

func (h *MailboxHandler) mailboxTTLMinutes(c *gin.Context) int {
	ttlMinutes := 30
	if ttlStr, err := h.store.GetSetting(c.Request.Context(), "mailbox_ttl_minutes"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(ttlStr)); err == nil && n >= 0 {
			ttlMinutes = n
		}
	}
	return ttlMinutes
}
