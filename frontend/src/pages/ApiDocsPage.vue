<script setup lang="ts">
import { computed, ref, watchEffect } from 'vue'
import UiIcon from '../components/UiIcon.vue'
import { PRODUCT_NAME } from '../config/brand'
import { localeState, tr } from '../stores/i18n'
import { authState } from '../stores/auth'
import { toast } from '../stores/toast'
import { setPageHeader } from '../stores/ui'
import { copyText } from '../utils/format'
import { publicAPIOrigin } from '../services/api'

type Namespace = 'api' | 'public' | 'system'
type AuthKind = 'api_token' | 'donation_token' | 'public'
type GroupKey = 'mailboxes' | 'emails' | 'lookup' | 'donation' | 'public' | 'system'
type ParameterLocation = 'path' | 'query' | 'body'
type LocalizedText = { zhCN: string; enUS: string }
type EndpointParameter = {
  name: string
  type: string
  location: ParameterLocation
  required: boolean
  description: LocalizedText
}
type Endpoint = {
  namespace: Namespace
  group: GroupKey
  method: 'GET' | 'POST' | 'PUT' | 'DELETE'
  path: string
  examplePath?: string
  query?: string
  auth: AuthKind
  scope: string
  title: LocalizedText
  detail: LocalizedText
  params: EndpointParameter[]
  body?: string
  responseStatus: number
  response: string
  responseType?: 'json' | 'sse' | 'binary'
}

const origin = publicAPIOrigin || window.location.origin
const apiBase = `${origin}/api/v1`
const publicBase = `${origin}/public/v1`

const deploymentSMTP = computed(() => {
  const hostname = String(authState.publicSettings.smtp_hostname || '').trim()
  const serverIP = String(authState.publicSettings.smtp_server_ip || '').trim()
  return hostname || serverIP || '<smtp-host-from-settings>'
})

function resolveDeploymentValue(value: string): string {
  return value.replaceAll('mail.your-host.example', deploymentSMTP.value)
}

const copy = (zhCN: string, enUS: string): LocalizedText => ({ zhCN, enUS })
const param = (name: string, type: string, location: ParameterLocation, required: boolean, zhCN: string, enUS: string): EndpointParameter => ({
  name, type, location, required, description: copy(zhCN, enUS),
})

