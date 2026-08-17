<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import UiIcon from '../components/UiIcon.vue'
import { api } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import type { APIUsageReport, DomainHealth, IngressStats, IntegrationAuditEvent, MaintenancePreview, RuntimeObservability, SystemSummary } from '../types/api'
import { copyText, formatDate, formatMetric } from '../utils/format'

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const warning = ref('')
const summary = ref<SystemSummary | null>(null)
const health = ref<DomainHealth[]>([])
const ingress = ref<IngressStats | null>(null)
const runtime = ref<RuntimeObservability | null>(null)
const usage = ref<APIUsageReport | null>(null)
const audit = ref<IntegrationAuditEvent[]>([])
const auditQuery = ref('')
const auditStatus = ref('all')
const auditLimit = ref(50)
const maintenance = ref<MaintenancePreview | null>(null)
const cleanupAge = ref(240)
const usageHours = ref(24)
const usageBusy = ref(false)
let loadController: AbortController | undefined
let usageController: AbortController | undefined
let maintenanceController: AbortController | undefined
let liveTimer: number | undefined
let liveBusy = false

const activeHealth = computed(() => health.value.filter(item => item.is_active !== false))
const healthyCount = computed(() => activeHealth.value.filter(item => item.root_mx_ok && item.wildcard_mx_ok).length)
const maxUsage = computed(() => Math.max(1, ...(usage.value?.buckets || []).map(item => item.total_requests)))
const visibleAudit = computed(() => audit.value.filter(event => {
  const query = auditQuery.value.trim().toLowerCase()
  const matchesQuery = !query || [event.domain, event.integration, event.action, event.detail || ''].join(' ').toLowerCase().includes(query)
  return matchesQuery && (auditStatus.value === 'all' || event.status === auditStatus.value)
}))

function formatBytes(value = 0): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

async function load(): Promise<void> {
	loadController?.abort()
	const controller = new AbortController()
	loadController = controller
  loading.value = true
  error.value = ''
  warning.value = ''
  try {
    const results = await Promise.allSettled([
      api.systemSummary(controller.signal),
      api.domainHealth(controller.signal),
      api.ingressStats(controller.signal),
      api.runtimeObservability(controller.signal),
      api.apiUsage(usageHours.value, controller.signal),
      api.maintenancePreview(cleanupAge.value, controller.signal),
      api.integrationAudit(auditLimit.value, controller.signal),
    ])
	if (controller.signal.aborted) return
    if (results[0].status === 'rejected') throw results[0].reason
    summary.value = results[0].value
    health.value = results[1].status === 'fulfilled' ? results[1].value : []
    ingress.value = results[2].status === 'fulfilled' ? results[2].value : null
    runtime.value = results[3].status === 'fulfilled' ? results[3].value : null
    usage.value = results[4].status === 'fulfilled' ? results[4].value : null
    maintenance.value = results[5].status === 'fulfilled' ? results[5].value : null
    audit.value = results[6].status === 'fulfilled' ? results[6].value : []
    const failed = results.slice(1).filter(result => result.status === 'rejected').length
    if (failed) warning.value = tr(`${failed} 组辅助指标暂时不可用，可手动刷新重试。`, `${failed} secondary metric groups are unavailable. Refresh to retry.`)
  } catch (cause) {
	if (controller.signal.aborted) return
    error.value = cause instanceof Error ? cause.message : tr('运维状态加载失败', 'Unable to load operations status')
  } finally {
	if (loadController === controller) {
		loadController = undefined
		loading.value = false
	}
  }
}

async function refreshLiveMetrics(): Promise<void> {
  if (liveBusy || loading.value || document.visibilityState !== 'visible') return
  liveBusy = true
  try {
    const [system, ingressStats, runtimeReport] = await Promise.all([
      api.systemSummary(),
      api.ingressStats(),
      api.runtimeObservability(),
    ])
    summary.value = system
    ingress.value = ingressStats
    runtime.value = runtimeReport
  } catch (cause) {
    warning.value = cause instanceof Error ? cause.message : tr('实时指标刷新失败', 'Live metrics refresh failed')
  } finally {
    liveBusy = false
  }
}

