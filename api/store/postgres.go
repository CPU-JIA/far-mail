package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"html"
	"math/big"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"farmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

type Store struct {
	pool                   *pgxpool.Pool
	tokenAuthMu            sync.RWMutex
	tokenAuthCache         map[string]tokenAuthCacheEntry
	tokenAuthEpoch         atomic.Uint64
	tokenAuthLoadGroup     singleflight.Group
	activeDomainsMu        sync.RWMutex
	activeDomainsCache     activeDomainsCacheEntry
	activeDomainsEpoch     atomic.Uint64
	activeDomainsLoadGroup singleflight.Group
	tokenCacheHits         atomic.Uint64
	tokenCacheMisses       atomic.Uint64
	domainCacheHits        atomic.Uint64
	domainCacheMisses      atomic.Uint64
}

var (
	storeHTMLTagRe     = regexp.MustCompile(`<[^>]+>`)
	storeStyleBlockRe  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	storeScriptBlockRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	storeLinkRe        = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

var storeCodeHints = [...]string{
	"TEMPORARY VERIFICATION CODE",
	"VERIFICATION CODE",
	"VERIFY CODE",
	"验证码",
	"校验码",
	"动态码",
	"PASSCODE",
	"ONE-TIME",
	"ONE TIME",
	"OTP",
	"CODE",
}

// New 创建带连接池的 Store。PgBouncer 负责高并发复用，本地池保持小而有界。
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	// Owner-console traffic and LMTP workers share this pool; PgBouncer remains
	// the concurrency boundary, while a small local pool reduces idle sockets.
	cfg.MaxConns = 32
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 45 * time.Second
	cfg.HealthCheckPeriod = 15 * time.Second

	// PgBouncer transaction 模式不支持 named prepared statements。
	// pgx v5 默认使用 QueryExecModeCacheStatement（会发送 Parse/Bind/Execute），
	// 多个连接复用同一个后端连接时会触发 "prepared statement already in use"。
	// 改为 SimpleProtocol：直接发送明文 SQL，完全绕过服务端 prepared statement 机制。
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	s := &Store{pool: pool, tokenAuthCache: make(map[string]tokenAuthCacheEntry)}
	if err := s.ensureAuxSchema(ctx); err != nil {
		return nil, fmt.Errorf("ensure aux schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// GetAdminAPIKey 获取第一个管理员账号的后台鉴权 Key（用于写入 admin.key 文件）
func (s *Store) GetAdminAPIKey(ctx context.Context) (string, error) {
	var apiKey string
	err := s.pool.QueryRow(ctx,
		`SELECT api_key FROM accounts WHERE is_admin = TRUE ORDER BY created_at LIMIT 1`,
	).Scan(&apiKey)
	return apiKey, err
}

// ==================== Domain ====================

func (s *Store) AddDomain(ctx context.Context, domain string) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`INSERT INTO domains (domain, is_active, status, visibility, source_type, verified_at) VALUES ($1, TRUE, 'active', 'public', 'manual', NOW())
		 RETURNING id, domain, is_active, status, visibility, source_type, verified_at, created_at, mx_checked_at`,
		strings.ToLower(domain),
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.Visibility, &d.SourceType, &d.VerifiedAt, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	s.invalidateActiveDomainsCache()
	return &d, nil
}

// AddDomainPending 添加待验证域名（后台轮询 MX 记录）
func (s *Store) AddDomainPending(ctx context.Context, domain string) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`INSERT INTO domains (domain, is_active, status) VALUES ($1, FALSE, 'pending')
		 ON CONFLICT (domain) DO UPDATE
		   SET status = CASE WHEN domains.status = 'active' THEN 'active' ELSE 'pending' END,
		       is_active = CASE WHEN domains.status = 'active' THEN TRUE ELSE FALSE END
		 RETURNING id, domain, is_active, status, visibility, source_type, verified_at, created_at, mx_checked_at`,
		strings.ToLower(domain),
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.Visibility, &d.SourceType, &d.VerifiedAt, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	s.invalidateActiveDomainsCache()
	return &d, nil
}

func (s *Store) ListDomains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, domain, is_active, status, visibility, source_type, verified_at, created_at, mx_checked_at FROM domains ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Domain])
}

