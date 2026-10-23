import { apiClient } from './client'
import { toSnakeCase, toCamelCase } from '../utils'
import { BaseApiService } from './base-api'
import type { ResourceConfig } from './types'

export interface ListConfigParams {
  environmentKey: string
  pipelineKey: string
  type?: string
  minVersion?: string
  maxVersion?: string
  isLatest?: boolean
  [key: string]: string | boolean | undefined
}

class ConfigApiService extends BaseApiService<ResourceConfig> {
  protected baseUrl = '/api/v1/config'

  protected getIdParamName(): string {
    return 'resource_key'
  }

  async updateWithRevision(data: ResourceConfig, expectedEditRevision: string) {
    const response = await apiClient.post<{ config: ResourceConfig }>(`${this.baseUrl}/update`, {
      config: toSnakeCase(data), expected_edit_revision: expectedEditRevision,
    })
    return toCamelCase(response.data?.config) as unknown as ResourceConfig
  }

  // 重写getRequestKey方法，使用正确的请求键名
  protected getRequestKey(): string {
    return 'config'
  }

  // 重写detail方法，因为需要环境和管道key参数
  async detail(id: string | number, params?: Record<string, unknown>): Promise<{ [key: string]: ResourceConfig | null }> {
    return super.detail(id, params)
  }

  // 重写delete方法，因为需要环境和管道key参数
  async delete(id: string | number, params?: Record<string, unknown>): Promise<void> {
    return super.delete(id, params)
  }
}

export const configApi = new ConfigApiService()

