<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch, watchEffect } from 'vue'
import AppModal from '../components/AppModal.vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import UiIcon from '../components/UiIcon.vue'
import { api } from '../services/api'
import { loadPublicSettings } from '../stores/auth'
import { localizeBackendText, tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import { setPageHeader } from '../stores/ui'
import type { DomainDonation, DonationRewardEvent, DonationRewardToken, DonationSummary } from '../types/api'
import { formatDate, formatMetric, timeAgo } from '../utils/format'

type DonationView = 'tokens' | 'domains' | 'policy'

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const items = ref<DomainDonation[]>([])
const rewardTokens = ref<DonationRewardToken[]>([])
const rewardEvents = ref<DonationRewardEvent[]>([])
const summary = ref<DonationSummary | null>(null)
const settings = reactive<Record<string, string>>({})
const activeView = ref<DonationView>('tokens')
const adjusting = ref<DonationRewardToken | null>(null)
const ledgerTokenId = ref('')
const domainQuery = ref('')
const domainStatus = ref('all')
const domainPage = ref(1)
const domainPageSize = 20
const adjustment = reactive({ total_delta: 0, daily_delta: 0, rpm_delta: 0, note: '' })

const viewOrder: DonationView[] = ['tokens', 'domains', 'policy']
const tabs = computed(() => [
  { id: 'tokens' as const, label: tr('奖励密钥', 'Reward Tokens') },
  { id: 'domains' as const, label: tr('域名记录', 'Domain records') },
  { id: 'policy' as const, label: tr('奖励规则', 'Reward policy') },
])
const policyGroups = computed(() => [
  {
    key: 'grant',
    kicker: tr('域名池', 'DOMAIN POOL'),
    title: tr('基础奖励', 'Base grant'),
    fields: [
      { key: 'donation_reward_total_request_limit', label: tr('总额度增量', 'Total quota grant'), min: 1 },
      { key: 'donation_reward_daily_request_limit', label: tr('每日额度增量', 'Daily quota grant'), min: 0 },
      { key: 'donation_reward_rate_limit_per_minute', label: tr('RPM 增量', 'RPM grant'), min: 1 },
    ],
  },
  {
    key: 'pool',
    kicker: 'TOKEN POOL',
    title: tr('奖励池限制', 'Reward pool limits'),
    fields: [
      { key: 'donation_token_rate_limit_cap', label: tr('RPM 汇总上限', 'Aggregate RPM cap'), min: 0 },
      { key: 'donation_max_domains_per_token', label: tr('可绑定域名数', 'Domain capacity'), min: 1 },
    ],
  },
  {
    key: 'verification',
    kicker: 'DNS',
    title: tr('复检策略', 'Verification policy'),
    fields: [
      { key: 'donation_dns_failure_tolerance', label: tr('临时错误容忍次数', 'Transient failure tolerance'), min: 1 },
      { key: 'donation_recheck_minutes', label: tr('复检周期（分钟）', 'Recheck interval (minutes)'), min: 1 },
    ],
  },
])
const policyFields = computed(() => policyGroups.value.flatMap(group => group.fields))
const visibleEvents = computed(() => ledgerTokenId.value
  ? rewardEvents.value.filter(event => event.token_id === ledgerTokenId.value)
  : rewardEvents.value)
const filteredDonations = computed(() => items.value.filter(item => {
  const query = domainQuery.value.trim().toLowerCase()
  const matchesQuery = !query || item.domain.toLowerCase().includes(query) || item.token_prefix.toLowerCase().includes(query)
  const matchesStatus = domainStatus.value === 'all' || (domainStatus.value === 'active' ? item.reward_active : item.status === domainStatus.value)
  return matchesQuery && matchesStatus
}))
const domainPages = computed(() => Math.max(1, Math.ceil(filteredDonations.value.length / domainPageSize)))
const visibleDonations = computed(() => filteredDonations.value.slice((domainPage.value - 1) * domainPageSize, domainPage.value * domainPageSize))

function policyPayload(): Record<string, string> {
  const payload: Record<string, string> = { donation_enabled: settings.donation_enabled === 'false' ? 'false' : 'true' }
  policyFields.value.forEach(field => { payload[field.key] = String(settings[field.key] ?? field.min) })
  return payload
}

function statusLabel(item: DomainDonation): string {
  if (item.status === 'active' && item.reward_active) return tr('有效', 'Active')
  if (item.status === 'pending') return tr('待验证', 'Pending')
  if (item.status === 'revoked') return tr('已撤销', 'Revoked')
  return tr('已失效', 'Inactive')
}

function statusTone(item: DomainDonation): string {
  if (item.status === 'active' && item.reward_active) return 'success'
  if (item.status === 'pending') return 'warn'
  return 'danger'
}

function tokenStatusLabel(token: DonationRewardToken): string {
  if (token.status === 'active') return tr('可用', 'Active')
  if (token.status === 'revoked') return tr('已撤销', 'Revoked')
  if (token.status === 'expired') return tr('已过期', 'Expired')
  return tr('待激活', 'Inactive')
}

function tokenStatusTone(token: DonationRewardToken): string {
  if (token.status === 'active') return 'success'
  if (token.status === 'inactive') return 'warn'
  return 'danger'
}

function tokenUsage(token: DonationRewardToken): number {
  if (token.total_request_limit <= 0) return 0
  return Math.min(100, Math.max(0, token.request_count_total / token.total_request_limit * 100))
}

function tokenDomains(tokenId: string): DomainDonation[] {
  return items.value.filter(item => item.token_id === tokenId)
}

function eventLabel(event: DonationRewardEvent): string {
  if (event.event_type === 'grant') return tr('奖励生效', 'Grant activated')
  if (event.event_type === 'revoke') return tr('奖励收回', 'Grant revoked')
  if (event.event_type === 'manual_adjust') return tr('人工调整', 'Manual adjustment')
  if (event.event_type === 'policy_update') return tr('规则更新', 'Policy update')
  return event.event_type
}

function eventTone(event: DonationRewardEvent): string {
  if (event.event_type === 'grant') return 'success'
  if (event.event_type === 'revoke') return 'danger'
  if (event.event_type === 'manual_adjust') return 'info'
  return 'neutral'
}

function signedMetric(value: number): string {
  if (!value) return '0'
  return `${value > 0 ? '+' : ''}${formatMetric(value)}`
}

function selectView(view: DonationView): void {
  activeView.value = view
  requestAnimationFrame(() => document.getElementById(`donation-tab-${view}`)?.focus())
}

function handleTabKey(event: KeyboardEvent, view: DonationView): void {
  const current = viewOrder.indexOf(view)
  if (event.key === 'Home') selectView(viewOrder[0])
  else if (event.key === 'End') selectView(viewOrder[viewOrder.length - 1])
  else if (event.key === 'ArrowRight') selectView(viewOrder[(current + 1) % viewOrder.length])
  else if (event.key === 'ArrowLeft') selectView(viewOrder[(current - 1 + viewOrder.length) % viewOrder.length])
  else return
  event.preventDefault()
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [donations, currentSettings] = await Promise.all([api.donations(), api.settings()])
    items.value = donations.data || []
    rewardTokens.value = donations.tokens || []
    rewardEvents.value = donations.events || []
    summary.value = donations.summary
    Object.assign(settings, currentSettings)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('捐赠计划加载失败', 'Unable to load donations')
  } finally {
    loading.value = false
  }
}

