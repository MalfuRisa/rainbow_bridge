import { BaseApiService } from './base-api'
import { apiClient } from './client'
import { toCamelCase, toSnakeCase } from '../utils'
import type { Pipeline } from './types'
import { assertEnvironment, assertScope } from '@/lib/scope'

class PipelineApiService extends BaseApiService<Pipeline> {
  protected baseUrl = '/api/v1/pipeline'

  protected getIdParamName(): string {
    return 'pipeline_key'
  }

  protected getRequestKey(): string {
    return 'pipeline'
  }

  protected getResponseKey(): string {
    return 'pipeline'
  }

  // 重写list方法，因为需要环境key参数
  async list(params?: Record<string, unknown>): Promise<{ total: number; list: Pipeline[] }> {
    return super.list(params)
  }

  // 重写detail方法，因为需要环境key参数
  async detail(id: string | number, params?: Record<string, unknown>): Promise<{ [key: string]: Pipeline | null }> {
    return super.detail(id, params)
  }

  // 重写delete方法，因为需要环境key参数
  async delete(id: string | number, params?: Record<string, unknown>): Promise<void> {
    return super.delete(id, params)
  }

  // 重写create方法，因为需要环境key参数
  async create(data: Pipeline): Promise<{ [key: string]: Pipeline | null }> {
    const snakeCaseData = toSnakeCase(data) as any
    assertEnvironment(snakeCaseData.environment_key, snakeCaseData.environment_id)
    const payload = {
      environment_key: snakeCaseData.environment_key,
      expected_environment_id: snakeCaseData.environment_id,
      pipeline: snakeCaseData
    }
    const resp = await apiClient.post<{ [key: string]: any }>(`${this.baseUrl}/create`, payload)
    const key = this.getResponseKey()
    const createdData = resp.data?.[key]
    const camelCaseData = createdData ? toCamelCase(createdData) as unknown as Pipeline : null
    return { [key]: camelCaseData }
  }

  // 重写update方法，因为需要环境key参数
  async update(data: Pipeline): Promise<{ [key: string]: Pipeline | null }> {
    const snakeCaseData = toSnakeCase(data) as any
    assertScope(snakeCaseData.environment_key, snakeCaseData.pipeline_key, snakeCaseData.scope_id)
    const payload = {
      environment_key: snakeCaseData.environment_key,
      expected_scope_id: snakeCaseData.scope_id,
      pipeline: snakeCaseData
    }
    const resp = await apiClient.post<{ [key: string]: any }>(`${this.baseUrl}/update`, payload)
    const key = this.getResponseKey()
    const updatedData = resp.data?.[key]
    const camelCaseData = updatedData ? toCamelCase(updatedData) as unknown as Pipeline : null
    return { [key]: camelCaseData }
  }
}

export const pipelineApi = new PipelineApiService()

