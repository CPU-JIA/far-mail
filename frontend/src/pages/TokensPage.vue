<script setup lang="ts">
import { onMounted, reactive, ref, watchEffect } from 'vue'
import AppModal from '../components/AppModal.vue'
import UiIcon from '../components/UiIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import { api } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { localeState, tr } from '../stores/i18n'
import { toast } from '../stores/toast'
import type { AccountToken } from '../types/api'
import { copyText, formatMetric, timeAgo } from '../utils/format'

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const tokens = ref<AccountToken[]>([])
const modal = ref<'form' | 'secret' | 'confirm' | null>(null)
const editing = ref<AccountToken | null>(null)
const secret = ref('')
const secretTitle = ref('API Token')
const revealedByToken = ref<Record<string, string>>({})
const actionTokenID = ref<string | null>(null)
const pendingAction = ref<{ type: 'rotate' | 'delete'; token: AccountToken } | null>(null)
const form = reactive({ name: '', scope: 'read', expiresInDays: 30, permanent: false, rpm: 0, daily: 0, total: 0 })

function rememberSecret(tokenID: string, value: string): void {
  const next = { ...revealedByToken.value, [tokenID]: value }
  revealedByToken.value = next
}

function forgetSecret(tokenID: string): void {
  const next = { ...revealedByToken.value }
  delete next[tokenID]
  revealedByToken.value = next
}

function scopeLabel(scope: string): string {
  if (scope === 'owner') return tr('API 完全控制', 'Full API access')
  if (scope === 'cleanup') return tr('读写与清理', 'Read, write, and cleanup')
  return tr('只读查询', 'Read only')
}

function maskedToken(token: AccountToken): string {
  return `${token.token_prefix}********`
}

function copyableToken(token: AccountToken): string {
  return revealedByToken.value[token.id] || ''
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    tokens.value = await api.tokens()
    const tokenIDs = new Set(tokens.value.map(token => token.id))
    const knownSecrets = Object.fromEntries(Object.entries(revealedByToken.value).filter(([id]) => tokenIDs.has(id)))
    revealedByToken.value = knownSecrets
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('API Token 加载失败', 'Unable to load API Tokens')
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  Object.assign(form, { name: '', scope: 'read', expiresInDays: 30, permanent: false, rpm: 0, daily: 0, total: 0 })
  modal.value = 'form'
}

function openEdit(token: AccountToken): void {
  editing.value = token
  Object.assign(form, {
    name: token.name,
    scope: token.scope,
    expiresInDays: 0,
    permanent: !token.expires_at,
    rpm: token.rate_limit_per_minute,
    daily: token.daily_request_limit,
    total: token.total_request_limit,
  })
  modal.value = 'form'
}

async function save(): Promise<void> {
  busy.value = true
  const payload = {
    name: form.name.trim() || tr(`${scopeLabel(form.scope)}脚本`, `${scopeLabel(form.scope)} script`),
    scope: form.scope,
    rate_limit_per_minute: Number(form.rpm || 0),
    daily_request_limit: Number(form.daily || 0),
    total_request_limit: Number(form.total || 0),
    expires_in_days: form.permanent ? -1 : Number(form.expiresInDays || 0),
    permanent: form.permanent,
    keep_expiry: Boolean(editing.value && !form.permanent && !form.expiresInDays),
  }
  try {
    if (editing.value) {
      await api.updateToken(editing.value.id, payload)
      modal.value = null
      toast(tr('API Token 已更新', 'API Token updated'), 'success')
    } else {
      const response = await api.createToken(payload)
      rememberSecret(response.token.id, response.access_token)
      showSecret(tr('API Token 已创建', 'API Token created'), response.access_token)
      toast(tr('API Token 已创建', 'API Token created'), 'success')
    }
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('保存失败', 'Save failed'), 'error')
  } finally {
    busy.value = false
  }
}

function showSecret(title: string, value: string): void {
  secretTitle.value = title
  secret.value = value
  modal.value = 'secret'
}

function requestAction(type: 'rotate' | 'delete', token: AccountToken): void {
  if (actionTokenID.value) return
  pendingAction.value = { type, token }
  modal.value = 'confirm'
}

