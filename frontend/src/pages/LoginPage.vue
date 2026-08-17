<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BrandMark from '../components/BrandMark.vue'
import UiIcon from '../components/UiIcon.vue'
import { PRODUCT_NAME } from '../config/brand'
import { authState, login } from '../stores/auth'
import { toast } from '../stores/toast'
import { t, tr } from '../stores/i18n'

const router = useRouter()
const route = useRoute()
const key = ref('')
const busy = ref(false)
const showKey = ref(false)
const capsLock = ref(false)
const formError = ref('')
const siteTitle = computed(() => String(authState.publicSettings.site_title || PRODUCT_NAME))
const logoUrl = computed(() => String(authState.publicSettings.site_logo_url || ''))
const donationEnabled = computed(() => String(authState.publicSettings.donation_enabled ?? 'true') !== 'false')

async function submit(): Promise<void> {
  if (busy.value) return
  formError.value = ''
  busy.value = true
  try {
    await login(key.value)
    toast(tr('登录成功', 'Signed in'), 'success')
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
    await router.replace(redirect)
  } catch (error) {
    formError.value = error instanceof Error ? error.message : tr('登录失败', 'Sign-in failed')
  } finally {
    busy.value = false
  }
}

function detectCapsLock(event: KeyboardEvent): void {
  capsLock.value = event.getModifierState('CapsLock')
}
</script>

<template>
  <div id="auth-page" class="auth-v23">
    <header class="auth-topbar">
      <div class="auth-topbar-brand">
        <div class="auth-topbar-logo" data-brand-logo><img v-if="logoUrl" :src="logoUrl" alt="" /><BrandMark v-else /></div>
        <span class="brand-title">{{ siteTitle }}</span>
      </div>
      <button v-if="donationEnabled" class="auth-tab" type="button" @click="router.push({ name: 'donate' })">{{ t('donateDomain') }}</button>
    </header>

    <main class="auth-login-stage">
      <section class="auth-login-container">
        <form class="auth-form-panel auth-login-panel" @submit.prevent="submit">
          <div class="auth-panel-head"><h2>{{ tr('登录', 'Sign in') }}</h2></div>
          <div class="form-group">
            <label class="form-label" for="login-key">{{ tr('登录密钥', 'Admin Key') }}</label>
            <div class="auth-password-field">
              <input
                id="login-key"
                v-model="key"
                class="form-input"
                :type="showKey ? 'text' : 'password'"
                :placeholder="tr('输入登录密钥', 'Enter Admin Key')"
                autocomplete="current-password"
                :aria-invalid="Boolean(formError)"
                :aria-describedby="formError ? 'login-error' : capsLock ? 'login-caps-lock' : undefined"
                autofocus
                @input="formError = ''"
                @keydown="detectCapsLock"
                @keyup="detectCapsLock"
              />
              <button class="icon-btn auth-password-toggle" type="button" :aria-label="showKey ? tr('隐藏密钥', 'Hide key') : tr('显示密钥', 'Show key')" :title="showKey ? tr('隐藏密钥', 'Hide key') : tr('显示密钥', 'Show key')" @click="showKey = !showKey">
                <UiIcon :name="showKey ? 'eyeOff' : 'eye'" :size="16" />
              </button>
            </div>
            <p v-if="capsLock" id="login-caps-lock" class="auth-field-hint">{{ tr('Caps Lock 已开启', 'Caps Lock is on') }}</p>
            <p v-if="formError" id="login-error" class="auth-inline-error" role="alert">{{ formError }}</p>
          </div>
          <button class="btn btn-primary btn-block" type="submit" :disabled="busy">{{ busy ? t('authenticating') : t('login') }}</button>
        </form>
      </section>
    </main>
  </div>
</template>