const endpoints: Endpoint[] = [
  {
    namespace: 'api', group: 'mailboxes', method: 'GET', path: '/domains', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('查询可用根域', 'List active root domains'),
    detail: copy('返回可用于创建邮箱的活动根域。随机子域由调用方拼接在活动根域之前，根域必须配置通配 MX 才能接收子域邮件。', 'Returns active root domains available for mailbox creation. Clients build subdomains under a root domain; wildcard MX is required to receive subdomain email.'),
    params: [], responseStatus: 200,
    response: `{"domains":[{"id":1,"domain":"example.com","is_active":true,"status":"active","visibility":"public","source_type":"manual"}]}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'POST', path: '/mailboxes', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('创建邮箱', 'Create a mailbox'),
    detail: copy('address 和 domain 均可省略。domain 可以是活动根域，也可以是该根域下的有效子域。', 'Both address and domain are optional. Domain may be an active root domain or a valid subdomain beneath one.'),
    params: [
      param('address', 'string', 'body', false, '邮箱本地部分；留空时生成 10 位随机值', 'Mailbox local part; a 10-character value is generated when omitted'),
      param('domain', 'string', 'body', false, '活动根域或其子域；留空时随机选择活动根域', 'Active root domain or subdomain; a root domain is selected when omitted'),
    ],
    body: `{"address":"verify","domain":"example.com"}`, responseStatus: 201,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","account_id":"00000000-0000-0000-0000-000000000000","address":"verify","domain_id":1,"full_address":"verify@example.com","expires_at":"2026-07-31T00:00:00Z","keep_forever":false,"created_at":"2026-07-30T22:00:00Z"}}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'GET', path: '/mailboxes', query: 'page=1&size=20', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('查询邮箱', 'List mailboxes'),
    detail: copy('分页查询当前 API Token 可访问的邮箱，并支持地址、域名、保留状态和到期时间筛选。', 'Lists mailboxes visible to the current API Token with pagination and optional address, domain, retention, and expiry filters.'),
    params: [
      param('page', 'integer', 'query', false, '页码，默认 1', 'Page number, default 1'),
      param('size', 'integer', 'query', false, '每页数量，默认 20，最大 100', 'Page size, default 20, maximum 100'),
      param('q', 'string', 'query', false, '邮箱地址搜索词', 'Mailbox address search text'),
      param('domain', 'string', 'query', false, '限定域名', 'Limit results to a domain'),
      param('keep_forever', 'boolean', 'query', false, '仅返回永久保留邮箱', 'Return retained mailboxes only'),
      param('expiring_within_hours', 'integer', 'query', false, '仅返回指定小时内到期的邮箱', 'Return mailboxes expiring within this many hours'),
    ], responseStatus: 200,
    response: `{"data":[],"total":0,"page":1,"size":20,"q":"","domain":"","keep_forever":false,"expiring_within_hours":0}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'GET', path: '/mailboxes/:id', examplePath: '/mailboxes/$MAILBOX_ID', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('读取邮箱', 'Get a mailbox'), detail: copy('按 UUID 读取邮箱信息。邮件、验证码和验证链接通过对应 Endpoint 获取。', 'Gets a mailbox by UUID. Use the dedicated endpoints for email, verification codes, and links.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID')], responseStatus: 200,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com","expires_at":"2026-07-31T00:00:00Z","keep_forever":false}}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'PUT', path: '/mailboxes/:id/retention', examplePath: '/mailboxes/$MAILBOX_ID/retention', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('更新邮箱保留策略', 'Update mailbox retention'), detail: copy('设置永久保留，或恢复站点默认邮箱 TTL。', 'Retains a mailbox indefinitely or returns it to the site mailbox TTL.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID'), param('keep_forever', 'boolean', 'body', true, '是否永久保留', 'Whether to retain indefinitely')],
    body: `{"keep_forever":true}`, responseStatus: 200,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com","keep_forever":true,"expires_at":null}}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'POST', path: '/mailboxes/retention/batch', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('批量更新保留策略', 'Batch update mailbox retention'), detail: copy('一次更新多个邮箱的永久保留状态。', 'Updates the retention state of multiple mailboxes.'),
    params: [param('ids', 'uuid[]', 'body', true, '邮箱 ID 数组', 'Mailbox ID array'), param('keep_forever', 'boolean', 'body', true, '是否永久保留', 'Whether to retain indefinitely')],
    body: `{"ids":["00000000-0000-0000-0000-000000000000"],"keep_forever":true}`, responseStatus: 200,
    response: `{"data":[{"id":"00000000-0000-0000-0000-000000000000","keep_forever":true,"expires_at":null}],"updated_count":1}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'DELETE', path: '/mailboxes/:id', examplePath: '/mailboxes/$MAILBOX_ID', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('删除邮箱', 'Delete a mailbox'), detail: copy('永久删除邮箱及其关联邮件。', 'Permanently deletes a mailbox and its associated email.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID')], responseStatus: 200, response: `{"message":"mailbox deleted"}`,
  },
  {
    namespace: 'api', group: 'mailboxes', method: 'POST', path: '/mailboxes/cleanup', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('按条件清理邮箱', 'Clean up mailboxes'), detail: copy('按地址、域名、过期状态或空邮箱条件批量删除。所有条件都省略时会匹配当前 Token 可管理的邮箱。', 'Deletes mailboxes by address, domain, expiry, or empty-mailbox filters. Omitting every filter matches mailboxes manageable by the current Token.'),
    params: [
      param('query', 'string', 'body', false, '邮箱地址搜索词', 'Mailbox address search text'),
      param('domain', 'string', 'body', false, '限定域名', 'Limit results to a domain'),
      param('only_expired', 'boolean', 'body', false, '仅清理已过期邮箱', 'Delete expired mailboxes only'),
      param('only_empty', 'boolean', 'body', false, '仅清理空邮箱', 'Delete empty mailboxes only'),
    ], body: `{"only_expired":true}`, responseStatus: 200,
    response: `{"deleted":0,"query":"","domain":"","only_expired":true,"only_empty":false}`,
  },
  {
    namespace: 'api', group: 'emails', method: 'GET', path: '/mailboxes/:id/emails', examplePath: '/mailboxes/$MAILBOX_ID/emails', query: 'page=1&size=20', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('查询邮件', 'List mailbox email'), detail: copy('分页读取指定邮箱的邮件摘要。', 'Lists email summaries for a mailbox.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID'), param('page', 'integer', 'query', false, '页码，默认 1', 'Page number, default 1'), param('size', 'integer', 'query', false, '每页数量，默认 20，最大 100', 'Page size, default 20, maximum 100')],
    responseStatus: 200, response: `{"data":[{"id":"00000000-0000-0000-0000-000000000000","sender":"noreply@example.com","subject":"Verification code 123456","has_attachments":false,"size_bytes":2048,"received_at":"2026-07-30T22:00:00Z"}],"total":1,"page":1,"size":20}`,
  },
  {
    namespace: 'api', group: 'emails', method: 'GET', path: '/mailboxes/:id/emails/:email_id', examplePath: '/mailboxes/$MAILBOX_ID/emails/$EMAIL_ID', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('读取邮件', 'Get an email'), detail: copy('读取邮件正文、Headers、附件标记以及预解析的验证码和验证链接。', 'Gets the email body, headers, attachment flag, and projected verification code and link.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID'), param('email_id', 'uuid', 'path', true, '邮件 ID', 'Email ID')], responseStatus: 200,
    response: `{"email":{"id":"00000000-0000-0000-0000-000000000000","mailbox_id":"00000000-0000-0000-0000-000000000000","sender":"noreply@example.com","subject":"Verification code 123456","body_text":"Your code is 123456","body_html":"<p>Your code is 123456</p>","has_attachments":false,"parsed_code":"123456","parsed_code_source":"body","size_bytes":2048,"received_at":"2026-07-30T22:00:00Z"}}`,
  },
  {
    namespace: 'api', group: 'emails', method: 'DELETE', path: '/mailboxes/:id/emails/:email_id', examplePath: '/mailboxes/$MAILBOX_ID/emails/$EMAIL_ID', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('删除邮件', 'Delete an email'), detail: copy('永久删除指定邮件。', 'Permanently deletes an email.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID'), param('email_id', 'uuid', 'path', true, '邮件 ID', 'Email ID')], responseStatus: 200, response: `{"message":"email deleted"}`,
  },
  {
    namespace: 'api', group: 'emails', method: 'POST', path: '/emails/cleanup', auth: 'api_token', scope: 'cleanup / owner',
    title: copy('清理历史邮件', 'Clean up email'), detail: copy('按邮箱或邮件搜索词、域名和最小邮件年龄批量删除。', 'Deletes email by mailbox or email search text, domain, and minimum message age.'),
    params: [param('query', 'string', 'body', false, '邮箱或邮件搜索词', 'Mailbox or email search text'), param('domain', 'string', 'body', false, '限定域名', 'Limit results to a domain'), param('older_than_minutes', 'integer', 'body', false, '仅删除早于该分钟数的邮件', 'Delete email older than this many minutes')],
    body: `{"older_than_minutes":240}`, responseStatus: 200, response: `{"deleted":0,"query":"","domain":"","older_than_minutes":240}`,
  },
  {
    namespace: 'api', group: 'emails', method: 'GET', path: '/mailboxes/:id/events', examplePath: '/mailboxes/$MAILBOX_ID/events', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('实时接收邮件事件', 'Stream mailbox events'), detail: copy('建立 SSE 长连接。服务端先发送 ready，每封新邮件提交后发送 email，并每 20 秒发送 heartbeat。', 'Opens an SSE stream. The server sends ready first, email after each committed message, and a heartbeat every 20 seconds.'),
    params: [param('id', 'uuid', 'path', true, '邮箱 ID', 'Mailbox ID')], responseStatus: 200, responseType: 'sse',
    response: `event: ready\ndata: {"mailbox_id":"00000000-0000-0000-0000-000000000000"}\n\nid: 00000000-0000-0000-0000-000000000000\nevent: email\ndata: {"mailbox_id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com","email":{"id":"00000000-0000-0000-0000-000000000000","sender":"noreply@example.com","subject":"Verification code 123456","has_attachments":false,"parsed_code":"123456","parsed_code_source":"body","parsed_link":"https://example.com/verify","parsed_link_source":"body"}}`,
  },
  {
    namespace: 'api', group: 'lookup', method: 'GET', path: '/lookup/mailbox', query: 'address=verify@example.com', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('按地址查找邮箱', 'Look up a mailbox'), detail: copy('通过完整邮箱地址查找邮箱。', 'Finds a mailbox by its full email address.'),
    params: [param('address', 'string', 'query', true, '完整邮箱地址', 'Full email address')], responseStatus: 200,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com","keep_forever":false}}`,
  },
  {
    namespace: 'api', group: 'lookup', method: 'GET', path: '/lookup/latest', query: 'address=verify@example.com', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('读取最新邮件', 'Get the latest email'), detail: copy('按完整邮箱地址返回邮箱与最新一封完整邮件；邮箱不存在或没有邮件时返回 404。', 'Returns a mailbox and its latest complete email by address; returns 404 when the mailbox or email does not exist.'),
    params: [param('address', 'string', 'query', true, '完整邮箱地址', 'Full email address')], responseStatus: 200,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com"},"email":{"id":"00000000-0000-0000-0000-000000000000","subject":"Verification code 123456","body_text":"Your code is 123456"}}`,
  },
  {
    namespace: 'api', group: 'lookup', method: 'GET', path: '/lookup/latest-code', query: 'address=verify@example.com', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('读取最新验证码', 'Get the latest verification code'), detail: copy('从 mailbox_state 投影读取最新验证码；没有邮箱或邮件时返回 404。', 'Reads the latest verification code from the mailbox_state projection; returns 404 when the mailbox or email does not exist.'),
    params: [param('address', 'string', 'query', true, '完整邮箱地址', 'Full email address')], responseStatus: 200,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com"},"email_id":"00000000-0000-0000-0000-000000000000","sender":"noreply@example.com","subject":"Verification code 123456","received_at":"2026-07-30T22:00:00Z","code":"123456","matched_by":"body","has_code":true}`,
  },
  {
    namespace: 'api', group: 'lookup', method: 'GET', path: '/lookup/latest-link', query: 'address=verify@example.com', auth: 'api_token', scope: 'read / cleanup / owner',
    title: copy('读取最新验证链接', 'Get the latest verification link'), detail: copy('从 mailbox_state 投影读取最新 HTTP/HTTPS 验证链接；没有邮箱或邮件时返回 404。', 'Reads the latest HTTP/HTTPS verification link from the mailbox_state projection; returns 404 when the mailbox or email does not exist.'),
    params: [param('address', 'string', 'query', true, '完整邮箱地址', 'Full email address')], responseStatus: 200,
    response: `{"mailbox":{"id":"00000000-0000-0000-0000-000000000000","full_address":"verify@example.com"},"email_id":"00000000-0000-0000-0000-000000000000","sender":"noreply@example.com","subject":"Verify your email","received_at":"2026-07-30T22:00:00Z","link":"https://example.com/verify","matched_by":"body","has_link":true}`,
  },
  {
    namespace: 'api', group: 'donation', method: 'POST', path: '/donations', auth: 'donation_token', scope: 'donation token / no quota charge',
    title: copy('使用奖励 Token 继续捐赠', 'Donate with an existing reward Token'), detail: copy('仅接受现有奖励 API Token，将新根域关联到同一 Token。该请求不消耗奖励 Token 的调用额度。', 'Accepts an existing donation reward API Token and associates another root domain with it. This request does not consume reward quota.'),
    params: [param('domain', 'string', 'body', true, '提交者控制的根域名', 'Root domain controlled by the contributor'), param('enable_subdomains', 'boolean', 'body', false, '是否加入通配 MX 指引', 'Whether to include wildcard MX guidance')],
    body: `{"domain":"example.com","enable_subdomains":true}`, responseStatus: 202,
    response: `{"donation_id":"00000000-0000-0000-0000-000000000000","claim_secret":"claim-secret-returned-once","access_token":"","token_prefix":"0123456789abcdef","status":"pending","enable_subdomains":true,"dns_required":[{"type":"MX","host":"@","value":"mail.your-host.example","priority":10},{"type":"TXT","host":"_far-mail-donate","value":"far-mail-site-verification=challenge"}],"message":"domain submitted"}`,
  },
  {
    namespace: 'public', group: 'public', method: 'GET', path: '/settings', auth: 'public', scope: 'public',
    title: copy('读取公开站点配置', 'Get public site settings'), detail: copy('返回公开页面所需的站点名称、Logo、SMTP 指引来源、捐赠开关与奖励规则。', 'Returns the site name, logo, SMTP guidance source, donation switch, and public reward policy.'),
    params: [], responseStatus: 200,
    response: `{"site_title":"FAR Mail","site_logo_url":"","smtp_server_ip":"","smtp_hostname":"mail.your-host.example","inbox_refresh_seconds":"3","announcement":"","donation_enabled":"true","donation_reward_rate_limit_per_minute":"30","donation_reward_daily_request_limit":"5000","donation_reward_total_request_limit":"100000"}`,
  },
  {
    namespace: 'public', group: 'public', method: 'GET', path: '/logo', auth: 'public', scope: 'public',
    title: copy('读取站点 Logo', 'Get the site logo'), detail: copy('输出站长配置的 Logo 图片，或重定向到外部 Logo URL；未配置时返回 404。', 'Returns the configured logo image or redirects to an external logo URL; returns 404 when no logo is configured.'),
    params: [], responseStatus: 200, responseType: 'binary', response: `HTTP/1.1 200 OK\nContent-Type: image/png\nCache-Control: public, max-age=86400, immutable\n\n<binary image body>`,
  },
  {
    namespace: 'public', group: 'public', method: 'POST', path: '/domains/submit', auth: 'public', scope: 'public / 5 requests per minute',
    title: copy('首次捐赠域名', 'Submit a first domain donation'), detail: copy('创建待验证捐赠、claim secret 和新的奖励 API Token。claim secret 与完整 Token 只在本次响应中返回。', 'Creates a pending donation, claim secret, and reward API Token. The claim secret and complete Token are returned only in this response.'),
    params: [param('domain', 'string', 'body', true, '提交者控制的根域名', 'Root domain controlled by the contributor'), param('enable_subdomains', 'boolean', 'body', false, '是否加入通配 MX 指引', 'Whether to include wildcard MX guidance')],
    body: `{"domain":"example.com","enable_subdomains":true}`, responseStatus: 202,
    response: `{"donation_id":"00000000-0000-0000-0000-000000000000","claim_secret":"claim-secret-returned-once","access_token":"0123456789abcdef0123456789abcdef","token_prefix":"0123456789abcdef","status":"pending","enable_subdomains":true,"dns_required":[{"type":"MX","host":"@","value":"mail.your-host.example","priority":10},{"type":"TXT","host":"_far-mail-donate","value":"far-mail-site-verification=challenge"}],"message":"domain submitted"}`,
  },
  {
    namespace: 'public', group: 'public', method: 'POST', path: '/domains/status', auth: 'public', scope: 'claim secret / 120 requests per minute',
    title: copy('查询捐赠验证状态', 'Get donation verification status'), detail: copy('使用 donation_id 和独立 claim secret 查询状态；必要时触发一次 DNS 复检。', 'Uses the donation ID and independent claim secret to get status and may trigger a DNS recheck.'),
    params: [param('donation_id', 'uuid', 'body', true, '捐赠记录 ID', 'Donation ID'), param('claim_secret', 'string', 'body', true, '首次提交时返回的状态查询凭据', 'Status credential returned by the initial submission')],
    body: `{"donation_id":"00000000-0000-0000-0000-000000000000","claim_secret":"claim-secret-returned-once"}`, responseStatus: 200,
    response: `{"id":"00000000-0000-0000-0000-000000000000","domain":"example.com","status":"pending","is_active":false,"mx_checked_at":"2026-07-30T22:00:00Z","dns_required":[{"type":"MX","host":"@","value":"mail.your-host.example","priority":10},{"type":"TXT","host":"_far-mail-donate","value":"far-mail-site-verification=challenge"}]}`,
  },
  {
    namespace: 'system', group: 'system', method: 'GET', path: '/health', auth: 'public', scope: 'public',
    title: copy('健康检查', 'Health check'), detail: copy('返回 Go API 进程状态与当前 Unix 时间。', 'Returns the Go API process status and current Unix time.'),
    params: [], responseStatus: 200, response: `{"status":"ok","time":1785420000}`,
  },
]

