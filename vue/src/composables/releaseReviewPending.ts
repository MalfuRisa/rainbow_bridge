import { getBasePath } from '@/lib/api/client'
import type { ReleaseCreatePayload, ReleaseActivatePayload } from '@/lib/api/release'
import type { CompareTarget } from '@/lib/api/release-compare'

export type PendingRelease = { kind: 'create'; payload: ReleaseCreatePayload } |
  { kind: 'activate'; payload: ReleaseActivatePayload; target: CompareTarget }
export const pendingReleaseKey = (userId: string, scopeId: string) =>
  `rainbow-release-review:${JSON.stringify([getBasePath(), userId, scopeId])}`

/** Only uncertain request intents are stored, never configuration content or credentials. */
export function readPendingRelease(key: string, scopeId: string): PendingRelease | null {
  const raw = sessionStorage.getItem(key)
  if (!raw) return null
  const value = JSON.parse(raw) as PendingRelease
  const p = value?.payload
  if (!p || p.expected_scope_id !== scopeId || !p.environment_key || !p.pipeline_key ||
      (p.version !== undefined && typeof p.version !== 'string') || typeof p.reason !== 'string' || typeof p.request_id !== 'string' ||
      !['create', 'activate'].includes(value.kind)) throw new Error('未确认操作记录不完整，请刷新版本列表核对结果')
  const revision = value.kind === 'create' ? value.payload.expected_freeze_revision : value.payload.expected_active_revision
  if (typeof revision !== 'string' || !/^\d+$/.test(revision) ||
      (value.kind === 'activate' && (!p.version || !value.target?.snapshot_id || value.target.version !== p.version))) {
    throw new Error('未确认操作记录不完整，请刷新版本列表核对结果')
  }
  return value
}
export function writePendingRelease(key: string, value: PendingRelease | null) {
  if (value) sessionStorage.setItem(key, JSON.stringify(value))
  else sessionStorage.removeItem(key)
}
