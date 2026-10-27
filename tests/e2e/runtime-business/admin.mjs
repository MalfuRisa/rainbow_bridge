import { randomUUID } from 'node:crypto'
import { businessOk, errorType, request } from './http.mjs'

function query(params) {
  return '?' + new URLSearchParams(params).toString()
}

export class AdminFixture {
  constructor(client) {
    this.client = client
    this.scopes = new Map()
  }

  async environmentID(environmentKey) {
    const data = businessOk(await this.client.get('/api/v1/environment/list'), 'environment/list')
    const item = (data.list ?? []).find(row => row.environment_key === environmentKey)
    if (!item?.environment_id) throw new Error(`environment ${environmentKey} has no environment_id`)
    return item.environment_id
  }

  async createEnvironment(environmentKey, environmentName) {
    businessOk(await this.client.post('/api/v1/environment/create', {
      environment: { environment_key: environmentKey, environment_name: environmentName, is_active: true },
    }), `environment/create ${environmentKey}`)
    return this.environmentID(environmentKey)
  }

  async createPipeline(scope) {
    const environmentID = scope.environmentId ?? await this.environmentID(scope.environmentKey)
    const data = businessOk(await this.client.post('/api/v1/pipeline/create', {
      environment_key: scope.environmentKey,
      expected_environment_id: environmentID,
      pipeline: {
        pipeline_key: scope.pipelineKey,
        pipeline_name: scope.pipelineName ?? scope.pipelineKey,
        is_active: true,
      },
    }), `pipeline/create ${scope.environmentKey}/${scope.pipelineKey}`)
    const pipeline = data.pipeline
    if (!pipeline?.scope_id) throw new Error('pipeline/create returned no scope_id')
    const result = {
      ...scope,
      environmentId: environmentID,
      scopeId: pipeline.scope_id,
      environmentName: pipeline.environment_name ?? scope.environmentName,
      pipelineName: pipeline.pipeline_name ?? scope.pipelineKey,
    }
    this.scopes.set(`${scope.environmentKey}/${scope.pipelineKey}`, result)
    return result
  }

  async listPipelines(environmentKey) {
    return businessOk(await this.client.get(`/api/v1/pipeline/list?environment_key=${encodeURIComponent(environmentKey)}`), 'pipeline/list')
  }

  async createScope(environmentKey, pipelineKey, names = {}) {
    const environmentId = await this.createEnvironment(
      environmentKey, names.environmentName ?? `QA ${environmentKey}`)
    // Environment creation seeds its default pipeline; reuse it instead of
    // turning that documented convenience into a fixture failure.
    const existing = (await this.listPipelines(environmentKey)).list
      ?.find(item => item.pipeline_key === pipelineKey && item.is_active !== false)
    if (existing?.scope_id) {
      return {
        environmentKey,
        pipelineKey,
        environmentId,
        environmentName: names.environmentName ?? `QA ${environmentKey}`,
        pipelineName: existing.pipeline_name ?? pipelineKey,
        scopeId: existing.scope_id,
      }
    }
    return this.createPipeline({
      environmentKey,
      pipelineKey,
      environmentId,
      environmentName: names.environmentName ?? `QA ${environmentKey}`,
      pipelineName: names.pipelineName ?? `QA ${pipelineKey}`,
    })
  }

