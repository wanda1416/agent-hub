<script lang="ts" setup>
import { Button, Segmented, Select, Switch, Tag, message } from 'ant-design-vue'
import { BellRing, ExternalLink, Radar, RefreshCw, Volume2 } from 'lucide-vue-next'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import * as AppAPI from '../../wailsjs/go/main/App'
import { BrowserOpenURL, EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import { MONITOR_MODEL_IDS, useAppStore } from '../stores/app'
import type { MonitorAlertMode } from '../stores/app'
import type { MonitorEvent, MonitorModelSnapshot, MonitorSnapshot } from '../types/monitor'

const appStore = useAppStore()

const snapshot = ref<MonitorSnapshot | null>(null)
const refreshing = ref(false)
let refreshTimer: number | undefined

const intervalOptions = [5, 10, 15, 30, 60].map((value) => ({
  value,
  label: appStore.t('monitor.intervalValue', { count: value }),
}))

const alertModeOptions = computed(() => [
  { value: 'inapp', label: appStore.t('monitor.alertInapp') },
  { value: 'notification', label: appStore.t('monitor.alertNotification') },
  { value: 'popup', label: appStore.t('monitor.alertPopup') },
])

const followedModels = computed(() => snapshot.value?.models.filter((model) => model.followed) ?? [])

const statusKind = computed(() => {
  if (!appStore.monitorEnabled) return 'disabled'
  if (!snapshot.value?.lastPoll) return 'unknown'
  if (followedModels.value.some((model) => model.status === 'error')) return 'attention'
  if (followedModels.value.some((model) => model.siteStatus && model.siteStatus !== 'healthy')) return 'delayed'
  return 'healthy'
})

const statusText = computed(() => {
  switch (statusKind.value) {
    case 'disabled':
      return appStore.t('monitor.statusDisabled')
    case 'attention':
      return appStore.t('monitor.statusAttention')
    case 'delayed':
      return appStore.t('monitor.statusDelayed')
    case 'healthy':
      return appStore.t('monitor.statusHealthy')
    default:
      return appStore.t('monitor.statusUnknown')
  }
})

const lastPollText = computed(() => {
  const value = snapshot.value?.lastPoll
  if (!value) return appStore.t('monitor.neverChecked')
  return appStore.t('monitor.lastPoll', { time: formatTime(value) })
})

function formatTime(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleTimeString(appStore.locale === 'zh' ? 'zh-CN' : 'en-US', {
    hour: '2-digit',
    minute: '2-digit',
  })
}

function openUrl(url?: string) {
  if (url) BrowserOpenURL(url)
}

function activeEvents(model: MonitorModelSnapshot): MonitorEvent[] {
  return (model.events || []).filter((event) => event.active)
}

function historyEvents(model: MonitorModelSnapshot): MonitorEvent[] {
  return (model.events || []).filter((event) => !event.active).slice(0, 5)
}

function truncate(value: string, limit: number) {
  return value.length > limit ? `${value.slice(0, limit)}…` : value
}

async function loadSnapshot() {
  try {
    const result = await AppAPI.GetMonitorSnapshot()
    snapshot.value = result as unknown as MonitorSnapshot
  } catch (error) {
    console.error('Failed to load monitor snapshot:', error)
  }
}

function applySnapshot(result: unknown) {
  snapshot.value = result as MonitorSnapshot
  if (refreshing.value) {
    refreshing.value = false
    if (refreshTimer) window.clearTimeout(refreshTimer)
  }
}

async function refresh() {
  refreshing.value = true
  try {
    await AppAPI.RefreshMonitor()
    refreshTimer = window.setTimeout(() => {
      refreshing.value = false
    }, 4000)
  } catch (error) {
    refreshing.value = false
    console.error('Failed to refresh monitor:', error)
  }
}

function toggleEnabled(value: unknown) {
  appStore.saveMonitorConfig({ enabled: Boolean(value) }).catch(() => {
    message.error(appStore.t('monitor.saveFailed'))
  })
}

function changeInterval(value: unknown) {
  appStore.saveMonitorConfig({ interval: Number(value) }).catch(() => {
    message.error(appStore.t('monitor.saveFailed'))
  })
}

function changeAlertMode(value: unknown) {
  appStore.saveMonitorConfig({ alertMode: value as MonitorAlertMode }).catch(() => {
    message.error(appStore.t('monitor.saveFailed'))
  })
}

function toggleSound(value: unknown) {
  appStore.saveMonitorConfig({ alertSound: Boolean(value) }).catch(() => {
    message.error(appStore.t('monitor.saveFailed'))
  })
}

function toggleModel(model: MonitorModelSnapshot, value: unknown) {
  const followed = Boolean(value)
  const selected = new Set(appStore.monitorModels)
  if (followed) selected.add(model.id)
  else selected.delete(model.id)
  const ordered = MONITOR_MODEL_IDS.filter((id) => selected.has(id))
  appStore.saveMonitorConfig({ models: [...ordered] }).catch(() => {
    message.error(appStore.t('monitor.saveFailed'))
  })
}

async function testAlert() {
  try {
    await AppAPI.TestMonitorAlert()
  } catch (error) {
    console.error('Failed to send test alert:', error)
    message.error(appStore.t('monitor.saveFailed'))
  }
}

onMounted(async () => {
  await loadSnapshot()
  EventsOn('monitor:changed', applySnapshot)
})

onUnmounted(() => {
  EventsOff('monitor:changed')
  if (refreshTimer) window.clearTimeout(refreshTimer)
})
</script>

<template>
  <section class="monitor-page">
    <header class="monitor-head">
      <div class="monitor-heading">
        <div class="monitor-title">
          <Radar :size="20" />
          <span>{{ appStore.t('monitor.title') }}</span>
          <span class="monitor-status" :class="`is-${statusKind}`">{{ statusText }}</span>
        </div>
        <div class="monitor-subtitle">{{ appStore.t('monitor.subtitle') }}</div>
        <div class="monitor-meta">{{ lastPollText }}</div>
      </div>
      <Button :loading="refreshing" @click="refresh">
        <RefreshCw v-if="!refreshing" :size="15" />
        <span>{{ appStore.t('monitor.refresh') }}</span>
      </Button>
    </header>

    <div class="monitor-settings">
      <div class="setting-block">
        <div class="setting-copy">
          <div class="setting-title">{{ appStore.t('monitor.enabled') }}</div>
          <div class="setting-desc">{{ appStore.t('monitor.enabledDesc') }}</div>
        </div>
        <Switch :checked="appStore.monitorEnabled" @change="toggleEnabled" />
      </div>

      <div class="setting-block">
        <div class="setting-copy">
          <div class="setting-title">{{ appStore.t('monitor.interval') }}</div>
          <div class="setting-desc">{{ appStore.t('monitor.intervalHint') }}</div>
        </div>
        <Select
          :value="appStore.monitorInterval"
          :options="intervalOptions"
          style="width: 118px"
          @change="changeInterval"
        />
      </div>

      <div class="setting-block wide">
        <div class="setting-copy">
          <div class="setting-title">{{ appStore.t('monitor.alertMode') }}</div>
          <div class="setting-desc">{{ appStore.t('monitor.alertModeDesc') }}</div>
        </div>
        <Segmented
          :value="appStore.monitorAlertMode"
          :options="alertModeOptions"
          @change="changeAlertMode"
        />
      </div>

      <div class="setting-block">
        <div class="setting-copy">
          <div class="setting-title">
            <Volume2 :size="14" />
            {{ appStore.t('monitor.alertSound') }}
          </div>
          <div class="setting-desc">{{ appStore.t('monitor.alertSoundDesc') }}</div>
        </div>
        <Switch :checked="appStore.monitorAlertSound" @change="toggleSound" />
      </div>

      <div class="setting-action">
        <Button @click="testAlert">
          <BellRing :size="15" />
          <span>{{ appStore.t('monitor.testAlert') }}</span>
        </Button>
      </div>
    </div>

    <div v-if="!appStore.monitorEnabled" class="monitor-offline">
      {{ appStore.t('monitor.offlineHint') }}
    </div>

    <div class="model-grid">
      <article
        v-for="model in snapshot?.models || []"
        :key="model.id"
        class="model-card"
        :class="{ muted: !model.followed || !appStore.monitorEnabled }"
      >
        <header class="model-head">
          <div class="model-identity">
            <span class="model-dot" :class="`is-${model.followed ? model.status : 'disabled'}`" />
            <span class="model-name">{{ model.name }}</span>
            <Tag v-if="model.status === 'error' && model.followed" color="error">
              {{ appStore.t('monitor.fetchError') }}
            </Tag>
            <Tag v-else-if="model.siteStatus && model.siteStatus !== 'healthy'" color="warning">
              {{ model.siteStatus }}
            </Tag>
          </div>
          <Switch
            size="small"
            :checked="model.followed"
            @change="(value) => toggleModel(model, value)"
          />
        </header>

        <div v-if="!model.followed" class="model-empty">{{ appStore.t('monitor.modelDisabled') }}</div>

        <template v-else>
          <div v-if="model.error" class="model-error">{{ model.error }}</div>

          <div v-if="activeEvents(model).length" class="event-list">
            <div
              v-for="event in activeEvents(model)"
              :key="event.id"
              class="event-item active"
            >
              <div class="event-row">
                <span class="event-chip">{{ event.displayLabel || event.label || event.type }}</span>
                <span v-if="event.estimate?.label" class="event-estimate">
                  {{ appStore.t('monitor.estimate') }} {{ event.estimate.label }}
                </span>
              </div>
              <div class="event-title">{{ event.title || event.displayLabel }}</div>
              <div class="event-scope">
                {{ appStore.t('monitor.scope') }}：{{
                  event.presentation?.scopeLabel || event.scope || appStore.t('monitor.scopeUnknown')
                }}
              </div>
              <div v-if="event.posts?.length && event.posts[0].text" class="event-quote">
                {{ truncate(event.posts[0].text, 150) }}
              </div>
              <button class="event-link" type="button" @click="openUrl(event.url || model.pageUrl)">
                {{ appStore.t('monitor.source') }}
                <ExternalLink :size="12" />
              </button>
            </div>
          </div>
          <div v-else-if="!model.error" class="model-empty">{{ appStore.t('monitor.noActive') }}</div>

          <details v-if="historyEvents(model).length" class="model-history">
            <summary>{{ appStore.t('monitor.recent') }} ({{ historyEvents(model).length }})</summary>
            <div
              v-for="event in historyEvents(model)"
              :key="event.id"
              class="event-item history"
            >
              <div class="event-row">
                <span class="event-chip subtle">{{ event.displayLabel || event.label || event.type }}</span>
                <span class="event-confirmed">{{ appStore.t('monitor.confirmed') }}</span>
              </div>
              <div class="event-title">{{ event.title || event.displayLabel }}</div>
              <div v-if="event.occurredOn" class="event-scope">{{ event.occurredOn }}</div>
            </div>
          </details>

          <footer class="model-foot">
            <button class="model-page-link" type="button" @click="openUrl(model.pageUrl)">
              {{ appStore.t('monitor.openPage') }}
              <ExternalLink :size="12" />
            </button>
            <span v-if="model.checkedAt">{{ formatTime(model.checkedAt) }}</span>
          </footer>
        </template>
      </article>
    </div>

    <footer class="monitor-source">
      {{ appStore.t('monitor.dataSource') }}：
      <button type="button" @click="openUrl('https://aihot.news/resets')">AIHOT</button>
      <span>· {{ appStore.t('monitor.dataSourceHint') }}</span>
    </footer>
  </section>
</template>

<style scoped>
.monitor-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.monitor-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.monitor-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 20px;
  font-weight: 700;
  color: #0f172a;
}

