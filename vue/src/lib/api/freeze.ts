/**
 * 冻结控制 API。管理令牌只在单次请求头中传递,不落任何持久存储。
 */
import { apiClient } from './client'

export interface FreezeStatusData {
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
  environment_key: string
  pipeline_key: string
  reason: string
  expected_revision: string
}

function bearer(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}` }
}

export const freezeApi = {
  /** 读取范围状态(无凭据要求)。 */
  async status(environmentKey: string, pipelineKey: string): Promise<FreezeStatusData> {
    const response = await apiClient.request<FreezeStatusData>(
      'GET',
      '/api/v1/freeze/status',
      { environment_key: environmentKey, pipeline_key: pipelineKey },
      { showErrorToast: false }
    )
    return response.data as FreezeStatusData
  },

  /** 锁定;token 只在本次请求头中出现。 */
  async lock(payload: FreezeTransitionPayload, token: string): Promise<FreezeStatusData> {
    const response = await apiClient.request<FreezeStatusData>(
      'POST',
      '/api/v1/freeze/lock',
      { ...payload },
      { headers: bearer(token), showErrorToast: false }
    )
    return response.data as FreezeStatusData
  },

  /** 解除冻结;token 只在本次请求头中出现。 */
  async unlock(payload: FreezeTransitionPayload, token: string): Promise<FreezeStatusData> {
    const response = await apiClient.request<FreezeStatusData>(
      'POST',
      '/api/v1/freeze/unlock',
      { ...payload },
      { headers: bearer(token), showErrorToast: false }
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
    const response = await apiClient.request<FreezeHistoryData>(
      'GET',
      '/api/v1/freeze/history',
      {
        environment_key: environmentKey,
        pipeline_key: pipelineKey,
        limit,
        before_event_id: beforeEventId,
      },
      { showErrorToast: false }
    )
    return response.data as FreezeHistoryData
  },
}