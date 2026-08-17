package model

import (
	"time"

	"github.com/google/uuid"
)

type Account struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	APIKey    string    `json:"-"`
	IsAdmin   bool      `json:"is_admin"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AccountToken struct {
	ID                 uuid.UUID  `json:"id"`
	AccountID          uuid.UUID  `json:"account_id"`
	Name               string     `json:"name"`
	TokenPrefix        string     `json:"token_prefix"`
	Scope              string     `json:"scope"`
	IsPrimary          bool       `json:"is_primary"`
	TokenKind          string     `json:"token_kind"`
	RateLimitPerMinute int        `json:"rate_limit_per_minute"`
	DailyRequestLimit  int        `json:"daily_request_limit"`
	TotalRequestLimit  int64      `json:"total_request_limit"`
	RequestCountTotal  int64      `json:"request_count_total"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type DomainDonation struct {
	ID                uuid.UUID  `json:"id"`
	DomainID          int        `json:"domain_id"`
	Domain            string     `json:"domain"`
	TokenID           uuid.UUID  `json:"token_id"`
	TokenPrefix       string     `json:"token_prefix"`
	IncludeSubdomains bool       `json:"include_subdomains"`
	ChallengeToken    string     `json:"-"`
	Status            string     `json:"status"`
	RewardActive      bool       `json:"reward_active"`
	RewardRPM         int        `json:"reward_rate_limit_per_minute"`
	RewardDaily       int        `json:"reward_daily_request_limit"`
	RewardTotal       int64      `json:"reward_total_request_limit"`
	FailureCount      int        `json:"failure_count"`
	LastError         string     `json:"last_error"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	ActivatedAt       *time.Time `json:"activated_at,omitempty"`
	RewardRevokedAt   *time.Time `json:"reward_revoked_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	EffectiveRPM      int        `json:"effective_rate_limit_per_minute"`
	EffectiveDaily    int        `json:"effective_daily_request_limit"`
	EffectiveTotal    int64      `json:"effective_total_request_limit"`
	RequestCountTotal int64      `json:"request_count_total"`
}

type DonationSummary struct {
	TotalDonations    int64 `json:"total_donations"`
	ActiveDonations   int64 `json:"active_donations"`
	PendingDonations  int64 `json:"pending_donations"`
	InactiveDonations int64 `json:"inactive_donations"`
	RewardTokenTotal  int64 `json:"reward_token_total"`
	EffectiveQuota    int64 `json:"effective_total_quota"`
	ConsumedQuota     int64 `json:"consumed_total_quota"`
}