const groupOrder: GroupKey[] = ['mailboxes', 'emails', 'lookup', 'donation', 'public', 'system']
const groupNames: Record<GroupKey, LocalizedText> = {
  mailboxes: copy('邮箱与域名', 'MAILBOXES & DOMAINS'),
  emails: copy('邮件与事件', 'EMAIL & EVENTS'),
  lookup: copy('快速查询', 'LOOKUP'),
  donation: copy('奖励续捐', 'REWARD DONATION'),
  public: copy('公开接口', 'PUBLIC'),
  system: copy('系统', 'SYSTEM'),
}

function localize(value: LocalizedText): string {
  return localeState.locale === 'en-US' ? value.enUS : value.zhCN
}

function namespaceBase(namespace: Namespace): string {
  if (namespace === 'api') return '/api/v1'
  if (namespace === 'public') return '/public/v1'
  return ''
}

function endpointPath(endpoint: Endpoint, useExample = false): string {
  const path = useExample && endpoint.examplePath ? endpoint.examplePath : endpoint.path
  return `${namespaceBase(endpoint.namespace)}${path}`
}

function endpointURL(endpoint: Endpoint, useExample = false): string {
  const query = endpoint.query ? `?${endpoint.query}` : ''
  return `${origin}${endpointPath(endpoint, useExample)}${query}`
}

