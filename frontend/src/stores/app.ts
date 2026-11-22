import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import * as AppAPI from '../../wailsjs/go/main/App'
import { process as ProcessModels, quota as QuotaModels } from '../../wailsjs/go/models'
import { i18n } from '../plugins/i18n'
import { trackError } from '../services/analytics'
import type { Config as QuotaConfig } from '../types/quota'

export type ProcessStatus = 'running' | 'stopped' | 'errored' | 'starting'

export type MonitorAlertMode = 'inapp' | 'notification' | 'popup'

export const MONITOR_MODEL_IDS = ['codex', 'antigravity', 'claude', 'grok-bot'] as const

export interface ProcessItem {
  id: string
  name: string
  command: string
  args: string[]
  workingDir: string
  status: ProcessStatus
  autoStart: boolean
  autoRestart: boolean
  restartPolicy: string
  maxRetries: number
  env: Record<string, string>
  pid: number
  restarts: number
  lastError: string
}

export interface ProcessMemory {
  pid: number
  bytes: number
}

// How same-name processes are treated when importing a process file.
export type ProcessImportMode = 'skip' | 'overwrite'

// One entry of a process file as reported by the backend preview. conflict is
// set to "name" when a process with the same name already exists, and error is
// set when the entry cannot be imported at all.
export interface ProcessImportCandidate {
  definition: {
    id: string
    name: string
    command: string
    args?: string[]
    workingDir?: string
    env?: Record<string, string>
    autoStart?: boolean
    restartPolicy?: string
    maxRetries?: number
  }
  conflict: string
  error: string
}

export interface ProcessImportPreview {
  path: string
  appVersion: string
  exportedAt?: string
  total: number
  importable: number
  conflicts: number
  processes: ProcessImportCandidate[]
}

export interface ProcessImportResult {
  total: number
  imported: string[]
  updated: string[]
  skipped: string[]
  invalid: string[]
}

function defaultQuotaConfig(): QuotaConfig {
  return {
    enabled: true,
    intervalMinutes: 5,
    alertThresholds: [80, 90, 100],
    gemini: {
      enabled: false,
      baseURL: 'http://127.0.0.1:8083',
      adminToken: '',
    },
    antigravity: {
      enabled: false,
      baseURL: 'http://127.0.0.1:8045',
      adminToken: '',
    },
    grok: {
      enabled: false,
      snapshotPath: '%LOCALAPPDATA%\\GrokUsagePet\\usage.json',
      planKind: 'auto',
    },
  }
}

function normalizeQuotaConfig(config?: QuotaConfig | QuotaModels.Config | null): QuotaConfig {
  const defaults = defaultQuotaConfig()
  if (!config) return defaults
  const planKind = config.grok?.planKind
  return {
    enabled: typeof config.enabled === 'boolean' ? config.enabled : defaults.enabled,
    intervalMinutes: typeof config.intervalMinutes === 'number' ? config.intervalMinutes : defaults.intervalMinutes,
    alertThresholds: Array.isArray(config.alertThresholds) ? [...config.alertThresholds] : defaults.alertThresholds,
    gemini: { ...defaults.gemini, ...config.gemini },
    antigravity: { ...defaults.antigravity, ...config.antigravity },
    grok: {
      ...defaults.grok,
      ...config.grok,
      planKind: planKind === 'x_unified' || planKind === 'legacy' ? planKind : 'auto',
    },
  }
}

