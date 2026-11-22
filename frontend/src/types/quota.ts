export interface ServiceConfig {
  enabled: boolean
  baseURL: string
  adminToken: string
}

export interface GrokConfig {
  enabled: boolean
  snapshotPath: string
  planKind: 'auto' | 'x_unified' | 'legacy'
}

export interface Config {
  enabled: boolean
  intervalMinutes: number
  alertThresholds: number[]
  gemini: ServiceConfig
  antigravity: ServiceConfig
  grok: GrokConfig
}

export interface ProductUsage {
  product: string
  usedPercent?: number | null
}

export interface Pool {
  id: string
  provider: string
  accountId: string
  accountName: string
  groupName: string
  plan: string
  planKind: string
  windowKind: string
  usedPercent?: number | null
  remainingPercent?: number | null
  resetAt?: string | null
  burnRatePerHour?: number | null
  estimatedExhaustionAt?: string | null
  observedAt?: string | null
  status: string
  error: string
  sourceKind: string
  limited: boolean
  products: ProductUsage[]
}

export interface CreditBalance {
  accountId: string
  balance: number
  unit: string
  expiresAt?: string | null
  observedAt?: string | null
}

export interface SourceStatus {
  id: string
  provider: string
  name: string
  status: string
  error: string
  checkedAt?: string | null
  lastSuccessAt?: string | null
  detail: string
}

export interface Snapshot {
  enabled: boolean
  intervalMinutes: number
  lastPoll?: string | null
  pools: Pool[]
  credits: CreditBalance[]
  sources: SourceStatus[]
}

export interface Alert {
  poolId: string
  poolName: string
  threshold: number
  usedPercent: number
  resetAt?: string | null
}

export interface AlertPayload {
  alerts: Alert[]
}
