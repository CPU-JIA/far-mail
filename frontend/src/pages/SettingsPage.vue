<script setup lang="ts">
import { computed, onMounted, reactive, ref, watchEffect } from 'vue'
import AppModal from '../components/AppModal.vue'
import BrandMark from '../components/BrandMark.vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import UiIcon from '../components/UiIcon.vue'
import { PRODUCT_NAME } from '../config/brand'
import { api } from '../services/api'
import { authState, loadPublicSettings, setAdminKeyAfterRotation } from '../stores/auth'
import { setPageHeader } from '../stores/ui'
import { tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import { askConfirm } from '../stores/confirm'
import { copyText, formatDate } from '../utils/format'
import type { CloudflareIntegrationStatus, NotificationIntegrationStatus } from '../types/api'

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const rotatedKey = ref('')
const keyModal = ref(false)
const showCurrentKey = ref(false)
const customAdminKey = ref('')
const settings = reactive<Record<string, string>>({})
const activeSection = ref<'site' | 'mail' | 'api' | 'integrations' | 'auth'>('site')
const notificationStatus = ref<NotificationIntegrationStatus | null>(null)
const cloudflareStatus = ref<CloudflareIntegrationStatus | null>(null)
const notificationForm = reactive({
  genericEnabled: false, genericUrl: '', genericSecret: '',
  telegramEnabled: false, telegramBotToken: '', telegramChatId: '',
  discordEnabled: false, discordUrl: '',
})
const cloudflareToken = ref('')
const integrationTestDomain = ref('')

function focusSection(index: number): void {
  const next = (index + settingSections.value.length) % settingSections.value.length
  activeSection.value = settingSections.value[next].key
  requestAnimationFrame(() => document.getElementById(`settings-tab-${settingSections.value[next].key}`)?.focus())
}

const settingSections = computed(() => [
  { key: 'site', label: tr('站点信息', 'Site'), description: tr('名称与图标', 'Name and icon') },
  { key: 'mail', label: tr('邮件服务', 'Mail'), description: tr('SMTP 与生命周期', 'SMTP and lifecycle') },
  { key: 'api', label: tr('API 策略', 'API policy'), description: tr('API Token 默认值', 'API Token defaults') },
  { key: 'integrations', label: tr('集成', 'Integrations'), description: tr('通知与 Cloudflare', 'Notifications and Cloudflare') },
  { key: 'auth', label: tr('登录密钥', 'Admin Key'), description: tr('查看与更新', 'View and rotate') },
] as const)

const textFields = computed(() => [
  { key: 'site_title', label: tr('站点名称', 'Site name'), hint: tr('显示在浏览器标题、登录页和侧栏。', 'Shown in the browser title, sign-in page, and sidebar.'), placeholder: PRODUCT_NAME },
])
const mailFields = computed(() => [
  { key: 'smtp_server_ip', label: tr('SMTP 服务器 IP', 'SMTP server IP'), hint: tr('用于 MX 校验和 SPF 提示。', 'Used for MX checks and SPF records.'), placeholder: '192.0.2.10' },
  { key: 'smtp_hostname', label: tr('SMTP 主机名', 'SMTP hostname'), hint: tr('域名 MX 记录应指向这里。', 'Domain MX records should point here.'), placeholder: 'mail.example.com' },
  { key: 'mailbox_ttl_minutes', label: tr('邮箱生命周期（分钟）', 'Mailbox lifetime (minutes)'), hint: tr('0 表示不自动过期。', '0 disables automatic expiry.'), placeholder: '30' },
  { key: 'email_retention_minutes', label: tr('邮件默认保留时长（分钟）', 'Email retention (minutes)'), hint: tr('默认 1440 分钟（24 小时）；0 表示不按时间自动删除。', 'Default 1440 minutes (24 hours); 0 disables age-based deletion.'), placeholder: '1440' },
  { key: 'inbox_refresh_seconds', label: tr('收件箱刷新周期（秒）', 'Inbox refresh interval (seconds)'), hint: tr('最小 2 秒。', 'Minimum 2 seconds.'), placeholder: '3' },
])
const tokenFields = computed(() => [
  { key: 'token_default_expires_days', label: tr('默认有效天数', 'Default validity (days)'), hint: tr('新 API Token 的默认有效期。', 'Default validity for new API Tokens.'), placeholder: '30' },
  { key: 'token_default_rate_limit_per_minute', label: tr('默认 RPM', 'Default RPM'), hint: tr('0 表示不限。', '0 means unlimited.'), placeholder: '0' },
  { key: 'token_default_daily_request_limit', label: tr('默认每日限制', 'Default daily limit'), hint: tr('0 表示不限。', '0 means unlimited.'), placeholder: '0' },
  { key: 'token_default_total_request_limit', label: tr('默认总量限制', 'Default total limit'), hint: tr('0 表示不限。', '0 means unlimited.'), placeholder: '0' },
])

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [siteSettings, notifications, cloudflare] = await Promise.all([api.settings(), api.notificationIntegrations(), api.cloudflareIntegration()])
    Object.assign(settings, siteSettings)
    notificationStatus.value = notifications
    cloudflareStatus.value = cloudflare
    notificationForm.genericEnabled = notifications.generic.enabled
    notificationForm.telegramEnabled = notifications.telegram.enabled
    notificationForm.telegramChatId = notifications.telegram.chat_id || ''
    notificationForm.discordEnabled = notifications.discord.enabled
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('站点设置加载失败', 'Unable to load site settings')
  } finally {
    loading.value = false
  }
}

