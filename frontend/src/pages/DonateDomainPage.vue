<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import BrandMark from '../components/BrandMark.vue'
import UiIcon from '../components/UiIcon.vue'
import { PRODUCT_NAME } from '../config/brand'
import { api } from '../services/api'
import { authState } from '../stores/auth'
import { toast } from '../stores/toast'
import { localizeBackendText, tr } from '../stores/i18n'
import type { DomainStatusResponse, DomainSubmitResponse } from '../types/api'
import { copyText, formatMetric, timeAgo } from '../utils/format'

type DonationMode = 'new' | 'existing'

const router = useRouter()
const domain = ref('')
const mode = ref<DonationMode>('new')
const existingToken = ref('')
const enableSubdomains = ref(true)
const busy = ref(false)
const result = ref<DomainSubmitResponse | null>(null)
const status = ref<DomainStatusResponse | null>(null)
const pollError = ref('')
let pollTimer: number | undefined
let polling = false
let visibilityHandler: (() => void) | undefined

const siteTitle = computed(() => String(authState.publicSettings.site_title || PRODUCT_NAME))
const logoUrl = computed(() => String(authState.publicSettings.site_logo_url || ''))
const currentDonation = computed(() => status.value?.donation || result.value?.donation)
const isActive = computed(() => Boolean(currentDonation.value?.reward_active))
const rewardRule = computed(() => ({
  rpm: Number(authState.publicSettings.donation_reward_rate_limit_per_minute || 30),
  daily: Number(authState.publicSettings.donation_reward_daily_request_limit || 5000),
  total: Number(authState.publicSettings.donation_reward_total_request_limit || 100000),
}))

function validDomain(value: string): boolean {
  return /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i.test(value)
}

async function submit(): Promise<void> {
  const value = domain.value.trim().toLowerCase()
  if (!validDomain(value)) {
    toast(tr('请输入有效根域名', 'Enter a valid root domain'), 'warn')
    return
  }
  if (mode.value === 'existing' && !existingToken.value.trim()) {
    toast(tr('请输入已有奖励 API Token', 'Enter an existing reward API Token'), 'warn')
    return
  }
  busy.value = true
  status.value = null
  try {
    result.value = mode.value === 'existing'
      ? await api.donateDomainWithToken(value, enableSubdomains.value, existingToken.value)
      : await api.donateDomain(value, enableSubdomains.value)
    if (result.value.donation_id && result.value.claim_secret) {
      sessionStorage.setItem(`far_mail_donation_claim_${result.value.donation_id}`, result.value.claim_secret)
    }
    if (result.value.access_token) {
      sessionStorage.setItem(`far_mail_donation_token_${result.value.donation_id}`, result.value.access_token)
    }
    toast(tr('域名已提交', 'Domain submitted'), 'success')
    startPolling()
  } catch (cause) {
    toast(cause instanceof Error ? cause.message : tr('域名提交失败', 'Domain submission failed'), 'error')
  } finally {
    busy.value = false
  }
}

function claimSecret(): string {
  const id = result.value?.donation_id
  if (!id) return ''
  return result.value?.claim_secret || sessionStorage.getItem(`far_mail_donation_claim_${id}`) || ''
}

function stopPolling(): void {
  if (pollTimer) window.clearInterval(pollTimer)
  if (visibilityHandler) document.removeEventListener('visibilitychange', visibilityHandler)
  pollTimer = undefined
  visibilityHandler = undefined
  polling = false
}

function startPolling(): void {
  stopPolling()
  const id = result.value?.donation_id
  if (!id || isActive.value) return
  const check = async (): Promise<void> => {
    if (polling || document.visibilityState !== 'visible' || !id) return
    polling = true
    try {
      status.value = await api.donatedDomainStatus(id, claimSecret())
      pollError.value = ''
      if (status.value.is_active) {
        stopPolling()
        toast(tr('域名验证通过，奖励已生效', 'Domain verified and reward activated'), 'success')
      }
    } catch (cause) {
      pollError.value = cause instanceof Error ? cause.message : tr('验证状态暂时无法获取', 'Verification status is temporarily unavailable')
    }
    finally { polling = false }
  }
  void check()
  pollTimer = window.setInterval(check, 5000)
  visibilityHandler = () => { if (document.visibilityState === 'visible') void check() }
  document.addEventListener('visibilitychange', visibilityHandler)
}

async function copy(value: string, message: string): Promise<void> {
  await copyText(value)
  toast(message, 'success')
}

function reset(): void {
  stopPolling()
  result.value = null
  status.value = null
  domain.value = ''
  existingToken.value = ''
}

onBeforeUnmount(stopPolling)
onMounted(() => {
  if (String(authState.publicSettings.donation_enabled ?? 'true') === 'false') {
    void router.replace({ name: 'login' })
  }
})
</script>

