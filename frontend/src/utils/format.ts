import { localeState, tr } from '../stores/i18n'

function localeCode(): string {
  return localeState.locale
}

export function formatDate(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(localeCode(), { hour12: false })
}

export function timeAgo(value?: string | null): string {
  if (!value) return '—'
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return tr('刚刚', 'Just now')
  if (seconds < 3600) return tr(`${Math.floor(seconds / 60)} 分钟前`, `${Math.floor(seconds / 60)}m ago`)
  if (seconds < 86400) return tr(`${Math.floor(seconds / 3600)} 小时前`, `${Math.floor(seconds / 3600)}h ago`)
  return tr(`${Math.floor(seconds / 86400)} 天前`, `${Math.floor(seconds / 86400)}d ago`)
}

export function formatMetric(value?: number | string | null): string {
  return Number(value || 0).toLocaleString(localeCode())
}

export function mailboxParts(address = ''): [string, string] {
  const index = address.lastIndexOf('@')
  return index > 0 ? [address.slice(0, index), address.slice(index)] : [address, '']
}

export function retentionLabel(keepForever: boolean, expiresAt?: string | null): string {
  if (keepForever) return tr('永久保留', 'Retained')
  if (!expiresAt) return tr('长期有效', 'No expiry')
  if (new Date(expiresAt).getTime() <= Date.now()) return tr('已过期', 'Expired')
  const remaining = Math.max(0, new Date(expiresAt).getTime() - Date.now())
  const minutes = Math.ceil(remaining / 60000)
  if (minutes < 60) return tr(`${minutes} 分钟后到期`, `Expires in ${minutes}m`)
  const hours = Math.ceil(minutes / 60)
  if (hours < 24) return tr(`${hours} 小时后到期`, `Expires in ${hours}h`)
  const days = Math.ceil(hours / 24)
  return tr(`${days} 天后到期`, `Expires in ${days}d`)
}

export async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  document.body.appendChild(textarea)
  textarea.select()
  document.execCommand('copy')
  textarea.remove()
}

export function extractCode(input: string): string {
  const hinted = input.match(/(?:验证码|校验码|verification\s*code|otp|passcode|code)[^A-Z0-9]{0,24}([A-Z0-9]{4,10})/i)
  return hinted?.[1] || input.match(/\b\d{4,8}\b/)?.[0] || ''
}

export function extractLink(input: string): string {
  return input.match(/https?:\/\/[^\s"'<>]+/)?.[0] || ''
}
