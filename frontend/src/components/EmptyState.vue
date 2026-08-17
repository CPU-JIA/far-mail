<script setup lang="ts">
import { computed } from 'vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ icon?: string; title: string; description: string }>()

const glyph = computed(() => {
  if (!props.icon) return '·'
  if (['inbox', 'domains', 'key', 'alert', 'file'].includes(props.icon)) return props.icon
  if (/📭|📬|@/.test(props.icon)) return 'inbox'
  if (/🌐|◎/.test(props.icon)) return 'domains'
  if (/🔑|#/.test(props.icon)) return 'key'
  if (/!/.test(props.icon)) return 'alert'
  return 'file'
})
</script>

<template>
  <div class="empty-state">
    <div class="empty-icon"><UiIcon :name="glyph" :size="18" /></div>
    <h3>{{ title }}</h3>
    <p>{{ description }}</p>
    <div v-if="$slots.default" class="empty-actions"><slot /></div>
  </div>
</template>
