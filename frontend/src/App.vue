<script setup lang="ts">
import { RouterView } from 'vue-router'
import { toasts } from './stores/toast'
import LoadingState from './components/LoadingState.vue'
import AppModal from './components/AppModal.vue'
import { authState } from './stores/auth'
import { confirmState, resolveConfirm } from './stores/confirm'
</script>

<template>
  <LoadingState v-if="!authState.initialized" />
  <RouterView v-else />
  <div id="toast-container" aria-live="polite">
    <div v-for="item in toasts" :key="item.id" class="toast" :class="item.tone" role="status">
      <div class="toast-body">{{ item.message }}</div>
    </div>
  </div>
  <AppModal
    v-if="confirmState.open"
    :title="confirmState.title"
    :confirm-label="confirmState.confirmLabel"
    :confirm-tone="confirmState.danger ? 'danger' : 'primary'"
    @close="resolveConfirm(false)"
    @confirm="resolveConfirm(true)"
  >
    <p class="confirm-message">{{ confirmState.message }}</p>
  </AppModal>
</template>
