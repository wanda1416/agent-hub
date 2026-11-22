<script lang="ts" setup>
import { Button, Input, InputNumber, InputPassword, Select, Switch, Tag, message } from 'ant-design-vue'
import { FileJson2, Gauge, RefreshCw, Save, WalletCards } from 'lucide-vue-next'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import * as AppAPI from '../../wailsjs/go/main/App'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import { useAppStore } from '../stores/app'
import type { Config as QuotaConfig, CreditBalance, Pool, Snapshot } from '../types/quota'

const appStore = useAppStore()

const snapshot = ref<Snapshot | null>(null)
const refreshing = ref(false)
const saving = ref(false)
const loadError = ref('')

function copyConfig(config: QuotaConfig): QuotaConfig {
  const defaults = [80, 90, 100]
  return {
    ...config,
    alertThresholds: defaults.map((fallback, index) => config.alertThresholds?.[index] ?? fallback),
    gemini: { ...config.gemini },
    antigravity: { ...config.antigravity },
    grok: { ...config.grok },
  }
}

const form = ref<QuotaConfig>(copyConfig(appStore.quotaConfig))

watch(
  () => appStore.quotaConfig,
  (config) => {
    form.value = copyConfig(config)
  },
  { deep: true }
)

const intervalOptions = computed(() => [5, 10, 15, 30, 60].map((value) => ({
  value,
  label: appStore.t('quota.intervalValue', { count: value }),
})))

const planKindOptions = computed(() => [
  { value: 'auto', label: appStore.t('quota.planAuto') },
  { value: 'x_unified', label: appStore.t('quota.planUnified') },
  { value: 'legacy', label: appStore.t('quota.planLegacy') },
])

const pools = computed(() => snapshot.value?.pools || [])
const credits = computed(() => snapshot.value?.credits || [])
const sources = computed(() => snapshot.value?.sources || [])

const statusKind = computed(() => {
  if (!appStore.quotaConfig.enabled || snapshot.value?.enabled === false) return 'disabled'
  if (sources.value.some((source) => source.status === 'error' || !!source.error) ||
    pools.value.some((pool) => pool.status === 'error' || !!pool.error)) return 'attention'
  if (sources.value.some((source) => source.status === 'stale' || source.status === 'degraded') ||
    pools.value.some((pool) => pool.status === 'stale' || pool.status === 'degraded')) return 'delayed'
  if (!snapshot.value?.lastPoll) return 'unknown'
  return 'healthy'
})

const statusText = computed(() => {
  switch (statusKind.value) {
    case 'disabled':
      return appStore.t('quota.statusDisabled')
    case 'attention':
      return appStore.t('quota.statusAttention')
    case 'delayed':
      return appStore.t('quota.statusDelayed')
    case 'healthy':
      return appStore.t('quota.statusHealthy')
    default:
      return appStore.t('quota.statusUnknown')
  }
})

const lastPollText = computed(() => {
  const value = snapshot.value?.lastPoll
  if (!value) return appStore.t('quota.neverChecked')
  return appStore.t('quota.lastPoll', { time: formatDateTime(value) })
})

function formatDateTime(value?: string | null) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(appStore.locale === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

function formatPercent(value?: number | null) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return `${Math.trunc(value * 100) / 100}%`
}

function formatRate(value?: number | null) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return appStore.t('quota.pointsPerHour', { value })
}

function formatBalance(credit: CreditBalance) {
  if (!Number.isFinite(credit.balance)) return '--'
  const balance = new Intl.NumberFormat(appStore.locale === 'zh' ? 'zh-CN' : 'en-US').format(credit.balance)
  return credit.unit ? `${balance} ${credit.unit}` : balance
}

function statusLabel(status: string) {
  const normalized = status === 'healthy' ? 'ok' : status
  const known = ['ok', 'stale', 'error', 'degraded', 'limited', 'disabled', 'unknown']
  return known.includes(normalized) ? appStore.t(`quota.poolStatus.${normalized}`) : status || appStore.t('quota.poolStatus.unknown')
}

function statusColor(status: string) {
  if (status === 'error') return 'error'
  if (status === 'stale' || status === 'degraded' || status === 'limited') return 'warning'
  if (status === 'ok' || status === 'healthy') return 'success'
  return 'default'
}

