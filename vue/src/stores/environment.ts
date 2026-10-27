import { isSessionError } from '@/lib/api/http'
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { environmentApi } from '@/lib/api/environment'
import { pipelineApi } from '@/lib/api/pipeline'
import { runtimeApi } from '@/lib/api/runtime'
import {
  fromApiEnvironment,
  fromApiPipeline,
  toApiEnvironment,
  toApiPipeline,
  fromApiEnvironmentOverview,
} from '@/lib/api/transformers'
import type { Environment, Pipeline } from '@/lib/types'
import type { ApiEnvironment } from '@/lib/api/types'
import { toast } from 'vue-sonner'
import { registerScopes, assertEnvironment, assertScope } from '@/lib/scope'

export const useEnvironmentStore = defineStore('environment', () => {
  const environments = ref<Environment[]>([])
  const pipelines = ref<Pipeline[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  let overviewBatch = 0
  const reset = () => {
    overviewBatch++
    environments.value = []
    pipelines.value = []
    loading.value = false
    error.value = null
  }

  const fetchOverview = async () => {
    const batch = ++overviewBatch
    loading.value = true
    error.value = null
    try {
      const response = await runtimeApi.overview()
      if (batch !== overviewBatch) return
      environments.value = response.list.map(fromApiEnvironmentOverview)
      registerScopes(environments.value)
    } catch (e) {
      if (batch !== overviewBatch) return
      error.value = e instanceof Error ? e.message : '获取环境列表失败'
      if (!isSessionError(e)) toast.error(error.value)
    } finally {
      if (batch === overviewBatch) loading.value = false
    }
  }

  const fetchEnvironments = async () => {
    loading.value = true
    error.value = null
    try {
      const response = await environmentApi.list()
      environments.value = response.list.map((env: any) => fromApiEnvironment(env, []))
    } catch (e) {
      error.value = e instanceof Error ? e.message : '获取环境列表失败'
      if (!isSessionError(e)) toast.error(error.value)
    } finally {
      loading.value = false
    }
  }

  const fetchPipelines = async (environmentKey: string) => {
    loading.value = true
    error.value = null
    try {
      const response = await pipelineApi.list({ environmentKey })
      pipelines.value = response.list.map((item: any) => fromApiPipeline(item))
    } catch (e) {
      error.value = e instanceof Error ? e.message : '获取渠道列表失败'
      if (!isSessionError(e)) toast.error(error.value)
    } finally {
      loading.value = false
    }
  }

  const createEnvironment = async (env: { key: string; name: string }) => {
    try {
      const apiEnv = toApiEnvironment(env)
      await environmentApi.create(apiEnv as any)
      await fetchOverview()
      toast.success('环境创建成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '创建环境失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const updateEnvironment = async (env: { key: string; name: string; environmentId: string }) => {
    try {
      const apiEnv = toApiEnvironment(env)
      await environmentApi.update(apiEnv as any)
      await fetchOverview()
      toast.success('环境更新成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '更新环境失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const deleteEnvironment = async (environmentKey: string, expectedEnvironmentId: string) => {
    try {
      assertEnvironment(environmentKey, expectedEnvironmentId)
      await environmentApi.delete(environmentKey, { expectedEnvironmentId })
      await fetchOverview()
      toast.success('环境删除成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '删除环境失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const createPipeline = async (environmentKey: string, pipeline: { key: string; name: string }, expectedEnvironmentId: string) => {
    try {
      const apiPipeline = toApiPipeline({ ...pipeline, environmentId: expectedEnvironmentId }, environmentKey)
      await pipelineApi.create(apiPipeline as any)
      await fetchOverview()
      toast.success('渠道创建成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '创建渠道失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const updatePipeline = async (environmentKey: string, pipeline: { key: string; name: string; scopeId: string }) => {
    try {
      const apiPipeline = toApiPipeline(pipeline, environmentKey)
      await pipelineApi.update(apiPipeline as any)
      await fetchOverview()
      toast.success('渠道更新成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '更新渠道失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  const deletePipeline = async (environmentKey: string, pipelineKey: string, expectedScopeId: string) => {
    try {
      assertScope(environmentKey, pipelineKey, expectedScopeId)
      await pipelineApi.delete(pipelineKey, { environmentKey, expectedScopeId })
      await fetchOverview()
      toast.success('渠道删除成功')
    } catch (e) {
      const message = e instanceof Error ? e.message : '删除渠道失败'
      if (!isSessionError(e)) toast.error(message)
      throw e
    }
  }

  return {
    reset,
    environments,
    pipelines,
    loading,
    error,
    fetchOverview,
    fetchEnvironments,
    fetchPipelines,
    createEnvironment,
    updateEnvironment,
    deleteEnvironment,
    createPipeline,
    updatePipeline,
    deletePipeline,
  }
})
