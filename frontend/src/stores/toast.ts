import { reactive } from 'vue'

export type ToastTone = 'info' | 'success' | 'warn' | 'error'

export interface ToastItem {
  id: number
  message: string
  tone: ToastTone
}

export const toasts = reactive<ToastItem[]>([])
let nextId = 1

export function toast(message: string, tone: ToastTone = 'info'): void {
  const existing = toasts.find(item => item.message === message && item.tone === tone)
  if (existing) {
    const index = toasts.indexOf(existing)
    if (index >= 0) toasts.splice(index, 1)
  }
  const item = { id: nextId++, message, tone }
  toasts.push(item)
  window.setTimeout(() => {
    const index = toasts.findIndex(entry => entry.id === item.id)
    if (index >= 0) toasts.splice(index, 1)
  }, 3200)
}
