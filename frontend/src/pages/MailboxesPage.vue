<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch, watchEffect } from 'vue'
import { useRouter } from 'vue-router'
import AppModal from '../components/AppModal.vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import MailboxCard from '../components/MailboxCard.vue'
import { api } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import type { CleanupPreview, Domain, Mailbox } from '../types/api'
import { formatMetric } from '../utils/format'
import { addRandomSubdomain, pickRandomDomain } from '../utils/mailbox'

const router = useRouter()
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const warning = ref('')
const items = ref<Mailbox[]>([])
const total = ref(0)
const page = ref(1)
const size = 12
const selected = ref<string[]>([])
const domains = ref<Domain[]>([])
const filters = reactive({ q: '', domain: '', retention: 'all' })
const applied = reactive({ q: '', domain: '', retention: 'all' })
const modal = ref<'create' | 'mailbox-cleanup' | 'email-cleanup' | null>(null)
const newAddress = ref('')
const newDomain = ref('')
const domainMode = ref<'random' | 'fixed'>('random')
const randomSubdomain = ref(false)
const subdomainLevels = ref(1)
const cleanup = reactive({ onlyExpired: true, onlyEmpty: false, olderThanMinutes: 240 })
const cleanupPreview = ref<CleanupPreview | null>(null)
const previewBusy = ref(false)
let previewController: AbortController | undefined

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / size)))
const allPageSelected = computed(() => items.value.length > 0 && items.value.every(item => selected.value.includes(item.id)))
const activeDomains = computed(() => domains.value.filter(domain => domain.is_active))

function queryParams(): Record<string, string | number | boolean> {
  return {
    page: page.value,
    size,
    q: applied.q,
    domain: applied.domain,
    keep_forever: applied.retention === 'permanent',
    expiring_within_hours: applied.retention === 'expiring' ? 24 : 0,
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await api.mailboxes(queryParams())
    items.value = result.data || []
    total.value = result.total || 0
    selected.value = selected.value.filter(id => items.value.some(item => item.id === id))
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('邮箱目录加载失败', 'Unable to load mailbox directory')
  } finally {
    loading.value = false
  }
}

function applyFilters(): void {
  Object.assign(applied, filters)
  page.value = 1
  void load()
}

function resetFilters(): void {
  Object.assign(filters, { q: '', domain: '', retention: 'all' })
  applyFilters()
}

function changePage(next: number): void {
  if (next < 1 || next > totalPages.value || next === page.value) return
  page.value = next
  void load()
}

function toggleSelection(mailbox: Mailbox): void {
  selected.value = selected.value.includes(mailbox.id)
    ? selected.value.filter(id => id !== mailbox.id)
    : [...selected.value, mailbox.id]
}

function togglePageSelection(): void {
  selected.value = allPageSelected.value ? [] : items.value.map(item => item.id)
}

