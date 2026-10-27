import { apiClient } from './client'
import { toSnakeCase } from '../utils'
import { assertEnvironment, assertScope, type ScopeTarget } from '@/lib/scope'
import { exportTransfer } from './transfer-export'
import type {
  ApiResourceConfig,
  ImportSummary,
} from './types'

export interface ImportJsonData {
  expected_environment_id?: string
  expected_scope_id?: string
  configs: ApiResourceConfig[]
  overwrite?: boolean
  environment_mapping?: Record<string, string>
  pipeline_mapping?: Record<string, string>
}

export interface MigrateParams {
  sourceExpectedEnvironmentId: string
  sourceExpectedScopeId: string
  targetExpectedEnvironmentId: string
  targetExpectedScopeId: string
  sourceEnvironmentKey: string
  sourcePipelineKey: string
  targetEnvironmentKey: string
  targetPipelineKey: string
  resourceKeys?: string[]
  overwrite?: boolean
  [key: string]: string | string[] | boolean | undefined
}

export interface MigrateResultItem {
  resource_key: string
  name: string
  alias: string
  status: 'succeeded' | 'skipped' | 'failed'
  message?: string
}

export interface MigrateSummary {
  total: number
  succeeded: number
  skipped: number
  failed?: number
  items: MigrateResultItem[]
}

export interface MigrateResponse {
  code: number
  msg: string
  error?: string
  data?: MigrateSummary
}

export interface ExportTreeConfig {
  resource_key: string
  name: string
  alias: string
  type: string
}

export interface ExportTreePipeline {
  scope_id: string
  environment_id: string
  pipeline_key: string
  pipeline_name: string
  description: string
  is_active: boolean
  config_count: number
  configs: ExportTreeConfig[]
}

export interface ExportTreeEnvironment {
  environment_id: string
  environment_key: string
  environment_name: string
  description: string
  is_active: boolean
  pipelines: ExportTreePipeline[]
}

export interface ExportTreeData {
  environments: ExportTreeEnvironment[]
}

export interface ExportTreeResponse {
  code: number
  msg: string
  error?: string
  data?: ExportTreeData
}

export interface ExportSelection {
  expected_environment_id?: string
  expected_scope_id?: string
  environment_key: string
  pipeline_key?: string
  resource_keys?: string[]
}

export interface ExportSelectiveParams {
  source: 'editor'
  format: 'zip' | 'tar.gz'
  selections: ExportSelection[]
}

export interface ImportPreviewConfig {
  resource_key: string
  name: string
  alias: string
  type: string
  status: 'new' | 'exists' | 'conflict'
}

export interface ImportPreviewPipeline {
  pipeline_key: string
  pipeline_name: string
  status: 'new' | 'exists'
  configs: ImportPreviewConfig[]
}

export interface ImportPreviewEnvironment {
  environment_key: string
  environment_name: string
  status: 'new' | 'exists'
  pipelines: ImportPreviewPipeline[]
}

export interface ImportPreviewSummary {
  total_environments: number
  total_pipelines: number
  total_configs: number
  total_assets: number
  new_count: number
  existing_count: number
  conflict_count: number
}

export interface ImportPreviewData {
  format: string
  environments: ImportPreviewEnvironment[]
  summary: ImportPreviewSummary
}

export interface ImportPreviewResponse {
  code: number
  msg: string
  error?: string
  data?: ImportPreviewData
}

export const transferApi = {
  importJson: async (data: ImportJsonData): Promise<ImportSummary | null> => {
    const resp = await apiClient.post<ImportSummary>('/api/v1/transfer/import', data as unknown as Record<string, unknown>)
    return resp.data || null
  },

  importFile: async (file: File, target?: ScopeTarget, overwrite = false): Promise<ImportSummary | null> => {
    const formData = new FormData()
    formData.append('archive', file)
    if (target) {
      assertEnvironment(target.environmentKey, target.environmentId)
      assertScope(target.environmentKey, target.pipelineKey, target.scopeId)
      formData.append('environment_key', target.environmentKey)
      formData.append('pipeline_key', target.pipelineKey)
      formData.append('expected_environment_id', target.environmentId)
      formData.append('expected_scope_id', target.scopeId)
    }
    formData.append('overwrite', String(overwrite))

    const resp = await apiClient.upload<ImportSummary>('/api/v1/transfer/import', formData)
    return resp.data || null
  },

  migrate: async (params: MigrateParams): Promise<MigrateSummary | null> => {
    assertEnvironment(params.sourceEnvironmentKey, params.sourceExpectedEnvironmentId)
    assertEnvironment(params.targetEnvironmentKey, params.targetExpectedEnvironmentId)
    assertScope(params.sourceEnvironmentKey, params.sourcePipelineKey, params.sourceExpectedScopeId)
    assertScope(params.targetEnvironmentKey, params.targetPipelineKey, params.targetExpectedScopeId)
    const snakeParams = toSnakeCase(params) as Record<string, unknown>
    const resp = await apiClient.post<MigrateSummary>('/api/v1/transfer/migrate', snakeParams)
    return resp.data || null
  },

  getExportTree: async (): Promise<ExportTreeEnvironment[]> => {
    const resp = await apiClient.get<ExportTreeData>('/api/v1/transfer/export-tree')
    return resp.data?.environments || []
  },

  exportSelective: async (params: ExportSelectiveParams): Promise<Blob> => {
    return (await exportTransfer(params)).blob
  },

  importPreview: async (file: File): Promise<ImportPreviewData | null> => {
    const formData = new FormData()
    formData.append('file', file)

    const resp = await apiClient.upload<ImportPreviewData>('/api/v1/transfer/import-preview', formData)
    return resp.data || null
  },

  importSelective: async (
    file: File,
    selections: ExportSelection[],
    overwrite: boolean
  ): Promise<ImportSummary | null> => {
    if (!selections.length) throw new Error('请选择要导入的配置。')
    for (const selection of selections) {
      if (selection.expected_environment_id) assertEnvironment(selection.environment_key, selection.expected_environment_id)
      if (selection.pipeline_key && selection.expected_scope_id) assertScope(selection.environment_key, selection.pipeline_key, selection.expected_scope_id)
    }
    const formData = new FormData()
    formData.append('file', file)
    formData.append('selections', JSON.stringify(selections))
    formData.append('overwrite', String(overwrite))

    const resp = await apiClient.upload<ImportSummary>('/api/v1/transfer/import-selective', formData)
    return resp.data || null
  },
}
