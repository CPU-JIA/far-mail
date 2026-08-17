import { reactive } from 'vue'

export interface HeaderAction {
  label: string
  tone?: 'primary' | 'ghost' | 'danger'
  glyph?: string
  run: () => void | Promise<void>
}

export const uiState = reactive<{
  title: string
  subtitle: string
  actions: HeaderAction[]
}>({
  title: '仪表盘',
  subtitle: '邮件运营概览',
  actions: [],
})

export function setPageHeader(title: string, subtitle = '', actions: HeaderAction[] = []): void {
  uiState.title = title
  uiState.subtitle = subtitle
  uiState.actions = actions
}