function poolTitle(pool: Pool) {
  return pool.groupName || pool.accountName || pool.accountId || pool.provider || appStore.t('quota.unknownAccount')
}

function poolSubtitle(pool: Pool) {
  const title = poolTitle(pool)
  return [pool.provider, pool.plan, pool.accountName && pool.accountName !== title ? pool.accountName : '']
    .filter(Boolean)
    .join(' · ')
}

type PaceState = 'behind' | 'overspend' | 'onTrack' | 'noData' | 'unknown'

function usedPercentOf(pool: Pool) {
  return typeof pool.usedPercent === 'number' && Number.isFinite(pool.usedPercent) ? pool.usedPercent : null
}

// Weekly pools run for seven days, so the cycle starts one week before the
// reported reset time.
function timeProgress(pool: Pool): number | null {
  if (!pool.resetAt) return null
  const reset = new Date(pool.resetAt).getTime()
  if (Number.isNaN(reset)) return null
  const kind = (pool.windowKind || '').toLowerCase()
  if (kind !== 'weekly' && kind !== 'week' && kind !== '7d') return null
  const start = reset - 7 * 24 * 60 * 60 * 1000
  const ratio = (Date.now() - start) / (reset - start)
  return Math.min(100, Math.max(0, ratio * 100))
}

function paceState(pool: Pool): PaceState {
  const used = usedPercentOf(pool)
  if (used === null) return 'noData'
  const time = timeProgress(pool)
  if (time === null) return 'unknown'
  const diff = used - time
  if (diff >= 1) return 'overspend'
  if (diff <= -1) return 'behind'
  return 'onTrack'
}

function paceHint(pool: Pool) {
  switch (paceState(pool)) {
    case 'behind':
      return appStore.t('quota.paceBehind')
    case 'overspend':
      return appStore.t('quota.paceOverspend')
    case 'onTrack':
      return appStore.t('quota.paceOnTrack')
    case 'noData':
      return appStore.t('quota.paceNoData')
    default:
      return appStore.t('quota.paceUnknown')
  }
}

function barSolid(pool: Pool) {
  const used = usedPercentOf(pool)
  const time = timeProgress(pool)
  const solid = time === null ? used : used === null ? null : Math.min(used, time)
  return `${solid ?? 0}%`
}

function barGap(pool: Pool) {
  const used = usedPercentOf(pool)
  const time = timeProgress(pool)
  if (used === null || time === null) return ''
  const gap = Math.abs(used - time)
  return gap >= 0.5 ? `${gap}%` : ''
}

function barMark(pool: Pool) {
  const time = timeProgress(pool)
  return time === null ? '' : `${time}%`
}