export const useAppStore = defineStore('app', () => {
  const locale = ref<'zh' | 'en'>('zh')
  const isDark = ref(false)
  const autoStart = ref(false)
  const autoStartWSL = ref(false)
  const processes = ref<ProcessItem[]>([])
  // Working-set memory per process, refreshed only on demand. Keyed by id and
  // carrying the pid so a restarted process does not show the old number.
  const processMemory = ref<Record<string, ProcessMemory>>({})
  const logs = ref<string[]>([])

  // Reset monitor settings mirrored from the backend configuration.
  const monitorEnabled = ref(true)
  const monitorInterval = ref(10)
  const monitorModels = ref<string[]>([...MONITOR_MODEL_IDS])
  const monitorAlertMode = ref<MonitorAlertMode>('notification')
  const monitorAlertSound = ref(true)

  // Quota settings mirrored from the backend configuration.
  const quotaConfig = ref<QuotaConfig>(defaultQuotaConfig())

  const t = (key: string, params?: Record<string, unknown>) => i18n.global.t(key, params || {})

  // applyLocale updates the in-memory locale without touching the backend.
  const applyLocale = (nextLocale: 'zh' | 'en') => {
    locale.value = nextLocale
    i18n.global.locale.value = nextLocale
  }

  const setLocale = async (nextLocale: 'zh' | 'en') => {
    applyLocale(nextLocale)
    // Persist to backend config so the CLI shares the same source of truth.
    try {
      await AppAPI.SetLocale(nextLocale)
    } catch (error) {
      console.error('Failed to save locale setting:', error)
    }
  }

  // applyTheme applies the theme locally and mirrors it to localStorage so the
  // first-paint script can avoid a light flash.
  const applyTheme = (nextTheme: 'light' | 'dark') => {
    const dark = nextTheme === 'dark'
    isDark.value = dark
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('theme', nextTheme)
  }

  const setTheme = async (nextIsDark: boolean) => {
    const nextTheme = nextIsDark ? 'dark' : 'light'
    applyTheme(nextTheme)
    try {
      await AppAPI.SetTheme(nextTheme)
    } catch (error) {
      console.error('Failed to save theme setting:', error)
    }
  }

  // applyConfig applies a full configuration snapshot pushed by the backend.
  // It is the single entry point for configuration arriving from any source
  // (GUI, CLI, tray), so the UI never drifts from the persisted state.
  const applyConfig = (config: {
    theme?: string
    locale?: string
    autoStart?: boolean
    autoStartWSL?: boolean
    monitorEnabled?: boolean
    monitorInterval?: number
    monitorModels?: string[]
    monitorAlertMode?: string
    monitorAlertSound?: boolean
    quota?: QuotaConfig | QuotaModels.Config
  }) => {
    if (config.theme) applyTheme(config.theme === 'dark' ? 'dark' : 'light')
    if (config.locale) applyLocale(config.locale === 'en' ? 'en' : 'zh')
    if (typeof config.autoStart === 'boolean') autoStart.value = config.autoStart
    if (typeof config.autoStartWSL === 'boolean') autoStartWSL.value = config.autoStartWSL
    if (typeof config.monitorEnabled === 'boolean') monitorEnabled.value = config.monitorEnabled
    if (typeof config.monitorInterval === 'number') monitorInterval.value = config.monitorInterval
    if (Array.isArray(config.monitorModels)) monitorModels.value = [...config.monitorModels]
    if (config.monitorAlertMode === 'inapp' || config.monitorAlertMode === 'notification' || config.monitorAlertMode === 'popup') {
      monitorAlertMode.value = config.monitorAlertMode
    }
    if (typeof config.monitorAlertSound === 'boolean') monitorAlertSound.value = config.monitorAlertSound
    if (config.quota) quotaConfig.value = normalizeQuotaConfig(config.quota)
  }

  // saveMonitorConfig persists a partial change and applies it locally right
  // away so switches do not wait for the config:changed round trip.
  const saveMonitorConfig = async (patch: Partial<{
    enabled: boolean
    interval: number
    models: string[]
    alertMode: MonitorAlertMode
    alertSound: boolean
  }>) => {
    if (patch.enabled !== undefined) monitorEnabled.value = patch.enabled
    if (patch.interval !== undefined) monitorInterval.value = patch.interval
    if (patch.models !== undefined) monitorModels.value = [...patch.models]
    if (patch.alertMode !== undefined) monitorAlertMode.value = patch.alertMode
    if (patch.alertSound !== undefined) monitorAlertSound.value = patch.alertSound
    try {
      await AppAPI.SetMonitorConfig(
        monitorEnabled.value,
        monitorInterval.value,
        [...monitorModels.value],
        monitorAlertMode.value,
        monitorAlertSound.value
      )
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to save monitor config: ${errorMsg}`)
      console.error('Failed to save monitor config:', error)
      throw error
    }
  }

  const saveQuotaConfig = async (nextConfig: QuotaConfig) => {
    const previous = normalizeQuotaConfig(quotaConfig.value)
    const next = normalizeQuotaConfig(nextConfig)
    quotaConfig.value = next
    try {
      await AppAPI.SetQuotaConfig(new QuotaModels.Config(next))
    } catch (error) {
      quotaConfig.value = previous
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to save quota config: ${errorMsg}`)
      console.error('Failed to save quota config:', error)
      throw error
    }
  }

  // Load processes from backend
  const loadProcesses = async () => {
    try {
      const snapshots = await AppAPI.ListProcesses()
      processes.value = snapshots.map((snap: ProcessModels.Snapshot) => ({
        id: snap.definition.id,
        name: snap.definition.name,
        command: snap.definition.command,
        args: snap.definition.args || [],
        workingDir: snap.definition.workingDir || '',
        status: snap.status as ProcessStatus,
        autoStart: snap.definition.autoStart || false,
        autoRestart: snap.definition.autoRestart || false,
        restartPolicy: snap.definition.restartPolicy || 'on_failure',
        maxRetries: snap.definition.maxRetries || 0,
        env: snap.definition.env || {},
        pid: snap.pid,
        restarts: snap.restarts,
        lastError: snap.lastError || '',
      }))
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to load processes: ${errorMsg}`)
      console.error('Failed to load processes:', error)
    }
  }

  // Sample working-set memory. Deliberately not part of the periodic status
  // poll: the process page calls it on mount and from the refresh button only.
  const loadProcessMemory = async () => {
    try {
      const items = await AppAPI.GetProcessMemory()
      const next: Record<string, ProcessMemory> = {}
      for (const item of items || []) {
        next[item.id] = { pid: item.pid, bytes: item.bytes }
      }
      processMemory.value = next
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to load process memory: ${errorMsg}`)
      console.error('Failed to load process memory:', error)
    }
  }

  // Add a new process
  const addProcess = async (definition: ProcessModels.Definition) => {
    try {
      await AppAPI.AddProcess(definition)
      await loadProcesses()
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to add process: ${errorMsg}`)
      console.error('Failed to add process:', error)
      throw error
    }
  }

  // Remove a process
  const removeProcess = async (id: string) => {
    try {
      await AppAPI.RemoveProcess(id)
      await loadProcesses()
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to remove process: ${errorMsg}`)
      console.error('Failed to remove process:', error)
      throw error
    }
  }

  // Update a process
  const updateProcess = async (id: string, definition: ProcessModels.Definition) => {
    try {
      await AppAPI.UpdateProcess(id, definition)
      await loadProcesses()
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to update process: ${errorMsg}`)
      console.error('Failed to update process:', error)
      throw error
    }
  }

  // Toggle the auto-start flag of a process without stopping it
  const setProcessAutoStart = async (id: string, enabled: boolean) => {
    try {
      await AppAPI.SetProcessAutoStart(id, enabled)
      await loadProcesses()
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to set auto-start: ${errorMsg}`)
      console.error('Failed to set auto-start:', error)
      throw error
    }
  }

  // Start a process
  const startProcess = async (id: string) => {
    try {
      await AppAPI.StartProcess(id)
      // Refresh after a short delay to get updated status
      setTimeout(loadProcesses, 500)
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to start process: ${errorMsg}`)
      console.error('Failed to start process:', error)
      throw error
    }
  }

  // Stop a process
  const stopProcess = async (id: string) => {
    try {
      await AppAPI.StopProcess(id)
      setTimeout(loadProcesses, 500)
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to stop process: ${errorMsg}`)
      console.error('Failed to stop process:', error)
      throw error
    }
  }

  // Restart a process
  const restartProcess = async (id: string) => {
    try {
      await AppAPI.RestartProcess(id)
      setTimeout(loadProcesses, 500)
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to restart process: ${errorMsg}`)
      console.error('Failed to restart process:', error)
      throw error
    }
  }

  // Export every process definition to a user-chosen JSON file. Returns the
  // saved path, or "" when the user cancelled the save dialog.
  const exportProcesses = async () => {
    try {
      return await AppAPI.ExportProcesses()
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to export processes: ${errorMsg}`)
      console.error('Failed to export processes:', error)
      throw error
    }
  }

  // Open the import file picker and return the chosen path ("" on cancel).
  const selectProcessImportFile = async () => AppAPI.SelectProcessImportFile()

  // Parse a process file without applying it, so the user can review entries
  // and same-name conflicts first.
  const previewProcessImport = async (path: string) => {
    const preview = await AppAPI.PreviewProcessImport(path)
    return preview as unknown as ProcessImportPreview
  }

  // Apply a previously previewed process file and refresh the list.
  const importProcesses = async (path: string, mode: ProcessImportMode) => {
    try {
      const result = (await AppAPI.ImportProcesses(path, mode)) as unknown as ProcessImportResult
      await loadProcesses()
      return result
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to import processes: ${errorMsg}`)
      console.error('Failed to import processes:', error)
      throw error
    }
  }

  // Load logs for a specific process
  const loadProcessLogs = async (id: string) => {
    try {
      const entries = await AppAPI.GetProcessLogs(id)
      logs.value = entries.map((entry: any) => {
        const date = new Date(entry.timestamp)
        const timestamp = date.toLocaleString('zh-CN', {
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
          hour12: false
        })
        return `[${timestamp}] ${entry.stream}: ${entry.line}`
      })
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error)
      trackError(`Failed to load logs: ${errorMsg}`)
      console.error('Failed to load logs:', error)
    }
  }

  const runningCount = computed(() => processes.value.filter((item) => item.status === 'running').length)
  const stoppedCount = computed(() => processes.value.filter((item) => item.status === 'stopped').length)
  const failedCount = computed(() => processes.value.filter((item) => item.status === 'errored').length)

  // Initialize app settings from backend
  const initSettings = async () => {
    try {
      const config = await AppAPI.GetConfig()
      // Load theme from backend config, falling back to localStorage for
      // installs created before the theme field existed.
      const savedTheme = (config.theme as string) || localStorage.getItem('theme') || 'light'
      applyConfig({
        theme: savedTheme,
        locale: (config.locale as string) || 'zh',
        autoStart: config.autoStart,
        autoStartWSL: config.autoStartWSL,
        monitorEnabled: config.monitorEnabled,
        monitorInterval: config.monitorInterval,
        monitorModels: config.monitorModels,
        monitorAlertMode: config.monitorAlertMode,
        monitorAlertSound: config.monitorAlertSound,
        quota: config.quota,
      })
    } catch (error) {
      console.error('Failed to load settings:', error)
    }
  }

  return {
    locale,
    isDark,
    autoStart,
    autoStartWSL,
    processes,
    logs,
    monitorEnabled,
    monitorInterval,
    monitorModels,
    monitorAlertMode,
    monitorAlertSound,
    quotaConfig,
    runningCount,
    stoppedCount,
    failedCount,
    setLocale,
    setTheme,
    saveMonitorConfig,
    saveQuotaConfig,
    applyLocale,
    applyTheme,
    applyConfig,
    t,
    initSettings,
    loadProcesses,
    addProcess,
    removeProcess,
    updateProcess,
    setProcessAutoStart,
    startProcess,
    stopProcess,
    restartProcess,
    processMemory,
    loadProcessMemory,
    loadProcessLogs,
    exportProcesses,
    selectProcessImportFile,
    previewProcessImport,
    importProcesses,
  }
})