function authTokenVariable(endpoint: Endpoint): string {
  return endpoint.auth === 'donation_token' ? '$DONATION_TOKEN' : '$TOKEN'
}

function authLabel(endpoint: Endpoint): string {
  if (endpoint.auth === 'api_token') return `Bearer · ${endpoint.scope}`
  if (endpoint.auth === 'donation_token') return tr('奖励 Token · 不计额度', 'Donation Token · no quota')
  return tr('公开', 'Public')
}

function requestHeaders(endpoint: Endpoint): string[] {
  const headers: string[] = []
  if (endpoint.auth === 'api_token' || endpoint.auth === 'donation_token') headers.push(`Authorization: Bearer ${authTokenVariable(endpoint)}`)
  if (endpoint.responseType === 'sse') headers.push('Accept: text/event-stream')
  if (endpoint.body) headers.push('Content-Type: application/json')
  return headers
}

function curlExample(endpoint: Endpoint): string {
  const first = `curl${endpoint.method === 'GET' ? '' : ` -X ${endpoint.method}`} "${endpointURL(endpoint, true)}"`
  const lines = [first, ...requestHeaders(endpoint).map(header => `  -H "${header}"`)]
  if (endpoint.body) lines.push(`  -d '${endpoint.body}'`)
  return lines.map((line, index) => index < lines.length - 1 ? `${line} \\` : line).join('\n')
}