.dark .monitor-title {
  color: #f1f5f9;
}

.monitor-status {
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 11.5px;
  font-weight: 600;
}

.monitor-status.is-healthy {
  color: #047857;
  background: #d1fae5;
}

.monitor-status.is-delayed {
  color: #b45309;
  background: #fef3c7;
}

.monitor-status.is-attention {
  color: #b91c1c;
  background: #fee2e2;
}

.monitor-status.is-disabled,
.monitor-status.is-unknown {
  color: #475569;
  background: #e2e8f0;
}

.dark .monitor-status.is-disabled,
.dark .monitor-status.is-unknown {
  color: #cbd5e1;
  background: #334155;
}

.dark .monitor-status.is-healthy {
  color: #6ee7b7;
  background: rgba(6, 95, 70, 0.4);
}

.dark .monitor-status.is-delayed {
  color: #fcd34d;
  background: rgba(120, 53, 15, 0.4);
}

.dark .monitor-status.is-attention {
  color: #fca5a5;
  background: rgba(127, 29, 29, 0.4);
}

.monitor-subtitle {
  margin-top: 4px;
  font-size: 13px;
  color: #64748b;
}

.monitor-meta {
  margin-top: 4px;
  font-size: 12px;
  color: #94a3b8;
}

.monitor-settings {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 14px 24px;
  padding: 18px;
  border-radius: 14px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  background: rgba(255, 255, 255, 0.85);
  backdrop-filter: blur(8px);
}

