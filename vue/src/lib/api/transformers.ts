import type { Environment, Pipeline, ConfigItem } from '../types'
import type {
  ApiEnvironment,
  ApiPipeline,
  ApiResourceConfig,
  ApiEnvironmentOverview,
  ApiFileAsset,
  FileAsset,
  ResourceConfig,
  EnvironmentOverview,
} from './types'

export function fromApiEnvironment(api: ApiEnvironment, pipelines: Pipeline[] = []): Environment {
  return {
    id: api.environment_id,
    environmentId: api.environment_id,
    key: api.environment_key,
    name: api.environment_name,
    pipelines,
  }
}

export function toApiEnvironment(env: Partial<Environment> & { key: string; name: string }): ApiEnvironment {
  return {
    environment_id: env.environmentId || '',
    environment_key: env.key,
    environment_name: env.name,
    description: '',
    sort_order: 0,
    is_active: true,
  }
}

export function fromApiPipeline(api: ApiPipeline): Pipeline {
  return {
    id: api.scope_id,
    scopeId: api.scope_id,
    environmentId: api.environment_id,
    key: api.pipeline_key,
    name: api.pipeline_name,
  }
}

export function toApiPipeline(
  pipeline: Partial<Pipeline> & { key: string; name: string },
  environmentKey: string
): ApiPipeline {
  return {
    scope_id: pipeline.scopeId || '',
    environment_id: pipeline.environmentId || '',
    environment_key: environmentKey,
    pipeline_key: pipeline.key,
    pipeline_name: pipeline.name,
    description: '',
    sort_order: 0,
    is_active: true,
  }
}

function mapFrontendTypeToBackend(frontendType: string): string {
  return frontendType
}

function transformContentToBackend(_frontendType: string, content: string): string {
  return content
}

function transformContentFromBackend(backendType: string, content: string | object): { type: string; content: string } {
  const contentStr = typeof content === 'object' ? JSON.stringify(content, null, 2) : content
  return { type: backendType, content: contentStr }
}

export function fromApiConfig(api: ApiResourceConfig | ResourceConfig): ConfigItem {
  if ('resource_key' in api) {
    const { type, content } = transformContentFromBackend(api.type, api.content)
    
    return {
      editRevision: api.edit_revision,
      scopeId: api.scope_id,
      id: api.resource_key,
      name: api.name,
      alias: api.alias,
      type: type as ConfigItem['type'],
      content,
      environmentId: api.environment_key,
      pipelineId: api.pipeline_key,
      description: api.description || '',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    }
  } else {
    const { type, content } = transformContentFromBackend(api.type, api.content)
    
    return {
      editRevision: api.editRevision,
      scopeId: api.scopeId,
      id: api.resourceKey,
      name: api.name,
      alias: api.alias,
      type: type as ConfigItem['type'],
      content,
      environmentId: api.environmentKey,
      pipelineId: api.pipelineKey,
      description: api.description || '',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    }
  }
}

export function toApiConfig(
  config: Partial<ConfigItem> & {
    name: string
    type: string
    content: string
    environmentId: string
    pipelineId: string
  }
): ApiResourceConfig {
  const backendType = mapFrontendTypeToBackend(config.type)
  const backendContent = transformContentToBackend(config.type, config.content)
  
  return {
    resource_key: config.id || '',
    scope_id: config.scopeId || '',
    alias: config.alias || '',
    name: config.name,
    environment_key: config.environmentId,
    pipeline_key: config.pipelineId,
    content: backendContent,
    type: backendType,
    remark: '',
    is_perm: false,
    description: config.description || '',
  }
}

export function fromApiEnvironmentOverview(api: ApiEnvironmentOverview | EnvironmentOverview): Environment {
  if ('environment_key' in api) {
    return {
      id: api.environment_id,
      environmentId: api.environment_id,
      key: api.environment_key,
      name: api.environment_name,
      pipelines: (api.pipelines || []).map((p) => ({
        id: p.scope_id,
        scopeId: p.scope_id,
        environmentId: p.environment_id || api.environment_id,
        key: p.pipeline_key,
        name: p.pipeline_name,
      })),
    }
  } else {
    return {
      id: api.environmentId,
      environmentId: api.environmentId,
      key: api.environmentKey,
      name: api.environmentName,
      pipelines: (api.pipelines || []).map((p) => ({
        id: p.scopeId,
        scopeId: p.scopeId,
        environmentId: p.environmentId || api.environmentId,
        key: p.pipelineKey,
        name: p.pipelineName,
      })),
    }
  }
}

export interface Asset {
  scopeId: string
  id: string
  name: string
  type: string
  size: number
  url: string
  environmentKey: string
  pipelineKey: string
  remark: string
}

export function fromApiAsset(api: ApiFileAsset | FileAsset): Asset {
  if ('file_id' in api) {
    return {
      id: api.file_id,
      scopeId: api.scope_id,
      name: api.file_name,
      type: api.content_type,
      size: api.file_size,
      url: api.url,
      environmentKey: api.environment_key,
      pipelineKey: api.pipeline_key,
      remark: api.remark,
    }
  } else {
    return {
      id: api.fileId,
      scopeId: api.scopeId,
      name: api.fileName,
      type: api.contentType,
      size: api.fileSize,
      url: api.url,
      environmentKey: api.environmentKey,
      pipelineKey: api.pipelineKey,
      remark: api.remark,
    }
  }
}
