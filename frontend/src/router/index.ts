import { createRouter, createWebHistory } from 'vue-router'
import { authState, isAuthenticated } from '../stores/auth'
import AppShell from '../components/AppShell.vue'
import LoginPage from '../pages/LoginPage.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginPage },
    { path: '/donate', name: 'donate', component: () => import('../pages/DonateDomainPage.vue') },
    {
      path: '/',
      component: AppShell,
      meta: { requiresAuth: true },
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: () => import('../pages/DashboardPage.vue') },
        { path: 'mailboxes', name: 'mailboxes', component: () => import('../pages/MailboxesPage.vue') },
        { path: 'inbox/:mailboxId', name: 'inbox', component: () => import('../pages/InboxPage.vue') },
        { path: 'inbox/:mailboxId/email/:emailId', name: 'email', component: () => import('../pages/EmailPage.vue') },
        { path: 'domains', name: 'domains', component: () => import('../pages/DomainsPage.vue') },
        { path: 'donation-plan', name: 'donation-plan', component: () => import('../pages/DonationPlanPage.vue') },
        { path: 'tokens', name: 'tokens', component: () => import('../pages/TokensPage.vue') },
        { path: 'settings', name: 'settings', component: () => import('../pages/SettingsPage.vue') },
        { path: 'api-docs', name: 'api-docs', component: () => import('../pages/ApiDocsPage.vue') },
        { path: 'analytics', name: 'analytics', component: () => import('../pages/AnalyticsPage.vue') },
        { path: 'operations', name: 'operations', component: () => import('../pages/OperationsPage.vue') },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: () => isAuthenticated.value ? '/dashboard' : '/login' },
  ],
})

router.beforeEach(to => {
  if (to.name === 'donate' && String(authState.publicSettings.donation_enabled ?? 'true') === 'false') {
    return { name: isAuthenticated.value ? 'dashboard' : 'login' }
  }
  if (to.meta.requiresAuth && !isAuthenticated.value) return { name: 'login', query: { redirect: to.fullPath } }
  if (to.name === 'login' && isAuthenticated.value) return { name: 'dashboard' }
  return true
})

export default router
