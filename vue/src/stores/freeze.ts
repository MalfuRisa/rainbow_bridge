import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { freezeApi, type FreezeStatusData, type FreezeTransitionPayload } from '@/lib/api/freeze'
import { scopeIdFor } from '@/lib/scope'

/** 冻结状态缓存:仅用于展示与按钮禁用;服务端每次写入都会自行复查。 */
export const useFreezeStore = defineStore('freeze', () => {
  const statuses = ref<Record<string, FreezeStatusData>>({})
  const loading = ref(false)

  const scopeKey = (environmentKey: string, pipelineKey: string) => scopeIdFor(environmentKey, pipelineKey)

  const statusFor = (environmentKey: string, pipelineKey: string): FreezeStatusData | null => {
    if (!environmentKey || !pipelineKey) return null
    return statuses.value[scopeKey(environmentKey, pipelineKey)] || null
  }

  const isLocked = (environmentKey: string, pipelineKey: string): boolean =>
    statusFor(environmentKey, pipelineKey)?.state === 'locked'

  const adminConfigured = computed(() =>
    Object.values(statuses.value).some((status) => status.admin_configured)
  )

  const loadStatus = async (environmentKey: string, pipelineKey: string): Promise<FreezeStatusData | null> => {
    if (!environmentKey || !pipelineKey) return null
    const scopeId = scopeKey(environmentKey, pipelineKey)
    loading.value = true
    try {
      const status = await freezeApi.status(environmentKey, pipelineKey)
      if (!scopeId || scopeId !== scopeKey(environmentKey, pipelineKey)) return null
      statuses.value = { ...statuses.value, [scopeId]: status }
      return status
    } catch {
      // 状态读取失败不阻塞页面;按钮禁用仅依赖已知状态,服务端仍会拦截
      return null
    } finally {
      loading.value = false
    }
  }

  const loadMany = async (scopes: { environmentKey: string; pipelineKey: string }[]) => {
    await Promise.all(
      scopes
        .filter((scope) => scope.environmentKey && scope.pipelineKey)
        .map((scope) => loadStatus(scope.environmentKey, scope.pipelineKey))
    )
  }

  const lock = async (payload: FreezeTransitionPayload) => {
    const status = await freezeApi.lock(payload)
    if (scopeIdFor(payload.environment_key, payload.pipeline_key) === payload.expected_scope_id) statuses.value = { ...statuses.value, [payload.expected_scope_id]: status }
    return status
  }

  const unlock = async (payload: FreezeTransitionPayload) => {
    const status = await freezeApi.unlock(payload)
    if (scopeIdFor(payload.environment_key, payload.pipeline_key) === payload.expected_scope_id) statuses.value = { ...statuses.value, [payload.expected_scope_id]: status }
    return status
  }

  const reset = () => {
    statuses.value = {}
    loading.value = false
  }

  return {
    statuses,
    loading,
    statusFor,
    isLocked,
    adminConfigured,
    loadStatus,
    loadMany,
    lock,
    unlock,
    reset,
  }
})
