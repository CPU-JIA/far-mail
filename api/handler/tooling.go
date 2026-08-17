package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"farmail/middleware"
	"farmail/model"
	"farmail/store"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type ToolingHandler struct {
	store        *store.Store
	rdb          *redis.Client
	smtpServerIP string
	smtpHostname string
	lmtpMu       sync.RWMutex
	lmtpRunning  bool
	lmtpAddr     string
	smtpProbeMu  sync.Mutex
	smtpProbeAt  time.Time
	smtpProbeKey string
	smtpProbeOK  bool
	summaryMu    sync.Mutex
	summaryAt    time.Time
	summaryCache model.SystemSummary
}

// SetLMTPStatus is updated by main after the Go ingress listener is started.
func (h *ToolingHandler) SetLMTPStatus(running bool, addr string) {
	h.lmtpMu.Lock()
	defer h.lmtpMu.Unlock()
	h.lmtpRunning = running
	h.lmtpAddr = strings.TrimSpace(addr)
}

func NewToolingHandler(s *store.Store, rdb *redis.Client, smtpServerIP, smtpHostname string) *ToolingHandler {
	return &ToolingHandler{
		store:        s,
		rdb:          rdb,
		smtpServerIP: strings.TrimSpace(smtpServerIP),
		smtpHostname: strings.TrimSpace(smtpHostname),
	}
}

func (h *ToolingHandler) IntegrationAudit(c *gin.Context) {
	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	events, err := h.store.ListIntegrationAudit(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to load integration audit"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": events})
}

func (h *ToolingHandler) CleanupMailboxes(c *gin.Context) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	var req struct {
		Query       string `json:"query"`
		Domain      string `json:"domain"`
		OnlyExpired bool   `json:"only_expired"`
		OnlyEmpty   bool   `json:"only_empty"`
	}
	_ = c.ShouldBindJSON(&req)

	adminOwner := account.IsAdmin && token != nil && token.Scope == "owner"
	deleted, err := h.store.PurgeMailboxes(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), req.Query, req.Domain, req.OnlyEmpty, req.OnlyExpired)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted, "query": strings.TrimSpace(req.Query), "domain": strings.TrimSpace(req.Domain), "only_expired": req.OnlyExpired, "only_empty": req.OnlyEmpty})
}

func (h *ToolingHandler) CleanupEmails(c *gin.Context) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	var req struct {
		Query            string `json:"query"`
		Domain           string `json:"domain"`
		OlderThanMinutes int    `json:"older_than_minutes"`
	}
	_ = c.ShouldBindJSON(&req)

	adminOwner := account.IsAdmin && token != nil && token.Scope == "owner"
	deleted, err := h.store.PurgeEmails(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), req.Query, req.Domain, req.OlderThanMinutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted, "query": strings.TrimSpace(req.Query), "domain": strings.TrimSpace(req.Domain), "older_than_minutes": req.OlderThanMinutes})
}