async function savePolicy(): Promise<void> {
  busy.value = true
  try {
    await api.saveSettings(policyPayload())
    toast(tr('奖励规则已保存', 'Reward policy saved'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('奖励规则保存失败', 'Unable to save reward policy'), 'error')
  } finally {
    busy.value = false
  }
}

async function toggleDonation(): Promise<void> {
  const enabled = settings.donation_enabled !== 'false'
  busy.value = true
  try {
    await api.saveSettings({ donation_enabled: enabled ? 'true' : 'false' })
    await loadPublicSettings()
    toast(enabled ? tr('捐赠页面已开启', 'Donation page enabled') : tr('捐赠页面已关闭', 'Donation page disabled'), 'success')
  } catch (cause) {
    settings.donation_enabled = enabled ? 'false' : 'true'
    toast(cause instanceof Error ? cause.message : tr('捐赠页面状态更新失败', 'Unable to update donation page'), 'error')
  } finally {
    busy.value = false
  }
}

async function applyPolicy(): Promise<void> {
  if (!await askConfirm({
    title: tr('更新全部奖励池', 'Update all reward pools'),
    message: tr('新规则会应用到全部未撤销奖励，人工调整继续保留。', 'The new policy will apply to every non-revoked grant; manual adjustments remain.'),
    confirmLabel: tr('保存并更新', 'Save and update'),
  })) return
  busy.value = true
  try {
    await api.saveSettings(policyPayload())
    await api.applyDonationPolicy()
    toast(tr('现有奖励池已更新', 'Existing reward pools updated'), 'success')
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('奖励规则应用失败', 'Unable to apply reward policy'), 'error')
  } finally {
    busy.value = false
  }
}