function syncLivePolling(): void {
  if (liveTimer !== undefined) window.clearInterval(liveTimer)
  liveTimer = undefined
  if (document.visibilityState !== 'visible') return
  liveTimer = window.setInterval(() => void refreshLiveMetrics(), 15_000)
}

async function loadAPIUsage(): Promise<void> {
	usageController?.abort()
	const controller = new AbortController()
	usageController = controller
	usageBusy.value = true
	try {
		usage.value = await api.apiUsage(usageHours.value, controller.signal)
	} catch (cause) {
		if (controller.signal.aborted) return
		toast(cause instanceof Error ? cause.message : tr('API 使用数据加载失败', 'Unable to load API usage'), 'error')
	} finally {
		if (usageController === controller) usageController = undefined
		usageBusy.value = false
	}
}

async function loadMaintenancePreview(): Promise<void> {
	maintenanceController?.abort()
	const controller = new AbortController()
	maintenanceController = controller
	try {
		maintenance.value = await api.maintenancePreview(cleanupAge.value, controller.signal)
	} catch (cause) {
		if (controller.signal.aborted) return
		toast(cause instanceof Error ? cause.message : tr('维护预估失败', 'Maintenance preview failed'), 'error')
	} finally {
		if (maintenanceController === controller) maintenanceController = undefined
	}
}

async function copyDiagnostics(): Promise<void> {
  const report = {
    generated_at: new Date().toISOString(),
    system: summary.value,
    lmtp_ingress: ingress.value,
    domain_health: health.value,
    api_usage: usage.value?.summary || null,
  }
  await copyText(JSON.stringify(report, null, 2))
  toast(tr('诊断快照已复制', 'Diagnostic snapshot copied'), 'success')
}

