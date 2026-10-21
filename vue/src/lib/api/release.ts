/**
 * 发布版本 API:管理接口统一复用会话与 CSRF。
 */
import { apiClient, getBasePath } from './client'
import { ApiError, ErrorType } from './error'
import { fetchApi } from './http'

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

export interface ReleaseDeletePayload {
  environment_key: string
  pipeline_key: string
  version: string
  expected_snapshot_id: string
  request_id: string
  reason: string
}
export interface ReleaseDeleteResult {
  request_id: string
  snapshot_id: string
  version: string
  deleted: boolean
  deleted_at: string
  deleted_by: string
  reason: string
  replayed: boolean
}


export const releaseApi = {
  async delete(payload: ReleaseDeletePayload): Promise<ReleaseDeleteResult> {
    const response = await apiClient.request<ReleaseDeleteResult>('POST', '/api/v1/release/delete',
      { ...payload }, { showErrorToast: false })
    const data = response.data
    if (!data || data.deleted !== true || data.request_id !== payload.request_id ||
        data.snapshot_id !== payload.expected_snapshot_id || data.version !== payload.version ||
        typeof data.deleted_at !== 'string' || !data.deleted_at ||
        typeof data.deleted_by !== 'string' || !data.deleted_by ||
        typeof data.reason !== 'string' || typeof data.replayed !== 'boolean') {
      throw new ApiError(502, '删除响应不完整或目标不匹配，请确认结果', '', ErrorType.API)
    }
    return data
  },
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

  /** 创建版本(不切换生效);身份由统一请求层处理。 */
  async create(payload: ReleaseCreatePayload): Promise<{ meta: ReleaseVersionMeta; replayed: boolean }> {
    const response = await apiClient.request<{ meta: ReleaseVersionMeta; replayed: boolean }>(
      'POST',
      '/api/v1/release/create',
      { ...payload },
      { showErrorToast: false },
    )
    return response.data as { meta: ReleaseVersionMeta; replayed: boolean }
  },

  /** 切换生效版本(含切回旧版);身份由统一请求层处理。 */
  async activate(payload: ReleaseActivatePayload): Promise<{
    operation: { request_id: string; replayed: boolean; applied_revision: string }
    active: ReleaseActiveInfo | null
    version: string
  }> {
    const response = await apiClient.request<{
      operation: { request_id: string; replayed: boolean; applied_revision: string }
      active: ReleaseActiveInfo | null
      version: string
    }>('POST', '/api/v1/release/activate', { ...payload }, { showErrorToast: false })
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

// --------------------- 首页(当前生效配置)读取与固定快照导出 ---------------------

/** raw 模式下每项 content 都是字符串(结构化内容为渲染后的 JSON 原文)。 */
export interface RuntimeReleaseRawConfig {
  resource_key: string
  alias: string
  name: string
  type: string
  content: string
  remark: string
  is_perm: boolean
  description: string
}

export interface RuntimeReleaseRawEnvironment {
  environment_key: string
  environment_name: string
  pipeline_key: string
  pipeline_name: string
}

export interface RuntimeReleaseRawData {
  snapshot_id: string
  version: string
  source_freeze_revision: string
  payload_sha256: string
  selection_mode: string
  active_revision: string
  environment: RuntimeReleaseRawEnvironment
  configs: RuntimeReleaseRawConfig[]
}

export interface StaticSnapshotPackage {
  blob: Blob
  fileName: string
  snapshotId: string
  version: string
}

/** 响应不兼容(200 HTML、被忽略的格式参数、范围/模式不符)时抛出的错误。 */
function incompatibleResponse(message: string): ApiError {
  return new ApiError(502, message, '', ErrorType.API)
}

function requireText(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw incompatibleResponse(`生效读取响应缺少 ${field}`)
  }
  return value
}

/** 逐字段校验 raw 读取结果,任何缺失或结构不符都按加载失败处理。 */
function parseRuntimeReleaseRaw(
  body: unknown,
  environmentKey: string,
  pipelineKey: string,
): RuntimeReleaseRawData {
  const envelope = body as { code?: number; data?: unknown } | null
  const data = (envelope?.data ?? null) as Partial<RuntimeReleaseRawData> | null
  if (!data || typeof data !== 'object') {
    throw incompatibleResponse('生效读取响应缺少 data')
  }
  const environment = (data.environment ?? null) as Partial<RuntimeReleaseRawEnvironment> | null
  if (!environment || environment.environment_key !== environmentKey || environment.pipeline_key !== pipelineKey) {
    throw incompatibleResponse('生效读取响应的范围与本次请求不一致')
  }
  if (data.selection_mode !== 'active') {
    throw incompatibleResponse(`生效读取响应的选择模式异常:${String(data.selection_mode ?? '')}`)
  }
  const configs = Array.isArray(data.configs) ? data.configs : null
  if (!configs) {
    throw incompatibleResponse('生效读取响应缺少 configs 数组')
  }
  const normalized = configs.map((item) => {
    const raw = item as Partial<RuntimeReleaseRawConfig>
    if (typeof raw.content !== 'string') {
      throw incompatibleResponse(`配置 ${String(raw.alias ?? '')} 的内容不是字符串原文`)
    }
    return {
      resource_key: requireText(raw.resource_key, 'resource_key'),
      alias: typeof raw.alias === 'string' ? raw.alias : '',
      name: typeof raw.name === 'string' ? raw.name : '',
      type: typeof raw.type === 'string' ? raw.type : '',
      content: raw.content,
      remark: typeof raw.remark === 'string' ? raw.remark : '',
      is_perm: Boolean(raw.is_perm),
      description: typeof raw.description === 'string' ? raw.description : '',
    }
  })
  return {
    snapshot_id: requireText(data.snapshot_id, 'snapshot_id'),
    version: requireText(data.version, 'version'),
    source_freeze_revision: typeof data.source_freeze_revision === 'string' ? data.source_freeze_revision : '',
    payload_sha256: typeof data.payload_sha256 === 'string' ? data.payload_sha256 : '',
    selection_mode: 'active',
    active_revision: typeof data.active_revision === 'string' ? data.active_revision : '',
    environment: {
      environment_key: environment.environment_key as string,
      environment_name: typeof environment.environment_name === 'string' ? environment.environment_name : '',
      pipeline_key: environment.pipeline_key as string,
      pipeline_name: typeof environment.pipeline_name === 'string' ? environment.pipeline_name : '',
    },
    configs: normalized,
  }
}

/** 读取当前生效版本(raw 原文);不做任何降级替代。 */
export async function fetchActiveReleaseRaw(
  environmentKey: string,
  pipelineKey: string,
): Promise<RuntimeReleaseRawData> {
  const response = await apiClient.request<unknown>(
    'GET',
    '/api/v1/runtime/release',
    { content_format: 'raw' },
    {
      headers: {
        'x-environment': environmentKey,
        'x-pipeline': pipelineKey,
      },
      showErrorToast: false,
    },
  )
  return parseRuntimeReleaseRaw(response, environmentKey, pipelineKey)
}

function snapshotExportURL(environmentKey: string, pipelineKey: string, version: string, snapshotId: string): string {
  const basePath = getBasePath().replace(/\/$/, '')
  const url = new URL(`${basePath}/api/v1/runtime/release/static`, window.location.origin)
  url.searchParams.set('version', version)
  url.searchParams.set('expected_snapshot_id', snapshotId)
  return url.toString()
}

/** 解析导出失败响应:先取 JSON 错误,绝不把错误体或 HTML 当成 ZIP。 */
async function exportErrorFrom(response: Response): Promise<ApiError> {
  let payload: Record<string, unknown> | null = null
  const contentType = response.headers.get('content-type') ?? ''
  if (contentType.includes('application/json')) {
    payload = (await response.json().catch(() => null)) as Record<string, unknown> | null
  }
  const message = (payload?.error as string) || (payload?.msg as string) || `导出失败:HTTP ${response.status}`
  const error = new ApiError(typeof payload?.code === 'number' ? (payload.code as number) : response.status, message)
  error.errorType = (payload?.error_type as string) || ''
  error.payload = payload ?? undefined
  return error
}

function fileNameFromDisposition(disposition: string): string {
  const match = /filename="?([^";]+)"?/i.exec(disposition)
  return match ? match[1].trim() : ''
}

