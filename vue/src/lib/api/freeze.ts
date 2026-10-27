/**
 * 冻结控制 API。统一通过会话与 CSRF 完成管理认证。
 */
import { apiClient } from './client'
import { readScope, assertScope } from '@/lib/scope'

export interface FreezeStatusData {
  scope_id: string
  environment_key: string
  pipeline_key: string
  state: 'unlocked' | 'locked'
  revision: string
  last_changed_at: string
  last_changed_by: string
  last_reason: string
  admin_configured: boolean
}

export interface FreezeBlockedScope {
  environment_key: string
  pipeline_key: string
  state: string
  revision: string
}

export interface FreezeResourceIssue {
  resource_key: string
  alias: string
  file_id: string
  reason: string
}

export interface FreezeEventItem {
  event_id: string
  event_type: string
  from_state: string
  to_state: string
  revision: string
  reason: string
  actor: string
  created_at: string
}

export interface FreezeHistoryData {
  items: FreezeEventItem[]
  has_more: boolean
  next_before_event_id: string
}

export interface FreezeTransitionPayload {
  expected_scope_id: string
  environment_key: string
  pipeline_key: string
  reason: string
  expected_revision: string
}


export const freezeApi = {
  /** 读取范围状态(管理会话)。 */
  async status(environmentKey: string, pipelineKey: string): Promise<FreezeStatusData> {
    const response = await readScope(environmentKey, pipelineKey, scopeId => apiClient.request<FreezeStatusData>(
      'GET',
      '/api/v1/freeze/status',
      { environment_key: environmentKey, pipeline_key: pipelineKey, scope_id: scopeId },
      { showErrorToast: false }
    ))
    return response.data as FreezeStatusData
  },

  /** 锁定;身份由统一请求层处理。 */
  async lock(payload: FreezeTransitionPayload): Promise<FreezeStatusData> {
    assertScope(payload.environment_key, payload.pipeline_key, payload.expected_scope_id)
    const response = await apiClient.request<FreezeStatusData>(
      'POST',
      '/api/v1/freeze/lock',
      { ...payload },
      { showErrorToast: false }
    )
    return response.data as FreezeStatusData
  },

  /** 解除冻结;身份由统一请求层处理。 */
  async unlock(payload: FreezeTransitionPayload): Promise<FreezeStatusData> {
    assertScope(payload.environment_key, payload.pipeline_key, payload.expected_scope_id)
    const response = await apiClient.request<FreezeStatusData>(
      'POST',
      '/api/v1/freeze/unlock',
      { ...payload },
      { showErrorToast: false }
    )
    return response.data as FreezeStatusData
  },

  /** 历史查询(追加式审计,按事件 ID 倒序)。 */
  async history(
    environmentKey: string,
    pipelineKey: string,
    limit = 20,
    beforeEventId = ''
  ): Promise<FreezeHistoryData> {
    const response = await readScope(environmentKey, pipelineKey, scopeId => apiClient.request<FreezeHistoryData>(
      'GET',
      '/api/v1/freeze/history',
      {
        environment_key: environmentKey,
        pipeline_key: pipelineKey,
        scope_id: scopeId,
        limit,
        before_event_id: beforeEventId,
      },
      { showErrorToast: false }
    ))
    return response.data as FreezeHistoryData
  },
}
