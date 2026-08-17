<script setup lang="ts">
import { computed } from 'vue'
import { authState } from '../stores/auth'
import UiIcon from './UiIcon.vue'
import { tr } from '../stores/i18n'

const logo = computed(() => String(authState.publicSettings.site_logo_url || ''))
</script>

<template>
  <img v-if="logo" :src="logo" :alt="tr('站点图标', 'Site icon')" />
  <UiIcon v-else name="mail" :size="16" />
</template>