  async upload(scope, asset) {
    const response = await this.client.upload('/api/v1/asset/upload', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      remark: 'runtime business fixture',
    }, { field: 'file', fileName: asset.fileName, contentType: asset.contentType, bytes: asset.bytes })
    const data = businessOk(response, `asset/upload ${scope.environmentKey}/${scope.pipelineKey}/${asset.fileName}`)
    if (!data.asset?.file_id || !data.reference?.startsWith('asset://')) {
      throw new Error('asset upload did not return file_id/reference')
    }
    if (data.asset.file_size !== asset.bytes.length || data.asset.content_type !== asset.contentType) {
      throw new Error(`asset metadata mismatch: ${JSON.stringify(data.asset)}`)
    }
    return { fileId: data.asset.file_id, reference: data.reference, scopeId: data.asset.scope_id, raw: data.asset }
  }

  async createConfig(scope, input) {
    const data = businessOk(await this.client.post('/api/v1/config/create', {
      expected_scope_id: scope.scopeId,
      config: {
        environment_key: scope.environmentKey,
        pipeline_key: scope.pipelineKey,
        alias: input.alias,
        name: input.name,
        type: input.type,
        content: input.content,
        description: input.description ?? '',
        remark: input.remark ?? '',
        is_perm: input.is_perm ?? false,
      },
    }), `config/create ${scope.environmentKey}/${scope.pipelineKey}/${input.alias}`)
    return data.config
  }

  async configDetail(scope, resourceKey) {
    return businessOk(await this.client.get(`/api/v1/config/detail${query({
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      resource_key: resourceKey,
    })}`), `config/detail ${resourceKey}`).config
  }

  async updateConfig(scope, config, changes) {
    const current = await this.configDetail(scope, config.resource_key)
    const merged = {
      resource_key: current.resource_key,
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      alias: current.alias,
      name: changes.name ?? current.name,
      type: changes.type ?? current.type,
      content: changes.content ?? current.content,
      remark: changes.remark ?? current.remark,
      is_perm: changes.is_perm ?? current.is_perm,
      description: changes.description ?? current.description,
    }
    return businessOk(await this.client.post('/api/v1/config/update', {
      expected_scope_id: scope.scopeId,
      expected_edit_revision: current.edit_revision,
      config: merged,
    }), `config/update ${current.alias}`).config
  }

  async configList(scope) {
    return businessOk(await this.client.get(`/api/v1/config/list${query({
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
    })}`), 'config/list')
  }

  async freezeStatus(scope) {
    return businessOk(await this.client.get(`/api/v1/freeze/status${query({
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
    })}`), 'freeze/status')
  }

  async lock(scope, reason = 'runtime business lock') {
    const status = await this.freezeStatus(scope)
    if (status.state === 'locked') return status.revision
    const data = businessOk(await this.client.post('/api/v1/freeze/lock', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      expected_revision: status.revision,
      reason,
    }), 'freeze/lock')
    return data.revision
  }

  async unlock(scope, reason = 'runtime business unlock') {
    const status = await this.freezeStatus(scope)
    if (status.state === 'unlocked') return status.revision
    const data = businessOk(await this.client.post('/api/v1/freeze/unlock', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      expected_revision: status.revision,
      reason,
    }), 'freeze/unlock')
    return data.revision
  }

  async createRelease(scope, reason = 'runtime business create') {
    const revision = await this.lock(scope)
    const data = businessOk(await this.client.post('/api/v1/release/create', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      expected_freeze_revision: revision,
      request_id: randomUUID(),
      reason,
    }), 'release/create')
    if (!data.meta?.version || !data.meta?.snapshot_id) throw new Error('release/create returned no meta')
    return data.meta
  }

  async active(scope) {
    const response = await this.client.get(`/api/v1/release/active${query({
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
    })}`)
    businessOk(response, 'release/active')
    return response.json.data?.active ?? null
  }

  async activate(scope, version, reason = 'runtime business activate') {
    const active = await this.active(scope)
    const data = businessOk(await this.client.post('/api/v1/release/activate', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      version,
      expected_active_revision: active?.revision ?? '0',
      request_id: randomUUID(),
      reason,
    }), 'release/activate')
    return data
  }

  async deleteRelease(scope, version, snapshotID, reason = 'runtime business delete') {
    return businessOk(await this.client.post('/api/v1/release/delete', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      version,
      expected_snapshot_id: snapshotID,
      request_id: randomUUID(),
      reason,
    }), 'release/delete')
  }

  async compareActive(scope, targetVersion, targetSnapshot) {
    const active = await this.active(scope)
    return this.client.post('/api/v1/release/compare', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
      version: targetVersion,
      expected_snapshot_id: targetSnapshot,
      expected_active_revision: active?.revision ?? '0',
      expected_active_snapshot_id: active?.snapshot_id ?? '',
      limit: 20,
      offset: 0,
    })
  }

  async deletePipeline(scope) {
    return this.client.post('/api/v1/pipeline/delete', {
      environment_key: scope.environmentKey,
      pipeline_key: scope.pipelineKey,
      expected_scope_id: scope.scopeId,
    })
  }

  async deleteEnvironment(scope) {
    return this.client.post('/api/v1/environment/delete', {
      environment_key: scope.environmentKey,
      expected_environment_id: scope.environmentId,
    })
  }
}

export function expectReleaseOK(response, context) {
  if (response.status !== 200 || response.json?.code !== 200) {
    throw new Error(`${context}: HTTP=${response.status} type=${errorType(response)} ${response.text.slice(0, 400)}`)
  }
  return response.json.data
}

export async function anonymous(target, logicalPath, init = {}) {
  return request(target, logicalPath, init)
}