function errorText(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function applySnapshot(result: unknown) {
  snapshot.value = result as Snapshot
  loadError.value = ''
}

async function loadSnapshot() {
  try {
    const result = await AppAPI.GetQuotaSnapshot()
    applySnapshot(result)
  } catch (error) {
    loadError.value = errorText(error)
  }
}

async function refresh() {
  refreshing.value = true
  try {
    const result = await AppAPI.RefreshQuota()
    applySnapshot(result)
  } catch (error) {
    loadError.value = errorText(error)
    message.error(appStore.t('quota.refreshFailed'))
  } finally {
    refreshing.value = false
  }
}

async function saveConfig() {
  if (form.value.alertThresholds.some((value) => typeof value !== 'number' || !Number.isFinite(value) || value < 0 || value > 100)) {
    message.error(appStore.t('quota.thresholdInvalid'))
    return
  }
  saving.value = true
  try {
    await appStore.saveQuotaConfig(copyConfig(form.value))
    message.success(appStore.t('quota.saveSuccess'))
  } catch {
    message.error(appStore.t('quota.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  await loadSnapshot()
  EventsOn('quota:changed', applySnapshot)
})

onUnmounted(() => {
  EventsOff('quota:changed')
})
</script>

<template>
  <section class="quota-page">
    <header class="quota-head">
      <div>
        <div class="quota-title">
          <Gauge :size="20" />
          <span>{{ appStore.t('quota.title') }}</span>
          <span class="quota-status" :class="`is-${statusKind}`">{{ statusText }}</span>
        </div>
        <div class="quota-subtitle">{{ appStore.t('quota.subtitle') }}</div>
        <div class="quota-meta">{{ lastPollText }}</div>
      </div>
      <Button :loading="refreshing" @click="refresh">
        <RefreshCw v-if="!refreshing" :size="15" />
        <span>{{ appStore.t('quota.refresh') }}</span>
      </Button>
    </header>

    <div v-if="loadError" class="quota-error">
      <strong>{{ appStore.t('quota.loadFailed') }}</strong>
      <span>{{ loadError }}</span>
    </div>

    <div v-if="!appStore.quotaConfig.enabled" class="quota-offline">
      {{ appStore.t('quota.offlineHint') }}
    </div>

    <section class="quota-section">
      <div class="section-heading">
        <span class="section-title">{{ appStore.t('quota.poolsTitle') }}</span>
        <span class="section-count">{{ pools.length }}</span>
      </div>

      <div v-if="pools.length" class="pool-grid">
        <article
          v-for="pool in pools"
          :key="pool.id"
          class="pool-card"
          :class="{
            'is-stale': pool.status === 'stale',
            'is-error': pool.status === 'error' || !!pool.error,
            'is-limited': pool.limited,
          }"
        >
          <header class="pool-head">
            <div class="pool-identity">
              <div class="pool-name">{{ poolTitle(pool) }}</div>
              <div class="pool-provider">{{ poolSubtitle(pool) }}</div>
            </div>
            <div class="pool-remaining">
              <span class="pool-remaining-label">{{ appStore.t('quota.remaining') }}</span>
              <strong :class="`is-${paceState(pool)}`">{{ formatPercent(pool.remainingPercent) }}</strong>
              <div class="pool-tags">
                <Tag v-if="pool.status" :color="statusColor(pool.status)">{{ statusLabel(pool.status) }}</Tag>
                <Tag v-if="pool.limited && pool.status !== 'limited'" color="warning">
                  {{ appStore.t('quota.poolStatus.limited') }}
                </Tag>
              </div>
            </div>
          </header>

          <div class="pace-bar" :class="`is-${paceState(pool)}`">
            <div class="pace-solid" :style="{ width: barSolid(pool) }"></div>
            <div v-if="barGap(pool)" class="pace-gap" :style="{ width: barGap(pool) }"></div>
            <div v-if="barMark(pool)" class="pace-mark" :style="{ left: barMark(pool) }"></div>
          </div>

          <div class="pace-meta">
            <span>{{ appStore.t('quota.used') }} {{ formatPercent(pool.usedPercent) }}</span>
            <span v-if="timeProgress(pool) !== null">{{ appStore.t('quota.timeProgress') }} {{ formatPercent(timeProgress(pool)) }}</span>
            <span>{{ appStore.t('quota.resetAt') }} {{ formatDateTime(pool.resetAt) }}</span>
            <span v-if="pool.estimatedExhaustionAt">{{ appStore.t('quota.exhaustionAt') }} {{ formatDateTime(pool.estimatedExhaustionAt) }}</span>
            <span v-if="typeof pool.burnRatePerHour === 'number'">{{ appStore.t('quota.burnRate') }} {{ formatRate(pool.burnRatePerHour) }}</span>
          </div>

          <div class="pace-hint" :class="`is-${paceState(pool)}`">{{ paceHint(pool) }}</div>

          <div v-if="pool.products?.length" class="product-chips">
            <span v-for="product in pool.products" :key="product.product" class="product-chip">
              {{ product.product || appStore.t('quota.unknownProduct') }}
              <strong>{{ formatPercent(product.usedPercent) }}</strong>
            </span>
          </div>

          <div v-if="pool.error" class="pool-error">{{ pool.error }}</div>
        </article>
      </div>

      <div v-else class="quota-empty">
        <Gauge :size="28" />
        <div class="empty-title">{{ appStore.t('quota.emptyTitle') }}</div>
        <div class="empty-desc">
          {{ appStore.quotaConfig.enabled ? appStore.t('quota.emptyDesc') : appStore.t('quota.emptyDisabled') }}
        </div>
      </div>
    </section>

    <section v-if="credits.length" class="quota-section">
      <div class="section-heading">
        <span class="section-title with-icon"><WalletCards :size="16" />{{ appStore.t('quota.creditsTitle') }}</span>
        <span class="section-count">{{ credits.length }}</span>
      </div>
      <div class="credit-grid">
        <article v-for="credit in credits" :key="credit.accountId" class="credit-card">
          <div class="credit-account">{{ credit.accountId }}</div>
          <div class="credit-balance">{{ formatBalance(credit) }}</div>
          <div class="credit-meta">{{ appStore.t('quota.expiresAt') }}：{{ formatDateTime(credit.expiresAt) }}</div>
          <div class="credit-meta">{{ appStore.t('quota.observedAt') }}：{{ formatDateTime(credit.observedAt) }}</div>
        </article>
      </div>
    </section>

    <section v-if="sources.length" class="quota-section">
      <div class="section-heading">
        <span class="section-title">{{ appStore.t('quota.sourcesTitle') }}</span>
        <span class="section-count">{{ sources.length }}</span>
      </div>
      <div class="source-status-list">
        <article v-for="source in sources" :key="source.id" class="source-status-card">
          <header class="source-status-head">
            <div>
              <span class="source-status-name">{{ source.name || source.provider || source.id }}</span>
              <span v-if="source.provider" class="source-provider">{{ source.provider }}</span>
            </div>
            <Tag :color="statusColor(source.status)">{{ statusLabel(source.status) }}</Tag>
          </header>
          <div v-if="source.detail" class="source-detail">{{ source.detail }}</div>
          <div v-if="source.error" class="pool-error">{{ source.error }}</div>
          <footer class="source-times">
            <span>{{ appStore.t('quota.checkedAt') }}：{{ formatDateTime(source.checkedAt) }}</span>
            <span>{{ appStore.t('quota.lastSuccessAt') }}：{{ formatDateTime(source.lastSuccessAt) }}</span>
          </footer>
        </article>
      </div>
    </section>

    <section class="quota-settings">
      <div class="settings-section-head">
        <div>
          <div class="section-title">{{ appStore.t('quota.settingsTitle') }}</div>
          <div class="setting-desc">{{ appStore.t('quota.settingsDesc') }}</div>
        </div>
        <Button type="primary" :loading="saving" @click="saveConfig">
          <Save v-if="!saving" :size="14" />
          <span>{{ appStore.t('quota.saveSettings') }}</span>
        </Button>
      </div>

      <div class="global-settings">
        <div class="setting-block">
          <div class="setting-copy">
            <div class="setting-title">{{ appStore.t('quota.enabled') }}</div>
            <div class="setting-desc">{{ appStore.t('quota.enabledDesc') }}</div>
          </div>
          <Switch v-model:checked="form.enabled" />
        </div>

        <div class="setting-block">
          <div class="setting-copy">
            <div class="setting-title">{{ appStore.t('quota.interval') }}</div>
            <div class="setting-desc">{{ appStore.t('quota.intervalHint') }}</div>
          </div>
          <Select v-model:value="form.intervalMinutes" :options="intervalOptions" style="width: 118px" />
        </div>

        <div class="setting-block wide">
          <div class="setting-copy">
            <div class="setting-title">{{ appStore.t('quota.thresholds') }}</div>
            <div class="setting-desc">{{ appStore.t('quota.thresholdsDesc') }}</div>
          </div>
          <div class="threshold-inputs">
            <InputNumber v-model:value="form.alertThresholds[0]" :min="0" :max="100" :precision="0" addon-after="%" />
            <InputNumber v-model:value="form.alertThresholds[1]" :min="0" :max="100" :precision="0" addon-after="%" />
            <InputNumber v-model:value="form.alertThresholds[2]" :min="0" :max="100" :precision="0" addon-after="%" />
          </div>
        </div>
      </div>

      <div class="external-note">
        <div>{{ appStore.t('quota.externalHint') }}</div>
        <div>{{ appStore.t('quota.privacyHint') }}</div>
      </div>

      <div class="source-config-grid">
        <article class="source-config-card">
          <header class="source-config-head">
            <div>
              <div class="source-name">Gemini Web</div>
              <div class="source-kind">{{ appStore.t('quota.localService') }}</div>
            </div>
            <Switch v-model:checked="form.gemini.enabled" size="small" />
          </header>
          <label class="field-label">{{ appStore.t('quota.baseURL') }}</label>
          <Input v-model:value="form.gemini.baseURL" placeholder="http://127.0.0.1:8083" />
          <label class="field-label">Admin Token</label>
          <InputPassword v-model:value="form.gemini.adminToken" autocomplete="off" />
        </article>

        <article class="source-config-card">
          <header class="source-config-head">
            <div>
              <div class="source-name">Antigravity</div>
              <div class="source-kind">{{ appStore.t('quota.localService') }}</div>
            </div>
            <Switch v-model:checked="form.antigravity.enabled" size="small" />
          </header>
          <label class="field-label">{{ appStore.t('quota.baseURL') }}</label>
          <Input v-model:value="form.antigravity.baseURL" placeholder="http://127.0.0.1:8045" />
          <label class="field-label">Admin Token</label>
          <InputPassword v-model:value="form.antigravity.adminToken" autocomplete="off" />
        </article>

        <article class="source-config-card">
          <header class="source-config-head">
            <div>
              <div class="source-name">Grok</div>
              <div class="source-kind">{{ appStore.t('quota.snapshotFile') }}</div>
            </div>
            <Switch v-model:checked="form.grok.enabled" size="small" />
          </header>
          <label class="field-label">{{ appStore.t('quota.snapshotPath') }}</label>
          <Input v-model:value="form.grok.snapshotPath">
            <template #prefix><FileJson2 :size="14" /></template>
          </Input>
          <label class="field-label">{{ appStore.t('quota.planKind') }}</label>
          <Select v-model:value="form.grok.planKind" :options="planKindOptions" style="width: 100%" />
          <div class="field-hint">{{ appStore.t('quota.unifiedHint') }}</div>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.quota-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px;
  border: 1px solid rgba(226, 232, 240, 0.6);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.8);
  backdrop-filter: blur(4px);
}