async function batchRetention(keep: boolean): Promise<void> {
  if (!selected.value.length) return
  busy.value = true
  try {
    await api.updateMailboxRetentionBatch(selected.value, keep)
    toast(tr(`${selected.value.length} 个邮箱已更新`, `${selected.value.length} mailboxes updated`), 'success')
    selected.value = []
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('批量更新失败', 'Batch update failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function createMailbox(): Promise<void> {
  if (!activeDomains.value.length) return
  busy.value = true
  try {
    const rootDomain = domainMode.value === 'fixed'
      ? newDomain.value
      : pickRandomDomain(activeDomains.value.map(domain => domain.domain))
    const domain = randomSubdomain.value
      ? addRandomSubdomain(rootDomain, subdomainLevels.value)
      : domainMode.value === 'fixed' ? rootDomain : ''
    const mailbox = await api.createMailbox(newAddress.value.trim(), domain)
    toast(tr(`邮箱 ${mailbox.full_address} 已创建`, `Mailbox ${mailbox.full_address} created`), 'success')
    modal.value = null
    newAddress.value = ''
    page.value = 1
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('创建失败', 'Create failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function updateRetention(mailbox: Mailbox): Promise<void> {
  try {
    Object.assign(mailbox, await api.updateMailboxRetention(mailbox.id, !mailbox.keep_forever))
    toast(mailbox.keep_forever ? tr('已设为永久保留', 'Mailbox retained') : tr('已恢复自动过期', 'Site retention restored'), 'success')
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
    toast(tr('邮箱已删除', 'Mailbox deleted'), 'success')
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('删除失败', 'Delete failed'), 'error')
  }
}

async function cleanupMailboxes(): Promise<void> {
  busy.value = true
  try {
    const result = await api.cleanupMailboxes({
      query: applied.q,
      domain: applied.domain,
      only_expired: cleanup.onlyExpired,
      only_empty: cleanup.onlyEmpty,
    })
    toast(tr(`已清理 ${Number(result?.deleted || 0)} 个邮箱`, `${Number(result?.deleted || 0)} mailboxes deleted`), 'success')
    modal.value = null
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('清理失败', 'Cleanup failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function cleanupEmails(): Promise<void> {
  busy.value = true
  try {
    const result = await api.cleanupEmails({
      query: applied.q,
      domain: applied.domain,
      older_than_minutes: cleanup.olderThanMinutes,
    })
    toast(tr(`已清理 ${Number(result?.deleted || 0)} 封邮件`, `${Number(result?.deleted || 0)} emails deleted`), 'success')
    modal.value = null
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('清理失败', 'Cleanup failed'), 'error')
  } finally {
    busy.value = false
  }
}

function openCreate(): void {
  newDomain.value = activeDomains.value[0]?.domain || ''
  domainMode.value = 'random'
  randomSubdomain.value = false
  subdomainLevels.value = 1
  modal.value = 'create'
}

async function openCleanup(kind: 'mailbox-cleanup' | 'email-cleanup'): Promise<void> {
  modal.value = kind
  await loadCleanupPreview()
}

async function loadCleanupPreview(): Promise<void> {
  if (modal.value !== 'mailbox-cleanup' && modal.value !== 'email-cleanup') return
  previewController?.abort()
  const controller = new AbortController()
  previewController = controller
  previewBusy.value = true
  try {
    cleanupPreview.value = await api.cleanupPreview({
      kind: modal.value === 'mailbox-cleanup' ? 'mailboxes' : 'emails',
      query: applied.q,
      domain: applied.domain,
      only_expired: cleanup.onlyExpired,
      only_empty: cleanup.onlyEmpty,
      older_than_minutes: cleanup.olderThanMinutes,
    }, controller.signal)
  } catch (cause) {
    if (!controller.signal.aborted) toast(cause instanceof Error ? cause.message : tr('清理预估失败', 'Cleanup preview failed'), 'error')
  } finally {
    if (previewController === controller) {
      previewController = undefined
      previewBusy.value = false
    }
  }
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

async function loadDependencies(): Promise<void> {
  try {
    domains.value = await api.domains()
  } catch (cause) {
    warning.value = cause instanceof Error ? cause.message : tr('域名列表加载失败，暂时无法创建邮箱。', 'Domain list unavailable; mailbox creation is temporarily unavailable.')
  }
  await load()
}
onMounted(() => void loadDependencies())
watch(() => [cleanup.onlyExpired, cleanup.onlyEmpty, cleanup.olderThanMinutes], () => {
  if (modal.value === 'mailbox-cleanup' || modal.value === 'email-cleanup') void loadCleanupPreview()
})
onBeforeUnmount(() => previewController?.abort())
watchEffect(() => setPageHeader(tr('邮箱目录', 'Mailbox directory'), tr('搜索和维护站点邮箱', 'Search and maintain site mailboxes'), [
  { label: tr('新建邮箱', 'New mailbox'), tone: 'primary', glyph: '+', run: openCreate },
  { label: tr('清理邮件', 'Clean email'), glyph: '×', run: () => openCleanup('email-cleanup') },
  { label: tr('清理邮箱', 'Clean mailboxes'), glyph: '×', run: () => openCleanup('mailbox-cleanup') },
]))
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('邮箱目录加载失败', 'Unable to load mailbox directory')" :description="error">
    <button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button>
  </EmptyState>
  <div v-else class="console-page mailbox-directory-page">
    <div v-if="warning" class="operations-data-warning" role="status"><span>{{ warning }}</span><button class="btn btn-ghost btn-sm" type="button" @click="loadDependencies">{{ tr('重试', 'Retry') }}</button></div>
    <section class="directory-summary-strip">
      <span class="hero-chip"><small>{{ tr('邮箱总数', 'Mailboxes') }}</small><strong>{{ formatMetric(total) }}</strong></span>
      <span class="hero-chip"><small>{{ tr('当前页', 'Page') }}</small><strong>{{ page }} / {{ totalPages }}</strong></span>
      <span class="hero-chip"><small>{{ tr('本页永久保留', 'Retained on page') }}</small><strong>{{ items.filter(item => item.keep_forever).length }}</strong></span>
      <span class="hero-chip"><small>{{ tr('已选择', 'Selected') }}</small><strong>{{ selected.length }}</strong></span>
      <div class="directory-summary-note">{{ applied.q || applied.domain || applied.retention !== 'all' ? tr('筛选已启用', 'Filters active') : tr('显示全部邮箱', 'Showing all mailboxes') }}</div>
    </section>

    <section class="section-card directory-toolbar-card">
      <div class="section-head compact-head">
        <div><div class="section-kicker">{{ tr('筛选', 'FILTER') }}</div><h3 class="section-title">{{ tr('搜索与批量操作', 'Search and batch actions') }}</h3></div>
        <div class="section-meta">{{ tr(`${total} 个邮箱`, `${total} mailboxes`) }}</div>
      </div>
      <form class="directory-toolbar-grid" @submit.prevent="applyFilters">
        <div class="form-group"><label class="form-label" for="mailbox-filter-query">{{ tr('地址关键字', 'Address keyword') }}</label><input id="mailbox-filter-query" v-model="filters.q" class="form-input" placeholder="openai / otp / user123" /></div>
        <div class="form-group"><label class="form-label" for="mailbox-filter-domain">{{ tr('域名', 'Domain') }}</label><select id="mailbox-filter-domain" v-model="filters.domain" class="form-input"><option value="">{{ tr('全部域名', 'All domains') }}</option><option v-for="domain in domains" :key="domain.id" :value="domain.domain">{{ domain.domain }}</option></select></div>
        <div class="form-group">
          <label class="form-label" for="mailbox-filter-retention">{{ tr('保留状态', 'Retention') }}</label>
          <select id="mailbox-filter-retention" v-model="filters.retention" class="form-input">
            <option value="all">{{ tr('全部邮箱', 'All mailboxes') }}</option>
            <option value="permanent">{{ tr('仅永久保留', 'Retained only') }}</option>
            <option value="expiring">{{ tr('仅 24 小时内到期', 'Expiring within 24 hours') }}</option>
          </select>
        </div>
        <div class="directory-toolbar-actions">
          <button class="btn btn-primary btn-sm" type="submit">{{ tr('搜索', 'Search') }}</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="resetFilters">{{ tr('重置', 'Reset') }}</button>
        </div>
      </form>
      <div class="mailbox-batch-actions compact-batch-actions">
        <button class="btn btn-ghost btn-sm" type="button" @click="togglePageSelection">{{ allPageSelected ? tr('取消本页全选', 'Deselect page') : tr('本页全选', 'Select page') }}</button>
        <button class="btn btn-ghost btn-sm" type="button" :disabled="!selected.length || busy" @click="batchRetention(true)">{{ tr('设为永久', 'Retain') }}</button>
        <button class="btn btn-ghost btn-sm" type="button" :disabled="!selected.length || busy" @click="batchRetention(false)">{{ tr('取消永久', 'Use site policy') }}</button>
        <button class="btn btn-ghost btn-sm" type="button" :disabled="!selected.length" @click="selected = []">{{ tr('清空选择', 'Clear selection') }}</button>
      </div>
    </section>

    <section class="section-card console-section mailbox-list-section">
      <div class="section-head compact-head">
        <div><div class="section-kicker">{{ tr('邮箱', 'MAILBOXES') }}</div><h3 class="section-title">{{ tr('邮箱目录', 'Mailbox directory') }}</h3></div>
        <div class="section-meta">{{ tr(`共 ${formatMetric(total)} 个 · 每页 ${size} 个`, `${formatMetric(total)} total · ${size} per page`) }}</div>
      </div>
      <div v-if="items.length" class="mailbox-grid mailbox-directory-grid">
        <MailboxCard
          v-for="mailbox in items"
          :key="mailbox.id"
          :mailbox="mailbox"
          selectable
          :selected="selected.includes(mailbox.id)"
          @open="router.push({ name: 'inbox', params: { mailboxId: mailbox.id }, query: { address: mailbox.full_address } })"
          @select="toggleSelection"
          @retention="updateRetention"
          @remove="removeMailbox"
        />
      </div>
      <EmptyState v-else icon="inbox" :title="tr('没有邮箱', 'No mailboxes')" :description="tr('更换筛选条件或创建新邮箱。', 'Change the filters or create a mailbox.')">
        <button class="btn btn-primary btn-sm" type="button" @click="openCreate">{{ tr('新建邮箱', 'New mailbox') }}</button>
      </EmptyState>
      <div v-if="totalPages > 1" class="mailbox-directory-pager">
        <div class="pager">
          <button class="btn btn-ghost btn-sm" type="button" :disabled="page <= 1" @click="changePage(page - 1)">{{ tr('上一页', 'Previous') }}</button>
          <span>{{ tr(`第 ${page} 页 / 共 ${totalPages} 页`, `Page ${page} of ${totalPages}`) }}</span>
          <button class="btn btn-ghost btn-sm" type="button" :disabled="page >= totalPages" @click="changePage(page + 1)">{{ tr('下一页', 'Next') }}</button>
        </div>
      </div>
    </section>
  </div>

  <AppModal v-if="modal === 'create'" :title="tr('新建邮箱', 'New mailbox')" :confirm-label="tr('创建邮箱', 'Create mailbox')" :busy="busy" :confirm-disabled="!activeDomains.length" @close="modal = null" @confirm="createMailbox">
    <div class="form-group"><label class="form-label" for="directory-mailbox-address">{{ tr('邮箱前缀', 'Local part') }}</label><input id="directory-mailbox-address" v-model="newAddress" class="form-input" :placeholder="tr('留空则随机生成', 'Leave blank to generate')" /></div>
    <div class="form-group">
      <span class="form-label">{{ tr('根域', 'Root domain') }}</span>
      <div class="domain-mode-control" role="group" :aria-label="tr('根域选择方式', 'Root domain selection')"><button type="button" :class="{ active: domainMode === 'random' }" @click="domainMode = 'random'">{{ tr('随机选择', 'Random') }}</button><button type="button" :class="{ active: domainMode === 'fixed' }" @click="domainMode = 'fixed'">{{ tr('指定域名', 'Specific') }}</button></div>
      <select v-if="activeDomains.length && domainMode === 'fixed'" v-model="newDomain" class="form-input" :aria-label="tr('指定根域', 'Specific root domain')"><option v-for="domain in activeDomains" :key="domain.id" :value="domain.domain">{{ domain.domain }}</option></select>
      <div v-if="!activeDomains.length" class="form-empty-notice">
        <span>{{ tr('暂无可用域名。', 'No active domains.') }}</span>
        <button class="btn btn-ghost btn-sm" type="button" @click="modal = null; router.push({ name: 'domains' })">{{ tr('域名管理', 'Manage domains') }}</button>
      </div>
    </div>
    <div class="mailbox-subdomain-row"><label class="setting-toggle-row"><input v-model="randomSubdomain" type="checkbox" :disabled="!activeDomains.length" /><span><strong>{{ tr('随机子域', 'Random subdomain') }}</strong></span></label><div v-if="randomSubdomain" class="form-group mailbox-subdomain-level"><label class="form-label" for="directory-subdomain-levels">{{ tr('层级', 'Levels') }}</label><select id="directory-subdomain-levels" v-model.number="subdomainLevels" class="form-input"><option v-for="level in 5" :key="level" :value="level">{{ level }}</option></select></div></div>
  </AppModal>

  <AppModal v-if="modal === 'mailbox-cleanup'" :title="tr('清理邮箱', 'Clean mailboxes')" :confirm-label="tr('执行清理', 'Run cleanup')" confirm-tone="danger" :busy="busy || previewBusy" :confirm-disabled="!cleanupPreview?.matching_mailboxes" @close="modal = null" @confirm="cleanupMailboxes">
    <p class="form-hint">{{ tr('沿用当前地址和域名筛选；邮箱内邮件会同时删除。', 'Uses the current address and domain filters; email in matching mailboxes is also deleted.') }}</p>
    <div class="maintenance-preview"><span>{{ tr('预计删除', 'Estimated impact') }}</span><strong>{{ formatMetric(cleanupPreview?.matching_mailboxes) }} {{ tr('个邮箱', 'mailboxes') }}</strong></div>
    <label class="setting-toggle-row"><input v-model="cleanup.onlyExpired" type="checkbox" /><span><strong>{{ tr('只删除已过期邮箱', 'Expired mailboxes only') }}</strong></span></label>
    <label class="setting-toggle-row"><input v-model="cleanup.onlyEmpty" type="checkbox" /><span><strong>{{ tr('只删除空邮箱', 'Empty mailboxes only') }}</strong></span></label>
  </AppModal>

  <AppModal v-if="modal === 'email-cleanup'" :title="tr('清理历史邮件', 'Delete old email')" :confirm-label="tr('执行清理', 'Run cleanup')" confirm-tone="danger" :busy="busy || previewBusy" :confirm-disabled="!cleanupPreview?.matching_emails" @close="modal = null" @confirm="cleanupEmails">
    <p class="form-hint">{{ tr('沿用当前地址和域名筛选，邮箱本身不会删除。', 'Uses the current address and domain filters without deleting mailboxes.') }}</p>
    <div class="maintenance-preview"><span>{{ tr('预计删除', 'Estimated impact') }}</span><strong>{{ formatMetric(cleanupPreview?.matching_emails) }} {{ tr('封邮件', 'emails') }}</strong><small>{{ formatBytes(cleanupPreview?.matching_bytes || 0) }}</small></div>
    <div class="form-group"><label class="form-label" for="cleanup-email-age">{{ tr('邮件最小年龄（分钟）', 'Minimum email age (minutes)') }}</label><input id="cleanup-email-age" v-model.number="cleanup.olderThanMinutes" class="form-input" type="number" min="1" /></div>
  </AppModal>
</template>
