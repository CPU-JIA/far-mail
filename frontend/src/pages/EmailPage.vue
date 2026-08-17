<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import UiIcon from '../components/UiIcon.vue'
import { api } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { localeState, tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import type { EmailMessage, Mailbox } from '../types/api'
import { copyText, extractCode, extractLink, formatDate, retentionLabel } from '../utils/format'

const route = useRoute()
const router = useRouter()
const mailboxId = String(route.params.mailboxId)
const emailId = String(route.params.emailId)
const mailbox = ref<Mailbox | null>(null)
const email = ref<EmailMessage | null>(null)
const loading = ref(true)
const error = ref('')

const bodyText = computed(() => email.value?.body_text || '')
const bodyHtml = computed(() => email.value?.body_html || '')
const code = computed(() => email.value?.parsed_code || extractCode(`${email.value?.subject || ''}\n${bodyText.value}\n${bodyHtml.value}`))
const link = computed(() => email.value?.parsed_link || extractLink(`${bodyText.value}\n${bodyHtml.value}`))
const linkHost = computed(() => {
  try { return link.value ? new URL(link.value).hostname : '' } catch { return tr('验证链接', 'Verification link') }
})

async function load(): Promise<void> {
  loading.value = true
  try {
    const [mailboxValue, emailValue] = await Promise.all([api.mailbox(mailboxId), api.email(mailboxId, emailId)])
    mailbox.value = mailboxValue
    email.value = emailValue
    setPageHeader(emailValue.subject || tr('(无主题)', '(No subject)'), tr(`来自：${emailValue.sender || '—'}`, `From: ${emailValue.sender || '—'}`), [
      { label: tr('返回列表', 'Back to inbox'), glyph: '←', run: () => router.push({ name: 'inbox', params: { mailboxId }, query: { address: mailboxValue.full_address } }) },
      { label: tr('删除邮件', 'Delete email'), tone: 'danger', glyph: '×', run: remove },
    ])
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('邮件加载失败', 'Unable to load email')
  } finally {
    loading.value = false
  }
}

async function remove(): Promise<void> {
  if (!await askConfirm({
    title: tr('删除邮件', 'Delete email'),
    message: tr('当前邮件将被永久删除。', 'This email will be permanently deleted.'),
    confirmLabel: tr('永久删除', 'Delete permanently'),
    danger: true,
  })) return
  try {
    await api.deleteEmail(mailboxId, emailId)
    toast(tr('邮件已删除', 'Email deleted'), 'success')
    await router.replace({ name: 'inbox', params: { mailboxId }, query: { address: mailbox.value?.full_address } })
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('删除失败', 'Delete failed'), 'error')
  }
}

async function copy(value: string): Promise<void> {
  await copyText(value)
  toast(tr('已复制', 'Copied'), 'success')
}

onMounted(load)
watch(() => localeState.locale, () => {
  if (!email.value || !mailbox.value) return
  setPageHeader(email.value.subject || tr('(无主题)', '(No subject)'), tr(`来自：${email.value.sender || '—'}`, `From: ${email.value.sender || '—'}`), [
    { label: tr('返回列表', 'Back to inbox'), glyph: '←', run: () => router.push({ name: 'inbox', params: { mailboxId }, query: { address: mailbox.value?.full_address } }) },
    { label: tr('删除邮件', 'Delete email'), tone: 'danger', glyph: '×', run: remove },
  ])
})
onBeforeUnmount(() => setPageHeader(tr('邮件内容', 'Email')))
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('邮件加载失败', 'Unable to load email')" :description="error" />
  <div v-else-if="mailbox && email" class="page-stack email-view-stack">
    <section class="section-card email-view-summary-card">
      <div class="section-head compact-head">
        <div><div class="section-kicker">{{ tr('邮件内容', 'EMAIL') }}</div><h2 class="section-title email-view-title">{{ email.subject || tr('(无主题)', '(No subject)') }}</h2><p class="section-desc">{{ tr('优先显示 HTML 正文，否则显示纯文本。', 'HTML is shown when available, with plain text as fallback.') }}</p></div>
        <div class="section-meta"><span class="status-chip neutral">{{ retentionLabel(mailbox.keep_forever, mailbox.expires_at) }}</span></div>
      </div>
      <div class="email-view-fact-grid email-view-fact-grid-compact">
        <article class="email-view-fact"><span>{{ tr('发件人', 'From') }}</span><strong>{{ email.sender || '—' }}</strong></article>
        <article class="email-view-fact"><span>{{ tr('收件人', 'To') }}</span><strong>{{ mailbox.full_address }}</strong></article>
        <article class="email-view-fact"><span>{{ tr('收件时间', 'Received') }}</span><strong>{{ formatDate(email.received_at) }}</strong></article>
        <article class="email-view-fact"><span>{{ tr('邮箱状态', 'Mailbox status') }}</span><strong>{{ mailbox.keep_forever ? tr('永久保留', 'Retained') : tr('自动过期', 'Auto-expiring') }}</strong></article>
      </div>
      <div class="email-signal-grid">
        <article class="email-signal-card" :class="code ? 'has-signal' : 'is-empty'">
          <div class="email-signal-head"><span>{{ tr('验证码', 'Verification code') }}</span><button v-if="code" class="icon-btn" type="button" :aria-label="tr('复制验证码', 'Copy verification code')" :title="tr('复制验证码', 'Copy verification code')" @click="copy(code)"><UiIcon name="copy" /></button></div>
          <strong>{{ code || tr('未识别', 'Not detected') }}</strong>
        </article>
        <article class="email-signal-card email-link-signal" :class="link ? 'has-signal' : 'is-empty'">
          <div class="email-signal-head">
            <span>{{ tr('验证链接', 'Verification link') }}</span>
            <div v-if="link" class="email-signal-actions"><button class="icon-btn" type="button" :aria-label="tr('复制验证链接', 'Copy verification link')" :title="tr('复制验证链接', 'Copy verification link')" @click="copy(link)"><UiIcon name="copy" /></button><a class="btn btn-primary btn-sm" :href="link" target="_blank" rel="noreferrer">{{ tr('打开', 'Open') }}</a></div>
          </div>
          <a v-if="link" class="email-link-card" :href="link" target="_blank" rel="noreferrer"><span>{{ linkHost }}</span><code>{{ link }}</code></a>
          <strong v-else>{{ tr('未识别', 'Not detected') }}</strong>
        </article>
      </div>
    </section>

    <section class="section-card email-content-shell" style="padding:0;overflow:hidden">
      <div class="email-detail-header">
        <div class="email-subject-big">{{ email.subject || tr('(无主题)', '(No subject)') }}</div>
        <div class="email-info-row"><span>{{ tr('发件人：', 'From: ') }}<strong>{{ email.sender || '—' }}</strong></span><span class="info-dot">•</span><span>{{ tr('收件人：', 'To: ') }}<strong>{{ mailbox.full_address }}</strong></span><span class="info-dot">•</span><span>{{ formatDate(email.received_at) }}</span></div>
      </div>
      <iframe v-if="bodyHtml" class="email-body-frame" :title="tr('邮件 HTML 正文', 'Email HTML body')" sandbox :srcdoc="bodyHtml"></iframe>
      <div v-else class="email-body-text" style="white-space:pre-wrap">{{ bodyText || tr('(邮件内容为空)', '(Email body is empty)') }}</div>
    </section>
  </div>
</template>
