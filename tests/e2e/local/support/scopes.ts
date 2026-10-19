import { Target, url } from './env'
import {
  MultipartFile,
  RawResponse,
  expectBusinessOk,
  getJson,
  postJson,
  postMultipart,
  request,
  sha256Hex,
} from './net'

/** 三组哨兵范围(T04/T05 等有副作用用例前后对比用)。 */
export interface Scope {
  env: string
  pipeline: string
  label: string
  sentinel: string
}

export const scopeDevMain: Scope = { env: 'qa_dev', pipeline: 'main', label: 'qa_dev/main', sentinel: 'KEEP_DEV_MAIN' }
export const scopeDevBeta: Scope = { env: 'qa_dev', pipeline: 'beta', label: 'qa_dev/beta', sentinel: 'KEEP_DEV_BETA' }
export const scopeProdMain: Scope = { env: 'qa_prod', pipeline: 'main', label: 'qa_prod/main', sentinel: 'KEEP_PROD_MAIN' }
export const allScopes: Scope[] = [scopeDevMain, scopeDevBeta, scopeProdMain]

/** 最小合法 PNG(1x1 透明)。 */
export const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
)
export const TINY_TXT = Buffer.from('rainbow-bridge qa text asset\n', 'utf8')

export interface AssetUpload {
  fileId: string
  reference: string
  fileName: string
  sha256: string
  raw: RawResponse
}

export interface ResourceConfig {
  resource_key: string
  alias: string
  name: string
  environment_key: string
  pipeline_key: string
  content: string
  type: string
  remark: string
  is_perm: boolean
  description: string
}

