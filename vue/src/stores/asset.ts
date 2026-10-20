import { isSessionError } from '@/lib/api/http'
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { assetApi } from '@/lib/api/asset'
import { fromApiAsset } from '@/lib/api/transformers'
import type { Asset } from '@/lib/api/transformers'
import { toast } from 'vue-sonner'

export const useAssetStore = defineStore('asset', () => {
  const assets = ref<Asset[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  let requestId = 0
  let currentScope = ''
  const scopeKey = (environmentKey: string, pipelineKey: string) => JSON.stringify([environmentKey, pipelineKey])
  const resetAssets = () => {
    requestId += 1
    currentScope = ''
    assets.value = []
    error.value = null
    loading.value = false
  }

  const fetchAssets = async (environmentKey: string, pipelineKey: string) => {
    const batch = ++requestId
    const scope = scopeKey(environmentKey, pipelineKey)
    if (scope !== currentScope) assets.value = []
    currentScope = scope
    loading.value = true
    error.value = null
    try {
      const response = await assetApi.list({ environmentKey, pipelineKey })
      if (batch !== requestId) return
      assets.value = response.list.map(fromApiAsset)
    } catch (e) {
      if (batch !== requestId) return
      error.value = e instanceof Error ? e.message : '获取资源列表失败'
      if (!isSessionError(e)) toast.error(error.value)
    } finally {
      if (batch === requestId) loading.value = false
    }
  }

  const uploadAsset = async (
    file: File,
    environmentKey: string,
    pipelineKey: string
  ): Promise<Asset | null> => {
    try {
      const result = await assetApi.upload(file, environmentKey, pipelineKey)
      if (currentScope === scopeKey(environmentKey, pipelineKey)) await fetchAssets(environmentKey, pipelineKey)
      toast.success('资源上传成功')
      return result.asset ? fromApiAsset(result.asset) : null
    } catch (e) {
      const message = e instanceof Error ? e.message : '上传资源失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  return {
    assets,
    loading,
    error,
    fetchAssets,
    resetAssets,
    uploadAsset,
    getAssetUrl: assetApi.getFileUrl,
  }
})
