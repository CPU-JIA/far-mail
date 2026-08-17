<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import BrandMark from './BrandMark.vue'
import UiIcon from './UiIcon.vue'
import { PRODUCT_NAME } from '../config/brand'
import { authState, logout, setTheme, toggleTheme } from '../stores/auth'
import { localeState, setLocale, t, tr, type Locale } from '../stores/i18n'
import { uiState, type HeaderAction } from '../stores/ui'

const route = useRoute()
const router = useRouter()
const sidebarOpen = ref(false)
const accountMenuOpen = ref(false)
const sidebarCollapsed = ref(localStorage.getItem('far_mail_sidebar_collapsed') === 'true')

const title = computed(() => String(authState.publicSettings.site_title || PRODUCT_NAME))
const username = computed(() => authState.session?.account.username || tr('站长', 'Owner'))
const logoUrl = computed(() => String(authState.publicSettings.site_logo_url || ''))

const navGroups = computed(() => [
  {
    label: t('workspace'),
    items: [
      { name: 'dashboard', label: t('dashboard'), icon: 'dashboard' },
      { name: 'mailboxes', label: t('mailboxes'), icon: 'inbox' },
      { name: 'domains', label: t('domains'), icon: 'domains' },
      { name: 'donation-plan', label: t('donationPlan'), icon: 'gift' },
    ],
  },
  {
    label: t('automation'),
    items: [
      { name: 'tokens', label: t('tokens'), icon: 'key' },
      { name: 'api-docs', label: t('docs'), icon: 'docs' },
    ],
  },
  {
    label: t('operations'),
    items: [
      { name: 'analytics', label: t('analytics'), icon: 'chart' },
      { name: 'operations', label: t('operationsCenter'), icon: 'operations' },
    ],
  },
  {
    label: t('system'),
    items: [
      { name: 'settings', label: t('settings'), icon: 'settings' },
    ],
  },
])

watch(() => route.fullPath, () => { sidebarOpen.value = false; accountMenuOpen.value = false })
watch([() => uiState.title, title], ([pageTitle, siteTitle]) => {
  document.title = pageTitle ? `${pageTitle} · ${siteTitle}` : siteTitle
}, { immediate: true })

function closeAccountMenu(event: MouseEvent): void {
  const target = event.target as HTMLElement
  if (!target.closest('.sidebar-account')) accountMenuOpen.value = false
}

function handleEscape(event: KeyboardEvent): void {
  if (event.key === 'Escape') accountMenuOpen.value = false
}

function chooseLocale(next: Locale): void {
  setLocale(next)
  accountMenuOpen.value = false
}

function toggleSidebar(): void {
  sidebarCollapsed.value = !sidebarCollapsed.value
  localStorage.setItem('far_mail_sidebar_collapsed', String(sidebarCollapsed.value))
  accountMenuOpen.value = false
}

onMounted(() => {
  document.addEventListener('click', closeAccountMenu)
  document.addEventListener('keydown', handleEscape)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', closeAccountMenu)
  document.removeEventListener('keydown', handleEscape)
})

function signOut(): void {
  accountMenuOpen.value = false
  logout()
  void router.replace({ name: 'login' })
}

function actionGlyph(action: HeaderAction): string {
  if (action.glyph === '+') return 'plus'
  if (action.glyph === '↻') return 'refresh'
  if (action.glyph === '←') return 'back'
  if (action.glyph === '□') return 'copy'
  if (action.glyph === '×') return 'trash'
  if (action.glyph === 'bell') return 'bell'
  if (action.glyph === 'bellOff') return 'bellOff'
  if (/新建|添加|创建|new|add|create/i.test(action.label)) return 'plus'
  if (/刷新|refresh|reload/i.test(action.label)) return 'refresh'
  if (/返回|back/i.test(action.label)) return 'back'
  if (/复制|copy/i.test(action.label)) return 'copy'
  if (/删除|清理|delete|remove|clean/i.test(action.label)) return 'trash'
  if (/永久|保留|archive|retain/i.test(action.label)) return 'archive'
  return ''
}
</script>

