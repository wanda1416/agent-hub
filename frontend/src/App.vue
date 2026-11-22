<script lang="ts" setup>
import { ConfigProvider, theme } from 'ant-design-vue'
import enUS from 'ant-design-vue/es/locale/en_US'
import zhCN from 'ant-design-vue/es/locale/zh_CN'
import { Cpu, Radar, Settings } from 'lucide-vue-next'
import { computed, getCurrentInstance, onMounted, onUnmounted, ref } from 'vue'
import AppLogo from './components/AppLogo.vue'
import MonitorAlert from './components/MonitorAlert.vue'
import UpdateDownloadModal from './components/UpdateDownloadModal.vue'
import MonitorPage from './views/Monitor.vue'
import ProcessList from './views/Process.vue'
import SettingsPage from './views/Setting.vue'
import type { MonitorAlertPayload, MonitorSnapshot } from './types/monitor'
import { trackVisit } from './services/analytics'
import { autoCheckVersion, isAppStoreBuild } from './services/version'
import { useAppStore } from './stores/app'
import { initTestRegistry, registerNavigate, reportTestError, reportTestWarn, testActionSet, testActionUnset } from './utils/test'
import * as AppAPI from '../wailsjs/go/main/App'
import { EventsEmit, EventsOff, EventsOn } from '../wailsjs/runtime/runtime'

const appStore = useAppStore()

// Active tab state
type Tab = 'processes' | 'monitor' | 'settings'
const activeTab = ref<Tab>('processes')
const monitorUnread = ref(false)

// Strong alert state. Snoozing keeps the payload so the modal can reappear
// after ten minutes without marking the announcement as acknowledged.
const alertOpen = ref(false)
const alertPayload = ref<MonitorAlertPayload | null>(null)
let snoozeTimer: number | undefined
let snoozedPayload: MonitorAlertPayload | null = null

function switchTab(tab: Tab) {
  activeTab.value = tab
  if (tab === 'monitor') monitorUnread.value = false
}

function handleMonitorAlert(payload: MonitorAlertPayload) {
  alertPayload.value = payload
  alertOpen.value = true
}

function acknowledgeAlert() {
  alertOpen.value = false
  alertPayload.value = null
  snoozedPayload = null
  if (snoozeTimer) {
    window.clearTimeout(snoozeTimer)
    snoozeTimer = undefined
  }
}

function snoozeAlert() {
  snoozedPayload = alertPayload.value
  alertOpen.value = false
  alertPayload.value = null
  if (snoozeTimer) window.clearTimeout(snoozeTimer)
  snoozeTimer = window.setTimeout(() => {
    if (snoozedPayload) {
      alertPayload.value = snoozedPayload
      alertOpen.value = true
      snoozedPayload = null
    }
  }, 10 * 60 * 1000)
}

function viewMonitor() {
  acknowledgeAlert()
  switchTab('monitor')
}

// Track main page visit on mount
onMounted(async () => {
  // Initialize settings from backend
  await appStore.initSettings()

  trackVisit('Main')

  // Auto check version after 5 seconds (disabled in App Store builds)
  if (!isAppStoreBuild) {
    autoCheckVersion(5000)
  }

  // ── 自动化测试：初始化 window.__test，注册 App 级 action ─────────────────
  const _vueApp = getCurrentInstance()?.appContext.app
  initTestRegistry({
    onStartTestMode: async () => {
      // 接入 Vue 全局错误处理
      if (_vueApp) {
        const _origErrorHandler = _vueApp.config.errorHandler
        _vueApp.config.errorHandler = (err, _instance, info) => {
          const msg = err instanceof Error ? err.message : String(err)
          reportTestError(`[vue:${info}] ${msg}`)
          if (_origErrorHandler) _origErrorHandler(err, _instance, info)
          else console.error(`[vue:${info}]`, err)
        }
        _vueApp.config.warnHandler = (msg, _instance, trace) => {
          reportTestWarn(`[vue:warn] ${msg}${trace ? '\n' + trace : ''}`)
        }
      }
      // 拦截 ant-design-vue message.error / message.warning
      const { message: antMessage } = await import('ant-design-vue')
      const _origMsgError = antMessage.error.bind(antMessage)
      const _origMsgWarning = antMessage.warning.bind(antMessage)
      ;(antMessage as unknown as Record<string, unknown>).error = (...args: unknown[]) => {
        reportTestError(`[message.error] ${String(args[0])}`)
        return (_origMsgError as (...a: unknown[]) => unknown)(...args)
      }
      ;(antMessage as unknown as Record<string, unknown>).warning = (...args: unknown[]) => {
        reportTestWarn(`[message.warning] ${String(args[0])}`)
        return (_origMsgWarning as (...a: unknown[]) => unknown)(...args)
      }
    },
  })

  registerNavigate(async (path: string) => {
    if (path === 'processes' || path === 'monitor' || path === 'settings') {
      switchTab(path)
    }
  })

  // App 级 action
  testActionSet('App.getTitle', () => document.title)
  testActionSet('App.getActiveTab', () => activeTab.value)
  testActionSet('App.navigate', async (params: unknown) => {
    const { tab } = params as { tab: string }
    if (tab === 'processes' || tab === 'monitor' || tab === 'settings') {
      switchTab(tab)
    }
    return activeTab.value
  })
  testActionSet('App.exists', (params: unknown) => {
    const { selector } = params as { selector: string }
    return !!document.querySelector(selector)
  })
  testActionSet('App.count', (params: unknown) => {
    const { selector } = params as { selector: string }
    return document.querySelectorAll(selector).length
  })
  testActionSet('App.getText', (params: unknown) => {
    const { selector } = params as { selector: string }
    return document.querySelector(selector)?.textContent?.trim() ?? null
  })
  testActionSet('App.click', (params: unknown) => {
    const { selector } = params as { selector: string }
    const el = document.querySelector(selector) as HTMLElement | null
    if (!el) throw new Error(`元素不存在: "${selector}"`)
    el.click()
    return true
  })
  testActionSet('App.getConfig', () => AppAPI.GetConfig())

  // ── 截图专用 action（prepare / cleanup）───────────────────────────────────
  

  // 监听来自 Go 后端的调用请求（由 HTTP /auto ui-call 转发过来）
  EventsOn('autotest:call', async (data: { id: string; name: string; params: unknown }) => {
    try {
      const result = await (window as unknown as Record<string, { callAction: (n: string, a?: unknown) => Promise<unknown> }>).__test?.callAction(data.name, data.params)
      EventsEmit('autotest:result:' + data.id, { result })
    } catch (e: unknown) {
      EventsEmit('autotest:result:' + data.id, { error: (e as Error)?.message || String(e) })
    }
  })

  // 配置由 CLI 或托盘等外部来源改变时，整份配置回填 Store，界面各处自动同步
  EventsOn('config:changed', (data: { config: Record<string, unknown>; origin: string }) => {
    appStore.applyConfig(data.config)
  })
  // 进程列表或状态变化（含 CLI 启停、自动重启）时刷新列表
  EventsOn('processes:changed', () => {
    appStore.loadProcesses()
  })
  // 重置公告变化：不在监控页时点亮侧边栏红点
  EventsOn('monitor:changed', (snapshot: MonitorSnapshot) => {
    if (activeTab.value === 'monitor') return
    const hasActive = (snapshot?.models || []).some(
      (model) => model.followed && (model.events || []).some((event) => event.active)
    )
    if (hasActive) monitorUnread.value = true
  })
  EventsOn('monitor:alert', handleMonitorAlert)
  // 点击系统通知后跳转到监控页
  EventsOn('monitor:focus', () => switchTab('monitor'))
})

