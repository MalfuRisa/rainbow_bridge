import { apiClient } from './client'
import { assertScope } from '@/lib/scope'
import type { ReleaseDetailConfig } from './release'

export interface CompareTarget { version: string; snapshot_id: string }
interface CompareScopePayload {
  environment_key: string
  pipeline_key: string
  expected_scope_id: string
  version: string
  expected_snapshot_id: string
  offset: number
  limit: number
}
export interface ActiveComparePayload extends CompareScopePayload {
  comparison_mode: 'active'
  expected_active_revision: string
  expected_active_snapshot_id: string
}
export interface FixedComparePayload extends CompareScopePayload {
  comparison_mode: 'fixed'
  baseline_version: string
  expected_baseline_snapshot_id: string
}
export type ReleaseComparePayload = ActiveComparePayload | FixedComparePayload
export interface ReleaseChange {
  resource_key: string
  change_type: 'added' | 'modified' | 'removed'
  changed_fields: string[]
  before: Pick<ReleaseDetailConfig, 'alias' | 'name' | 'type'> | null
  after: Pick<ReleaseDetailConfig, 'alias' | 'name' | 'type'> | null
}
interface ComparisonData {
  scope_id: string
  target: CompareTarget
  baseline: CompareTarget | null
  has_changes: boolean
  summary: { added: number; modified: number; removed: number; unchanged: number }
  items: ReleaseChange[]
  total_changes: number
  offset: number
  limit: number
  has_more: boolean
  next_offset: number | null
}
export type ReleaseComparison = ComparisonData & (
  { comparison_mode: 'active'; active_revision: string; target_is_active: boolean } |
  { comparison_mode: 'fixed'; active_revision: null; target_is_active: null }
)

export async function compareRelease(payload: ReleaseComparePayload): Promise<ReleaseComparison> {
  assertScope(payload.environment_key, payload.pipeline_key, payload.expected_scope_id)
  const response = await apiClient.request<ReleaseComparison>('POST', '/api/v1/release/compare',
    { ...payload }, { showErrorToast: false })
  assertScope(payload.environment_key, payload.pipeline_key, payload.expected_scope_id)
  const data = response.data
  const modeMatches = data && data.comparison_mode === payload.comparison_mode && (payload.comparison_mode === 'active'
    ? data.active_revision === payload.expected_active_revision && typeof data.target_is_active === 'boolean' &&
      (data.baseline?.snapshot_id ?? '') === payload.expected_active_snapshot_id
    : data.active_revision === null && data.target_is_active === null &&
      data.baseline?.version === payload.baseline_version && data.baseline.snapshot_id === payload.expected_baseline_snapshot_id)
  if (!data || data.scope_id !== payload.expected_scope_id || data.target?.version !== payload.version ||
      data.target.snapshot_id !== payload.expected_snapshot_id || !modeMatches ||
      !Array.isArray(data.items) || !data.summary || data.offset !== payload.offset ||
      typeof data.has_changes !== 'boolean' ||
      !Number.isSafeInteger(data.total_changes) || data.total_changes < 0 ||
      (['added', 'modified', 'removed', 'unchanged'] as const).some(key => !Number.isSafeInteger(data.summary[key]) || data.summary[key] < 0) ||
      data.summary.added + data.summary.modified + data.summary.removed !== data.total_changes ||
      data.has_changes !== (data.total_changes > 0) ||
      data.items.some(item => !item.resource_key || !['added', 'modified', 'removed'].includes(item.change_type) || !Array.isArray(item.changed_fields)) ||
      (data.has_more && (!Number.isSafeInteger(data.next_offset) || data.next_offset! <= payload.offset))) {
    throw new Error('比较结果不完整或目标已变化，请重新比较')
  }
  return data
}
