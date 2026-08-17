<script setup lang="ts">
import { tr } from '../stores/i18n'
</script>

<template>
  <div class="page-skeleton" :aria-label="tr('正在加载页面', 'Loading page')" aria-busy="true">
    <div class="skeleton-grid skeleton-metrics">
      <div v-for="index in 4" :key="index" class="skeleton-block"></div>
    </div>
    <div class="skeleton-block skeleton-panel"></div>
    <div class="skeleton-block skeleton-table"></div>
  </div>
</template>