.dark .monitor-settings {
  background: rgba(30, 41, 59, 0.75);
  border-color: #334155;
}

.setting-block {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.setting-block.wide {
  grid-column: span 2;
  justify-content: flex-start;
}

.setting-copy {
  min-width: 0;
}

.setting-title {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 13.5px;
  font-weight: 600;
  color: #0f172a;
}

.setting-desc {
  margin-top: 2px;
  font-size: 12px;
  color: #94a3b8;
}

.dark .setting-title {
  color: #e2e8f0;
}

.setting-action {
  display: flex;
  align-items: center;
  justify-content: flex-end;
}

.monitor-offline {
  padding: 10px 14px;
  border-radius: 10px;
  font-size: 13px;
  color: #92400e;
  background: #fef3c7;
  border: 1px solid #fde68a;
}

.dark .monitor-offline {
  color: #fcd34d;
  background: rgba(120, 53, 15, 0.35);
  border-color: rgba(180, 83, 9, 0.5);
}

.model-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 14px;
}

.model-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  border-radius: 14px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  background: rgba(255, 255, 255, 0.9);
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.04);
  transition: opacity 0.15s ease;
}

.dark .model-card {
  background: rgba(30, 41, 59, 0.8);
  border-color: #334155;
  box-shadow: none;
}

