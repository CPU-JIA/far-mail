import type {
  AccountToken,
  Domain,
  DomainHealth,
  DomainStatusResponse,
  DomainSubmitResponse,
  DonationListResponse,
  EmailMessage,
  EmailSummary,
  LookupResult,
  Mailbox,
  PageResponse,
  PublicSettings,
  RecentCode,
  SessionResponse,
  SystemSummary,
  IngressStats,
  APIUsageReport,
  MaintenancePreview,
  CleanupPreview,
  MailboxEmailEvent,
  DNSPlan,
  NotificationIntegrationStatus,
  CloudflareIntegrationStatus,
} from '../types/api'
import { localeState, tr } from '../stores/i18n'

export const publicAPIOrigin = String(window.__FAR_MAIL_RUNTIME_CONFIG__?.apiOrigin || '').trim().replace(/\/+$/, '')
const CONSOLE_BASE = `${publicAPIOrigin}/console/v1`
const PUBLIC_BASE = `${publicAPIOrigin}/public/v1`
const API_BASE = `${publicAPIOrigin}/api/v1`
const REQUEST_TIMEOUT_MS = 10_000
let adminKey = ''

export function publicResourceURL(value: string): string {
  const resource = String(value || '').trim()
  if (!resource.startsWith('/') || resource.startsWith('//')) return resource
  return `${publicAPIOrigin}${resource}`
}

const zhErrors: Record<string, string> = {
  'not found': '资源不存在',
  'missing admin console key': '缺少登录密钥',
  'invalid admin console key': '登录密钥无效',
  'admin console auth key required': '需要登录密钥',
  'missing API access token': '缺少 API Token',
  'invalid API access token': 'API Token 无效',
  'API access token revoked': 'API Token 已撤销',
  'API access token expired': 'API Token 已过期',
  'rate limit exceeded': '已超过 RPM 限制',
  'daily request limit exceeded': '已超过每日调用额度',
  'total request limit exceeded': '已用完总调用额度',
  'rate limiter unavailable': '限流服务暂不可用',
  'insufficient token scope': 'API Token scope 权限不足',
  'mailbox not found': '邮箱不存在',
  'no emails found': '邮箱暂无邮件',
  'domain not found': '域名不存在',
  'token not found': 'API Token 不存在',
  'invalid token id': 'API Token ID 无效',
  'at least one active owner token must remain': '至少需要保留一个可用的 Owner Token',
  'scope must be read, cleanup, or owner': 'Permission scope 必须为 read、cleanup 或 owner',
  'token limits must not be negative': 'API Token 限额不能为负数',
  'address already taken, try again': '邮箱地址已被占用，请更换后重试',
  'no active domains available': '当前没有可用域名',
  'invalid domain format': '域名格式无效',
  'enter a root domain without wildcard, @, or email address': '请输入根域名，不要带通配符、@ 或邮箱地址',
  'domain is too long': '域名过长',
  'manage this domain from the donation plan': '请在捐赠计划中管理这个域名',
  'domain already submitted or owner-managed': '该域名已经提交或由站长管理',
  'reward API Token domain limit reached': '该奖励 API Token 已达到可关联域名上限',
  'invalid reward API Token': '奖励 API Token 无效',
  'no rows in result set': '目标不存在或状态已发生变化',
  'token not found or already disabled': 'API Token 不存在或已处于暂停状态',
  'token not found or already enabled': 'API Token 不存在或已处于启用状态',
  'token name must not exceed 128 characters': 'API Token 名称不能超过 128 个字符',
  'token limits must not exceed 1000000000': 'API Token 限额不能超过 10 亿',
  'expires_in_days must not exceed 3650': '有效期不能超过 3650 天',
  'address must be 1-64 lowercase letters, numbers, dots, underscores, plus signs, or dashes': '邮箱前缀须为 1–64 位小写字母、数字、点、下划线、加号或短横线',
  'invalid request': '请求参数无效',
}

