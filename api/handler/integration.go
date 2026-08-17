package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"farmail/integrations"
	"farmail/store"

	"github.com/gin-gonic/gin"
)

type IntegrationHandler struct {
	store        *store.Store
	secrets      *integrations.SecretStore
	dispatcher   *integrations.Dispatcher
	cloudflare   *integrations.CloudflareClient
	smtpServerIP string
	smtpHostname string
}

func NewIntegrationHandler(s *store.Store, secrets *integrations.SecretStore, dispatcher *integrations.Dispatcher, smtpServerIP, smtpHostname string) *IntegrationHandler {
	return &IntegrationHandler{store: s, secrets: secrets, dispatcher: dispatcher, cloudflare: integrations.NewCloudflareClient(), smtpServerIP: smtpServerIP, smtpHostname: smtpHostname}
}

func (h *IntegrationHandler) NotificationConfig(c *gin.Context) {
	config := h.secrets.Notifications()
	c.JSON(http.StatusOK, gin.H{
		"generic":  gin.H{"enabled": config.Generic.Enabled, "configured": config.Generic.URL != "", "target": maskedTarget(config.Generic.URL), "signed": config.Generic.Secret != ""},
		"telegram": gin.H{"enabled": config.Telegram.Enabled, "configured": config.Telegram.BotToken != "" && config.Telegram.ChatID != "", "chat_id": config.Telegram.ChatID},
		"discord":  gin.H{"enabled": config.Discord.Enabled, "configured": config.Discord.URL != "", "target": maskedTarget(config.Discord.URL)},
		"delivery": h.dispatcher.Status(),
	})
}

type notificationChannelUpdate struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Secret   string `json:"secret"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	Clear    bool   `json:"clear"`
}

type notificationUpdateRequest struct {
	Generic  notificationChannelUpdate `json:"generic"`
	Telegram notificationChannelUpdate `json:"telegram"`
	Discord  notificationChannelUpdate `json:"discord"`
}

func (h *IntegrationHandler) UpdateNotifications(c *gin.Context) {
	var request notificationUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notification configuration"})
		return
	}
	config := h.secrets.Notifications()
	if request.Generic.Clear {
		config.Generic = integrations.GenericWebhookConfig{}
	} else {
		config.Generic.Enabled = request.Generic.Enabled
		if value := strings.TrimSpace(request.Generic.URL); value != "" {
			if err := validateIntegrationURL(value); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid generic Webhook URL"})
				return
			}
			config.Generic.URL = value
		}
		if request.Generic.Secret != "" {
			config.Generic.Secret = request.Generic.Secret
		}
	}
	if request.Telegram.Clear {
		config.Telegram = integrations.TelegramConfig{}
	} else {
		config.Telegram.Enabled = request.Telegram.Enabled
		if value := strings.TrimSpace(request.Telegram.BotToken); value != "" {
			config.Telegram.BotToken = value
		}
		if value := strings.TrimSpace(request.Telegram.ChatID); value != "" {
			config.Telegram.ChatID = value
		}
	}
	if request.Discord.Clear {
		config.Discord = integrations.DiscordConfig{}
	} else {
		config.Discord.Enabled = request.Discord.Enabled
		if value := strings.TrimSpace(request.Discord.URL); value != "" {
			if err := validateIntegrationURL(value); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid Discord Webhook URL"})
				return
			}
			config.Discord.URL = value
		}
	}
	if config.Generic.Enabled && config.Generic.URL == "" || config.Telegram.Enabled && (config.Telegram.BotToken == "" || config.Telegram.ChatID == "") || config.Discord.Enabled && config.Discord.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled notification channels require complete credentials"})
		return
	}
	if err := h.secrets.UpdateNotifications(func(value *integrations.NotificationConfig) { *value = config }); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to store notification configuration"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "notification configuration updated"})
}

func (h *IntegrationHandler) TestNotification(c *gin.Context) {
	var request struct {
		Channel string `json:"channel"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notification channel required"})
		return
	}
	channel := strings.ToLower(strings.TrimSpace(request.Channel))
	if channel != "generic" && channel != "telegram" && channel != "discord" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported notification channel"})
		return
	}
	if err := h.dispatcher.Test(c.Request.Context(), channel); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "test notification delivered"})
}