function notificationPayload(clearChannel = ''): Record<string, unknown> {
  return {
    generic: { enabled: notificationForm.genericEnabled, url: notificationForm.genericUrl, secret: notificationForm.genericSecret, clear: clearChannel === 'generic' },
    telegram: { enabled: notificationForm.telegramEnabled, bot_token: notificationForm.telegramBotToken, chat_id: notificationForm.telegramChatId, clear: clearChannel === 'telegram' },
    discord: { enabled: notificationForm.discordEnabled, url: notificationForm.discordUrl, clear: clearChannel === 'discord' },
  }
}

async function saveNotifications(clearChannel = ''): Promise<void> {
  busy.value = true
  try {
    await api.saveNotificationIntegrations(notificationPayload(clearChannel))
    notificationStatus.value = await api.notificationIntegrations()
    notificationForm.genericEnabled = notificationStatus.value.generic.enabled
    notificationForm.telegramEnabled = notificationStatus.value.telegram.enabled
    notificationForm.telegramChatId = notificationStatus.value.telegram.chat_id || ''
    notificationForm.discordEnabled = notificationStatus.value.discord.enabled
    notificationForm.genericUrl = ''
    notificationForm.genericSecret = ''
    notificationForm.telegramBotToken = ''
    notificationForm.discordUrl = ''
    toast(clearChannel ? tr('通知渠道已清除', 'Notification channel cleared') : tr('通知集成已保存', 'Notification integrations saved'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('通知集成保存失败', 'Unable to save notification integrations'), 'error')
  } finally { busy.value = false }
}

async function testNotification(channel: 'generic' | 'telegram' | 'discord'): Promise<void> {
  busy.value = true
  try {
    await api.testNotificationIntegration(channel)
    notificationStatus.value = await api.notificationIntegrations()
    toast(tr('测试通知已送达', 'Test notification delivered'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('测试通知失败', 'Test notification failed'), 'error')
  } finally { busy.value = false }
}

async function saveCloudflare(clear = false): Promise<void> {
  if (!clear && !cloudflareToken.value.trim()) {
    toast(tr('请输入 Cloudflare API Token', 'Enter a Cloudflare API Token'), 'warn')
    return
  }
  busy.value = true
  try {
    await api.saveCloudflareIntegration(cloudflareToken.value, clear)
    cloudflareToken.value = ''
    cloudflareStatus.value = await api.cloudflareIntegration()
    toast(clear ? tr('Cloudflare 配置已清除', 'Cloudflare configuration cleared') : tr('Cloudflare 配置已保存', 'Cloudflare configuration saved'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('Cloudflare 配置保存失败', 'Unable to save Cloudflare configuration'), 'error')
  } finally { busy.value = false }
}

async function testCloudflare(): Promise<void> {
  busy.value = true
  try {
    const result = await api.testCloudflareIntegration(integrationTestDomain.value.trim())
    toast(result.zone ? tr(`已连接 Zone：${result.zone}`, `Connected to zone: ${result.zone}`) : tr('Cloudflare Token 有效', 'Cloudflare Token is valid'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('Cloudflare 连接失败', 'Cloudflare connection failed'), 'error')
  } finally { busy.value = false }
}

async function save(key: string): Promise<void> {
  busy.value = true
  try {
    await api.saveSettings({ [key]: settings[key] || '' })
    await loadPublicSettings()
    toast(tr('设置已保存', 'Setting saved'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('保存失败', 'Save failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function uploadLogo(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (!file.type.startsWith('image/')) {
    toast(tr('请选择图片文件', 'Choose an image file'), 'warn')
    return
  }
  if (file.size > 512 * 1024) {
    toast(tr('图标文件不能超过 512 KB', 'The icon must be 512 KB or smaller'), 'warn')
    return
  }
  busy.value = true
  try {
    const dataUrl = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
    })
    settings.site_logo_url = dataUrl
    await save('site_logo_url')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('图标读取失败', 'Unable to read icon'), 'error')
  } finally {
    busy.value = false
    input.value = ''
  }
}

async function clearLogo(): Promise<void> {
  settings.site_logo_url = ''
  await save('site_logo_url')
}

async function rotateAdminKey(): Promise<void> {
  const value = customAdminKey.value.trim()
  if (value && !/^sk-[a-z0-9_-]{1,24}-(?:[0-9a-f]{16}|[0-9a-f]{32})$/.test(value)) {
    toast(tr('密钥格式无效，请检查后重试', 'Invalid Admin Key format'), 'warn')
    return
  }
  if (!await askConfirm({
    title: tr('更新登录密钥', 'Update Admin Key'),
    message: value
      ? tr('使用新的登录密钥后，当前密钥会立即失效。', 'The current Admin Key will stop working immediately after this change.')
      : tr('生成新登录密钥后，当前密钥会立即失效。', 'The current Admin Key will stop working immediately after rotation.'),
    confirmLabel: value ? tr('更新密钥', 'Update key') : tr('生成新密钥', 'Generate key'),
    danger: true,
  })) return
  busy.value = true
  try {
    const result = await api.rotateAdminKey(value)
    rotatedKey.value = result.admin_auth_key
    setAdminKeyAfterRotation(result.admin_auth_key)
    customAdminKey.value = ''
    keyModal.value = true
    toast(tr('登录密钥已更新', 'Admin Key updated'), 'success')
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('登录密钥更新失败', 'Admin Key update failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function copyKey(): Promise<void> {
  await copyText(rotatedKey.value)
  toast(tr('登录密钥已复制', 'Admin Key copied'), 'success')
}

async function copyCurrentAdminKey(): Promise<void> {
  await copyText(authState.adminKey)
  toast(tr('当前密钥已复制', 'Current Admin Key copied'), 'success')
}


watchEffect(() => setPageHeader(tr('站点设置', 'Site settings'), tr('站点、邮件服务和凭据默认策略', 'Site, mail service, and credential defaults')))
onMounted(() => void load())
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('站点设置加载失败', 'Unable to load site settings')" :description="error"><button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></EmptyState>
  <div v-else class="page-stack settings-page-v18">
    <nav class="settings-tabs" role="tablist" :aria-label="tr('设置分区', 'Settings sections')">
      <button
        v-for="section in settingSections"
        :key="section.key"
        class="settings-tab"
        :class="{ active: activeSection === section.key }"
        type="button"
        role="tab"
        :id="`settings-tab-${section.key}`"
        :aria-selected="activeSection === section.key"
        :aria-controls="`settings-panel-${section.key}`"
        :tabindex="activeSection === section.key ? 0 : -1"
        @click="activeSection = section.key"
        @keydown.right.prevent="focusSection(settingSections.findIndex(item => item.key === section.key) + 1)"
        @keydown.left.prevent="focusSection(settingSections.findIndex(item => item.key === section.key) - 1)"
      >
        <strong>{{ section.label }}</strong>
        <small>{{ section.description }}</small>
      </button>
    </nav>

    <section id="settings-panel-site" v-show="activeSection === 'site'" class="section-card" role="tabpanel" aria-labelledby="settings-tab-site">
      <div class="section-head"><div><div class="section-kicker">{{ tr('品牌', 'BRAND') }}</div><h3 class="section-title">{{ tr('站点信息', 'Site identity') }}</h3><p class="section-desc">{{ tr('调整站点名称和图标。', 'Update the site name and icon.') }}</p></div></div>
      <div class="settings-grid">
        <article v-for="field in textFields" :key="field.key" class="setting-item-card">
          <div class="form-group"><label class="form-label" :for="`setting-${field.key}`">{{ field.label }}</label><div class="setting-input-row"><input :id="`setting-${field.key}`" v-model="settings[field.key]" class="form-input" :placeholder="field.placeholder" :maxlength="field.key === 'site_title' ? 80 : 500" /><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="save(field.key)">{{ tr('保存', 'Save') }}</button></div><div class="form-hint">{{ field.hint }}</div></div>
        </article>
        <article class="setting-item-card">
          <div class="form-group"><label class="form-label" for="site-logo-upload">{{ tr('站点图标', 'Site icon') }}</label><div class="settings-logo-row"><img v-if="authState.publicSettings.site_logo_url" :src="String(authState.publicSettings.site_logo_url)" :alt="tr('当前站点图标', 'Current site icon')" /><span v-else class="logo-mark"><BrandMark /></span><label class="file-picker btn btn-ghost btn-sm" for="site-logo-upload">{{ tr('选择图片', 'Choose image') }}</label><input id="site-logo-upload" class="visually-hidden" type="file" accept="image/*" :aria-label="tr('上传站点图标', 'Upload site icon')" @change="uploadLogo" /></div><div class="setting-action-row"><span class="form-hint">{{ tr('最大 512 KB，上传后立即生效。', 'Maximum 512 KB. Changes apply immediately.') }}</span><button class="btn btn-ghost btn-sm" type="button" :disabled="busy" @click="clearLogo">{{ tr('恢复默认图标', 'Restore default') }}</button></div></div>
        </article>
      </div>
    </section>

    <section id="settings-panel-mail" v-show="activeSection === 'mail'" class="section-card" role="tabpanel" aria-labelledby="settings-tab-mail">
      <div class="section-head"><div><div class="section-kicker">{{ tr('邮件', 'MAIL') }}</div><h3 class="section-title">{{ tr('邮件服务', 'Mail service') }}</h3><p class="section-desc">{{ tr('域名验证、邮箱生命周期和收件箱刷新。', 'Domain checks, mailbox lifetime, and inbox refresh.') }}</p></div></div>
      <div class="settings-grid">
        <article v-for="field in mailFields" :key="field.key" class="setting-item-card"><div class="form-group"><label class="form-label" :for="`setting-${field.key}`">{{ field.label }}</label><div class="setting-input-row"><input :id="`setting-${field.key}`" v-model="settings[field.key]" class="form-input" :placeholder="field.placeholder" :type="field.key.includes('minutes') || field.key.includes('seconds') ? 'number' : 'text'" :min="field.key === 'inbox_refresh_seconds' ? 2 : 0" /><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="save(field.key)">{{ tr('保存', 'Save') }}</button></div><div class="form-hint">{{ field.hint }}</div></div></article>
      </div>
    </section>

    <section id="settings-panel-api" v-show="activeSection === 'api'" class="section-card" role="tabpanel" aria-labelledby="settings-tab-api">
      <div class="section-head"><div><div class="section-kicker">API</div><h3 class="section-title">{{ tr('API Token 默认策略', 'API Token defaults') }}</h3><p class="section-desc">{{ tr('仅用于新签发的自动化 API Token。', 'Applied only to newly issued automation API Tokens.') }}</p></div></div>
      <div class="settings-grid">
        <article v-for="field in tokenFields" :key="field.key" class="setting-item-card"><div class="form-group"><label class="form-label" :for="`setting-${field.key}`">{{ field.label }}</label><div class="setting-input-row"><input :id="`setting-${field.key}`" v-model="settings[field.key]" class="form-input" type="number" min="0" :placeholder="field.placeholder" /><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="save(field.key)">{{ tr('保存', 'Save') }}</button></div><div class="form-hint">{{ field.hint }}</div></div></article>
      </div>
    </section>

    <section id="settings-panel-integrations" v-show="activeSection === 'integrations'" class="section-card" role="tabpanel" aria-labelledby="settings-tab-integrations">
      <div class="section-head"><div><div class="section-kicker">{{ tr('事件', 'EVENTS') }}</div><h3 class="section-title">{{ tr('通知集成', 'Notification integrations') }}</h3><p class="section-desc">{{ tr('新邮件到达后向已启用的渠道发送最小化事件数据。', 'Send minimal event data to enabled channels when email arrives.') }}</p></div><div v-if="notificationStatus?.delivery.last_success_at" class="section-meta">{{ tr('最近投递成功', 'Last delivered') }} · {{ formatDate(notificationStatus.delivery.last_success_at) }}</div></div>
      <div class="integration-grid">
        <article class="integration-card">
          <div class="integration-card-head"><span class="integration-icon"><UiIcon name="webhook" :size="18" /></span><div><strong>Generic Webhook</strong><small>{{ notificationStatus?.generic.configured ? notificationStatus.generic.target : tr('未配置', 'Not configured') }}</small></div><label class="switch-control"><input v-model="notificationForm.genericEnabled" type="checkbox" :aria-label="tr('启用 Generic Webhook', 'Enable Generic Webhook')"><span></span></label></div>
          <div class="form-group"><label class="form-label" for="generic-webhook-url">Webhook URL</label><input id="generic-webhook-url" v-model="notificationForm.genericUrl" class="form-input" type="url" placeholder="https://example.com/hooks/mail"></div>
          <div class="form-group"><label class="form-label" for="generic-webhook-secret">{{ tr('签名密钥', 'Signing secret') }}</label><input id="generic-webhook-secret" v-model="notificationForm.genericSecret" class="form-input" type="password" autocomplete="new-password" :placeholder="notificationStatus?.generic.signed ? tr('已配置，留空不修改', 'Configured; leave empty to keep') : tr('可选', 'Optional')"></div>
          <div class="integration-actions"><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="saveNotifications()">{{ tr('保存', 'Save') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !notificationStatus?.generic.configured" @click="testNotification('generic')">{{ tr('测试', 'Test') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !notificationStatus?.generic.configured" @click="saveNotifications('generic')">{{ tr('清除', 'Clear') }}</button></div>
        </article>
        <article class="integration-card">
          <div class="integration-card-head"><span class="integration-icon"><UiIcon name="send" :size="18" /></span><div><strong>Telegram Bot</strong><small>{{ notificationStatus?.telegram.configured ? `Chat ID ${notificationStatus.telegram.chat_id}` : tr('未配置', 'Not configured') }}</small></div><label class="switch-control"><input v-model="notificationForm.telegramEnabled" type="checkbox" :aria-label="tr('启用 Telegram', 'Enable Telegram')"><span></span></label></div>
          <div class="form-group"><label class="form-label" for="telegram-bot-token">Bot Token</label><input id="telegram-bot-token" v-model="notificationForm.telegramBotToken" class="form-input" type="password" autocomplete="new-password" :placeholder="notificationStatus?.telegram.configured ? tr('已配置，留空不修改', 'Configured; leave empty to keep') : '123456:ABC...'"></div>
          <div class="form-group"><label class="form-label" for="telegram-chat-id">Chat ID</label><input id="telegram-chat-id" v-model="notificationForm.telegramChatId" class="form-input" placeholder="-1001234567890"></div>
          <div class="integration-actions"><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="saveNotifications()">{{ tr('保存', 'Save') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !notificationStatus?.telegram.configured" @click="testNotification('telegram')">{{ tr('测试', 'Test') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !notificationStatus?.telegram.configured" @click="saveNotifications('telegram')">{{ tr('清除', 'Clear') }}</button></div>
        </article>
        <article class="integration-card">
          <div class="integration-card-head"><span class="integration-icon"><UiIcon name="message" :size="18" /></span><div><strong>Discord Webhook</strong><small>{{ notificationStatus?.discord.configured ? notificationStatus.discord.target : tr('未配置', 'Not configured') }}</small></div><label class="switch-control"><input v-model="notificationForm.discordEnabled" type="checkbox" :aria-label="tr('启用 Discord', 'Enable Discord')"><span></span></label></div>
          <div class="form-group"><label class="form-label" for="discord-webhook-url">Webhook URL</label><input id="discord-webhook-url" v-model="notificationForm.discordUrl" class="form-input" type="password" autocomplete="new-password" :placeholder="notificationStatus?.discord.configured ? tr('已配置，留空不修改', 'Configured; leave empty to keep') : 'https://discord.com/api/webhooks/...'"></div>
          <div class="integration-actions"><button class="btn btn-primary btn-sm" type="button" :disabled="busy" @click="saveNotifications()">{{ tr('保存', 'Save') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !notificationStatus?.discord.configured" @click="testNotification('discord')">{{ tr('测试', 'Test') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !notificationStatus?.discord.configured" @click="saveNotifications('discord')">{{ tr('清除', 'Clear') }}</button></div>
        </article>
      </div>

      <div class="section-head integration-section-break"><div><div class="section-kicker">DNS</div><h3 class="section-title">Cloudflare</h3><p class="section-desc">{{ tr('用于域名页的 DNS 预览与一键配置。', 'Used for DNS preview and one-click configuration on the Domains page.') }}</p></div><span class="status-chip" :class="cloudflareStatus?.configured ? 'success' : 'neutral'">{{ cloudflareStatus?.configured ? tr('已配置', 'Configured') : tr('未配置', 'Not configured') }}</span></div>
      <div class="integration-cloudflare-row">
        <div class="form-group"><label class="form-label" for="cloudflare-token">Cloudflare API Token</label><input id="cloudflare-token" v-model="cloudflareToken" class="form-input" type="password" autocomplete="new-password" :disabled="cloudflareStatus?.source === 'environment'" :placeholder="cloudflareStatus?.source === 'environment' ? tr('由环境变量管理', 'Managed by environment') : cloudflareStatus?.configured ? tr('已配置，输入新值可替换', 'Configured; enter a new value to replace') : tr('Zone:Read + Zone:DNS:Edit', 'Zone:Read + Zone:DNS:Edit')"></div>
        <div class="form-group"><label class="form-label" for="cloudflare-test-zone">{{ tr('测试 Zone（可选）', 'Test zone (optional)') }}</label><input id="cloudflare-test-zone" v-model="integrationTestDomain" class="form-input" placeholder="example.com"></div>
      </div>
      <div class="integration-actions"><button class="btn btn-primary btn-sm" type="button" :disabled="busy || cloudflareStatus?.source === 'environment'" @click="saveCloudflare(false)">{{ tr('保存 Token', 'Save Token') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !cloudflareStatus?.configured" @click="testCloudflare">{{ tr('验证连接', 'Verify connection') }}</button><button class="btn btn-ghost btn-sm" type="button" :disabled="busy || !cloudflareStatus?.configured || cloudflareStatus?.source === 'environment'" @click="saveCloudflare(true)">{{ tr('清除', 'Clear') }}</button></div>
      <div v-if="notificationStatus?.delivery.last_error" class="form-hint integration-error">{{ tr('最近投递错误', 'Last delivery error') }}：{{ notificationStatus.delivery.last_error }}</div>
    </section>

    <section id="settings-panel-auth" v-show="activeSection === 'auth'" class="section-card settings-auth-panel" role="tabpanel" aria-labelledby="settings-tab-auth">
      <div class="section-head"><div><div class="section-kicker">{{ tr('访问控制', 'ACCESS') }}</div><h3 class="section-title">{{ tr('登录密钥', 'Admin Key') }}</h3></div></div>
      <div class="settings-auth-form">
        <div class="form-group"><label class="form-label">{{ tr('当前密钥', 'Current Admin Key') }}</label><div class="admin-key-current"><code>{{ showCurrentKey ? authState.adminKey : `${authState.adminKey.slice(0, 8)}${'•'.repeat(Math.max(0, authState.adminKey.length - 12))}${authState.adminKey.slice(-4)}` }}</code><button class="icon-btn" type="button" :aria-label="showCurrentKey ? tr('隐藏当前密钥', 'Hide current Admin Key') : tr('显示当前密钥', 'Reveal current Admin Key')" :title="showCurrentKey ? tr('隐藏当前密钥', 'Hide current Admin Key') : tr('显示当前密钥', 'Reveal current Admin Key')" @click="showCurrentKey = !showCurrentKey"><UiIcon :name="showCurrentKey ? 'eyeOff' : 'eye'" :size="16" /></button><button class="doc-inline-copy" type="button" :aria-label="tr('复制当前密钥', 'Copy current Admin Key')" :title="tr('复制当前密钥', 'Copy current Admin Key')" @click="copyCurrentAdminKey"><UiIcon name="copy" :size="16" /></button></div></div>
        <div class="form-group"><label class="form-label" for="custom-admin-key">{{ tr('新密钥（可选）', 'New Admin Key (optional)') }}</label><div class="setting-input-row"><input id="custom-admin-key" v-model="customAdminKey" class="form-input" type="password" placeholder="sk-mail-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" autocomplete="new-password" /><button class="btn btn-danger btn-sm" type="button" :disabled="busy" @click="rotateAdminKey">{{ tr('更新密钥', 'Update key') }}</button></div><div class="form-hint">{{ tr('留空自动生成。', 'Leave blank to generate.') }}</div></div>
      </div>
    </section>

  </div>

  <AppModal v-if="keyModal" :title="tr('新的登录密钥', 'New Admin Key')" :confirm-label="tr('完成', 'Done')" @close="keyModal = false" @confirm="keyModal = false">
    <div class="api-key-secret-result"><span class="secret-value is-revealed">{{ rotatedKey }}</span><button class="icon-btn" type="button" :aria-label="tr('复制登录密钥', 'Copy Admin Key')" :title="tr('复制登录密钥', 'Copy Admin Key')" @click="copyKey"><UiIcon name="copy" :size="16" /></button></div>
  </AppModal>
</template>
