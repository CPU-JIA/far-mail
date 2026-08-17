<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import UiIcon from './UiIcon.vue'
import { t } from '../stores/i18n'

defineProps<{
  title: string
  confirmLabel?: string
  confirmTone?: 'primary' | 'danger'
  size?: 'default' | 'wide'
  busy?: boolean
  confirmDisabled?: boolean
}>()

const emit = defineEmits<{
  close: []
  confirm: []
}>()

const dialog = ref<HTMLElement | null>(null)
const titleId = useId()
let restoreFocus: HTMLElement | null = null

function handleKey(event: KeyboardEvent): void {
  if (event.key === 'Escape') emit('close')
}

function handleTab(event: KeyboardEvent): void {
  if (event.key !== 'Tab' || !dialog.value) return
  const focusable = [...dialog.value.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href]')]
  if (!focusable.length) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}

onMounted(() => {
  restoreFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  window.addEventListener('keydown', handleKey)
  window.addEventListener('keydown', handleTab)
  void nextTick(() => dialog.value?.querySelector<HTMLElement>('input, select, textarea, button:not([disabled])')?.focus())
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKey)
  window.removeEventListener('keydown', handleTab)
  restoreFocus?.focus()
})
</script>

<template>
  <Teleport to="body">
    <div class="modal-overlay" @click.self="emit('close')">
      <section ref="dialog" class="modal" :class="{ 'modal-wide': size === 'wide' }" role="dialog" aria-modal="true" :aria-labelledby="titleId">
        <header class="modal-header">
          <h2 :id="titleId" class="modal-title">{{ title }}</h2>
          <button class="modal-close icon-btn" type="button" :aria-label="t('close')" :title="t('close')" @click="emit('close')"><UiIcon name="close" :size="16" /></button>
        </header>
        <div class="modal-content"><slot /></div>
        <footer class="modal-actions">
          <button class="btn btn-ghost" type="button" :disabled="busy" @click="emit('close')">{{ t('cancel') }}</button>
          <button
            class="btn"
            :class="confirmTone === 'danger' ? 'btn-danger' : 'btn-primary'"
            type="button"
            :disabled="busy || confirmDisabled"
            @click="emit('confirm')"
          >
            {{ busy ? t('processing') : (confirmLabel || t('confirm')) }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>
