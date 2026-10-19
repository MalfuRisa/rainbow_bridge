import { Target } from './env'
import { MultipartFile, RawResponse, expectBusinessOk, getJson, postJson, postMultipart, request } from './net'
import { Scope } from './scopes'

/** /transfer/* 系列端点封装：字段名严格按后端实现(import 用 archive，预览/选择导入用 file)。 */

export interface TransferFileInput {
  bytes: Buffer
  filename: string
  contentType?: string
}

function toMultipart(file: TransferFileInput, field: string): MultipartFile {
  return {
    field,
    filename: file.filename,
    contentType: file.contentType ?? 'application/zip',
    bytes: file.bytes,
  }
}

export async function importWithArchiveField(
  target: Target,
  file: TransferFileInput,
  options: { env?: string; pipeline?: string; overwrite?: boolean; field?: string } = {},
): Promise<RawResponse> {
  return postMultipart(
    target,
    '/api/v1/transfer/import',
    {
      environment_key: options.env ?? '',
      pipeline_key: options.pipeline ?? '',
      overwrite: options.overwrite ? 'true' : 'false',
    },
    [toMultipart(file, options.field ?? 'archive')],
  )
}

export async function importPreview(target: Target, file: TransferFileInput): Promise<RawResponse> {
  return postMultipart(target, '/api/v1/transfer/import-preview', {}, [toMultipart(file, 'file')])
}

export async function importSelective(
  target: Target,
  file: TransferFileInput,
  options: {
    selections?: { environment_key: string; pipeline_key?: string; resource_keys?: string[] }[]
    overwrite?: boolean
  } = {},
): Promise<RawResponse> {
  return postMultipart(
    target,
    '/api/v1/transfer/import-selective',
    {
      selections: JSON.stringify(options.selections ?? []),
      overwrite: options.overwrite ? 'true' : 'false',
    },
    [toMultipart(file, 'file')],
  )
}

export async function exportSelective(
  target: Target,
  selections: { environment_key: string; pipeline_key?: string; resource_keys?: string[] }[],
  format: 'zip' | 'tar.gz' = 'zip',
): Promise<RawResponse> {
  return postJson(target, '/api/v1/transfer/export', { format, selections })
}

export async function getExportTree(target: Target): Promise<RawResponse> {
  return getJson(target, '/api/v1/transfer/export-tree')
}

export async function migrate(
  target: Target,
  input: {
    source_environment_key: string
    source_pipeline_key: string
    target_environment_key: string
    target_pipeline_key: string
  },
): Promise<RawResponse> {
  return postJson(target, '/api/v1/transfer/migrate', input)
}

/** 运行时接口：x-environment / x-pipeline 头，业务状态在响应体内。 */
export async function runtimeConfig(target: Target, scope: Scope): Promise<RawResponse> {
  return getJson(target, '/api/v1/runtime/config', {
    'x-environment': scope.env,
    'x-pipeline': scope.pipeline,
  })
}

export async function runtimeStatic(target: Target, scope: Scope): Promise<RawResponse> {
  const query = `environment_key=${encodeURIComponent(scope.env)}&pipeline_key=${encodeURIComponent(scope.pipeline)}`
  return getJson(target, `/api/v1/runtime/static?${query}`)
}

export async function runtimeOverview(target: Target): Promise<RawResponse> {
  return getJson(target, '/api/v1/runtime/overview')
}

export interface ImportSummaryData {
  total: number
  environment_keys: string[]
  pipeline_keys: string[]
  items: { resource_key: string; alias: string; name: string; type: string; environment_key: string; pipeline_key: string }[]
}

export function importSummary(response: RawResponse): ImportSummaryData | null {
  const data = response.json?.data
  if (!data || typeof data.total !== 'number') return null
  return data as ImportSummaryData
}

export interface PreviewSummary {
  total_environments: number
  total_pipelines: number
  total_configs: number
  total_assets: number
  new_count: number
  existing_count: number
  conflict_count: number
}

export function previewSummary(response: RawResponse): PreviewSummary | null {
  return (response.json?.data?.summary as PreviewSummary) ?? null
}

export async function expectBusinessFailure(response: RawResponse, context: string): Promise<string> {
  const code = response.json?.code
  if (code === 200) {
    throw new Error(`${context}: 预期业务失败，实际成功: ${response.text.slice(0, 300)}`)
  }
  return String(response.json?.error ?? response.text.slice(0, 200))
}

export { expectBusinessOk, request }