function httpExample(endpoint: Endpoint): string {
  const query = endpoint.query ? `?${endpoint.query}` : ''
  const lines = [`${endpoint.method} ${endpointPath(endpoint, true)}${query} HTTP/1.1`, `Host: ${window.location.host}`, ...requestHeaders(endpoint)]
  if (endpoint.body) lines.push('', formatJSON(endpoint.body))
  return lines.join('\n')
}

function jsExamplePath(endpoint: Endpoint): string {
  return endpointURL(endpoint, true)
    .replaceAll('$MAILBOX_ID', '${mailboxId}')
    .replaceAll('$EMAIL_ID', '${emailId}')
    .replaceAll('$DONATION_ID', '${donationId}')
}

function javascriptExample(endpoint: Endpoint): string {
  const headers: string[] = []
  if (endpoint.auth === 'api_token') headers.push("Authorization: 'Bearer ' + token")
  if (endpoint.auth === 'donation_token') headers.push("Authorization: 'Bearer ' + donationToken")
  if (endpoint.responseType === 'sse') headers.push("Accept: 'text/event-stream'")
  if (endpoint.body) headers.push("'Content-Type': 'application/json'")

  const options = [`  method: '${endpoint.method}',`]
  if (headers.length) options.push(`  headers: { ${headers.join(', ')} },`)
  if (endpoint.body) options.push(`  body: JSON.stringify(${formatJSON(endpoint.body)}),`)
  const reader = endpoint.responseType === 'binary' ? 'blob' : endpoint.responseType === 'sse' ? 'body' : 'json'
  const readLine = reader === 'body' ? 'const stream = response.body' : `const data = await response.${reader}()`
  return `const response = await fetch(\`${jsExamplePath(endpoint)}\`, {\n${options.join('\n')}\n})\n${readLine}`
}