<template>
  <div id="auth-page" class="auth-v23">
    <header class="auth-topbar">
      <div class="auth-topbar-brand">
        <div class="auth-topbar-logo" data-brand-logo><img v-if="logoUrl" :src="logoUrl" alt="" /><BrandMark v-else /></div>
        <span class="brand-title">{{ siteTitle }}</span>
      </div>
      <button class="auth-tab" type="button" @click="router.push({ name: 'login' })">{{ tr('返回登录', 'Back to sign in') }}</button>
    </header>

    <main class="donation-public-main">
      <section v-if="!result" class="donation-public-layout">
        <header class="donation-public-heading">
          <p>{{ tr('域名捐赠', 'DOMAIN CONTRIBUTION') }}</p>
          <h1>{{ tr('捐献收件域名', 'Donate a receiving domain') }}</h1>
          <div class="donation-reward-line">
            <span><strong>{{ formatMetric(rewardRule.total) }}</strong> {{ tr('总额度', 'total') }}</span>
            <span><strong>{{ formatMetric(rewardRule.daily) }}</strong> {{ tr('每日', 'daily') }}</span>
            <span><strong>{{ rewardRule.rpm }}</strong> RPM</span>
          </div>
        </header>

        <form class="donation-public-form" @submit.prevent="submit">
          <div class="donation-mode-switch" role="tablist" :aria-label="tr('奖励 API Token', 'Reward API Token')">
            <button type="button" :class="{ active: mode === 'new' }" @click="mode = 'new'">{{ tr('创建奖励 Token', 'Create reward Token') }}</button>
            <button type="button" :class="{ active: mode === 'existing' }" @click="mode = 'existing'">{{ tr('累加到已有 Token', 'Add to existing Token') }}</button>
          </div>
          <div class="form-group"><label class="form-label" for="donate-domain">{{ tr('根域名', 'Root domain') }}</label><input id="donate-domain" v-model="domain" class="form-input" placeholder="example.com" autocomplete="off" autofocus /></div>
          <div v-if="mode === 'existing'" class="form-group"><label class="form-label" for="reward-token">{{ tr('奖励 API Token', 'Reward API Token') }}</label><input id="reward-token" v-model="existingToken" class="form-input" type="password" :placeholder="tr('输入已有 Token', 'Enter existing Token')" autocomplete="off" /></div>
          <label class="checkbox-row auth-checkbox-row"><input v-model="enableSubdomains" type="checkbox" /><span>{{ tr('启用通配子域收件', 'Enable wildcard subdomains') }}</span></label>
          <button class="btn btn-primary btn-block" type="submit" :disabled="busy">{{ busy ? tr('正在提交…', 'Submitting…') : tr('提交域名', 'Submit domain') }}</button>
        </form>
      </section>

      <section v-else class="donation-claim-layout">
        <header class="donation-claim-head">
          <div><p>{{ tr('域名验证', 'DOMAIN CLAIM') }}</p><h1>{{ result.domain.domain }}</h1></div>
          <span class="status-chip" :class="isActive ? 'success' : currentDonation?.status === 'inactive' ? 'danger' : 'warn'">{{ isActive ? tr('已生效', 'Active') : currentDonation?.status === 'inactive' ? tr('验证失效', 'Verification failed') : tr('等待验证', 'Pending') }}</span>
        </header>

        <div v-if="result.access_token" class="donation-token-panel">
          <div><span>{{ tr('奖励 API Token', 'Reward API Token') }}</span><code>{{ result.access_token }}</code></div>
          <button class="icon-btn" type="button" :aria-label="tr('复制奖励 API Token', 'Copy reward API Token')" :title="tr('复制奖励 API Token', 'Copy reward API Token')" @click="copy(result.access_token, tr('奖励 API Token 已复制', 'Reward API Token copied'))"><UiIcon name="copy" :size="16" /></button>
        </div>

        <section class="donation-dns-section">
          <div class="donation-section-head"><div><span>{{ tr('DNS 配置', 'DNS CONFIGURATION') }}</span><strong>{{ tr('添加以下记录', 'Add these records') }}</strong></div><small>{{ currentDonation?.last_checked_at ? tr(`最近检查 ${timeAgo(currentDonation.last_checked_at)}`, `Checked ${timeAgo(currentDonation.last_checked_at)}`) : tr('尚未检查', 'Not checked') }}</small></div>
          <div class="donation-dns-list">
            <article v-for="record in (status?.dns_required || result.dns_required || [])" :key="`${record.type}-${record.host}`">
              <strong>{{ record.type }}</strong><code>{{ record.host }}</code><code>{{ record.value }}</code><span>{{ record.priority || '—' }}</span>
              <button class="icon-btn" type="button" :aria-label="tr(`复制 ${record.type} 记录值`, `Copy ${record.type} value`)" :title="tr('复制记录值', 'Copy record value')" @click="copy(record.value, tr('记录值已复制', 'Record value copied'))"><UiIcon name="copy" :size="15" /></button>
            </article>
          </div>
        </section>

        <section v-if="currentDonation" class="donation-effective-strip">
          <div><span>RPM</span><strong>{{ currentDonation.effective_rate_limit_per_minute }}</strong></div>
          <div><span>{{ tr('每日额度', 'Daily quota') }}</span><strong>{{ formatMetric(currentDonation.effective_daily_request_limit) }}</strong></div>
          <div><span>{{ tr('总额度', 'Total quota') }}</span><strong>{{ formatMetric(currentDonation.effective_total_request_limit) }}</strong></div>
          <div><span>{{ tr('已使用', 'Used') }}</span><strong>{{ formatMetric(currentDonation.request_count_total) }}</strong></div>
        </section>

        <div v-if="currentDonation?.last_error && !isActive" class="donation-check-result"><UiIcon name="alert" :size="16" /><span>{{ localizeBackendText(currentDonation.last_error) }}</span></div>
        <div v-if="pollError" class="operations-data-warning" role="status"><UiIcon name="alert" :size="15" /><span>{{ pollError }}</span><button class="btn btn-ghost btn-sm" type="button" @click="pollError = ''; startPolling()">{{ tr('重试', 'Retry') }}</button></div>
        <div class="auth-entry-actions"><button class="btn btn-ghost" type="button" @click="reset">{{ tr('继续捐献', 'Donate another') }}</button><button class="btn btn-primary" type="button" @click="router.push({ name: 'login' })">{{ tr('完成', 'Done') }}</button></div>
      </section>
    </main>
  </div>
</template>
