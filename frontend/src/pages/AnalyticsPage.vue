<script setup lang="ts">
import { computed, onMounted, ref, watchEffect } from 'vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingState from '../components/LoadingState.vue'
import { api } from '../services/api'
import { setPageHeader } from '../stores/ui'
import { tr } from '../stores/i18n'
import type { AnalyticsDay, AnalyticsSummary } from '../types/api'
import { formatMetric } from '../utils/format'

const loading = ref(true)
const error = ref('')
const summary = ref<AnalyticsSummary | null>(null)
const days = ref<AnalyticsDay[]>([])
const windowDays = ref(7)

const maxMailboxes = computed(() => Math.max(1, ...days.value.map(day => day.mailboxes)))
const maxEmails = computed(() => Math.max(1, ...days.value.map(day => day.emails)))
const maxCodes = computed(() => Math.max(1, ...days.value.map(day => day.codes)))
const hasActivity = computed(() => days.value.some(day => day.mailboxes > 0 || day.emails > 0 || day.codes > 0))
const storageLabel = computed(() => {
  const bytes = Number(summary.value?.storage_bytes || 0)
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
})

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await api.analyticsSummary(windowDays.value)
    summary.value = result.summary
    days.value = result.days || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : tr('统计数据加载失败', 'Unable to load analytics')
  } finally {
    loading.value = false
  }
}

watchEffect(() => setPageHeader(tr('数据统计', 'Analytics'), tr('查看邮箱、邮件、验证码和 API 调用的运营趋势', 'Mailbox, email, verification code, and API activity')))
onMounted(() => void load())
</script>