function formatJSON(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function responseLanguage(endpoint: Endpoint): string {
  if (endpoint.responseType === 'sse') return 'text'
  if (endpoint.responseType === 'binary') return 'http'
  return 'json'
}

const endpointGroups = computed(() => groupOrder.map(key => ({
  key,
  label: localize(groupNames[key]),
  endpoints: endpoints.filter(endpoint => {
    if (endpoint.group !== key) return false
    const query = docsQuery.value.trim().toLowerCase()
    return !query || endpoint.path.toLowerCase().includes(query) || endpoint.method.toLowerCase().includes(query) || localize(endpoint.title).toLowerCase().includes(query)
  }),
})).filter(group => group.endpoints.length > 0))
const apiEndpointCount = computed(() => endpoints.filter(endpoint => endpoint.namespace === 'api').length)
const publicEndpointCount = computed(() => endpoints.filter(endpoint => endpoint.namespace === 'public').length)
const systemEndpointCount = computed(() => endpoints.filter(endpoint => endpoint.namespace === 'system').length)
const selectedEndpoint = ref<Endpoint>(endpoints[0])
const docsQuery = ref('')
const selectedEndpointIndex = computed({
  get: () => endpoints.indexOf(selectedEndpoint.value),
  set: (index: number) => chooseEndpoint(endpoints[index] ?? endpoints[0]),
})
const exampleMode = ref<'request' | 'response'>('request')
const language = ref<'cURL' | 'HTTP' | 'JavaScript'>('cURL')

const exampleCode = computed(() => {
  if (exampleMode.value === 'response') return formatJSON(resolveDeploymentValue(selectedEndpoint.value.response))
  if (language.value === 'HTTP') return httpExample(selectedEndpoint.value)
  if (language.value === 'JavaScript') return javascriptExample(selectedEndpoint.value)
  return curlExample(selectedEndpoint.value)
})

function markdownEndpoint(endpoint: Endpoint): string {
  const parameterRows = endpoint.params.length
    ? ['| 参数 | 位置 | 类型 | 必填 | 说明 |', '|---|---|---|---|---|', ...endpoint.params.map(item => `| \`${item.name}\` | ${item.location} | \`${item.type}\` | ${item.required ? tr('是', 'yes') : tr('否', 'no')} | ${localize(item.description)} |`)].join('\n')
    : tr('无请求参数。', 'No request parameters.')
  return [
    `### ${localize(endpoint.title)}`,
    '', localize(endpoint.detail), '',
    `- Endpoint: \`${endpoint.method} ${endpointPath(endpoint)}\``,
    `- ${tr('认证', 'Authentication')}: ${authLabel(endpoint)}`,
    `- ${tr('成功状态', 'Success status')}: \`${endpoint.responseStatus}\``, '',
    `#### ${tr('参数', 'Parameters')}`, '', parameterRows, '',
    `#### ${tr('请求', 'Request')}`, '', '```bash', curlExample(endpoint), '```', '',
    `#### ${tr('响应', 'Response')} ${endpoint.responseStatus}`, '', `\`\`\`${responseLanguage(endpoint)}`, formatJSON(resolveDeploymentValue(endpoint.response)), '```',
  ].join('\n')
}

const docsMarkdown = computed(() => {
  const indexRows = endpoints.map(endpoint => `| \`${endpoint.method}\` | \`${endpointPath(endpoint)}\` | ${authLabel(endpoint)} | ${localize(endpoint.title)} |`).join('\n')
  const details = endpoints.map(markdownEndpoint).join('\n\n')
  return [
    `# ${PRODUCT_NAME} API`, '',
    `> ${tr('由当前部署地址生成', 'Generated from the current deployment')}: ${origin}`, '',
    `- API Base URL: \`${apiBase}\``,
    `- Public Base URL: \`${publicBase}\``,
    `- Health: \`${origin}/health\``, '',
    `## ${tr('凭据边界', 'Credential boundary')}`, '',
    tr('- `/api/v1` 仅接受 `Authorization: Bearer <API_TOKEN>`。', '- `/api/v1` accepts only `Authorization: Bearer <API_TOKEN>`.'),
    tr('- `/console/v1` 的 Admin Key 使用 `X-Admin-Key`，不能调用 `/api/v1`。', '- The `/console/v1` Admin Key uses `X-Admin-Key` and cannot call `/api/v1`.'),
    tr('- API Token 不能登录 `/console/v1`；两种凭据没有 fallback。', '- API Tokens cannot sign in to `/console/v1`; no credential fallback exists.'),
    tr('- 旧 `/api`、`/v1`、`GET /me` 和 query-string key 均不兼容并返回 404。', '- Legacy `/api`, `/v1`, `GET /me`, and query-string keys are unsupported and return 404.'), '',
    `## ${tr('随机子域', 'Random subdomains')}`, '',
    tr('先调用 `GET /api/v1/domains` 选择活动根域，再由调用方生成子域标签，将完整的 `子域.根域` 作为 `POST /api/v1/mailboxes` 的 `domain`。根域需要通配 MX；API 不提供独立 `subdomain` 字段。', 'Call `GET /api/v1/domains` to select an active root, generate labels client-side, and send the complete `subdomain.root` as `domain` to `POST /api/v1/mailboxes`. The root requires wildcard MX; the API has no separate `subdomain` field.'), '',
    `## ${tr('推荐调用流程', 'Recommended workflow')}`, '',
    tr('1. 使用 API Token 调用 `GET /api/v1/domains` 读取活动根域。', '1. Use an API Token to call `GET /api/v1/domains` and load active root domains.'),
    tr('2. 如需随机子域，由调用方生成标签并拼成 `随机标签.活动根域`。', '2. When a random subdomain is needed, generate labels client-side and build `random-label.active-root`.'),
    tr('3. 调用 `POST /api/v1/mailboxes` 创建邮箱，保存返回的 `mailbox.id` 与 `full_address`。', '3. Call `POST /api/v1/mailboxes`, then retain the returned `mailbox.id` and `full_address`.'),
    tr('4. 等待邮件时可订阅 `GET /api/v1/mailboxes/:id/events`，或轮询 `GET /api/v1/lookup/latest-code?address=...`。', '4. Subscribe to `GET /api/v1/mailboxes/:id/events` or poll `GET /api/v1/lookup/latest-code?address=...` while waiting for email.'),
    tr('5. 需要正文时读取邮件列表与邮件详情；业务完成后按策略删除邮箱。', '5. Read the email list and detail when the body is needed, then delete the mailbox according to the cleanup policy.'), '',
    `## ${tr('接口索引', 'Endpoint index')}`, '',
    '| Method | Path | Auth / scope | Summary |', '|---|---|---|---|', indexRows, '',
    `## ${tr('接口详情', 'Endpoint details')}`, '', details, '',
    `## ${tr('错误响应', 'Error response')}`, '', '```json', '{', '  "success": false,', '  "error": "mailbox not found",', '  "message": "mailbox not found",', '  "error_code": "not_found",', '  "status": 404,', '  "request_id": "00000000-0000-0000-0000-000000000000"', '}', '```', '',
    `## ${tr('限流响应头', 'Rate-limit headers')}`, '', '`RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`, `RateLimit-Policy`, `X-RateLimit-Daily-Remaining`, `X-RateLimit-Total-Remaining`, `Retry-After`',
  ].join('\n')
})

async function copyTextWithToast(value: string): Promise<void> {
  await copyText(value)
  toast(tr('已复制到剪贴板', 'Copied to clipboard'), 'success')
}

function chooseEndpoint(endpoint: Endpoint): void {
  selectedEndpoint.value = endpoint
  exampleMode.value = 'request'
}

watchEffect(() => setPageHeader(tr('开发文档', 'Developer docs'), tr('API 接入参考与调用示例', 'API reference and examples')))
</script>

<template>
  <div class="console-page api-docs-page-clean">
    <section class="section-card api-docs-export">
      <div>
        <h2>{{ PRODUCT_NAME }} API</h2>
        <p>{{ tr(`${apiEndpointCount} 个 API 接口 · ${publicEndpointCount} 个公开接口 · ${systemEndpointCount} 个健康检查`, `${apiEndpointCount} API · ${publicEndpointCount} Public · ${systemEndpointCount} Health`) }} · Base URL: <code>{{ origin }}</code></p>
      </div>
      <button class="btn api-docs-markdown-button" type="button" @click="copyTextWithToast(docsMarkdown)"><UiIcon name="copy" :size="17" />{{ tr('复制完整 Markdown', 'Copy complete Markdown') }}</button>
    </section>

    <div class="api-docs-reference">
      <label class="section-card api-docs-mobile-picker">
        <span>{{ tr('选择接口', 'Select endpoint') }} · {{ endpoints.length }}</span>
        <select v-model.number="selectedEndpointIndex" :aria-label="tr('选择接口', 'Select endpoint')">
          <optgroup v-for="group in endpointGroups" :key="group.key" :label="group.label">
            <option v-for="endpoint in group.endpoints" :key="`${endpoint.method}-${endpointPath(endpoint)}`" :value="endpoints.indexOf(endpoint)">{{ localize(endpoint.title) }} · {{ endpoint.method }}</option>
          </optgroup>
        </select>
      </label>
      <aside class="api-docs-endpoint-list section-card" :aria-label="tr('Endpoint 列表', 'Endpoint list')">
        <div class="api-docs-sidebar-label">{{ tr('接口', 'ENDPOINTS') }} · {{ endpoints.length }}</div>
        <label class="sr-only" for="api-docs-search">{{ tr('搜索接口', 'Search endpoints') }}</label>
        <input id="api-docs-search" v-model="docsQuery" class="form-input api-docs-search" :placeholder="tr('搜索路径、方法或名称', 'Search path, method, or name')" />
        <template v-for="group in endpointGroups" :key="group.key">
          <div class="api-docs-group-label">{{ group.label }} · {{ group.endpoints.length }}</div>
          <button v-for="endpoint in group.endpoints" :key="`${endpoint.method}-${endpointPath(endpoint)}`" type="button" :class="{ active: selectedEndpoint === endpoint }" :title="`${localize(endpoint.title)} · ${endpoint.method} ${endpointPath(endpoint)}`" @click="chooseEndpoint(endpoint)">
            <span class="endpoint-nav-title">{{ localize(endpoint.title) }}</span>
            <span class="method-mini" :class="endpoint.method.toLowerCase()">{{ endpoint.method }}</span>
          </button>
        </template>
      </aside>

      <div class="api-docs-main">
        <section class="section-card endpoint-detail-head">
          <div>
            <div class="endpoint-method-path">
              <span class="method-badge" :class="selectedEndpoint.method.toLowerCase()">{{ selectedEndpoint.method }}</span>
              <code>{{ endpointPath(selectedEndpoint) }}</code>
              <button class="doc-inline-copy" type="button" :aria-label="tr('复制 Endpoint', 'Copy Endpoint')" :title="tr('复制 Endpoint', 'Copy Endpoint')" @click="copyTextWithToast(`${origin}${endpointPath(selectedEndpoint)}`)"><UiIcon name="copy" :size="15" /></button>
            </div>
            <h2>{{ localize(selectedEndpoint.title) }}</h2>
            <p>{{ localize(selectedEndpoint.detail) }}</p>
          </div>
          <span class="endpoint-scope">{{ authLabel(selectedEndpoint) }} · {{ selectedEndpoint.responseStatus }}</span>
        </section>

        <section class="section-card endpoint-params-section">
          <div class="section-head compact-head">
            <div><div class="section-kicker">{{ tr('请求参数', 'REQUEST PARAMETERS') }}</div><h3 class="section-title">{{ tr('请求参数', 'Parameters') }}</h3></div>
            <span class="section-meta">{{ tr(`${selectedEndpoint.params.length} 个字段`, `${selectedEndpoint.params.length} fields`) }}</span>
          </div>
          <div v-if="selectedEndpoint.params.length" class="endpoint-params-list">
            <div v-for="item in selectedEndpoint.params" :key="`${item.location}-${item.name}`" class="endpoint-param-row">
              <code>{{ item.name }}</code><span class="param-type">{{ item.location }} · {{ item.type }}</span><span class="param-required" :class="{ required: item.required }">{{ item.required ? tr('必填', 'required') : tr('可选', 'optional') }}</span><p>{{ localize(item.description) }}</p>
            </div>
          </div>
          <p v-else class="endpoint-empty-params">{{ tr('无请求参数', 'No request parameters') }}</p>
        </section>

        <section class="section-card endpoint-example-section">
          <div class="section-head compact-head">
            <div><div class="section-kicker">{{ tr('示例', 'EXAMPLE') }}</div><h3 class="section-title">{{ exampleMode === 'request' ? tr('调用示例', 'Request example') : tr(`响应 · ${selectedEndpoint.responseStatus}`, `Response · ${selectedEndpoint.responseStatus}`) }}</h3></div>
            <div class="example-tools">
              <div class="example-tabs"><button type="button" :class="{ active: exampleMode === 'request' }" @click="exampleMode = 'request'">{{ tr('请求', 'Request') }}</button><button type="button" :class="{ active: exampleMode === 'response' }" @click="exampleMode = 'response'">{{ tr('响应', 'Response') }}</button></div>
              <select v-if="exampleMode === 'request'" v-model="language" class="example-language" :aria-label="tr('示例语言', 'Example language')"><option>cURL</option><option>HTTP</option><option>JavaScript</option></select>
              <button class="doc-inline-copy example-copy" type="button" :aria-label="tr('复制调用示例', 'Copy example')" :title="tr('复制调用示例', 'Copy example')" @click="copyTextWithToast(exampleCode)"><UiIcon name="copy" :size="16" /></button>
            </div>
          </div>
          <div class="code-block"><pre><code>{{ exampleCode }}</code></pre></div>
        </section>
      </div>
    </div>
  </div>
</template>