func (s *Store) GetActiveDomains(ctx context.Context) ([]model.Domain, error) {
	if domains, ok := s.getCachedActiveDomains(); ok {
		return domains, nil
	}
	for {
		value, err, _ := s.activeDomainsLoadGroup.Do("active", func() (any, error) {
			if domains, ok := s.getCachedActiveDomains(); ok {
				return activeDomainsLoadResult{domains: domains, epoch: s.activeDomainsEpoch.Load()}, nil
			}
			epoch := s.activeDomainsEpoch.Load()
			domains, err := s.loadActiveDomains(ctx)
			if err != nil {
				return nil, err
			}
			result := activeDomainsLoadResult{domains: domains, epoch: epoch}
			if s.activeDomainsEpoch.Load() == epoch {
				s.cacheActiveDomains(domains)
			}
			return result, nil
		})
		if err != nil {
			return nil, err
		}
		result := value.(activeDomainsLoadResult)
		if result.epoch != s.activeDomainsEpoch.Load() {
			s.activeDomainsLoadGroup.Forget("active")
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		}
		return cloneDomains(result.domains), nil
	}
}

func (s *Store) loadActiveDomains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, domain, is_active, status, visibility, source_type, verified_at, created_at, mx_checked_at FROM domains WHERE is_active = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Domain])
}

func (s *Store) GetRandomActiveDomain(ctx context.Context) (*model.Domain, error) {
	domains, err := s.GetActiveDomains(ctx)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, pgx.ErrNoRows
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(domains))))
	if err != nil {
		return nil, err
	}
	domain := cloneDomain(domains[index.Int64()])
	return &domain, nil
}