async function recheck(item: DomainDonation): Promise<void> {
  busy.value = true
  try {
    await api.recheckDonation(item.id)
    toast(tr('验证已完成', 'Verification completed'), 'success')
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('验证失败', 'Verification failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function revoke(item: DomainDonation): Promise<void> {
  if (!await askConfirm({
    title: tr('撤销域名奖励', 'Revoke domain grant'),
    message: tr(`${item.domain} 对应的奖励额度会立即收回。`, `The quota granted by ${item.domain} will be revoked immediately.`),
    confirmLabel: tr('撤销奖励', 'Revoke grant'),
    danger: true,
  })) return
  busy.value = true
  try {
    await api.revokeDonation(item.id, tr('管理员撤销', 'Revoked by owner'))
    toast(tr('奖励已撤销', 'Grant revoked'), 'success')
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('撤销失败', 'Revoke failed'), 'error')
  } finally {
    busy.value = false
  }
}

function openAdjustment(token: DonationRewardToken): void {
  adjusting.value = token
  Object.assign(adjustment, { total_delta: 0, daily_delta: 0, rpm_delta: 0, note: '' })
}

async function submitAdjustment(): Promise<void> {
  if (!adjusting.value) return
  if (!adjustment.total_delta && !adjustment.daily_delta && !adjustment.rpm_delta) {
    toast(tr('至少填写一项额度变化', 'Enter at least one quota adjustment'), 'warn')
    return
  }
  busy.value = true
  try {
    await api.adjustDonationToken(adjusting.value.id, { ...adjustment })
    adjusting.value = null
    toast(tr('奖励池额度已更新', 'Reward pool quota updated'), 'success')
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('额度更新失败', 'Quota update failed'), 'error')
  } finally {
    busy.value = false
  }
}