<template>
  <div class="app-layout" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
    <a class="skip-link" href="#main-content">{{ localeState.locale === 'en-US' ? 'Skip to content' : '跳到主要内容' }}</a>
    <div class="sidebar-backdrop" :class="{ show: sidebarOpen }" @click="sidebarOpen = false"></div>

    <aside id="main-sidebar" class="sidebar" :class="{ 'mob-open': sidebarOpen, collapsed: sidebarCollapsed }">
      <div class="sidebar-brand">
        <button class="brand-link" type="button" @click="router.push({ name: 'dashboard' })">
          <span class="logo-mark" data-brand-logo>
            <img v-if="logoUrl" :src="logoUrl" alt="" />
            <BrandMark v-else />
          </span>
          <span class="sidebar-brand-copy">
            <strong>{{ title }}</strong>
            <small>{{ t('mailOperations') }}</small>
          </span>
        </button>
        <button class="sidebar-mobile-close icon-btn" type="button" :aria-label="t('close')" :title="t('close')" @click="sidebarOpen = false"><UiIcon name="closePanel" /></button>
      </div>
      <div class="sidebar-collapse-row">
        <button class="sidebar-collapse-btn" type="button" :aria-label="sidebarCollapsed ? tr('展开侧边栏', 'Expand sidebar') : tr('收起侧边栏', 'Collapse sidebar')" :title="sidebarCollapsed ? tr('展开侧边栏', 'Expand sidebar') : tr('收起侧边栏', 'Collapse sidebar')" @click="toggleSidebar">
          <UiIcon :name="sidebarCollapsed ? 'openPanel' : 'closePanel'" :size="15" />
        </button>
      </div>

      <nav class="sidebar-nav" :aria-label="t('ownerConsole')">
        <section v-for="group in navGroups" :key="group.label" class="nav-group">
          <div class="nav-section">{{ group.label }}</div>
          <button
            v-for="item in group.items"
            :key="item.name"
            class="nav-item"
            :class="{ active: route.name === item.name }"
            :aria-current="route.name === item.name ? 'page' : undefined"
            type="button"
            @click="router.push({ name: item.name })"
          >
            <span class="nav-icon"><UiIcon :name="item.icon" /></span>
            <span>{{ item.label }}</span>
          </button>
        </section>
      </nav>

      <div class="sidebar-bottom">
        <div class="sidebar-account" :class="{ 'is-open': accountMenuOpen }">
          <div class="user-avatar"><img v-if="logoUrl" :src="logoUrl" alt="" /><BrandMark v-else /></div>
          <div class="user-chip-info">
            <div class="user-chip-name">{{ username }}</div>
          </div>
          <button class="icon-btn account-trigger" type="button" :aria-expanded="accountMenuOpen" aria-haspopup="menu" :aria-label="t('accountMenu')" :title="t('accountMenu')" @click.stop="accountMenuOpen = !accountMenuOpen">
            <UiIcon name="more" />
          </button>
          <div v-if="accountMenuOpen" class="account-menu" role="menu">
            <div class="account-menu-heading">{{ t('appearance') }}</div>
            <div class="account-menu-segment" role="group" :aria-label="t('appearance')">
              <button type="button" :class="{ active: authState.theme === 'light' }" @click="setTheme('light')"><UiIcon name="sun" :size="14" />{{ t('light') }}</button>
              <button type="button" :class="{ active: authState.theme === 'dark' }" @click="setTheme('dark')"><UiIcon name="moon" :size="14" />{{ t('dark') }}</button>
            </div>
            <div class="account-menu-heading">{{ t('language') }}</div>
            <div class="account-menu-segment" role="group" :aria-label="t('language')">
              <button type="button" :class="{ active: localeState.locale === 'zh-CN' }" @click="chooseLocale('zh-CN')"><UiIcon name="languages" :size="14" />简体中文</button>
              <button type="button" :class="{ active: localeState.locale === 'en-US' }" @click="chooseLocale('en-US')"><UiIcon name="languages" :size="14" />English</button>
            </div>
            <div class="account-menu-divider"></div>
            <button class="account-menu-item danger" type="button" role="menuitem" @click="signOut"><UiIcon name="logout" :size="15" />{{ t('signOut') }}</button>
          </div>
        </div>
      </div>
    </aside>

    <div class="shell-main">
      <header class="mobile-topbar">
        <button class="icon-btn" type="button" :aria-label="t('menu')" :title="t('menu')" @click="sidebarOpen = true"><UiIcon name="menu" /></button>
        <button class="mobile-brand" type="button" @click="router.push({ name: 'dashboard' })">{{ title }}</button>
        <button class="icon-btn" type="button" :aria-label="authState.theme === 'dark' ? t('light') : t('dark')" :title="authState.theme === 'dark' ? t('light') : t('dark')" @click="toggleTheme">
          <UiIcon :name="authState.theme === 'dark' ? 'sun' : 'moon'" />
        </button>
      </header>

      <main id="main-content" class="main-shell">
        <header class="page-header">
          <div class="page-header-copy">
            <h1>{{ uiState.title }}</h1>
            <p v-if="uiState.subtitle">{{ uiState.subtitle }}</p>
          </div>
          <div v-if="uiState.actions.length" class="page-actions">
            <button
              v-for="action in uiState.actions"
              :key="action.label"
              class="btn btn-sm"
              :class="action.tone === 'primary' ? 'btn-primary' : action.tone === 'danger' ? 'btn-danger' : 'btn-ghost'"
              type="button"
              @click="action.run"
            >
              <span v-if="actionGlyph(action)" class="action-glyph"><UiIcon :name="actionGlyph(action)" :size="14" /></span>
              {{ action.label }}
            </button>
          </div>
        </header>

        <div id="page-content" class="page-content">
          <RouterView />
        </div>
      </main>
    </div>
  </div>
</template>