<template>
  <LoadingState v-if="loading" />
  <EmptyState v-else-if="error" icon="!" :title="tr('统计数据加载失败', 'Unable to load analytics')" :description="error"><button class="btn btn-primary btn-sm" type="button" @click="load">{{ tr('重试', 'Retry') }}</button></EmptyState>
  <div v-else class="console-page analytics-page">
    <section class="analytics-metric-grid">
      <article class="analytics-metric"><span>{{ tr('邮箱总数', 'Mailboxes') }}</span><strong>{{ formatMetric(summary?.mailbox_total) }}</strong><small>{{ tr(`${formatMetric(summary?.permanent_mailbox_total)} 个永久保留`, `${formatMetric(summary?.permanent_mailbox_total)} retained`) }}</small></article>
      <article class="analytics-metric"><span>{{ tr('邮件总量', 'Emails') }}</span><strong>{{ formatMetric(summary?.email_total) }}</strong><small>{{ tr(`近 24 小时 ${formatMetric(summary?.email_last_24h)}`, `${formatMetric(summary?.email_last_24h)} in 24 hours`) }}</small></article>
      <article class="analytics-metric"><span>{{ tr('验证码邮件', 'Verification emails') }}</span><strong>{{ formatMetric(summary?.code_email_total) }}</strong><small>{{ tr(`近 7 天 ${formatMetric(summary?.email_last_7d)} 封邮件`, `${formatMetric(summary?.email_last_7d)} emails in 7 days`) }}</small></article>
      <article class="analytics-metric"><span>{{ tr('API 今日调用', 'API calls today') }}</span><strong>{{ formatMetric(summary?.token_calls_today) }}</strong><small>{{ tr(`${formatMetric(summary?.active_token_total)} 个有效 API Token`, `${formatMetric(summary?.active_token_total)} active API Tokens`) }}</small></article>
    </section>

    <section class="section-card analytics-chart-card">
      <div class="section-head compact-head"><div><div class="section-kicker">{{ tr('趋势', 'TREND') }}</div><h3 class="section-title">{{ tr('收信趋势', 'Inbound activity') }}</h3><p class="section-desc">{{ tr('按自然日统计；三组柱形使用各自峰值刻度，悬停查看原始数量。', 'Daily totals; each series uses its own peak scale. Hover for raw counts.') }}</p></div><select v-model.number="windowDays" class="form-input analytics-window-select" :aria-label="tr('统计时间范围', 'Analytics window')" @change="load"><option :value="7">7d</option><option :value="14">14d</option><option :value="30">30d</option></select></div>
      <div v-if="hasActivity" class="analytics-chart" role="img" :aria-label="tr('最近七天收信趋势图', 'Inbound activity for the last seven days')">
        <div class="analytics-chart-grid">
          <div v-for="day in days" :key="day.day" class="analytics-chart-day">
            <div class="analytics-day-bars">
              <span class="analytics-bar analytics-bar-mailbox" :style="{ height: `${Math.max(3, day.mailboxes / maxMailboxes * 100)}%`, opacity: day.mailboxes ? 1 : .16 }" :title="tr(`${day.day} · 新建邮箱 ${day.mailboxes}`, `${day.day} · ${day.mailboxes} mailboxes`)"></span>
              <span class="analytics-bar analytics-bar-email" :style="{ height: `${Math.max(3, day.emails / maxEmails * 100)}%`, opacity: day.emails ? 1 : .16 }" :title="tr(`${day.day} · 邮件 ${day.emails}`, `${day.day} · ${day.emails} emails`)"></span>
              <span class="analytics-bar analytics-bar-code" :style="{ height: `${Math.max(3, day.codes / maxCodes * 100)}%`, opacity: day.codes ? 1 : .16 }" :title="tr(`${day.day} · 验证码 ${day.codes}`, `${day.day} · ${day.codes} codes`)"></span>
            </div>
            <strong>{{ day.emails }}</strong><small>{{ day.day.slice(5) }}</small>
          </div>
        </div>
      </div>
      <div v-else class="analytics-chart-empty"><span class="analytics-empty-line"></span><strong>{{ tr('暂无收信数据', 'No inbound activity') }}</strong><small>{{ tr('收到新邮件后，趋势会显示在这里。', 'Activity will appear after new mail arrives.') }}</small></div>
      <div class="analytics-legend"><span><i class="analytics-dot mailbox"></i>{{ tr('新建邮箱', 'Mailboxes') }}</span><span><i class="analytics-dot email"></i>{{ tr('收到邮件', 'Emails') }}</span><span><i class="analytics-dot code"></i>{{ tr('验证码', 'Codes') }}</span></div>
    </section>

    <section class="analytics-detail-grid">
      <article class="section-card analytics-detail-card"><div class="section-kicker">{{ tr('域名', 'DOMAINS') }}</div><h3 class="section-title">{{ tr('域名池', 'Domain pool') }}</h3><div class="analytics-detail-value">{{ formatMetric(summary?.active_domain_total) }} <small>/ {{ formatMetric(summary?.domain_total) }}</small></div><p class="section-desc">{{ tr(`活动域名 / 总域名，待验证 ${formatMetric(summary?.pending_domain_total)} 个。`, `Active / total domains; ${formatMetric(summary?.pending_domain_total)} pending.`) }}</p></article>
      <article class="section-card analytics-detail-card"><div class="section-kicker">{{ tr('存储', 'STORAGE') }}</div><h3 class="section-title">{{ tr('邮件体积', 'Email storage') }}</h3><div class="analytics-detail-value">{{ storageLabel }}</div><p class="section-desc">{{ tr(`数据库邮件正文累计大小；raw 文件引用 ${formatMetric(summary?.raw_file_references)} 条。`, `Stored message bodies; ${formatMetric(summary?.raw_file_references)} raw file references.`) }}</p></article>
      <article class="section-card analytics-detail-card"><div class="section-kicker">{{ tr('链接', 'LINKS') }}</div><h3 class="section-title">{{ tr('验证链接', 'Verification links') }}</h3><div class="analytics-detail-value">{{ formatMetric(summary?.link_email_total) }}</div><p class="section-desc">{{ tr('已识别到验证链接的邮件数量。', 'Emails containing a detected verification link.') }}</p></article>
    </section>
  </div>
</template>
