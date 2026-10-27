import { onUnmounted, reactive, ref } from 'vue'
import { runtimeApi } from '@/lib/api/runtime'
import { fromApiEnvironmentOverview } from '@/lib/api/transformers'
import type { Environment } from '@/lib/types'
import { toast } from 'vue-sonner'
import { registerScopes } from '@/lib/scope'

/** Public home data must not be cleared by management-session invalidation. */
export function useRuntimeOverview() {
  const environments = ref<Environment[]>([])
  const loading = ref(false)
  let batchId = 0
  onUnmounted(() => { batchId++ })
  async function fetchOverview() {
    const batch = ++batchId
    loading.value = true
    try {
      const response = await runtimeApi.overview()
      if (batch !== batchId) return
      environments.value = response.list.map(fromApiEnvironmentOverview)
      registerScopes(environments.value)
    } catch (cause) {
      if (batch !== batchId) return
      toast.error(cause instanceof Error ? cause.message : '获取公开环境概览失败')
    } finally { if (batch === batchId) loading.value = false }
  }
  return reactive({ environments, loading, fetchOverview })
}
