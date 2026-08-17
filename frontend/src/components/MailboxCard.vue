<script setup lang="ts">
import { computed } from 'vue'
import type { Mailbox } from '../types/api'
import { copyText, formatDate, formatMetric, mailboxParts, retentionLabel } from '../utils/format'
import { toast } from '../stores/toast'
import UiIcon from './UiIcon.vue'
import { tr } from '../stores/i18n'

const props = defineProps<{
  mailbox: Mailbox
  selectable?: boolean
  selected?: boolean
}>()

const emit = defineEmits<{
  open: [mailbox: Mailbox]
  select: [mailbox: Mailbox]
  retention: [mailbox: Mailbox]
  remove: [mailbox: Mailbox]
}>()

const parts = computed(() => mailboxParts(props.mailbox.full_address))
const latestSummary = computed(() => {
  if (props.mailbox.latest_code) return tr(`验证码 ${props.mailbox.latest_code}`, `Code ${props.mailbox.latest_code}`)
  if (props.mailbox.latest_link) return tr('含验证链接', 'Verification link detected')
  if (Number(props.mailbox.email_count || 0) > 0) return tr('已有邮件', 'Email received')
  return tr('等待来信', 'Waiting for email')
})

async function copyAddress(): Promise<void> {
  await copyText(props.mailbox.full_address)
  toast(tr('邮箱地址已复制', 'Mailbox address copied'), 'success')
}
</script>

<template>
  <article class="mailbox-card" :class="{ 'is-selected': selected }" @click="emit('open', mailbox)">
    <div class="mailbox-card-headline">
      <div class="mailbox-address mailbox-address-split">
        <strong>{{ parts[0] }}</strong><span>{{ parts[1] }}</span>
      </div>
      <div class="mailbox-card-top-actions">
        <button
          v-if="selectable"
          class="mailbox-card-select"
          :class="{ 'is-selected': selected }"
          type="button"
          @click.stop="emit('select', mailbox)"
        >{{ selected ? tr('已选', 'Selected') : tr('选择', 'Select') }}</button>
        <span class="status-chip" :class="mailbox.keep_forever ? 'success' : 'neutral'">
          {{ retentionLabel(mailbox.keep_forever, mailbox.expires_at) }}
        </span>
      </div>
    </div>
    <div class="mailbox-compact-meta">
      <span>{{ tr(`${formatMetric(mailbox.email_count)} 封邮件`, `${formatMetric(mailbox.email_count)} emails`) }}</span>
      <span>{{ mailbox.latest_received_at ? formatDate(mailbox.latest_received_at) : tr('暂无来信', 'No email yet') }}</span>
      <span>{{ mailbox.keep_forever ? tr('不会自动删除', 'Never deleted automatically') : tr('按站点策略清理', 'Site retention policy') }}</span>
    </div>
    <div class="mailbox-latest-line" :class="{ 'has-code': mailbox.latest_code }">{{ latestSummary }}</div>
    <div v-if="mailbox.latest_link" class="mailbox-retention-note mailbox-inline-link">
      {{ mailbox.latest_link.slice(0, 96) }}
    </div>
    <div class="mailbox-actions">
      <button class="btn btn-ghost btn-sm" type="button" @click.stop="emit('open', mailbox)">{{ tr('查看邮件', 'View email') }}</button>
      <button class="btn btn-ghost btn-sm" type="button" @click.stop="emit('retention', mailbox)">
        {{ mailbox.keep_forever ? tr('取消永久', 'Use site policy') : tr('设为永久', 'Retain') }}
      </button>
      <button class="icon-btn copy-text-btn" type="button" :aria-label="tr('复制邮箱地址', 'Copy mailbox address')" :title="tr('复制邮箱地址', 'Copy mailbox address')" @click.stop="copyAddress"><UiIcon name="copy" /></button>
      <button class="btn btn-danger btn-sm" type="button" @click.stop="emit('remove', mailbox)">{{ tr('删除', 'Delete') }}</button>
    </div>
  </article>
</template>
