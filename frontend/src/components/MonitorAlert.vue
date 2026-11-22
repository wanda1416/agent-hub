<script lang="ts" setup>
import { Modal } from 'ant-design-vue'
import { BellRing, Clock3, ExternalLink, Radar } from 'lucide-vue-next'
import { computed, onUnmounted, watch } from 'vue'
import { BrowserOpenURL, WindowSetAlwaysOnTop } from '../../wailsjs/runtime/runtime'
import type { MonitorAlertPayload, MonitorEvent } from '../types/monitor'
import { useAppStore } from '../stores/app'

const props = defineProps<{
  open: boolean
  payload: MonitorAlertPayload | null
}>()

const emit = defineEmits<{
  (e: 'acknowledge'): void
  (e: 'snooze'): void
  (e: 'view'): void
}>()

const appStore = useAppStore()

const rows = computed(() => {
  const payload = props.payload
  if (!payload) return [] as { modelName: string; pageUrl: string; event: MonitorEvent }[]
  const list: { modelName: string; pageUrl: string; event: MonitorEvent }[] = []
  payload.alerts.forEach((alert) => {
    alert.events.forEach((event) => {
      list.push({ modelName: alert.modelName, pageUrl: alert.pageUrl, event })
    })
  })
  return list
})

const alertTitle = computed(() =>
  props.payload?.test ? appStore.t('alert.testTitle') : appStore.t('alert.title')
)

function openExternal(url?: string) {
  if (!url) return
  BrowserOpenURL(url)
}

// A short three-note chime generated with the Web Audio API; no asset needed.
function playChime() {
  if (!props.payload?.sound) return
  try {
    const Ctor = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!Ctor) return
    const ctx = new Ctor()
    const now = ctx.currentTime
    ;[0, 0.3, 0.6].forEach((offset, index) => {
      const oscillator = ctx.createOscillator()
      const gain = ctx.createGain()
      oscillator.type = 'sine'
      oscillator.frequency.value = index === 1 ? 880 : 660
      gain.gain.setValueAtTime(0.0001, now + offset)
      gain.gain.exponentialRampToValueAtTime(0.25, now + offset + 0.02)
      gain.gain.exponentialRampToValueAtTime(0.0001, now + offset + 0.24)
      oscillator.connect(gain)
      gain.connect(ctx.destination)
      oscillator.start(now + offset)
      oscillator.stop(now + offset + 0.26)
    })
    window.setTimeout(() => ctx.close().catch(() => undefined), 1400)
  } catch {
    // Audio is a nice-to-have; the modal is the actual alert.
  }
}

watch(
  () => props.open,
  (open) => {
    WindowSetAlwaysOnTop(open)
    if (open) playChime()
  },
  { immediate: true }
)

onUnmounted(() => WindowSetAlwaysOnTop(false))

function acknowledge() {
  WindowSetAlwaysOnTop(false)
  emit('acknowledge')
}

function snooze() {
  WindowSetAlwaysOnTop(false)
  emit('snooze')
}
</script>

