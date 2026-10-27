import { BaseApiService } from './base-api'
import { apiClient } from './client'
import { toCamelCase } from '../utils'
import type { FileAsset, UploadAssetResponse } from './types'
import { assertScope, readScope } from '@/lib/scope'

class AssetApiService extends BaseApiService<FileAsset> {
  protected baseUrl = '/api/v1/asset'

  protected getIdParamName(): string {
    return 'file_id'
  }

  // 重写list方法，因为需要环境和管道key参数
  async list(params: { environmentKey: string; pipelineKey: string; scopeId?: string }): Promise<{ total: number; list: FileAsset[] }> {
    return readScope(params.environmentKey, params.pipelineKey, scopeId => super.list({ ...params, scopeId }), params.scopeId)
  }

  // 上传文件
  async upload(
    file: File,
    environmentKey: string,
    pipelineKey: string,
    expectedScopeId: string,
    remark?: string
  ) {
    assertScope(environmentKey, pipelineKey, expectedScopeId)
    const formData = new FormData()
    formData.append('file', file)
    formData.append('environment_key', environmentKey)
    formData.append('pipeline_key', pipelineKey)
    formData.append('expected_scope_id', expectedScopeId)
    if (remark) {
      formData.append('remark', remark)
    }

    const resp = await apiClient.upload<UploadAssetResponse['data']>('/api/v1/asset/upload', formData)
    const asset = resp.data?.asset
    const camelCaseAsset = asset ? toCamelCase(asset) as unknown as FileAsset : null
    return {
      asset: camelCaseAsset,
      reference: resp.data?.reference || '',
    }
  }

  // 获取文件URL
  getFileUrl(fileId: string): string {
    return `/api/v1/asset/file/${fileId}`
  }
}

export const assetApi = new AssetApiService()