.model-card.muted {
  opacity: 0.62;
}

.model-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.model-identity {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.model-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #cbd5e1;
}

.model-dot.is-ok {
  background: #10b981;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.15);
}

.model-dot.is-error {
  background: #ef4444;
  box-shadow: 0 0 0 3px rgba(239, 68, 68, 0.15);
}

.model-name {
  font-size: 15px;
  font-weight: 700;
  color: #0f172a;
}

.dark .model-name {
  color: #f1f5f9;
}

.model-empty {
  padding: 10px 0;
  font-size: 12.5px;
  color: #94a3b8;
}

.model-error {
  padding: 8px 10px;
  border-radius: 8px;
  font-size: 12px;
  color: #b91c1c;
  background: #fef2f2;
  word-break: break-word;
}

.dark .model-error {
  color: #fca5a5;
  background: rgba(127, 29, 29, 0.3);
}

.event-list,
.model-history {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.event-item {
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  background: #f8fafc;
}

.event-item.active {
  border-color: rgba(16, 185, 129, 0.45);
  background: rgba(16, 185, 129, 0.06);
}

.dark .event-item {
  background: #0f172a;
  border-color: #334155;
}

.dark .event-item.active {
  background: rgba(16, 185, 129, 0.12);
  border-color: rgba(52, 211, 153, 0.4);
}

.event-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.event-chip {
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  color: #047857;
  background: #d1fae5;
}

.event-chip.subtle {
  color: #475569;
  background: #e2e8f0;
}

.event-estimate {
  font-size: 11.5px;
  color: #059669;
}

.event-confirmed {
  margin-left: auto;
  font-size: 11px;
  color: #94a3b8;
}

.event-title {
  margin-top: 6px;
  font-size: 13.5px;
  font-weight: 600;
  color: #0f172a;
}

.dark .event-title {
  color: #e2e8f0;
}

.event-scope {
  margin-top: 3px;
  font-size: 12px;
  color: #64748b;
}

.event-quote {
  margin-top: 6px;
  padding-left: 8px;
  border-left: 2px solid rgba(16, 185, 129, 0.35);
  font-size: 12px;
  line-height: 1.55;
  color: #64748b;
}

.event-link,
.model-page-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 7px;
  padding: 0;
  border: none;
  background: none;
  font-size: 12px;
  color: #10b981;
  cursor: pointer;
}

.event-link:hover,
.model-page-link:hover {
  color: #059669;
}

.model-history summary {
  font-size: 12px;
  font-weight: 600;
  color: #64748b;
  cursor: pointer;
}

.model-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: 6px;
  font-size: 11.5px;
  color: #94a3b8;
}

.model-foot .model-page-link {
  margin-top: 0;
}

.monitor-source {
  padding-top: 4px;
  font-size: 11.5px;
  color: #94a3b8;
}

.monitor-source button {
  padding: 0;
  border: none;
  background: none;
  font-size: 11.5px;
  color: #10b981;
  cursor: pointer;
}
</style>
