import { isSessionError } from '@/lib/api/http'
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { configApi, type ListConfigParams } from '@/lib/api/config'
import { fromApiConfig, toApiConfig } from '@/lib/api/transformers'
import type { ConfigItem, ConfigType } from '@/lib/types'
import { toast } from 'vue-sonner'

export const useConfigStore = defineStore('config', () => {
  const configs = ref<ConfigItem[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  let requestId = 0
  let currentScope = ''
  const scopeKey = (environmentKey: string, pipelineKey: string) => JSON.stringify([environmentKey, pipelineKey])

  const resetConfigs = () => {
    requestId += 1
    currentScope = ''
    configs.value = []
    loading.value = false
    error.value = null
  }

  const fetchConfigs = async (environmentKey: string, pipelineKey: string, type?: string) => {
    const batch = ++requestId
    const scope = scopeKey(environmentKey, pipelineKey)
    if (scope !== currentScope) configs.value = []
    currentScope = scope
    loading.value = true
    error.value = null
    try {
      const params: ListConfigParams = {
        environmentKey: environmentKey,
        pipelineKey: pipelineKey,
      }
      if (type) {
        params.type = type
      }
      const response = await configApi.list(params)
      if (batch !== requestId) return
      configs.value = response.list.map(fromApiConfig)
    } catch (e) {
      if (batch !== requestId) return
      error.value = e instanceof Error ? e.message : '获取配置列表失败'
      if (!isSessionError(e)) toast.error(error.value)
    } finally {
      if (batch === requestId) loading.value = false
    }
  }

  const refreshCurrentScope = async (environmentKey: string, pipelineKey: string) => {
    // A mutation started in A must not replace the list after the user moves to B.
    if (currentScope === scopeKey(environmentKey, pipelineKey)) await fetchConfigs(environmentKey, pipelineKey)
  }

  const fetchConfigDetail = async (
    environmentKey: string,
    pipelineKey: string,
    resourceKey: string
  ): Promise<ConfigItem | null> => {
    loading.value = true
    error.value = null
    try {
      const response = await configApi.detail(resourceKey, { environmentKey, pipelineKey })
      return response.config ? fromApiConfig(response.config) : null
    } catch (e) {
      error.value = e instanceof Error ? e.message : '获取配置详情失败'
      if (!isSessionError(e)) toast.error(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  const createConfig = async (
    environmentKey: string,
    pipelineKey: string,
    config: {
      name: string
      alias: string
      type: ConfigType
      content: string
      description: string
    }
  ) => {
    try {
      const apiConfig = toApiConfig({
        name: config.name,
        alias: config.alias,
        type: config.type,
        content: config.content,
        description: config.description,
        environmentId: environmentKey,
        pipelineId: pipelineKey,
      })
      await configApi.create(apiConfig as any)
      await refreshCurrentScope(environmentKey, pipelineKey)
      toast.success('配置创建成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '创建配置失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const updateConfig = async (
    environmentKey: string,
    pipelineKey: string,
    config: {
      id: string
      name: string
      alias: string
      type: ConfigType
      content: string
      description: string
    }
  ) => {
    try {
      const apiConfig = toApiConfig({
        id: config.id,
        name: config.name,
        alias: config.alias,
        type: config.type,
        content: config.content,
        description: config.description,
        environmentId: environmentKey,
        pipelineId: pipelineKey,
      })
      await configApi.update(apiConfig as any)
      await refreshCurrentScope(environmentKey, pipelineKey)
      toast.success('配置更新成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '更新配置失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const deleteConfig = async (environmentKey: string, pipelineKey: string, resourceKey: string) => {
    try {
      await configApi.delete(resourceKey, { environmentKey, pipelineKey })
      await refreshCurrentScope(environmentKey, pipelineKey)
      toast.success('配置删除成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '删除配置失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  return {
    configs,
    loading,
    error,
    fetchConfigs,
    resetConfigs,
    fetchConfigDetail,
    createConfig,
    updateConfig,
    deleteConfig,
  }
})
