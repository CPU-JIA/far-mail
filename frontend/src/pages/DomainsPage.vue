<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, watchEffect } from 'vue'
import { useRouter } from 'vue-router'
import AppModal from '../components/AppModal.vue'
import EmptyState from '../components/EmptyState.vue'
import UiIcon from '../components/UiIcon.vue'
import LoadingState from '../components/LoadingState.vue'
import { api, ApiError } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import type { CloudflareIntegrationStatus, DNSPlan, Domain, DomainHealth, DomainSubmitResponse } from '../types/api'
import { copyText, formatDate, timeAgo } from '../utils/format'

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const warning = ref('')
const domains = ref<Domain[]>([])
const health = ref<DomainHealth[]>([])
const addOpen = ref(false)
const domainInput = ref('')
const submitResult = ref<DomainSubmitResponse | null>(null)
const dnsPlan = ref<DNSPlan | null>(null)
const cloudflareStatus = ref<CloudflareIntegrationStatus | null>(null)
const dnsMode = ref<'manual' | 'cloudflare'>('manual')
const router = useRouter()
let dnsPreviewTimer: number | undefined
let dnsPreviewController: AbortController | undefined

const pending = computed(() => domains.value.filter(domain => domain.status === 'pending'))
const established = computed(() => domains.value.filter(domain => domain.status !== 'pending'))
const activeCount = computed(() => domains.value.filter(domain => domain.is_active).length)
const donatedCount = computed(() => domains.value.filter(domain => domain.source_type === 'donated').length)

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  warning.value = ''
  try {
    const results = await Promise.allSettled([api.domains(), api.domainHealth(), api.cloudflareIntegration()])
    if (results[0].status === 'rejected') throw results[0].reason
    domains.value = results[0].value
    health.value = results[1].status === 'fulfilled' ? results[1].value : []
    cloudflareStatus.value = results[2].status === 'fulfilled' ? results[2].value : null
    if (results.slice(1).some(result => result.status === 'rejected')) warning.value = tr('域名健康或 Cloudflare 状态暂时不可用，可重试。', 'Domain health or Cloudflare status is unavailable. Retry to refresh it.')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('域名数据加载失败', 'Unable to load domains')
  } finally {
    loading.value = false
  }
}

async function previewDNS(mode: 'manual' | 'cloudflare'): Promise<void> {
  const value = domainInput.value.trim().toLowerCase()
  if (!/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i.test(value)) {
    toast(tr('请输入有效根域名', 'Enter a valid root domain'), 'warn')
    return
  }
	dnsPreviewController?.abort()
	const controller = new AbortController()
	dnsPreviewController = controller
	busy.value = true
	try {
		dnsMode.value = mode
		const result = mode === 'cloudflare' ? await api.cloudflarePreview(value, controller.signal) : await api.dnsPreview(value, controller.signal)
		if (!controller.signal.aborted && domainInput.value.trim().toLowerCase() === value) dnsPlan.value = result
	} catch (cause) {
		if (controller.signal.aborted) return
		toast(cause instanceof Error ? cause.message : tr('DNS 预览失败', 'Unable to preview DNS'), 'error')
	} finally {
		if (dnsPreviewController === controller) {
			dnsPreviewController = undefined
			busy.value = false
		}
	}
}

function openAdd(): void {
  addOpen.value = true
  submitResult.value = null
  dnsPlan.value = null
  dnsMode.value = 'manual'
  domainInput.value = ''
}

function recordHost(name: string): string {
  const domain = domainInput.value.trim().toLowerCase()
  if (name === domain) return '@'
  if (domain && name.endsWith(`.${domain}`)) return name.slice(0, -(domain.length + 1))
  return name
}

async function copyDNS(value: string): Promise<void> {
  await copyText(value)
  toast(tr('DNS 记录已复制', 'DNS record copied'), 'success')
}

