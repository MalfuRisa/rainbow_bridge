import { fetchApi, StaleSessionError } from './http'
import { apiURL } from './url'
import { ApiError } from './error'
import { sessionGeneration } from '../auth/session'

export type BackupKind = 'create' | 'validate' | 'restore'
export type BackupState = 'queued' | 'running' | 'succeeded' | 'failed' | 'interrupted' | 'restart_required' | 'cancelled'
export interface BackupCounts {
  environments: number; pipelines: number; configs: number; assets: number; versions: number
  deleted_versions: number; freeze_events: number; release_events: number; configs_deleted: number; assets_deleted: number
}
export interface BackupItem {
  backup_id: string; created_at: string; consistent_at: string; sha256: string; size: number
  counts: BackupCounts; actor: string; reason: string
}
export interface BackupJob {
  job_id: string; request_id: string; kind: BackupKind; state: BackupState; phase: string
  created_at: string; updated_at: string; actor: string; reason: string
  backup_id: string | null; preview_id: string | null
  result: { backup_id?: string; preview_id?: string; requires_restart?: boolean } | null
  error: { error_type: string; msg: string } | null
}
export interface BackupCapabilities {
  supported: boolean; reasons: string[]; restore_requires_restart: boolean; mode: string
  limits: { package_bytes: number; max_backups: number }
  rollback_retention?: { keep_count: number; managed: boolean }
}
export interface RestorePreview {
  preview_id: string; job_id: string; valid: boolean; package_sha256: string; expires_at: string; target_instance_id: string
  backup?: { backup_id: string; created_at: string; consistent_at: string; counts: BackupCounts; files: number }
  warnings: string[]; issues: string[]
}
export type BackupAction = 'create' | 'restore/preview' | 'restore/execute' | 'restore/cancel' | 'delete'
export interface BackupIntent { action: BackupAction; payload: Record<string, string>; file?: { name: string; size: number; lastModified: number } }

// This module alone accepts the backup contract's code=202; other API envelopes stay unchanged.
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const generation = sessionGeneration()
  const response = await fetchApi(apiURL('/api/v1/backup/' + path), init)
  const body = await response.json()
  if (generation !== sessionGeneration()) throw new StaleSessionError()
  if (![200, 202].includes(body?.code) || !body.data) {
    const error = new ApiError(body?.code || 502, '备份服务响应异常，请确认请求结果')
    error.errorType = body?.error_type || ''
    throw error
  }
  return body.data as T
}

export const backupApi = {
  capabilities: () => request<BackupCapabilities>('capabilities'),
  list: (before = '') => request<{ items: BackupItem[]; has_more: boolean; next_before_id: string }>('list?' + new URLSearchParams({ limit: '20', before_id: before })),
  status: async (id: string) => (await request<{ job: BackupJob }>('status?' + new URLSearchParams({ job_id: id }))).job,
  preview: (id: string) => request<RestorePreview>('restore/preview?' + new URLSearchParams({ preview_id: id })),
  async mutate(intent: BackupIntent, file?: File): Promise<{ job?: BackupJob; deleted?: boolean; cancelled?: boolean }> {
    if (intent.action === 'restore/preview') {
      if (!file) throw new Error('请重新选择上次上传的原备份包')
      const data = new FormData()
      data.append('request_id', intent.payload.request_id!)
      data.append('file', file)
      return request(intent.action, { method: 'POST', body: data })
    }
    return request(intent.action, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(intent.payload) })
  },
  async download(id: string) {
    const generation = sessionGeneration()
    const response = await fetchApi(apiURL('/api/v1/backup/download?' + new URLSearchParams({ backup_id: id })))
    if (response.headers.get('content-type')?.includes('application/json')) throw new Error('下载响应不是备份 ZIP，请重试')
    const blob = await response.blob()
    if (generation !== sessionGeneration()) throw new StaleSessionError()
    if (!blob.size) throw new Error('备份文件为空，请重试')
    const signature = new Uint8Array(await blob.slice(0, 4).arrayBuffer())
    if (generation !== sessionGeneration()) throw new StaleSessionError()
    if (signature[0] !== 0x50 || signature[1] !== 0x4b || !((signature[2] === 3 && signature[3] === 4) || (signature[2] === 5 && signature[3] === 6))) throw new Error('下载响应不是备份 ZIP，请重试')
    const filename = /filename\*?=(?:UTF-8''|"?)([^";]+)/i.exec(response.headers.get('content-disposition') || '')?.[1]
    return { blob, filename: filename ? decodeURIComponent(filename).replace(/[\\/:*?"<>|]/g, '_') : `rainbow-business-backup-${id}.zip` }
  },
}
