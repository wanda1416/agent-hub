export interface MonitorPost {
  author?: string
  publishedAt?: string
  stage?: string
  text?: string
  originalText?: string
  url?: string
}

export interface MonitorEstimate {
  from?: string
  through?: string
  basis?: string
  label?: string
  reason?: string
}

export interface MonitorPresentation {
  status?: string
  scopeKnown?: boolean
  scopeLabel?: string
  audienceZh?: string
  productsZh?: string
}

export interface MonitorEvent {
  id: string
  type?: string
  label?: string
  displayLabel?: string
  presentation?: MonitorPresentation
  estimate?: MonitorEstimate
  status?: string
  title?: string
  scope?: string
  createdAt?: string
  updatedAt?: string
  confirmedAt?: string
  occurredOn?: string
  posts?: MonitorPost[]
  url?: string
  active?: boolean
}

export interface MonitorModelSnapshot {
  id: string
  name: string
  pageUrl: string
  followed: boolean
  status: string
  error?: string
  siteStatus?: string
  checkedAt?: string
  lastVerifiedAt?: string
  events: MonitorEvent[]
}

export interface MonitorSnapshot {
  enabled: boolean
  interval: number
  alertMode: string
  alertSound: boolean
  lastPoll?: string
  models: MonitorModelSnapshot[]
}

export interface MonitorAlert {
  modelId: string
  modelName: string
  pageUrl: string
  events: MonitorEvent[]
}

export interface MonitorAlertPayload {
  alerts: MonitorAlert[]
  sound: boolean
  test: boolean
}