const zhStatusErrors: Record<number, string> = {
  400: '请求参数无效',
  401: '登录密钥无效或已更新，请重新登录',
  403: '当前凭据无权执行此操作',
  404: '目标不存在或已被删除',
  409: '当前操作与已有数据冲突',
  429: '请求过于频繁，请稍后重试',
  500: '服务暂时异常，请稍后重试',
  502: '上游服务暂时不可用',
  503: '服务暂时不可用，请稍后重试',
}

const enErrors: Record<string, string> = {
  '请输入根域名，不要带通配符、@ 或邮箱地址': 'Enter a root domain without a wildcard, @, or email address',
  '域名格式不正确': 'Invalid domain format',
  '域名过长': 'Domain is too long',
  '请在捐赠计划中管理这个域名': 'Manage this domain from the donation plan',
  '该域名已经提交或由站长管理': 'This domain has already been submitted or is owner-managed',
  '该奖励 API Token 已达到可关联域名上限': 'This reward API Token has reached its domain limit',
  '奖励 API Token 无效': 'Invalid reward API Token',
}

function localizedError(value: unknown, status: number): string {
  const message = String(value || `HTTP ${status}`)
  if (localeState.locale === 'en-US') return enErrors[message] || message
  if (zhErrors[message]) return zhErrors[message]
  if (/[㐀-鿿]/.test(message)) return message
  if (message.startsWith('domain not found under active root domains:')) {
    return `该域名不属于当前可用根域：${message.slice(message.lastIndexOf(':') + 1).trim()}`
  }
  return zhStatusErrors[status] || '操作失败，请稍后重试'
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
    readonly requestId?: string,
    readonly details?: Record<string, unknown>,
  ) {
    super(message)
  }
}

export function configureAdminKey(key: string): void {
  adminKey = key.trim()
}

export async function streamMailboxEvents(
  mailboxId: string,
  signal: AbortSignal,
  onEvent: (event: MailboxEmailEvent) => void,
  onOpen?: () => void,
): Promise<void> {
  const headers = new Headers({ Accept: 'text/event-stream' })
  if (adminKey) headers.set('X-Admin-Key', adminKey)
  const response = await fetch(`${CONSOLE_BASE}/mailboxes/${encodeURIComponent(mailboxId)}/events`, { headers, signal })
  if (!response.ok || !response.body) {
    throw new ApiError(tr('实时收信连接失败', 'Realtime inbox connection failed'), response.status, 'realtime_unavailable')
  }
  onOpen?.()
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  while (!signal.aborted) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, '\n')
    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      const block = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      let eventName = 'message'
      const data: string[] = []
      block.split('\n').forEach(line => {
        if (line.startsWith('event:')) eventName = line.slice(6).trim()
        else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
      })
      if (eventName === 'email' && data.length) {
        try { onEvent(JSON.parse(data.join('\n')) as MailboxEmailEvent) } catch { /* ignore malformed event */ }
      }
      boundary = buffer.indexOf('\n\n')
    }
  }
}

function query(params: Record<string, string | number | boolean | undefined>): string {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '' && value !== false && value !== 0) {
      search.set(key, String(value))
    }
  })
  const value = search.toString()
  return value ? `?${value}` : ''
}

async function request<T>(path: string, init: RequestInit = {}, authenticated = true): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  if (authenticated && adminKey) headers.set('X-Admin-Key', adminKey)

  const timeout = new AbortController()
  const timer = window.setTimeout(() => timeout.abort(), REQUEST_TIMEOUT_MS)
  const abortFromCaller = () => timeout.abort()
  if (init.signal) {
    if (init.signal.aborted) timeout.abort()
    else init.signal.addEventListener('abort', abortFromCaller, { once: true })
  }
  let response: Response
  try {
    response = await fetch(path, { ...init, headers, signal: timeout.signal })
  } catch (error) {
    if (timeout.signal.aborted) throw new ApiError(tr('请求超时或已取消', 'Request timed out or was cancelled'), 0, 'request_timeout')
    throw new ApiError(localeState.locale === 'en-US' && error instanceof Error ? error.message : tr('网络请求失败', 'Network request failed'), 0, 'network_error')
  } finally {
    window.clearTimeout(timer)
    init.signal?.removeEventListener('abort', abortFromCaller)
  }
  if (response.status === 204) return undefined as T

  const contentType = response.headers.get('content-type') || ''
  const payload = contentType.includes('application/json')
    ? await response.json().catch(() => ({}))
    : await response.text().catch(() => '')

  if (!response.ok) {
    const data = typeof payload === 'object' && payload !== null ? payload as Record<string, unknown> : {}
    throw new ApiError(
      localizedError(data.message || data.error || payload, response.status),
      response.status,
      data.error_code ? String(data.error_code) : undefined,
      data.request_id ? String(data.request_id) : response.headers.get('X-Request-ID') || undefined,
      data,
    )
  }
  return payload as T
}

