import { computed, reactive } from 'vue'
import { PRODUCT_NAME } from '../config/brand'
import { api, configureAdminKey, publicResourceURL } from '../services/api'
import type { PublicSettings, SessionResponse } from '../types/api'
import { tr } from './i18n'

const ADMIN_KEY_STORAGE = 'far_mail_admin_key'
const THEME_STORAGE = 'far_mail_theme'

export const authState = reactive<{
  adminKey: string
  session: SessionResponse | null
  publicSettings: PublicSettings
  initialized: boolean
  theme: 'light' | 'dark'
}>({
  adminKey: localStorage.getItem(ADMIN_KEY_STORAGE) || '',
  session: null,
  publicSettings: {},
  initialized: false,
  theme: localStorage.getItem(THEME_STORAGE) === 'dark' ? 'dark' : 'light',
})

export const isAuthenticated = computed(() => Boolean(authState.adminKey && authState.session?.account.is_admin))

function applyTheme(): void {
  document.documentElement.dataset.theme = authState.theme
}

export async function loadPublicSettings(): Promise<PublicSettings> {
  try {
    authState.publicSettings = await api.publicSettings()
    authState.publicSettings.site_logo_url = publicResourceURL(authState.publicSettings.site_logo_url || '')
    document.title = authState.publicSettings.site_title || PRODUCT_NAME
  } catch {
    authState.publicSettings = {}
  }
  return authState.publicSettings
}

export async function login(adminKey: string, persist = true): Promise<SessionResponse> {
  const key = adminKey.trim()
  if (!key) throw new Error(tr('请输入登录密钥', 'Enter the Admin Key'))
  if (!/^sk-[a-z0-9_-]{1,24}-(?:[0-9a-f]{16}|[0-9a-f]{32})$/.test(key)) {
    throw new Error(tr('登录密钥格式无效', 'Invalid Admin Key format'))
  }
  configureAdminKey(key)
  const session = await api.session()
  if (session.auth_mode !== 'admin_console' || !session.account?.is_admin) {
    configureAdminKey('')
    throw new Error(tr('登录密钥无效', 'Invalid Admin Key'))
  }
  authState.adminKey = key
  authState.session = session
  if (persist) localStorage.setItem(ADMIN_KEY_STORAGE, key)
  return session
}

export function logout(): void {
  authState.adminKey = ''
  authState.session = null
  configureAdminKey('')
  localStorage.removeItem(ADMIN_KEY_STORAGE)
}

export function setAdminKeyAfterRotation(key: string): void {
  authState.adminKey = key
  configureAdminKey(key)
  localStorage.setItem(ADMIN_KEY_STORAGE, key)
}

export function toggleTheme(): void {
  setTheme(authState.theme === 'dark' ? 'light' : 'dark')
}

export function setTheme(theme: 'light' | 'dark'): void {
  authState.theme = theme
  localStorage.setItem(THEME_STORAGE, authState.theme)
  applyTheme()
}

export async function initializeAuth(): Promise<void> {
  applyTheme()
  await loadPublicSettings()
  if (authState.adminKey) {
    try {
      await login(authState.adminKey, false)
    } catch {
      logout()
    }
  }
  authState.initialized = true
}
