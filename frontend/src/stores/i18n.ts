import { computed, reactive } from 'vue'

export type Locale = 'zh-CN' | 'en-US'

const LOCALE_STORAGE = 'far_mail_locale'
const localeValue = localStorage.getItem(LOCALE_STORAGE)

export const localeState = reactive<{ locale: Locale }>({
  locale: localeValue === 'en-US' ? 'en-US' : 'zh-CN',
})

const messages: Record<Locale, Record<string, string>> = {
  'zh-CN': {
    donationPlan: '捐赠计划',
    workspace: '工作区', automation: '自动化', operations: '运维', system: '系统', dashboard: '仪表盘', mailboxes: '邮箱目录', domains: '域名管理', tokens: 'API 密钥', docs: '开发文档', analytics: '数据统计', operationsCenter: '运维中心', settings: '站点设置', owner: '站长', ownerConsole: '管理后台', mailOperations: '邮件运营', appearance: '外观', language: '语言', accountMenu: '账户菜单', changePassword: '修改密码', signOut: '退出登录', light: '浅色', dark: '深色', chinese: '简体中文', english: 'English', close: '关闭', menu: '菜单', refresh: '刷新', copy: '复制', save: '保存', cancel: '取消', confirm: '确认操作', processing: '处理中…', create: '创建', delete: '删除', enabled: '已启用', disabled: '已停用', active: '启用中', apiToken: 'API Token', adminKey: '登录密钥', smtp: 'SMTP', lmtp: 'LMTP', login: '登录', authenticating: '正在登录…', adminAuth: '登录密钥', adminKeyHint: '', consoleDescription: '', donateDomain: '捐献收件域名', ownerLogin: '登录', mailboxAndMail: '邮箱与邮件管理', domainAndSettings: '域名与系统设置', tokenIssuance: 'API 密钥签发', keyFormat: '', siteSettings: '站点设置', systemStatus: '系统状态',
  },
  'en-US': {
    donationPlan: 'Donations',
    workspace: 'Workspace', automation: 'Automation', operations: 'Operations', system: 'System', dashboard: 'Dashboard', mailboxes: 'Mailbox Directory', domains: 'Domains', tokens: 'API Tokens', docs: 'Developer Docs', analytics: 'Analytics', operationsCenter: 'Operations Center', settings: 'Site Settings', owner: 'Owner', ownerConsole: 'Console', mailOperations: 'Mail operations', appearance: 'Appearance', language: 'Language', accountMenu: 'Account menu', changePassword: 'Change password', signOut: 'Sign out', light: 'Light', dark: 'Dark', chinese: '简体中文', english: 'English', close: 'Close', menu: 'Menu', refresh: 'Refresh', copy: 'Copy', save: 'Save', cancel: 'Cancel', confirm: 'Confirm', processing: 'Working…', create: 'Create', delete: 'Delete', enabled: 'Enabled', disabled: 'Disabled', active: 'Active', apiToken: 'API Token', adminKey: 'Sign-in key', smtp: 'SMTP', lmtp: 'LMTP', login: 'Sign in', authenticating: 'Signing in…', adminAuth: 'Sign-in key', adminKeyHint: '', consoleDescription: '', donateDomain: 'Donate a receiving domain', ownerLogin: 'Sign in', mailboxAndMail: 'Mailbox and email management', domainAndSettings: 'Domains and system settings', tokenIssuance: 'API Token issuance', keyFormat: '', siteSettings: 'Site settings', systemStatus: 'System status',
  },
}

export const locale = computed(() => localeState.locale)
export const isEnglish = computed(() => localeState.locale === 'en-US')

export function t(key: string): string {
  return messages[localeState.locale][key] || messages['zh-CN'][key] || key
}

/** Keeps short, page-local copy reactive without maintaining opaque translation keys. */
export function tr(zhCN: string, enUS: string): string {
  return localeState.locale === 'en-US' ? enUS : zhCN
}

const backendMessages: Array<[string, string]> = [
  ['TXT 验证记录未就绪', 'TXT verification record is not ready'],
  ['TXT 验证记录不匹配', 'TXT verification record does not match'],
  ['MX 查询暂时不可用', 'MX lookup is temporarily unavailable'],
  ['未找到 MX 记录', 'No MX record found'],
  ['站点邮件服务器尚未配置', 'The site mail server is not configured'],
  ['MX 与 TXT 验证通过', 'MX and TXT verification passed'],
  ['MX 目标解析暂时不可用', 'MX target resolution is temporarily unavailable'],
]

export function localizeBackendText(value?: string | null): string {
  const text = String(value || '').trim()
  if (!text) return ''
  for (const [zhCN, enUS] of backendMessages) {
    if (text === zhCN) return tr(zhCN, enUS)
    if (text === enUS) return tr(zhCN, enUS)
  }
  if (text.startsWith('MX 未指向 ')) return tr(text, `MX does not point to ${text.slice('MX 未指向 '.length)}`)
  if (text.startsWith('MX does not point to ')) return tr(`MX 未指向 ${text.slice('MX does not point to '.length)}`, text)
  return text
}

export function setLocale(next: Locale): void {
  localeState.locale = next
  localStorage.setItem(LOCALE_STORAGE, next)
  document.documentElement.lang = next
}

export function toggleLocale(): void {
  setLocale(localeState.locale === 'zh-CN' ? 'en-US' : 'zh-CN')
}

export function initializeLocale(): void {
  document.documentElement.lang = localeState.locale
}
