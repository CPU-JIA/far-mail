package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"farmail/config"
	"farmail/handler"
	"farmail/ingress"
	"farmail/integrations"
	"farmail/middleware"
	"farmail/store"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	cleanupIntervalSeconds, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AUTO_CLEAN_INTERVAL_SECONDS")))
	if cleanupIntervalSeconds <= 0 {
		cleanupIntervalSeconds = 60
	}
	cleanupEmailMaxAgeMinutes, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AUTO_CLEAN_EMAIL_MAX_AGE_MINUTES")))
	if cleanupEmailMaxAgeMinutes <= 0 {
		cleanupEmailMaxAgeMinutes = 1440
	}
	cleanupEmailMaxCount, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AUTO_CLEAN_EMAIL_MAX_COUNT")))
	if cleanupEmailMaxCount <= 0 {
		cleanupEmailMaxCount = 30000
	}
	disableCleanupLoop := strings.EqualFold(strings.TrimSpace(os.Getenv("DISABLE_CLEANUP_LOOP")), "true")
	disableDomainHealthLoop := strings.EqualFold(strings.TrimSpace(os.Getenv("DISABLE_DOMAIN_HEALTH_LOOP")), "true")
	disableMXVerifierLoop := strings.EqualFold(strings.TrimSpace(os.Getenv("DISABLE_MX_VERIFIER_LOOP")), "true")

	// ==================== 连接数据库 ====================
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := store.New(ctx, cfg.DBDSN)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	defer db.Close()
	log.Println("✓ Database connected")

	// ==================== 连接 Redis ====================
	rdb := redis.NewClient(&redis.Options{
		Addr:            cfg.RedisAddr,
		Password:        cfg.RedisPassword,
		DB:              0,
		PoolSize:        16,
		MinIdleConns:    2,
		MaxIdleConns:    4,
		ConnMaxIdleTime: 2 * time.Minute,
		DialTimeout:     3 * time.Second,
		ReadTimeout:     2 * time.Second,
		WriteTimeout:    2 * time.Second,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}
	defer rdb.Close()
	log.Println("✓ Redis connected")

	// ==================== Gin 路由 ====================
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	trustedProxyCIDRs := strings.FieldsFunc(os.Getenv("TRUSTED_PROXY_CIDRS"), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	if len(trustedProxyCIDRs) == 0 {
		trustedProxyCIDRs = []string{"172.16.0.0/12"}
	}
	if err := r.SetTrustedProxies(trustedProxyCIDRs); err != nil {
		log.Fatalf("invalid TRUSTED_PROXY_CIDRS: %v", err)
	}
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.ErrorEnvelope())

	// CORS is limited to the configured owner console origin. Same-origin
	// deployments leave this empty and therefore do not need cross-origin access.
	allowedOrigin := strings.TrimSpace(os.Getenv("CONSOLE_ORIGIN"))
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:8889"
	}
	// Local development commonly alternates between localhost and 127.0.0.1.
	// Keep production CORS exact while allowing only these equivalent loopback
	// origins when the configured console origin is local.
	allowedOrigins := []string{allowedOrigin}
	if strings.HasPrefix(allowedOrigin, "http://localhost:") {
		port := strings.TrimPrefix(allowedOrigin, "http://localhost:")
		allowedOrigins = append(allowedOrigins, "http://127.0.0.1:"+port, "http://[::1]:"+port)
	} else if strings.HasPrefix(allowedOrigin, "http://127.0.0.1:") {
		port := strings.TrimPrefix(allowedOrigin, "http://127.0.0.1:")
		allowedOrigins = append(allowedOrigins, "http://localhost:"+port, "http://[::1]:"+port)
	}
	internalSyncKey := strings.TrimSpace(os.Getenv("INTERNAL_SYNC_KEY"))
	if internalSyncKey == "" || internalSyncKey == "change_me_internal_sync_key" || internalSyncKey == "replace_with_64_hex_characters" {
		log.Fatal("INTERNAL_SYNC_KEY must be set to a non-default secret")
	}
	r.Use(cors.New(cors.Config{
		AllowOrigins: allowedOrigins,
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "X-Admin-Key", middleware.RequestIDHeader},
		ExposeHeaders: []string{
			"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset", "RateLimit-Policy", "Retry-After",
			"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
			"X-RateLimit-Minute-Limit", "X-RateLimit-Minute-Remaining", "X-RateLimit-Minute-Reset",
			"X-RateLimit-Daily-Limit", "X-RateLimit-Daily-Remaining", "X-RateLimit-Daily-Reset",
			"X-RateLimit-Total-Limit", "X-RateLimit-Total-Remaining",
			middleware.RequestIDHeader, "X-Token-Scope", "X-Token-Daily-Remaining", "X-Token-Total-Remaining",
		},
		MaxAge: 12 * time.Hour,
	}))

	// 健康检查（无需认证）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().Unix()})
	})
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	// 初始化 handlers
	accountH := handler.NewAccountHandler(db)
	domainH := handler.NewDomainHandler(db, cfg.SMTPServerIP, cfg.SMTPHostname)
	mailboxH := handler.NewMailboxHandler(db)
	lmtpSrv := ingress.NewServer(db, ingress.ConfigFromEnv(cfg.SMTPHostname))
	integrationSecrets, err := integrations.NewSecretStore(strings.TrimSpace(os.Getenv("INTEGRATION_SECRETS_FILE")))
	if err != nil {
		log.Fatalf("failed to load integration configuration: %v", err)
	}
	notificationDispatcher := integrations.NewDispatcher(integrationSecrets)
	emailH := handler.NewEmailHandler(db, lmtpSrv)
	toolingH := handler.NewToolingHandler(db, rdb, cfg.SMTPServerIP, cfg.SMTPHostname)
	analyticsH := handler.NewAnalyticsHandler(db)
	tokenH := handler.NewTokenHandler(db)
	settingH := handler.NewSettingHandler(db, cfg.SMTPServerIP, cfg.SMTPHostname)
	integrationH := handler.NewIntegrationHandler(db, integrationSecrets, notificationDispatcher, cfg.SMTPServerIP, cfg.SMTPHostname)
	apiUsageRecorder := middleware.NewAPIUsageRecorder(db)
	defer apiUsageRecorder.Close()
	effectiveSMTPServerIP := func(ctx context.Context) string {
		if value, err := db.GetSetting(ctx, "smtp_server_ip"); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(cfg.SMTPServerIP)
	}

	registerPublicRoutes := func(group *gin.RouterGroup) {
		group.GET("/settings", settingH.GetPublic)
		group.GET("/logo", settingH.GetLogo)
		group.POST("/domains/submit", middleware.RateLimit(rdb, 5, 60), domainH.PublicSubmit)
		group.POST("/domains/status", middleware.RateLimit(rdb, 120, 60), domainH.PublicStatus)
	}

	registerConsoleRoutes := func(group *gin.RouterGroup) {
		group.Use(middleware.AdminAuth(db), middleware.AdminOnly())

		group.GET("/session", accountH.Session)

		group.GET("/domains", domainH.List)
		group.GET("/domains/health", toolingH.DomainHealth)
		group.GET("/system/summary", toolingH.SystemSummary)
		group.GET("/activity/codes", toolingH.RecentCodes)
		group.GET("/analytics/summary", analyticsH.Summary)
		group.GET("/operations/ingress", func(c *gin.Context) {
			if lmtpSrv == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "LMTP ingress is not running"})
				return
			}
			c.JSON(http.StatusOK, lmtpSrv.Stats())
		})
		group.GET("/operations/runtime", func(c *gin.Context) {
			redisStats := rdb.PoolStats()
			c.JSON(http.StatusOK, gin.H{
				"postgres_pool": db.PoolStats(),
				"cache":         db.CacheStats(),
				"redis_pool": gin.H{
					"hits":        redisStats.Hits,
					"misses":      redisStats.Misses,
					"timeouts":    redisStats.Timeouts,
					"total_conns": redisStats.TotalConns,
					"idle_conns":  redisStats.IdleConns,
					"stale_conns": redisStats.StaleConns,
				},
				"api_observability": apiUsageRecorder.Stats(),
				"lmtp":              lmtpSrv.Stats(),
			})
		})
		group.GET("/operations/api-usage", toolingH.APIUsage)
		group.GET("/operations/audit", toolingH.IntegrationAudit)
		group.GET("/operations/maintenance/preview", toolingH.MaintenancePreview)
		group.POST("/operations/cleanup/preview", toolingH.CleanupPreview)

		group.POST("/mailboxes", mailboxH.Create)
		group.GET("/mailboxes", mailboxH.List)
		group.POST("/mailboxes/retention/batch", mailboxH.BatchUpdateRetention)
		group.GET("/mailboxes/:id", mailboxH.Get)
		group.PUT("/mailboxes/:id/retention", mailboxH.UpdateRetention)
		group.DELETE("/mailboxes/:id", mailboxH.Delete)
		group.POST("/mailboxes/cleanup", toolingH.CleanupMailboxes)

		group.GET("/mailboxes/:id/emails", emailH.List)
		group.GET("/mailboxes/:id/events", emailH.Events)
		group.GET("/mailboxes/:id/emails/:email_id", emailH.Get)
		group.DELETE("/mailboxes/:id/emails/:email_id", emailH.Delete)
		group.POST("/emails/cleanup", toolingH.CleanupEmails)

		group.GET("/lookup/mailbox", toolingH.LookupMailbox)
		group.GET("/lookup/latest", toolingH.LookupLatest)
		group.GET("/lookup/latest-code", toolingH.LookupLatestCode)
		group.GET("/lookup/latest-link", toolingH.LookupLatestLink)

		group.GET("/tokens", tokenH.List)
		group.POST("/tokens", tokenH.Create)
		group.PATCH("/tokens/:id", tokenH.Update)
		group.POST("/tokens/:id/rotate", tokenH.Rotate)
		group.POST("/tokens/:id/disable", tokenH.Disable)
		group.POST("/tokens/:id/enable", tokenH.Enable)
		group.DELETE("/tokens/:id", tokenH.Delete)

		group.POST("/auth-key/rotate", accountH.RotateAdminAuthKey)
		group.DELETE("/domains/:id", domainH.Delete)
		group.PUT("/domains/:id/toggle", domainH.Toggle)
		group.POST("/domains/mx-register", domainH.MXRegister)
		group.POST("/domains/health/refresh", toolingH.RefreshDomainHealth)
		group.GET("/donations", domainH.ListDonations)
		group.POST("/donations/tokens/:id/adjust", domainH.AdjustDonationToken)
		group.POST("/donations/:id/recheck", domainH.RecheckDonation)
		group.POST("/donations/:id/revoke", domainH.RevokeDonation)
		group.POST("/donations/:id/adjust", domainH.AdjustDonation)
		group.POST("/donations/policy/apply", domainH.ApplyDonationPolicy)
		group.GET("/settings", settingH.AdminGetAll)
		group.PUT("/settings", settingH.AdminUpdate)
		group.GET("/integrations/notifications", integrationH.NotificationConfig)
		group.PUT("/integrations/notifications", integrationH.UpdateNotifications)
		group.POST("/integrations/notifications/test", integrationH.TestNotification)
		group.GET("/integrations/cloudflare", integrationH.CloudflareConfig)
		group.PUT("/integrations/cloudflare", integrationH.UpdateCloudflare)
		group.POST("/integrations/cloudflare/test", integrationH.TestCloudflare)
		group.POST("/integrations/cloudflare/preview", integrationH.CloudflarePreview)
		group.POST("/integrations/cloudflare/apply", integrationH.CloudflareApply)
		group.POST("/dns/preview", integrationH.DNSPreview)
	}

	registerAPIRoutes := func(group *gin.RouterGroup) {
		group.POST("/donations", middleware.DonationAuth(db), domainH.AuthenticatedDonationSubmit)
		group.Use(middleware.APIAuth(db, rdb), apiUsageRecorder.Middleware())

		group.POST("/mailboxes", middleware.RequireAnyScope("cleanup", "owner"), mailboxH.Create)
		group.GET("/domains", domainH.ListActive)
		group.GET("/mailboxes", mailboxH.List)
		group.POST("/mailboxes/retention/batch", middleware.RequireAnyScope("cleanup", "owner"), mailboxH.BatchUpdateRetention)
		group.GET("/mailboxes/:id", mailboxH.Get)
		group.PUT("/mailboxes/:id/retention", middleware.RequireAnyScope("cleanup", "owner"), mailboxH.UpdateRetention)
		group.DELETE("/mailboxes/:id", middleware.RequireAnyScope("cleanup", "owner"), mailboxH.Delete)
		group.POST("/mailboxes/cleanup", middleware.RequireAnyScope("cleanup", "owner"), toolingH.CleanupMailboxes)

		group.GET("/mailboxes/:id/emails", emailH.List)
		group.GET("/mailboxes/:id/events", emailH.Events)
		group.GET("/mailboxes/:id/emails/:email_id", emailH.Get)
		group.DELETE("/mailboxes/:id/emails/:email_id", middleware.RequireAnyScope("cleanup", "owner"), emailH.Delete)
		group.POST("/emails/cleanup", middleware.RequireAnyScope("cleanup", "owner"), toolingH.CleanupEmails)

		group.GET("/lookup/mailbox", toolingH.LookupMailbox)
		group.GET("/lookup/latest", toolingH.LookupLatest)
		group.GET("/lookup/latest-code", toolingH.LookupLatestCode)
		group.GET("/lookup/latest-link", toolingH.LookupLatestLink)
	}

	registerPublicRoutes(r.Group("/public/v1"))
	registerConsoleRoutes(r.Group("/console/v1"))
	registerAPIRoutes(r.Group("/api/v1"))

	// 内部接口仅保留 Postfix 域名同步。
	internal := r.Group("/internal")
	{
		internal.GET("/domains.txt", func(c *gin.Context) {
			provided := strings.TrimSpace(c.GetHeader("X-Internal-Sync-Key"))
			if provided == "" || provided != internalSyncKey {
				c.Status(http.StatusUnauthorized)
				return
			}
			domains, err := db.GetActiveDomains(c.Request.Context())
			if err != nil {
				c.String(http.StatusInternalServerError, "")
				return
			}
			lines := make([]string, 0, len(domains))
			for _, d := range domains {
				lines = append(lines, d.Domain+"     OK")
			}
			c.Header("Content-Type", "text/plain; charset=utf-8")
			c.String(http.StatusOK, strings.Join(lines, "\n"))
		})
	}

	// ==================== LMTP 收件入口 ====================
	if err := lmtpSrv.Start(ctx); err != nil {
		toolingH.SetLMTPStatus(false, "")
		log.Fatalf("failed to start LMTP ingress: %v", err)
	}
	toolingH.SetLMTPStatus(true, ingress.ConfigFromEnv(cfg.SMTPHostname).Addr)
	notificationCtx, stopNotifications := context.WithCancel(ctx)
	notificationEvents, unsubscribeNotifications := lmtpSrv.Subscribe(uuid.Nil)
	notificationDispatcher.Start(notificationCtx, notificationEvents, unsubscribeNotifications)

	// ==================== 邮箱自动过期清理 ====================
	if disableCleanupLoop {
		log.Println("! Go mailbox/email cleaner disabled by DISABLE_CLEANUP_LOOP")
	} else {
		go func() {
			retentionMinutes := func() int {
				raw, err := db.GetSetting(ctx, "email_retention_minutes")
				if err != nil {
					return cleanupEmailMaxAgeMinutes
				}
				value, err := strconv.Atoi(strings.TrimSpace(raw))
				if err != nil || value < 0 {
					return cleanupEmailMaxAgeMinutes
				}
				return value
			}
			ticker := time.NewTicker(time.Duration(cleanupIntervalSeconds) * time.Second)
			defer ticker.Stop()
			log.Printf("✓ Mailbox/email cleaner started (interval=%ds, email_max_age=%dmin, email_max_count=%d)",
				cleanupIntervalSeconds, retentionMinutes(), cleanupEmailMaxCount)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if deleted, err := db.DeleteExpiredMailboxes(ctx); err != nil {
					log.Printf("[cleaner] error: %v", err)
				} else if deleted > 0 {
					log.Printf("[cleaner] deleted %d expired mailboxes", deleted)
				}
				if maxAgeMinutes := retentionMinutes(); maxAgeMinutes > 0 {
					if deleted, err := db.DeleteEmailsOlderThan(ctx, time.Duration(maxAgeMinutes)*time.Minute); err != nil {
						log.Printf("[cleaner] delete old emails error: %v", err)
					} else if deleted > 0 {
						log.Printf("[cleaner] deleted %d old emails", deleted)
					}
				}
				if deleted, err := db.TrimEmailsToMaxCount(ctx, cleanupEmailMaxCount); err != nil {
					log.Printf("[cleaner] trim emails error: %v", err)
				} else if deleted > 0 {
					log.Printf("[cleaner] trimmed %d overflow emails", deleted)
				}
			}
		}()
	}

	// ==================== 域名健康快照刷新 ====================
	if disableDomainHealthLoop {
		log.Println("! Go domain health refresher disabled by DISABLE_DOMAIN_HEALTH_LOOP")
	} else {
		go func() {
			refresh := func() {
				if err := toolingH.RefreshDomainHealthCache(ctx); err != nil {
					log.Printf("[domain-health] refresh error: %v", err)
				}
			}
			refresh()
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refresh()
				}
			}
		}()
	}

	// ==================== MX 自动验证轮询 ====================
	if disableMXVerifierLoop {
		log.Println("! Go MX verifier disabled by DISABLE_MX_VERIFIER_LOOP")
	} else {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			log.Println("✓ MX domain verifier started (pending check=30s, active re-check=6h)")
			reCheckTicker := time.NewTicker(6 * time.Hour)
			defer reCheckTicker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					donations, err := db.ListDueDonations(ctx)
					if err != nil {
						log.Printf("[donation-verifier] list due error: %v", err)
					} else {
						serverIP := effectiveSMTPServerIP(ctx)
						serverHostname := strings.TrimSpace(cfg.SMTPHostname)
						if value, settingErr := db.GetSetting(ctx, "smtp_hostname"); settingErr == nil && strings.TrimSpace(value) != "" {
							serverHostname = strings.TrimSpace(value)
						}
						for i := range donations {
							donation := &donations[i]
							result := store.CheckDonationRecords(donation.Domain, serverIP, serverHostname, db.DonationTXTValue(donation))
							updated, verifyErr := db.ApplyDonationVerification(ctx, donation.ID, result)
							if verifyErr != nil {
								log.Printf("[donation-verifier] %s: %v", donation.Domain, verifyErr)
							} else if !donation.RewardActive && updated.RewardActive {
								notificationDispatcher.PublishDomain("donation.reward_granted", donation.Domain, result.Status)
							} else if donation.RewardActive && !updated.RewardActive {
								notificationDispatcher.PublishDomain("donation.reward_revoked", donation.Domain, result.Status)
							}
						}
					}

					// 处理待验证域名
					pendingDomains, err := db.ListPendingDomains(ctx)
					if err != nil {
						log.Printf("[mx-verifier] list pending error: %v", err)
						continue
					}
					if len(pendingDomains) == 0 {
						continue
					}
					serverIP := effectiveSMTPServerIP(ctx)
					for _, d := range pendingDomains {
						if d.SourceType == "donated" {
							continue
						}
						matched, _, mxStatus := store.CheckDomainMX(d.Domain, serverIP)
						db.TouchDomainCheckTime(ctx, d.ID)
						if matched {
							if err := db.PromoteDomainToActive(ctx, d.ID); err != nil {
								log.Printf("[mx-verifier] promote %s error: %v", d.Domain, err)
							} else {
								log.Printf("[mx-verifier] ✓ %s MX verified, domain activated", d.Domain)
							}
						} else {
							log.Printf("[mx-verifier] waiting: %s — %s", d.Domain, mxStatus)
						}
					}

				case <-reCheckTicker.C:
					// 每 6 小时重新检测所有已激活域名，MX 失效则自动停用
					activeDomains, err := db.GetActiveDomains(ctx)
					if err != nil {
						log.Printf("[mx-recheck] list active error: %v", err)
						continue
					}
					serverIP := effectiveSMTPServerIP(ctx)
					log.Printf("[mx-recheck] checking %d active domains", len(activeDomains))
					for _, d := range activeDomains {
						if d.SourceType == "donated" {
							continue
						}
						matched, _, mxStatus := store.CheckDomainMX(d.Domain, serverIP)
						db.TouchDomainCheckTime(ctx, d.ID)
						if !matched {
							if err := db.DisableDomainMX(ctx, d.ID); err != nil {
								log.Printf("[mx-recheck] disable %s error: %v", d.Domain, err)
							} else {
								log.Printf("[mx-recheck] ⚠ %s MX no longer valid (%s), domain disabled", d.Domain, mxStatus)
								notificationDispatcher.PublishDomain("domain.disabled", d.Domain, mxStatus)
							}
						}
					}
				}
			}
		}()
	}

	// ==================== 写出后台登录密钥文件 ====================
	go func() {
		// 等待 DB 就绪后再读取（延迟 1 秒），并在关闭时及时取消。
		timer := time.NewTimer(1 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		adminKey, err := db.GetAdminAPIKey(ctx)
		if err != nil {
			log.Printf("[adminkey] could not fetch admin key: %v", err)
			return
		}
		keyFile := os.Getenv("ADMIN_KEY_FILE")
		if keyFile == "" {
			keyFile = "/data/admin.key"
		}
		if err := os.MkdirAll(filepath.Dir(keyFile), 0700); err == nil {
			content := "# FAR Mail Admin Console Auth Key\n# Format: sk-<custom>-<16 or 32 hex>; default prefix: sk-mail-.\n# Send only through X-Admin-Key to /console/v1. API tokens are stored and authenticated separately for /api/v1.\n\nADMIN_AUTH_KEY=" + adminKey + "\n"
			if err := os.WriteFile(keyFile, []byte(content), 0600); err != nil {
				log.Printf("[adminkey] write file error: %v", err)
			} else {
				log.Printf("✓ Admin console auth key written to %s", keyFile)
			}
		}
		prefix := adminKey
		if index := strings.LastIndex(prefix, "-"); index > 0 {
			prefix = prefix[:index+1]
		}
		log.Printf("✓ Admin console auth key loaded (prefix=%s length=%d)", prefix, len(adminKey))
	}()

	// ==================== 启动服务 ====================
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("✓ API server listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// 优雅关闭
	<-ctx.Done()
	log.Println("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopNotifications()
	if err := lmtpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("LMTP ingress shutdown error: %v", err)
	}
	toolingH.SetLMTPStatus(false, "")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}
