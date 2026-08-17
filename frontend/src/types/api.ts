export interface Account {
  id: string
  username: string
  is_admin: boolean
  is_active: boolean
  created_at: string
}

export interface SessionToken {
  id: string
  name: string
  token_prefix: string
  scope: string
  is_primary: boolean
  auth_mode?: string
  expires_at?: string | null
  rate_limit_per_minute: number
  daily_request_limit: number
  total_request_limit: number
  request_count_total: number
  remaining_today: number
  remaining_total: number
}

export interface SessionResponse {
  auth_mode: string
  credential_kind?: string
  account: Account
  token: SessionToken
}

export interface PublicSettings {
  site_title?: string
  site_logo_url?: string
  smtp_server_ip?: string
  smtp_hostname?: string
  inbox_refresh_seconds?: string | number
  announcement?: string
  [key: string]: string | number | boolean | undefined
}

export interface Domain {
  id: number
  domain: string
  is_active: boolean
  status: 'active' | 'pending' | 'disabled' | string
  visibility?: string
  source_type?: string
  verified_at?: string | null
  mx_checked_at?: string | null
  created_at: string
}

export interface DomainHealth {
  domain: string
  is_active?: boolean
  status?: string
  root_mx_ok: boolean
  wildcard_mx_ok: boolean
  root_mx_status?: string
  wildcard_mx_status?: string
  checked_at?: string
}

export interface Mailbox {
  id: string
  account_id?: string
  address?: string
  domain_id: number
  full_address: string
  created_at: string
  expires_at?: string | null
  keep_forever: boolean
  email_count?: number
  latest_code?: string
  latest_link?: string
  latest_received_at?: string | null
}

export interface EmailSummary {
  id: string
  sender: string
  subject: string
  has_attachments?: boolean
  parsed_code?: string
  parsed_code_source?: string
  parsed_link?: string
  parsed_link_source?: string
  size_bytes?: number
  received_at: string
}

export interface MailboxEmailEvent {
  mailbox_id: string
  full_address: string
  email: EmailSummary
}

export interface EmailMessage extends EmailSummary {
  mailbox_id: string
  body_text?: string
  body_html?: string
  raw_message?: string
  parsed_code?: string
  parsed_link?: string
}

export interface AccountToken {
  id: string
  name: string
  token_prefix: string
  scope: string
  is_primary: boolean
  token_kind?: 'standard' | 'donation' | string
  is_current?: boolean
  rate_limit_per_minute: number
  daily_request_limit: number
  total_request_limit: number
  request_count_total: number
  remaining_total: number
  last_used_at?: string | null
  expires_at?: string | null
  revoked_at?: string | null
  status: 'active' | 'disabled' | 'expired' | string
  created_at: string
}

export interface SystemSummary {
  db_ok: boolean
  redis_ok: boolean
  smtp_hostname?: string
  smtp_server_ip?: string
  smtp_configured?: boolean
  smtp_reachable?: boolean
  smtp_source?: string
  lmtp_running?: boolean
  lmtp_addr?: string
  mailbox_total?: number
  email_total?: number
  active_domain_count: number
  healthy_root_domains: number
  healthy_wildcard_domains: number
  unhealthy_domain_count: number
  last_health_check_at?: string | null
}

export interface IngressStats {
  addr: string
  workers: number
  queue_size: number
  queue_depth: number
  queue_high_water: number
  in_flight: number
  in_flight_high_water: number
  active_workers: number
  active_connections: number
  connections_accepted: number
  jobs_submitted: number
  jobs_delivered: number
  jobs_temp_failed: number
  jobs_perm_failed: number
  queue_full: number
  oversized_messages: number
  delivery_timeouts: number
  jobs_cancelled: number
  data_bytes: number
  avg_parse_ms: number
  avg_db_ms: number
  body_max_bytes: number
  store_body_max_bytes: number
  snapshot_domains: number
  snapshot_loaded_at?: string
}

export interface RuntimeObservability {
  postgres_pool: {
    total_conns: number
    acquired_conns: number
    idle_conns: number
    constructing_conns: number
    max_conns: number
    acquire_count: number
    canceled_acquire_count: number
    empty_acquire_count: number
    acquire_duration_ms: number
  }
  redis_pool: {
    hits: number
    misses: number
    timeouts: number
    total_conns: number
    idle_conns: number
    stale_conns: number
  }
  cache: {
    token_hits: number
    token_misses: number
    active_domain_hits: number
    active_domain_misses: number
  }
  api_observability: {
    queue_depth: number
    queue_capacity: number
    pending_depth: number
    queue_high_water: number
    enqueued: number
    dropped: number
    flushes: number
    flushed_events: number
    flush_errors: number
    failed_events: number
    last_flush_unix_sec?: number
  }
  lmtp: IngressStats
}

export interface AnalyticsSummary {
  mailbox_total: number
  permanent_mailbox_total: number
  email_total: number
  email_last_24h: number
  email_last_7d: number
  code_email_total: number
  link_email_total: number
  storage_bytes: number
  raw_file_references: number
  domain_total: number
  active_domain_total: number
  pending_domain_total: number
  active_token_total: number
  token_calls_today: number
}

export interface AnalyticsDay {
  day: string
  mailboxes: number
  emails: number
  codes: number
}