.dark .quota-page {
  border-color: rgba(51, 65, 85, 0.6);
  background: rgba(30, 41, 59, 0.8);
}

.quota-head,
.settings-section-head,
.section-heading,
.source-config-head,
.pool-head,
.source-status-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.quota-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 20px;
  font-weight: 700;
  color: #0f172a;
}

.dark .quota-title {
  color: #f1f5f9;
}

.quota-status {
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 11.5px;
  font-weight: 600;
}

.quota-status.is-healthy {
  color: #047857;
  background: #d1fae5;
}

.quota-status.is-delayed {
  color: #b45309;
  background: #fef3c7;
}

.quota-status.is-attention {
  color: #b91c1c;
  background: #fee2e2;
}

.quota-status.is-disabled,
.quota-status.is-unknown {
  color: #475569;
  background: #e2e8f0;
}

.dark .quota-status.is-healthy {
  color: #6ee7b7;
  background: rgba(6, 95, 70, 0.4);
}

.dark .quota-status.is-delayed {
  color: #fcd34d;
  background: rgba(120, 53, 15, 0.4);
}

.dark .quota-status.is-attention {
  color: #fca5a5;
  background: rgba(127, 29, 29, 0.4);
}

.dark .quota-status.is-disabled,
.dark .quota-status.is-unknown {
  color: #cbd5e1;
  background: #334155;
}

