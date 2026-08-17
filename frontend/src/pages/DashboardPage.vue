<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from 'vue'
import { useRouter } from 'vue-router'
import AppModal from '../components/AppModal.vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import MailboxCard from '../components/MailboxCard.vue'
import UiIcon from '../components/UiIcon.vue'
import { api } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import type { Domain, LookupResult, Mailbox, RecentCode, SystemSummary } from '../types/api'
import { copyText, formatDate, formatMetric } from '../utils/format'
import { addRandomSubdomain, pickRandomDomain } from '../utils/mailbox'

const router = useRouter()
const loading = ref(true)
const error = ref('')
const warning = ref('')
const mailboxes = ref<Mailbox[]>([])
const domains = ref<Domain[]>([])
const codes = ref<RecentCode[]>([])
const summary = ref<SystemSummary | null>(null)
const lookupAddress = ref('')
const lookup = ref<LookupResult | null>(null)
const lookupBusy = ref(false)
const createOpen = ref(false)
const createBusy = ref(false)
const newAddress = ref('')
const newDomain = ref('')
const domainMode = ref<'random' | 'fixed'>('random')
const randomSubdomain = ref(false)
const subdomainLevels = ref(1)

const activeDomains = computed(() => domains.value.filter(domain => domain.is_active))
const totalEmails = computed(() => Number(summary.value?.email_total || 0))

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  warning.value = ''
  try {
    const results = await Promise.allSettled([
      api.mailboxes({ page: 1, size: 8 }),
      api.domains(),
      api.recentCodes(8),
      api.systemSummary(),
    ])
    if (results[0].status === 'rejected') throw results[0].reason
    if (results[1].status === 'rejected') throw results[1].reason
    mailboxes.value = results[0].value.data || []
    domains.value = results[1].value
    codes.value = results[2].status === 'fulfilled' ? results[2].value : []
    summary.value = results[3].status === 'fulfilled' ? results[3].value : null
    if (results.slice(2).some(result => result.status === 'rejected')) warning.value = tr('部分仪表盘指标暂时不可用，可重试。', 'Some dashboard metrics are unavailable. Retry to refresh them.')
    if (!newDomain.value) newDomain.value = activeDomains.value[0]?.domain || ''
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('仪表盘加载失败', 'Unable to load dashboard')
  } finally {
    loading.value = false
  }
}

async function runLookup(mode: 'code' | 'latest' | 'link' | 'mailbox'): Promise<void> {
  const address = lookupAddress.value.trim()
  if (!address.includes('@')) {
    toast(tr('请输入完整邮箱地址', 'Enter a complete email address'), 'warn')
    return
  }
  lookupBusy.value = true
  try {
    if (mode === 'mailbox') {
      const result = await api.lookupMailbox(address)
      await openMailbox(result.mailbox)
      return
    }
    lookup.value = mode === 'code'
      ? await api.lookupLatestCode(address)
      : mode === 'link'
        ? await api.lookupLatestLink(address)
        : await api.lookupLatest(address)
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('查询失败', 'Lookup failed'), 'error')
  } finally {
    lookupBusy.value = false
  }
}

async function createMailbox(): Promise<void> {
  if (!activeDomains.value.length) return
  createBusy.value = true
  try {
    const rootDomain = domainMode.value === 'fixed'
      ? newDomain.value
      : pickRandomDomain(activeDomains.value.map(domain => domain.domain))
    const domain = randomSubdomain.value
      ? addRandomSubdomain(rootDomain, subdomainLevels.value)
      : domainMode.value === 'fixed' ? rootDomain : ''
    const mailbox = await api.createMailbox(newAddress.value.trim(), domain)
    toast(tr(`邮箱 ${mailbox.full_address} 已创建`, `Mailbox ${mailbox.full_address} created`), 'success')
    createOpen.value = false
    newAddress.value = ''
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('创建失败', 'Create failed'), 'error')
  } finally {
    createBusy.value = false
  }
}