onUnmounted(() => {
  EventsOff('autotest:call')
  EventsOff('config:changed')
  EventsOff('processes:changed')
  EventsOff('monitor:changed')
  EventsOff('monitor:alert')
  EventsOff('monitor:focus')
  if (snoozeTimer) window.clearTimeout(snoozeTimer)
  testActionUnset([
    'App.getConfig',
  ])
})

const antLocale = computed(() => {
  return appStore.locale === 'zh' ? zhCN : enUS
})

const themeConfig = computed(() => ({
  algorithm: appStore.isDark ? theme.darkAlgorithm : theme.defaultAlgorithm,
  token: {
    colorPrimary: appStore.isDark ? '#34d399' : '#10b981',
    borderRadius: 8,
    fontFamily:
      'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
  },
}))
</script>

<template>
  <ConfigProvider :locale="antLocale" :theme="themeConfig">
    <div class="app-shell">
      <div class="app-layout">
        <!-- Left Sidebar with Vertical Tabs -->
        <div class="sidebar">
          <div class="logo-area">
            <AppLogo class="logo-image" />
            <span class="logo-text">AgentHub</span>
          </div>
          <div class="sidebar-tabs">
            <a-button
              class="tab-button"
              :class="{ active: activeTab === 'processes' }"
              type="text"
              block
              @click="switchTab('processes')"
            >
              <Cpu class="w-5 h-5" aria-hidden="true" />
              <span class="tab-label">{{ appStore.t('processes.title') }}</span>
            </a-button>
            <a-button
              class="tab-button"
              :class="{ active: activeTab === 'monitor' }"
              type="text"
              block
              @click="switchTab('monitor')"
            >
              <Radar class="w-5 h-5" aria-hidden="true" />
              <span class="tab-label">{{ appStore.t('monitor.title') }}</span>
              <span v-if="monitorUnread" class="tab-dot" aria-hidden="true" />
            </a-button>
            <a-button
              class="tab-button"
              :class="{ active: activeTab === 'settings' }"
              type="text"
              block
              @click="switchTab('settings')"
            >
              <Settings class="w-5 h-5" aria-hidden="true" />
              <span class="tab-label">{{ appStore.t('settings.title') }}</span>
            </a-button>
          </div>
        </div>

        <!-- Main Content Area -->
        <div class="main-content">
          <!-- Processes View -->
          <div v-show="activeTab === 'processes'" class="content-view">
            <ProcessList />
          </div>

          <!-- Monitor View -->
          <div v-show="activeTab === 'monitor'" class="content-view">
            <MonitorPage />
          </div>

          <!-- Settings View -->
          <div v-show="activeTab === 'settings'" class="content-view">
            <SettingsPage />
          </div>
        </div>
      </div>
    </div>

    <!-- 强弹窗提醒 -->
    <MonitorAlert
      :open="alertOpen"
      :payload="alertPayload"
      @acknowledge="acknowledgeAlert"
      @snooze="snoozeAlert"
      @view="viewMonitor"
    />

    <!-- 下载进度弹窗 / 后台下载指示 -->
    <UpdateDownloadModal />
  </ConfigProvider>
</template>

<style scoped>
.tab-dot {
  width: 6px;
  height: 6px;
  margin-top: 1px;
  border-radius: 50%;
  background: #ef4444;
  box-shadow: 0 0 0 3px rgba(239, 68, 68, 0.18);
}
</style>