func (h *IntegrationHandler) CloudflareConfig(c *gin.Context) {
	config, source := h.secrets.Cloudflare()
	c.JSON(http.StatusOK, gin.H{"configured": strings.TrimSpace(config.APIToken) != "", "source": source})
}

func (h *IntegrationHandler) UpdateCloudflare(c *gin.Context) {
	_, source := h.secrets.Cloudflare()
	if source == "environment" {
		c.JSON(http.StatusConflict, gin.H{"error": "Cloudflare API Token is managed by environment"})
		return
	}
	var request struct {
		APIToken string `json:"api_token"`
		Clear    bool   `json:"clear"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid Cloudflare configuration"})
		return
	}
	token := strings.TrimSpace(request.APIToken)
	if request.Clear {
		token = ""
	} else if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cloudflare API Token required"})
		return
	}
	if err := h.secrets.SetCloudflare(integrations.CloudflareConfig{APIToken: token}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to store Cloudflare configuration"})
		return
	}
	_ = h.store.RecordIntegrationAudit(c.Request.Context(), "cloudflare", "credentials", "", map[bool]string{true: "cleared", false: "updated"}[request.Clear], "token value omitted")
	c.JSON(http.StatusOK, gin.H{"message": "Cloudflare configuration updated"})
}

func (h *IntegrationHandler) TestCloudflare(c *gin.Context) {
	var request struct {
		Domain string `json:"domain"`
	}
	_ = c.ShouldBindJSON(&request)
	config, _ := h.secrets.Cloudflare()
	result, err := h.cloudflare.Test(c.Request.Context(), config.APIToken, request.Domain)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Cloudflare connection verified", "zone": result})
}

type dnsRequest struct {
	Domain           string `json:"domain"`
	ConfirmConflicts bool   `json:"confirm_conflicts"`
}

func (h *IntegrationHandler) DNSPreview(c *gin.Context) {
	var request dnsRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain required"})
		return
	}
	records, err := integrations.BuildDNSRecords(request.Domain, h.effectiveSetting(c, "smtp_hostname", h.smtpHostname), h.effectiveSetting(c, "smtp_server_ip", h.smtpServerIP))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	items := make([]integrations.DNSAction, 0, len(records))
	for _, record := range records {
		items = append(items, integrations.DNSAction{Record: record, Status: "required"})
	}
	c.JSON(http.StatusOK, integrations.DNSPlan{Domain: strings.ToLower(strings.TrimSpace(request.Domain)), Items: items})
}

func (h *IntegrationHandler) CloudflarePreview(c *gin.Context) {
	h.cloudflarePlan(c, false)
}

func (h *IntegrationHandler) CloudflareApply(c *gin.Context) {
	h.cloudflarePlan(c, true)
}

func (h *IntegrationHandler) cloudflarePlan(c *gin.Context, apply bool) {
	var request dnsRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain required"})
		return
	}
	records, err := integrations.BuildDNSRecords(request.Domain, h.effectiveSetting(c, "smtp_hostname", h.smtpHostname), h.effectiveSetting(c, "smtp_server_ip", h.smtpServerIP))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config, _ := h.secrets.Cloudflare()
	var plan integrations.DNSPlan
	if apply {
		plan, err = h.cloudflare.Apply(c.Request.Context(), config.APIToken, request.Domain, records, request.ConfirmConflicts)
	} else {
		plan, err = h.cloudflare.Preview(c.Request.Context(), config.APIToken, request.Domain, records)
	}
	if err != nil {
		status := "failed"
		if plan.RolledBack {
			status = "rolled_back"
		}
		_ = h.store.RecordIntegrationAudit(c.Request.Context(), "cloudflare", map[bool]string{true: "apply", false: "preview"}[apply], plan.Domain, status, fmt.Sprintf("items=%d", len(plan.Items)))
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "plan": plan})
		return
	}
	_ = h.store.RecordIntegrationAudit(c.Request.Context(), "cloudflare", map[bool]string{true: "apply", false: "preview"}[apply], plan.Domain, "success", fmt.Sprintf("items=%d", len(plan.Items)))
	c.JSON(http.StatusOK, plan)
}

func (h *IntegrationHandler) effectiveSetting(c *gin.Context, key, fallback string) string {
	if value, err := h.store.GetSetting(c.Request.Context(), key); err == nil && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(fallback)
}

func maskedTarget(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Hostname() + "/..."
}

func validateIntegrationURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return url.InvalidHostError(value)
	}
	return nil
}
