import type { ReleaseActiveInfo } from './release'
import { ApiError } from './error'

export interface ReleaseActiveState {
  active: ReleaseActiveInfo | null
  active_revision: string
}

/** This GET contract is distinct from fixed compare's intentionally null revision. */
export function parseReleaseActiveState(value: unknown, scopeId: string): ReleaseActiveState {
  const state = value as Partial<ReleaseActiveState> | null
  const revision = state?.active_revision
  const active = state?.active
  const revisionValid = typeof revision === 'string' && /^\d+$/.test(revision)
  const activeValid = active === null || (active && typeof active === 'object' && !Array.isArray(active) &&
    active.scope_id === scopeId && typeof active.snapshot_id === 'string' && active.snapshot_id.trim() !== '' &&
    typeof active.version === 'string' && active.version.trim() !== '' && active.revision === revision)
  if (!state || !revisionValid || !activeValid) {
    const error = new ApiError(502, '生效状态响应不完整或不一致，请确认服务版本后重试。')
    error.errorType = 'RELEASE_ACTIVE_STATE_INVALID'
    throw error
  }
  return state as ReleaseActiveState
}
