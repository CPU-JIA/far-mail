import { reactive } from 'vue'

export interface ConfirmState {
  open: boolean
  title: string
  message: string
  confirmLabel: string
  danger: boolean
  resolve: ((value: boolean) => void) | null
}

export const confirmState = reactive<ConfirmState>({
  open: false,
  title: '',
  message: '',
  confirmLabel: '',
  danger: false,
  resolve: null,
})

export function askConfirm(options: { title: string; message: string; confirmLabel: string; danger?: boolean }): Promise<boolean> {
  if (confirmState.resolve) confirmState.resolve(false)
  return new Promise<boolean>(resolve => {
    confirmState.open = true
    confirmState.title = options.title
    confirmState.message = options.message
    confirmState.confirmLabel = options.confirmLabel
    confirmState.danger = Boolean(options.danger)
    confirmState.resolve = resolve
  })
}

export function resolveConfirm(value: boolean): void {
  const resolve = confirmState.resolve
  confirmState.open = false
  confirmState.resolve = null
  resolve?.(value)
}