async function applyCloudflare(): Promise<void> {
  const value = domainInput.value.trim().toLowerCase()
  if (!dnsPlan.value || dnsPlan.value.domain !== value) {
    await previewDNS('cloudflare')
    return
  }
  const conflicts = dnsPlan.value.items.filter(item => item.status === 'conflict')
  const confirmConflicts = conflicts.length > 0 && conflicts.every(item => item.detail !== 'multiple records already exist')
    ? await askConfirm({
      title: tr('覆盖 DNS 冲突', 'Replace DNS conflicts'),
      message: tr(`发现 ${conflicts.length} 条冲突记录，将使用当前部署值覆盖。`, `${conflicts.length} conflicting records will be replaced with values from this deployment.`),
      confirmLabel: tr('覆盖并配置', 'Replace and apply'),
      danger: true,
    })
    : false
  if (conflicts.some(item => item.detail === 'multiple records already exist')) {
    toast(tr('存在多条同名记录，请先在 Cloudflare 控制台处理冲突', 'Multiple records exist; resolve them in Cloudflare first'), 'warn')
  }
  busy.value = true
  try {
    dnsPlan.value = await api.cloudflareApply(value, confirmConflicts)
    if (dnsPlan.value.items.some(item => item.status === 'conflict')) toast(tr('可应用的记录已处理，冲突项仍需确认', 'Applicable records were processed; conflicts still require review'), 'warn')
    else toast(tr('Cloudflare DNS 配置已执行', 'Cloudflare DNS configuration applied'), 'success')
  } catch (cause) {
    const plan = cause instanceof ApiError ? cause.details?.plan as DNSPlan | undefined : undefined
    if (plan) dnsPlan.value = plan
    if (plan?.rolled_back) toast(tr('配置未完成，已自动回滚已写入的记录', 'Configuration failed; applied records were automatically rolled back'), 'warn')
    else toast(cause instanceof Error ? cause.message : tr('Cloudflare DNS 配置失败', 'Unable to apply Cloudflare DNS'), 'error')
  } finally { busy.value = false }
}

async function prepareAddDomain(): Promise<void> {
  if (!dnsPlan.value || dnsPlan.value.domain !== domainInput.value.trim().toLowerCase()) {
    await previewDNS('manual')
    return
  }
  await addDomain()
}