watchEffect(() => setPageHeader(tr('捐赠计划', 'Donations'), tr('域名池、奖励密钥与额度流水', 'Domain pool, reward Tokens, and quota ledger'), [
  { label: tr('刷新', 'Refresh'), tone: 'ghost', glyph: '↻', run: load },
]))
onMounted(() => void load())
watch([domainQuery, domainStatus], () => { domainPage.value = 1 })
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="alert" :title="tr('捐赠计划加载失败', 'Unable to load donations')" :description="error"><button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></EmptyState>
  <div v-else class="page-stack donation-admin-page">
    <nav class="donation-admin-tabs" role="tablist" :aria-label="tr('捐赠计划视图', 'Donation views')">
      <button
        v-for="tab in tabs"
        :id="`donation-tab-${tab.id}`"
        :key="tab.id"
        type="button"
        role="tab"
        :class="{ active: activeView === tab.id }"
        :aria-selected="activeView === tab.id"
        :aria-controls="`donation-panel-${tab.id}`"
        :tabindex="activeView === tab.id ? 0 : -1"
        @click="activeView = tab.id"
        @keydown="handleTabKey($event, tab.id)"
      >{{ tab.label }}</button>
    </nav>

    <section v-if="activeView === 'tokens'" id="donation-panel-tokens" role="tabpanel" aria-labelledby="donation-tab-tokens" class="donation-view-stack">
      <div class="donation-summary-strip">
        <article><span>{{ tr('奖励密钥', 'Reward Tokens') }}</span><strong>{{ formatMetric(summary?.reward_token_total) }}</strong><small>{{ tr(`${formatMetric(summary?.active_donations)} 个有效域名`, `${formatMetric(summary?.active_donations)} active domains`) }}</small></article>
        <article><span>{{ tr('域名池', 'Domain pool') }}</span><strong>{{ formatMetric(summary?.total_donations) }}</strong><small>{{ tr(`${formatMetric(summary?.pending_donations)} 个待验证`, `${formatMetric(summary?.pending_donations)} pending`) }}</small></article>
        <article><span>{{ tr('有效总额度', 'Effective quota') }}</span><strong>{{ formatMetric(summary?.effective_total_quota) }}</strong><small>{{ tr('全部奖励池', 'Across all reward pools') }}</small></article>
        <article><span>{{ tr('累计调用', 'Requests used') }}</span><strong>{{ formatMetric(summary?.consumed_total_quota) }}</strong><small>{{ tr('奖励密钥调用量', 'Reward Token usage') }}</small></article>
      </div>

      <div v-if="rewardTokens.length" class="donation-token-grid">
        <article v-for="token in rewardTokens" :key="token.id" class="donation-token-card">
          <header class="donation-token-card-head">
            <div><span>{{ tr('奖励 API Token', 'Reward API Token') }}</span><code>{{ token.token_prefix }}••••••••</code></div>
            <span class="status-chip" :class="tokenStatusTone(token)">{{ tokenStatusLabel(token) }}</span>
          </header>
          <div class="donation-token-domain-line">
            <span v-for="domain in tokenDomains(token.id)" :key="domain.id" :class="{ active: domain.reward_active }">{{ domain.domain }}</span>
          </div>
          <div class="donation-token-metrics">
            <div><span>{{ tr('绑定域名', 'Domains') }}</span><strong>{{ token.domain_count }}</strong><small>{{ tr(`${token.active_domain_count} 个有效`, `${token.active_domain_count} active`) }}</small></div>
            <div><span>RPM</span><strong>{{ formatMetric(token.rate_limit_per_minute) }}</strong><small>{{ tr('当前汇总', 'Current pool') }}</small></div>
            <div><span>{{ tr('每日额度', 'Daily quota') }}</span><strong>{{ formatMetric(token.daily_request_limit) }}</strong><small>{{ tr('当前汇总', 'Current pool') }}</small></div>
            <div><span>{{ tr('剩余总额度', 'Remaining') }}</span><strong>{{ formatMetric(token.remaining_total) }}</strong><small>/ {{ formatMetric(token.total_request_limit) }}</small></div>
          </div>
          <div class="donation-token-usage" :aria-label="tr('总额度使用进度', 'Total quota usage')"><span :style="{ width: `${tokenUsage(token)}%` }"></span></div>
          <footer class="donation-token-card-foot">
            <small>{{ token.last_used_at ? tr(`最近调用 ${timeAgo(token.last_used_at)}`, `Last used ${timeAgo(token.last_used_at)}`) : tr(`签发于 ${formatDate(token.created_at)}`, `Issued ${formatDate(token.created_at)}`) }}</small>
            <div>
              <button class="btn btn-ghost btn-sm" type="button" @click="ledgerTokenId = token.id"><UiIcon name="activity" :size="14" />{{ tr('查看流水', 'View ledger') }}</button>
              <button class="icon-btn" type="button" :aria-label="tr('调整奖励池额度', 'Adjust reward pool quota')" :title="tr('调整奖励池额度', 'Adjust reward pool quota')" @click="openAdjustment(token)"><UiIcon name="edit" :size="15" /></button>
            </div>
          </footer>
        </article>
      </div>
      <EmptyState v-else icon="key" :title="tr('尚未签发奖励密钥', 'No reward Tokens issued')" description="" />

      <section class="donation-ledger-section">
        <div class="section-head compact-head">
          <div><div class="section-kicker">REWARD LEDGER</div><h3 class="section-title">{{ tr('额度流水', 'Quota ledger') }}</h3></div>
          <button v-if="ledgerTokenId" class="btn btn-ghost btn-sm" type="button" @click="ledgerTokenId = ''">{{ tr('全部记录', 'All events') }}</button>
        </div>
        <div v-if="visibleEvents.length" class="donation-ledger-list">
          <article v-for="event in visibleEvents" :key="event.id" class="donation-ledger-row">
            <span class="donation-event-mark" :class="eventTone(event)"><UiIcon :name="event.event_type === 'revoke' ? 'pause' : event.event_type === 'manual_adjust' ? 'edit' : event.event_type === 'policy_update' ? 'refresh' : 'check'" :size="14" /></span>
            <div class="donation-event-main"><strong>{{ eventLabel(event) }}</strong><small>{{ event.domain || `${event.token_prefix}••••••••` }}<template v-if="event.note"> · {{ event.note }}</template></small></div>
            <div class="donation-event-deltas"><strong>{{ signedMetric(event.total_delta) }}</strong><small>{{ signedMetric(event.daily_delta) }}/{{ tr('日', 'day') }} · {{ signedMetric(event.rpm_delta) }} RPM</small></div>
            <time>{{ formatDate(event.created_at) }}</time>
          </article>
        </div>
        <EmptyState v-else icon="clipboard" :title="tr('暂无额度流水', 'No quota events')" description="" />
      </section>
    </section>

    <section v-else-if="activeView === 'domains'" id="donation-panel-domains" role="tabpanel" aria-labelledby="donation-tab-domains" class="section-card donation-admin-list">
      <div class="donation-domain-toolbar"><label class="sr-only" for="donation-domain-search">{{ tr('搜索域名或 Token 前缀', 'Search domain or Token prefix') }}</label><input id="donation-domain-search" v-model="domainQuery" class="form-input" :placeholder="tr('搜索域名或 Token 前缀', 'Search domain or Token prefix')"><label class="sr-only" for="donation-status-filter">{{ tr('奖励状态', 'Grant status') }}</label><select id="donation-status-filter" v-model="domainStatus" class="form-input"><option value="all">{{ tr('全部状态', 'All statuses') }}</option><option value="active">{{ tr('有效', 'Active') }}</option><option value="pending">{{ tr('待验证', 'Pending') }}</option><option value="inactive">{{ tr('已失效', 'Inactive') }}</option><option value="revoked">{{ tr('已撤销', 'Revoked') }}</option></select><span>{{ tr(`${filteredDonations.length} 个域名`, `${filteredDonations.length} domains`) }}</span></div>
      <div v-if="visibleDonations.length" class="donation-admin-table" role="table">
        <div class="donation-admin-row donation-admin-header" role="row"><span role="columnheader">{{ tr('域名', 'Domain') }}</span><span role="columnheader">{{ tr('奖励密钥', 'Reward Token') }}</span><span role="columnheader">{{ tr('状态', 'Status') }}</span><span role="columnheader">{{ tr('入池奖励', 'Pool grant') }}</span><span role="columnheader">{{ tr('最近检查', 'Last checked') }}</span><span role="columnheader" :aria-label="tr('操作', 'Actions')"></span></div>
        <article v-for="item in visibleDonations" :key="item.id" class="donation-admin-row" role="row">
          <div class="donation-domain-cell"><strong>{{ item.domain }}</strong><small>{{ item.include_subdomains ? tr('含通配子域', 'Wildcard included') : tr('仅根域', 'Root only') }} · {{ formatDate(item.created_at) }}</small></div>
          <div class="donation-token-cell"><code>{{ item.token_prefix }}••••••••</code><small>{{ tr(`${formatMetric(item.request_count_total)} 次调用`, `${formatMetric(item.request_count_total)} requests`) }}</small></div>
          <div><span class="status-chip" :class="statusTone(item)">{{ statusLabel(item) }}</span><small v-if="item.failure_count" class="donation-failure-count">{{ tr(`${item.failure_count} 次失败`, `${item.failure_count} failures`) }}</small></div>
          <div class="donation-quota-cell"><strong>+{{ formatMetric(item.reward_total_request_limit) }}</strong><small>+{{ formatMetric(item.reward_daily_request_limit) }}/{{ tr('日', 'day') }} · +{{ item.reward_rate_limit_per_minute }} RPM</small></div>
          <div class="donation-check-cell"><span>{{ item.last_checked_at ? timeAgo(item.last_checked_at) : tr('尚未', 'Never') }}</span><small>{{ localizeBackendText(item.last_error) || '—' }}</small></div>
          <div class="donation-row-actions">
            <button class="icon-btn" type="button" :disabled="busy" :aria-label="tr('立即复检', 'Recheck now')" :title="tr('立即复检', 'Recheck now')" @click="recheck(item)"><UiIcon name="refresh" :size="15" /></button>
            <button v-if="item.status !== 'revoked'" class="icon-btn danger" type="button" :disabled="busy" :aria-label="tr('撤销奖励', 'Revoke grant')" :title="tr('撤销奖励', 'Revoke grant')" @click="revoke(item)"><UiIcon name="pause" :size="15" /></button>
          </div>
        </article>
      </div>
      <EmptyState v-else icon="domains" :title="items.length ? tr('没有匹配的域名', 'No matching domains') : tr('暂无入池域名', 'No contributed domains')" description="" />
      <div v-if="domainPages > 1" class="pager"><button class="btn btn-ghost btn-sm" type="button" :disabled="domainPage <= 1" @click="domainPage--">{{ tr('上一页', 'Previous') }}</button><span>{{ domainPage }} / {{ domainPages }}</span><button class="btn btn-ghost btn-sm" type="button" :disabled="domainPage >= domainPages" @click="domainPage++">{{ tr('下一页', 'Next') }}</button></div>
    </section>

    <section v-else id="donation-panel-policy" role="tabpanel" aria-labelledby="donation-tab-policy" class="donation-policy-stack">
      <div class="section-card donation-policy-top">
        <div><span>{{ tr('公开捐赠入口', 'Public donation page') }}</span><strong>{{ settings.donation_enabled === 'false' ? tr('已关闭', 'Disabled') : tr('已开启', 'Enabled') }}</strong></div>
        <label class="switch-control"><input v-model="settings.donation_enabled" type="checkbox" true-value="true" false-value="false" :disabled="busy" :aria-label="tr('启用公开捐赠入口', 'Enable public donation page')" @change="toggleDonation" /><span></span></label>
      </div>
      <section v-for="group in policyGroups" :key="group.key" class="section-card donation-policy-group">
        <header><span>{{ group.kicker }}</span><h3>{{ group.title }}</h3></header>
        <div class="donation-policy-grid" :class="`fields-${group.fields.length}`">
          <label v-for="field in group.fields" :key="field.key" class="form-group"><span class="form-label">{{ field.label }}</span><input v-model="settings[field.key]" class="form-input" type="number" :min="field.min" /></label>
        </div>
      </section>
      <div class="donation-policy-actions"><button class="btn btn-ghost" type="button" :disabled="busy" @click="applyPolicy">{{ tr('更新现有奖励池', 'Update existing pools') }}</button><button class="btn btn-primary" type="button" :disabled="busy" @click="savePolicy">{{ busy ? tr('正在处理…', 'Working…') : tr('保存规则', 'Save policy') }}</button></div>
    </section>
  </div>

  <AppModal v-if="adjusting" :title="tr('调整奖励池额度', 'Adjust reward pool quota')" :confirm-label="tr('确认调整', 'Apply adjustment')" :busy="busy" @close="adjusting = null" @confirm="submitAdjustment">
    <div class="donation-adjust-target"><strong>{{ tr('奖励 API Token', 'Reward API Token') }}</strong><code>{{ adjusting.token_prefix }}••••••••</code></div>
    <div class="donation-adjust-preview"><div><span>{{ tr('当前总额度', 'Current total') }}</span><strong>{{ formatMetric(adjusting.total_request_limit) }}</strong><small>→ {{ formatMetric(Math.max(0, adjusting.total_request_limit + adjustment.total_delta)) }}</small></div><div><span>{{ tr('当前每日额度', 'Current daily') }}</span><strong>{{ formatMetric(adjusting.daily_request_limit) }}</strong><small>→ {{ formatMetric(Math.max(0, adjusting.daily_request_limit + adjustment.daily_delta)) }}</small></div><div><span>{{ tr('当前 RPM', 'Current RPM') }}</span><strong>{{ adjusting.rate_limit_per_minute }}</strong><small>→ {{ Math.max(0, adjusting.rate_limit_per_minute + adjustment.rpm_delta) }}</small></div></div>
    <div class="settings-grid settings-grid-3"><label class="form-group"><span class="form-label">{{ tr('总额度变化', 'Total quota delta') }}</span><input v-model.number="adjustment.total_delta" class="form-input" type="number" /></label><label class="form-group"><span class="form-label">{{ tr('每日额度变化', 'Daily quota delta') }}</span><input v-model.number="adjustment.daily_delta" class="form-input" type="number" /></label><label class="form-group"><span class="form-label">{{ tr('RPM 变化', 'RPM delta') }}</span><input v-model.number="adjustment.rpm_delta" class="form-input" type="number" /></label></div>
    <label class="form-group"><span class="form-label">{{ tr('备注', 'Note') }}</span><input v-model="adjustment.note" class="form-input" maxlength="200" /></label>
  </AppModal>
</template>