// GetDomainByName 按域名字符串查找活跃域名，供创建邮箱时指定域名使用
func (s *Store) GetDomainByName(ctx context.Context, domain string) (*model.Domain, error) {
	normalized := strings.ToLower(strings.TrimSpace(domain))
	domains, err := s.GetActiveDomains(ctx)
	if err != nil {
		return nil, err
	}
	for index := range domains {
		if strings.EqualFold(domains[index].Domain, normalized) {
			domain := cloneDomain(domains[index])
			return &domain, nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (s *Store) ResolveActiveMailboxDomain(ctx context.Context, requested string) (*model.Domain, string, error) {
	normalized := strings.ToLower(strings.TrimSpace(requested))
	normalized = strings.Trim(normalized, ".")
	if normalized == "" {
		return nil, "", pgx.ErrNoRows
	}

	if exact, err := s.GetDomainByName(ctx, normalized); err == nil {
		return exact, exact.Domain, nil
	}

	activeDomains, err := s.GetActiveDomains(ctx)
	if err != nil {
		return nil, "", err
	}

	var best *model.Domain
	for i := range activeDomains {
		root := strings.ToLower(strings.TrimSpace(activeDomains[i].Domain))
		if root == "" {
			continue
		}
		if normalized == root || strings.HasSuffix(normalized, "."+root) {
			if best == nil || len(root) > len(best.Domain) {
				copyDomain := activeDomains[i]
				best = &copyDomain
			}
		}
	}
	if best == nil {
		return nil, "", pgx.ErrNoRows
	}
	return best, normalized, nil
}

func (s *Store) GetDomainByID(ctx context.Context, domainID int) (*model.Domain, error) {
	var d model.Domain
	err := s.pool.QueryRow(ctx,
		`SELECT id, domain, is_active, status, visibility, source_type, verified_at, created_at, mx_checked_at FROM domains WHERE id = $1`,
		domainID,
	).Scan(&d.ID, &d.Domain, &d.IsActive, &d.Status, &d.Visibility, &d.SourceType, &d.VerifiedAt, &d.CreatedAt, &d.MxCheckedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListPendingDomains 返回所有待验证域名
func (s *Store) ListPendingDomains(ctx context.Context) ([]model.Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, domain, is_active, status, visibility, source_type, verified_at, created_at, mx_checked_at
		 FROM domains WHERE status = 'pending'
		 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.Domain])
}

// PromoteDomainToActive 验证通过，激活域名
func (s *Store) PromoteDomainToActive(ctx context.Context, domainID int) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET is_active = TRUE, status = 'active', verified_at = COALESCE(verified_at, NOW()), mx_checked_at = $1 WHERE id = $2`,
		now, domainID)
	if err == nil {
		s.invalidateActiveDomainsCache()
	}
	return err
}

// TouchDomainCheckTime 更新最后检测时间
func (s *Store) TouchDomainCheckTime(ctx context.Context, domainID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET mx_checked_at = NOW() WHERE id = $1`, domainID)
	if err == nil {
		s.invalidateActiveDomainsCache()
	}
	return err
}

// DisableDomainMX MX检测失败，自动停用域名
func (s *Store) DisableDomainMX(ctx context.Context, domainID int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET is_active = FALSE, status = 'disabled', mx_checked_at = NOW() WHERE id = $1`,
		domainID)
	if err == nil {
		s.invalidateActiveDomainsCache()
	}
	return err
}

func (s *Store) DeleteDomain(ctx context.Context, domainID int) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)
	if err == nil {
		s.invalidateActiveDomainsCache()
	}
	return err
}

func (s *Store) ToggleDomain(ctx context.Context, domainID int, active bool) error {
	status := "disabled"
	if active {
		status = "active"
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET is_active = $1, status = $2 WHERE id = $3`, active, status, domainID)
	if err == nil {
		s.invalidateActiveDomainsCache()
	}
	return err
}

// ==================== Mailbox ====================