export const api = {
  publicSettings: () => request<PublicSettings>(`${PUBLIC_BASE}/settings`, {}, false),
  donateDomain: (domain: string, enableSubdomains: boolean) => request<DomainSubmitResponse>(`${PUBLIC_BASE}/domains/submit`, {
    method: 'POST',
    body: JSON.stringify({ domain, enable_subdomains: enableSubdomains }),
  }, false),
  donateDomainWithToken: (domain: string, enableSubdomains: boolean, token: string) => request<DomainSubmitResponse>(`${API_BASE}/donations`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token.trim()}` },
    body: JSON.stringify({ domain, enable_subdomains: enableSubdomains }),
  }, false),
  donatedDomainStatus: (donationId: string, claimSecret: string) => request<DomainStatusResponse>(`${PUBLIC_BASE}/domains/status`, {
    method: 'POST',
    body: JSON.stringify({ donation_id: donationId, claim_secret: claimSecret }),
  }, false),
  session: () => request<SessionResponse>(`${CONSOLE_BASE}/session`),
  systemSummary: (signal?: AbortSignal) => request<SystemSummary>(`${CONSOLE_BASE}/system/summary`, { signal }),
  recentCodes: (limit = 12) => request<{ data: RecentCode[] }>(`${CONSOLE_BASE}/activity/codes${query({ limit })}`).then(r => r.data || []),
  analyticsSummary: (days = 7) => request<{ summary: import('../types/api').AnalyticsSummary; days: import('../types/api').AnalyticsDay[]; window_days: number }>(`${CONSOLE_BASE}/analytics/summary${query({ days })}`),
  ingressStats: (signal?: AbortSignal) => request<IngressStats>(`${CONSOLE_BASE}/operations/ingress`, { signal }),
  runtimeObservability: (signal?: AbortSignal) => request<RuntimeObservability>(`${CONSOLE_BASE}/operations/runtime`, { signal }),
  integrationAudit: (limit = 50, signal?: AbortSignal) => request<{ data: import('../types/api').IntegrationAuditEvent[] }>(`${CONSOLE_BASE}/operations/audit${query({ limit })}`, { signal }).then(r => r.data || []),
  apiUsage: (hours = 24, signal?: AbortSignal) => request<APIUsageReport>(`${CONSOLE_BASE}/operations/api-usage${query({ hours })}`, { signal }),
  maintenancePreview: (olderThanMinutes = 240, signal?: AbortSignal) => request<MaintenancePreview>(`${CONSOLE_BASE}/operations/maintenance/preview${query({ older_than_minutes: olderThanMinutes })}`, { signal }),
  cleanupPreview: (payload: Record<string, unknown>, signal?: AbortSignal) => request<CleanupPreview>(`${CONSOLE_BASE}/operations/cleanup/preview`, { method: 'POST', body: JSON.stringify(payload), signal }),

  domains: (signal?: AbortSignal) => request<{ domains: Domain[] }>(`${CONSOLE_BASE}/domains`, { signal }).then(r => r.domains || []),
  domainHealth: (signal?: AbortSignal) => request<{ data: DomainHealth[] }>(`${CONSOLE_BASE}/domains/health`, { signal }).then(r => r.data || []),
  addDomain: (domain: string) => request<DomainSubmitResponse>(`${CONSOLE_BASE}/domains/mx-register`, { method: 'POST', body: JSON.stringify({ domain }) }),
  toggleDomain: (id: number, active: boolean) => request(`${CONSOLE_BASE}/domains/${id}/toggle`, { method: 'PUT', body: JSON.stringify({ active }) }),
  deleteDomain: (id: number) => request(`${CONSOLE_BASE}/domains/${id}`, { method: 'DELETE' }),
  refreshDomainHealth: () => request<{ data: DomainHealth[] }>(`${CONSOLE_BASE}/domains/health/refresh`, { method: 'POST' }),
  donations: () => request<DonationListResponse>(`${CONSOLE_BASE}/donations`),
  recheckDonation: (id: string) => request<{ donation: import('../types/api').DomainDonation }>(`${CONSOLE_BASE}/donations/${id}/recheck`, { method: 'POST' }),
  revokeDonation: (id: string, note = '') => request(`${CONSOLE_BASE}/donations/${id}/revoke`, { method: 'POST', body: JSON.stringify({ note }) }),
  adjustDonation: (id: string, payload: { total_delta: number; daily_delta: number; rpm_delta: number; note: string }) => request<{ donation: import('../types/api').DomainDonation }>(`${CONSOLE_BASE}/donations/${id}/adjust`, { method: 'POST', body: JSON.stringify(payload) }),
  adjustDonationToken: (id: string, payload: { total_delta: number; daily_delta: number; rpm_delta: number; note: string }) => request<{ message: string }>(`${CONSOLE_BASE}/donations/tokens/${id}/adjust`, { method: 'POST', body: JSON.stringify(payload) }),
  applyDonationPolicy: () => request<{ message: string }>(`${CONSOLE_BASE}/donations/policy/apply`, { method: 'POST' }),

  mailboxes: (params: Record<string, string | number | boolean | undefined> = {}) => request<PageResponse<Mailbox>>(`${CONSOLE_BASE}/mailboxes${query(params)}`),
  mailbox: (id: string, signal?: AbortSignal) => request<{ mailbox: Mailbox }>(`${CONSOLE_BASE}/mailboxes/${id}`, { signal }).then(r => r.mailbox),
  createMailbox: (address = '', domain = '') => request<{ mailbox: Mailbox }>(`${CONSOLE_BASE}/mailboxes`, { method: 'POST', body: JSON.stringify({ address, domain }) }).then(r => r.mailbox),
  updateMailboxRetention: (id: string, keep: boolean) => request<{ mailbox: Mailbox }>(`${CONSOLE_BASE}/mailboxes/${id}/retention`, { method: 'PUT', body: JSON.stringify({ keep_forever: keep }) }).then(r => r.mailbox),
  updateMailboxRetentionBatch: (ids: string[], keep: boolean) => request<{ data: Mailbox[] }>(`${CONSOLE_BASE}/mailboxes/retention/batch`, { method: 'POST', body: JSON.stringify({ ids, keep_forever: keep }) }),
  deleteMailbox: (id: string) => request(`${CONSOLE_BASE}/mailboxes/${id}`, { method: 'DELETE' }),
  cleanupMailboxes: (payload: Record<string, unknown>) => request<{ deleted: number }>(`${CONSOLE_BASE}/mailboxes/cleanup`, { method: 'POST', body: JSON.stringify(payload) }),
  cleanupEmails: (payload: Record<string, unknown>) => request<{ deleted: number }>(`${CONSOLE_BASE}/emails/cleanup`, { method: 'POST', body: JSON.stringify(payload) }),

  emails: (mailboxId: string, page = 1, size = 100, signal?: AbortSignal) => request<PageResponse<EmailSummary>>(`${CONSOLE_BASE}/mailboxes/${mailboxId}/emails${query({ page, size })}`, { signal }),
  email: (mailboxId: string, emailId: string) => request<{ email: EmailMessage }>(`${CONSOLE_BASE}/mailboxes/${mailboxId}/emails/${emailId}`).then(r => r.email),
  deleteEmail: (mailboxId: string, emailId: string) => request(`${CONSOLE_BASE}/mailboxes/${mailboxId}/emails/${emailId}`, { method: 'DELETE' }),

  lookupMailbox: (address: string) => request<{ mailbox: Mailbox }>(`${CONSOLE_BASE}/lookup/mailbox${query({ address })}`),
  lookupLatest: (address: string) => request<LookupResult>(`${CONSOLE_BASE}/lookup/latest${query({ address })}`),
  lookupLatestCode: (address: string) => request<LookupResult>(`${CONSOLE_BASE}/lookup/latest-code${query({ address })}`),
  lookupLatestLink: (address: string) => request<LookupResult>(`${CONSOLE_BASE}/lookup/latest-link${query({ address })}`),

  tokens: () => request<{ data: AccountToken[] }>(`${CONSOLE_BASE}/tokens`).then(r => r.data || []),
  createToken: (payload: Record<string, unknown>) => request<{ token: AccountToken; access_token: string }>(`${CONSOLE_BASE}/tokens`, { method: 'POST', body: JSON.stringify(payload) }),
  updateToken: (id: string, payload: Record<string, unknown>) => request<{ token: AccountToken }>(`${CONSOLE_BASE}/tokens/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  rotateToken: (id: string) => request<{ token: AccountToken; access_token: string }>(`${CONSOLE_BASE}/tokens/${id}/rotate`, { method: 'POST' }),
  disableToken: (id: string) => request(`${CONSOLE_BASE}/tokens/${id}/disable`, { method: 'POST' }),
  enableToken: (id: string) => request(`${CONSOLE_BASE}/tokens/${id}/enable`, { method: 'POST' }),
  deleteToken: (id: string) => request(`${CONSOLE_BASE}/tokens/${id}`, { method: 'DELETE' }),

  settings: () => request<Record<string, string>>(`${CONSOLE_BASE}/settings`),
  saveSettings: (payload: Record<string, string>) => request(`${CONSOLE_BASE}/settings`, { method: 'PUT', body: JSON.stringify(payload) }),
  notificationIntegrations: () => request<NotificationIntegrationStatus>(`${CONSOLE_BASE}/integrations/notifications`),
  saveNotificationIntegrations: (payload: Record<string, unknown>) => request(`${CONSOLE_BASE}/integrations/notifications`, { method: 'PUT', body: JSON.stringify(payload) }),
  testNotificationIntegration: (channel: 'generic' | 'telegram' | 'discord') => request(`${CONSOLE_BASE}/integrations/notifications/test`, { method: 'POST', body: JSON.stringify({ channel }) }),
  cloudflareIntegration: (signal?: AbortSignal) => request<CloudflareIntegrationStatus>(`${CONSOLE_BASE}/integrations/cloudflare`, { signal }),
  saveCloudflareIntegration: (apiToken = '', clear = false) => request(`${CONSOLE_BASE}/integrations/cloudflare`, { method: 'PUT', body: JSON.stringify({ api_token: apiToken, clear }) }),
  testCloudflareIntegration: (domain = '') => request<{ message: string; zone?: string }>(`${CONSOLE_BASE}/integrations/cloudflare/test`, { method: 'POST', body: JSON.stringify({ domain }) }),
  dnsPreview: (domain: string, signal?: AbortSignal) => request<DNSPlan>(`${CONSOLE_BASE}/dns/preview`, { method: 'POST', body: JSON.stringify({ domain }), signal }),
  cloudflarePreview: (domain: string, signal?: AbortSignal) => request<DNSPlan>(`${CONSOLE_BASE}/integrations/cloudflare/preview`, { method: 'POST', body: JSON.stringify({ domain }), signal }),
  cloudflareApply: (domain: string, confirmConflicts = false) => request<DNSPlan>(`${CONSOLE_BASE}/integrations/cloudflare/apply`, { method: 'POST', body: JSON.stringify({ domain, confirm_conflicts: confirmConflicts }) }),
  rotateAdminKey: (adminAuthKey = '') => request<{ admin_auth_key: string }>(`${CONSOLE_BASE}/auth-key/rotate`, { method: 'POST', body: JSON.stringify({ admin_auth_key: adminAuthKey }) }),
}