func (h *ToolingHandler) MaintenancePreview(c *gin.Context) {
	minutes, _ := strconv.Atoi(strings.TrimSpace(c.Query("older_than_minutes")))
	preview, err := h.store.PreviewMaintenance(c.Request.Context(), minutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (h *ToolingHandler) CleanupPreview(c *gin.Context) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	var req struct {
		Kind             string `json:"kind"`
		Query            string `json:"query"`
		Domain           string `json:"domain"`
		OnlyExpired      bool   `json:"only_expired"`
		OnlyEmpty        bool   `json:"only_empty"`
		OlderThanMinutes int    `json:"older_than_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cleanup preview"})
		return
	}
	adminOwner := account.IsAdmin && token != nil && token.Scope == "owner"
	switch strings.ToLower(strings.TrimSpace(req.Kind)) {
	case "mailboxes":
		count, err := h.store.PreviewPurgeMailboxes(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), req.Query, req.Domain, req.OnlyEmpty, req.OnlyExpired)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to preview mailbox cleanup"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"kind": "mailboxes", "matching_mailboxes": count, "matching_emails": 0, "matching_bytes": 0})
	case "emails":
		count, bytes, err := h.store.PreviewPurgeEmails(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), req.Query, req.Domain, req.OlderThanMinutes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to preview email cleanup"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"kind": "emails", "matching_mailboxes": 0, "matching_emails": count, "matching_bytes": bytes})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "cleanup preview kind must be mailboxes or emails"})
	}
}

func (h *ToolingHandler) APIUsage(c *gin.Context) {
	hours, _ := strconv.Atoi(strings.TrimSpace(c.Query("hours")))
	report, err := h.store.GetAPIUsageReport(c.Request.Context(), hours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, report)
}

func (h *ToolingHandler) LookupMailbox(c *gin.Context) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	address := strings.ToLower(strings.TrimSpace(c.Query("address")))
	if address == "" || !strings.Contains(address, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "address query is required"})
		return
	}

	adminOwner := account.IsAdmin && token != nil && token.Scope == "owner"
	mailbox, err := h.store.LookupMailboxScoped(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), address)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mailbox": mailbox})
}

func (h *ToolingHandler) LookupLatest(c *gin.Context) {
	mailbox, email, err := h.resolveLatest(c)
	if err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"mailbox": mailbox, "email": email})
}

func (h *ToolingHandler) LookupLatestCode(c *gin.Context) {
	mailbox, state, email, err := h.resolveLatestProjection(c)
	if err != nil {
		return
	}
	code := ""
	matchedBy := ""
	var emailID any
	var sender, subject string
	var receivedAt any
	if state != nil && state.LatestEmailID != nil {
		emailID = *state.LatestEmailID
		sender = state.LatestSender
		subject = state.LatestSubject
		receivedAt = state.LatestReceivedAt
		code = state.LatestCode
		matchedBy = state.LatestCodeSource
		if code != "" && !store.IsLikelyVerificationCode(code) {
			if latest, err := h.store.GetLatestEmailForMailbox(c.Request.Context(), mailbox.ID); err == nil {
				email = latest
				code, matchedBy = extractCode(strings.TrimSpace(latest.Subject + "\n" + latest.BodyText + "\n" + latest.BodyHTML))
				emailID = latest.ID
				sender = latest.Sender
				subject = latest.Subject
				receivedAt = latest.ReceivedAt
			} else {
				code = ""
				matchedBy = ""
			}
		}
	} else if email != nil {
		emailID = email.ID
		sender = email.Sender
		subject = email.Subject
		receivedAt = email.ReceivedAt
		code = email.ParsedCode
		matchedBy = email.ParsedCodeSource
		if code == "" || !store.IsLikelyVerificationCode(code) {
			code, matchedBy = extractCode(strings.TrimSpace(email.Subject + "\n" + email.BodyText + "\n" + email.BodyHTML))
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"mailbox":     mailbox,
		"email_id":    emailID,
		"sender":      sender,
		"subject":     subject,
		"received_at": receivedAt,
		"code":        code,
		"matched_by":  matchedBy,
		"has_code":    code != "",
	})
}

func (h *ToolingHandler) LookupLatestLink(c *gin.Context) {
	mailbox, state, email, err := h.resolveLatestProjection(c)
	if err != nil {
		return
	}
	link := ""
	matchedBy := ""
	var emailID any
	var sender, subject string
	var receivedAt any
	if state != nil && state.LatestEmailID != nil {
		emailID = *state.LatestEmailID
		sender = state.LatestSender
		subject = state.LatestSubject
		receivedAt = state.LatestReceivedAt
		link = state.LatestLink
		matchedBy = state.LatestLinkSource
	} else if email != nil {
		emailID = email.ID
		sender = email.Sender
		subject = email.Subject
		receivedAt = email.ReceivedAt
		link = email.ParsedLink
		matchedBy = email.ParsedLinkSource
		if link == "" {
			link, matchedBy = store.ExtractLink(strings.TrimSpace(email.Subject + "\n" + email.BodyText + "\n" + email.BodyHTML))
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"mailbox":     mailbox,
		"email_id":    emailID,
		"sender":      sender,
		"subject":     subject,
		"received_at": receivedAt,
		"link":        link,
		"matched_by":  matchedBy,
		"has_link":    link != "",
	})
}

func (h *ToolingHandler) RecentCodes(c *gin.Context) {
	account := middleware.GetAccount(c)
	limit := 12
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	items, err := h.store.ListRecentCodeActivity(c.Request.Context(), account.ID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for i := range items {
		items[i].ExtractedCode, items[i].MatchedBy = extractCode(strings.TrimSpace(items[i].Subject + "\n" + items[i].BodyText + "\n" + items[i].BodyHTML))
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ToolingHandler) DomainHealth(c *gin.Context) {
	items, err := h.store.ListDomainHealthSnapshots(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "server_ip": h.effectiveSMTPServerIP(c.Request.Context())})
}

func (h *ToolingHandler) RefreshDomainHealth(c *gin.Context) {
	if err := h.RefreshDomainHealthCache(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items, err := h.store.ListDomainHealthSnapshots(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ToolingHandler) SystemSummary(c *gin.Context) {
	c.JSON(http.StatusOK, h.buildSystemSummary(c.Request.Context()))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (h *ToolingHandler) RefreshDomainHealthCache(ctx context.Context) error {
	domains, err := h.store.ListDomains(ctx)
	if err != nil {
		return err
	}
	serverIP := h.effectiveSMTPServerIP(ctx)
	for _, d := range domains {
		rootOK, rootHosts, rootStatus := store.CheckDomainMX(d.Domain, serverIP)
		probeDomain := fmt.Sprintf("__mxprobe%d.%s", time.Now().UnixNano(), d.Domain)
		wildOK, wildHosts, wildStatus := store.CheckDomainMX(probeDomain, serverIP)
		if err := h.store.UpsertDomainHealthSnapshot(ctx, model.DomainHealth{
			Domain:           d.Domain,
			IsActive:         d.IsActive,
			Status:           d.Status,
			MxCheckedAt:      d.MxCheckedAt,
			RootMXOK:         rootOK,
			WildcardMXOK:     wildOK,
			RootMXStatus:     rootStatus,
			WildcardMXStatus: wildStatus,
			RootMXHosts:      rootHosts,
			WildcardMXHosts:  wildHosts,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *ToolingHandler) effectiveSMTPServerIP(ctx context.Context) string {
	if v, err := h.store.GetSetting(ctx, "smtp_server_ip"); err == nil && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return h.smtpServerIP
}

func (h *ToolingHandler) effectiveSMTPHostname(ctx context.Context) string {
	if v, err := h.store.GetSetting(ctx, "smtp_hostname"); err == nil && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return h.smtpHostname
}

func (h *ToolingHandler) resolveLatest(c *gin.Context) (*model.Mailbox, *model.Email, error) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	address := strings.ToLower(strings.TrimSpace(c.Query("address")))
	if address == "" || !strings.Contains(address, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "address query is required"})
		return nil, nil, fmt.Errorf("bad request")
	}

	adminOwner := account.IsAdmin && token != nil && token.Scope == "owner"
	mailbox, email, err := h.store.LookupLatestEmailScoped(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), address)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
			return nil, nil, err
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, nil, err
	}
	if email == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no emails found"})
		return nil, nil, pgx.ErrNoRows
	}
	return mailbox, email, nil
}

func (h *ToolingHandler) resolveLatestProjection(c *gin.Context) (*model.Mailbox, *model.MailboxState, *model.Email, error) {
	account := middleware.GetAccount(c)
	token := middleware.GetToken(c)
	address := strings.ToLower(strings.TrimSpace(c.Query("address")))
	if address == "" || !strings.Contains(address, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "address query is required"})
		return nil, nil, nil, fmt.Errorf("bad request")
	}

	adminOwner := account.IsAdmin && token != nil && token.Scope == "owner"
	mailbox, state, err := h.store.LookupMailboxProjectionScoped(c.Request.Context(), account.ID, adminOwner, donationCreatorToken(c), address)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "mailbox not found"})
			return nil, nil, nil, err
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, nil, nil, err
	}

	if state != nil {
		return mailbox, state, nil, nil
	}

	email, err := h.store.GetLatestEmailForMailbox(c.Request.Context(), mailbox.ID)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "no emails found"})
			return nil, nil, nil, err
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, nil, nil, err
	}
	return mailbox, nil, email, nil
}

func extractCode(source string) (string, string) {
	return store.ExtractCode(source)
}

func (h *ToolingHandler) buildSystemSummary(ctx context.Context) model.SystemSummary {
	h.summaryMu.Lock()
	if !h.summaryAt.IsZero() && time.Since(h.summaryAt) < 2*time.Second {
		cached := h.summaryCache
		h.summaryMu.Unlock()
		return cached
	}
	h.summaryMu.Unlock()

	dbOK := h.store.Ping(ctx) == nil
	redisOK := h.rdb.Ping(ctx).Err() == nil
	smtpHostname, smtpIP, smtpSource := h.effectiveSMTPConfig(ctx)
	smtpReachable := h.smtpReachable(smtpHostname, smtpIP)
	h.lmtpMu.RLock()
	lmtpRunning, lmtpAddr := h.lmtpRunning, h.lmtpAddr
	h.lmtpMu.RUnlock()
	activeDomains, _ := h.store.GetActiveDomains(ctx)
	mailboxTotal, emailTotal, _ := h.store.GetMailboxEmailTotals(ctx)
	healthyRoot, healthyWild, unhealthy, lastChecked, _ := h.store.GetDomainHealthSummary(ctx)
	summary := model.SystemSummary{
		DBOK:                 dbOK,
		RedisOK:              redisOK,
		SMTPHostname:         smtpHostname,
		SMTPServerIP:         smtpIP,
		SMTPConfigured:       smtpHostname != "" || smtpIP != "",
		SMTPReachable:        smtpReachable,
		SMTPSource:           smtpSource,
		LMTPRunning:          lmtpRunning,
		LMTPAddr:             lmtpAddr,
		MailboxTotal:         mailboxTotal,
		EmailTotal:           emailTotal,
		ActiveDomainCount:    len(activeDomains),
		HealthyRootDomains:   healthyRoot,
		HealthyWildcardCount: healthyWild,
		UnhealthyDomainCount: unhealthy,
		LastHealthCheckAt:    lastChecked,
	}
	h.summaryMu.Lock()
	h.summaryCache = summary
	h.summaryAt = time.Now()
	h.summaryMu.Unlock()
	return summary
}

func (h *ToolingHandler) smtpReachable(hostname, ip string) bool {
	key := strings.TrimSpace(ip) + "|" + strings.TrimSpace(hostname)
	h.smtpProbeMu.Lock()
	defer h.smtpProbeMu.Unlock()
	if key == h.smtpProbeKey && time.Since(h.smtpProbeAt) < 10*time.Second {
		return h.smtpProbeOK
	}
	ok := false
	if ip != "" {
		ok = probeTCP(ip, 25)
	}
	if !ok && hostname != "" {
		ok = probeTCP(hostname, 25)
	}
	h.smtpProbeKey, h.smtpProbeAt, h.smtpProbeOK = key, time.Now(), ok
	return ok
}

func (h *ToolingHandler) effectiveSMTPConfig(ctx context.Context) (hostname, ip, source string) {
	hostname, ip = h.smtpHostname, h.smtpServerIP
	source = "environment"
	settingsHostname, hostnameFromSettings := h.settingValue(ctx, "smtp_hostname")
	settingsIP, ipFromSettings := h.settingValue(ctx, "smtp_server_ip")
	if hostnameFromSettings {
		v := settingsHostname
		hostname = strings.TrimSpace(v)
	}
	if ipFromSettings {
		v := settingsIP
		ip = strings.TrimSpace(v)
	}
	if hostnameFromSettings && ipFromSettings {
		source = "settings"
	} else if hostnameFromSettings || ipFromSettings {
		source = "mixed"
	}
	if hostname == "" && ip == "" {
		source = "not_configured"
	}
	return hostname, ip, source
}

func (h *ToolingHandler) settingValue(ctx context.Context, key string) (string, bool) {
	v, err := h.store.GetSetting(ctx, key)
	if err != nil || strings.TrimSpace(v) == "" {
		return "", false
	}
	return strings.TrimSpace(v), true
}

func probeTCP(host string, port int) bool {
	address := net.JoinHostPort(strings.TrimSpace(host), fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", address, 700*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