async function confirmAction(): Promise<void> {
  const action = pendingAction.value
  if (!action || actionTokenID.value) return
  actionTokenID.value = action.token.id
  try {
    if (action.type === 'rotate') {
      const response = await api.rotateToken(action.token.id)
      rememberSecret(response.token.id, response.access_token)
      pendingAction.value = null
      showSecret(tr('API Token 已重新生成', 'API Token regenerated'), response.access_token)
    } else {
      await api.deleteToken(action.token.id)
      forgetSecret(action.token.id)
      pendingAction.value = null
      modal.value = null
      toast(tr('API Token 已删除', 'API Token deleted'), 'success')
    }
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : action.type === 'rotate' ? tr('重新生成失败', 'Regeneration failed') : tr('删除失败', 'Delete failed'), 'error')
  } finally {
    actionTokenID.value = null
  }
}

async function copyToken(token: AccountToken): Promise<void> {
  const value = copyableToken(token)
  if (value) {
    await copyText(value)
    toast(tr('API Token 已复制', 'API Token copied'), 'success')
    return
  }
  toast(tr('完整 Token 当前不可复制，请使用右侧重新生成按钮签发新 Token', 'The full Token is unavailable. Regenerate it before copying.'), 'warn')
}

async function toggle(token: AccountToken): Promise<void> {
  if (actionTokenID.value) return
  actionTokenID.value = token.id
  try {
    if (token.status === 'disabled') await api.enableToken(token.id)
    else await api.disableToken(token.id)
    toast(token.status === 'disabled' ? tr('API Token 已启用', 'API Token enabled') : tr('API Token 已禁用', 'API Token disabled'), 'success')
    await load()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('操作失败', 'Operation failed'), 'error')
  } finally {
    actionTokenID.value = null
  }
}

async function copySecret(): Promise<void> {
  await copyText(secret.value)
  toast(tr('API Token 已复制', 'API Token copied'), 'success')
}