export interface RecentCode {
  mailbox_id?: string
  full_address: string
  sender?: string
  subject?: string
  extracted_code?: string
  matched_by?: string
  received_at?: string
}

export interface PageResponse<T> {
  data: T[]
  total: number
  page: number
  size: number
}

export interface LookupResult {
  mailbox?: Mailbox
  email_id?: string
  sender?: string
  subject?: string
  received_at?: string
  code?: string
  link?: string
  has_code?: boolean
  has_link?: boolean
  matched_by?: string
}

export interface DnsRecord {
  type: string
  host: string
  value: string
  priority?: number
  description?: string
}

export interface DNSPlanRecord {
  type: string
  name: string
  content: string
  priority?: number
  proxied: boolean
}

export interface DNSPlanAction {
  record: DNSPlanRecord
  status: 'required' | 'create' | 'created' | 'update' | 'unchanged' | 'conflict' | 'rolled_back' | string
  detail?: string
}

export interface DNSPlan {
  domain: string
  zone_id?: string
  zone?: string
  items: DNSPlanAction[]
  rolled_back?: boolean
  rollback_error?: string
}

export interface IntegrationAuditEvent {
  id: number
  integration: string
  action: string
  domain: string
  status: string
  detail?: string
  created_at: string
}

export interface NotificationIntegrationStatus {
  generic: { enabled: boolean; configured: boolean; target?: string; signed: boolean }
  telegram: { enabled: boolean; configured: boolean; chat_id?: string }
  discord: { enabled: boolean; configured: boolean; target?: string }
  delivery: { last_attempt_at?: string; last_success_at?: string; last_channel?: string; last_error?: string; queued: number; dropped: number }
}

export interface CloudflareIntegrationStatus {
  configured: boolean
  source: 'environment' | 'file' | string
}

export interface DomainSubmitResponse {
  domain: Domain
  donation?: DomainDonation
  donation_id?: string
  claim_secret?: string
  access_token?: string
  token_prefix?: string
  status: string
  message?: string
  mx_status?: string
  dns_required?: DnsRecord[]
  smtp_hostname?: string
  server_ip?: string
  enable_subdomains?: boolean
}

export interface DomainStatusResponse {
  id: string
  domain: string
  status: string
  is_active: boolean
  mx_checked_at?: string | null
  donation?: DomainDonation
  dns_required?: DnsRecord[]
}

export interface DomainDonation {
  id: string
  domain_id: number
  domain: string
  token_id: string
  token_prefix: string
  include_subdomains: boolean
  status: 'pending' | 'active' | 'inactive' | 'revoked' | string
  reward_active: boolean
  reward_rate_limit_per_minute: number
  reward_daily_request_limit: number
  reward_total_request_limit: number
  failure_count: number
  last_error?: string
  last_checked_at?: string | null
  activated_at?: string | null
  reward_revoked_at?: string | null
  created_at: string
  updated_at: string
  effective_rate_limit_per_minute: number
  effective_daily_request_limit: number
  effective_total_request_limit: number
  request_count_total: number
}

export interface DonationSummary {
  total_donations: number
  active_donations: number
  pending_donations: number
  inactive_donations: number
  reward_token_total: number
  effective_total_quota: number
  consumed_total_quota: number
}

export interface DonationListResponse {
  data: DomainDonation[]
  summary: DonationSummary
  tokens: DonationRewardToken[]
  events: DonationRewardEvent[]
}

export interface DonationRewardToken {
  id: string
  token_prefix: string
  domain_count: number
  active_domain_count: number
  pending_domain_count: number
  inactive_domain_count: number
  rate_limit_per_minute: number
  daily_request_limit: number
  total_request_limit: number
  request_count_total: number
  remaining_total: number
  status: 'active' | 'inactive' | 'revoked' | 'expired' | string
  last_used_at?: string | null
  created_at: string
}

export interface DonationRewardEvent {
  id: number
  token_id: string
  token_prefix: string
  donation_id?: string
  domain?: string
  event_type: 'grant' | 'revoke' | 'manual_adjust' | 'policy_update' | string
  total_delta: number
  daily_delta: number
  rpm_delta: number
  note?: string
  created_at: string
}

export interface APIUsageSummary {
  total_requests: number
  error_requests: number
  avg_latency_ms: number
  p95_latency_ms: number
}

export interface APIUsageBucket {
  hour: string
  total_requests: number
  error_requests: number
  avg_latency_ms: number
}

export interface APIUsageRoute {
  method: string
  route: string
  total_requests: number
  error_requests: number
  avg_latency_ms: number
}

export interface APIUsageError {
  created_at: string
  token_name: string
  method: string
  route: string
  status_code: number
  latency_ms: number
  request_id: string
}

export interface APIUsageReport {
  hours: number
  summary: APIUsageSummary
  buckets: APIUsageBucket[]
  routes: APIUsageRoute[]
  recent_errors: APIUsageError[]
}

export interface MaintenancePreview {
  expired_mailboxes: number
  empty_mailboxes: number
  old_emails: number
  old_email_bytes: number
  older_than_minutes: number
}

export interface CleanupPreview {
  kind: 'mailboxes' | 'emails'
  matching_mailboxes: number
  matching_emails: number
  matching_bytes: number
}
