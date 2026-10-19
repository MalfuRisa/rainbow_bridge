/**
 * 发布版本 API:只读接口沿用现有部署访问控制;创建/切换在单次请求头携带管理令牌。
 */
import { apiClient } from './client'

export interface ReleaseVersionMeta {
  snapshot_id: string
  environment_key: string
  pipeline_key: string
  version: string
  environment_name: string
  pipeline_name: string
  source_freeze_revision: string
  creation_request_id: string
  config_count: number
  asset_count: number
  asset_bytes: number
  payload_sha256: string
  manifest_sha256: string
  external_references: string[]
  reason: string
  actor: string
  created_at: string
}

export interface ReleaseActiveInfo {
  snapshot_id: string
  version: string
  revision: string
  changed_at: string
  changed_by: string
  changed_reason: string
}

export interface ReleaseListItem {
  meta: ReleaseVersionMeta
  is_active: boolean
}

export interface ReleaseListData {
  items: ReleaseListItem[]
  has_more: boolean
  next_before_id: string
  active: ReleaseActiveInfo | null
}

export interface ReleaseDetailConfig {
  resource_key: string
  alias: string
  name: string
  type: string
  content: string
  remark: string
  is_perm: boolean
  description: string
}

export interface ReleaseDetailAsset {
  asset_id: string
  file_name: string
  content_type: string
  size: number
  sha256: string
  url: string
}

export interface ReleaseDetailData {
  meta: ReleaseVersionMeta
  configs: ReleaseDetailConfig[]
  assets: ReleaseDetailAsset[]
  active: ReleaseActiveInfo | null
}

export interface ReleaseEventItem {
  event_id: string
  event_type: string
  request_id: string
  snapshot_id: string
  version: string
  from_active_snapshot_id: string
  to_active_snapshot_id: string
  from_head_revision: string
  to_head_revision: string
  reason: string
  actor: string
  created_at: string
}

export interface ReleaseHistoryData {
  items: ReleaseEventItem[]
  has_more: boolean
  next_before_event_id: string
}

export interface ReleaseResourceIssue {
  resource_key: string
  alias: string
  file_id: string
  reason: string
}

export interface ReleaseCreatePayload {
  environment_key: string
  pipeline_key: string
  version: string
  expected_freeze_revision: string
  request_id: string
  reason: string
}

export interface ReleaseActivatePayload {
  environment_key: string
  pipeline_key: string
  version: string
  expected_active_revision: string
  request_id: string
  reason: string
}

function bearer(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}` }
}

export const releaseApi = {
  async list(environmentKey: string, pipelineKey: string, beforeId = ''): Promise<ReleaseListData> {
    const response = await apiClient.request<ReleaseListData>(
      'GET',
      '/api/v1/release/list',
      { environment_key: environmentKey, pipeline_key: pipelineKey, limit: 20, before_id: beforeId },
      { showErrorToast: false },
    )
    return response.data as ReleaseListData
  },

  async detail(environmentKey: string, pipelineKey: string, version: string): Promise<ReleaseDetailData> {
    const response = await apiClient.request<ReleaseDetailData>(
      'GET',
      '/api/v1/release/detail',
      { environment_key: environmentKey, pipeline_key: pipelineKey, version },
      { showErrorToast: false },
    )
    return response.data as ReleaseDetailData
  },

  async active(environmentKey: string, pipelineKey: string): Promise<ReleaseActiveInfo | null> {
    const response = await apiClient.request<{ active: ReleaseActiveInfo | null }>(
      'GET',
      '/api/v1/release/active',
      { environment_key: environmentKey, pipeline_key: pipelineKey },
      { showErrorToast: false },
    )
    return response.data?.active ?? null
  },

  async history(environmentKey: string, pipelineKey: string, beforeEventId = ''): Promise<ReleaseHistoryData> {
    const response = await apiClient.request<ReleaseHistoryData>(
      'GET',
      '/api/v1/release/history',
      { environment_key: environmentKey, pipeline_key: pipelineKey, limit: 20, before_event_id: beforeEventId },
      { showErrorToast: false },
    )
    return response.data as ReleaseHistoryData
  },

  /** 创建版本(不切换生效);token 只在本次请求头中出现。 */
  async create(payload: ReleaseCreatePayload, token: string): Promise<{ meta: ReleaseVersionMeta; replayed: boolean }> {
    const response = await apiClient.request<{ meta: ReleaseVersionMeta; replayed: boolean }>(
      'POST',
      '/api/v1/release/create',
      { ...payload },
      { headers: bearer(token), showErrorToast: false },
    )
    return response.data as { meta: ReleaseVersionMeta; replayed: boolean }
  },

  /** 切换生效版本(含切回旧版);token 只在本次请求头中出现。 */
  async activate(payload: ReleaseActivatePayload, token: string): Promise<{
    operation: { request_id: string; replayed: boolean; applied_revision: string }
    active: ReleaseActiveInfo | null
    version: string
  }> {
    const response = await apiClient.request<{
      operation: { request_id: string; replayed: boolean; applied_revision: string }
      active: ReleaseActiveInfo | null
      version: string
    }>('POST', '/api/v1/release/activate', { ...payload }, { headers: bearer(token), showErrorToast: false })
    return response.data as {
      operation: { request_id: string; replayed: boolean; applied_revision: string }
      active: ReleaseActiveInfo | null
      version: string
    }
  },
}

/** 版本运行时读取地址(接入说明用)。 */
export function releaseRuntimeURL(basePath: string, version?: string): string {
  const normalized = basePath.replace(/\/$/, '')
  return version
    ? `${normalized}/api/v1/runtime/release?version=${encodeURIComponent(version)}`
    : `${normalized}/api/v1/runtime/release`
}