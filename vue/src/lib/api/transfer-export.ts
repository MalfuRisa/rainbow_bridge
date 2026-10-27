import { apiClient } from './client'
import { ApiError } from './error'
import { assertEnvironment, assertScope } from '@/lib/scope'
import type { ExportSelection } from './transfer'

export type ExportSource = 'editor' | 'active' | 'version'
export type ExportFormat = 'zip' | 'tar.gz'
export type ExportRequest = { format: ExportFormat } & (
  { source: 'editor'; selections: ExportSelection[] } |
  { source: 'active'; environment_key: string; pipeline_key: string; expected_scope_id: string; expected_snapshot_id: string } |
  { source: 'version'; environment_key: string; pipeline_key: string; expected_scope_id: string; expected_snapshot_id: string; version: string }
)
export interface ExportResolvedScope {
  environment_key: string; pipeline_key: string; environment_id: string; scope_id: string
  environment_name?: string; pipeline_name?: string; version?: string; snapshot_id?: string
}
export interface ExportPreview {
  resolved_source: { kind: ExportSource; captured_at: string; version?: string; snapshot_id?: string; scope_id?: string; scopes: ExportResolvedScope[] }
  counts: { configs: number; assets: number; bytes: string }; estimated_bytes: string; warnings: string[]
}
export function assertExportTarget(request: ExportRequest) {
  if (request.source !== 'editor') {
    assertScope(request.environment_key, request.pipeline_key, request.expected_scope_id)
    if (!request.expected_snapshot_id) throw new Error('请先选择并核对发布版本。')
    return
  }
  if (!request.selections.length) throw new Error('请选择编辑区内容。')
  for (const item of request.selections) {
    assertEnvironment(item.environment_key, item.expected_environment_id || '')
    if (item.pipeline_key) assertScope(item.environment_key, item.pipeline_key, item.expected_scope_id || '')
  }
}
export async function previewTransfer(request: ExportRequest) {
  assertExportTarget(request)
  const response = await apiClient.post<ExportPreview>('/api/v1/transfer/export-preview', { ...request }, { showErrorToast: false })
  assertExportTarget(request)
  const preview = response.data
  if (!preview || preview.resolved_source?.kind !== request.source || !preview.counts ||
      typeof preview.counts.bytes !== 'string' || !/^\d+$/.test(preview.counts.bytes) ||
      !Array.isArray(preview.resolved_source.scopes) ||
      (request.source !== 'editor' && (preview.resolved_source.snapshot_id !== request.expected_snapshot_id || preview.resolved_source.scope_id !== request.expected_scope_id)) ||
      (request.source === 'version' && preview.resolved_source.version !== request.version) ||
      (request.source === 'editor' && !preview.resolved_source.scopes.every(scope => request.selections.some(selection =>
        selection.environment_key === scope.environment_key && selection.expected_environment_id === scope.environment_id &&
        (!selection.pipeline_key || (selection.pipeline_key === scope.pipeline_key && selection.expected_scope_id === scope.scope_id)))))) {
    throw new ApiError(502, '导出预览与所选来源不一致，请刷新后重新核对。')
  }
  return preview
}
export async function exportTransfer(request: ExportRequest) {
  assertExportTarget(request)
  const archive = await apiClient.downloadArchive('/api/v1/transfer/export', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(request),
  })
  assertExportTarget(request)
  if ((archive.source && archive.source !== request.source) || (request.source !== 'editor' &&
      ((archive.snapshotId && archive.snapshotId !== request.expected_snapshot_id) ||
       (request.source === 'version' && archive.version && archive.version !== request.version)))) {
    throw new ApiError(502, '下载内容与所选来源不一致，请刷新后重新核对。')
  }
  return archive
}
export function transferBytes(value: string) {
  if (!/^\d+$/.test(value)) return '—'
  const bytes = BigInt(value)
  for (const [unit, size] of [['GiB', 1073741824n], ['MiB', 1048576n], ['KiB', 1024n]] as const) {
    if (bytes >= size) return `${bytes / size}.${(bytes % size) * 10n / size} ${unit}`
  }
  return `${bytes} B`
}