.quota-subtitle,
.quota-meta {
  margin-top: 4px;
  font-size: 13px;
  color: #64748b;
}

.quota-meta {
  font-size: 12px;
  color: #94a3b8;
}

.quota-settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 18px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.85);
}

.dark .quota-settings {
  border-color: #334155;
  background: rgba(15, 23, 42, 0.35);
}

.section-title,
.setting-title {
  font-size: 13.5px;
  font-weight: 650;
  color: #0f172a;
}

.section-title {
  font-size: 14px;
}

.section-title.with-icon {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.setting-desc,
.field-hint {
  margin-top: 3px;
  font-size: 12px;
  line-height: 1.5;
  color: #94a3b8;
}

.dark .section-title,
.dark .setting-title {
  color: #e2e8f0;
}

.global-settings {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px 24px;
}

.setting-block {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.setting-block.wide {
  grid-column: span 2;
}

.setting-copy {
  min-width: 0;
}

.threshold-inputs {
  display: flex;
  gap: 8px;
}

.threshold-inputs :deep(.ant-input-number-group-wrapper) {
  width: 96px;
}

.external-note {
  padding: 10px 12px;
  border-left: 3px solid rgba(16, 185, 129, 0.45);
  border-radius: 6px;
  background: rgba(16, 185, 129, 0.06);
  font-size: 12px;
  line-height: 1.65;
  color: #64748b;
}

.dark .external-note {
  color: #94a3b8;
  background: rgba(16, 185, 129, 0.08);
}

.source-config-grid,
.pool-grid,
.credit-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 14px;
}

.source-config-card,
.pool-card,
.credit-card,
.source-status-card {
  padding: 14px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  border-radius: 12px;
  background: rgba(248, 250, 252, 0.75);
}

.dark .source-config-card,
.dark .pool-card,
.dark .credit-card,
.dark .source-status-card {
  border-color: #334155;
  background: rgba(15, 23, 42, 0.55);
}

.source-config-head {
  align-items: center;
  margin-bottom: 12px;
}

.source-name,
.pool-name,
.credit-account,
.source-status-name {
  font-size: 14px;
  font-weight: 650;
  color: #0f172a;
}

.dark .source-name,
.dark .pool-name,
.dark .credit-account,
.dark .source-status-name {
  color: #f1f5f9;
}

.source-kind,
.pool-provider,
.source-provider {
  margin-top: 2px;
  font-size: 11.5px;
  color: #94a3b8;
}

.field-label {
  display: block;
  margin: 10px 0 5px;
  font-size: 12px;
  font-weight: 600;
  color: #64748b;
}

.dark .field-label {
  color: #94a3b8;
}

.quota-offline,
.quota-error {
  display: flex;
  gap: 8px;
  padding: 10px 14px;
  border: 1px solid #fde68a;
  border-radius: 10px;
  background: #fef3c7;
  font-size: 12.5px;
  color: #92400e;
}

.quota-error {
  flex-direction: column;
  border-color: #fecaca;
  background: #fef2f2;
  color: #b91c1c;
  word-break: break-word;
}

.dark .quota-offline {
  border-color: rgba(180, 83, 9, 0.5);
  background: rgba(120, 53, 15, 0.35);
  color: #fcd34d;
}

.dark .quota-error {
  border-color: rgba(153, 27, 27, 0.5);
  background: rgba(127, 29, 29, 0.3);
  color: #fca5a5;
}

.quota-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.section-heading {
  align-items: center;
  justify-content: flex-start;
}

.section-count {
  padding: 1px 7px;
  border-radius: 999px;
  background: #e2e8f0;
  font-size: 11px;
  color: #64748b;
}

.dark .section-count {
  background: #334155;
  color: #cbd5e1;
}

.pool-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  background: rgba(255, 255, 255, 0.9);
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.04);
}