/**
 * 固定导出页面刚展示的那份快照:同时校验 HTTP 成功、ZIP 类型与两项版本响应头。
 * 任何一项不符都不创建下载。
 */
export async function exportShownSnapshot(
  environmentKey: string,
  pipelineKey: string,
  version: string,
  snapshotId: string,
): Promise<StaticSnapshotPackage> {
  const response = await fetchApi(snapshotExportURL(environmentKey, pipelineKey, version, snapshotId), {
    method: 'GET',
    headers: {
      'x-environment': environmentKey,
      'x-pipeline': pipelineKey,
    },
  })
  if (!response.ok) {
    throw await exportErrorFrom(response)
  }
  const contentType = response.headers.get('content-type') ?? ''
  if (!contentType.includes('application/zip')) {
    throw incompatibleResponse(`导出响应的类型不是 ZIP:${contentType || '未知'}`)
  }
  const headerSnapshot = response.headers.get('x-rainbow-snapshot-id') ?? ''
  const headerVersion = response.headers.get('x-rainbow-version') ?? ''
  if (headerSnapshot !== snapshotId || headerVersion !== version) {
    throw incompatibleResponse('导出响应的版本标识与页面展示的不一致')
  }
  const blob = await response.blob()
  const fileName =
    fileNameFromDisposition(response.headers.get('content-disposition') ?? '') ||
    `release-${snapshotId}-${version}.zip`
  return { blob, fileName, snapshotId: headerSnapshot, version: headerVersion }
}