func (s *Store) CreateMailbox(ctx context.Context, accountID uuid.UUID, creatorTokenID *uuid.UUID, address string, domainID int, fullAddress string, ttlMinutes int) (*model.Mailbox, error) {
	var expiresAt *time.Time
	if ttlMinutes > 0 {
		t := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
		expiresAt = &t
	}
	var m model.Mailbox
	err := s.pool.QueryRow(ctx,
		`INSERT INTO mailboxes (account_id, creator_token_id, address, domain_id, full_address, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, account_id, address, domain_id, full_address, created_at, expires_at, keep_forever`,
		accountID, creatorTokenID, address, domainID, fullAddress, expiresAt,
	).Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt, &m.KeepForever)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListMailboxes(ctx context.Context, accountID uuid.UUID, creatorTokenID *uuid.UUID, page, size int) ([]model.Mailbox, int, error) {
	where := `account_id = $1`
	args := []any{accountID}
	if creatorTokenID != nil {
		args = append(args, *creatorTokenID)
		where += fmt.Sprintf(` AND creator_token_id = $%d`, len(args))
	}
	args = append(args, size)
	limitPlaceholder := len(args)
	args = append(args, (page-1)*size)
	offsetPlaceholder := len(args)

	// Count and page share one pool acquisition and one database round trip.
	// Separate lateral branches retain index-friendly LIMIT/OFFSET behavior and
	// still return the exact total when the requested page is empty.
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT page.id, page.account_id, page.address, page.domain_id,
		       page.full_address, page.created_at, page.expires_at,
		       page.keep_forever, totals.total
		FROM LATERAL (
			SELECT COUNT(*)::bigint AS total
			FROM mailboxes
			WHERE %s
		) AS totals
		LEFT JOIN LATERAL (
			SELECT id, account_id, address, domain_id, full_address,
			       created_at, expires_at, keep_forever
			FROM mailboxes
			WHERE %s
			ORDER BY created_at DESC, id DESC
			LIMIT $%d OFFSET $%d
		) AS page ON TRUE
		ORDER BY page.created_at DESC NULLS LAST, page.id DESC NULLS LAST
	`, where, where, limitPlaceholder, offsetPlaceholder), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	mailboxes := make([]model.Mailbox, 0, size)
	var total int64
	for rows.Next() {
		var item model.Mailbox
		var id, itemAccountID *uuid.UUID
		var address, fullAddress *string
		var domainID *int
		var createdAt, expiresAt *time.Time
		var keepForever *bool
		if err := rows.Scan(
			&id, &itemAccountID, &address, &domainID,
			&fullAddress, &createdAt, &expiresAt, &keepForever, &total,
		); err != nil {
			return nil, 0, err
		}
		if id == nil {
			continue
		}
		item.ID = *id
		item.AccountID = *itemAccountID
		item.Address = *address
		item.DomainID = *domainID
		item.FullAddress = *fullAddress
		item.CreatedAt = *createdAt
		item.ExpiresAt = expiresAt
		item.KeepForever = *keepForever
		mailboxes = append(mailboxes, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return mailboxes, int(total), nil
}

func (s *Store) GetMailbox(ctx context.Context, mailboxID uuid.UUID, accountID uuid.UUID, creatorTokenID *uuid.UUID) (*model.Mailbox, error) {
	var m model.Mailbox
	err := s.pool.QueryRow(ctx,
		`SELECT id, account_id, address, domain_id, full_address, created_at, expires_at, keep_forever
		 FROM mailboxes WHERE id = $1 AND account_id = $2 AND ($3::uuid IS NULL OR creator_token_id = $3)`,
		mailboxID, accountID, creatorTokenID,
	).Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt, &m.KeepForever)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) DeleteMailbox(ctx context.Context, mailboxID uuid.UUID, accountID uuid.UUID, creatorTokenID *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM mailboxes WHERE id = $1 AND account_id = $2 AND ($3::uuid IS NULL OR creator_token_id = $3)`, mailboxID, accountID, creatorTokenID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteExpiredMailboxes 刪除已过期的邮箱（及其所有邮件）
func (s *Store) DeleteExpiredMailboxes(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM mailboxes WHERE keep_forever = FALSE AND expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) SetMailboxKeepForever(ctx context.Context, mailboxID, accountID uuid.UUID, creatorTokenID *uuid.UUID, keepForever bool, ttlMinutes int) (*model.Mailbox, error) {
	query := `
		UPDATE mailboxes
		SET keep_forever = $4,
			expires_at = CASE
				WHEN $4 THEN NULL
				WHEN $5 <= 0 THEN NULL
				ELSE NOW() + make_interval(mins => $5)
			END
		WHERE id = $1 AND account_id = $2 AND ($3::uuid IS NULL OR creator_token_id = $3)
		RETURNING id, account_id, address, domain_id, full_address, created_at, expires_at, keep_forever
	`

	var m model.Mailbox
	err := s.pool.QueryRow(ctx, query, mailboxID, accountID, creatorTokenID, keepForever, ttlMinutes).
		Scan(&m.ID, &m.AccountID, &m.Address, &m.DomainID, &m.FullAddress, &m.CreatedAt, &m.ExpiresAt, &m.KeepForever)
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE mailbox_state
		SET expires_at = $2,
		    keep_forever = $3,
		    updated_at = NOW()
		WHERE mailbox_id = $1
	`, m.ID, m.ExpiresAt, m.KeepForever)
	return &m, nil
}

func (s *Store) DeleteEmailsOlderThan(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	return s.deleteEmailsAndRefreshProjection(ctx,
		`DELETE FROM emails AS e
		 WHERE e.received_at < NOW() - $1::interval
		 RETURNING e.id, e.mailbox_id`,
		fmt.Sprintf("%.0f seconds", maxAge.Seconds()),
	)
}

func (s *Store) TrimEmailsToMaxCount(ctx context.Context, maxCount int) (int64, error) {
	if maxCount <= 0 {
		return 0, nil
	}
	return s.deleteEmailsAndRefreshProjection(ctx,
		`WITH overflow AS (
			SELECT id
			FROM emails
			ORDER BY received_at DESC, id DESC
			OFFSET $1
		)
		DELETE FROM emails AS e
		WHERE e.id IN (SELECT id FROM overflow)
		RETURNING e.id, e.mailbox_id`,
		maxCount,
	)
}

// CheckDomainMX 检测域名MX记录是否指向指定服务器IP
func CheckDomainMX(domain, serverIP string) (matched bool, mxHosts []string, status string) {
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return false, nil, fmt.Sprintf("DNS lookup failed: %v", err)
	}
	if len(mxRecords) == 0 {
		return false, nil, "no MX record found"
	}
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(mx.Host, ".")
		mxHosts = append(mxHosts, host)
		addrs, err := net.LookupHost(host)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if addr == serverIP {
				return true, mxHosts, fmt.Sprintf("MX record matched: %s -> %s", host, addr)
			}
		}
	}
	return false, mxHosts, fmt.Sprintf("MX records (%s) do not point to this server (%s)", strings.Join(mxHosts, ","), serverIP)
}

// ==================== Email ====================

func (s *Store) ListEmails(ctx context.Context, mailboxID, accountID uuid.UUID, creatorTokenID *uuid.UUID, page, size int) ([]model.EmailSummary, int, error) {
	where := `m.id = $1 AND m.account_id = $2`
	args := []any{mailboxID, accountID}
	if creatorTokenID != nil {
		args = append(args, *creatorTokenID)
		where += fmt.Sprintf(` AND m.creator_token_id = $%d`, len(args))
	}
	args = append(args, size)
	limitPlaceholder := len(args)
	args = append(args, (page-1)*size)
	offsetPlaceholder := len(args)

	// A single statement returns both the exact count and the requested page.
	// LEFT JOIN LATERAL deliberately emits one NULL page row for an empty inbox
	// or an offset past the end, preserving authorization/not-found semantics
	// without a second network round trip.
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT page.id,
		       COALESCE(page.sender, ''),
		       COALESCE(page.subject, ''),
		       COALESCE(page.has_attachments, FALSE),
		       COALESCE(page.parsed_code, ''),
		       COALESCE(page.parsed_code_source, ''),
		       COALESCE(page.parsed_link, ''),
		       COALESCE(page.parsed_link_source, ''),
		       COALESCE(page.size_bytes, 0),
		       page.received_at,
		       totals.total
		FROM mailboxes AS m
		CROSS JOIN LATERAL (
			SELECT COUNT(*)::bigint AS total
			FROM emails AS counted
			WHERE counted.mailbox_id = m.id
		) AS totals
		LEFT JOIN LATERAL (
			SELECT e.id, e.sender, e.subject, e.has_attachments,
			       e.parsed_code, e.parsed_code_source, e.parsed_link, e.parsed_link_source,
			       e.size_bytes, e.received_at
			FROM emails AS e
			WHERE e.mailbox_id = m.id
			ORDER BY e.received_at DESC, e.id DESC
			LIMIT $%d OFFSET $%d
		) AS page ON TRUE
		WHERE %s
		ORDER BY page.received_at DESC NULLS LAST, page.id DESC NULLS LAST
	`, limitPlaceholder, offsetPlaceholder, where), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	emails := make([]model.EmailSummary, 0, size)
	var total int64
	foundMailbox := false
	for rows.Next() {
		foundMailbox = true
		var item model.EmailSummary
		var emailID *uuid.UUID
		var receivedAt *time.Time
		if err := rows.Scan(&emailID, &item.Sender, &item.Subject, &item.HasAttachments,
			&item.ParsedCode, &item.ParsedCodeSource, &item.ParsedLink, &item.ParsedLinkSource,
			&item.SizeBytes, &receivedAt, &total); err != nil {
			return nil, 0, err
		}
		if emailID == nil || receivedAt == nil {
			continue
		}
		item.ID = *emailID
		item.ReceivedAt = *receivedAt
		emails = append(emails, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if !foundMailbox {
		return nil, 0, pgx.ErrNoRows
	}
	return emails, int(total), nil
}

func (s *Store) GetEmail(ctx context.Context, emailID, mailboxID, accountID uuid.UUID, creatorTokenID *uuid.UUID) (*model.Email, error) {
	var e model.Email
	err := s.pool.QueryRow(ctx,
		`SELECT id, mailbox_id, sender, subject, body_text, body_html, raw_message, message_id, headers_json::text, has_attachments, raw_path, raw_retention_until, processed_at, size_bytes, received_at
		 FROM emails e
		 JOIN mailboxes m ON m.id = e.mailbox_id
		 WHERE e.id = $1
		   AND e.mailbox_id = $2
		   AND m.account_id = $3
		   AND ($4::uuid IS NULL OR m.creator_token_id = $4)`,
		emailID, mailboxID, accountID, creatorTokenID,
	).Scan(&e.ID, &e.MailboxID, &e.Sender, &e.Subject, &e.BodyText, &e.BodyHTML, &e.RawMessage, &e.MessageID, &e.HeadersJSON, &e.HasAttachments, &e.RawPath, &e.RawRetentionUntil, &e.ProcessedAt, &e.SizeBytes, &e.ReceivedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) DeleteEmail(ctx context.Context, emailID, mailboxID, accountID uuid.UUID, creatorTokenID *uuid.UUID) error {
	query := `DELETE FROM emails AS e
		 USING mailboxes m
		 WHERE e.id = $1
		   AND e.mailbox_id = $2
		   AND m.id = e.mailbox_id
		   AND m.account_id = $3`
	args := []any{emailID, mailboxID, accountID}
	if creatorTokenID != nil {
		query += ` AND m.creator_token_id = $4`
		args = append(args, *creatorTokenID)
	}
	query += ` RETURNING e.id, e.mailbox_id`
	deleted, err := s.deleteEmailsAndRefreshProjection(ctx, query, args...)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// deleteEmailsAndRefreshProjection executes a trusted DELETE ... RETURNING
// statement and updates mailbox_state from the same command. PostgreSQL's
// data-modifying CTEs share a snapshot, so replacement explicitly excludes the
// rows returned by deleted. Only affected mailboxes touch the projection table;
// non-latest deletions keep the current projection and merely decrement count.
func (s *Store) deleteEmailsAndRefreshProjection(ctx context.Context, deleteStatement string, args ...any) (int64, error) {
	query := `
		WITH deleted AS (
			` + deleteStatement + `
		), affected AS (
			SELECT mailbox_id, COUNT(*)::bigint AS deleted_count
			FROM deleted
			GROUP BY mailbox_id
		), replacement AS (
			SELECT affected.mailbox_id,
			       latest.id, latest.sender, latest.subject,
			       latest.parsed_code, latest.parsed_code_source,
			       latest.parsed_link, latest.parsed_link_source,
			       latest.received_at
			FROM affected
			LEFT JOIN LATERAL (
				SELECT e.id, e.sender, e.subject,
				       e.parsed_code, e.parsed_code_source,
				       e.parsed_link, e.parsed_link_source, e.received_at
				FROM emails AS e
				WHERE e.mailbox_id = affected.mailbox_id
				  AND NOT EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = e.id)
				ORDER BY e.received_at DESC, e.id DESC
				LIMIT 1
			) AS latest ON TRUE
		), projection_updated AS (
			UPDATE mailbox_state AS state
			SET email_count = GREATEST(0, state.email_count - affected.deleted_count),
			    latest_email_id = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN replacement.id ELSE state.latest_email_id END,
			    latest_sender = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN COALESCE(replacement.sender, '') ELSE state.latest_sender END,
			    latest_subject = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN COALESCE(replacement.subject, '') ELSE state.latest_subject END,
			    latest_code = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN COALESCE(replacement.parsed_code, '') ELSE state.latest_code END,
			    latest_code_source = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN COALESCE(replacement.parsed_code_source, '') ELSE state.latest_code_source END,
			    latest_link = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN COALESCE(replacement.parsed_link, '') ELSE state.latest_link END,
			    latest_link_source = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN COALESCE(replacement.parsed_link_source, '') ELSE state.latest_link_source END,
			    latest_received_at = CASE
					WHEN state.latest_email_id IS NULL
					  OR EXISTS (SELECT 1 FROM deleted AS removed WHERE removed.id = state.latest_email_id)
					THEN replacement.received_at ELSE state.latest_received_at END,
			    updated_at = NOW()
			FROM affected
			JOIN replacement ON replacement.mailbox_id = affected.mailbox_id
			WHERE state.mailbox_id = affected.mailbox_id
			RETURNING state.mailbox_id
		)
		SELECT COUNT(*)::bigint FROM deleted`

	var deleted int64
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&deleted); err != nil {
		return 0, err
	}
	return deleted, nil
}

// ==================== Helpers ====================

func GenerateRandomAddress() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	length := 10
	result := make([]byte, length)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

func normalizeExtractionText(source string) string {
	text := html.UnescapeString(source)
	if strings.Contains(text, "<") {
		text = storeStyleBlockRe.ReplaceAllString(text, " ")
		text = storeScriptBlockRe.ReplaceAllString(text, " ")
		text = storeHTMLTagRe.ReplaceAllString(text, " ")
	}
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return collapseExtractionWhitespace(text)
}

func collapseExtractionWhitespace(value string) string {
	needsNormalization := false
	previousSpace := true
	for _, r := range value {
		space := unicode.IsSpace(r)
		if space && (r != ' ' || previousSpace) {
			needsNormalization = true
			break
		}
		previousSpace = space
	}
	if !needsNormalization && !previousSpace {
		return value
	}

	var builder strings.Builder
	builder.Grow(len(value))
	pendingSpace := false
	wrote := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			if wrote {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace {
			builder.WriteByte(' ')
			pendingSpace = false
		}
		builder.WriteRune(r)
		wrote = true
	}
	return builder.String()
}

func IsLikelyVerificationCode(value string) bool {
	code := strings.ToUpper(strings.TrimSpace(value))
	return isLikelyVerificationCodeUpper(code)
}

func isLikelyVerificationCodeUpper(code string) bool {
	if len(code) < 4 || len(code) > 10 {
		return false
	}
	allDigits := true
	allAlpha := true
	for _, r := range code {
		switch {
		case r >= '0' && r <= '9':
			allAlpha = false
		case r >= 'A' && r <= 'Z':
			allDigits = false
		default:
			return false
		}
	}
	if strings.Trim(code, "0") == "" {
		return false
	}
	if allDigits {
		return len(code) >= 4 && len(code) <= 8
	}
	// HTML/template words are common false positives when parsing raw HTML emails.
	if allAlpha {
		switch code {
		case "HTML", "BODY", "HEAD", "META", "STYLE", "SCRIPT", "TITLE", "TABLE", "TR", "TD", "SPAN", "DIV", "FONT", "CLASS", "WIDTH", "HEIGHT",
			"OPENAI", "TEMPORARY", "VERIFICATION", "VERIFY", "CODE", "CONTINUE", "PLEASE", "IGNORE", "ACCOUNT", "CREATE", "CREATING", "TEAM", "BEST",
			"HELLO", "WELCOME", "LOGIN", "SIGNIN", "BUTTON", "EMAIL", "MAIL", "NOREPLY", "SECURITY":
			return false
		}
	}
	return true
}

func ExtractCode(source string) (string, string) {
	flat := strings.ToUpper(normalizeExtractionText(source))
	if flat == "" {
		return "", ""
	}
	for searchAt := 0; searchAt < len(flat); {
		_, start, found := nextCodeHint(flat, searchAt)
		if !found {
			break
		}
		end := start + 260
		if end > len(flat) {
			end = len(flat)
		}
		window := flat[start:end]
		if m := firstLikelyCode(window, true); m != "" {
			return m, "keyword"
		}
		if m := firstLikelyCode(window, false); m != "" {
			return m, "keyword"
		}
		searchAt = start
	}
	if m := firstLikelyCode(flat, true); m != "" {
		return m, "digits"
	}
	if m := firstLikelyCode(flat, false); m != "" {
		return m, "token"
	}
	return "", ""
}

func nextCodeHint(source string, offset int) (int, int, bool) {
	bestStart := len(source)
	bestEnd := -1
	for _, hint := range storeCodeHints {
		index := strings.Index(source[offset:], hint)
		if index < 0 {
			continue
		}
		start := offset + index
		end := start + len(hint)
		if start < bestStart || (start == bestStart && end > bestEnd) {
			bestStart = start
			bestEnd = end
		}
	}
	if bestEnd < 0 {
		return 0, 0, false
	}
	return bestStart, bestEnd, true
}

func firstLikelyCode(source string, digitsOnly bool) string {
	for index := 0; index < len(source); {
		if !codeByteMatches(source[index], digitsOnly) {
			index++
			continue
		}
		start := index
		for index < len(source) && codeByteMatches(source[index], digitsOnly) {
			index++
		}
		if (start > 0 && isASCIIWordByte(source[start-1])) || (index < len(source) && isASCIIWordByte(source[index])) {
			continue
		}
		candidate := source[start:index]
		if digitsOnly {
			if len(candidate) < 4 || len(candidate) > 8 {
				continue
			}
		} else if len(candidate) < 6 || len(candidate) > 10 {
			continue
		}
		if isLikelyVerificationCodeUpper(candidate) {
			return candidate
		}
	}
	return ""
}

func codeByteMatches(value byte, digitsOnly bool) bool {
	if value >= '0' && value <= '9' {
		return true
	}
	return !digitsOnly && value >= 'A' && value <= 'Z'
}

func isASCIIWordByte(value byte) bool {
	return value == '_' || (value >= '0' && value <= '9') || (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func ExtractLink(source string) (string, string) {
	best := ""
	bestScore := -1 << 30
	for _, matched := range storeLinkRe.FindAllString(source, -1) {
		link := strings.TrimRight(matched, ").,!?")
		if link == "" {
			continue
		}
		lower := strings.ToLower(link)
		if hasAnySuffix(lower, []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".woff", ".woff2", ".ttf", ".eot"}) {
			continue
		}
		score := 0
		if strings.Contains(lower, "verify") ||
			strings.Contains(lower, "verification") ||
			strings.Contains(lower, "confirm") ||
			strings.Contains(lower, "activate") ||
			strings.Contains(lower, "reset") ||
			strings.Contains(lower, "magic") ||
			strings.Contains(lower, "signin") ||
			strings.Contains(lower, "login") ||
			strings.Contains(lower, "auth") ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "invite") {
			score += 10
		}
		if strings.Contains(lower, "cdn.") || strings.Contains(lower, "/static/") || strings.Contains(lower, "/assets/") {
			score -= 10
		}
		if score > bestScore {
			bestScore = score
			best = link
		}
	}
	if best == "" {
		return "", ""
	}
	return best, "http"
}

func hasAnySuffix(value string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