onMounted(() => {
  void load()
})
watchEffect(() => setPageHeader('API Token', tr('自动化访问凭据', 'Automation credentials'), [{ label: tr('新建 API Token', 'New API Token'), tone: 'primary', glyph: '+', run: openCreate }]))
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('API Token 加载失败', 'Unable to load API Tokens')" :description="error"><button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></EmptyState>
  <div v-else class="console-page api-key-page-v18">
    <section class="section-card api-key-table-panel">
      <div v-if="tokens.length" class="api-key-table-wrap api-key-table-wrap-first">
        <div class="api-key-table" role="table" :aria-label="tr('API Token 列表', 'API Token list')">
          <div class="api-key-table-head" role="row">
            <span role="columnheader">{{ tr('名称', 'Name') }}</span><span role="columnheader">Token</span><span role="columnheader">{{ tr('状态', 'Status') }}</span><span role="columnheader">RPM</span><span role="columnheader">{{ tr('用量限制', 'Usage limit') }}</span><span role="columnheader">{{ tr('截止日期', 'Expires') }}</span><span role="columnheader">{{ tr('最近使用', 'Last used') }}</span><span role="columnheader" :aria-label="tr('操作', 'Actions')"></span>
          </div>
          <article v-for="token in tokens" :key="token.id" class="api-key-table-row" :class="{ 'is-disabled': token.status === 'disabled', 'is-expired': token.status === 'expired' }" role="row">
            <div class="api-key-name-cell"><strong>{{ token.name || tr('未命名 Token', 'Unnamed Token') }}</strong><small>{{ scopeLabel(token.scope) }}</small></div>
            <div class="api-key-value-cell"><code class="api-key-masked-value">{{ maskedToken(token) }}</code><button class="icon-btn" :class="{ 'is-reissue': !copyableToken(token) }" type="button" :disabled="actionTokenID === token.id" :aria-label="copyableToken(token) ? tr('复制 API Token', 'Copy API Token') : tr('完整 Token 当前不可复制', 'Full Token unavailable')" :title="copyableToken(token) ? tr('复制 API Token', 'Copy API Token') : tr('重新生成后可复制', 'Regenerate before copying')" @click="copyToken(token)"><UiIcon name="copy" :size="15" /></button></div>
            <span class="status-chip" :class="token.status === 'active' ? 'success' : token.status === 'expired' ? 'warn' : 'neutral'">{{ token.status === 'active' ? tr('可用', 'Active') : token.status === 'expired' ? tr('已过期', 'Expired') : tr('已禁用', 'Disabled') }}</span>
            <span class="api-key-table-value" data-label="RPM">{{ token.rate_limit_per_minute > 0 ? token.rate_limit_per_minute : tr('不限', 'Unlimited') }}</span>
            <span class="api-key-usage-cell" :data-label="tr('用量', 'Usage')"><strong>{{ token.total_request_limit > 0 ? formatMetric(token.total_request_limit) : tr('无限制', 'Unlimited') }}</strong><small>{{ tr(`已使用 ${formatMetric(token.request_count_total)}`, `${formatMetric(token.request_count_total)} used`) }}</small></span>
            <span class="api-key-table-value" :data-label="tr('截止日期', 'Expires')">{{ token.expires_at ? new Date(token.expires_at).toLocaleDateString(localeState.locale) : tr('永不过期', 'Never') }}</span>
            <span class="api-key-table-value" :data-label="tr('最近使用', 'Last used')">{{ token.last_used_at ? timeAgo(token.last_used_at) : '-' }}</span>
            <div class="api-key-table-actions">
              <button class="icon-btn" type="button" :disabled="actionTokenID === token.id" :aria-label="tr('重新生成 API Token', 'Regenerate API Token')" :title="tr('重新生成 API Token', 'Regenerate API Token')" @click="requestAction('rotate', token)"><UiIcon name="refresh" :size="15" /></button>
              <button class="icon-btn" type="button" :disabled="actionTokenID === token.id" :aria-label="tr('编辑 API Token', 'Edit API Token')" :title="tr('编辑 API Token', 'Edit API Token')" @click="openEdit(token)"><UiIcon name="edit" :size="15" /></button>
              <button class="icon-btn" type="button" :disabled="actionTokenID === token.id" :aria-label="token.status === 'disabled' ? tr('启用 API Token', 'Enable API Token') : tr('禁用 API Token', 'Disable API Token')" :title="token.status === 'disabled' ? tr('启用 API Token', 'Enable API Token') : tr('禁用 API Token', 'Disable API Token')" @click="toggle(token)"><UiIcon :name="token.status === 'disabled' ? 'play' : 'pause'" :size="15" /></button>
              <button class="icon-btn danger" type="button" :disabled="actionTokenID === token.id" :aria-label="tr('删除 API Token', 'Delete API Token')" :title="tr('删除 API Token', 'Delete API Token')" @click="requestAction('delete', token)"><UiIcon name="trash" :size="15" /></button>
            </div>
          </article>
        </div>
      </div>
      <EmptyState v-else icon="key" :title="tr('暂无 API Token', 'No API Tokens')" :description="tr('为自动化脚本、CI 或清理任务签发第一枚 Token。', 'Issue a Token for automation, CI, or cleanup jobs.')"><button class="btn btn-primary btn-sm" type="button" @click="openCreate">{{ tr('新建 API Token', 'New API Token') }}</button></EmptyState>
    </section>
  </div>

  <AppModal v-if="modal === 'form'" :title="editing ? tr('编辑 API Token', 'Edit API Token') : tr('新建 API Token', 'New API Token')" :confirm-label="editing ? tr('保存修改', 'Save changes') : tr('创建并显示 Token', 'Create and reveal Token')" :size="'wide'" :busy="busy" @close="modal = null" @confirm="save">
    <div class="token-workbench">
      <div class="token-form-section"><div class="token-form-section-head"><span>{{ tr('标识', 'IDENTITY') }}</span><strong>{{ tr('基本信息', 'Identity') }}</strong></div><div class="settings-grid settings-grid-2"><div class="form-group"><label class="form-label" for="token-name">{{ tr('Token 名称', 'Token name') }}</label><input id="token-name" v-model="form.name" class="form-input" :placeholder="tr('例如：CI 邮箱清理', 'e.g. CI mailbox cleanup')" /><div class="form-hint">{{ tr('使用可识别的用途名称。', 'Use a name that identifies its purpose.') }}</div></div><div class="form-group"><label class="form-label" for="token-scope">Permission scope</label><select id="token-scope" v-model="form.scope" class="form-input"><option value="read">{{ tr('Read only · 只读查询', 'Read only') }}</option><option value="cleanup">{{ tr('Cleanup · 读写与清理', 'Cleanup · read and write') }}</option><option value="owner">{{ tr('Owner · API 完全控制', 'Owner · full API access') }}</option></select><div class="form-hint">{{ tr('scope 决定 Token 可以执行的动作。', 'scope controls the operations available to this Token.') }}</div></div></div></div>
      <div class="token-form-section"><div class="token-form-section-head"><span>{{ tr('限制', 'LIMITS') }}</span><strong>{{ tr('请求预算', 'Request budget') }}</strong></div><div class="settings-grid settings-grid-3"><div class="form-group"><label class="form-label" for="token-rpm">RPM</label><input id="token-rpm" v-model.number="form.rpm" class="form-input" type="number" min="0" placeholder="0" /><div class="form-hint">{{ tr('0 = 不限', '0 = unlimited') }}</div></div><div class="form-group"><label class="form-label" for="token-daily">{{ tr('每日限制', 'Daily limit') }}</label><input id="token-daily" v-model.number="form.daily" class="form-input" type="number" min="0" placeholder="0" /><div class="form-hint">{{ tr('按 Asia/Shanghai 重置', 'Resets in Asia/Shanghai') }}</div></div><div class="form-group"><label class="form-label" for="token-total">{{ tr('总量限制', 'Total limit') }}</label><input id="token-total" v-model.number="form.total" class="form-input" type="number" min="0" placeholder="0" /><div class="form-hint">{{ tr('整个 Token 生命周期', 'Across the Token lifecycle') }}</div></div></div></div>
      <div class="token-form-section"><div class="token-form-section-head"><span>{{ tr('生命周期', 'LIFECYCLE') }}</span><strong>{{ tr('有效期', 'Expiry') }}</strong></div><div class="token-lifecycle-row"><label class="setting-toggle-row"><input v-model="form.permanent" type="checkbox" /><span><strong>{{ tr('长期有效', 'No expiry') }}</strong><small>{{ tr('适用于受控的内部服务。', 'For controlled internal services.') }}</small></span></label><div v-if="!form.permanent" class="form-group token-expiry-field"><label class="form-label" for="token-expiry-days">{{ tr('有效天数', 'Valid for (days)') }}</label><input id="token-expiry-days" v-model.number="form.expiresInDays" class="form-input" type="number" min="1" /></div></div></div>
    </div>
  </AppModal>

  <AppModal v-if="modal === 'secret'" :title="secretTitle" :confirm-label="tr('已保存', 'Saved')" @close="modal = null" @confirm="modal = null">
    <div class="api-key-secret-result"><span class="secret-value is-revealed">{{ secret }}</span><button class="icon-btn" type="button" :aria-label="tr('复制 API Token', 'Copy API Token')" :title="tr('复制 API Token', 'Copy API Token')" @click="copySecret"><UiIcon name="copy" :size="16" /></button></div>
    <p class="form-hint">{{ tr('完整 Token 仅显示一次。', 'The full Token is shown once.') }}</p>
  </AppModal>

  <AppModal v-if="modal === 'confirm' && pendingAction" :title="pendingAction.type === 'rotate' ? tr('重新生成 API Token', 'Regenerate API Token') : tr('永久删除 API Token', 'Delete API Token')" :confirm-label="pendingAction.type === 'rotate' ? tr('重新生成', 'Regenerate') : tr('永久删除', 'Delete permanently')" :confirm-tone="pendingAction.type === 'delete' ? 'danger' : 'primary'" :busy="actionTokenID === pendingAction.token.id" @close="modal = null; pendingAction = null" @confirm="confirmAction">
    <div class="confirm-target"><strong>{{ pendingAction.token.name }}</strong><code>{{ maskedToken(pendingAction.token) }}</code></div>
    <p class="form-hint">{{ pendingAction.type === 'rotate' ? tr('当前 Token 将立即失效。', 'The current Token will stop working immediately.') : tr('此操作不可撤销。', 'This action cannot be undone.') }}</p>
  </AppModal>
</template>