/** 列表/详情里的资源路径可能已带部署前缀；这里统一解析成绝对 URL。 */
export function resolveUrl(target: Target, value: string): string {
  if (/^https?:\/\//.test(value)) return value
  const prefix = target.prefix.endsWith('/') ? target.prefix.slice(0, -1) : target.prefix
  if (prefix && value.startsWith(prefix + '/')) {
    return `${target.origin}${value}`
  }
  return url(target, value)
}

async function ensureEnvironment(target: Target, scope: Scope): Promise<void> {
  await postJson(target, '/api/v1/environment/create', {
    environment: { environment_key: scope.env, environment_name: `QA ${scope.env}`, is_active: true },
  })
  const list = await getJson(target, '/api/v1/environment/list')
  const names = (list.json?.data?.list ?? []).map((item: any) => item.environment_key)
  if (!names.includes(scope.env)) {
    throw new Error(`环境 ${scope.env} 创建后仍不存在: ${list.text.slice(0, 300)}`)
  }
}

async function ensurePipeline(target: Target, scope: Scope): Promise<void> {
  await postJson(target, '/api/v1/pipeline/create', {
    environment_key: scope.env,
    pipeline: { pipeline_key: scope.pipeline, pipeline_name: `QA ${scope.pipeline}`, is_active: true },
  })
  const list = await getJson(target, `/api/v1/pipeline/list?environment_key=${encodeURIComponent(scope.env)}`)
  const keys = (list.json?.data?.list ?? []).map((item: any) => item.pipeline_key)
  if (!keys.includes(scope.pipeline)) {
    throw new Error(`渠道 ${scope.label} 创建后仍不存在: ${list.text.slice(0, 300)}`)
  }
}

export async function ensureScope(target: Target, scope: Scope): Promise<void> {
  await ensureEnvironment(target, scope)
  await ensurePipeline(target, scope)
}

export async function createConfig(
  target: Target,
  scope: Scope,
  input: {
    alias: string
    name: string
    type: string
    content: string
    remark?: string
    description?: string
    is_perm?: boolean
  },
  expectOk = true,
): Promise<{ response: RawResponse; config: ResourceConfig | null }> {
  const response = await postJson(target, '/api/v1/config/create', {
    config: {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      alias: input.alias,
      name: input.name,
      type: input.type,
      content: input.content,
      remark: input.remark ?? '',
      description: input.description ?? '',
      is_perm: input.is_perm ?? false,
    },
  })
  if (expectOk) {
    expectBusinessOk(response, `创建配置 ${scope.label}/${input.alias}`)
    return { response, config: response.json?.data?.config ?? null }
  }
  return { response, config: response.json?.data?.config ?? null }
}

export async function updateConfig(
  target: Target,
  scope: Scope,
  config: Record<string, unknown>,
  expectOk = true,
): Promise<RawResponse> {
  const response = await postJson(target, '/api/v1/config/update', {
    config: {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      ...config,
    },
  })
  if (expectOk) expectBusinessOk(response, `更新配置 ${scope.label}/${String(config.resource_key)}`)
  return response
}

export async function deleteConfig(
  target: Target,
  scope: Scope,
  resourceKey: string,
  expectOk = true,
): Promise<RawResponse> {
  const response = await postJson(target, '/api/v1/config/delete', {
    environment_key: scope.env,
    pipeline_key: scope.pipeline,
    resource_key: resourceKey,
  })
  if (expectOk) expectBusinessOk(response, `删除配置 ${scope.label}/${resourceKey}`)
  return response
}

export async function listConfigs(target: Target, scope: Scope, type = ''): Promise<ResourceConfig[]> {
  const query = `environment_key=${encodeURIComponent(scope.env)}&pipeline_key=${encodeURIComponent(scope.pipeline)}`
  const response = await getJson(target, `/api/v1/config/list?${query}${type ? `&type=${type}` : ''}`)
  expectBusinessOk(response, `列表 ${scope.label}`)
  return response.json?.data?.list ?? []
}

export async function getConfigDetail(
  target: Target,
  scope: Scope,
  resourceKey: string,
): Promise<RawResponse> {
  const query = `environment_key=${encodeURIComponent(scope.env)}&pipeline_key=${encodeURIComponent(scope.pipeline)}&resource_key=${encodeURIComponent(resourceKey)}`
  return getJson(target, `/api/v1/config/detail?${query}`)
}

export async function uploadAsset(
  target: Target,
  scope: Scope,
  file: { fileName: string; contentType: string; bytes: Buffer; remark?: string },
): Promise<AssetUpload> {
  const multipart: MultipartFile = {
    field: 'file',
    filename: file.fileName,
    contentType: file.contentType,
    bytes: file.bytes,
  }
  const response = await postMultipart(
    target,
    '/api/v1/asset/upload',
    {
      environment_key: scope.env,
      pipeline_key: scope.pipeline,
      remark: file.remark ?? '',
    },
    [multipart],
  )
  expectBusinessOk(response, `上传资源 ${file.fileName}`)
  const asset = response.json?.data?.asset
  if (!asset?.file_id) {
    throw new Error(`上传资源响应缺少 file_id: ${response.text.slice(0, 300)}`)
  }
  return {
    fileId: asset.file_id,
    reference: response.json?.data?.reference ?? `asset://${asset.file_id}`,
    fileName: asset.file_name,
    sha256: sha256Hex(file.bytes),
    raw: response,
  }
}

export async function listAssets(target: Target, scope: Scope): Promise<Record<string, any>[]> {
  const query = `environment_key=${encodeURIComponent(scope.env)}&pipeline_key=${encodeURIComponent(scope.pipeline)}`
  const response = await getJson(target, `/api/v1/asset/list?${query}`)
  expectBusinessOk(response, `资源列表 ${scope.label}`)
  return response.json?.data?.list ?? []
}

export interface ScopeSnapshot {
  configs: Record<string, unknown>[]
  assets: Record<string, unknown>[]
  assetSha: Record<string, string>
}

export async function snapshotScope(target: Target, scope: Scope): Promise<ScopeSnapshot> {
  const configs = (await listConfigs(target, scope))
    .map((item) => ({
      alias: item.alias,
      resource_key: item.resource_key,
      name: item.name,
      type: item.type,
      content: item.content,
      remark: item.remark,
      is_perm: item.is_perm,
      description: item.description,
    }))
    .sort((a, b) => String(a.alias).localeCompare(String(b.alias)))

  const assetRows = await listAssets(target, scope)
  const assets = assetRows
    .map((asset) => ({
      file_id: asset.file_id,
      file_name: asset.file_name,
      file_size: asset.file_size,
      content_type: asset.content_type,
      remark: asset.remark,
      url: asset.url,
    }))
    .sort((a, b) => String(a.file_id).localeCompare(String(b.file_id)))

  const assetSha: Record<string, string> = {}
  for (const asset of assets) {
    const download = await request(target, resolveUrl(target, String(asset.url)), { method: 'GET' })
    if (download.status === 200) {
      assetSha[String(asset.file_id)] = sha256Hex(download.bytes)
    } else {
      assetSha[String(asset.file_id)] = `HTTP_${download.status}`
    }
  }
  return { configs, assets, assetSha }
}

export interface AllScopesSnapshot {
  at: string
  scopes: Record<string, ScopeSnapshot>
}

export async function snapshotAllScopes(target: Target): Promise<AllScopesSnapshot> {
  const scopes: Record<string, ScopeSnapshot> = {}
  for (const scope of allScopes) {
    scopes[scope.label] = await snapshotScope(target, scope)
  }
  return { at: new Date().toISOString(), scopes }
}

/** 返回差异描述；空数组表示完全一致(逐字节比较，不只比数量)。 */
export function diffSnapshots(before: AllScopesSnapshot, after: AllScopesSnapshot): string[] {
  const differences: string[] = []
  for (const scope of allScopes) {
    const beforeScope = before.scopes[scope.label]
    const afterScope = after.scopes[scope.label]
    if (JSON.stringify(beforeScope.configs) !== JSON.stringify(afterScope.configs)) {
      differences.push(`${scope.label} 配置集合变化`)
    }
    if (JSON.stringify(beforeScope.assets) !== JSON.stringify(afterScope.assets)) {
      differences.push(`${scope.label} 资源集合变化`)
    }
    if (JSON.stringify(beforeScope.assetSha) !== JSON.stringify(afterScope.assetSha)) {
      differences.push(`${scope.label} 资源字节变化`)
    }
  }
  return differences
}

/** 建立三组哨兵范围(重复执行时保留既有哨兵，返回 existed 标记)。 */
export async function setupSentinelScopes(
  target: Target,
  marker: string,
): Promise<{ sentinels: Record<string, string>; existed: string[] }> {
  const sentinels: Record<string, string> = {}
  const existed: string[] = []
  for (const scope of allScopes) {
    await ensureScope(target, scope)
    const current = await listConfigs(target, scope)
    const found = current.find((item) => item.alias === scope.sentinel)
    if (found) {
      sentinels[scope.label] = found.resource_key
      existed.push(scope.label)
      continue
    }
    const { config } = await createConfig(target, scope, {
      alias: scope.sentinel,
      name: `哨兵 ${scope.label}`,
      type: 'text',
      content: `sentinel:${marker}:${scope.label}`,
      description: 'qa sentinel',
    })
    sentinels[scope.label] = config?.resource_key ?? ''
  }
  return { sentinels, existed }
}