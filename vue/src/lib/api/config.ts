import { apiClient } from './client'
import { toSnakeCase, toCamelCase } from '../utils'
import { BaseApiService } from './base-api'
import type { ResourceConfig } from './types'
import { assertScope, readScope } from '@/lib/scope'

export interface ListConfigParams {
  environmentKey: string
  pipelineKey: string
  type?: string
  minVersion?: string
  maxVersion?: string
  isLatest?: boolean
  scopeId?: string
  [key: string]: string | boolean | undefined
}

class ConfigApiService extends BaseApiService<ResourceConfig> {
  protected baseUrl = '/api/v1/config'

  protected getIdParamName(): string {
    return 'resource_key'
  }

  async updateWithRevision(data: ResourceConfig, expectedEditRevision: string, expectedScopeId: string) {
    const config = toSnakeCase(data) as Record<string, string>
    assertScope(config.environment_key!, config.pipeline_key!, expectedScopeId)
    const response = await apiClient.post<{ config: ResourceConfig }>(`${this.baseUrl}/update`, {
      config, expected_edit_revision: expectedEditRevision, expected_scope_id: expectedScopeId,
    })
    return toCamelCase(response.data?.config) as unknown as ResourceConfig
  }

  async create(data: ResourceConfig): Promise<{ [key: string]: ResourceConfig | null }> {
    const config = toSnakeCase(data) as Record<string, string>
    assertScope(config.environment_key!, config.pipeline_key!, config.scope_id!)
    const response = await apiClient.post<{ config: ResourceConfig }>(`${this.baseUrl}/create`, { config, expected_scope_id: config.scope_id })
    return { config: response.data?.config ? toCamelCase(response.data.config) as unknown as ResourceConfig : null }
  }

  async list(params: ListConfigParams) {
    return readScope(params.environmentKey, params.pipelineKey, scopeId => super.list({ ...params, scopeId }), params.scopeId)
  }

  // 重写getRequestKey方法，使用正确的请求键名
  protected getRequestKey(): string {
    return 'config'
  }

  // 重写detail方法，因为需要环境和管道key参数
  async detail(id: string | number, params?: Record<string, unknown>): Promise<{ [key: string]: ResourceConfig | null }> {
    return readScope(String(params?.environmentKey || ''), String(params?.pipelineKey || ''), scopeId => super.detail(id, { ...params, scopeId }), params?.scopeId as string | undefined)
  }

  // 重写delete方法，因为需要环境和管道key参数
  async delete(id: string | number, params?: Record<string, unknown>): Promise<void> {
    assertScope(String(params?.environmentKey || ''), String(params?.pipelineKey || ''), String(params?.expectedScopeId || ''))
    return super.delete(id, params)
  }
}

export const configApi = new ConfigApiService()

