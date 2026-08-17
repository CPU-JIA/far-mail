<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import UiIcon from '../components/UiIcon.vue'
import { api } from '../services/api'
import { authState } from '../stores/auth'
import { setPageHeader } from '../stores/ui'
import { localeState, tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import type { EmailSummary, Mailbox, MailboxEmailEvent } from '../types/api'
import { copyText, formatDate, mailboxParts, retentionLabel, timeAgo } from '../utils/format'

const route = useRoute()
const router = useRouter()
const mailboxId = String(route.params.mailboxId)
const mailbox = ref<Mailbox | null>(null)
const emails = ref<EmailSummary[]>([])
const loading = ref(true)
const error = ref('')
const pollError = ref('')
const realtimeState = ref<'connecting' | 'connected' | 'fallback'>('connecting')
const notificationsEnabled = ref(localStorage.getItem('far_mail_inbox_notifications') === 'true' && typeof Notification !== 'undefined' && Notification.permission === 'granted')
let pollTimer: number | undefined
let reconnectTimer: number | undefined
let connectionTimeout: number | undefined
let streamController: AbortController | undefined
let loadController: AbortController | undefined
let pollController: AbortController | undefined
let polling = false
let stopped = false
let refreshHandler: (() => void) | undefined

const parts = computed(() => mailboxParts(mailbox.value?.full_address || String(route.query.address || '')))
const refreshSeconds = computed(() => Math.max(1, Number(authState.publicSettings.inbox_refresh_seconds || 3) || 3))

async function load(showLoading = true): Promise<void> {
	loadController?.abort()
	const controller = new AbortController()
	loadController = controller
	if (showLoading) loading.value = true
	error.value = ''
	try {
		const [mailboxValue, emailPage] = await Promise.all([
		api.mailbox(mailboxId, controller.signal),
		api.emails(mailboxId, 1, 100, controller.signal),
		])
		if (controller.signal.aborted || stopped) return
		mailbox.value = mailboxValue
		emails.value = emailPage.data || []
		setHeader()
	} catch (cause) {
		if (controller.signal.aborted || stopped) return
		error.value = cause instanceof Error ? cause.message : tr('收件箱加载失败', 'Unable to load inbox')
	} finally {
		if (loadController === controller) {
			loadController = undefined
			loading.value = false
		}
	}
}

function setHeader(): void {
  const address = mailbox.value?.full_address || tr('收件箱', 'Inbox')
  setPageHeader(address, tr('邮件列表', 'Email list'), [
    { label: tr('复制地址', 'Copy address'), glyph: '□', run: copyAddress },
    { label: notificationsEnabled.value ? tr('关闭通知', 'Disable notifications') : tr('开启通知', 'Enable notifications'), glyph: notificationsEnabled.value ? 'bellOff' : 'bell', run: toggleNotifications },
    { label: mailbox.value?.keep_forever ? tr('取消永久保留', 'Use site policy') : tr('设为永久保留', 'Retain mailbox'), glyph: '∞', run: updateRetention },
    { label: tr('刷新', 'Refresh'), tone: 'primary', glyph: '↻', run: () => load(false) },
    { label: tr('返回', 'Back'), glyph: '←', run: () => router.push({ name: 'mailboxes' }) },
  ])
}

function onEmailEvent(event: MailboxEmailEvent): void {
  if (event.mailbox_id !== mailboxId || emails.value.some(item => item.id === event.email.id)) return
  emails.value = [event.email, ...emails.value]
  if (notificationsEnabled.value && document.visibilityState !== 'visible' && Notification.permission === 'granted') {
    const notice = new Notification(event.email.subject || tr('收到新邮件', 'New email received'), {
      body: `${event.email.sender || tr('未知发件人', 'Unknown sender')} · ${event.full_address}`,
      tag: `far-mail-email-${event.email.id}`,
    })
    notice.onclick = () => {
      window.focus()
      void router.push({ name: 'email', params: { mailboxId, emailId: event.email.id }, query: { address: event.full_address } })
      notice.close()
    }
  }
}

async function toggleNotifications(): Promise<void> {
  if (notificationsEnabled.value) {
    notificationsEnabled.value = false
    localStorage.removeItem('far_mail_inbox_notifications')
    setHeader()
    return
  }
  if (typeof Notification === 'undefined') {
    toast(tr('当前浏览器不支持系统通知', 'This browser does not support notifications'), 'warn')
    return
  }
  const permission = await Notification.requestPermission()
  if (permission !== 'granted') {
    toast(tr('浏览器未授予通知权限', 'Notification permission was not granted'), 'warn')
    return
  }
  notificationsEnabled.value = true
  localStorage.setItem('far_mail_inbox_notifications', 'true')
  toast(tr('新邮件通知已开启', 'New email notifications enabled'), 'success')
  setHeader()
}

async function pollOnce(): Promise<void> {
	if (polling || document.visibilityState !== 'visible' || realtimeState.value !== 'fallback') return
	polling = true
	const controller = new AbortController()
	pollController = controller
	try {
		const fresh = await api.emails(mailboxId, 1, 100, controller.signal)
		if (controller.signal.aborted || stopped) return
		const next = fresh.data || []
		if (next.length !== emails.value.length || next[0]?.id !== emails.value[0]?.id || next[next.length - 1]?.id !== emails.value[emails.value.length - 1]?.id) emails.value = next
	} catch (cause) {
		if (!controller.signal.aborted) pollError.value = cause instanceof Error ? cause.message : tr('自动刷新失败', 'Automatic refresh failed')
	}
	finally {
		if (pollController === controller) pollController = undefined
		polling = false
	}
}

function startFallbackPolling(): void {
  realtimeState.value = 'fallback'
  if (!pollTimer) pollTimer = window.setInterval(() => void pollOnce(), refreshSeconds.value * 1000)
  void pollOnce()
}

function stopFallbackPolling(): void {
	if (pollTimer) window.clearInterval(pollTimer)
	pollTimer = undefined
	pollController?.abort()
	pollController = undefined
}

function connectRealtime(): void {
  if (stopped) return
	streamController?.abort()
	if (connectionTimeout) window.clearTimeout(connectionTimeout)
	const controller = new AbortController()
  streamController = controller
  realtimeState.value = 'connecting'
  let opened = false
	connectionTimeout = window.setTimeout(() => {
    if (opened || controller.signal.aborted || stopped) return
    controller.abort()
    startFallbackPolling()
    reconnectTimer = window.setTimeout(connectRealtime, 5000)
  }, 10_000)
  void api.streamMailboxEvents(mailboxId, controller.signal, onEmailEvent, () => {
    opened = true
		window.clearTimeout(connectionTimeout!)
		connectionTimeout = undefined
    realtimeState.value = 'connected'
    stopFallbackPolling()
  })
    .then(() => { if (!controller.signal.aborted) throw new Error('stream closed') })
    .catch(() => {
      if (controller.signal.aborted || stopped) return
		if (connectionTimeout) window.clearTimeout(connectionTimeout)
		connectionTimeout = undefined
      startFallbackPolling()
      reconnectTimer = window.setTimeout(connectRealtime, 5000)
    })
}

async function copyAddress(): Promise<void> {
  if (!mailbox.value) return
  await copyText(mailbox.value.full_address)
  toast(tr('邮箱地址已复制', 'Mailbox address copied'), 'success')
}

async function updateRetention(): Promise<void> {
  if (!mailbox.value) return
  try {
    mailbox.value = await api.updateMailboxRetention(mailbox.value.id, !mailbox.value.keep_forever)
    toast(mailbox.value.keep_forever ? tr('已设为永久保留', 'Mailbox retained') : tr('已恢复自动过期', 'Site retention restored'), 'success')
    setHeader()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('更新失败', 'Update failed'), 'error')
  }
}