.dark .pool-card {
  background: rgba(15, 23, 42, 0.55);
  box-shadow: none;
}

.pool-card.is-stale {
  border-color: #fbbf24;
}

.pool-card.is-error {
  border-color: rgba(239, 68, 68, 0.55);
}

.pool-card.is-limited:not(.is-error) {
  border-color: rgba(245, 158, 11, 0.5);
}

.pool-identity {
  min-width: 0;
}

.pool-name,
.pool-provider {
  overflow-wrap: anywhere;
}

.pool-tags {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 4px;
}

.pool-tags :deep(.ant-tag) {
  margin-inline-end: 0;
}

.pool-remaining {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
  flex-shrink: 0;
}

.pool-remaining-label {
  font-size: 11.5px;
  color: #94a3b8;
}

.pool-remaining strong {
  font-size: 26px;
  line-height: 1.1;
  font-variant-numeric: tabular-nums;
  color: #047857;
}

.pool-remaining strong.is-overspend {
  color: #dc2626;
}

.pool-remaining strong.is-noData,
.pool-remaining strong.is-unknown {
  color: #64748b;
}

.dark .pool-remaining strong {
  color: #6ee7b7;
}

.dark .pool-remaining strong.is-overspend {
  color: #f87171;
}

.dark .pool-remaining strong.is-noData,
.dark .pool-remaining strong.is-unknown {
  color: #94a3b8;
}