async function refreshDomains(): Promise<void> {
  busy.value = true
  try {
    const result = await api.refreshDomainHealth()
    health.value = result.data || []
    summary.value = await api.systemSummary()
    try {
      ingress.value = await api.ingressStats()
    } catch (cause) {
      warning.value = cause instanceof Error ? cause.message : tr('LMTP 指标刷新失败', 'LMTP metrics refresh failed')
    }
    toast(tr('域名健康快照已刷新', 'Domain health snapshot refreshed'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('域名检查失败', 'Domain check failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function cleanupEmails(): Promise<void> {
  busy.value = true
  try {
    const result = await api.cleanupEmails({ older_than_minutes: Number(cleanupAge.value || 240) })
    toast(tr(`已清理 ${result.deleted} 封邮件`, `${result.deleted} emails deleted`), 'success')
    await Promise.all([load(), loadMaintenancePreview()])
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('清理失败', 'Cleanup failed'), 'error')
  } finally {
    busy.value = false
  }
}

watchEffect(() => setPageHeader(tr('运维中心', 'Operations'), tr('收信链路、API 观测和数据生命周期', 'Inbound delivery, API observability, and data lifecycle'), [
  { label: tr('复制诊断快照', 'Copy diagnostics'), tone: 'ghost', glyph: '□', run: copyDiagnostics },
]))
onMounted(() => {
  document.addEventListener('visibilitychange', syncLivePolling)
  syncLivePolling()
  void load()
})
onBeforeUnmount(() => {
	loadController?.abort()
	usageController?.abort()
	maintenanceController?.abort()
  document.removeEventListener('visibilitychange', syncLivePolling)
  if (liveTimer !== undefined) window.clearInterval(liveTimer)
})
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('运维状态加载失败', 'Unable to load operations status')" :description="error"><button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></EmptyState>
  <div v-else class="console-page operations-page">
    <div v-if="warning" class="operations-data-warning" role="status"><UiIcon name="alert" :size="15" /><span>{{ warning }}</span><button class="btn btn-ghost btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></div>
    <section class="operations-status-grid">
      <article class="operation-status-card"><span>{{ tr('数据库', 'Database') }}</span><strong class="status-chip" :class="summary?.db_ok ? 'success' : 'danger'">{{ summary?.db_ok ? tr('正常', 'Healthy') : tr('异常', 'Down') }}</strong><small>PostgreSQL</small></article>
      <article class="operation-status-card"><span>Redis</span><strong class="status-chip" :class="summary?.redis_ok ? 'success' : 'danger'">{{ summary?.redis_ok ? tr('正常', 'Healthy') : tr('异常', 'Down') }}</strong><small>{{ tr('限流与缓存', 'Rate limits and cache') }}</small></article>
      <article class="operation-status-card"><span>{{ tr('域名健康', 'Domain health') }}</span><strong class="status-chip" :class="healthyCount === activeHealth.length && activeHealth.length > 0 ? 'success' : activeHealth.length ? 'warn' : 'info'">{{ activeHealth.length ? `${healthyCount} / ${activeHealth.length}` : tr('暂无活动域名', 'No active domains') }}</strong><small>{{ tr('Root + Wildcard MX · 仅统计启用域名', 'Root + Wildcard MX · active only') }}</small></article>
      <article class="operation-status-card"><span>{{ tr('SMTP 主机', 'SMTP host') }}</span><strong class="status-chip" :class="summary?.smtp_reachable ? 'success' : summary?.smtp_configured ? 'warn' : 'danger'">{{ summary?.smtp_reachable ? tr('可达', 'Reachable') : summary?.smtp_configured ? tr('已配置', 'Configured') : tr('未配置', 'Not configured') }}</strong><small>{{ summary?.smtp_hostname || summary?.smtp_server_ip || tr('未配置主机', 'No host configured') }} · {{ summary?.smtp_source === 'settings' ? tr('后台设置', 'Owner settings') : summary?.smtp_source === 'mixed' ? tr('设置 + 环境变量', 'Settings + environment') : summary?.smtp_source === 'environment' ? tr('环境变量', 'Environment') : tr('未知来源', 'Unknown source') }}</small></article>
      <article class="operation-status-card"><span>LMTP ingress</span><strong class="status-chip" :class="summary?.lmtp_running ? 'success' : 'danger'">{{ summary?.lmtp_running ? tr('运行中', 'Running') : tr('未运行', 'Stopped') }}</strong><small>{{ summary?.lmtp_addr || tr('未监听', 'Not listening') }} · Go LMTP</small></article>
    </section>

    <section class="operations-action-grid">
      <article class="section-card operations-action-card"><div class="section-kicker">{{ tr('域名检查', 'DOMAIN CHECK') }}</div><h3 class="section-title">{{ tr('刷新域名健康快照', 'Refresh domain health') }}</h3><p class="section-desc">{{ tr('检查根域与通配子域 MX，并保存最新结果。', 'Check root and wildcard MX records and save the latest result.') }}</p><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="refreshDomains">{{ busy ? tr('处理中…', 'Checking…') : tr('立即检查', 'Run check') }}</button></article>
      <article class="section-card operations-action-card"><div class="section-kicker">{{ tr('数据保留', 'RETENTION') }}</div><h3 class="section-title">{{ tr('清理历史邮件', 'Delete old email') }}</h3><p class="section-desc">{{ tr('删除指定时间之前的邮件，不影响邮箱和域名。', 'Delete email older than the selected age without affecting mailboxes or domains.') }}</p><div v-if="maintenance" class="maintenance-preview"><span>{{ tr('预计删除', 'Estimated impact') }}</span><strong>{{ formatMetric(maintenance.old_emails) }} {{ tr('封邮件', 'emails') }}</strong><small>{{ formatBytes(maintenance.old_email_bytes) }} · {{ tr(`${formatMetric(maintenance.expired_mailboxes)} 个过期邮箱`, `${formatMetric(maintenance.expired_mailboxes)} expired mailboxes`) }}</small></div><div class="operations-cleanup-row"><input v-model.number="cleanupAge" class="form-input" type="number" min="1" @change="loadMaintenancePreview" /><span>{{ tr('分钟前', 'minutes old') }}</span><button class="btn btn-danger btn-sm" type="button" :disabled="busy" @click="cleanupEmails">{{ tr('执行清理', 'Delete email') }}</button></div></article>
    </section>

    <section v-if="ingress" class="section-card operations-ingress-card">
      <div class="section-head compact-head"><div><div class="section-kicker">LMTP INGRESS</div><h3 class="section-title">{{ tr('收信入口实时指标', 'Inbound delivery metrics') }}</h3><p class="section-desc">{{ tr('队列、worker 与投递结果来自 Go LMTP ingress。', 'Queue, worker, and delivery metrics from Go LMTP ingress.') }}</p></div><div class="section-meta">{{ ingress.addr }}</div></div>
      <div class="operations-ingress-grid">
        <article><span>{{ tr('队列深度', 'Queue depth') }}</span><strong>{{ ingress.queue_depth }} / {{ ingress.queue_size }}</strong><small>In-flight {{ ingress.in_flight }}</small></article>
        <article><span>{{ tr('活动连接', 'Active connections') }}</span><strong>{{ ingress.active_connections }}</strong><small>{{ tr(`累计 ${ingress.connections_accepted}`, `${ingress.connections_accepted} accepted`) }}</small></article>
        <article><span>Workers</span><strong>{{ ingress.active_workers }} / {{ ingress.workers }}</strong><small>{{ tr('当前活跃', 'Active now') }}</small></article>
        <article><span>{{ tr('已投递', 'Delivered') }}</span><strong>{{ ingress.jobs_delivered }}</strong><small>{{ tr(`提交 ${ingress.jobs_submitted}`, `${ingress.jobs_submitted} submitted`) }}</small></article>
        <article><span>{{ tr('临时失败', 'Temporary failures') }}</span><strong>{{ ingress.jobs_temp_failed }}</strong><small>{{ tr(`超时 ${ingress.delivery_timeouts}`, `${ingress.delivery_timeouts} timeouts`) }}</small></article>
        <article><span>{{ tr('平均 DB', 'Average DB') }}</span><strong>{{ ingress.avg_db_ms.toFixed(2) }} ms</strong><small>{{ tr(`解析 ${ingress.avg_parse_ms.toFixed(2)} ms`, `Parse ${ingress.avg_parse_ms.toFixed(2)} ms`) }}</small></article>
      </div>
    </section>

    <section v-if="runtime" class="section-card runtime-observability-card">
      <div class="section-head compact-head"><div><div class="section-kicker">RUNTIME HEALTH</div><h3 class="section-title">{{ tr('运行时资源与队列', 'Runtime resources and queues') }}</h3><p class="section-desc">{{ tr('连接池、观测队列与收信高水位，帮助识别空转和饱和。', 'Pool pressure, telemetry backlog, and ingress high-water marks.') }}</p></div></div>
      <div class="operations-ingress-grid runtime-metrics-grid">
        <article><span>PostgreSQL pool</span><strong>{{ runtime.postgres_pool.acquired_conns }} / {{ runtime.postgres_pool.max_conns }}</strong><small>{{ tr(`空闲 ${runtime.postgres_pool.idle_conns} · 等待 ${runtime.postgres_pool.empty_acquire_count}`, `${runtime.postgres_pool.idle_conns} idle · ${runtime.postgres_pool.empty_acquire_count} waits`) }}</small></article>
        <article><span>Redis pool</span><strong>{{ runtime.redis_pool.total_conns }}</strong><small>{{ tr(`空闲 ${runtime.redis_pool.idle_conns} · 命中 ${runtime.redis_pool.hits}`, `${runtime.redis_pool.idle_conns} idle · ${runtime.redis_pool.hits} hits`) }}</small></article>
        <article><span>{{ tr('缓存命中', 'Cache hits') }}</span><strong>{{ runtime.cache.token_hits + runtime.cache.active_domain_hits }}</strong><small>{{ tr(`Token ${runtime.cache.token_hits} · 域名 ${runtime.cache.active_domain_hits}`, `Token ${runtime.cache.token_hits} · domains ${runtime.cache.active_domain_hits}`) }}</small></article>
        <article><span>{{ tr('API 观测队列', 'API telemetry queue') }}</span><strong>{{ runtime.api_observability.queue_depth }} / {{ runtime.api_observability.queue_capacity }}</strong><small>{{ tr(`待写 ${runtime.api_observability.pending_depth} · 峰值 ${runtime.api_observability.queue_high_water} · 丢弃 ${runtime.api_observability.dropped + runtime.api_observability.failed_events}`, `${runtime.api_observability.pending_depth} pending · peak ${runtime.api_observability.queue_high_water} · dropped ${runtime.api_observability.dropped + runtime.api_observability.failed_events}`) }}</small></article>
        <article><span>{{ tr('LMTP 在途峰值', 'LMTP in-flight peak') }}</span><strong>{{ runtime.lmtp.in_flight_high_water }}</strong><small>{{ tr(`队列峰值 ${runtime.lmtp.queue_high_water} · 满载拒绝 ${runtime.lmtp.queue_full}`, `Queue peak ${runtime.lmtp.queue_high_water} · full ${runtime.lmtp.queue_full}`) }}</small></article>
      </div>
    </section>

    <section class="section-card api-observability-card">
      <div class="section-head compact-head">
        <div><div class="section-kicker">{{ tr('API 观测', 'API OBSERVABILITY') }}</div><h3 class="section-title">{{ tr('API 调用概览', 'API request overview') }}</h3><p class="section-desc">{{ tr('仅记录路由、状态码与耗时，不记录凭据和请求内容。', 'Routes, status codes, and latency only. Credentials and payloads are never recorded.') }}</p></div>
        <div class="operations-range-control"><select v-model.number="usageHours" class="form-input" :aria-label="tr('观测时间范围', 'Observation window')" @change="loadAPIUsage"><option :value="6">6h</option><option :value="24">24h</option><option :value="168">7d</option><option :value="336">14d</option></select><button class="icon-btn" type="button" :disabled="usageBusy" :aria-label="tr('刷新 API 观测', 'Refresh API usage')" :title="tr('刷新 API 观测', 'Refresh API usage')" @click="loadAPIUsage"><UiIcon name="refresh" :size="15" /></button></div>
      </div>
      <div v-if="usage" class="api-observability-layout">
        <div class="api-observability-metrics">
          <article><span>{{ tr('调用总量', 'Requests') }}</span><strong>{{ formatMetric(usage.summary.total_requests) }}</strong></article>
          <article><span>{{ tr('错误请求', 'Errors') }}</span><strong>{{ formatMetric(usage.summary.error_requests) }}</strong></article>
          <article><span>{{ tr('平均延迟', 'Average latency') }}</span><strong>{{ usage.summary.avg_latency_ms.toFixed(1) }} ms</strong></article>
          <article><span>P95</span><strong>{{ usage.summary.p95_latency_ms.toFixed(1) }} ms</strong></article>
        </div>
        <div v-if="usage.buckets.length" class="api-usage-chart" :aria-label="tr('API 调用趋势', 'API request trend')" role="img">
          <span v-for="bucket in usage.buckets" :key="bucket.hour" :style="{ height: `${Math.max(4, bucket.total_requests / maxUsage * 100)}%` }" :title="`${formatDate(bucket.hour)} · ${bucket.total_requests}`"></span>
        </div>
        <div v-if="usage.routes.length" class="api-route-list">
          <div class="api-route-row api-route-head"><span>{{ tr('路由', 'Route') }}</span><span>{{ tr('调用', 'Requests') }}</span><span>{{ tr('错误', 'Errors') }}</span><span>{{ tr('平均延迟', 'Avg latency') }}</span></div>
          <div v-for="route in usage.routes" :key="`${route.method}-${route.route}`" class="api-route-row"><code><b>{{ route.method }}</b> {{ route.route }}</code><span>{{ formatMetric(route.total_requests) }}</span><span :class="{ 'text-danger': route.error_requests > 0 }">{{ formatMetric(route.error_requests) }}</span><span>{{ route.avg_latency_ms.toFixed(1) }} ms</span></div>
        </div>
        <EmptyState v-else icon="chart" :title="tr('暂无 API 调用', 'No API requests')" :description="tr('使用 API Token 调用 /api/v1 后，观测数据会显示在这里。', 'Requests made with an API Token will appear here.')" />
      </div>
    </section>

    <section class="section-card">
      <div class="section-head compact-head"><div><div class="section-kicker">{{ tr('集成审计', 'INTEGRATION AUDIT') }}</div><h3 class="section-title">{{ tr('最近的 DNS 操作', 'Recent DNS operations') }}</h3><p class="section-desc">{{ tr('只记录集成、动作、域名、结果和时间，不记录 Cloudflare Token 或响应内容。', 'Records integration, action, domain, result, and time only. Cloudflare Tokens and provider responses are never stored.') }}</p></div><div class="section-meta">{{ tr(`${audit.length} 条`, `${audit.length} events`) }}</div></div>
      <div class="audit-toolbar"><label class="sr-only" for="audit-search">{{ tr('筛选审计', 'Filter audit') }}</label><input id="audit-search" v-model="auditQuery" class="form-input" :placeholder="tr('搜索域名、动作或集成', 'Search domain, action, or integration')"><label class="sr-only" for="audit-status">{{ tr('审计状态', 'Audit status') }}</label><select id="audit-status" v-model="auditStatus" class="form-input"><option value="all">{{ tr('全部状态', 'All statuses') }}</option><option value="success">{{ tr('成功', 'Success') }}</option><option value="failed">{{ tr('失败', 'Failed') }}</option><option value="rolled_back">{{ tr('已回滚', 'Rolled back') }}</option></select><button v-if="audit.length >= auditLimit" class="btn btn-ghost btn-sm" type="button" @click="auditLimit += 50; void load()">{{ tr('加载更多', 'Load more') }}</button></div>
      <div v-if="visibleAudit.length" class="operations-health-list">
        <article v-for="event in visibleAudit" :key="event.id" class="operations-health-row">
          <div><strong>{{ event.domain || tr('全局配置', 'Global configuration') }}</strong><small>{{ event.integration }} · {{ event.action }} · {{ event.detail || tr('无附加信息', 'No additional detail') }}</small></div>
          <span class="status-chip" :class="event.status === 'success' ? 'success' : event.status === 'rolled_back' ? 'warn' : 'danger'">{{ event.status === 'success' ? tr('成功', 'Success') : event.status === 'rolled_back' ? tr('已回滚', 'Rolled back') : tr('失败', 'Failed') }}</span>
          <time>{{ formatDate(event.created_at) }}</time>
        </article>
      </div>
      <EmptyState v-else icon="activity" :title="audit.length ? tr('没有匹配的审计事件', 'No matching audit events') : tr('暂无集成操作', 'No integration operations')" :description="tr('调整筛选条件或执行一次 DNS/Cloudflare 操作。', 'Adjust filters or run a DNS/Cloudflare operation.')" />
    </section>

    <section class="section-card">
      <div class="section-head compact-head"><div><div class="section-kicker">{{ tr('健康快照', 'HEALTH SNAPSHOT') }}</div><h3 class="section-title">{{ tr('域名健康明细', 'Domain health detail') }}</h3><p class="section-desc">{{ tr('最近检查：', 'Last checked: ') }}{{ formatDate(summary?.last_health_check_at) }}</p></div><div class="section-meta">{{ tr(`${health.length} 个域名`, `${health.length} domains`) }}</div></div>
      <div v-if="health.length" class="operations-health-list"><article v-for="item in health" :key="item.domain" class="operations-health-row"><div><strong>{{ item.domain }}</strong><small>{{ item.status || 'unknown' }}</small></div><span class="status-chip" :class="item.root_mx_ok ? 'success' : 'danger'">Root {{ item.root_mx_ok ? tr('正常', 'Healthy') : tr('异常', 'Failed') }}</span><span class="status-chip" :class="item.wildcard_mx_ok ? 'success' : 'warn'">Wildcard {{ item.wildcard_mx_ok ? tr('正常', 'Healthy') : tr('异常', 'Failed') }}</span><time>{{ formatDate(item.checked_at) }}</time></article></div>
      <EmptyState v-else icon="domains" :title="tr('暂无健康快照', 'No health snapshot')" :description="tr('添加域名并执行一次健康检查后，结果会显示在这里。', 'Add a domain and run a health check to create a snapshot.')" />
    </section>
  </div>
</template>