async function addDomain(): Promise<void> {
  const value = domainInput.value.trim().toLowerCase()
  if (!/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i.test(value)) {
    toast(tr('请输入有效根域名', 'Enter a valid root domain'), 'warn')
    return
  }
  busy.value = true
  try {
    submitResult.value = await api.addDomain(value)
    toast(tr('域名已提交', 'Domain submitted'), 'success')
    domainInput.value = ''
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('域名提交失败', 'Domain submission failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function toggle(domain: Domain): Promise<void> {
  try {
    await api.toggleDomain(domain.id, !domain.is_active)
    domain.is_active = !domain.is_active
    domain.status = domain.is_active ? 'active' : 'disabled'
    toast(domain.is_active ? tr('域名已启用', 'Domain enabled') : tr('域名已停用', 'Domain disabled'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('操作失败', 'Operation failed'), 'error')
  }
}

async function remove(domain: Domain): Promise<void> {
  if (!await askConfirm({
    title: tr('删除域名', 'Delete domain'),
    message: tr(`${domain.domain} 将从域名池删除，已有邮箱可能无法继续收件。`, `${domain.domain} will be removed from the pool and existing mailboxes may stop receiving mail.`),
    confirmLabel: tr('删除域名', 'Delete domain'),
    danger: true,
  })) return
  try {
    await api.deleteDomain(domain.id)
    domains.value = domains.value.filter(item => item.id !== domain.id)
    toast(tr('域名已删除', 'Domain deleted'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('删除失败', 'Delete failed'), 'error')
  }
}

async function refreshHealth(): Promise<void> {
  busy.value = true
  try {
    const result = await api.refreshDomainHealth()
    health.value = result.data || []
    toast(tr('域名健康状态已刷新', 'Domain health refreshed'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('刷新失败', 'Refresh failed'), 'error')
  } finally {
    busy.value = false
  }
}

function healthFor(domain: string): DomainHealth | undefined {
  return health.value.find(item => item.domain === domain)
}

watchEffect(() => setPageHeader(tr('域名管理', 'Domains'), tr('站点收件域名与 MX 状态', 'Receiving domains and MX status'), [
  { label: tr('添加域名', 'Add domain'), tone: 'primary', glyph: '+', run: openAdd },
  { label: tr('刷新健康状态', 'Refresh health'), glyph: '↻', run: refreshHealth },
]))
watch(domainInput, value => {
  if (dnsPreviewTimer) window.clearTimeout(dnsPreviewTimer)
  dnsPlan.value = null
  dnsMode.value = 'manual'
  const normalized = value.trim().toLowerCase()
  if (!/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i.test(normalized)) return
  dnsPreviewTimer = window.setTimeout(() => void previewDNS('manual'), 350)
})
onMounted(() => void load())
onBeforeUnmount(() => {
	if (dnsPreviewTimer) window.clearTimeout(dnsPreviewTimer)
	dnsPreviewController?.abort()
})
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('域名数据加载失败', 'Unable to load domains')" :description="error">
    <button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button>
  </EmptyState>
  <div v-else class="page-stack">
    <div v-if="warning" class="operations-data-warning" role="status"><UiIcon name="alert" :size="15" /><span>{{ warning }}</span><button class="btn btn-ghost btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></div>
    <section class="console-metrics-strip domain-metrics-strip">
      <article class="console-stat"><span>{{ tr('全部域名', 'All domains') }}</span><strong>{{ domains.length }}</strong></article>
      <article class="console-stat"><span>{{ tr('启用中', 'Active') }}</span><strong>{{ activeCount }}</strong></article>
      <article class="console-stat"><span>{{ tr('待验证', 'Pending') }}</span><strong>{{ pending.length }}</strong></article>
      <article class="console-stat"><span>{{ tr('捐赠接入', 'Donated') }}</span><strong>{{ donatedCount }}</strong></article>
    </section>

    <section v-if="pending.length" class="section-card pending-card">
      <div class="section-head"><div><div class="section-kicker">{{ tr('待验证', 'PENDING') }}</div><h3 class="section-title">{{ tr('待 MX 验证', 'Pending MX verification') }}</h3><p class="section-desc">{{ tr('DNS 会定期检测，验证成功后自动激活。', 'DNS is checked periodically and activated after verification.') }}</p></div><div class="section-meta">{{ tr(`${pending.length} 个`, `${pending.length} domains`) }}</div></div>
      <div class="table-wrap"><table><thead><tr><th>{{ tr('域名', 'Domain') }}</th><th>{{ tr('上次检测', 'Last checked') }}</th><th>{{ tr('状态', 'Status') }}</th><th>{{ tr('操作', 'Actions') }}</th></tr></thead><tbody>
        <tr v-for="domain in pending" :key="domain.id"><td><code>{{ domain.domain }}</code></td><td>{{ domain.mx_checked_at ? timeAgo(domain.mx_checked_at) : tr('从未', 'Never') }}</td><td><span class="badge badge-gold"><UiIcon name="activity" :size="13" />{{ domain.source_type === 'donated' ? tr('捐赠验证', 'Donation check') : tr('检测中', 'Checking') }}</span></td><td><button v-if="domain.source_type === 'donated'" class="btn btn-ghost btn-sm" type="button" @click="router.push({ name: 'donation-plan' })">{{ tr('捐赠计划', 'Donations') }}</button><button v-else class="btn btn-danger btn-sm" type="button" @click="remove(domain)">{{ tr('删除', 'Delete') }}</button></td></tr>
      </tbody></table></div>
    </section>

    <section class="section-card">
      <div class="section-head"><div><div class="section-kicker">{{ tr('域名池', 'DOMAIN POOL') }}</div><h3 class="section-title">{{ tr('已接入域名', 'Connected domains') }}</h3><p class="section-desc">{{ tr('停用域名保留记录，但不再参与邮箱分配或收件。', 'Disabled domains remain listed but no longer receive or allocate mailboxes.') }}</p></div><div class="section-meta">{{ tr(`共 ${established.length} 个`, `${established.length} total`) }}</div></div>
      <div v-if="established.length" class="domain-admin-grid">
        <article v-for="domain in established" :key="domain.id" class="domain-admin-card">
          <div class="domain-admin-head"><code class="domain-name-pill">{{ domain.domain }}</code><div class="domain-status-group"><span v-if="domain.source_type === 'donated'" class="status-chip neutral">{{ tr('捐赠', 'Donated') }}</span><span class="status-chip" :class="domain.is_active ? 'success' : 'neutral'">{{ domain.is_active ? tr('启用中', 'Active') : tr('已停用', 'Disabled') }}</span></div></div>
          <div v-if="healthFor(domain.domain)" class="mailbox-compact-meta">
            <span>Root MX: {{ healthFor(domain.domain)?.root_mx_ok ? tr('正常', 'Healthy') : tr('异常', 'Failed') }}</span>
            <span>Wildcard: {{ healthFor(domain.domain)?.wildcard_mx_ok ? tr('正常', 'Healthy') : tr('异常', 'Failed') }}</span>
            <span>{{ formatDate(healthFor(domain.domain)?.checked_at) }}</span>
          </div>
          <div class="domain-admin-actions"><button v-if="domain.source_type === 'donated'" class="btn btn-ghost btn-sm" type="button" @click="router.push({ name: 'donation-plan' })">{{ tr('捐赠计划', 'Donations') }}</button><template v-else><button class="btn btn-ghost btn-sm" type="button" @click="toggle(domain)">{{ domain.is_active ? tr('停用', 'Disable') : tr('启用', 'Enable') }}</button><button class="btn btn-danger btn-sm" type="button" @click="remove(domain)">{{ tr('删除', 'Delete') }}</button></template></div>
        </article>
      </div>
      <EmptyState v-else icon="domains" :title="tr('暂无域名', 'No domains')" :description="tr('添加并验证域名后即可分配邮箱地址。', 'Add and verify a domain to allocate mailbox addresses.')">
        <button class="btn btn-primary btn-sm" type="button" @click="openAdd">{{ tr('添加域名', 'Add domain') }}</button>
      </EmptyState>
    </section>
  </div>

  <AppModal v-if="addOpen" :title="tr('添加收件域名', 'Add receiving domain')" :confirm-label="tr('提交并检测', 'Submit and check')" :confirm-disabled="!dnsPlan" :busy="busy" size="wide" @close="addOpen = false" @confirm="prepareAddDomain">
    <div class="form-group"><label class="form-label" for="domain-input">{{ tr('根域名', 'Root domain') }}</label><input id="domain-input" v-model="domainInput" class="form-input" placeholder="example.com" autofocus @keyup.enter="prepareAddDomain" /><div class="form-hint">{{ tr('输入后按 Enter 先查看当前部署的 DNS 指引。', 'Press Enter to preview DNS records for this deployment.') }}</div></div>
    <div v-if="cloudflareStatus?.configured" class="dns-mode-actions"><button class="btn btn-primary btn-sm" type="button" :disabled="busy || !dnsPlan" @click="applyCloudflare"><UiIcon name="cloud" :size="14" />{{ tr('Cloudflare 一键配置', 'Configure with Cloudflare') }}</button></div>
    <div v-if="dnsPlan" class="dns-plan">
      <div class="dns-plan-head"><strong>{{ dnsPlan.zone ? `Cloudflare · ${dnsPlan.zone}` : tr('当前部署所需 DNS 记录', 'DNS records for this deployment') }}</strong><span class="status-chip neutral">{{ dnsPlan.items.length }}</span></div>
      <article v-for="item in dnsPlan.items" :key="`${item.record.type}-${item.record.name}`" class="dns-plan-row">
        <span class="dns-record-type">{{ item.record.type }}</span><code>{{ recordHost(item.record.name) }}</code><UiIcon name="next" :size="14" /><code>{{ item.record.content }}<template v-if="item.record.priority"> · {{ item.record.priority }}</template></code><button class="doc-inline-copy dns-copy-button" type="button" :aria-label="tr('复制 DNS 记录值', 'Copy DNS record value')" :title="tr('复制 DNS 记录值', 'Copy DNS record value')" @click="copyDNS(item.record.content)"><UiIcon name="copy" :size="15" /></button><span class="status-chip" :class="item.status === 'unchanged' ? 'success' : item.status === 'rolled_back' ? 'warn' : item.status === 'conflict' ? 'danger' : 'neutral'">{{ item.status === 'required' ? tr('需要配置', 'Required') : item.status === 'create' ? tr('将新增', 'Create') : item.status === 'created' ? tr('已新增', 'Created') : item.status === 'update' ? tr('已更新', 'Updated') : item.status === 'rolled_back' ? tr('已回滚', 'Rolled back') : item.status === 'unchanged' ? tr('无需修改', 'Unchanged') : item.status === 'conflict' ? tr('冲突', 'Conflict') : item.status }}</span>
      </article>
    </div>
    <div v-if="submitResult" class="success-banner">{{ tr('域名已提交，系统会持续检测 MX 配置。', 'Domain submitted. MX configuration will be checked automatically.') }}</div>
  </AppModal>
</template>