.pace-bar {
  position: relative;
  display: flex;
  height: 10px;
  border-radius: 999px;
  overflow: hidden;
  background: #e2e8f0;
}

.dark .pace-bar {
  background: #1e293b;
}

.pace-solid {
  height: 100%;
  background: #10b981;
  transition: width 0.3s ease;
}

.pace-gap {
  height: 100%;
  transition: width 0.3s ease;
}

.pace-bar.is-behind .pace-gap {
  background-image: repeating-linear-gradient(45deg, #f59e0b 0 4px, rgba(245, 158, 11, 0.25) 4px 8px);
}

.pace-bar.is-overspend .pace-gap {
  background-image: repeating-linear-gradient(45deg, #ef4444 0 4px, rgba(239, 68, 68, 0.28) 4px 8px);
}

.pace-mark {
  position: absolute;
  top: -1px;
  bottom: -1px;
  width: 2px;
  background: rgba(15, 23, 42, 0.55);
}

.dark .pace-mark {
  background: rgba(226, 232, 240, 0.65);
}

.pace-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 11.5px;
  color: #64748b;
}

.pace-hint {
  font-size: 11.5px;
  color: #64748b;
}

.pace-hint.is-behind {
  color: #b45309;
}

.pace-hint.is-overspend {
  color: #b91c1c;
}

.pace-hint.is-onTrack {
  color: #047857;
}

.dark .pace-hint.is-behind {
  color: #fbbf24;
}

.dark .pace-hint.is-overspend {
  color: #f87171;
}

.dark .pace-hint.is-onTrack {
  color: #6ee7b7;
}

.product-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.product-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 9px;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.16);
  font-size: 11.5px;
  color: #475569;
}

.product-chip strong {
  font-variant-numeric: tabular-nums;
}

.dark .product-chip {
  color: #cbd5e1;
}

.pool-error {
  padding: 8px 10px;
  border-radius: 8px;
  background: #fef2f2;
  font-size: 12px;
  line-height: 1.5;
  color: #b91c1c;
  word-break: break-word;
}

.dark .pool-error {
  background: rgba(127, 29, 29, 0.3);
  color: #fca5a5;
}

.quota-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 7px;
  padding: 32px 16px;
  border: 1px dashed #cbd5e1;
  border-radius: 12px;
  color: #94a3b8;
  text-align: center;
}

.dark .quota-empty {
  border-color: #475569;
}

.empty-title {
  font-size: 14px;
  font-weight: 600;
  color: #64748b;
}

.dark .empty-title {
  color: #cbd5e1;
}

.empty-desc {
  max-width: 440px;
  font-size: 12px;
  line-height: 1.6;
}

.credit-balance {
  margin: 8px 0;
  font-size: 22px;
  font-weight: 700;
  color: #047857;
}

.dark .credit-balance {
  color: #6ee7b7;
}

.credit-meta,
.source-detail,
.source-times {
  margin-top: 4px;
  font-size: 11.5px;
  line-height: 1.5;
  color: #94a3b8;
}

.source-status-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.source-status-card {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.source-status-head {
  align-items: center;
}

.source-provider {
  display: inline-block;
  margin: 0 0 0 8px;
}

.source-detail {
  margin: 0;
  color: #64748b;
  word-break: break-word;
}

.source-times {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
}

@media (max-width: 720px) {
  .quota-head {
    flex-wrap: wrap;
  }

  .setting-block.wide {
    grid-column: auto;
    align-items: flex-start;
    flex-direction: column;
  }

  .source-config-grid,
  .pool-grid,
  .credit-grid {
    grid-template-columns: 1fr;
  }

  .threshold-inputs {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