async function openMailbox(mailbox: Mailbox): Promise<void> {
  await router.push({ name: 'inbox', params: { mailboxId: mailbox.id }, query: { address: mailbox.full_address } })
}

async function updateRetention(mailbox: Mailbox): Promise<void> {
  try {
    const updated = await api.updateMailboxRetention(mailbox.id, !mailbox.keep_forever)
    Object.assign(mailbox, updated)
    toast(updated.keep_forever ? tr('已设为永久保留', 'Mailbox retained') : tr('已恢复自动过期', 'Site retention restored'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('更新失败', 'Update failed'), 'error')
  }
}

async function removeMailbox(mailbox: Mailbox): Promise<void> {
  if (!await askConfirm({
    title: tr('删除邮箱', 'Delete mailbox'),
    message: tr(`${mailbox.full_address} 及其全部邮件将被永久删除。`, `${mailbox.full_address} and all of its email will be permanently deleted.`),
    confirmLabel: tr('永久删除', 'Delete permanently'),
    danger: true,
  })) return
  try {
    await api.deleteMailbox(mailbox.id)
    mailboxes.value = mailboxes.value.filter(item => item.id !== mailbox.id)
    toast(tr('邮箱已删除', 'Mailbox deleted'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('删除失败', 'Delete failed'), 'error')
  }
}

async function copy(value: string): Promise<void> {
  await copyText(value)
  toast(tr('已复制', 'Copied'), 'success')
}

function openCreate(): void {
  newDomain.value = activeDomains.value[0]?.domain || ''
  domainMode.value = 'random'
  randomSubdomain.value = false
  subdomainLevels.value = 1
  createOpen.value = true
}

watchEffect(() => setPageHeader(tr('仪表盘', 'Dashboard'), tr('邮件运营概览', 'Mail operations overview'), [
  { label: tr('新建邮箱', 'New mailbox'), tone: 'primary', glyph: '+', run: openCreate },
]))
onMounted(() => void load())
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('仪表盘加载失败', 'Unable to load dashboard')" :description="error">
    <button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button>
  </EmptyState>
  <div v-else class="console-page dashboard-console">
    <div v-if="warning" class="operations-data-warning" role="status"><UiIcon name="alert" :size="15" /><span>{{ warning }}</span><button class="btn btn-ghost btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></div>
    <section class="console-command-panel">
      <div class="console-command-main">
        <div class="console-label">{{ tr('操作入口', 'QUICK LOOKUP') }}</div>
        <div class="console-command-head">
          <div>
            <h2>{{ tr('收件控制台', 'Inbox console') }}</h2>
            <p>{{ tr('输入邮箱地址，查询验证码、验证链接或最新邮件。', 'Enter an address to find its latest code, verification link, or email.') }}</p>
          </div>
        </div>
        <div class="console-command-row console-command-row-v3">
          <label class="sr-only" for="quick-lookup-address">{{ tr('邮箱地址', 'Email address') }}</label>
          <input id="quick-lookup-address" v-model="lookupAddress" class="form-input quick-action-input" :placeholder="tr('邮箱地址，例如 code@example.com', 'Email address, e.g. code@example.com')" @keyup.enter="runLookup('latest')" />
          <button class="btn btn-primary primary-lookup-btn" type="button" :disabled="lookupBusy" @click="runLookup('code')">{{ tr('查验证码', 'Find code') }}</button>
          <button class="btn btn-secondary-action" type="button" :disabled="lookupBusy" @click="runLookup('mailbox')">{{ tr('打开邮箱', 'Open inbox') }}</button>
          <button class="btn btn-ghost btn-muted-action" type="button" :disabled="lookupBusy" @click="runLookup('latest')">{{ tr('最新邮件', 'Latest email') }}</button>
          <button class="btn btn-ghost btn-muted-action" type="button" :disabled="lookupBusy" @click="runLookup('link')">{{ tr('查链接', 'Find link') }}</button>
        </div>
        <div v-if="lookup" class="lookup-inline-wrap">
          <div class="lookup-result-card">
            <div class="lookup-result-main">
              <span>{{ lookup.subject || lookup.mailbox?.full_address || tr('查询结果', 'Lookup result') }}</span>
              <strong v-if="lookup.code">{{ lookup.code }}</strong>
              <a v-else-if="lookup.link" :href="lookup.link" target="_blank" rel="noreferrer">{{ lookup.link }}</a>
              <span v-else>{{ tr('已找到最新邮件', 'Latest email found') }}</span>
            </div>
            <button v-if="lookup.code || lookup.link" class="icon-btn" type="button" :aria-label="tr('复制查询结果', 'Copy result')" :title="tr('复制查询结果', 'Copy result')" @click="copy(lookup.code || lookup.link || '')"><UiIcon name="copy" /></button>
          </div>
        </div>
        <div v-else class="console-hint">{{ tr('输入地址开始查询。', 'Enter an address to start a lookup.') }}</div>
      </div>
    </section>

    <section class="console-metrics-strip">
      <article class="console-stat"><span>{{ tr('活动邮箱', 'Mailboxes') }}</span><strong>{{ formatMetric(summary?.mailbox_total) }}</strong></article>
      <article class="console-stat"><span>{{ tr('邮件总量', 'Emails') }}</span><strong>{{ formatMetric(totalEmails) }}</strong></article>
      <article class="console-stat"><span>{{ tr('可用域名', 'Active domains') }}</span><strong>{{ formatMetric(activeDomains.length) }}</strong></article>
    </section>

    <div class="dashboard-ops-grid">
      <section class="section-card activity-card compact-activity-card">
        <div class="section-head compact-head">
          <div><div class="section-kicker">{{ tr('动态', 'ACTIVITY') }}</div><h3 class="section-title">{{ tr('最近验证码', 'Recent codes') }}</h3></div>
          <div class="section-meta">{{ tr(`${codes.length} 条`, `${codes.length} entries`) }}</div>
        </div>
        <div v-if="codes.length" class="activity-list">
          <button v-for="item in codes" :key="`${item.full_address}-${item.received_at}`" class="activity-row" type="button" @click="lookupAddress = item.full_address; runLookup('code')">
            <span class="activity-address">{{ item.full_address }}</span>
            <strong>{{ item.extracted_code || '—' }}</strong>
            <small>{{ formatDate(item.received_at) }}</small>
          </button>
        </div>
        <EmptyState v-else icon="key" :title="tr('暂无验证码', 'No codes yet')" :description="tr('新邮件中的验证码会出现在这里。', 'Codes extracted from new email will appear here.')" />
      </section>

      <section class="section-card summary-card system-rail-card">
        <div class="section-head compact-head">
          <div><div class="section-kicker">{{ tr('状态', 'STATUS') }}</div><h3 class="section-title">{{ tr('系统状态', 'System status') }}</h3></div>
          <div class="section-meta">{{ formatDate(summary?.last_health_check_at) }}</div>
        </div>
        <div class="status-summary-grid">
          <div class="status-summary-item"><span class="info-pill" :class="summary?.db_ok ? 'success' : 'danger'">{{ tr('数据库', 'Database') }} · {{ summary?.db_ok ? tr('正常', 'Healthy') : tr('异常', 'Down') }}</span></div>
          <div class="status-summary-item"><span class="info-pill" :class="summary?.redis_ok ? 'success' : 'danger'">Redis · {{ summary?.redis_ok ? tr('正常', 'Healthy') : tr('异常', 'Down') }}</span></div>
          <div class="status-summary-item"><span class="info-pill" :class="Number(summary?.unhealthy_domain_count || 0) ? 'warn' : 'success'">{{ tr('域名', 'Domains') }} · {{ tr(`${summary?.unhealthy_domain_count || 0} 异常`, `${summary?.unhealthy_domain_count || 0} unhealthy`) }}</span></div>
          <div class="status-summary-item"><span class="info-pill" :class="summary?.smtp_reachable ? 'success' : summary?.smtp_configured ? 'warn' : 'danger'">SMTP · {{ summary?.smtp_reachable ? tr('可达', 'Reachable') : summary?.smtp_configured ? tr('已配置', 'Configured') : tr('未配置', 'Not configured') }}</span></div>
          <div class="status-summary-item"><span class="info-pill" :class="summary?.lmtp_running ? 'success' : 'danger'">LMTP · {{ summary?.lmtp_running ? tr('运行中', 'Running') : tr('未运行', 'Stopped') }}</span></div>
        </div>
      </section>
    </div>

    <section class="section-card console-section">
      <div class="section-head compact-head">
        <div><div class="section-kicker">{{ tr('邮箱', 'MAILBOXES') }}</div><h3 class="section-title">{{ tr('当前活动邮箱', 'Active mailboxes') }}</h3></div>
        <button class="btn btn-ghost btn-sm" type="button" @click="router.push({ name: 'mailboxes' })">{{ tr('查看全部', 'View all') }}</button>
      </div>
      <div v-if="mailboxes.length" class="mailbox-grid">
        <MailboxCard v-for="mailbox in mailboxes" :key="mailbox.id" :mailbox="mailbox" @open="openMailbox" @retention="updateRetention" @remove="removeMailbox" />
      </div>
      <EmptyState v-else icon="inbox" :title="tr('暂无邮箱', 'No mailboxes')" :description="tr('创建一个地址或等待新地址自动收件。', 'Create an address or wait for inbound mail.')">
        <button class="btn btn-primary btn-sm" type="button" @click="openCreate">{{ tr('新建邮箱', 'New mailbox') }}</button>
      </EmptyState>
    </section>
  </div>

  <AppModal v-if="createOpen" :title="tr('新建邮箱', 'New mailbox')" :confirm-label="tr('创建邮箱', 'Create mailbox')" :busy="createBusy" :confirm-disabled="!activeDomains.length" @close="createOpen = false" @confirm="createMailbox">
    <div class="form-group">
      <label class="form-label" for="dashboard-mailbox-address">{{ tr('邮箱前缀', 'Local part') }}</label>
      <input id="dashboard-mailbox-address" v-model="newAddress" class="form-input" :placeholder="tr('留空则随机生成', 'Leave blank to generate')" @keyup.enter="createMailbox" />
    </div>
    <div class="form-group">
      <span class="form-label">{{ tr('根域', 'Root domain') }}</span>
      <div class="domain-mode-control" role="group" :aria-label="tr('根域选择方式', 'Root domain selection')">
        <button type="button" :class="{ active: domainMode === 'random' }" @click="domainMode = 'random'">{{ tr('随机选择', 'Random') }}</button>
        <button type="button" :class="{ active: domainMode === 'fixed' }" @click="domainMode = 'fixed'">{{ tr('指定域名', 'Specific') }}</button>
      </div>
      <select v-if="activeDomains.length && domainMode === 'fixed'" v-model="newDomain" class="form-input" :aria-label="tr('指定根域', 'Specific root domain')">
        <option v-for="domain in activeDomains" :key="domain.id" :value="domain.domain">{{ domain.domain }}</option>
      </select>
      <div v-if="!activeDomains.length" class="form-empty-notice">
        <span>{{ tr('暂无可用域名。', 'No active domains.') }}</span>
        <button class="btn btn-ghost btn-sm" type="button" @click="createOpen = false; router.push({ name: 'domains' })">{{ tr('域名管理', 'Manage domains') }}</button>
      </div>
    </div>
    <div class="mailbox-subdomain-row">
      <label class="setting-toggle-row"><input v-model="randomSubdomain" type="checkbox" :disabled="!activeDomains.length" /><span><strong>{{ tr('随机子域', 'Random subdomain') }}</strong></span></label>
      <div v-if="randomSubdomain" class="form-group mailbox-subdomain-level"><label class="form-label" for="dashboard-subdomain-levels">{{ tr('层级', 'Levels') }}</label><select id="dashboard-subdomain-levels" v-model.number="subdomainLevels" class="form-input"><option v-for="level in 5" :key="level" :value="level">{{ level }}</option></select></div>
    </div>
  </AppModal>
</template>