type DonationRewardToken struct {
	ID                  uuid.UUID  `json:"id"`
	TokenPrefix         string     `json:"token_prefix"`
	DomainCount         int64      `json:"domain_count"`
	ActiveDomainCount   int64      `json:"active_domain_count"`
	PendingDomainCount  int64      `json:"pending_domain_count"`
	InactiveDomainCount int64      `json:"inactive_domain_count"`
	RateLimitPerMinute  int        `json:"rate_limit_per_minute"`
	DailyRequestLimit   int        `json:"daily_request_limit"`
	TotalRequestLimit   int64      `json:"total_request_limit"`
	RequestCountTotal   int64      `json:"request_count_total"`
	RemainingTotal      int64      `json:"remaining_total"`
	Status              string     `json:"status"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type DonationRewardEvent struct {
	ID          int64      `json:"id"`
	TokenID     uuid.UUID  `json:"token_id"`
	TokenPrefix string     `json:"token_prefix"`
	DonationID  *uuid.UUID `json:"donation_id,omitempty"`
	Domain      string     `json:"domain,omitempty"`
	EventType   string     `json:"event_type"`
	TotalDelta  int64      `json:"total_delta"`
	DailyDelta  int        `json:"daily_delta"`
	RPMDelta    int        `json:"rpm_delta"`
	Note        string     `json:"note"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Domain struct {
	ID          int        `json:"id"`
	Domain      string     `json:"domain"`
	IsActive    bool       `json:"is_active"`
	Status      string     `json:"status"`
	Visibility  string     `json:"visibility"`
	SourceType  string     `json:"source_type"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	MxCheckedAt *time.Time `json:"mx_checked_at,omitempty"`
}

type Mailbox struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"account_id"`
	Address     string     `json:"address"`
	DomainID    int        `json:"domain_id"`
	FullAddress string     `json:"full_address"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	KeepForever bool       `json:"keep_forever"`
}

type MailboxState struct {
	MailboxID        uuid.UUID  `json:"mailbox_id"`
	AccountID        uuid.UUID  `json:"account_id"`
	DomainID         int        `json:"domain_id"`
	DomainName       string     `json:"domain_name"`
	FullAddress      string     `json:"full_address"`
	LatestEmailID    *uuid.UUID `json:"latest_email_id,omitempty"`
	LatestSender     string     `json:"latest_sender"`
	LatestSubject    string     `json:"latest_subject"`
	LatestCode       string     `json:"latest_code"`
	LatestCodeSource string     `json:"latest_code_source"`
	LatestLink       string     `json:"latest_link"`
	LatestLinkSource string     `json:"latest_link_source"`
	LatestReceivedAt *time.Time `json:"latest_received_at,omitempty"`
	EmailCount       int64      `json:"email_count"`
	ExpiresAt        *time.Time `json:"expires_at"`
	KeepForever      bool       `json:"keep_forever"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Email struct {
	ID                uuid.UUID  `json:"id"`
	MailboxID         uuid.UUID  `json:"mailbox_id"`
	Sender            string     `json:"sender"`
	Subject           string     `json:"subject"`
	BodyText          string     `json:"body_text"`
	BodyHTML          string     `json:"body_html"`
	RawMessage        string     `json:"raw_message,omitempty"`
	MessageID         string     `json:"message_id,omitempty"`
	HeadersJSON       string     `json:"headers_json,omitempty"`
	HasAttachments    bool       `json:"has_attachments"`
	RawPath           string     `json:"raw_path,omitempty"`
	RawRetentionUntil *time.Time `json:"raw_retention_until,omitempty"`
	ProcessedAt       *time.Time `json:"processed_at,omitempty"`
	ParsedCode        string     `json:"parsed_code,omitempty"`
	ParsedCodeSource  string     `json:"parsed_code_source,omitempty"`
	ParsedLink        string     `json:"parsed_link,omitempty"`
	ParsedLinkSource  string     `json:"parsed_link_source,omitempty"`
	SizeBytes         int        `json:"size_bytes"`
	ReceivedAt        time.Time  `json:"received_at"`
}

type EmailSummary struct {
	ID               uuid.UUID `json:"id"`
	Sender           string    `json:"sender"`
	Subject          string    `json:"subject"`
	HasAttachments   bool      `json:"has_attachments"`
	ParsedCode       string    `json:"parsed_code,omitempty"`
	ParsedCodeSource string    `json:"parsed_code_source,omitempty"`
	ParsedLink       string    `json:"parsed_link,omitempty"`
	ParsedLinkSource string    `json:"parsed_link_source,omitempty"`
	SizeBytes        int       `json:"size_bytes"`
	ReceivedAt       time.Time `json:"received_at"`
}

type MailboxEmailEvent struct {
	MailboxID   uuid.UUID    `json:"mailbox_id"`
	FullAddress string       `json:"full_address"`
	Email       EmailSummary `json:"email"`
}

type RecentCodeActivity struct {
	MailboxID     uuid.UUID `json:"mailbox_id"`
	EmailID       uuid.UUID `json:"email_id"`
	FullAddress   string    `json:"full_address"`
	Sender        string    `json:"sender"`
	Subject       string    `json:"subject"`
	BodyText      string    `json:"body_text"`
	BodyHTML      string    `json:"body_html"`
	ReceivedAt    time.Time `json:"received_at"`
	ExtractedCode string    `json:"extracted_code,omitempty"`
	MatchedBy     string    `json:"matched_by,omitempty"`
}

type DomainHealth struct {
	Domain           string     `json:"domain"`
	IsActive         bool       `json:"is_active"`
	Status           string     `json:"status"`
	MxCheckedAt      *time.Time `json:"mx_checked_at,omitempty"`
	RootMXOK         bool       `json:"root_mx_ok"`
	WildcardMXOK     bool       `json:"wildcard_mx_ok"`
	RootMXStatus     string     `json:"root_mx_status"`
	WildcardMXStatus string     `json:"wildcard_mx_status"`
	RootMXHosts      []string   `json:"root_mx_hosts"`
	WildcardMXHosts  []string   `json:"wildcard_mx_hosts"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
}

type SystemSummary struct {
	DBOK                 bool       `json:"db_ok"`
	RedisOK              bool       `json:"redis_ok"`
	SMTPHostname         string     `json:"smtp_hostname"`
	SMTPServerIP         string     `json:"smtp_server_ip"`
	SMTPConfigured       bool       `json:"smtp_configured"`
	SMTPReachable        bool       `json:"smtp_reachable"`
	SMTPSource           string     `json:"smtp_source"`
	LMTPRunning          bool       `json:"lmtp_running"`
	LMTPAddr             string     `json:"lmtp_addr,omitempty"`
	MailboxTotal         int64      `json:"mailbox_total"`
	EmailTotal           int64      `json:"email_total"`
	ActiveDomainCount    int        `json:"active_domain_count"`
	HealthyRootDomains   int        `json:"healthy_root_domains"`
	HealthyWildcardCount int        `json:"healthy_wildcard_domains"`
	UnhealthyDomainCount int        `json:"unhealthy_domain_count"`
	LastHealthCheckAt    *time.Time `json:"last_health_check_at,omitempty"`
}

// AnalyticsSummary is the owner-facing operational aggregate used by the
// statistics and operations consoles. It intentionally contains counts only;
// raw message contents never leave the existing mail APIs.
type AnalyticsSummary struct {
	MailboxTotal          int64 `json:"mailbox_total"`
	PermanentMailboxTotal int64 `json:"permanent_mailbox_total"`
	EmailTotal            int64 `json:"email_total"`
	EmailLast24Hours      int64 `json:"email_last_24h"`
	EmailLast7Days        int64 `json:"email_last_7d"`
	CodeEmailTotal        int64 `json:"code_email_total"`
	LinkEmailTotal        int64 `json:"link_email_total"`
	StorageBytes          int64 `json:"storage_bytes"`
	RawFileReferences     int64 `json:"raw_file_references"`
	DomainTotal           int64 `json:"domain_total"`
	ActiveDomainTotal     int64 `json:"active_domain_total"`
	PendingDomainTotal    int64 `json:"pending_domain_total"`
	ActiveTokenTotal      int64 `json:"active_token_total"`
	TokenCallsToday       int64 `json:"token_calls_today"`
}

type AnalyticsDay struct {
	Day       string `json:"day"`
	Mailboxes int64  `json:"mailboxes"`
	Emails    int64  `json:"emails"`
	Codes     int64  `json:"codes"`
}