<template>
  <Modal
    :open="open"
    :closable="false"
    :mask-closable="false"
    :keyboard="false"
    :footer="null"
    :width="560"
    wrap-class-name="monitor-alert-modal"
  >
    <div class="monitor-alert">
      <div class="monitor-alert-head">
        <div class="monitor-alert-icon">
          <BellRing :size="22" />
        </div>
        <div>
          <div class="monitor-alert-title">
            {{ alertTitle }}
            <span v-if="payload?.test" class="monitor-alert-badge">{{ appStore.t('alert.testBadge') }}</span>
          </div>
          <div class="monitor-alert-sub">
            {{ appStore.t('alert.subtitle', { count: rows.length }) }}
          </div>
        </div>
      </div>

      <div class="monitor-alert-list">
        <div v-for="(row, index) in rows" :key="index" class="monitor-alert-item">
          <div class="monitor-alert-row">
            <span class="monitor-alert-model">{{ row.modelName }}</span>
            <span v-if="row.event.estimate?.label" class="monitor-alert-time">
              <Clock3 :size="13" />
              {{ appStore.t('monitor.estimate') }} {{ row.event.estimate.label }}
            </span>
          </div>
          <div class="monitor-alert-event">{{ row.event.title || row.event.displayLabel || row.event.label }}</div>
          <div v-if="row.event.presentation?.scopeLabel || row.event.scope" class="monitor-alert-scope">
            {{ appStore.t('monitor.scope') }}：{{ row.event.presentation?.scopeLabel || row.event.scope }}
          </div>
          <div v-if="row.event.posts?.length && row.event.posts[0].text" class="monitor-alert-quote">
            {{ row.event.posts[0].text }}
          </div>
          <button class="monitor-alert-link" type="button" @click="openExternal(row.event.url || row.pageUrl)">
            {{ appStore.t('monitor.source') }}
            <ExternalLink :size="13" />
          </button>
        </div>
      </div>

      <div class="monitor-alert-actions">
        <button class="monitor-alert-button ghost" type="button" @click="snooze">
          <Clock3 :size="15" />
          {{ appStore.t('alert.later') }}
        </button>
        <button class="monitor-alert-button ghost" type="button" @click="emit('view')">
          <Radar :size="15" />
          {{ appStore.t('alert.viewMonitor') }}
        </button>
        <button class="monitor-alert-button primary" type="button" @click="acknowledge">
          {{ appStore.t('alert.acknowledge') }}
        </button>
      </div>
    </div>
  </Modal>
</template>

<style>
.monitor-alert-modal .ant-modal-content {
  border-radius: 16px;
  padding: 0;
  overflow: hidden;
}

.monitor-alert {
  padding: 24px;
}

.monitor-alert-head {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  margin-bottom: 18px;
}

.monitor-alert-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  flex-shrink: 0;
  border-radius: 12px;
  color: #fff;
  background: linear-gradient(135deg, #f59e0b 0%, #ef4444 100%);
  box-shadow: 0 6px 16px rgba(239, 68, 68, 0.28);
}

.monitor-alert-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 18px;
  font-weight: 700;
  line-height: 1.3;
}

.monitor-alert-badge {
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  color: #b45309;
  background: #fef3c7;
}

.monitor-alert-sub {
  margin-top: 4px;
  font-size: 13px;
  color: #64748b;
}

.monitor-alert-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 46vh;
  overflow-y: auto;
  padding-right: 4px;
}

.monitor-alert-item {
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid rgba(226, 232, 240, 0.9);
  background: #f8fafc;
}

.monitor-alert-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.monitor-alert-model {
  font-size: 13px;
  font-weight: 700;
  color: #0f172a;
}

.monitor-alert-time {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: #059669;
}

.monitor-alert-event {
  margin-top: 6px;
  font-size: 14px;
  font-weight: 600;
  color: #0f172a;
}

.monitor-alert-scope,
.monitor-alert-quote {
  margin-top: 4px;
  font-size: 12.5px;
  line-height: 1.6;
  color: #64748b;
}

.monitor-alert-quote {
  padding-left: 10px;
  border-left: 2px solid rgba(16, 185, 129, 0.4);
}

.monitor-alert-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 8px;
  padding: 0;
  border: none;
  background: none;
  font-size: 12.5px;
  color: #10b981;
  cursor: pointer;
}

.monitor-alert-link:hover {
  color: #059669;
}

.monitor-alert-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
}

.monitor-alert-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 36px;
  padding: 0 14px;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid transparent;
  transition: all 0.15s ease;
}

.monitor-alert-button.ghost {
  color: #475569;
  border-color: #e2e8f0;
  background: #fff;
}

.monitor-alert-button.ghost:hover {
  color: #0f172a;
  border-color: #cbd5e1;
}

.monitor-alert-button.primary {
  color: #fff;
  background: #10b981;
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.28);
}

.monitor-alert-button.primary:hover {
  background: #059669;
}

.dark .monitor-alert-title,
.dark .monitor-alert-event,
.dark .monitor-alert-model {
  color: #f1f5f9;
}

.dark .monitor-alert-sub,
.dark .monitor-alert-scope,
.dark .monitor-alert-quote {
  color: #94a3b8;
}

.dark .monitor-alert-item {
  background: #1e293b;
  border-color: #334155;
}

.dark .monitor-alert-button.ghost {
  color: #cbd5e1;
  background: #1e293b;
  border-color: #334155;
}
</style>
