import { apiClient } from './client'
import { toCamelCase } from '../utils'
import { readScope } from '@/lib/scope'
import type {
  ApiEnvironmentOverview,
  EnvironmentOverview,
  RuntimeConfigData,
} from './types'

export const runtimeApi = {
  overview: async () => {
    const resp = await apiClient.get<{ total: number; list: ApiEnvironmentOverview[] }>('/api/v1/runtime/overview')
    const list = resp.data?.list || []
    const camelCaseList = list.map(item => toCamelCase(item) as unknown as EnvironmentOverview)
    return {
      total: resp.data?.total || 0,
      list: camelCaseList,
    }
  },

  config: async (
    environmentKey: string,
    pipelineKey: string
  ): Promise<RuntimeConfigData | null> => {
    const resp = await readScope(environmentKey, pipelineKey, scopeId => apiClient.get<RuntimeConfigData>('/api/v1/runtime/config', { scope_id: scopeId }, {
      headers: {
        'x-environment': environmentKey,
        'x-pipeline': pipelineKey,
      },
    }))
    return resp.data || null
  },

  exportStatic: async (
    environmentKey: string,
    pipelineKey: string
  ): Promise<Blob> => {
    return readScope(environmentKey, pipelineKey, scopeId => {
      const query = new URLSearchParams({ environment_key: environmentKey, pipeline_key: pipelineKey, scope_id: scopeId })
      return apiClient.download('/api/v1/runtime/static?' + query.toString())
    })
  },
}