async function removeEmail(email: EmailSummary): Promise<void> {
  if (!await askConfirm({
    title: tr('删除邮件', 'Delete email'),
    message: tr(`“${email.subject || '无主题'}”将被永久删除。`, `“${email.subject || 'No subject'}” will be permanently deleted.`),
    confirmLabel: tr('永久删除', 'Delete permanently'),
    danger: true,
  })) return
  try {
    await api.deleteEmail(mailboxId, email.id)
    emails.value = emails.value.filter(item => item.id !== email.id)
    toast(tr('邮件已删除', 'Email deleted'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('删除失败', 'Delete failed'), 'error')
  }
}

onMounted(async () => {
  await load()
  if (!error.value) connectRealtime()
  refreshHandler = () => { if (document.visibilityState === 'visible') void pollOnce() }
  document.addEventListener('visibilitychange', refreshHandler)
})
watch(() => localeState.locale, setHeader)

onBeforeUnmount(() => {
	stopped = true
	loadController?.abort()
	streamController?.abort()
	if (connectionTimeout) window.clearTimeout(connectionTimeout)
  stopFallbackPolling()
  if (reconnectTimer) window.clearTimeout(reconnectTimer)
  if (refreshHandler) document.removeEventListener('visibilitychange', refreshHandler)
  setPageHeader(tr('收件箱', 'Inbox'))
})
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('收件箱加载失败', 'Unable to load inbox')" :description="error">
    <button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button>
  </EmptyState>
  <div v-else-if="mailbox" class="page-stack">
    <section class="hero-panel hero-panel-compact">
      <div>
        <div class="hero-kicker">{{ tr('收件箱', 'INBOX') }}</div>
        <h2 class="mailbox-long-title">{{ mailbox.full_address }}</h2>
        <p class="hero-desc">{{ realtimeState === 'connected' ? tr('新邮件将实时送达当前列表。', 'New email appears here in real time.') : realtimeState === 'connecting' ? tr('正在建立实时连接。', 'Connecting to the realtime stream.') : tr('实时连接恢复中，当前使用自动刷新。', 'Realtime is reconnecting; automatic refresh is active.') }}</p>
        <div class="hero-chip-row">
          <span class="hero-chip"><small>{{ tr('邮件总数', 'Emails') }}</small><strong>{{ emails.length }}</strong></span>
          <span class="hero-chip"><small>{{ tr('实时收信', 'Realtime') }}</small><strong>{{ realtimeState === 'connected' ? tr('已连接', 'Connected') : realtimeState === 'connecting' ? tr('连接中', 'Connecting') : tr('恢复中', 'Reconnecting') }}</strong></span>
          <span class="hero-chip"><small>{{ tr('保留状态', 'Retention') }}</small><strong>{{ retentionLabel(mailbox.keep_forever, mailbox.expires_at) }}</strong></span>
        </div>
      </div>
      <aside class="hero-side-card hero-side-card-tight">
        <div class="hero-side-label">{{ tr('当前邮箱', 'CURRENT MAILBOX') }}</div>
        <div class="hero-side-value inbox-current-address mailbox-address mailbox-address-split"><strong>{{ parts[0] }}</strong><span>{{ parts[1] }}</span></div>
        <p class="hero-side-desc">{{ mailbox.keep_forever ? tr('该邮箱不会被自动清理。', 'This mailbox is retained.') : tr('该邮箱遵循站点生命周期策略。', 'This mailbox follows the site retention policy.') }}</p>
      </aside>
    </section>

    <section class="email-list-card">
      <div class="email-list-head">
        <div><div class="section-kicker">{{ tr('邮件', 'EMAIL') }}</div><h3 class="section-title">{{ tr('收件列表', 'Received email') }}</h3><p class="section-desc">{{ tr('按收件时间倒序排列。', 'Newest email first.') }}</p></div>
        <div class="section-meta">{{ emails.length ? tr(`共 ${emails.length} 封`, `${emails.length} emails`) : tr('暂无邮件', 'No email') }}</div>
      </div>
      <div v-if="pollError" class="operations-data-warning" role="status"><span>{{ pollError }}</span><button class="btn btn-ghost btn-sm" type="button" @click="pollError = ''; void load(false)">{{ tr('重试', 'Retry') }}</button></div>
      <article v-for="email in emails" :key="email.id" class="email-item" role="button" tabindex="0" @click="router.push({ name: 'email', params: { mailboxId, emailId: email.id }, query: { address: mailbox.full_address } })" @keydown.enter="router.push({ name: 'email', params: { mailboxId, emailId: email.id }, query: { address: mailbox.full_address } })" @keydown.space.prevent="router.push({ name: 'email', params: { mailboxId, emailId: email.id }, query: { address: mailbox.full_address } })">
        <div class="email-avatar">{{ (email.sender || '?').charAt(0).toUpperCase() }}</div>
        <div class="email-meta">
          <div class="email-meta-top"><div class="email-from">{{ email.sender || tr('(无发件人)', '(No sender)') }}</div><span class="status-chip neutral">{{ timeAgo(email.received_at) }}</span></div>
          <div class="email-subject">{{ email.subject || tr('(无主题)', '(No subject)') }}</div>
          <div class="email-preview">{{ email.parsed_code ? tr(`验证码 ${email.parsed_code}`, `Code ${email.parsed_code}`) : email.parsed_link ? tr('含验证链接', 'Has verification link') : email.has_attachments ? tr('含附件', 'Has attachments') : tr('查看邮件正文', 'View email') }}</div>
        </div>
        <div class="email-item-side">
          <div class="email-time">{{ formatDate(email.received_at) }}</div>
          <button class="btn btn-ghost btn-sm icon-only-btn" type="button" :aria-label="tr('删除邮件', 'Delete email')" :title="tr('删除邮件', 'Delete email')" @click.stop="removeEmail(email)"><UiIcon name="trash" :size="14" /></button>
        </div>
      </article>
      <EmptyState v-if="!emails.length" icon="inbox" :title="tr('暂无邮件', 'No email')" :description="tr(`向 ${mailbox.full_address} 发送邮件后会显示在这里。`, `Email sent to ${mailbox.full_address} will appear here.`)" />
    </section>
  </div>
</template>
