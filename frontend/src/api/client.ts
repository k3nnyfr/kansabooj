// Typed client for the Kansabooj REST API (backend/internal/httpapi).

export type ApiStatus = 'pending' | 'ok' | 'error' | 'auth_error' | 'stale'
export type Level = 'error' | 'warn' | 'info'

export interface ApiRow {
  id: string
  name: string
  owner?: string
  source_url: string
  source_type: string
  enabled: boolean
  status: ApiStatus
  last_error?: string
  last_error_kind?: string
  consecutive_errors: number
  last_checked_at?: string
  next_check_at?: string
  version?: string
  format?: string
  title?: string
  operation_count: number
  snapshot_id?: number
  last_change_at?: string
  change_count: number
  unacknowledged: number
  unacknowledged_breaking: number
}

export interface ApiConfig {
  interval?: string
  gateway?: { exposed_operations?: string[] }
  notify?: string[]
}

export interface CheckRow {
  started_at: string
  duration_ms: number
  trigger: string
  status: 'unchanged' | 'changed' | 'error'
  error_kind?: string
  http_status?: number
  error?: string
}

export interface ApiDetail {
  api: ApiRow
  config: ApiConfig
  checks: CheckRow[]
}

export interface TimelineEntry {
  id: number
  detected_at: string
  from_snapshot_id?: number
  to_snapshot_id: number
  version_from?: string
  version_to?: string
  errors: number
  warns: number
  infos: number
  in_scope: number
  in_scope_errors: number
  first_capture: boolean
  acknowledged_at?: string
  acknowledged_by?: string
}

export interface ChangeItem {
  rule_id: string
  level: Level
  method?: string
  path?: string
  in_scope: boolean
  text: string
}

export interface ChangeDetail extends TimelineEntry {
  api_id: string
  items: ChangeItem[]
}

export interface CheckResult {
  status: 'unchanged' | 'changed' | 'error'
  not_modified: boolean
  duration_ms: number
  error?: string
  error_kind?: string
  change_id?: number
}

export interface Meta {
  version: string
  language: 'fr' | 'en'
  languages: string[]
  user: string
}

export interface ServiceStatus {
  scheduler: { last_tick: string; config_loaded_at: string; running: string[]; next_checks: Record<string, string> }
  failing: ApiRow[]
  total: number
}

async function request<T>(method: string, path: string): Promise<T> {
  const res = await fetch(path, { method, headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      msg = ((await res.json()) as { error?: string }).error ?? msg
    } catch {
      /* not JSON */
    }
    throw new Error(msg)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

const enc = encodeURIComponent

export const api = {
  meta: () => request<Meta>('GET', '/api/meta'),
  status: () => request<ServiceStatus>('GET', '/api/status'),
  list: () => request<ApiRow[]>('GET', '/api/apis'),
  get: (id: string) => request<ApiDetail>('GET', `/api/apis/${enc(id)}`),
  timeline: (id: string, before?: number) =>
    request<TimelineEntry[]>('GET', `/api/apis/${enc(id)}/timeline?limit=30${before ? `&before=${before}` : ''}`),
  change: (id: number) => request<ChangeDetail>('GET', `/api/changes/${id}`),
  check: (id: string) => request<CheckResult>('POST', `/api/apis/${enc(id)}/check`),
  ack: (changeId: number) => request<void>('POST', `/api/changes/${changeId}/ack`),
  ackAll: (id: string) => request<{ acknowledged: number }>('POST', `/api/apis/${enc(id)}/ack`),
  specUrl: (snapshotId: number, form: 'raw' | 'normalized') => `/api/snapshots/${snapshotId}/spec?form=${form}`,
}
